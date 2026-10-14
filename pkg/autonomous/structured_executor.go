// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	// Register JPEG decoder for Screenshot() that returns
	// JPEG on some ADB versions.
	_ "image/jpeg"
	_ "image/png"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"digital.vasic.helixqa/pkg/analysis"
	"digital.vasic.helixqa/pkg/config"
	"digital.vasic.helixqa/pkg/llm"
	"digital.vasic.helixqa/pkg/navigator"
	"digital.vasic.helixqa/pkg/shellexec"
	"digital.vasic.helixqa/pkg/testbank"
)

// ActionExecutor interface for platform actions
type ActionExecutor interface {
	Screenshot(ctx context.Context) ([]byte, error)
}

// StructuredTestExecutor runs test cases from test banks in a
// systematic manner before curiosity-driven exploration.
type StructuredTestExecutor struct {
	config       PipelineConfig
	execFactory  ExecutorFactory
	vision       llm.Provider
	onFinding    func(analysis.AnalysisFinding)
	onScreenshot func(platform string, data []byte)
	// http is the executor for ActionTypeHTTP steps. Lazy-built
	// the first time an HTTP step appears, using
	// PipelineConfig.HTTPBaseURL (or env HELIXQA_HTTP_BASE_URL as
	// a fallback). Nil until needed.
	http *HTTPExecutor

	// playwright is the executor for ActionTypePlaywright steps.
	// Lazy-built the first time a Playwright step appears, using
	// PipelineConfig.PlaywrightCDPURL (or env
	// HELIXQA_PLAYWRIGHT_CDP_URL). Nil until configured AND
	// needed; absent config means Playwright steps SKIP rather
	// than execute.
	playwright *PlaywrightExecutor

	// settleDelay is the UI settle time before each screenshot.
	settleDelay time.Duration

	// reach caches, per platform, the result of the one reachability probe
	// (ExecutorProber) — nil entry = reachable.
	reachMu sync.Mutex
	reach   map[string]probeResult

	// caseHTTPSteps counts the http: requests sent in the CURRENT case;
	// assert: reads only a response this case produced.
	caseHTTPSteps int
}

// probeResult is one cached reachability answer.
type probeResult struct {
	err error
	at  time.Time
}

// failedProbeTTL bounds how long a FAILED probe is trusted: a device that
// came back is probed again after it. A successful probe stays cached
// until a device action fails (then it is re-probed uncached).
var failedProbeTTL = 15 * time.Second

// ExecutorProber is implemented by factories that can check that a
// platform's configured device / display / browser actually answers
// (DefaultExecutorFactory does). The structured executor probes each
// platform ONCE; when the probe fails, every step that needs the device is
// BLOCKED (environment), never run against nothing.
type ExecutorProber interface {
	Probe(ctx context.Context, platform string) error
}

// probeReachable returns the platform's cached reachability, probing when
// there is no answer yet or a failed answer is older than failedProbeTTL.
// The cache is per platform.
func (ste *StructuredTestExecutor) probeReachable(ctx context.Context, platform string) error {
	if _, ok := ste.execFactory.(ExecutorProber); !ok {
		return nil
	}
	ste.reachMu.Lock()
	pr, done := ste.reach[platform]
	ste.reachMu.Unlock()
	if done && (pr.err == nil || time.Since(pr.at) < failedProbeTTL) {
		return pr.err
	}
	return ste.reprobe(ctx, platform)
}

// reprobe runs the factory's probe now (uncached) and records the answer.
func (ste *StructuredTestExecutor) reprobe(ctx context.Context, platform string) error {
	prober, ok := ste.execFactory.(ExecutorProber)
	if !ok {
		return nil
	}
	err := prober.Probe(ctx, platform)
	ste.reachMu.Lock()
	defer ste.reachMu.Unlock()
	if ste.reach == nil {
		ste.reach = map[string]probeResult{}
	}
	ste.reach[platform] = probeResult{err: err, at: time.Now()}
	return err
}

// NewStructuredTestExecutor creates a new structured test executor.
func NewStructuredTestExecutor(
	config PipelineConfig,
	execFactory ExecutorFactory,
	vision llm.Provider,
	onFinding func(analysis.AnalysisFinding),
	onScreenshot func(platform string, data []byte),
) *StructuredTestExecutor {
	return &StructuredTestExecutor{
		config:       config,
		execFactory:  execFactory,
		vision:       vision,
		onFinding:    onFinding,
		onScreenshot: onScreenshot,
		settleDelay:  500 * time.Millisecond,
	}
}

// Execute runs all structured test cases from the test bank directory.
// It loads test banks and executes each test case's steps systematically.
func (ste *StructuredTestExecutor) Execute(
	ctx context.Context,
) (*StructuredExecutionResult, error) {
	result := &StructuredExecutionResult{
		TestCasesRun:    0,
		TestCasesPassed: 0,
		TestCasesFailed: 0,
		StepsExecuted:   0,
		Findings:        []analysis.AnalysisFinding{},
	}

	// Load test banks from BanksDir
	if ste.config.BanksDir == "" {
		fmt.Println("  [structured] No BanksDir configured, skipping structured tests")
		return result, nil
	}

	banksDir := ste.config.BanksDir
	banks, err := testbank.LoadDir(banksDir)
	if err != nil {
		// Try alternative path relative to project root
		altDir := filepath.Join(
			ste.config.ProjectRoot,
			"challenges", "helixqa-banks",
		)
		banks, err = testbank.LoadDir(altDir)
		if err != nil {
			fmt.Printf(
				"  [structured] Failed to load test banks: %v\n",
				err,
			)
			return result, err
		}
		banksDir = altDir
	}

	fmt.Printf(
		"  [structured] Loaded %d test banks from %s\n",
		len(banks), banksDir,
	)

	// FIX-QA-2026-04-21-019 preflight: on Android TV launchers the
	// home screen stacks channel rows from every app that published
	// TV channels. If any of those non-target apps is running when
	// structured tests start pressing DPAD, the first ENTER can hand
	// control to the foreign app. Proactively force-stop the apps
	// most commonly observed stealing focus before the first step.
	// The target app itself is never in this list.
	ste.preflightStopCompetingApps(ctx)

	ste.executeBanks(ctx, banks, result)
	return result, nil
}

// executeBanks runs the applicable test cases of all banks in dependency
// order (declared `dependencies:` first, ID as the tie-break — the same
// order `helixqa run` uses, testbank.DependencyOrder). Cases in, or behind,
// a dependency cycle are not run: each is BLOCKED with the reason.
func (ste *StructuredTestExecutor) executeBanks(
	ctx context.Context,
	banks []*testbank.BankFile,
	result *StructuredExecutionResult,
) {
	var cases []testbank.TestCase
	for _, bank := range banks {
		fmt.Printf(
			"  [structured] Loaded bank: %s (%d test cases)\n",
			bank.Name, len(bank.TestCases),
		)
		for _, tc := range bank.TestCases {
			if ste.testAppliesToPlatforms(tc) {
				cases = append(cases, tc)
			}
		}
	}
	// Unique keys (a duplicated ID keeps every case); dependencies name IDs
	// and resolve to the first case with that ID.
	keys := make([]string, len(cases))
	first := map[string]string{}
	byKey := map[string]testbank.TestCase{}
	for i, tc := range cases {
		k := tc.ID
		if _, dup := first[tc.ID]; dup {
			k = fmt.Sprintf("%s\x00%d", tc.ID, i)
		} else {
			first[tc.ID] = k
		}
		keys[i] = k
		byKey[k] = tc
	}
	deps := map[string][]string{}
	for i, tc := range cases {
		for _, d := range tc.Dependencies {
			if k, ok := first[d]; ok {
				deps[keys[i]] = append(deps[keys[i]], k)
			}
		}
	}
	order, cyclic := testbank.DependencyOrder(keys, deps)
	for _, k := range order {
		tc := byKey[k]
		if err := ste.executeTestCase(ctx, tc, result); err != nil {
			fmt.Printf("  [structured] Test case '%s' failed: %v\n", tc.ID, err)
		}
	}
	var cycleIDs []string
	for _, k := range cyclic {
		cycleIDs = append(cycleIDs, byKey[k].ID)
	}
	for _, k := range cyclic {
		ste.blockCaseUnrun(byKey[k], "not run: dependency cycle among "+strings.Join(cycleIDs, ", "), result)
	}
}

