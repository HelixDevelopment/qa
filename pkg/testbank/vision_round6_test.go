// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package testbank

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Review round 5, finding 6: invisible code points outside Unicode Cf —
// Other_Default_Ignorable_Code_Point (CGJ U+034F, Hangul fillers U+115F /
// U+1160 / U+3164 / U+FFA0, Khmer U+17B4 / U+17B5) and Variation_Selector
// (U+FE00..U+FE0F, U+E0100..U+E01EF, Mongolian U+180B..) — are dropped by
// the token fold, so none can split a forbid token or disguise a
// recognize token.
func TestVisionFields_R6_InvisibleCodePointsFolded(t *testing.T) {
	for _, in := range []string{
		"Log͏in", "Log️in", "Log\U000E0100in", "Logㅤin", "Logﾠin",
		"Logᅟin", "Logᅠin", "Log឴in", "Log឵in", "Log᠋in", "Log︀in",
	} {
		if got := VisionTokenKey(in); got != "login" {
			t.Errorf("VisionTokenKey(%+q) = %q, want %q", in, got, "login")
		}
	}
	// Visible letters and marks that change the word are kept.
	for in, want := range map[string]string{"Logiń": "logiń", "Log in": "log in", "Lоgin": "lоgin"} {
		if got := VisionTokenKey(in); got != want {
			t.Errorf("VisionTokenKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// Review round 5, finding 10: recognize matches whole elements by default;
// recognize_contains opts into contained-words matching. It needs
// recognize and contradicts recognize_exact.
func TestVisionFields_R6_RecognizeContains(t *testing.T) {
	var st TestStep
	if err := yaml.Unmarshal([]byte("action: screenshot\nrecognize: [Login]\nrecognize_contains: true\n"), &st); err != nil {
		t.Fatal(err)
	}
	if !st.RecognizeContains || st.ExactRecognize() {
		t.Fatalf("recognize_contains not loaded: %+v", st)
	}
	if !(TestStep{Recognize: []string{"Login"}}).ExactRecognize() {
		t.Fatal("recognize is not exact by default")
	}
	for name, step := range map[string]TestStep{
		"contains without recognize": {Action: "screenshot", VisionVerify: true, RecognizeContains: true},
		"contains and exact":         {Action: "screenshot", Recognize: []string{"Login"}, RecognizeContains: true, RecognizeExact: true},
	} {
		tc := TestCase{ID: "TC-C", Name: "c", Steps: []TestStep{step}}
		if msg := tc.IsValid(); !strings.Contains(msg, "recognize_contains") {
			t.Errorf("%s accepted: %q", name, msg)
		}
	}
	ok := TestCase{ID: "TC-D", Name: "d", Steps: []TestStep{{Action: "screenshot", Recognize: []string{"Login"}, RecognizeContains: true}}}
	if msg := ok.IsValid(); msg != "" {
		t.Fatalf("valid recognize_contains refused: %s", msg)
	}
}
