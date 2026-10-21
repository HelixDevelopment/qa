// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"digital.vasic.challenges/pkg/challenge"
	"digital.vasic.challenges/pkg/logging"

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
// cuts `shell: sleep 6` — the failure is kept (exit 1), the cut case is
// not counted as a failure of its own.
func TestOrchRun_TimeoutKeepsExecutedFailure(t *testing.T) {
	bank := "version: \"1.0\"\nname: to\ntest_cases:\n" +
		"  - id: T1\n    name: fails\n    platforms: [desktop]\n    steps:\n      - name: a\n        action: \"shell: exit 3\"\n" +
		"  - id: T2\n    name: slow\n    platforms: [desktop]\n    steps:\n      - name: a\n        action: \"shell: sleep 6\"\n" +
		"  - id: T3\n    name: never\n    platforms: [desktop]\n    steps:\n      - name: a\n        action: \"shell: true\"\n"
	cfg := &config.Config{Banks: []string{writeBank(t, bank)}, Platforms: []config.Platform{config.PlatformDesktop},
		OutputDir: t.TempDir(), Speed: config.SpeedFast, ReportFormat: config.ReportMarkdown,
		Timeout: time.Minute, StepTimeout: 30 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	res, err := New(cfg).Run(ctx)
	if err == nil {
		t.Fatal("timeout not surfaced")
	}
	if res == nil || res.Report == nil || res.Report.FailedChallenges != 1 || exitLike(res, err) != 1 {
		t.Fatalf("executed failure lost or cut case counted as failure: %+v err=%v", res.Report, err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel2()
	cut := "version: \"1.0\"\nname: to2\ntest_cases:\n" +
		"  - id: T2\n    name: slow\n    platforms: [desktop]\n    steps:\n      - name: a\n        action: \"shell: sleep 6\"\n"
	cfg.Banks = []string{writeBank(t, cut)}
	cfg.OutputDir = t.TempDir()
	res, err = New(cfg).Run(ctx2)
	if got := exitLike(res, err); got != 2 {
		t.Fatalf("a case cut by the timeout exits %d, want 2 (not a failure): %+v err=%v", got, res, err)
	}
}

// orderRunner records the order challenges are run in.
type orderRunner struct {
	scriptRunner
	ids []string
}

func (r *orderRunner) Run(ctx context.Context, id challenge.ID, c *challenge.Config) (*challenge.Result, error) {
	r.ids = append(r.ids, string(id))
	return r.scriptRunner.Run(ctx, id, c)
}

// Round 7: challenges run in a deterministic (ID) order, so a timeout cuts
// the same challenge on every run instead of a random one.
func TestOrchRun_DeterministicChallengeOrder(t *testing.T) {
	bank := "version: \"1.0\"\nname: ord\ntest_cases:\n"
	for _, id := range []string{"C5", "C2", "C6", "C1", "C4", "C3"} {
		bank += "  - id: " + id + "\n    name: " + id + "\n    platforms: [desktop]\n    steps:\n      - name: a\n        action: \"shell: true\"\n"
	}
	for i := 0; i < 5; i++ {
		r := &orderRunner{}
		cfg := &config.Config{Banks: []string{writeBank(t, bank)}, Platforms: []config.Platform{config.PlatformDesktop},
			OutputDir: t.TempDir(), Speed: config.SpeedFast, ReportFormat: config.ReportMarkdown,
			Timeout: time.Minute, StepTimeout: 10 * time.Second}
		if _, err := New(cfg, WithRunner(r)).Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(r.ids, ","); got != "C1,C2,C3,C4,C5,C6" {
			t.Fatalf("run %d: challenge order %s, want C1..C6", i, got)
		}
	}
}

func statusOf(res *Result) map[string]string {
	out := map[string]string{}
	if res == nil || res.Report == nil {
		return out
	}
	for _, pr := range res.Report.PlatformResults {
		for _, cr := range pr.ChallengeResults {
			out[string(pr.Platform)+":"+string(cr.ChallengeID)] = string(cr.Status)
		}
	}
	return out
}

// Review round 7, finding 1 (HIGH): Ctrl-C (a CANCEL, not a deadline)
// during a REAL `shell:` step made the challenge FAILED (the interrupted
// result carried no assertion, so the runner's anti-bluff gate turned it
// into a failure). It is SKIPPED "interrupted"; a step that FAILED before
// the cancellation keeps its FAILED verdict — for cancel and deadline.
func TestOrchRun_RealShellInterrupted(t *testing.T) {
	slow := "version: \"1.0\"\nname: rs\ntest_cases:\n" +
		"  - id: R1\n    name: one\n    platforms: [desktop]\n    steps:\n      - name: a\n        action: \"shell: true\"\n" +
		"  - id: R2\n    name: slow\n    platforms: [desktop]\n    steps:\n      - name: a\n        action: \"shell: sleep 38; true\"\n" +
		"  - id: R3\n    name: three\n    platforms: [desktop]\n    steps:\n      - name: a\n        action: \"shell: true\"\n"
	failFirst := "version: \"1.0\"\nname: rf\ntest_cases:\n" +
		"  - id: F1\n    name: fails then slow\n    platforms: [desktop]\n    steps:\n      - name: a\n        action: \"shell: exit 3\"\n" +
		"      - name: b\n        action: \"shell: sleep 38; true\"\n"
	for _, c := range []struct {
		name, bank string
		deadline   bool
		want       int
		wantStatus map[string]string
	}{
		{"cancel during R2", slow, false, 2, map[string]string{"desktop:R1": "passed", "desktop:R2": "skipped", "desktop:R3": "skipped"}},
		{"deadline during R2", slow, true, 2, map[string]string{"desktop:R1": "passed", "desktop:R2": "skipped", "desktop:R3": "skipped"}},
		{"cancel after a failed step", failFirst, false, 1, map[string]string{"desktop:F1": "failed"}},
		{"deadline after a failed step", failFirst, true, 1, map[string]string{"desktop:F1": "failed"}},
	} {
		var ctx context.Context
		var cancel context.CancelFunc
		if c.deadline {
			ctx, cancel = context.WithTimeout(context.Background(), 1200*time.Millisecond)
		} else {
			ctx, cancel = context.WithCancel(context.Background())
			time.AfterFunc(1200*time.Millisecond, cancel)
		}
		cfg := &config.Config{Banks: []string{writeBank(t, c.bank)}, Platforms: []config.Platform{config.PlatformDesktop},
			OutputDir: t.TempDir(), Speed: config.SpeedFast, ReportFormat: config.ReportMarkdown,
			Timeout: time.Minute, StepTimeout: 60 * time.Second}
		start := time.Now()
		res, err := New(cfg).Run(ctx)
		cancel()
		if d := time.Since(start); d > 6*time.Second {
			t.Errorf("%s: run took %v after the cancellation", c.name, d)
		}
		if err == nil {
			t.Errorf("%s: cancellation not surfaced", c.name)
		}
		if got := exitLike(res, err); got != c.want {
			t.Errorf("%s: exit %d, want %d (%v)", c.name, got, c.want, statusOf(res))
		}
		st := statusOf(res)
		for k, v := range c.wantStatus {
			if st[k] != v {
				t.Errorf("%s: %s = %q, want %q (all: %v)", c.name, k, st[k], v, st)
			}
		}
		if c.want == 2 && res != nil && res.Report != nil {
			for _, pr := range res.Report.PlatformResults {
				for _, cr := range pr.ChallengeResults {
					if cr.ChallengeID == "R2" && !strings.Contains(cr.Error, "interrupted") {
						t.Errorf("%s: R2 not recorded as interrupted: %q", c.name, cr.Error)
					}
				}
			}
		}
	}
}

// Review round 7, finding 4: declared `dependencies:` order the run (ID only
// breaks ties); a dependency cycle is not run but reported (SKIPPED with
// the reason), never silently reordered.
func TestOrchRun_DependencyOrderAndCycle(t *testing.T) {
	bank := "version: \"1.0\"\nname: dep\ntest_cases:\n" +
		"  - id: a-check\n    name: a\n    platforms: [desktop]\n    dependencies: [z-setup]\n    steps:\n      - name: s\n        action: \"shell: true\"\n" +
		"  - id: z-setup\n    name: z\n    platforms: [desktop]\n    steps:\n      - name: s\n        action: \"shell: true\"\n" +
		"  - id: c-loop\n    name: c\n    platforms: [desktop]\n    dependencies: [d-loop]\n    steps:\n      - name: s\n        action: \"shell: true\"\n" +
		"  - id: d-loop\n    name: d\n    platforms: [desktop]\n    dependencies: [c-loop]\n    steps:\n      - name: s\n        action: \"shell: true\"\n"
	r := &orderRunner{}
	cfg := &config.Config{Banks: []string{writeBank(t, bank)}, Platforms: []config.Platform{config.PlatformDesktop},
		OutputDir: t.TempDir(), Speed: config.SpeedFast, ReportFormat: config.ReportMarkdown,
		Timeout: time.Minute, StepTimeout: 10 * time.Second}
	res, err := New(cfg, WithRunner(r)).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.ids, ","); got != "z-setup,a-check" {
		t.Fatalf("run order %s, want z-setup,a-check (cycle not run)", got)
	}
	st := statusOf(res)
	if st["desktop:c-loop"] != "skipped" || st["desktop:d-loop"] != "skipped" {
		t.Fatalf("cycle members not reported: %v", st)
	}
	found := false
	for _, pr := range res.Report.PlatformResults {
		for _, cr := range pr.ChallengeResults {
			if cr.ChallengeID == "c-loop" && strings.Contains(cr.Error, "dependency cycle") {
				found = true
			}
		}
	}
	if !found || res.ExitCode() != 2 {
		t.Fatalf("cycle reason missing or exit %d", res.ExitCode())
	}
}

// LOW: a cancelled run lists the challenges that never started (SKIPPED
// "not started"), and a cancellation during the LAST challenge is still
// surfaced.
func TestOrchRun_CancelledRunListsNotStarted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &scriptRunner{cancel: cancel, script: map[int]string{1: "cancel+pass"}}
	cfg := &config.Config{Banks: []string{writeBank(t, cancelBank)}, Platforms: []config.Platform{config.PlatformDesktop, config.PlatformWeb},
		OutputDir: t.TempDir(), Speed: config.SpeedFast, ReportFormat: config.ReportMarkdown,
		Timeout: time.Minute, StepTimeout: 10 * time.Second}
	res, err := New(cfg, WithRunner(r)).Run(ctx)
	cancel()
	st := statusOf(res)
	for _, k := range []string{"desktop:O2", "desktop:O3", "web:O1", "web:O2", "web:O3"} {
		if st[k] != "skipped" {
			t.Errorf("%s = %q, want a SKIPPED not-started record (all %v)", k, st[k], st)
		}
	}
	if err == nil || exitLike(res, err) != 2 {
		t.Fatalf("err=%v exit=%d", err, exitLike(res, err))
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	r2 := &scriptRunner{cancel: cancel2, script: map[int]string{3: "cancel+pass"}}
	cfg.Platforms = []config.Platform{config.PlatformDesktop}
	cfg.OutputDir = t.TempDir()
	cfg.Banks = []string{writeBank(t, cancelBank)}
	_, err = New(cfg, WithRunner(r2)).Run(ctx2)
	cancel2()
	if err == nil {
		t.Fatal("a cancellation during the last challenge was not surfaced")
	}
}

// Review round 8, finding 1: the interruption must be STRUCTURAL — a step's
// OUTPUT that happens to contain the marker text (or the skip sentinel) is
// data, never a verdict.
func TestOrchRun_MarkerTextInStepOutputIsData(t *testing.T) {
	bank := "version: \"1.0\"\nname: mk\ntest_cases:\n" +
		"  - id: M1\n    name: m\n    platforms: [desktop]\n    steps:\n" +
		"      - name: a\n        action: \"shell: echo interrupted-by-run-cancellation; exit 3\"\n" +
		"      - name: b\n        action: \"shell: sleep 38.9; true\"\n"
	for _, deadline := range []bool{false, true} {
		var ctx context.Context
		var cancel context.CancelFunc
		if deadline {
			ctx, cancel = context.WithTimeout(context.Background(), 1200*time.Millisecond)
		} else {
			ctx, cancel = context.WithCancel(context.Background())
			time.AfterFunc(1200*time.Millisecond, cancel)
		}
		cfg := &config.Config{Banks: []string{writeBank(t, bank)}, Platforms: []config.Platform{config.PlatformDesktop},
			OutputDir: t.TempDir(), Speed: config.SpeedFast, ReportFormat: config.ReportMarkdown,
			Timeout: time.Minute, StepTimeout: 60 * time.Second}
		res, err := New(cfg).Run(ctx)
		cancel()
		if got := exitLike(res, err); got != 1 || statusOf(res)["desktop:M1"] != "failed" {
			t.Errorf("deadline=%v: marker text in a FAILED step's output: exit %d, %v — want FAILED, exit 1", deadline, got, statusOf(res))
		}
	}
	// Unit level: only the wrapper's own entry counts.
	out := &challenge.Result{RecordedActions: []string{`shell-step[0]: exit=3 output="interrupted-by-run-cancellation\n"`,
		`shell-step[0]: echo skip-reason: interrupted-by-run-cancellation`}}
	if hasInterruptedMarker(out) || hasSkipSentinel(out) {
		t.Fatal("marker / sentinel text inside recorded step output was read as the wrapper's entry")
	}
	// A passing step that prints the skip sentinel stays PASSED.
	d := &definitionChallenge{def: &challenge.Definition{ID: "SK", Name: "sk"}, cfg: &challenge.Config{Timeout: 30 * time.Second}}
	r := d.executeDesktopShellSteps(context.Background(), time.Now(), []testbank.TestStep{{Action: "shell: echo 'skip-reason: fake'"}})
	restoreSkippedFromDefinitionWrapper(r)
	if r.Status != challenge.StatusPassed {
		t.Fatalf("a step printing the skip sentinel was restored to %s", r.Status)
	}
}

// Review round 8, finding 2: steps relying on the default 2xx expectation
// are listed at load, executed and `_skip`ped steps separately.
func TestDefaultStatusHTTPSteps_Listed(t *testing.T) {
	cases := []testbank.TestCase{{ID: "A", Steps: []testbank.TestStep{
		{Name: "h", Action: "http: GET /x"},
		{Name: "s", Action: "http: GET /y", ExpectStatus: 200},
		{Name: "k", Action: "http: GET /z", Skip: true},
		{Name: "c", Action: "shell: true"},
	}}}
	active, skipped := testbank.DefaultStatusHTTPSteps(cases)
	if strings.Join(active, "|") != `A step 0 "h" (http: GET /x)` || strings.Join(skipped, "|") != `A step 2 "k" (http: GET /z)` {
		t.Fatalf("active %q skipped %q", active, skipped)
	}
}

// recLogger records every log line with its level.
type recLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *recLogger) add(level, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, level+" "+msg)
}
func (l *recLogger) Info(msg string, _ ...logging.Field)        { l.add("INFO", msg) }
func (l *recLogger) Warn(msg string, _ ...logging.Field)        { l.add("WARN", msg) }
func (l *recLogger) Error(msg string, _ ...logging.Field)       { l.add("ERROR", msg) }
func (l *recLogger) Debug(msg string, _ ...logging.Field)       { l.add("DEBUG", msg) }
func (l *recLogger) WithFields(...logging.Field) logging.Logger { return l }
func (l *recLogger) LogAPIRequest(logging.APIRequestLog)        {}
func (l *recLogger) LogAPIResponse(logging.APIResponseLog)      {}
func (l *recLogger) Close() error                               { return nil }
func (l *recLogger) find(level, sub string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, x := range l.lines {
		if strings.HasPrefix(x, level+" ") && strings.Contains(x, sub) {
			return true
		}
	}
	return false
}

