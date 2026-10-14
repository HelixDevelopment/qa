// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package testbank

import (
	"strings"
	"testing"
)

// Review round 6, finding 9: combining marks are part of the word. In
// Devanagari / Thai a vowel sign changes the word ("काल" is not "कल"); the
// optional Arabic harakat and Hebrew niqqud are dropped so a vowel-marked
// error banner still matches a plain forbid token.
func TestVisionFields_R7_CombiningMarks(t *testing.T) {
	differ := [][2]string{{"काल", "कल"}, {"कील", "कल"}, {"ปิด", "ปด"}, {"बाद", "बंद"}, {"समय", "सामय"}}
	for _, p := range differ {
		if a, b := VisionTokenKey(p[0]), VisionTokenKey(p[1]); a == b {
			t.Errorf("%q and %q fold to the same key %q — a vowel sign was dropped", p[0], p[1], a)
		}
	}
	for _, w := range []string{"काल", "ปิด", "बंद"} {
		if k := VisionTokenKey(w); strings.Contains(k, " ") {
			t.Errorf("VisionTokenKey(%q) = %q — a combining mark split the word", w, k)
		}
	}
	same := [][2]string{{"كَتَبَ", "كتب"}, {"خَطَأ", "خطأ"}, {"שָׁלוֹם", "שלום"}, {"שְׁגִיאָה", "שגיאה"}, {"Café", "Café"}}
	for _, p := range same {
		if a, b := VisionTokenKey(p[0]), VisionTokenKey(p[1]); a != b {
			t.Errorf("%q -> %q, %q -> %q — want the same key", p[0], a, p[1], b)
		}
	}
}

// Review round 6, finding 5: forbid is negative evidence whose absence is
// only credible when the same reading found a positive anchor — so forbid
// needs recognize.
func TestVisionFields_R7_ForbidNeedsRecognize(t *testing.T) {
	for name, st := range map[string]TestStep{
		"forbid + golden":        {Action: "screenshot", Golden: "home", Forbid: []string{"Error"}},
		"forbid + vision_verify": {Action: "screenshot", VisionVerify: true, Forbid: []string{"Error"}},
	} {
		tc := TestCase{ID: "TC-F", Name: "f", Steps: []TestStep{st}}
		if msg := tc.IsValid(); !strings.Contains(msg, "forbid") || !strings.Contains(msg, "recognize") {
			t.Errorf("%s accepted: %q", name, msg)
		}
	}
	ok := TestCase{ID: "TC-G", Name: "g", Steps: []TestStep{{Action: "screenshot", Recognize: []string{"Home"}, Forbid: []string{"Error"}}}}
	if msg := ok.IsValid(); msg != "" {
		t.Fatalf("forbid with recognize refused: %s", msg)
	}
}
