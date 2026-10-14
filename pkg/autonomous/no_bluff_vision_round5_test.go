// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"context"
	"strings"
	"testing"

	"digital.vasic.helixqa/pkg/testbank"
)

func runCase(t *testing.T, ste *StructuredTestExecutor, steps ...testbank.TestStep) *StructuredExecutionResult {
	t.Helper()
	res := &StructuredExecutionResult{}
	tc := testbank.TestCase{ID: "TC-R5", Name: "r5", Steps: steps}
	if err := ste.executeTestCase(context.Background(), tc, res); err != nil {
		t.Fatal(err)
	}
	return res
}

func requireEnvBlocked(t *testing.T, res *StructuredExecutionResult, why string) {
	t.Helper()
	if res.TestCasesBlocked != 1 || res.TestCasesFailed != 0 || res.TestCasesPassed != 0 {
		t.Fatalf("%s: passed=%d failed=%d blocked=%d — want BLOCKED", why, res.TestCasesPassed, res.TestCasesFailed, res.TestCasesBlocked)
	}
	if len(res.Findings) != 1 || res.Findings[0].Category.IsAppDefect() {
		t.Fatalf("%s: finding must be environment, got %+v", why, res.Findings)
	}
}

// Review round 4, finding 1: a frame_diff whose screenshot capture fails,
// and an Android-only action on a non-Android executor, are environment
// problems — BLOCKED, never a FAILED app-defect finding.
func TestNoBluffVision_CaptureAndExecutorKindFailuresBlock(t *testing.T) {
	// executeStep takes frame 0 (before); frame_diff takes 1 and 2.
	for _, failAt := range []int{1, 2} {
		ste, ex := newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2), noisePNG(t, 3), noisePNG(t, 4))
		ex.errAt = map[int]error{failAt: errTest("screencap failed")}
		requireEnvBlocked(t, runCase(t, ste, testbank.TestStep{Name: "motion", Action: "frame_diff: 10"}), "frame_diff capture failure")
	}
	for _, action := range []string{"playback_check: *", "adb_shell: input keyevent 66"} {
		ste, _ := newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2))
		requireEnvBlocked(t, runCase(t, ste, testbank.TestStep{Name: "android-only", Action: action}), action+" without an Android executor")
	}
}

// Review round 4, finding 2: with no executor, host-only steps (shell,
// sleep, http, assert) still run and decide on their own; only steps that
// need the device (UI actions, vision-gated steps) are BLOCKED.
func TestNoBluffVision_HostOnlyStepsRunWithoutExecutor(t *testing.T) {
	ste := func() *StructuredTestExecutor {
		return &StructuredTestExecutor{config: PipelineConfig{Platforms: []string{"android"}}, execFactory: failingFactory{}}
	}
	if res := runCase(t, ste(), testbank.TestStep{Name: "cli", Action: "shell: exit 1"}); res.TestCasesFailed != 1 || res.TestCasesBlocked != 0 {
		t.Fatalf("shell exit 1 without executor: failed=%d blocked=%d — a decidable failure was hidden", res.TestCasesFailed, res.TestCasesBlocked)
	}
	if res := runCase(t, ste(), testbank.TestStep{Name: "cli", Action: "shell: exit 0"}, testbank.TestStep{Name: "wait", Action: "sleep: 1"}); res.TestCasesPassed != 1 {
		t.Fatalf("host-only passing case without executor: %+v", res)
	}
	if res := runCase(t, ste(), testbank.TestStep{Name: "ui", Action: "keypress: X"}); res.TestCasesBlocked != 1 {
		t.Fatalf("UI step without executor must be BLOCKED: %+v", res)
	}
	res := runCase(t, ste(), testbank.TestStep{Name: "look", Action: "sleep: 1", VisionVerify: true})
	if res.TestCasesBlocked != 1 {
		t.Fatalf("gated host-only step without executor must be BLOCKED (no frame): %+v", res)
	}
	// The BLOCKED reason names the real cause (no executor), not a
	// downstream symptom ("frame not captured").
	if len(res.Cases) != 1 || len(res.Cases[0].Steps) != 1 ||
		!strings.Contains(res.Cases[0].Steps[0].Actual, "executor could not be created") {
		t.Fatalf("gated step's BLOCKED reason does not name the missing executor: %+v", res.Cases)
	}
}
