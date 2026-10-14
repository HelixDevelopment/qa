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

func blindRun(t *testing.T, reply string, st testbank.TestStep) TestStepResult {
	t.Helper()
	ste, _ := newSTE(&fakeVision{blind: reply}, noisePNG(t, 1), noisePNG(t, 2))
	return run(ste, st)
}

// Review round 4, findings 4 + 5 (R13, R13b, R14): structural limits of
// the strict JSON reply — every violation makes the reply unusable.
func TestNoBluffVision_BlindReplyLimits(t *testing.T) {
	st := recStep([]string{"Login"}, nil)
	many := make([]string, 0, maxBlindTexts+1)
	for i := 0; i <= maxBlindTexts; i++ {
		many = append(many, `"x"`)
	}
	unusable := map[string]string{
		"prose after closing fence (R13)": "```json\n{\"texts\":[\"Login\"],\"description\":\"x\"}\n```\nNote: Login is shown",
		"other fence language (R13b)":     "```text\n{\"texts\":[\"Login\"],\"description\":\"x\"}\n```",
		"too many elements (R14)":         `{"texts":["Login",` + strings.Join(many, ",") + `],"description":"x"}`,
		"element over length cap":         `{"texts":["Login ` + strings.Repeat("a", maxBlindTextLen) + `"],"description":"x"}`,
		"multi-line element":              `{"texts":["Settings\nLogin"],"description":"x"}`,
		"control char in element":         `{"texts":["Log\u0007in"],"description":"x"}`,
		"invalid UTF-8":                   "{\"texts\":[\"Login\xff\"],\"description\":\"x\"}",
	}
	for name, reply := range unusable {
		if r := blindRun(t, reply, st); r.Passed || !r.Blocked {
			t.Errorf("%s: passed=%v blocked=%v — must be unusable", name, r.Passed, r.Blocked)
		}
	}
	// Golden-good at the limits (recognize_contains: the token is part of
	// the longer element).
	st.RecognizeContains = true
	if r := blindRun(t, `{"texts":["Login `+strings.Repeat("a", maxBlindTextLen-6)+`"],"description":"x"}`, st); !r.Passed {
		t.Errorf("element at the length cap rejected: %q", r.Actual)
	}
}

// Pins R06: the LEFT word boundary — a token at the end of a longer word
// is not a match.
func TestNoBluffVision_LeftWordBoundary(t *testing.T) {
	missing, forbidden := matchTokensInTexts([]string{"Relogin", "SuperError"}, []string{"Login"}, []string{"Error"}, false)
	if len(missing) != 1 || len(forbidden) != 0 {
		t.Fatalf("suffix matched as a word: missing=%v forbidden=%v", missing, forbidden)
	}
}

// Review round 4, finding 4: optional exact-element mode — with
// recognize_exact a token must equal a whole texts element, so commentary
// such as "(no Login button visible)" cannot satisfy recognize [Login].
func TestNoBluffVision_RecognizeExactElement(t *testing.T) {
	exact := testbank.TestStep{Name: "s", Action: "keypress: X", Expected: "x", Recognize: []string{"Login"}, RecognizeExact: true}
	// Round 7: a token found only INSIDE a longer element is BLOCKED (with a
	// pointer to recognize_contains), never a pass.
	if r := blindRun(t, `{"texts":["Settings","(no Login button visible)"],"description":"x"}`, exact); r.Passed || !r.Blocked {
		t.Fatalf("exact mode matched commentary: %+v", r.Actual)
	}
	if r := blindRun(t, `{"texts":["Settings","LOGIN"],"description":"x"}`, exact); !r.Passed {
		t.Fatalf("exact mode missed a whole element: %q", r.Actual)
	}
	// Exact is now the default (round 5, finding 10): the same commentary
	// does not match a plain recognize either.
	if r := blindRun(t, `{"texts":["Settings","(no Login button visible)"],"description":"x"}`, recStep([]string{"Login"}, nil)); r.Passed {
		t.Fatalf("default recognize matched commentary: %q", r.Actual)
	}
	// With the recognize_contains opt-in the commentary matches
	// (documented residual of that mode).
	contains := recStep([]string{"Login"}, nil)
	contains.RecognizeContains = true
	if r := blindRun(t, `{"texts":["Settings","(no Login button visible)"],"description":"x"}`, contains); !r.Passed {
		t.Fatalf("recognize_contains residual changed: %q", r.Actual)
	}
}

// Review round 4, finding 7: zero-width characters and compatibility forms
// cannot hide a forbid token (or fake a recognize token).
func TestNoBluffVision_UnicodeFolding(t *testing.T) {
	cases := []struct {
		texts []string
		forb  []string
		hit   int
	}{
		{[]string{"Er\u200bror 500"}, []string{"Error"}, 1}, // zero-width space split
		{[]string{"Er\u200dror"}, []string{"Error"}, 1},     // zero-width joiner
		{[]string{"\ufeffError"}, []string{"Error"}, 1},     // BOM
		{[]string{"Ｅｒｒｏｒ"}, []string{"Error"}, 1},           // fullwidth
		{[]string{"𝐄𝐫𝐫𝐨𝐫"}, []string{"Error"}, 1},           // math bold
		{[]string{"ﬁle"}, []string{"file"}, 1},              // ligature
		{[]string{"Errors"}, []string{"Error"}, 0},          // still whole-word
	}
	for i, c := range cases {
		_, f := matchTokensInTexts(c.texts, nil, c.forb, false)
		if len(f) != c.hit {
			t.Errorf("case %d %q: forbidden=%v, want %d hit(s)", i, c.texts, f, c.hit)
		}
	}
}
