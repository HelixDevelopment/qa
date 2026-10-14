// Package orchestrator — definition_challenge.go.
//
// definitionChallenge bridges bank-loaded *challenge.Definition records
// into the runnable challenge.Challenge interface so the orchestrator
// can dispatch them through challenges/pkg/runner. Before this
// adapter existed, helixqa loaded YAML definitions into the bank
// but never registered them with registry.Default — so the
// orchestrator's per-definition dispatch loop in runPlatform always
// short-circuited (o.runner == nil OR registry.Get returned not-found),
// producing the "PASS-bluff detected: 0 challenges actually executed"
// honest-fail in cmd/helixqa/main.go.
//
// HXC-011 (close-out — desktop-platform real execution):
// definitionChallenge.Execute USED to unconditionally return
// Status=Skipped — it never shelled out to a bank case's `action:`
// command on any platform. For the `desktop` platform that is a
// §11.4 / CONST-035 PASS-bluff IN THE QA RUNNER ITSELF: a green
// (or honest-looking SKIP) line with no runtime evidence. The runner
// loaded the cases but never ran them.
//
// CONST-035 / §11.4.69 posture after HXC-011:
//   - A definitionChallenge that carries one or more executable
//     desktop-platform steps (`action: "shell: <cmd>"`) RUNS them
//     via os/exec, captures the real exit code + combined output,
//     and scores PASS only when every step exits 0. A non-zero exit
//     scores FAIL — the runner can no longer bluff a PASS.
//   - A definitionChallenge with NO step the desktop platform can
//     execute (prose-only steps, or steps that need an Android / UI
//     topology) returns an honest Status=Skipped with an explicit
//     reason — never a PASS. SKIP is for environment limitations and
//     MUST always carry an explicit reason (§11.4.3).
//
// CONST-051(B): the challenges submodule stays decoupled — this fix
// lives entirely in helix_qa. The wrapper carries the executable
// TestCase (loaded by pkg/testbank, which HelixQA owns) so the
// generic challenges/pkg/bank loader needs no project-specific
// `steps` field.
package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"digital.vasic.challenges/pkg/challenge"
	"digital.vasic.challenges/pkg/registry"

	"digital.vasic.helixqa/pkg/config"
	"digital.vasic.helixqa/pkg/shellexec"
	"digital.vasic.helixqa/pkg/testbank"
	"digital.vasic.helixqa/pkg/validator"
)

// definitionWrapperSkipSentinel is the prefix written into
// RecordedActions by definitionChallenge.Execute so the orchestrator
// can detect, after the runner has merged the result, that the
// original wrapper intended a Skipped status. See
// restoreSkippedFromDefinitionWrapper for the round-82 anti-bluff
// rationale.
const definitionWrapperSkipSentinel = "skip-reason:"

// restoreSkippedFromDefinitionWrapper restores Status=Skipped on a
// challenge.Result produced by the definitionChallenge wrapper after
// the challenges-runner's executeChallenge merge logic has overridden
// the wrapper's intent. See round-82 commit body for the full
// causal chain — the short version: the runner only preserves
// Failed/TimedOut/Error from the inner Execute call; Skipped with a
// passing assertion gets silently promoted to Passed, producing the
// canonical CONST-035 / Article XI §11.9 PASS-bluff (declarative-only
// definitions with no real backend dispatch report success to the
// end user).
//
// The signal that triggers restoration is the
// definitionWrapperSkipSentinel string at the start of any
// RecordedAction entry — only the wrapper writes that exact prefix.
// Non-wrapper results pass through untouched.
//
// Anti-bluff posture: this restores the HONEST skip, it does not
// hide a real PASS. The wrapper itself is what produced the
// sentinel; a definitionChallenge that REALLY executed desktop
// shell actions does NOT emit the sentinel, so the runner's Passed
// (or Failed) status stands.
func restoreSkippedFromDefinitionWrapper(r *challenge.Result) {
	if r == nil {
		return
	}
	if r.Status != challenge.StatusPassed {
		return
	}
	for _, action := range r.RecordedActions {
		if strings.HasPrefix(action, definitionWrapperSkipSentinel) {
			r.Status = challenge.StatusSkipped
			return
		}
	}
}