// blockCaseUnrun records a case that was not run at all as BLOCKED with an
// environment finding naming why.
func (ste *StructuredTestExecutor) blockCaseUnrun(tc testbank.TestCase, why string, result *StructuredExecutionResult) {
	result.TestCasesRun++
	result.TestCasesBlocked++
	fmt.Printf("  [structured] [%s] ⊘ BLOCKED — %s\n", tc.ID, why)
	result.Cases = append(result.Cases, StructuredCaseResult{ID: tc.ID, Name: tc.Name, Verdict: CaseVerdictBlocked})
	platform := ""
	if len(tc.Steps) > 0 {
		platform = ste.getPlatformForStep(tc.Steps[0])
	}
	finding := analysis.AnalysisFinding{
		Category:           analysis.CategoryEnvironment,
		Severity:           ste.priorityToSeverity(tc.Priority),
		Title:              fmt.Sprintf("Test Case BLOCKED (could not determine): %s", tc.Name),
		Description:        fmt.Sprintf("Case %s was not run — its outcome is UNKNOWN, not passed.\n%s", tc.ID, why),
		Platform:           platform,
		AcceptanceCriteria: "The bank's dependencies form no cycle, so every case can be ordered and run.",
	}
	result.Findings = append(result.Findings, finding)
	if ste.onFinding != nil {
		ste.onFinding(finding)
	}
}

// executeTestCase runs a single test case with all its steps.
func (ste *StructuredTestExecutor) executeTestCase(
	ctx context.Context,
	tc testbank.TestCase,
	result *StructuredExecutionResult,
) error {
	result.TestCasesRun++

	fmt.Printf(
		"  [structured] [%s] %s (priority: %s, steps: %d)\n",
		tc.ID, tc.Name, tc.Priority, len(tc.Steps),
	)

	// Article XI §11.5: env-dependent tests SKIP-OK when their
	// declared `requires_env` variables aren't set. Better to
	// honestly skip than fail-or-bluff-pass on unsupported hardware.
	for _, envVar := range tc.RequiresEnv {
		if os.Getenv(envVar) == "" {
			fmt.Printf(
				"  [structured] [%s] ⊘ SKIPPED — SKIP-OK: #%s — env %q not set (lab hardware unavailable)\n",
				tc.ID, envVar, envVar,
			)
			result.TestCasesSkipped++
			result.StepsExecuted += len(tc.Steps)
			return nil
		}
	}

	testPassed := true
	testBlocked := false
	blockedReason := ""
	blockedPlatform := ""
	executedSteps := 0
	skippedSteps := 0
	// observingSteps counts executed steps that OBSERVED app state (see
	// stepObserved). A case may PASS only when at least one ran.
	observingSteps := 0
	ste.caseHTTPSteps = 0
	var stepResults []TestStepResult

	for i, step := range tc.Steps {
		if step.Skip {
			// `_skip: true`: the author disabled this step — not this run's
			// action or assertion (the bank runner honours it the same way).
			fmt.Printf("    [step %d] %s — disabled (_skip)\n", i+1, step.Name)
			continue
		}
		var stepResult TestStepResult
		if cerr := ctx.Err(); cerr != nil {
			// The session ended (timeout / cancel) before this step: it
			// was never run — BLOCKED, not a verdict about the app.
			stepResult = TestStepResult{StepName: step.Name, Blocked: true, VisionGated: step.IsVisionGated(),
				Actual: fmt.Sprintf("BLOCKED (could not determine): the session context ended before this step ran: %v", cerr)}
		} else {
			stepResult = ste.executeStep(ctx, tc, step, i+1)
		}
		stepResults = append(stepResults, stepResult)
		result.StepsExecuted++

		if stepResult.Skipped {
			skippedSteps++
			continue
		}
		executedSteps++
		if stepObserved(step, stepResult) {
			observingSteps++
		}

		if stepResult.Blocked {
			// The step's outcome could not be determined. The case is
			// neither passed nor failed (no defect ticket is filed for
			// infrastructure the run could not use); its remaining
			// steps would build on an unknown state, so stop here.
			testBlocked = true
			blockedReason = fmt.Sprintf("step %d: %s", i+1, stepResult.Actual)
			blockedPlatform = ste.getPlatformForStep(step)
			break
		}

		if !stepResult.Passed {
			testPassed = false
			// Create finding for failed step
			finding := analysis.AnalysisFinding{
				Category: analysis.CategoryFunctional,
				Severity: ste.priorityToSeverity(tc.Priority),
				Title: fmt.Sprintf(
					"Test Case Failed: %s - Step %d",
					tc.Name, i+1,
				),
				Description: fmt.Sprintf(
					"Step: %s\nAction: %s\nExpected: %s\nActual: %s",
					step.Name, step.Action,
					step.Expected, stepResult.Actual,
				),
				Platform:           ste.getPlatformForStep(step),
				AcceptanceCriteria: fmt.Sprintf("Step '%s' executes successfully and produces the expected outcome: %s", step.Name, step.Expected),
			}
			result.Findings = append(result.Findings, finding)
			if ste.onFinding != nil {
				ste.onFinding(finding)
			}

			// Stop executing further steps on failure
			break
		}
	}

	// Nothing failed, nothing was blocked — but if no step OBSERVED app
	// state (only input / sleep / screenshot / prose steps, or screens no
	// model confirmed), nothing about the app was checked; and if a step
	// was skipped as not executable, the case was only partly run. Neither
	// is a pass.
	if !testBlocked && testPassed && !(executedSteps == 0 && skippedSteps > 0) {
		switch {
		case observingSteps == 0:
			testBlocked = true
			blockedReason = fmt.Sprintf("no step observed app state (%d executed: input / sleep / screenshot / "+
				"prose steps or screens no model confirmed; %d skipped) — nothing about the app was checked", executedSteps, skippedSteps)
		case skippedSteps > 0:
			testBlocked = true
			blockedReason = fmt.Sprintf("%d step(s) were skipped as not executable (prose / placeholder / no runtime) — "+
				"the case was only partly run", skippedSteps)
		}
		if testBlocked && len(tc.Steps) > 0 {
			blockedPlatform = ste.getPlatformForStep(tc.Steps[0])
		}
	}

	caseRec := StructuredCaseResult{ID: tc.ID, Name: tc.Name, Steps: stepResults}
	defer func() { result.Cases = append(result.Cases, caseRec) }()

	switch {
	case testBlocked && testPassed:
		result.TestCasesBlocked++
		caseRec.Verdict = CaseVerdictBlocked
		fmt.Printf(
			"  [structured] [%s] ⊘ BLOCKED — could not determine (%s)\n",
			tc.ID, blockedReason,
		)
		// File it: a case the run could not judge must be as visible as
		// one that failed — it is not a pass and must not vanish into a
		// count. The finding names the cause; it does not claim an app
		// defect.
		finding := analysis.AnalysisFinding{
			// Environment, not an app defect: kept out of bug
			// reproduction, ticketing and the issues-found count.
			Category: analysis.CategoryEnvironment,
			Severity: ste.priorityToSeverity(tc.Priority),
			Title: fmt.Sprintf(
				"Test Case BLOCKED (could not determine): %s", tc.Name,
			),
			Description: fmt.Sprintf(
				"Case %s could not be judged — its outcome is UNKNOWN, not passed.\n%s",
				tc.ID, blockedReason,
			),
			Platform: blockedPlatform,
			AcceptanceCriteria: "Every step obtains a determined verdict (device / display / " +
				"backend reachable, vision provider answering in the required format, " +
				"non-blank frames, goldens present) and at least one asserting step runs.",
		}
		result.Findings = append(result.Findings, finding)
		if ste.onFinding != nil {
			ste.onFinding(finding)
		}
	case executedSteps == 0 && skippedSteps > 0:
		caseRec.Verdict = CaseVerdictSkipped
		// Every step was a bank placeholder — nothing actually ran
		// against the app, so do not count as pass or fail.
		result.TestCasesSkipped++
		fmt.Printf(
			"  [structured] [%s] ⊘ SKIPPED (%d placeholder steps)\n",
			tc.ID, skippedSteps,
		)
	case testPassed:
		caseRec.Verdict = CaseVerdictPassed
		result.TestCasesPassed++
		if skippedSteps > 0 {
			fmt.Printf(
				"  [structured] [%s] ✓ PASSED (%d steps, %d placeholders skipped)\n",
				tc.ID, executedSteps, skippedSteps,
			)
		} else {
			fmt.Printf(
				"  [structured] [%s] ✓ PASSED (%d steps)\n",
				tc.ID, len(stepResults),
			)
		}
	default:
		caseRec.Verdict = CaseVerdictFailed
		result.TestCasesFailed++
		fmt.Printf(
			"  [structured] [%s] ✗ FAILED (%d/%d steps passed, %d skipped)\n",
			tc.ID, executedSteps-1, executedSteps, skippedSteps,
		)
	}

	return nil
}

