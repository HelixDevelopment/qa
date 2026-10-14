// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// no_bluff_vision_review_test.go extends the no_bluff_vision gate with the
// findings of the independent review of 8627c0d/4ccbd03. Each test is a
// golden-good / golden-bad pair for one finding and is paired with a
// mutant in challenges/scripts/no_bluff_vision_mutation_challenge.sh.

package autonomous

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"strings"
	"testing"

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
		{"same-line transcription", "TEXT: Settings | Wi-Fi\nDESCRIPTION: list", []string{"Settings"}, nil, true, false},
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