// testCasePlatformMatches reports whether tc targets the given run
// platform. A nil case, an empty Platforms list, or a list containing
// config.PlatformAll matches every platform. This is the standalone
// twin of (*definitionChallenge).platformMatches for the orchestrator
// aggregation call site, where the wrapper itself is no longer in
// scope (only the TestCase + platform are).
func testCasePlatformMatches(tc *testbank.TestCase, platform config.Platform) bool {
	if tc == nil || len(tc.Platforms) == 0 {
		return true
	}
	for _, p := range tc.Platforms {
		if p == config.PlatformAll || p == platform {
			return true
		}
	}
	return false
}

// promoteSkippedToPassed upgrades a SKIPPED challenge result to PASSED
// when (and only when) all of the following hold:
//
//   - the challenge result is non-nil and currently StatusSkipped;
//   - the wrapper skipped because it had no directly-executable
//     `shell:` step (the definitionWrapperSkipSentinel is present) —
//     i.e. the steps ran through the step-validation / LLM-bridge path
//     instead of the wrapper's os/exec path;
//   - the bank case targets the platform being run
//     (testCasePlatformMatches) — a case pinned to a non-matching
//     platform stays SKIPPED and is never promoted;
//   - the corresponding step-validation result PASSED.
//
// This closes the §107 reporting bluff where every challenge reported
// SKIPPED (and the summary read "0/N passed") even though every step
// in the "### Step Validation" table PASSED. A challenge whose steps
// all passed on a matching platform IS a pass, and the aggregation
// must reflect that.
//
// Anti-bluff posture: promotion requires a genuinely-passing step
// result. A SKIPPED challenge with no step result, a FAILED/ERROR step
// result, or a platform mismatch is left SKIPPED untouched — the
// function never invents a pass. As a hard guard, the desktop platform
// is NEVER promoted: desktop has no persistent app, so the step
// validator only proves crash-absence, which is not evidence a CLI
// command ran correctly — desktop cases must earn PASSED via real
// `shell:` execution. Promotion therefore serves only UI/app platforms
// (android/androidtv/web) where post-action crash-absence is the
// intended validation signal.
func promoteSkippedToPassed(
	cr *challenge.Result,
	sr *validator.StepResult,
	tc *testbank.TestCase,
	platform config.Platform,
) {
	if cr == nil || cr.Status != challenge.StatusSkipped {
		return
	}
	// Only promote wrapper-emitted honest skips, never some other
	// component's deliberate skip.
	if !hasSkipSentinel(cr) {
		return
	}
	// §107 anti-bluff hard guard: the desktop platform has NO persistent
	// application, so the step validator's signal is pure crash-absence
	// (det.CheckApp finds nothing running → StepPassed). Promoting a desktop
	// SKIP to PASSED on that vacuous signal would manufacture a PASS that no
	// command ever earned — exactly the metadata-only/absence-of-error
	// PASS-bluff the covenant forbids. Desktop cases MUST earn PASSED through
	// real `shell:` execution (executeDesktopShellSteps captures the real exit
	// code + output); a desktop case with only prose steps stays honestly
	// SKIPPED with the "convert prose steps to `action: \"shell: <cmd>\"`"
	// reason. Never promote on desktop.
	if platform == config.PlatformDesktop {
		return
	}
	if !testCasePlatformMatches(tc, platform) {
		return
	}
	// §107 anti-bluff wider guard (parity-audit 2026-05-30): the bank-runner
	// path executes ONLY `shell:` steps (executableShellSteps filters to
	// ActionTypeShell). The step validator that gates this promotion only
	// proves crash-absence — it NEVER runs a case's `http:` / `assert:` /
	// `tap:` / `adb_shell:` / … asserting steps. So promoting a SKIP whose
	// case carries any such asserting step would manufacture a PASS for
	// assertions that never executed — the same bluff the desktop guard
	// closes, but on every UI platform. Only cases whose steps are PURELY
	// observational (description / screenshot / sleep — where post-action
	// crash-absence IS the intended signal) may be promoted; a case with a
	// real asserting step stays honestly SKIPPED.
	if caseHasUnrunAssertingStep(tc, platform) {
		return
	}
	if sr == nil || sr.Status != validator.StepPassed {
		return
	}
	cr.Status = challenge.StatusPassed
}

// caseHasUnrunAssertingStep reports whether the test case carries, for the
// given platform, any step whose assertion the bank-runner path does NOT
// execute (see unrunAssertingSteps). If true, crash-absence is not evidence
// the case passed and it must not be promoted.
func caseHasUnrunAssertingStep(tc *testbank.TestCase, platform config.Platform) bool {
	return len(unrunAssertingSteps(tc, platform)) > 0
}