// executeStep runs a single test step.
func (ste *StructuredTestExecutor) executeStep(
	ctx context.Context,
	tc testbank.TestCase,
	step testbank.TestStep,
	stepNum int,
) TestStepResult {
	result := TestStepResult{
		StepName: step.Name,
		Passed:   false,
	}

	fmt.Printf(
		"    [step %d] %s\n", stepNum, step.Name,
	)

	// Determine platform for this step
	platform := ste.getPlatformForStep(step)

	// Get executor for platform
	executor, err := ste.execFactory.Create(platform)
	unreachable := false
	if err == nil {
		if perr := ste.probeReachable(ctx, platform); perr != nil {
			executor, err, unreachable = nil, perr, true
		}
	}
	if err != nil && isHostOnlyStep(step) {
		// shell: / http: / assert: / sleep: run on the host and decide on
		// their own; a missing device must not hide their real verdict.
		executor = nil
	} else if err != nil {
		// No device / display / browser to act on: the step could not be
		// run at all. That is an environment problem, never an app
		// defect — BLOCKED (filed as an environment finding).
		result.Blocked = true
		result.VisionGated = step.IsVisionGated()
		what := "executor could not be created"
		if unreachable {
			what = "device / display is unreachable (reachability probe failed)"
		}
		result.Actual = fmt.Sprintf(
			"BLOCKED (could not determine): %s for %q: %v", what, platform, err,
		)
		return result
	}

	// Wait for UI to render before screenshot.
	// Cold start handled separately by the test step's own sleep action.
	ste.settle()

	// FIX-QA-2026-04-21-019: Foreground guard. Before every step,
	// verify the app under test is still in the foreground. If the
	// previous step navigated to an Android TV home-screen channel
	// tile that belongs to a different app (RuTube, IPTV, YouTube,
	// mitv-videoplayer), a raw DPAD_ENTER can launch that foreign
	// app — all subsequent keypresses then land in the wrong app
	// and the LLM dutifully reports "home screen visible" because
	// it truly is, just the wrong app's home. Emit a CRITICAL
	// finding, force-launch the target app, and continue.
	ste.ensureAppForeground(ctx, platform, tc, stepNum)

	// Take screenshot before action
	var beforeSS []byte
	var beforeErr error
	if executor != nil {
		beforeSS, beforeErr = executor.Screenshot(ctx)
	}
	if beforeErr != nil {
		beforeSS = nil
	}
	if len(beforeSS) > 0 && !IsBlankScreenshot(beforeSS) && ste.onScreenshot != nil {
		ste.onScreenshot(platform, beforeSS)
	}

	// Execute the action based on type
	// CRITICAL: Now actually executes ADB commands, sleep, keypress, etc!
	actionResult := ste.performAction(ctx, executor, step)
	if !actionResult.Success && !actionResult.Blocked && !actionResult.Skipped {
		if cerr := ctx.Err(); cerr != nil {
			// The session ended DURING the action: its failure says
			// nothing about the app.
			actionResult = ActionResult{Blocked: true, Message: fmt.Sprintf(
				"the session context ended during the action (%v): %s", cerr, actionResult.Message)}
		} else if executor != nil && !isHostOnlyStep(step) {
			// A device action failed. The real executors report only
			// "exit status 1", so the cause is decided by re-probing the
			// device / display (uncached), never by the error text.
			if perr := ste.reprobe(ctx, platform); perr != nil {
				actionResult = ActionResult{Blocked: true, Message: fmt.Sprintf(
					"device / display unreachable (re-probed after the action failed: %v): %s — environment, not an app defect",
					perr, actionResult.Message)}
			}
		}
	}

	// Placeholder/skipped actions short-circuit — no verification,
	// no failure, just propagate the Skipped flag so the test case
	// loop can account for them separately.
	if actionResult.Blocked {
		result.Blocked = true
		result.VisionGated = step.IsVisionGated()
		result.Actual = "BLOCKED (could not determine): " + actionResult.Message
		return result
	}
	if actionResult.Skipped {
		if step.IsVisionGated() {
			// A vision-gated step asserts what the screen shows; if its
			// action could not even run, that assertion was never made.
			// It is not a placeholder to be ignored — the case cannot be
			// judged, so the step is BLOCKED (never a silent skip that lets
			// the rest of the case PASS).
			result.Blocked = true
			result.VisionGated = true
			result.VisionStatus = VisionBlocked
			result.VisionReason = "vision-gated step was skipped: " + actionResult.Message
			result.Actual = "BLOCKED (could not determine): " + result.VisionReason
			return result
		}
		result.Skipped = true
		result.Actual = actionResult.Message
		return result
	}

	// FIX-QA-2026-04-21-019 part 2: post-action drift check. The
	// previous step may have been a DPAD_ENTER on a launcher tile
	// that handed control to a foreign app (RuTube, IPTV, YouTube,
	// …). Re-check foreground immediately; if drift to a foreign
	// app is detected, force-stop + relaunch before screenshotting
	// so the after-action screenshot reflects Catalogizer's UI (or
	// the launcher), not the foreign app's.
	ste.ensureAppForeground(ctx, platform, tc, stepNum)

	// Take screenshot after action.
	// 500ms is enough for UI to settle after a keypress/tap.
	ste.settle()
	var afterSS []byte
	var afterErr error
	if executor != nil {
		afterSS, afterErr = executor.Screenshot(ctx)
	}
	if afterErr != nil {
		afterSS = nil
	}
	if len(afterSS) > 0 && !IsBlankScreenshot(afterSS) && ste.onScreenshot != nil {
		ste.onScreenshot(platform, afterSS)
	}
	if kind, _ := step.ParseAction(); kind == testbank.ActionTypeScreenshot && !step.IsVisionGated() && executor != nil && len(afterSS) == 0 {
		// A screenshot step whose capture failed observed nothing (an
		// offline device / display): BLOCKED, never "Screenshot requested".
		result.Blocked = true
		result.Actual = fmt.Sprintf("BLOCKED (could not determine): screenshot capture failed: %v", afterErr)
		return result
	}

	// Judge the step. A vision-gated step passes only on positive,
	// frame-bound evidence; a step whose outcome cannot be determined is
	// BLOCKED, never passed on action success (see vision_gate.go).
	ste.judgeStep(ctx, step, actionResult, beforeSS, afterSS, &result)
	return result
}

// settle waits for the UI to render between an action and the
// screenshot that judges it. The constructor sets 500ms (sufficient for
// most UI transitions on Android TV); a zero value does not wait.
func (ste *StructuredTestExecutor) settle() {
	if ste.settleDelay > 0 {
		time.Sleep(ste.settleDelay)
	}
}

