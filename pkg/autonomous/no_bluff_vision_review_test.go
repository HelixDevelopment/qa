// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// no_bluff_vision_review_test.go extends the no_bluff_vision gate with the
// findings of the independent review of 8627c0d/4ccbd03. Each test is a
// golden-good / golden-bad pair for one finding and is paired with a
// mutant in challenges/scripts/no_bluff_vision_mutation_challenge.sh.

package autonomous

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"strings"
	"testing"

	"digital.vasic.helixqa/pkg/llm"
	"digital.vasic.helixqa/pkg/testbank"
)

func noisePNGSized(t *testing.T, seed int64, w, h int) []byte {
	t.Helper()
	r := rand.New(rand.NewSource(seed))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(r.Intn(256)), uint8(r.Intn(256)), uint8(r.Intn(256)), 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Finding 4 (HIGH): recognize/forbid are matched against the TEXT:
// transcription section only — never against the model's own prose, the
// section headings, or a description that negates the token.
func TestNoBluffVision_RecognizeMatchesTranscriptionOnly(t *testing.T) {
	step := func(rec, forb []string) testbank.TestStep {
		return testbank.TestStep{Name: "s", Action: "keypress: X", Expected: "settings", Recognize: rec, Forbid: forb}
	}
	cases := []struct {
		name, reply   string
		rec, forb     []string
		pass, blocked bool
	}{
		{"negated in description is not on screen", "TEXT:\n(none)\nDESCRIPTION: The Settings screen is not shown.", []string{"Settings"}, nil, false, false},
		{"recognize found in transcription", "TEXT:\nSettings\nWi-Fi\nDESCRIPTION: A list; no error message is visible.", []string{"Settings"}, []string{"Error"}, true, false},
		{"same-line TEXT: content is prose, not a heading", "TEXT: Settings | Wi-Fi\nDESCRIPTION: list", []string{"Settings"}, nil, false, true},
		{"forbid in transcription fails", "TEXT:\nSettings\nError 500\nDESCRIPTION: banner", []string{"Settings"}, []string{"Error"}, false, false},
		{"heading words are not screen text", "TEXT:\n\nDESCRIPTION: blank list", []string{"Description"}, nil, false, false},
		{"no TEXT section cannot be judged", "The screen shows Settings and Wi-Fi.", []string{"Settings"}, nil, false, true},
	}
	for _, c := range cases {
		ste, _ := newSTE(&fakeVision{blind: c.reply}, noisePNG(t, 1), noisePNG(t, 2))
		r := run(ste, step(c.rec, c.forb))
		if r.Passed != c.pass || r.Blocked != c.blocked {
			t.Fatalf("%s: passed=%v blocked=%v (want %v/%v) actual=%q", c.name, r.Passed, r.Blocked, c.pass, c.blocked, r.Actual)
		}
	}
}

// Finding 5 (MEDIUM): expect_change alone proves only that SOMETHING
// changed (a crash dialog counts) — it is not a positive content check, so
// the step is labelled UNVERIFIED-by-vision, never VISION-VERIFIED.
func TestNoBluffVision_ExpectChangeAloneIsNotVerified(t *testing.T) {
	ste, _ := newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2))
	r := run(ste, testbank.TestStep{Name: "s", Action: "keypress: X", Expected: "x", ExpectChange: true})
	if !r.Passed || r.VisionStatus != VisionUnverified || !strings.Contains(r.Actual, "UNVERIFIED-by-vision") {
		t.Fatalf("expect_change-only step: passed=%v status=%q actual=%q", r.Passed, r.VisionStatus, r.Actual)
	}
	if strings.Contains(r.Actual, "VISION-VERIFIED") {
		t.Fatalf("expect_change-only step claims verification: %q", r.Actual)
	}
}

// Finding 5 (MEDIUM): frames of different dimensions are not comparable —
// "changed" cannot be decided, so the step is BLOCKED (never a claimed change).
func TestNoBluffVision_ExpectChangeIncomparableFramesBlocked(t *testing.T) {
	ste, _ := newSTE(&fakeVision{verify: "VERIFIED: yes"}, noisePNGSized(t, 1, 160, 120), noisePNGSized(t, 2, 200, 100))
	requireBlocked(t, run(ste, gated(func(s *testbank.TestStep) { s.ExpectChange = true })), "size mismatch")
}