// unrunAssertingSteps names every step of the case that asserts something
// the bank runner never executes on this platform: a vision-gated step of
// any action kind (vision_verify / recognize / forbid / golden /
// expect_change), or a non-observational action other than `shell:` (http,
// assert, tap, swipe, keypress, text, adb_shell, playback_check,
// frame_diff, playwright). Observational steps (description / screenshot /
// sleep without vision keys) and `shell:` steps (run by the bank runner)
// are not listed. A step pinned to ANOTHER platform, or explicitly skipped
// with `_skip`, is not this run's assertion and is not listed.
// Each entry reads `step <index> "<name>" (<action kind>[, vision-gated])`.
func unrunAssertingSteps(tc *testbank.TestCase, platform config.Platform) []string {
	if tc == nil {
		return nil
	}
	var out []string
	for i := range tc.Steps {
		step := tc.Steps[i]
		if step.Skip {
			continue
		}
		if step.Platform != "" && step.Platform != platform {
			continue
		}
		at, _ := step.ParseAction()
		gated := step.IsVisionGated()
		if !gated {
			switch at {
			case testbank.ActionTypeDescription,
				testbank.ActionTypeScreenshot,
				testbank.ActionTypeSleep,
				testbank.ActionTypeShell:
				continue
			}
		}
		desc := fmt.Sprintf("step %d %q (%s", i, step.Name, at)
		if gated {
			desc += ", vision-gated"
		}
		out = append(out, desc+")")
	}
	return out
}

// hasSkipSentinel reports whether the result carries the
// definitionChallenge wrapper's skip sentinel in its RecordedActions.
func hasSkipSentinel(r *challenge.Result) bool {
	if r == nil {
		return false
	}
	for _, action := range r.RecordedActions {
		if strings.HasPrefix(action, definitionWrapperSkipSentinel) {
			return true
		}
	}
	return false
}

// newDefinitionRegistry returns a fresh, non-shared registry that the
// orchestrator can populate with definitionChallenge wrappers. Using a
// dedicated per-run registry (rather than registry.Default) keeps
// concurrent helixqa invocations from polluting each other's
// challenge namespaces.
func newDefinitionRegistry() registry.Registry {
	return registry.NewRegistry()
}

// definitionChallenge wraps a *challenge.Definition so it satisfies
// the challenge.Challenge interface.
//
// When the orchestrator has the executable bank case for this
// definition (testCase != nil) AND a target platform that can run
// its steps, Execute genuinely runs them. Otherwise Execute returns
// an honest Skipped result with an explicit reason.
type definitionChallenge struct {
	def *challenge.Definition
	cfg *challenge.Config

	// testCase is the executable bank case backing this definition,
	// when the orchestrator could load it via pkg/testbank. nil for
	// definitions loaded only through the generic challenges/pkg/bank
	// loader (which drops the `steps` array).
	testCase *testbank.TestCase

	// platform is the target platform this wrapper executes against.
	// Set by the orchestrator per runPlatform iteration.
	platform config.Platform

	// last is the result Execute itself produced. The challenges runner
	// replaces it with "timed out" when the run's deadline passed; the
	// orchestrator reads it back so a step that FAILED before the
	// cancellation keeps its verdict, and an interruption stays one.
	lastMu sync.Mutex
	last   *challenge.Result
}

// newDefinitionChallenge constructs a wrapper with no executable
// backing — Execute will honestly skip.
func newDefinitionChallenge(def *challenge.Definition) *definitionChallenge {
	return &definitionChallenge{def: def}
}

// newDefinitionChallengeForPlatform constructs a wrapper that carries
// the executable bank case and the target platform, so Execute can
// genuinely run the case's actions when the platform supports them.
func newDefinitionChallengeForPlatform(
	def *challenge.Definition,
	tc *testbank.TestCase,
	platform config.Platform,
) *definitionChallenge {
	return &definitionChallenge{
		def:      def,
		testCase: tc,
		platform: platform,
	}
}