// performAction executes the action using the executor.
// CRITICAL: This now actually executes actions instead of just returning success!
func (ste *StructuredTestExecutor) performAction(
	ctx context.Context,
	executor navigator.ActionExecutor,
	step testbank.TestStep,
) ActionResult {
	actionType, actionValue := step.ParseAction()

	switch actionType {
	case testbank.ActionTypeADBShell:
		// Execute ADB shell command. Previous implementation only
		// ran a connection check (Screenshot) and reported success
		// without actually invoking the command, so every test that
		// relied on adb_shell: silently no-oped. Fix: route through
		// ADBExecutor.Shell so the device actually runs the command.
		fmt.Printf("      [action] adb shell: %s\n", actionValue)
		adbExec, ok := executor.(*navigator.ADBExecutor)
		if !ok {
			return ActionResult{Blocked: true, Message: "adb_shell requires an Android executor (environment / platform mismatch, not an app defect)"}
		}
		out, err := adbExec.Shell(ctx, actionValue)
		if err != nil {
			return ActionResult{Success: false, Message: fmt.Sprintf("adb shell error: %v (out: %s)", err, truncateOutput(out, 200))}
		}
		return ActionResult{Success: true, Message: fmt.Sprintf("Executed: %s", actionValue)}

	case testbank.ActionTypeShell:
		// Execute a host shell command via os/exec — the SAME real-execution
		// semantics the bank-driven runner uses
		// (orchestrator.executeDesktopShellSteps): run `sh -c <cmd>`, capture
		// the real exit code + combined output, score Success ONLY on exit 0.
		// Without this case the autonomous executor returned "Unknown action
		// type: shell" for every `shell:` step and filed FALSE-NEGATIVE
		// tickets even though the command passes under `helixqa run` — a §107
		// anti-bluff defect (a report claiming failure for a command it never
		// ran). The command itself encodes the assertion (e.g. piping stdout
		// through grep), so exit 0 == the asserted output was present.
		fmt.Printf("      [action] shell: %s\n", actionValue)
		shCtx := ctx
		if step.Timeout > 0 {
			var cancel context.CancelFunc
			shCtx, cancel = context.WithTimeout(
				ctx, time.Duration(step.Timeout)*time.Second,
			)
			defer cancel()
		}
		shCmd := shellexec.Command(shCtx, actionValue)
		shOut, shErr := shCmd.CombinedOutput()
		exitCode := 0
		if shCmd.ProcessState != nil {
			exitCode = shCmd.ProcessState.ExitCode()
		}
		if shCmd.ProcessState == nil {
			// Spawn failure: the command never ran, so it can satisfy no
			// expectation (expect_exit included).
			return ActionResult{Success: false, Message: fmt.Sprintf(
				"shell error: %v (out: %s)", shErr,
				truncateOutput(shOut, 200))}
		}
		// The verdict comes from the SAME function the bank-driven runner
		// uses (testbank.TestStep.CheckShellOutcome): no expect_exit means
		// exit 0, expect_exit N means exactly N, and expect_output_contains
		// is read from the combined stdout+stderr.
		if ok, why := step.CheckShellOutcome(exitCode, string(shOut)); !ok {
			return ActionResult{Success: false, Message: fmt.Sprintf(
				"%s: %s", why, truncateOutput(shOut, 200))}
		}
		return ActionResult{Success: true, Message: fmt.Sprintf(
			"shell exit=%d: %s", exitCode, truncateOutput(shOut, 200))}

	case testbank.ActionTypeSleep:
		// Sleep for specified milliseconds
		ms := 0
		fmt.Sscanf(actionValue, "%d", &ms)
		if ms <= 0 {
			ms = 2000 // Default 2 seconds
		}
		fmt.Printf("      [action] sleep: %dms\n", ms)
		select {
		case <-time.After(time.Duration(ms) * time.Millisecond):
		case <-ctx.Done():
			return ActionResult{Success: false, Message: fmt.Sprintf("sleep %dms cut short: %v", ms, ctx.Err())}
		}
		return ActionResult{Success: true, Message: fmt.Sprintf("Slept %dms", ms)}

	case testbank.ActionTypeScreenshot:
		// Screenshot will be taken after this
		fmt.Printf("      [action] screenshot\n")
		return ActionResult{Success: true, Message: "Screenshot requested"}

	case testbank.ActionTypeKeyPress:
		// Simulate key press
		fmt.Printf("      [action] keypress: %s\n", actionValue)
		if keyExecutor, ok := executor.(interface {
			KeyPress(context.Context, string) error
		}); ok {
			err := keyExecutor.KeyPress(ctx, actionValue)
			if err != nil {
				return ActionResult{Success: false, Message: fmt.Sprintf("Keypress failed: %v", err)}
			}
		}
		return ActionResult{Success: true, Message: fmt.Sprintf("Key pressed: %s", actionValue)}

	case testbank.ActionTypeText:
		// Enter text
		fmt.Printf("      [action] text: %s\n", actionValue)
		if typeExecutor, ok := executor.(interface {
			Type(context.Context, string) error
		}); ok {
			err := typeExecutor.Type(ctx, actionValue)
			if err != nil {
				return ActionResult{Success: false, Message: fmt.Sprintf("Type failed: %v", err)}
			}
		}
		return ActionResult{Success: true, Message: fmt.Sprintf("Typed: %s", actionValue)}

	case testbank.ActionTypeTap:
		// Tap at coordinates
		fmt.Printf("      [action] tap: %s\n", actionValue)
		var x, y int
		fmt.Sscanf(actionValue, "%d,%d", &x, &y)
		if tapExecutor, ok := executor.(interface {
			Click(context.Context, int, int) error
		}); ok {
			err := tapExecutor.Click(ctx, x, y)
			if err != nil {
				return ActionResult{Success: false, Message: fmt.Sprintf("Tap failed: %v", err)}
			}
		}
		return ActionResult{Success: true, Message: fmt.Sprintf("Tapped: %d,%d", x, y)}

	case testbank.ActionTypePlaybackCheck:
		// Query adb shell dumpsys media_session and assert that at
		// least one active session is in PlaybackState PLAYING. The
		// value format is "<package>" or "<package>:<minState>";
		// the default package is "*" (any app) and the default
		// minState is 3 (PLAYING).
		fmt.Printf("      [action] playback_check: %s\n", actionValue)
		adbExec, ok := executor.(*navigator.ADBExecutor)
		if !ok {
			return ActionResult{Blocked: true, Message: "playback_check requires an Android executor (environment / platform mismatch, not an app defect)"}
		}
		pkg, minState := parsePlaybackCheckArgs(actionValue)
		out, err := adbExec.Shell(ctx, "dumpsys media_session 2>/dev/null")
		if err != nil {
			return ActionResult{Success: false, Message: fmt.Sprintf("dumpsys failed: %v", err)}
		}
		ok2, reason := verifyPlaybackState(string(out), pkg, minState)
		if !ok2 {
			return ActionResult{Success: false, Message: fmt.Sprintf("playback not active: %s", reason)}
		}
		return ActionResult{Success: true, Message: fmt.Sprintf("playback verified: %s", reason)}

	case testbank.ActionTypeFrameDiff:
		// Capture two screenshots separated by waitMs milliseconds
		// (default 2000 ms) and assert that they differ. Used to
		// confirm that video playback is actually rendering frames
		// rather than sitting on a frozen first frame.
		fmt.Printf("      [action] frame_diff: %s\n", actionValue)
		waitMs := 2000
		if actionValue != "" {
			if n, err := parseIntField(actionValue); err == nil && n > 0 {
				waitMs = n
			}
		}
		first, err := executor.Screenshot(ctx)
		if err != nil || len(first) == 0 {
			return ActionResult{Blocked: true, Message: fmt.Sprintf("frame_diff: first screenshot failed: %v (capture problem, not an app defect)", err)}
		}
		time.Sleep(time.Duration(waitMs) * time.Millisecond)
		second, err := executor.Screenshot(ctx)
		if err != nil || len(second) == 0 {
			return ActionResult{Blocked: true, Message: fmt.Sprintf("frame_diff: second screenshot failed: %v (capture problem, not an app defect)", err)}
		}
		changed, diffPct, cmpErr := compareFrames(first, second)
		if cmpErr != nil {
			// Undecodable or differently sized frames say nothing about
			// motion — never report them as "motion detected".
			return ActionResult{Blocked: true, Message: fmt.Sprintf("frame_diff: %v — not comparable, cannot confirm motion (capture problem, not an app defect)", cmpErr)}
		}
		if isBlackFrame(first) || isBlackFrame(second) {
			// A blank / black capture (FLAG_SECURE or DRM surface, screen
			// off, compositor not rendering) shows nothing of the player:
			// identical black frames are not "frozen playback", and black
			// against a picture is not motion.
			return ActionResult{Blocked: true, Message: "frame_diff: a captured frame is black (secure or DRM surface, screen off, or capture not rendering) — motion cannot be judged (capture problem, not an app defect)"}
		}
		if !changed {
			return ActionResult{Success: false, Message: fmt.Sprintf("frames identical (diff %.1f%%) — playback appears frozen", diffPct)}
		}
		return ActionResult{Success: true, Message: fmt.Sprintf("frames differ by %.1f%% — motion detected", diffPct)}

	case testbank.ActionTypeHTTP:
		// HTTP request against the backend API. Action value is
		// "<METHOD> <PATH>"; the request body, headers, expected
		// status, and JSON-path / body-contains assertions are
		// declared on the TestStep itself. Lazy-builds the
		// per-session HTTPExecutor on first use.
		method, urlPath := parseHTTPAction(actionValue)
		fmt.Printf("      [action] http: %s %s\n", method, urlPath)
		if ste.http == nil {
			baseURL := ste.config.HTTPBaseURL
			if baseURL == "" {
				baseURL = os.Getenv("HELIXQA_HTTP_BASE_URL")
			}
			if baseURL == "" {
				// No backend configured: the request was never sent —
				// an environment gap, not an app defect.
				return ActionResult{Blocked: true, Message: "http: no base URL configured (HELIXQA_HTTP_BASE_URL / PipelineConfig.HTTPBaseURL) — the request was never sent (environment, not an app defect)"}
			}
			ste.http = NewHTTPExecutor(baseURL)
		}
		ste.caseHTTPSteps++
		return ste.http.Execute(ctx, method, urlPath, step)

	case testbank.ActionTypeAssert:
		// Structured assertion against the most recent HTTP
		// response captured by ActionTypeHTTP. Format:
		// "<kind>: <expr>" where kind is one of status_eq,
		// json_path_eq, body_contains, header_eq.
		if ste.http == nil || ste.caseHTTPSteps == 0 {
			// Nothing in THIS case produced a response to assert on (a
			// response left over from another case is not evidence here):
			// a bank defect, not an app failure.
			return ActionResult{Blocked: true, Message: "assert: no http: step ran earlier in this test case — nothing to assert on (bank defect, not an app defect)"}
		}
		fmt.Printf("      [action] assert: %s\n", actionValue)
		return runAssertion(ste.http, actionValue)

	case testbank.ActionTypePlaywright:
		// Web browser action via the Playwright CLI adapter
		// (challenges/pkg/userflow.PlaywrightCLIAdapter). Lazy-
		// builds the per-session executor on first use. When no
		// CDP URL is configured (PipelineConfig.PlaywrightCDPURL
		// nor HELIXQA_PLAYWRIGHT_CDP_URL env), the step SKIPs
		// with PLAYWRIGHT-RUNTIME-PENDING — Article XI §11.2.2
		// (no silent PASS when the real system is unreachable).
		fmt.Printf("      [action] playwright: %s\n", actionValue)
		if ste.playwright == nil {
			cdpURL := ste.config.PlaywrightCDPURL
			if cdpURL == "" {
				cdpURL = os.Getenv("HELIXQA_PLAYWRIGHT_CDP_URL")
			}
			if cdpURL == "" {
				return ActionResult{
					Skipped: true,
					Message: fmt.Sprintf("SKIP-OK: #PLAYWRIGHT-RUNTIME-PENDING — set PipelineConfig.PlaywrightCDPURL or HELIXQA_PLAYWRIGHT_CDP_URL to ws://host:port to run %q", actionValue),
				}
			}
			ste.playwright = NewPlaywrightExecutor(cdpURL)
		}
		return ste.playwright.Execute(ctx, actionValue)

	case testbank.ActionTypeDescription:
		// Legacy text-only action. If the author marked it as an
		// unfinished placeholder ("# CONVERT: Convert to executable ..."),
		// treat the step as SKIPPED rather than FAILED — the bank
		// entry is incomplete, not the app under test. This keeps
		// real failures visible and prevents 1000+ false-negative
		// findings from drowning out genuine issues.
		if strings.HasPrefix(strings.TrimSpace(actionValue), "# CONVERT: Convert to executable") {
			fmt.Printf("      [SKIP] Placeholder action (bank incomplete): %s\n", actionValue)
			return ActionResult{Skipped: true, Message: "Bank placeholder — convert to adb_shell:/sleep:/key:/text:/tap: action"}
		}
		// §107 parity alignment (parity-audit 2026-05-30): a plain prose
		// description is not executable — the bank-runner path SKIPs such a
		// case rather than FAILing it. Mirror that here so the SAME bank entry
		// gets the SAME verdict in both executors (was Success:false → a
		// false-negative finding that diverged from `helixqa run`'s honest
		// SKIP and drowned real issues in noise).
		fmt.Printf("      [SKIP] Text-only action (not executable): %s\n", actionValue)
		return ActionResult{Skipped: true, Message: "Text-only/description action - not executable (use adb_shell:/shell:/http:/sleep:/tap: etc.)"}

	case testbank.ActionTypeSwipe:
		// Swipe from (x1,y1) to (x2,y2). Mirrors the tap path with the
		// 4-coordinate gesture. Previously UNHANDLED → fell through to the
		// default "Unknown action type: swipe" FALSE-NEGATIVE for a
		// legitimate, schema-recognized action (parity-audit 2026-05-30).
		fmt.Printf("      [action] swipe: %s\n", actionValue)
		var sx1, sy1, sx2, sy2 int
		fmt.Sscanf(actionValue, "%d,%d,%d,%d", &sx1, &sy1, &sx2, &sy2)
		if swipeExecutor, ok := executor.(interface {
			Swipe(context.Context, int, int, int, int) error
		}); ok {
			if err := swipeExecutor.Swipe(ctx, sx1, sy1, sx2, sy2); err != nil {
				return ActionResult{Success: false, Message: fmt.Sprintf("Swipe failed: %v", err)}
			}
		}
		return ActionResult{Success: true, Message: fmt.Sprintf("Swiped: %d,%d->%d,%d", sx1, sy1, sx2, sy2)}

	default:
		return ActionResult{Success: false, Message: fmt.Sprintf("Unknown action type: %s", actionType)}
	}
}

