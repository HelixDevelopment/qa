// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"digital.vasic.challenges/pkg/challenge"

	"digital.vasic.helixqa/pkg/config"
	"digital.vasic.helixqa/pkg/reporter"
	"digital.vasic.helixqa/pkg/testbank"
)

func writeBank(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "nbv.bank.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func runBank(t *testing.T, body string) *Result {
	t.Helper()
	cfg := &config.Config{Banks: []string{writeBank(t, body)}, Platforms: []config.Platform{config.PlatformDesktop},
		OutputDir: t.TempDir(), Speed: config.SpeedNormal, ReportFormat: config.ReportMarkdown,
		Timeout: 2 * time.Minute, StepTimeout: 30 * time.Second}
	res, err := New(cfg).Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

const bankHead = "version: \"1.0\"\nname: nbv\ntest_cases:\n  - id: NBV-1\n    name: case\n    platforms: [desktop]\n    steps:\n"

// Review round 3, finding 2 (pins N32): END-TO-END through orch.Run — a
// desktop bank whose shell step passes but which carries a vision-gated
// step the runner cannot execute is NOT a success and has no passed
// challenge; its exit code is 2. Golden-good / golden-bad neighbours.
func TestOrchRun_ShellPlusGatedBankNotPassed(t *testing.T) {
	mixed := runBank(t, bankHead+
		"      - name: cli\n        action: \"shell: true\"\n"+
		"      - name: look\n        action: screenshot\n        expected: home\n        vision_verify: true\n")
	if mixed.Success || mixed.Report.PassedChallenges != 0 {
		t.Fatalf("shell+gated bank: success=%v passed=%d — the vision step never ran", mixed.Success, mixed.Report.PassedChallenges)
	}
	if got := mixed.ExitCode(); got != 2 {
		t.Fatalf("shell+gated bank exit = %d, want 2 (not executed is not a pass, not a failure)", got)
	}
	ok := runBank(t, bankHead+"      - name: cli\n        action: \"shell: true\"\n")
	if !ok.Success || ok.ExitCode() != 0 {
		t.Fatalf("shell-only bank: success=%v exit=%d", ok.Success, ok.ExitCode())
	}
	bad := runBank(t, bankHead+"      - name: cli\n        action: \"shell: exit 3\"\n")
	if bad.Success || bad.ExitCode() != 1 {
		t.Fatalf("failing shell bank: success=%v exit=%d", bad.Success, bad.ExitCode())
	}
}

// Review round 3, finding 3: `helixqa run` gets the three-valued contract.
func TestBankRunnerExit_ThreeValued(t *testing.T) {
	cases := []struct {
		name string
		r    *Result
		want int
	}{
		{"no report", &Result{}, 2},
		{"nothing executed", &Result{Report: &reporter.QAReport{}}, 2},
		{"skipped only", &Result{Report: &reporter.QAReport{TotalChallenges: 1}}, 2},
		{"passed + skipped", &Result{Report: &reporter.QAReport{TotalChallenges: 2, PassedChallenges: 1}}, 2},
		{"all passed", &Result{Report: &reporter.QAReport{TotalChallenges: 2, PassedChallenges: 2}}, 0},
		{"failed", &Result{Report: &reporter.QAReport{TotalChallenges: 2, PassedChallenges: 1, FailedChallenges: 1}}, 1},
		{"crash", &Result{Report: &reporter.QAReport{TotalChallenges: 1, PassedChallenges: 1, TotalCrashes: 1}}, 1},
		{"anr", &Result{Report: &reporter.QAReport{TotalChallenges: 1, PassedChallenges: 1, TotalANRs: 1}}, 1},
	}
	for _, c := range cases {
		if got := c.r.ExitCode(); got != c.want {
			t.Errorf("%s: ExitCode = %d, want %d", c.name, got, c.want)
		}
	}
}

func execDesktop(t *testing.T, steps ...testbank.TestStep) *challenge.Result {
	t.Helper()
	tc := &testbank.TestCase{ID: "DSK", Name: "dsk", Steps: steps}
	d := &definitionChallenge{def: &challenge.Definition{ID: "DSK", Name: "dsk"}, cfg: &challenge.Config{Timeout: 30 * time.Second},
		platform: config.PlatformDesktop, testCase: tc}
	res, err := d.Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// Pins N12 + finding 4: ungated frame_diff / playwright steps are
// asserting actions the desktop runner never executes, and the SKIPPED
// reason names every unexecuted step (index, name, action kind).
func TestDesktopShellCase_SkipReasonNamesUnexecutedSteps(t *testing.T) {
	shell := testbank.TestStep{Name: "cli", Action: "shell: true"}
	for _, other := range []testbank.TestStep{
		{Name: "motion", Action: "frame_diff: 10"},
		{Name: "browse", Action: "playwright: navigate http://127.0.0.1:1/"},
		{Name: "look", Action: "screenshot", VisionVerify: true},
	} {
		res := execDesktop(t, shell, other)
		if res.Status != challenge.StatusSkipped {
			t.Errorf("%s: status %q, want SKIPPED", other.Name, res.Status)
			continue
		}
		all := strings.Join(res.RecordedActions, "\n")
		want := `step 1 "` + other.Name + `"`
		if !strings.Contains(all, want) {
			t.Errorf("%s: skip reason does not name the unexecuted step (%s): %s", other.Name, want, all)
		}
	}
}

// Finding 5: a gated step pinned to ANOTHER platform, or explicitly
// `_skip`ped with a reason, is not an unexecuted assertion of THIS desktop
// run, so it does not stop the case from passing; a gated step for this
// platform still does.
func TestDesktopShellCase_HonoursPlatformAndSkip(t *testing.T) {
	shell := testbank.TestStep{Name: "cli", Action: "shell: true"}
	android := testbank.TestStep{Name: "tv", Action: "screenshot", VisionVerify: true, Platform: config.PlatformAndroid}
	skipped := testbank.TestStep{Name: "later", Action: "screenshot", VisionVerify: true, Skip: true, SkipReason: "needs a TV"}
	desktop := testbank.TestStep{Name: "here", Action: "screenshot", VisionVerify: true, Platform: config.PlatformDesktop}
	if res := execDesktop(t, shell, android); res.Status != challenge.StatusPassed {
		t.Errorf("android-pinned gated step blocked the desktop case: %q %v", res.Status, res.RecordedActions)
	}
	if res := execDesktop(t, shell, skipped); res.Status != challenge.StatusPassed {
		t.Errorf("_skip gated step blocked the case: %q %v", res.Status, res.RecordedActions)
	}
	if res := execDesktop(t, shell, desktop); res.Status == challenge.StatusPassed {
		t.Errorf("desktop-pinned gated step did not stop the PASS")
	}
	tc := &testbank.TestCase{Steps: []testbank.TestStep{android}}
	if caseHasUnrunAssertingStep(tc, config.PlatformDesktop) {
		t.Error("android-pinned step counted for desktop")
	}
	if !caseHasUnrunAssertingStep(tc, config.PlatformAndroid) {
		t.Error("android-pinned step not counted for android")
	}
}

// Review round 5, finding 5: an error AFTER cases ran (here: the report
// cannot be written) must not erase their verdict — Run returns the result
// with its report alongside the error, so a FAILED case still exits 1.
func TestOrchRun_FailedVerdictSurvivesPostExecutionError(t *testing.T) {
	out := t.TempDir()
	// A directory where the markdown report file must go makes the write fail.
	if err := os.MkdirAll(filepath.Join(out, "qa-report.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Banks: []string{writeBank(t, bankHead+"      - name: cli\n        action: \"shell: exit 3\"\n")},
		Platforms: []config.Platform{config.PlatformDesktop}, OutputDir: out, Speed: config.SpeedNormal,
		ReportFormat: config.ReportMarkdown, Timeout: 2 * time.Minute, StepTimeout: 30 * time.Second}
	res, err := New(cfg).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "write report") {
		t.Fatalf("report write failure not reported: %v", err)
	}
	if res == nil || res.Report == nil || res.Report.FailedChallenges != 1 || res.ExitCode() != 1 {
		t.Fatalf("FAILED verdict lost with the post-execution error: %+v", res)
	}
}