// platformMatches reports whether this wrapper's bank case targets
// the platform the wrapper is running against. A case with no
// declared platforms (or with config.PlatformAll) matches every
// platform; otherwise the run platform must appear in the case's
// Platforms list.
//
// When no executable bank case is available (testCase == nil) the
// definition is declarative-only and not pinned to any platform, so
// it is treated as matching — the skip in that situation is "no
// backend dispatcher", not "wrong platform".
func (d *definitionChallenge) platformMatches() bool {
	if d.testCase == nil {
		return true
	}
	if len(d.testCase.Platforms) == 0 {
		return true
	}
	for _, p := range d.testCase.Platforms {
		if p == config.PlatformAll || p == d.platform {
			return true
		}
	}
	return false
}

// ID returns the definition ID verbatim.
func (d *definitionChallenge) ID() challenge.ID {
	return d.def.ID
}

// Name returns the definition Name verbatim.
func (d *definitionChallenge) Name() string {
	return d.def.Name
}

// Description returns the definition Description verbatim.
func (d *definitionChallenge) Description() string {
	return d.def.Description
}

// Category returns the definition Category verbatim.
func (d *definitionChallenge) Category() string {
	return d.def.Category
}

// Dependencies returns the definition Dependencies verbatim.
func (d *definitionChallenge) Dependencies() []challenge.ID {
	return d.def.Dependencies
}

// Configure captures the runtime config for use in Execute.
func (d *definitionChallenge) Configure(cfg *challenge.Config) error {
	d.cfg = cfg
	return nil
}

// Validate is a no-op for declarative definitions — preconditions
// declared in def.Inputs are validated at Execute time when a real
// backend dispatches against them.
func (d *definitionChallenge) Validate(ctx context.Context) error {
	return nil
}

// executableShellSteps returns the bank case's steps whose action is
// a host shell command (`shell:`) that the desktop platform can run.
// The returned slice preserves bank order. An empty slice means the
// case has nothing the desktop platform can execute.
func (d *definitionChallenge) executableShellSteps() []testbank.TestStep {
	if d.testCase == nil {
		return nil
	}
	var out []testbank.TestStep
	for _, step := range d.testCase.Steps {
		// Honour an explicit per-step skip.
		if step.Skip {
			continue
		}
		// A step pinned to a non-target platform is not ours to run.
		if step.Platform != "" && step.Platform != d.platform {
			continue
		}
		actionType, _ := step.ParseAction()
		if actionType == testbank.ActionTypeShell {
			out = append(out, step)
		}
	}
	return out
}

// Execute dispatches the definition.
//
// HXC-011 fix: on the desktop platform, when the wrapper carries an
// executable bank case with one or more `shell:` steps, each step's
// command is genuinely run via os/exec — the real exit code drives
// the verdict. Otherwise the result is an honest Skipped with an
// explicit reason (never a hollow PASS).
func (d *definitionChallenge) Execute(ctx context.Context) (*challenge.Result, error) {
	start := time.Now()
	var res *challenge.Result

	// Real execution path: desktop platform + executable shell steps.
	if d.platform == config.PlatformDesktop {
		if steps := d.executableShellSteps(); len(steps) > 0 {
			res = d.executeDesktopShellSteps(ctx, start, steps)
		}
	}
	if res == nil {
		// Honest-skip path: nothing this platform/wrapper can execute.
		res = d.skippedResult(start)
	}
	d.lastMu.Lock()
	d.last = snapshotResult(res)
	d.lastMu.Unlock()
	return res, nil
}

// lastResult returns a copy of the result Execute last produced, or nil.
func (d *definitionChallenge) lastResult() *challenge.Result {
	d.lastMu.Lock()
	defer d.lastMu.Unlock()
	return snapshotResult(d.last)
}

// snapshotResult copies the verdict-bearing fields of r (Result holds a
// mutex and must not be copied whole).
func snapshotResult(r *challenge.Result) *challenge.Result {
	if r == nil {
		return nil
	}
	return &challenge.Result{
		ChallengeID: r.ChallengeID, ChallengeName: r.ChallengeName, Status: r.Status, Error: r.Error,
		StartTime: r.StartTime, EndTime: r.EndTime, Duration: r.Duration,
		RecordedActions: append([]string(nil), r.RecordedActions...),
		Assertions:      append([]challenge.AssertionResult(nil), r.Assertions...),
	}
}

// interruptedMarker tags a result whose case the RUN's cancellation /
// timeout cut short.
const interruptedMarker = "interrupted-by-run-cancellation"