// runAssertion evaluates a structured ActionTypeAssert expression
// against the HTTPExecutor's most recent response. Format:
// "status_eq: 200", "json_path_eq: $.foo = bar",
// "body_contains: hello", "header_eq: Content-Type = application/json".
func runAssertion(h *HTTPExecutor, value string) ActionResult {
	idx := strings.Index(value, ":")
	if idx <= 0 {
		return ActionResult{Success: false, Message: "assert: format is '<kind>: <expr>'"}
	}
	kind := strings.TrimSpace(value[:idx])
	expr := strings.TrimSpace(value[idx+1:])
	status, headers, body := h.LastResponse()

	switch kind {
	case "status_eq":
		var want int
		if _, err := fmt.Sscanf(expr, "%d", &want); err != nil {
			return ActionResult{Success: false, Message: fmt.Sprintf("assert status_eq: bad value %q", expr)}
		}
		if status != want {
			return ActionResult{Success: false, Message: fmt.Sprintf("assert status_eq: got %d, want %d", status, want)}
		}
		return ActionResult{Success: true, Message: fmt.Sprintf("status_eq: %d", status)}
	case "body_contains":
		if !strings.Contains(string(body), expr) {
			return ActionResult{Success: false, Message: fmt.Sprintf("assert body_contains: %q not in body", expr)}
		}
		return ActionResult{Success: true, Message: fmt.Sprintf("body_contains: %q", expr)}
	case "json_path_eq":
		// "$.path = expected"
		eq := strings.Index(expr, "=")
		if eq <= 0 {
			return ActionResult{Success: false, Message: "assert json_path_eq: format is '$.path = expected'"}
		}
		path := strings.TrimSpace(expr[:eq])
		want := strings.TrimSpace(expr[eq+1:])
		ok, val, err := jsonPathExists(body, path)
		if err != nil {
			return ActionResult{Success: false, Message: fmt.Sprintf("assert json_path_eq: %v", err)}
		}
		if !ok {
			return ActionResult{Success: false, Message: fmt.Sprintf("assert json_path_eq: path %q not found", path)}
		}
		got := fmt.Sprintf("%v", val)
		if got != want {
			return ActionResult{Success: false, Message: fmt.Sprintf("assert json_path_eq: %s = %q, want %q", path, got, want)}
		}
		return ActionResult{Success: true, Message: fmt.Sprintf("json_path_eq: %s = %q", path, got)}
	case "header_eq":
		// "Header-Name = expected"
		eq := strings.Index(expr, "=")
		if eq <= 0 {
			return ActionResult{Success: false, Message: "assert header_eq: format is 'Name = value'"}
		}
		name := strings.TrimSpace(expr[:eq])
		want := strings.TrimSpace(expr[eq+1:])
		got := headers.Get(name)
		if got != want {
			return ActionResult{Success: false, Message: fmt.Sprintf("assert header_eq: %s = %q, want %q", name, got, want)}
		}
		return ActionResult{Success: true, Message: fmt.Sprintf("header_eq: %s = %q", name, got)}
	default:
		return ActionResult{Success: false, Message: fmt.Sprintf("assert: unknown kind %q", kind)}
	}
}

// preflightStopCompetingApps force-stops known channel-publishing
// apps on Android TV so that a rogue DPAD_ENTER on a foreign
// channel tile does not silently hand control over and void the
// structured phase. The list is a generic set of Android TV apps
// observed in the wild to publish home channels; it is NOT the
// app under test. The target app (ste.config.AndroidPackage) is
// excluded from the stop list as a defensive measure even though
// it would never appear here, to keep this library
// project-agnostic (HelixQA Constitution §1).
//
// Failures to stop an app (not installed, already stopped, adb
// unavailable) are ignored — this is best-effort hygiene, not a
// test-fatal operation.
func (ste *StructuredTestExecutor) preflightStopCompetingApps(
	ctx context.Context,
) {
	device := ste.config.AndroidDevice
	if device == "" && len(ste.config.AndroidDevices) > 0 {
		device = ste.config.AndroidDevices[0]
	}
	if device == "" {
		return
	}

	// Consumer-owned list via config.CompetingAppPackages (HelixQA
	// Constitution §1 — no project-specific names baked into the
	// library). Callers populate this from their own env var; see
	// cmd/helixqa/main.go which reads HELIX_COMPETING_APP_PACKAGES
	// and passes it through. If empty, the preflight is a no-op
	// and the per-step foreground guard still catches drift.
	competitors := ste.config.CompetingAppPackages
	if len(competitors) == 0 {
		fmt.Printf(
			"  [structured] preflight: no competing-app packages configured " +
				"(HELIX_COMPETING_APP_PACKAGES) — per-step foreground guard " +
				"will still catch drift to foreign apps\n",
		)
		return
	}

	target := ste.config.AndroidPackage
	for _, pkg := range competitors {
		if pkg == "" || pkg == target {
			continue
		}
		_, _ = osexec.CommandContext(
			ctx,
			"adb", "-s", device,
			"shell", "am", "force-stop", pkg,
		).CombinedOutput()
	}
	fmt.Printf(
		"  [structured] preflight: force-stopped %d competing apps on %s\n",
		len(competitors), device,
	)
}

// isLauncherPackage returns true when the package string identifies
// one of the known Android TV launcher packages. Focus on a launcher
// is a legitimate intermediate state for tv-channel-* / tv-watch-next-*
// tests, which intentionally navigate to home to click a channel tile.
// Focus on any other foreign app is drift.
func isLauncherPackage(pkg string) bool {
	if pkg == "" {
		return false
	}
	switch pkg {
	case
		"com.mitv.tvhome.atv",
		"com.mitv.tvhome.michannel",
		"com.google.android.tvlauncher",
		"com.google.android.leanbacklauncher",
		"com.amazon.tv.launcher",
		"com.android.tv.launcher":
		return true
	}
	return false
}