// scriptRunner scripts challenge outcomes per call and can cancel the run
// (SIGINT / --timeout arriving while a platform is running).
type scriptRunner struct {
	mu     sync.Mutex
	calls  int
	cancel context.CancelFunc
	script map[int]string // call -> "fail" | "cancel+pass" | "cancel+fail" | "cancel+error"
}

func (r *scriptRunner) Run(ctx context.Context, id challenge.ID, _ *challenge.Config) (*challenge.Result, error) {
	r.mu.Lock()
	r.calls++
	n := r.calls
	r.mu.Unlock()
	st := challenge.StatusPassed
	switch r.script[n] {
	case "fail":
		st = challenge.StatusFailed
	case "cancel+pass":
		r.cancel()
	case "cancel+fail":
		r.cancel()
		st = challenge.StatusFailed
	case "cancel+error":
		r.cancel()
		return nil, ctx.Err()
	}
	now := time.Now()
	return &challenge.Result{ChallengeID: id, ChallengeName: string(id), Status: st, StartTime: now, EndTime: now}, nil
}
func (r *scriptRunner) RunAll(context.Context, *challenge.Config) ([]*challenge.Result, error) {
	return nil, nil
}
func (r *scriptRunner) RunSequence(context.Context, []challenge.ID, *challenge.Config) ([]*challenge.Result, error) {
	return nil, nil
}
func (r *scriptRunner) RunParallel(context.Context, []challenge.ID, *challenge.Config, int) ([]*challenge.Result, error) {
	return nil, nil
}