// hasInterruptedMarker reports a result carrying interruptedMarker.
func hasInterruptedMarker(r *challenge.Result) bool {
	if r == nil {
		return false
	}
	for _, a := range r.RecordedActions {
		if strings.Contains(a, interruptedMarker) {
			return true
		}
	}
	return false
}

// executeDesktopShellSteps runs every `shell:` step of the bank case
// via os/exec on the host, captures real exit codes + combined
// output, and scores PASS only when every step exits 0. Any non-zero
// exit (or spawn error) scores FAIL — a hollow metadata-only PASS is
// impossible because RecordedActions carry the real command output
// and the Duration is real wall-clock time.
func (d *definitionChallenge) executeDesktopShellSteps(
	ctx context.Context,
	start time.Time,
	steps []testbank.TestStep,
) *challenge.Result {
	recorded := make([]string, 0, len(steps)*2+1)
	assertions := make([]challenge.AssertionResult, 0, len(steps))
	allPassed := true
	var firstFailure string

	for i, step := range steps {
		_, command := step.ParseAction()
		stepTimeout := 30 * time.Second
		if d.cfg != nil && d.cfg.Timeout > 0 {
			stepTimeout = d.cfg.Timeout
		}
		if step.Timeout > 0 {
			stepTimeout = time.Duration(step.Timeout) * time.Second
		}

		stepCtx, cancel := context.WithTimeout(ctx, stepTimeout)
		// Own process group, killed whole on cancellation (no orphaned
		// children), Wait bounded by shellexec.WaitDelay.
		cmd := shellexec.Command(stepCtx, command)
		output, runErr := cmd.CombinedOutput()
		exitCode := 0
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}
		cancel()
		if ctx.Err() != nil && allPassed {
			// The RUN was cancelled / timed out while this command ran (not
			// the step's own timeout): its outcome is unknown — the case is
			// not executed, never a failure of its own.
			now := time.Now()
			recorded = append(recorded,
				fmt.Sprintf("shell-step[%d]: %s — interrupted by the run's cancellation / timeout (%v)", i, command, ctx.Err()),
				definitionWrapperSkipSentinel+" "+interruptedMarker+": the run was cancelled / timed out while step "+fmt.Sprintf("%d", i)+
					" ran — the case was not executed to the end and is not PASSED")
			return &challenge.Result{
				ChallengeID:     d.def.ID,
				ChallengeName:   d.def.Name,
				StartTime:       start,
				EndTime:         now,
				Duration:        now.Sub(start),
				RecordedActions: recorded,
				// The same evidence the honest-skip path carries, so the
				// runner's anti-bluff gate does not turn the interruption
				// into a failure.
				Assertions: append(assertions, challenge.AssertionResult{
					Type: "definition-loaded", Target: "definition.ID",
					Expected: string(d.def.ID), Actual: string(d.def.ID), Passed: true,
					Message: "bank-loaded definition bridged to Challenge interface; interrupted by the run's cancellation / timeout",
				}),
				Status: challenge.StatusSkipped,
			}
		}

		recorded = append(recorded,
			fmt.Sprintf("shell-step[%d]: %s", i, command))
		recorded = append(recorded,
			fmt.Sprintf("shell-step[%d]: exit=%d output=%q",
				i, exitCode, truncateForRecord(string(output))))

		// The verdict comes from the ONE shared function so this runner and
		// the autonomous executor cannot disagree: no expect_exit means the
		// command must exit 0; expect_exit N means exactly N; and
		// expect_output_contains is read from the combined output. A command
		// that never started (ProcessState nil) can satisfy nothing.
		stepPassed, why := step.CheckShellOutcome(exitCode, string(output))
		if cmd.ProcessState == nil {
			stepPassed, why = false, fmt.Sprintf("command did not run: %v", runErr)
		}
		if !stepPassed {
			allPassed = false
			if firstFailure == "" {
				firstFailure = fmt.Sprintf(
					"step %d %q: %s: %s",
					i, command, why,
					truncateForRecord(string(output)))
			}
		}

		wantExitCode := 0
		if step.ExpectExit != nil {
			wantExitCode = *step.ExpectExit
		}
		wantExit := fmt.Sprintf("%d", wantExitCode)
		exitOK := cmd.ProcessState != nil && exitCode == wantExitCode
		exitAssertionType := "shell-exit-zero"
		if step.ExpectExit != nil {
			exitAssertionType = "shell-exit"
		}
		assertions = append(assertions, challenge.AssertionResult{
			Type:     exitAssertionType,
			Target:   fmt.Sprintf("step[%d].exit_code", i),
			Expected: wantExit,
			Actual:   fmt.Sprintf("%d", exitCode),
			Passed:   exitOK,
			Message: fmt.Sprintf(
				"desktop shell action %q exited %d (expected %s)", command, exitCode, wantExit),
		})
		if step.ExpectOutputContains != nil {
			contains := *step.ExpectOutputContains != "" &&
				strings.Contains(string(output), *step.ExpectOutputContains)
			assertions = append(assertions, challenge.AssertionResult{
				Type:     "shell-output-contains",
				Target:   fmt.Sprintf("step[%d].output", i),
				Expected: *step.ExpectOutputContains,
				Actual:   truncateForRecord(string(output)),
				Passed:   contains,
				Message: fmt.Sprintf(
					"desktop shell action %q output contains expected substring: %t", command, contains),
			})
		}
	}

	now := time.Now()
	result := &challenge.Result{
		ChallengeID:     d.def.ID,
		ChallengeName:   d.def.Name,
		StartTime:       start,
		EndTime:         now,
		Duration:        now.Sub(start),
		RecordedActions: recorded,
		Assertions:      assertions,
	}
	switch {
	case allPassed && caseHasUnrunAssertingStep(d.testCase, d.platform):
		// The shell steps passed, but the case also asserts something this
		// runner never executed (a vision-gated step, an http:/tap:/…
		// action). The case was only partly run, so it is not PASSED: it
		// is an honest SKIPPED that names what did not run, and
		// promoteSkippedToPassed refuses to promote it for the same reason.
		result.Status = challenge.StatusSkipped
		result.RecordedActions = append(result.RecordedActions,
			definitionWrapperSkipSentinel+" shell step(s) passed, but these asserting step(s) were not executed by "+
				"the bank runner: "+strings.Join(unrunAssertingSteps(d.testCase, d.platform), "; ")+
				" — the case is not PASSED")
	case allPassed:
		result.Status = challenge.StatusPassed
	default:
		result.Status = challenge.StatusFailed
		result.Error = "desktop shell action failed: " + firstFailure
	}
	return result
}