// currentForegroundPackage extracts the foreground package name from
// `adb shell dumpsys window windows` output. Returns empty string on
// parse failure.
func currentForegroundPackage(dumpsysOut string) string {
	line := extractLine(dumpsysOut, "mCurrentFocus=")
	if line == "" {
		return ""
	}
	// Format: "mCurrentFocus=Window{... u0 <package>/<component>}"
	// Find the last '{...}' and split by space.
	i := strings.LastIndex(line, "{")
	if i < 0 {
		return ""
	}
	j := strings.LastIndex(line, "}")
	if j < 0 || j <= i {
		return ""
	}
	inner := line[i+1 : j]
	parts := strings.Fields(inner)
	if len(parts) == 0 {
		return ""
	}
	// Last field is "<package>/<component>".
	compStr := parts[len(parts)-1]
	slash := strings.Index(compStr, "/")
	if slash < 0 {
		return compStr
	}
	return compStr[:slash]
}

// ensureAppForeground guards against silent app-swap drift where
// a DPAD_ENTER on an Android TV launcher channel tile has handed
// control to a different app. The guard only applies to android /
// androidtv platforms where a target package is configured.
//
// On drift to a foreign app (non-launcher, non-target): emits a
// CRITICAL finding and force-relaunches the target app. Drift to a
// launcher is tolerated silently — tv-channel-* tests legitimately
// visit the launcher to click channel tiles.
//
// Test cases that INTENTIONALLY exercise system overlays (voice
// search → Google Katniss / `ihq` input method, launcher channel
// deep-links → RuTube / IPTV Pro by design) can opt out via
// `allow_foreground_leave: true` in the bank YAML — the guard then
// treats their drift events as expected behaviour rather than
// infrastructure failures.
//
// A foreground drift during a structured bank step that does NOT
// opt out is a Constitution-class failure — the affected test's
// remaining steps cannot be trusted, but the session continues so
// that subsequent test cases (which relaunch the app via their
// own adb_shell: am start seed steps) still run.
func (ste *StructuredTestExecutor) ensureAppForeground(
	ctx context.Context,
	platform string,
	tc testbank.TestCase,
	stepNum int,
) {
	if platform != "android" && platform != "androidtv" {
		return
	}
	// Per-test opt-out for tests that intentionally leave the app.
	if tc.AllowForegroundLeave {
		return
	}
	expectedPkg := ste.config.AndroidPackage
	if expectedPkg == "" {
		return
	}
	device := ste.config.AndroidDevice
	if device == "" && len(ste.config.AndroidDevices) > 0 {
		device = ste.config.AndroidDevices[0]
	}
	if device == "" {
		return
	}

	fgOut, _ := osexec.CommandContext(
		ctx,
		"adb", "-s", device,
		"shell", "dumpsys", "window", "windows",
	).CombinedOutput()
	fgStr := string(fgOut)
	if len(fgStr) == 0 {
		return
	}
	currentPkg := currentForegroundPackage(fgStr)
	if currentPkg == "" {
		return
	}
	if currentPkg == expectedPkg {
		return
	}
	// Launcher foreground is a legitimate intermediate state for
	// tv-channel-* / tv-watch-next-* tests that navigate to home to
	// select a channel tile. Only relaunch on true foreign-app drift.
	if isLauncherPackage(currentPkg) {
		return
	}

	// Foreground drift detected — extract current focus for finding.
	currentFocus := extractLine(fgStr, "mCurrentFocus=")
	if currentFocus == "" {
		currentFocus = "(unknown - dumpsys window windows did not report mCurrentFocus)"
	}

	fmt.Printf(
		"  [structured] [%s] ⚠ FOREGROUND DRIFT at step %d: "+
			"expected %s, found %q (package=%s) — force-stopping and relaunching\n",
		tc.ID, stepNum, expectedPkg, currentFocus, currentPkg,
	)

	// Force-stop the hijacker so subsequent DPAD events cannot land
	// back in it.
	_, _ = osexec.CommandContext(
		ctx,
		"adb", "-s", device,
		"shell", "am", "force-stop", currentPkg,
	).CombinedOutput()

	if ste.onFinding != nil {
		ste.onFinding(analysis.AnalysisFinding{
			Category: analysis.CategoryFunctional,
			Severity: analysis.SeverityCritical,
			Title: fmt.Sprintf(
				"Foreground Drift During Structured Test: %s (step %d)",
				tc.Name, stepNum,
			),
			Description: fmt.Sprintf(
				"Test case %q step %d detected that the target app "+
					"(%s) is no longer in the foreground. Current focus: %s. "+
					"This typically happens when a previous step's "+
					"DPAD_ENTER landed on a non-Catalogizer Android TV "+
					"home channel tile (RuTube, IPTV Pro, YouTube TV, "+
					"mitv-videoplayer) and launched that app. All "+
					"subsequent keypresses in the test landed in the "+
					"wrong app, producing false-positive PASS results. "+
					"The guard force-relaunched the target app; this "+
					"test's remaining steps were not trusted.",
				tc.ID, stepNum, expectedPkg, currentFocus,
			),
			Platform: platform,
			AcceptanceCriteria: fmt.Sprintf(
				"Every structured test step must execute against package %s. "+
					"Foreground drift into any other package is a CRITICAL "+
					"test-infrastructure failure and voids the step's result.",
				expectedPkg,
			),
		})
	}

	// IMPORTANT: do NOT append `--es qa_username/qa_password`
	// extras to the launch intent.
	//
	// HelixQA's "Fully Autonomous LLM-Driven QA" constitution
	// (helix_qa/CLAUDE.md) forbids scripted navigation flows /
	// hardcoded keystroke sequences that bypass the LLM. The
	// consuming-project side (Catalogizer's "Universal Solution
	// Principle" in CLAUDE.md) forbids QA-only receivers /
	// test-only Activity extras / app-side bypasses — the rule
	// is: fix detection in HelixQA, never in the app under test.
	//
	// Together those rules mean any `qa_username` extra HelixQA
	// emits is dead instrumentation: no app on the supported
	// platforms will read it, and emitting it lets a reviewer
	// believe a working bypass exists when it does not. That is
	// the exact bluff Article XI bans. The ADBExecutor.Type()
	// path now opens Compose-TV's IME via DPAD_CENTER before
	// typing (see pkg/navigator/executor.go), which is the real
	// fix for the form-fill failure mode that motivated the
	// extras in the first place.
	launchArgs := []string{
		"-s", device, "shell", "am", "start",
		"-n", expectedPkg + "/.ui.MainActivity",
	}
	_, _ = osexec.CommandContext(ctx, "adb", launchArgs...).CombinedOutput()
	time.Sleep(3 * time.Second)
}

// verifiedAnswerRe matches a standalone answer LINE: optional spaces,
// "VERIFIED:", yes|no, an optional full stop, nothing else.
var verifiedAnswerRe = regexp.MustCompile(`(?im)^[ \t]*VERIFIED:[ \t]*(yes|no)[ \t]*\.?[ \t]*\r?$`)

// actualLineRe matches the one optional "ACTUAL: <description>" line.
var actualLineRe = regexp.MustCompile(`^[ \t]*ACTUAL:`)

// verdictInTextRe finds a second verdict — the template's upper-case
// "VERIFIED: yes|no" — inside free text (a lower-case "Email verified: yes"
// badge is screen content, not a verdict).
var verdictInTextRe = regexp.MustCompile(`\bVERIFIED\s*:\s*(?i:yes|no)\b`)

// suspectActual scans the ACTUAL description of a VERIFIED reply and names
// what makes it unusable as support for the answer: a second verdict, or the
// model saying it could not see / read the image. "" when clean.
func suspectActual(actualLine string) string {
	text := actualLine
	if i := strings.Index(strings.ToUpper(text), "ACTUAL:"); i >= 0 {
		text = text[i+len("ACTUAL:"):]
	}
	switch {
	case verdictInTextRe.MatchString(text):
		return "carries a second verdict"
	case inabilityRe.MatchString(testbank.VisionTokenKey(text)):
		return "says the model could not see / read the image"
	}
	return ""
}

// inabilityRe finds, in a normalised ACTUAL text, the model saying it could
// not see / view / read the IMAGE itself ("I cannot see the image",
// "unable to view images", "could not read the screenshot"). It is
// deliberately narrow: an ordinary description ("The screenshot shows …",
// "I don't see any error dialogs") is not an inability. A heuristic — other
// hedges are documented, not detected.
var inabilityRe = regexp.MustCompile(`(^| )(cannot|can t|can not|could not|couldn t|unable to|not able to|am unable to|m unable to) ` +
	`(see|view|read|access|open|load|analy[sz]e|process|make out) ((the|this|that|any|your|an|a) )?(images?|screenshots?|pictures?|attachments?)( |$)`)

