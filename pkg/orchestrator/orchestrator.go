// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// Package orchestrator ties together the detector, validator,
// and reporter into a complete QA execution pipeline. It loads
// test banks via the Challenges framework, runs challenges per
// platform, validates each step, and produces a combined QA
// report.
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"digital.vasic.challenges/pkg/bank"
	"digital.vasic.challenges/pkg/challenge"
	"digital.vasic.challenges/pkg/logging"
	"digital.vasic.challenges/pkg/runner"

	"digital.vasic.helixqa/pkg/config"
	"digital.vasic.helixqa/pkg/detector"
	"digital.vasic.helixqa/pkg/reporter"
	"digital.vasic.helixqa/pkg/testbank"
	"digital.vasic.helixqa/pkg/validator"
)

// Result captures the complete outcome of a HelixQA run.
type Result struct {
	// Report is the generated QA report.
	Report *reporter.QAReport `json:"report"`

	// ReportPath is where the report was written.
	ReportPath string `json:"report_path"`

	// Success is true if no crashes or test failures occurred.
	Success bool `json:"success"`

	// StartTime is when the run began.
	StartTime time.Time `json:"start_time"`

	// EndTime is when the run completed.
	EndTime time.Time `json:"end_time"`

	// Duration is the total run time.
	Duration time.Duration `json:"duration"`
}

// ExitCode is the process exit status for a bank run (`helixqa run`), on
// the same three-valued contract as `helixqa autonomous`:
//   - 1: any challenge failed (failed / error / timed out / stuck), or a
//     crash or ANR was detected — a finding outranks could-not-determine;
//   - 2: nothing failed, but not every challenge passed: nothing executed,
//     or some/all challenges were SKIPPED (e.g. a case whose vision-gated
//     step the bank runner cannot execute) — not a pass, not a failure;
//   - 0: every challenge executed and passed, no crash, no ANR.
func (r *Result) ExitCode() int {
	q := r.Report
	if q == nil {
		return 2
	}
	if q.FailedChallenges > 0 || q.TotalCrashes > 0 || q.TotalANRs > 0 {
		return 1
	}
	if q.TotalChallenges == 0 || q.PassedChallenges < q.TotalChallenges {
		return 2
	}
	return 0
}

// Orchestrator is the main QA execution engine.
type Orchestrator struct {
	config   *config.Config
	detector *detector.Detector
	val      *validator.Validator
	reporter *reporter.Reporter
	logger   logging.Logger
	runner   runner.Runner
	bank     *bank.Bank

	// executableCases indexes the executable bank cases (loaded via
	// pkg/testbank, which — unlike the generic challenges/pkg/bank
	// loader — preserves the `steps` array including each step's
	// `action:`). The orchestrator bridges these into per-platform
	// definitionChallenge wrappers so a desktop-platform bank case's
	// `shell:` action is genuinely run. Closes HXC-011. Keyed by
	// challenge ID. Empty when banks were supplied via WithBank or
	// when re-parsing as testbank failed (the wrapper then honestly
	// skips rather than bluffing a PASS).
	executableCases map[challenge.ID]*testbank.TestCase
}

// Option configures an Orchestrator.
type Option func(*Orchestrator)

// WithLogger sets the logger.
func WithLogger(logger logging.Logger) Option {
	return func(o *Orchestrator) {
		o.logger = logger
	}
}

// WithRunner sets a custom challenge runner.
func WithRunner(r runner.Runner) Option {
	return func(o *Orchestrator) {
		o.runner = r
	}
}

// WithDetector sets a custom detector.
func WithDetector(d *detector.Detector) Option {
	return func(o *Orchestrator) {
		o.detector = d
	}
}

// WithValidator sets a custom validator.
func WithValidator(v *validator.Validator) Option {
	return func(o *Orchestrator) {
		o.val = v
	}
}

// WithReporter sets a custom reporter.
func WithReporter(r *reporter.Reporter) Option {
	return func(o *Orchestrator) {
		o.reporter = r
	}
}