// Final cleanup (review round 9, items 4, 6, 7): warnings are logged at
// WARN level; "0 in a dependency cycle" is not printed; a case ID defined
// in two (non-peer) bank files is reported naming both files.
func TestOrchRun_LoadWarnings(t *testing.T) {
	dir := t.TempDir()
	b1 := "version: \"1.0\"\nname: one\ntest_cases:\n  - id: DUP-1\n    name: a\n    platforms: [desktop]\n    steps:\n" +
		"      - name: h\n        action: \"http: GET /x\"\n"
	b2 := "version: \"1.0\"\nname: two\ntest_cases:\n  - id: DUP-1\n    name: b\n    platforms: [desktop]\n    steps:\n" +
		"      - name: s\n        action: \"shell: true\"\n"
	p1, p2 := filepath.Join(dir, "one.yaml"), filepath.Join(dir, "two.yaml")
	if err := os.WriteFile(p1, []byte(b1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p2, []byte(b2), 0o600); err != nil {
		t.Fatal(err)
	}
	lg := &recLogger{}
	cfg := &config.Config{Banks: []string{p1, p2}, Platforms: []config.Platform{config.PlatformDesktop},
		OutputDir: t.TempDir(), Speed: config.SpeedFast, ReportFormat: config.ReportMarkdown,
		Timeout: time.Minute, StepTimeout: 10 * time.Second}
	if _, err := New(cfg, WithLogger(lg)).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !lg.find("WARN", "expects a 2xx by default") || lg.find("INFO", "WARNING:") {
		t.Errorf("default-2xx warning not at WARN level: %v", lg.lines)
	}
	if !lg.find("WARN", "DUP-1") || !lg.find("WARN", "one.yaml") || !lg.find("WARN", "two.yaml") {
		t.Errorf("duplicate case ID across bank files not reported with both files: %v", lg.lines)
	}
	if lg.find("INFO", "0 in a dependency cycle") {
		t.Errorf("'0 in a dependency cycle' printed: %v", lg.lines)
	}
}

// Final cleanup (review round 9, item 8): a command that EXITED normally is
// scored on its own outcome even when the run's context ended meanwhile.
func TestShellStep_NormalExitAfterCancelIsJudged(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "escaped.pid")
	t.Cleanup(func() {
		if b, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 1 {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	d := &definitionChallenge{def: &challenge.Definition{ID: "NX", Name: "nx"}, cfg: &challenge.Config{Timeout: 30 * time.Second}}
	for exit, want := range map[string]string{"0": challenge.StatusPassed, "3": challenge.StatusFailed} {
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(300*time.Millisecond, cancel)
		// sh exits at once (normally); a detached child keeps the output
		// pipe open until the cancellation + WaitDelay release it.
		step := testbank.TestStep{Action: "shell: setsid sh -c 'echo $$ > " + pidFile + "; exec sleep 3' & exit " + exit}
		r := d.executeDesktopShellSteps(ctx, time.Now(), []testbank.TestStep{step})
		cancel()
		if r.Status != want {
			t.Errorf("exit %s with the run's context ended after it: %s, want %s", exit, r.Status, want)
		}
	}
}

// The YAML / JSON peers of one bank are not "duplicate case IDs".
func TestOrchRun_BankPeers(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"/x/a.yaml", "/x/a.json", true}, {"/x/a.yaml", "/y/a.json", false}, {"/x/a.yaml", "/x/b.yaml", false}} {
		if got := bankPeers(c.a, c.b); got != c.want {
			t.Errorf("bankPeers(%s, %s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