// strayReplyLine returns the first non-blank line of a VERIFIED reply that
// is neither a VERIFIED answer line nor the (single) ACTUAL: line — a
// refusal, a conditional, a retraction, a self-contradiction, a fence, an
// example — or "" when the reply has exactly the requested shape. A reply
// with a stray line is ambiguous: the answer line may be quoted, hedged or
// retracted by the text around it.
func strayReplyLine(response string) string {
	actuals := 0
	for _, ln := range strings.Split(response, "\n") {
		if strings.TrimSpace(ln) == "" || verifiedAnswerRe.MatchString(ln) {
			continue
		}
		if actualLineRe.MatchString(ln) {
			if actuals++; actuals == 1 {
				continue
			}
		}
		return strings.TrimSpace(ln)
	}
	return ""
}

// verifyOutcome uses LLM vision to verify if the screenshot matches expected state.
// Returns (verified, actualDescription, providerError). A non-nil error
// means the outcome could NOT be determined (no provider, provider
// failure, empty or ambiguous reply) — callers must never read it as a pass.
func (ste *StructuredTestExecutor) verifyOutcome(
	ctx context.Context,
	screenshot []byte,
	expected string,
) (bool, string, string, error) {
	cands := ste.visionCandidates()
	if len(cands) == 0 {
		return false, "No vision provider available - cannot verify outcome", "", errors.New("no vision provider")
	}
	// Ask candidates in order until one gives a DEFINITE answer. An error
	// or an ambiguous/empty reply moves on to the next provider; a definite
	// "VERIFIED: no" is final — the next provider is never asked (no
	// shopping for a yes).
	var tried []string
	for _, p := range cands {
		if ctx.Err() != nil {
			tried = append(tried, "context cancelled before asking "+p.Name())
			break
		}
		ok, actual, err := askVerified(ctx, p, screenshot, expected)
		if err == nil {
			return ok, actual, p.Name(), nil
		}
		tried = append(tried, p.Name()+": "+actual)
	}
	return false, strings.Join(tried, " | "), "", errors.New("no provider gave a definite VERIFIED answer")
}

// askVerified asks ONE provider whether the screenshot meets the
// expectation. Returns (verified, actualDescription, error); a non-nil
// error means this provider's outcome could not be determined.
func askVerified(
	ctx context.Context,
	vision llm.Provider,
	screenshot []byte,
	expected string,
) (bool, string, error) {

	prompt := fmt.Sprintf(
		"Analyze this screenshot and determine if the expected state is met.\n\n"+
			"Expected: %s\n\n"+
			"Respond with ONLY:\n"+
			"VERIFIED: yes/no\n"+
			"ACTUAL: brief description of what's visible",
		expected,
	)

	resp, err := vision.Vision(ctx, screenshot, prompt)
	if err != nil {
		return false, fmt.Sprintf("Vision analysis failed: %v", err), err
	}

	// Parse response
	response := ""
	if resp != nil {
		response = resp.Content
	}

	// FIX-QA-2026-04-21-013: the original implementation required
	// the exact literal "VERIFIED: yes" in the response. That's fine
	// for navigation-tuned providers that follow structured-output
	// prompts, but astica (first-ranked for the structured phase on
	// this project) returns rich natural-language descriptions and
	// never emits "VERIFIED: yes" literally. The old code then
	// treated every structured test as failing on vision verification
	// even when the action succeeded. Fix is tri-state:
	//
	//   1. Exactly one standalone answer LINE "VERIFIED: yes" or
	//      "VERIFIED: no" (nothing else on the line), and every other
	//      non-blank line is the ONE optional "ACTUAL: ..." line → honour it.
	//      An echoed template ("VERIFIED: yes/no"), "UNVERIFIED: yes", the
	//      phrase inside prose, or an answer line surrounded by any other
	//      text (refusal, conditional, retraction, fence, example) is not.
	//   2. Zero or several answer lines, or non-empty natural language →
	//      AMBIGUOUS; return an error. The caller (judgeStep) makes a
	//      vision-gated step BLOCKED and a non-gated step
	//      UNVERIFIED-by-vision — it never turns this into a pass.
	//   3. Response empty → error (same handling).
	actual := extractLine(response, "ACTUAL:")
	answers := verifiedAnswerRe.FindAllStringSubmatch(response, -1)
	saidYes, saidNo := false, false
	if len(answers) == 1 {
		saidYes = strings.EqualFold(answers[0][1], "yes")
		saidNo = !saidYes
	}
	switch {
	case len(answers) > 1:
		return false, fmt.Sprintf("Vision response carries %d VERIFIED answer lines — exactly one is required", len(answers)),
			errors.New("vision response ambiguous")
	case len(answers) == 1 && strayReplyLine(response) != "":
		stray := strayReplyLine(response)
		if len(stray) > 120 {
			stray = stray[:120] + "…"
		}
		return false, fmt.Sprintf("Vision response carries text besides the VERIFIED line and one ACTUAL: line (%q) — the answer may be quoted, hedged or retracted", stray),
			errors.New("vision response ambiguous")
	case saidYes && suspectActual(actual) != "":
		return false, "Vision response's ACTUAL line " + suspectActual(actual) + " — the answer may not rest on the frame",
			errors.New("vision response ambiguous")
	case saidYes:
		return true, actual, nil
	case saidNo:
		return false, actual, nil
	case strings.TrimSpace(response) == "":
		return false, "Vision response empty — cannot verify",
			errors.New("vision response empty")
	default:
		// Non-empty but not in the exact format. Treat as ambiguous
		// so the executor defers to actionResult.Success. Preserve
		// the raw response so the test log still shows what the
		// vision provider saw.
		snippet := response
		if len(snippet) > 200 {
			snippet = snippet[:200] + "…"
		}
		return false,
			"Vision response not in VERIFIED format: " + snippet,
			errors.New("vision response ambiguous")
	}
}

// testAppliesToPlatforms checks if a test case applies to configured platforms.
func (ste *StructuredTestExecutor) testAppliesToPlatforms(
	tc testbank.TestCase,
) bool {
	if len(tc.Platforms) == 0 {
		return true // Applies to all platforms
	}

	for _, tp := range tc.Platforms {
		for _, cp := range ste.config.Platforms {
			if string(tp) == cp ||
				tp == config.PlatformAll {
				return true
			}
		}
	}
	return false
}

// getPlatformForStep returns the platform for a step.
func (ste *StructuredTestExecutor) getPlatformForStep(
	step testbank.TestStep,
) string {
	if step.Platform != "" {
		return string(step.Platform)
	}
	// Default to first configured platform
	if len(ste.config.Platforms) > 0 {
		return ste.config.Platforms[0]
	}
	return "android"
}

// priorityToSeverity converts test priority to analysis severity.
func (ste *StructuredTestExecutor) priorityToSeverity(
	p testbank.Priority,
) analysis.FindingSeverity {
	switch p {
	case testbank.PriorityCritical:
		return analysis.SeverityCritical
	case testbank.PriorityHigh:
		return analysis.SeverityHigh
	case testbank.PriorityMedium:
		return analysis.SeverityMedium
	default:
		return analysis.SeverityLow
	}
}

// StructuredExecutionResult holds the results of structured test execution.
type StructuredExecutionResult struct {
	TestCasesRun     int `json:"test_cases_run"`
	TestCasesPassed  int `json:"test_cases_passed"`
	TestCasesFailed  int `json:"test_cases_failed"`
	TestCasesSkipped int `json:"test_cases_skipped"`
	// TestCasesBlocked counts cases that stopped on a step whose
	// outcome could not be determined (rc semantic 2) — never a pass.
	TestCasesBlocked int                        `json:"test_cases_blocked"`
	StepsExecuted    int                        `json:"steps_executed"`
	Findings         []analysis.AnalysisFinding `json:"findings"`
	// Cases is the per-case evidence record (verdict + step results,
	// frame hashes included).
	Cases []StructuredCaseResult `json:"cases"`
}

// TestStepResult represents the outcome of a single test step.
type TestStepResult struct {
	StepName string `json:"step_name"`
	Passed   bool   `json:"passed"`
	Skipped  bool   `json:"skipped"`
	// Blocked: the step's outcome could not be determined (a
	// vision-gated step without a usable verdict). Never Passed.
	Blocked bool   `json:"blocked"`
	Actual  string `json:"actual"`

	// VisionGated mirrors TestStep.IsVisionGated for the evidence record.
	VisionGated bool `json:"vision_gated"`
	// VisionStatus is what the vision layer established (see VisionStatus).
	VisionStatus VisionStatus `json:"vision_status"`
	// VisionReason explains VisionStatus in one line.
	VisionReason string `json:"vision_reason"`
	// VisionDescription is the expectation-blind description the model
	// gave (recognize/forbid evidence), when one was requested.
	VisionDescription string `json:"vision_description"`
	// VisionTexts is the verbatim on-screen text the model transcribed
	// (the only text recognize/forbid are matched against).
	VisionTexts []string `json:"vision_texts,omitempty"`
	// VisionProvider names the provider whose definite answer (or
	// transcription) the verdict rests on.
	VisionProvider string `json:"vision_provider"`
	// FrameSHA256 / FramePHash identify the exact after-action frame
	// bytes every verdict on this step refers to (pHash: 16 hex digits).
	FrameSHA256 string `json:"frame_sha256"`
	FramePHash  string `json:"frame_phash"`
	// BeforeFrameSHA256 identifies the before-action frame.
	BeforeFrameSHA256 string `json:"before_frame_sha256"`
	// GoldenDistance is the pHash Hamming distance to the step's golden,
	// or -1 when no golden was compared.
	GoldenDistance int `json:"golden_distance"`
}