// WithBank sets a pre-loaded test bank.
func WithBank(b *bank.Bank) Option {
	return func(o *Orchestrator) {
		o.bank = b
	}
}

// New creates an Orchestrator with the given configuration
// and options.
func New(cfg *config.Config, opts ...Option) *Orchestrator {
	o := &Orchestrator{
		config: cfg,
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// Run executes the complete QA pipeline:
// 1. Load test banks
// 2. For each platform, run challenges with validation
// 3. Generate combined report
func (o *Orchestrator) Run(
	ctx context.Context,
) (*Result, error) {
	result := &Result{
		StartTime: time.Now(),
	}

	o.log("Starting HelixQA run")

	// 1. Load test banks.
	if err := o.loadBanks(); err != nil {
		return nil, fmt.Errorf("load banks: %w", err)
	}

	definitions := o.bank.All()
	// The bank is a map: without an explicit order every run would execute
	// the challenges in a different random order (and a timeout would cut
	// a different one). Run them in ID order.
	sort.SliceStable(definitions, func(i, j int) bool { return definitions[i].ID < definitions[j].ID })
	o.log("Loaded %d challenge definitions from %d sources",
		len(definitions), len(o.bank.Sources()))

	// 1.5. Load executable bank cases via pkg/testbank. The generic
	// challenges/pkg/bank loader drops each case's `steps` array, so
	// it cannot execute a case's `action:` command. pkg/testbank
	// preserves the steps; the orchestrator indexes them so the
	// per-platform definitionChallenge wrappers can genuinely run a
	// desktop-platform `shell:` action (HXC-011 fix). A failure to
	// re-parse a bank as testbank is non-fatal — the wrapper simply
	// honestly skips that case rather than bluffing a PASS.
	o.loadExecutableCases()

	// 2. Create output directory.
	if err := os.MkdirAll(o.config.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	// 3. Run challenges for each platform.
	platforms := o.config.ExpandedPlatforms()
	var platformResults []*reporter.PlatformResult
	// runErr is an error raised after execution began (cancellation): the
	// platforms that already ran keep their verdicts in the report.
	var runErr error

platforms:
	for _, platform := range platforms {
		select {
		case <-ctx.Done():
			runErr = ctx.Err()
			break platforms
		default:
		}

		o.log("Testing platform: %s", platform)

		pr, err := o.runPlatform(ctx, platform, definitions)
		if err != nil {
			o.logError("Platform %s failed: %v",
				platform, err)
			if pr == nil {
				pr = &reporter.PlatformResult{
					Platform:  platform,
					StartTime: time.Now(),
					EndTime:   time.Now(),
				}
			}
			if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				// Cancelled / timed out WHILE the platform ran: keep the
				// challenges it already ran (a FAILED one still decides
				// the exit code) and stop — the run is incomplete.
				runErr = err
				platformResults = append(platformResults, pr)
				break platforms
			}
		}
		platformResults = append(platformResults, pr)
	}

	// 4. Generate combined report.
	rep := o.getReporter()
	qaReport, err := rep.GenerateQAReport(platformResults)
	if err != nil {
		return nil, fmt.Errorf("generate report: %w", err)
	}

	result.Report = qaReport
	result.EndTime = time.Now()
	result.Duration = result.EndTime.Sub(result.StartTime)
	reportPath, err := rep.WriteReport(
		qaReport, o.config.OutputDir,
	)
	if err != nil {
		// Cases already ran: return their verdicts WITH the error, so a
		// FAILED case still decides the exit code (1 outranks the error).
		return result, fmt.Errorf("write report: %w", err)
	}
	result.ReportPath = reportPath
	// CONST-035 anti-bluff: a run with 0 executed challenges is NOT a
	// success — it's a PASS-bluff (the absence-of-crash check passes
	// trivially when no app was launched, but no actual behavior was
	// verified). Success requires that at least one challenge actually
	// ran AND that every executed challenge passed AND no crash/ANR.
	// Previously the bool was `Failed==0 && Crashes==0 && ANRs==0`
	// which evaluated to true for 0/0 runs — the canonical structural
	// bluff per the operator's bluff taxonomy ("ValidateStep returns
	// PASSED whenever HasCrash==false, regardless of whether any
	// challenge body executed").
	result.Success = qaReport.TotalChallenges > 0 &&
		qaReport.PassedChallenges == qaReport.TotalChallenges &&
		qaReport.FailedChallenges == 0 &&
		qaReport.TotalCrashes == 0 &&
		qaReport.TotalANRs == 0

	o.log("HelixQA run complete: %d/%d passed, %d crashes, "+
		"%d ANRs, report at %s",
		qaReport.PassedChallenges,
		qaReport.TotalChallenges,
		qaReport.TotalCrashes,
		qaReport.TotalANRs,
		reportPath,
	)

	return result, runErr
}

// runPlatform executes all challenges for a single platform.
func (o *Orchestrator) runPlatform(
	ctx context.Context,
	platform config.Platform,
	definitions []*challenge.Definition,
) (*reporter.PlatformResult, error) {
	pr := &reporter.PlatformResult{
		Platform:  platform,
		StartTime: time.Now(),
	}

	evidenceDir := filepath.Join(
		o.config.OutputDir,
		"evidence",
		string(platform),
	)
	if err := os.MkdirAll(evidenceDir, 0755); err != nil {
		return nil, fmt.Errorf(
			"create evidence dir: %w", err,
		)
	}
	pr.EvidenceDir = evidenceDir

	// Create platform-specific detector if validation enabled.
	var val *validator.Validator
	if o.config.ValidateSteps {
		det := o.getDetector(platform, evidenceDir)
		val = validator.New(
			det,
			validator.WithEvidenceDir(evidenceDir),
		)
	}

	// Build the per-platform runner. When the caller supplied a
	// custom runner (WithRunner) it is used verbatim. Otherwise a
	// fresh per-platform registry is populated with definitionChallenge
	// wrappers that carry the executable bank case AND this platform —
	// so a desktop-platform `shell:` action is genuinely run (HXC-011).
	// A per-platform registry is required because the wrapper's
	// execution behaviour depends on the target platform; a single
	// shared registry could not distinguish desktop from android.
	platformRunner := o.runner
	if platformRunner == nil {
		reg := newDefinitionRegistry()
		for _, def := range definitions {
			tc := o.executableCases[def.ID]
			wrapper := newDefinitionChallengeForPlatform(def, tc, platform)
			if err := reg.Register(wrapper); err != nil {
				return nil, fmt.Errorf(
					"register definition %s for platform %s: %w",
					def.ID, platform, err)
			}
		}
		platformRunner = runner.NewRunner(runner.WithRegistry(reg))
		o.log("Platform %s: wired runner with %d bridged definitions "+
			"(%d executable bank cases) — HXC-011",
			platform, len(definitions), len(o.executableCases))
	}

	// Execute each challenge definition.
	for _, def := range definitions {
		select {
		case <-ctx.Done():
			pr.EndTime = time.Now()
			pr.Duration = pr.EndTime.Sub(pr.StartTime)
			return pr, ctx.Err()
		default:
		}

		// Create challenge config.
		cfg := challenge.NewConfig(def.ID)
		cfg.Verbose = o.config.Verbose
		cfg.Timeout = o.config.StepTimeout
		cfg.ResultsDir = filepath.Join(
			o.config.OutputDir,
			"results",
			string(platform),
			string(def.ID),
		)

		// Run challenge if runner is available.
		var challengeResult *challenge.Result
		if platformRunner != nil {
			cr, err := platformRunner.Run(
				ctx, def.ID, cfg,
			)
			if ctx.Err() != nil && interruptedOutcome(cr, err) {
				// The run was cancelled / timed out while this challenge
				// ran and it reports an error / timeout: its outcome is
				// unknown — not a failure. Record it as not executed and
				// stop the platform. (A challenge that FAILED on its own
				// before the cancellation keeps its verdict.)
				why := "run cancelled / timed out"
				if err != nil {
					why = err.Error()
				} else if cr != nil && cr.Error != "" {
					why = cr.Error
				}
				pr.ChallengeResults = append(pr.ChallengeResults, &challenge.Result{
					ChallengeID:   def.ID,
					ChallengeName: def.Name,
					Status:        challenge.StatusSkipped,
					Error:         "interrupted: " + why,
					StartTime:     time.Now(),
					EndTime:       time.Now(),
				})
				pr.EndTime = time.Now()
				pr.Duration = pr.EndTime.Sub(pr.StartTime)
				return pr, ctx.Err()
			}
			if err != nil {
				o.logError("Challenge %s failed: %v",
					def.ID, err)
				cr = &challenge.Result{
					ChallengeID:   def.ID,
					ChallengeName: def.Name,
					Status:        challenge.StatusError,
					Error:         err.Error(),
					StartTime:     time.Now(),
					EndTime:       time.Now(),
				}
			}
			// round-82 §11.4 anti-bluff fix: the close-out⁷⁵
			// definitionChallenge wrapper returns Status=Skipped
			// with a "skip-reason:" sentinel in RecordedActions —
			// but the challenges-runner's executeChallenge merge
			// logic only preserves Failed/TimedOut/Error from the
			// inner Execute call. For Skipped+passing-assertions,
			// the runner unconditionally overrides to Passed at
			// runner.go:527, producing the canonical CONST-035 /
			// Article XI §11.9 PASS-bluff: declarative-only
			// definitions with NO real backend dispatch report
			// success to the end user. The orchestrator owns the
			// wrapper, so it owns the restoration: detect the
			// "skip-reason:" sentinel and restore Skipped before
			// the reporter aggregates pass/fail counts. The
			// challenges submodule is decoupled (CONST-051(B)) —
			// fix lives here, not there.
			restoreSkippedFromDefinitionWrapper(cr)
			challengeResult = cr
		}

		// Validate step if enabled.
		var stepResult *validator.StepResult
		if val != nil {
			sr, err := val.ValidateStep(
				ctx,
				string(def.ID),
				platform,
			)
			if err != nil {
				o.logError("Validation failed for %s: %v",
					def.ID, err)
			}
			if sr != nil {
				stepResult = sr
				pr.StepResults = append(
					pr.StepResults, sr,
				)
				if sr.Detection != nil {
					if sr.Detection.HasCrash {
						pr.CrashCount++
					}
					if sr.Detection.HasANR {
						pr.ANRCount++
					}
				}
			}
		}

		// Reporting-aggregation fix: a challenge whose bank case
		// targets the platform being run AND whose step validation
		// PASSED must aggregate to PASSED — not SKIPPED. The wrapper
		// honestly skips a matching-platform case that has no
		// `shell:` step it can execute directly (the case's steps run
		// through the step-validation / LLM-bridge path instead), but
		// leaving that as SKIPPED while every step PASSED is itself a
		// §107 reporting bluff: the report claims 0/N passed even
		// though the steps executed and passed. promoteSkippedToPassed
		// performs that promotion ONLY when the platform genuinely
		// matches and step validation passed; a case pinned to a
		// non-matching platform stays SKIPPED (and is never counted as
		// a failure). See promoteSkippedToPassed for the full guard.
		if challengeResult != nil {
			promoteSkippedToPassed(
				challengeResult,
				stepResult,
				o.executableCases[def.ID],
				platform,
			)
			pr.ChallengeResults = append(
				pr.ChallengeResults, challengeResult,
			)
		}

		// Apply step delay based on speed mode.
		delay := o.config.StepDelay()
		if delay > 0 {
			select {
			case <-ctx.Done():
				break
			case <-time.After(delay):
			}
		}
	}

	pr.EndTime = time.Now()
	pr.Duration = pr.EndTime.Sub(pr.StartTime)
	return pr, nil
}

// loadExecutableCases re-parses every configured bank file through
// pkg/testbank — which preserves each case's `steps` array (including
// every step's `action:`) — and indexes the cases by challenge ID.
// The generic challenges/pkg/bank loader used by loadBanks drops the
// steps, so without this second parse the orchestrator would have no
// executable action data and definitionChallenge would only ever be
// able to honestly skip (HXC-011 root cause).
//
// Non-fatal by design: a bank that fails to re-parse as testbank
// (older JSON shape, etc.) simply contributes no executable cases —
// its definitions then honestly skip rather than bluff a PASS. When
// banks were supplied via WithBank there are no file paths to
// re-parse, so the map stays empty and every wrapper honestly skips.
func (o *Orchestrator) loadExecutableCases() {
	o.executableCases = make(map[challenge.ID]*testbank.TestCase)
	for _, path := range o.config.Banks {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		var files []string
		if info.IsDir() {
			entries, derr := os.ReadDir(path)
			if derr != nil {
				continue
			}
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				ext := filepath.Ext(e.Name())
				if ext == ".yaml" || ext == ".yml" || ext == ".json" {
					files = append(files, filepath.Join(path, e.Name()))
				}
			}
		} else {
			files = []string{path}
		}
		for _, f := range files {
			bf, lerr := testbank.LoadFile(f)
			if lerr != nil {
				o.logError("HXC-011: bank %s not re-parsable as "+
					"testbank (%v) — its cases will honestly skip", f, lerr)
				continue
			}
			for i := range bf.TestCases {
				tc := bf.TestCases[i]
				o.executableCases[challenge.ID(tc.ID)] = &tc
			}
		}
	}
}

// loadBanks loads test banks from configured paths.
func (o *Orchestrator) loadBanks() error {
	if o.bank != nil {
		return nil // Already loaded.
	}
	if len(o.config.Banks) == 0 {
		return fmt.Errorf("no test bank paths configured")
	}
	o.bank = bank.New()

	for _, path := range o.config.Banks {
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("stat bank %s: %w", path, err)
		}
		if info.IsDir() {
			if err := o.bank.LoadDir(path); err != nil {
				return fmt.Errorf(
					"load bank dir %s: %w", path, err,
				)
			}
		} else {
			if err := o.bank.LoadFile(path); err != nil {
				return fmt.Errorf(
					"load bank file %s: %w", path, err,
				)
			}
		}
	}
	return nil
}