// skippedResult builds the honest-skip result for a wrapper that has
// nothing the current platform can execute. The skip reason is
// explicit (§11.4.3) and carries the definitionWrapperSkipSentinel so
// restoreSkippedFromDefinitionWrapper keeps the Skipped status after
// the challenges-runner merge.
func (d *definitionChallenge) skippedResult(start time.Time) *challenge.Result {
	now := time.Now()
	var skipReason string
	switch {
	case d.testCase == nil:
		skipReason = "declarative-only definition (no executable bank " +
			"case loaded) — no backend dispatcher for this Category"
	case d.platform != config.PlatformDesktop:
		skipReason = fmt.Sprintf(
			"bank case has no executable action for the %q platform "+
				"(needs an Android/UI/web topology backend)", d.platform)
	default:
		skipReason = "bank case has no desktop-executable `shell:` " +
			"action — convert prose steps to `action: \"shell: <cmd>\"`"
	}
	return &challenge.Result{
		ChallengeID:   d.def.ID,
		ChallengeName: d.def.Name,
		Status:        challenge.StatusSkipped,
		StartTime:     start,
		EndTime:       now,
		Duration:      now.Sub(start),
		RecordedActions: []string{
			"definition-loaded: id=" + string(d.def.ID),
			"definition-loaded: category=" + d.def.Category,
			definitionWrapperSkipSentinel + " " + skipReason,
		},
		Assertions: []challenge.AssertionResult{{
			Type:     "definition-loaded",
			Target:   "definition.ID",
			Expected: string(d.def.ID),
			Actual:   string(d.def.ID),
			Passed:   true,
			Message: "bank-loaded definition bridged to Challenge " +
				"interface; honestly skipped (no executable action)",
		}},
		Error: "",
	}
}

// truncateForRecord caps command output captured into RecordedActions
// so a noisy command does not bloat the report.
func truncateForRecord(s string) string {
	const max = 512
	s = strings.TrimRight(s, "\n")
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

// Cleanup is a no-op for declarative definitions.
func (d *definitionChallenge) Cleanup(ctx context.Context) error {
	return nil
}

// Compile-time interface assertion.
var _ challenge.Challenge = (*definitionChallenge)(nil)