// exitLike mirrors cmd/helixqa runExitCode: a failure in the report is 1,
// otherwise any error is 2.
func exitLike(res *Result, err error) int {
	if res != nil && res.Report != nil && res.ExitCode() == 1 {
		return 1
	}
	if err != nil || res == nil {
		return 2
	}
	return res.ExitCode()
}

const cancelBank = "version: \"1.0\"\nname: cx\ntest_cases:\n" +
	"  - id: O1\n    name: one\n    platforms: [desktop, web]\n    steps:\n      - name: a\n        action: \"shell: true\"\n" +
	"  - id: O2\n    name: two\n    platforms: [desktop, web]\n    steps:\n      - name: a\n        action: \"shell: true\"\n" +
	"  - id: O3\n    name: three\n    platforms: [desktop, web]\n    steps:\n      - name: a\n        action: \"shell: true\"\n"

// Review round 6, finding 1 (HIGH): a cancellation (SIGINT / --timeout)
// that arrives WHILE a platform runs must keep that platform's partial
// results and surface the context error — a FAILED challenge that already
// ran still exits 1, an unfinished run never exits 0.
func TestOrchRun_CancellationKeepsPartialPlatformResults(t *testing.T) {
	cases := []struct {
		name      string
		platforms []config.Platform
		script    map[int]string
		want      int
	}{
		{"one platform: O1 FAILED, cancel during O2", []config.Platform{config.PlatformDesktop}, map[int]string{1: "fail", 2: "cancel+pass"}, 1},
		{"desktop passes; web O1 FAILED + cancel", []config.Platform{config.PlatformDesktop, config.PlatformWeb}, map[int]string{4: "cancel+fail"}, 1},
		{"desktop passes; cancel during web O2", []config.Platform{config.PlatformDesktop, config.PlatformWeb}, map[int]string{5: "cancel+pass"}, 2},
		{"desktop O3 FAILED + cancel between platforms", []config.Platform{config.PlatformDesktop, config.PlatformWeb}, map[int]string{3: "cancel+fail"}, 1},
		{"challenge interrupted (runner error) is not a failure", []config.Platform{config.PlatformDesktop}, map[int]string{2: "cancel+error"}, 2},
	}
	for _, c := range cases {
		ctx, cancel := context.WithCancel(context.Background())
		r := &scriptRunner{cancel: cancel, script: c.script}
		cfg := &config.Config{Banks: []string{writeBank(t, cancelBank)}, Platforms: c.platforms,
			OutputDir: t.TempDir(), Speed: config.SpeedFast, ReportFormat: config.ReportMarkdown,
			Timeout: time.Minute, StepTimeout: 10 * time.Second}
		res, err := New(cfg, WithRunner(r)).Run(ctx)
		cancel()
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Errorf("%s: context error not surfaced: %v", c.name, err)
		}
		if got := exitLike(res, err); got != c.want {
			t.Errorf("%s: exit %d, want %d (report %+v)", c.name, got, c.want, res)
		}
	}
}

// Finding 1, real execution: `shell: exit 3` FAILS, then a session timeout
// cuts `shell: sleep 4` — the failure is kept (exit 1), the cut case is
// not counted as a failure of its own.
func TestOrchRun_TimeoutKeepsExecutedFailure(t *testing.T) {
	bank := "version: \"1.0\"\nname: to\ntest_cases:\n" +
		"  - id: T1\n    name: fails\n    platforms: [desktop]\n    steps:\n      - name: a\n        action: \"shell: exit 3\"\n" +
		"  - id: T2\n    name: slow\n    platforms: [desktop]\n    steps:\n      - name: a\n        action: \"shell: sleep 4\"\n" +
		"  - id: T3\n    name: never\n    platforms: [desktop]\n    steps:\n      - name: a\n        action: \"shell: true\"\n"
	cfg := &config.Config{Banks: []string{writeBank(t, bank)}, Platforms: []config.Platform{config.PlatformDesktop},
		OutputDir: t.TempDir(), Speed: config.SpeedFast, ReportFormat: config.ReportMarkdown,
		Timeout: time.Minute, StepTimeout: 30 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	res, err := New(cfg).Run(ctx)
	if err == nil {
		t.Fatal("timeout not surfaced")
	}
	if res == nil || res.Report == nil || res.Report.FailedChallenges != 1 || exitLike(res, err) != 1 {
		t.Fatalf("executed failure lost or cut case counted as failure: %+v err=%v", res.Report, err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel2()
	cut := "version: \"1.0\"\nname: to2\ntest_cases:\n" +
		"  - id: T2\n    name: slow\n    platforms: [desktop]\n    steps:\n      - name: a\n        action: \"shell: sleep 4\"\n"
	cfg.Banks = []string{writeBank(t, cut)}
	cfg.OutputDir = t.TempDir()
	res, err = New(cfg).Run(ctx2)
	if got := exitLike(res, err); got != 2 {
		t.Fatalf("a case cut by the timeout exits %d, want 2 (not a failure): %+v err=%v", got, res, err)
	}
}
