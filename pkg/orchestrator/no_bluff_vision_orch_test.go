// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