// Finding 1 (CRITICAL): the answer must be exactly one standalone
// "VERIFIED: yes|no" line. An echoed prompt template ("VERIFIED: yes/no"),
// "UNVERIFIED: yes", prose that merely contains the phrase, or two answer
// lines are not an answer.
func TestNoBluffVision_VerifiedAnswerMustBeOneStandaloneLine(t *testing.T) {
	notAnAnswer := []string{
		"Respond with ONLY:\nVERIFIED: yes/no\nACTUAL: brief description of what's visible",
		"VERIFIED: yes/no",
		"UNVERIFIED: yes\nACTUAL: settings",
		"The expected state is VERIFIED: yes because the menu shows.",
		"VERIFIED: yes\nVERIFIED: yes\nACTUAL: duplicated",
		"VERIFIED: yes, mostly\nACTUAL: hedged",
	}
	for _, reply := range notAnAnswer {
		ste, _ := newSTE(&fakeVision{verify: reply}, noisePNG(t, 1), noisePNG(t, 2))
		requireBlocked(t, run(ste, gated(nil)), "reply "+reply)
	}
	for _, reply := range []string{
		"VERIFIED: yes\nACTUAL: settings",
		"  verified:  YES  \nACTUAL: settings",
		"ACTUAL: settings\nVERIFIED: yes.",
	} {
		ste, _ := newSTE(&fakeVision{verify: reply}, noisePNG(t, 1), noisePNG(t, 2))
		if r := run(ste, gated(nil)); !r.Passed || r.VisionStatus != VisionVerified {
			t.Fatalf("standalone yes line %q not accepted: %+v", reply, r)
		}
	}
	ste, _ := newSTE(&fakeVision{verify: "ACTUAL: home\nVERIFIED: no"}, noisePNG(t, 1), noisePNG(t, 2))
	if r := run(ste, gated(nil)); r.Passed || r.Blocked || r.VisionStatus != VisionRefuted {
		t.Fatalf("standalone no line not a FAIL: %+v", r)
	}
}