// ActionResult represents the outcome of performing an action.
type ActionResult struct {
	Success bool
	Skipped bool
	// Blocked: the action could not be judged for an environment reason
	// (e.g. frames that cannot be compared) — never a pass, never an app
	// defect.
	Blocked bool
	Message string
}

// Helper functions
func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// extractLine scans text line-by-line and returns the first line whose
// trimmed form contains (or starts with) prefix. Returns the trimmed
// line. Returns "" if no match. Used to pluck mCurrentFocus= out of a
// multi-kilobyte dumpsys dump without confusing the foreground-drift
// guard (FIX-QA-2026-04-21-019 part 3 — previously a stub that returned
// the entire text, which made currentForegroundPackage see the whole
// dumpsys as one giant line and classify InputMethod windows as drift).
func extractLine(text, prefix string) string {
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, prefix) || strings.Contains(line, prefix) {
			return line
		}
	}
	return ""
}

// truncateOutput clips command output to n runes so error messages
// stay readable when a failing adb shell call produces a wall of
// logcat or dumpsys text.
func truncateOutput(b []byte, n int) string {
	s := string(b)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// parseIntField parses a trimmed decimal integer. Returns error on
// an empty or malformed input so callers can fall back to defaults.
func parseIntField(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, err
	}
	return n, nil
}

// parsePlaybackCheckArgs splits a playback_check value into a
// package filter and a minimum PlaybackState integer. Accepts:
//   - ""                  -> ("*", 3)
//   - "com.example"       -> ("com.example", 3)
//   - "com.example:2"     -> ("com.example", 2)
//   - "*"                 -> ("*", 3)
func parsePlaybackCheckArgs(s string) (string, int) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "*", 3
	}
	pkg := s
	minState := 3
	if idx := strings.Index(s, ":"); idx > 0 {
		pkg = strings.TrimSpace(s[:idx])
		if n, err := parseIntField(s[idx+1:]); err == nil && n >= 0 {
			minState = n
		}
	}
	if pkg == "" {
		pkg = "*"
	}
	return pkg, minState
}

// verifyPlaybackState scans the full output of
// `dumpsys media_session` looking for a session record whose
// package matches (or any package if pkg == "*") AND whose
// PlaybackState integer is >= minState. Returns (true, reason)
// when a matching session is found and (false, reason) otherwise.
//
// The relevant dumpsys lines look like:
//
//	package=<app package under test>
//	state=PlaybackState {state=3, position=12345, ...
//
// PlaybackState integers: 0=NONE, 1=STOPPED, 2=PAUSED, 3=PLAYING,
// 4=FAST_FORWARDING, 5=REWINDING, 6=BUFFERING, 7=ERROR, 8=CONNECTING,
// 9=SKIPPING_TO_PREVIOUS, 10=SKIPPING_TO_NEXT, 11=SKIPPING_TO_QUEUE_ITEM.
// We treat state >= minState (default 3) as "playing or better",
// which matches the intent of "is the app actually playing something".
func verifyPlaybackState(dump, pkg string, minState int) (bool, string) {
	lines := strings.Split(dump, "\n")
	var currentPkg string
	var bestState = -1
	var bestPkg string
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "package=") {
			currentPkg = strings.TrimPrefix(line, "package=")
			continue
		}
		if !strings.Contains(line, "PlaybackState {state=") {
			continue
		}
		// Extract the integer after "state="
		idx := strings.Index(line, "state=")
		if idx < 0 {
			continue
		}
		rest := line[idx+len("state="):]
		n := 0
		for _, r := range rest {
			if r < '0' || r > '9' {
				break
			}
			n = n*10 + int(r-'0')
		}
		if pkg != "*" && currentPkg != pkg {
			continue
		}
		if n > bestState {
			bestState = n
			bestPkg = currentPkg
		}
	}
	if bestState < 0 {
		if pkg == "*" {
			return false, "no media session found in dumpsys media_session"
		}
		return false, fmt.Sprintf("no media session found for package %q", pkg)
	}
	if bestState < minState {
		return false, fmt.Sprintf("%s state=%d (want >=%d)", bestPkg, bestState, minState)
	}
	return true, fmt.Sprintf("%s state=%d", bestPkg, bestState)
}

// framesDiffer compares two PNG screenshots and returns whether
// they visually differ by more than ~1 % of sampled pixels. Uses
// the same 9x9 grid as IsBlankScreenshot but aggregates across
// 17x17 = 289 points for a tighter signal. Suitable for confirming
// that a video player is actually rendering new frames.
func framesDiffer(aBytes, bBytes []byte) (bool, float64) {
	a, _, errA := image.Decode(bytes.NewReader(aBytes))
	b, _, errB := image.Decode(bytes.NewReader(bBytes))
	if errA != nil || errB != nil {
		// Not comparable. Callers that judge a step use compareFrames,
		// which refuses this case before calling here; this fallback
		// value is never a verdict.
		return true, 100.0
	}
	ba := a.Bounds()
	bb := b.Bounds()
	w := ba.Dx()
	h := ba.Dy()
	if w != bb.Dx() || h != bb.Dy() {
		// Not comparable either (see compareFrames); never a verdict.
		return true, 100.0
	}
	const gridN = 17
	var diffCount, total int
	for iy := 1; iy <= gridN; iy++ {
		for ix := 1; ix <= gridN; ix++ {
			x := ba.Min.X + w*ix/(gridN+1)
			y := ba.Min.Y + h*iy/(gridN+1)
			ar, ag, ab, _ := a.At(x, y).RGBA()
			br, bg, bb2, _ := b.At(x+(bb.Min.X-ba.Min.X), y+(bb.Min.Y-ba.Min.Y)).RGBA()
			ar, ag, ab = ar>>8, ag>>8, ab>>8
			br, bg, bb2 = br>>8, bg>>8, bb2>>8
			d := absU32(ar, br) + absU32(ag, bg) + absU32(ab, bb2)
			if d > 30 {
				diffCount++
			}
			total++
		}
	}
	if total == 0 {
		return false, 0
	}
	pct := float64(diffCount) * 100.0 / float64(total)
	// >=1 % of sample points differ by >30 per-channel = real motion
	return pct >= 1.0, pct
}

func absU32(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}

// stepObserved reports whether an executed step OBSERVED app state: a
// vision-confirmed screen (VisionVerified), an http: response checked
// against expectations, an assert: on a response, a shell: exit code /
// output, playback_check (media session state), frame_diff (frames
// compared) or a playwright assertvisible / waitfor. Input steps (tap,
// keypress, text, swipe, adb_shell, other playwright verbs) only report
// that the input was ACCEPTED, and sleep / screenshot / prose observe
// nothing unless a model confirmed the screen.
func stepObserved(step testbank.TestStep, r TestStepResult) bool {
	if r.VisionStatus == VisionVerified {
		return true
	}
	if step.IsVisionGated() {
		return false
	}
	switch kind, value := step.ParseAction(); kind {
	case testbank.ActionTypeShell, testbank.ActionTypeHTTP, testbank.ActionTypeAssert,
		testbank.ActionTypePlaybackCheck, testbank.ActionTypeFrameDiff:
		return true
	case testbank.ActionTypePlaywright:
		verb, _ := splitVerb(value)
		return verb == "assertvisible" || verb == "waitfor"
	}
	return false
}

// isBlackFrame reports a decodable frame whose sampled pixels are all
// near-black (every channel <= 16): what a FLAG_SECURE / DRM surface or a
// switched-off screen captures. A uniform grey slate or a dark
// low-contrast scene is NOT black and is judged.
func isBlackFrame(b []byte) bool {
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return false
	}
	bd := img.Bounds()
	const gridN = 17
	for iy := 1; iy <= gridN; iy++ {
		for ix := 1; ix <= gridN; ix++ {
			r, g, bl, _ := img.At(bd.Min.X+bd.Dx()*ix/(gridN+1), bd.Min.Y+bd.Dy()*iy/(gridN+1)).RGBA()
			if r>>8 > 16 || g>>8 > 16 || bl>>8 > 16 {
				return false
			}
		}
	}
	return true
}

// isHostOnlyStep reports a non-gated step whose action runs on the host
// (shell:, http:, assert:, sleep:) and needs no device executor. A
// vision-gated step always needs one (it is judged on a frame).
func isHostOnlyStep(step testbank.TestStep) bool {
	if step.IsVisionGated() {
		return false
	}
	kind, _ := step.ParseAction()
	return kind.IsNonScreen() || kind == testbank.ActionTypeSleep
}