// getDetector returns the configured detector or creates one.
func (o *Orchestrator) getDetector(
	platform config.Platform,
	evidenceDir string,
) *detector.Detector {
	if o.detector != nil {
		return o.detector
	}

	opts := []detector.Option{
		detector.WithEvidenceDir(evidenceDir),
	}

	switch platform {
	case config.PlatformAndroid:
		opts = append(opts,
			detector.WithDevice(o.config.Device),
			detector.WithPackageName(o.config.PackageName),
		)
	case config.PlatformWeb:
		opts = append(opts,
			detector.WithBrowserURL(o.config.BrowserURL),
		)
	case config.PlatformDesktop:
		opts = append(opts,
			detector.WithProcessName(
				o.config.DesktopProcess,
			),
			detector.WithProcessPID(o.config.DesktopPID),
		)
	}

	return detector.New(platform, opts...)
}

// getReporter returns the configured reporter or creates one.
func (o *Orchestrator) getReporter() *reporter.Reporter {
	if o.reporter != nil {
		return o.reporter
	}
	return reporter.New(
		reporter.WithOutputDir(o.config.OutputDir),
		reporter.WithReportFormat(o.config.ReportFormat),
	)
}

// log writes an info-level log message.
func (o *Orchestrator) log(format string, args ...any) {
	if o.logger == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	o.logger.Info(msg)
}

// logError writes an error-level log message.
func (o *Orchestrator) logError(format string, args ...any) {
	if o.logger == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	o.logger.Error(msg)
}

// interruptedOutcome reports a challenge outcome that a cancellation /
// timeout of the RUN produces rather than the app: a runner error, no
// result, or an error / timed-out / stuck status.
func interruptedOutcome(cr *challenge.Result, err error) bool {
	if err != nil || cr == nil {
		return true
	}
	switch cr.Status {
	case challenge.StatusError, challenge.StatusTimedOut, challenge.StatusStuck:
		return true
	}
	return false
}