// Finding 6 (MEDIUM): an ambiguous or failing provider must not freeze
// every gated step at BLOCKED while a capable provider is configured — the
// next provider is asked; the operator's HELIX_VISION_ENDPOINT is asked
// first; and a DEFINITE answer is final (no shopping for a yes).
func TestNoBluffVision_ProviderFallThroughAndOrdering(t *testing.T) {
	// Ambiguous first provider -> the next one answers.
	a := &fakeVision{name: "astica", verify: "The screen shows a settings list with toggles."}
	b := &fakeVision{name: "qwen", verify: "VERIFIED: yes\nACTUAL: settings"}
	ste, _ := newSTE(llm.NewAdaptiveProvider(a, b), noisePNG(t, 1), noisePNG(t, 2))
	r := run(ste, gated(nil))
	if !r.Passed || r.VisionProvider != "qwen" {
		t.Fatalf("ambiguous first provider froze the step: passed=%v provider=%q actual=%q", r.Passed, r.VisionProvider, r.Actual)
	}

	// The operator endpoint is asked before registry providers.
	a = &fakeVision{name: "astica", verify: "VERIFIED: no"}
	ep := &fakeVision{name: llm.ProviderVisionEndpoint, verify: "VERIFIED: yes"}
	ste, _ = newSTE(llm.NewAdaptiveProvider(a, ep), noisePNG(t, 1), noisePNG(t, 2))
	r = run(ste, gated(nil))
	if !r.Passed || a.calls() != 0 || r.VisionProvider != llm.ProviderVisionEndpoint {
		t.Fatalf("endpoint not asked first: passed=%v astica-calls=%d provider=%q", r.Passed, a.calls(), r.VisionProvider)
	}

	// A definite "no" is final: the next provider is never asked.
	a = &fakeVision{name: "a", verify: "VERIFIED: no\nACTUAL: home"}
	b = &fakeVision{name: "b", verify: "VERIFIED: yes"}
	ste, _ = newSTE(llm.NewAdaptiveProvider(a, b), noisePNG(t, 1), noisePNG(t, 2))
	r = run(ste, gated(nil))
	if r.Passed || b.calls() != 0 || r.VisionStatus != VisionRefuted {
		t.Fatalf("definite no was shopped around: passed=%v b-calls=%d status=%q", r.Passed, b.calls(), r.VisionStatus)
	}

	// Blind transcription: a reply without a TEXT: section falls through too.
	a = &fakeVision{name: "a", blind: "A settings list."}
	b = &fakeVision{name: "b", blind: "TEXT:\nSettings\nDESCRIPTION: list"}
	ste, _ = newSTE(llm.NewAdaptiveProvider(a, b), noisePNG(t, 1), noisePNG(t, 2))
	r = run(ste, testbank.TestStep{Name: "s", Action: "keypress: X", Expected: "x", Recognize: []string{"Settings"}})
	if !r.Passed {
		t.Fatalf("blind reply without TEXT: did not fall through: %+v", r)
	}

	// Every provider unusable -> BLOCKED, naming each.
	a = &fakeVision{name: "a", verify: "maybe"}
	b = &fakeVision{name: "b", verifyErr: errTest("503")}
	ste, _ = newSTE(llm.NewAdaptiveProvider(a, b), noisePNG(t, 1), noisePNG(t, 2))
	r = run(ste, gated(nil))
	requireBlocked(t, r, "all providers unusable")
	if !strings.Contains(r.Actual, "a:") || !strings.Contains(r.Actual, "b:") {
		t.Fatalf("blocked reason does not name every provider tried: %q", r.Actual)
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

// Review round 2, finding 2 (HIGH): a vision-gated step that the executor
// SKIPS (prose description action, playwright without a CDP URL) asserted
// nothing — its case must be BLOCKED, never PASSED.
func TestNoBluffVision_SkippedGatedStepBlocksCase(t *testing.T) {
	t.Setenv("HELIXQA_PLAYWRIGHT_CDP_URL", "")
	gatedSkips := map[string]testbank.TestStep{
		"description + vision_verify": {Name: "look", Action: "Check that the settings screen is shown", Expected: "settings", VisionVerify: true},
		"playwright (no CDP) + recognize": {Name: "nav", Action: "playwright: navigate http://127.0.0.1:1/", Expected: "home", Recognize: []string{"Home"}},
	}
	for name, st := range gatedSkips {
		ste, _ := newSTE(&fakeVision{verify: "VERIFIED: yes", blind: "TEXT:\nHome\nDESCRIPTION: x"}, noisePNG(t, 1), noisePNG(t, 2))
		res := &StructuredExecutionResult{}
		tc := testbank.TestCase{ID: "TC-SKG", Name: "skipped gated", Steps: []testbank.TestStep{{Name: "ok", Action: "keypress: X", Expected: "x"}, st}}
		if err := ste.executeTestCase(context.Background(), tc, res); err != nil {
			t.Fatal(err)
		}
		if res.TestCasesPassed != 0 || res.TestCasesBlocked != 1 || res.ExitCode() != 2 {
			t.Errorf("%s: passed=%d blocked=%d skipped=%d exit=%d — a skipped gated step must BLOCK the case",
				name, res.TestCasesPassed, res.TestCasesBlocked, res.TestCasesSkipped, res.ExitCode())
		}
	}
	// Golden-good: an UNgated prose step is still an honest SKIP.
	ste, _ := newSTE(nilProvider, noisePNG(t, 1), noisePNG(t, 2))
	res := &StructuredExecutionResult{}
	tc := testbank.TestCase{ID: "TC-SKP", Name: "plain skip", Steps: []testbank.TestStep{{Name: "p", Action: "Just prose"}}}
	_ = ste.executeTestCase(context.Background(), tc, res)
	if res.TestCasesSkipped != 1 || res.TestCasesBlocked != 0 {
		t.Fatalf("ungated prose step: %+v", res)
	}
}

// Review round 2, finding 3 (HIGH): the TEXT: section must be exactly the
// block between a standalone TEXT: heading (the reply's first line) and a
// DESCRIPTION: heading. Any other heading-like line ends it and the reply is
// BLOCKED; filler such as "(none)" / "No text visible" is an empty
// transcription, never screen text.
func TestNoBluffVision_TranscriptionStructureStrict(t *testing.T) {
	cases := []struct {
		name, reply   string
		rec           []string
		pass, blocked bool
	}{
		{"markdown heading closes", "TEXT:\nSettings\n## Description\nno Login button is visible", []string{"Login"}, false, true},
		{"bold heading closes", "TEXT:\nSettings\n**Description**\nno Login button is visible", []string{"Login"}, false, true},
		{"dash heading closes", "TEXT:\nSettings\nDescription -\nno Login button is visible", []string{"Login"}, false, true},
		{"word-colon heading closes", "TEXT:\nSettings\nSummary:\nthe Login button is not visible", []string{"Login"}, false, true},
		{"extra section before DESCRIPTION", "TEXT:\nSettings\n## Notes\nno Login button is visible\nDESCRIPTION: settings list", []string{"Login"}, false, true},
		{"bold section before DESCRIPTION", "TEXT:\nSettings\n**Notes**\nthe Login link is missing\nDESCRIPTION: list", []string{"Login"}, false, true},
		{"prose line starting Text:", "Text: the Login button is not visible here.\nDESCRIPTION: blank", []string{"Login"}, false, true},
		{"TEXT not first", "Here you go.\nTEXT:\nLogin\nDESCRIPTION: form", []string{"Login"}, false, true},
		{"no DESCRIPTION heading", "TEXT:\nLogin\nPassword", []string{"Login"}, false, true},
		{"filler (none) is empty", "TEXT:\n(none)\nDESCRIPTION: blank", []string{"None"}, false, false},
		{"filler no text visible is empty", "TEXT:\nNo text visible\nDESCRIPTION: blank", []string{"No"}, false, false},
		{"well formed", "TEXT:\nLogin\nPassword: ****\nDESCRIPTION: a login form", []string{"Login", "Password"}, true, false},
		{"bold well formed", "**TEXT:**\nLogin\n**DESCRIPTION:** a form", []string{"Login"}, true, false},
	}
	for _, c := range cases {
		ste, _ := newSTE(&fakeVision{blind: c.reply}, noisePNG(t, 1), noisePNG(t, 2))
		r := run(ste, testbank.TestStep{Name: "s", Action: "keypress: X", Expected: "x", Recognize: c.rec})
		if r.Passed != c.pass || r.Blocked != c.blocked {
			t.Errorf("%s: passed=%v blocked=%v (want %v/%v) actual=%q", c.name, r.Passed, r.Blocked, c.pass, c.blocked, r.Actual)
		}
	}
}
