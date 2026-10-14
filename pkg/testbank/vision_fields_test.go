// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package testbank

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestVisionFields_ParseFromYAML proves the vision-gate keys
// (vision_verify, golden, phash_max_distance, recognize, forbid,
// expect_change) round-trip from a real YAML bank into TestStep — a key
// the schema silently dropped would make a gated step run ungated.
func TestVisionFields_ParseFromYAML(t *testing.T) {
	src := `
name: open login
action: "keypress: KEYCODE_ENTER"
expected: login screen visible
vision_verify: true
golden: login-screen
phash_max_distance: 6
recognize: ["Sign in", "Password"]
forbid: ["Error"]
expect_change: true
`
	var st TestStep
	if err := yaml.Unmarshal([]byte(src), &st); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !st.VisionVerify {
		t.Fatalf("vision_verify not parsed")
	}
	if st.Golden != "login-screen" {
		t.Fatalf("golden = %q", st.Golden)
	}
	if st.PHashMaxDistance == nil || *st.PHashMaxDistance != 6 {
		t.Fatalf("phash_max_distance not parsed: %v", st.PHashMaxDistance)
	}
	if len(st.Recognize) != 2 || st.Recognize[0] != "Sign in" {
		t.Fatalf("recognize = %v", st.Recognize)
	}
	if len(st.Forbid) != 1 || st.Forbid[0] != "Error" {
		t.Fatalf("forbid = %v", st.Forbid)
	}
	if !st.ExpectChange {
		t.Fatalf("expect_change not parsed")
	}
	if got := st.EffectivePHashMaxDistance(); got != 6 {
		t.Fatalf("EffectivePHashMaxDistance = %d, want 6", got)
	}
}

// TestVisionFields_Gating pins which keys make a step vision-gated and
// which of them need a vision MODEL (as opposed to a deterministic frame
// oracle). Golden-good: each key gates. Golden-bad: a plain step does not.
func TestVisionFields_Gating(t *testing.T) {
	d := 3
	cases := []struct {
		name       string
		st         TestStep
		gated      bool
		needsModel bool
	}{
		{"plain step", TestStep{Action: "keypress: X"}, false, false},
		{"vision_verify", TestStep{VisionVerify: true}, true, true},
		{"recognize", TestStep{Recognize: []string{"ok"}}, true, true},
		{"forbid", TestStep{Forbid: []string{"error"}}, true, true},
		{"golden only", TestStep{Golden: "g", PHashMaxDistance: &d}, true, false},
		{"expect_change only", TestStep{ExpectChange: true}, true, false},
	}
	for _, c := range cases {
		if got := c.st.IsVisionGated(); got != c.gated {
			t.Errorf("%s: IsVisionGated = %v, want %v", c.name, got, c.gated)
		}
		if got := c.st.NeedsVisionModel(); got != c.needsModel {
			t.Errorf("%s: NeedsVisionModel = %v, want %v", c.name, got, c.needsModel)
		}
	}
	if got := (TestStep{Golden: "g"}).EffectivePHashMaxDistance(); got != DefaultPHashMaxDistance {
		t.Errorf("default phash distance = %d, want %d", got, DefaultPHashMaxDistance)
	}
}

// TestVisionFields_LoadTimeRejectsVacuousAssertions: every vision key
// that could pass vacuously or contradict itself is refused at load, so a
// bank can never carry a check that silently asserts nothing.
func TestVisionFields_LoadTimeRejectsVacuousAssertions(t *testing.T) {
	neg := -1
	big := 65
	ok := 4
	bad := []struct {
		name string
		st   TestStep
		want string
	}{
		{"phash without golden", TestStep{Action: "sleep: 1", PHashMaxDistance: &ok}, "phash_max_distance"},
		{"phash negative", TestStep{Action: "sleep: 1", Golden: "g", PHashMaxDistance: &neg}, "0..16"},
		{"phash too big", TestStep{Action: "sleep: 1", Golden: "g", PHashMaxDistance: &big}, "0..16"},
		{"empty recognize token", TestStep{Action: "sleep: 1", Recognize: []string{"ok", "  "}}, "recognize"},
		{"empty forbid token", TestStep{Action: "sleep: 1", Forbid: []string{""}}, "forbid"},
		{"punctuation-only token", TestStep{Action: "sleep: 1", Recognize: []string{"!!"}}, "recognize"},
		{"token both recognized and forbidden", TestStep{Action: "sleep: 1", Recognize: []string{"Sign In"}, Forbid: []string{"sign in"}}, "both"},
		{"golden with path traversal", TestStep{Action: "sleep: 1", Golden: "../etc/passwd"}, "golden"},
	}
	for _, c := range bad {
		tc := TestCase{ID: "TC-V", Name: "v", Steps: []TestStep{c.st}}
		msg := tc.IsValid()
		if msg == "" {
			t.Errorf("%s: IsValid accepted a vacuous/contradictory vision assertion", c.name)
			continue
		}
		if !strings.Contains(msg, c.want) {
			t.Errorf("%s: IsValid = %q, want it to mention %q", c.name, msg, c.want)
		}
	}
	good := TestCase{ID: "TC-G", Name: "g", Steps: []TestStep{{
		Action: "keypress: X", VisionVerify: true, Golden: "home", PHashMaxDistance: &ok,
		Recognize: []string{"Home"}, Forbid: []string{"Error"}, ExpectChange: true,
	}}}
	if msg := good.IsValid(); msg != "" {
		t.Fatalf("valid vision step rejected: %s", msg)
	}
}

// TestVisionFields_ReviewLoadRules pins the load-time rules added after the
// independent review: findings 4 (heading-word tokens), 5 (pHash cap,
// expect_change / forbid need a positive check) and 9 (vision keys only on
// screen actions).
func TestVisionFields_ReviewLoadRules(t *testing.T) {
	d16, d17 := 16, 17
	bad := []struct {
		name string
		st   TestStep
		want string
	}{
		{"phash 17 above cap", TestStep{Action: "screenshot", Golden: "g", PHashMaxDistance: &d17}, "0..16"},
		{"recognize heading word Text", TestStep{Action: "screenshot", VisionVerify: true, Recognize: []string{"Text"}}, "heading"},
		{"forbid heading word Description", TestStep{Action: "screenshot", VisionVerify: true, Forbid: []string{"description:"}}, "heading"},
		{"expect_change alone", TestStep{Action: "keypress: X", ExpectChange: true}, "positive"},
		{"forbid alone", TestStep{Action: "screenshot", Forbid: []string{"Error"}}, "positive"},
		{"vision_verify on shell", TestStep{Action: "shell: true", VisionVerify: true}, "screen"},
		{"recognize on http", TestStep{Action: "http: GET /x", ExpectStatus: 200, Recognize: []string{"ok"}}, "screen"},
		{"golden on assert", TestStep{Action: "assert: status_eq: 200", Golden: "g"}, "screen"},
		{"vision_verify on prose description", TestStep{Action: "Check the settings screen", VisionVerify: true}, "not executable"},
		{"recognize on CONVERT placeholder", TestStep{Action: "# CONVERT: Convert to executable", VisionVerify: true, Recognize: []string{"x"}}, "not executable"},
	}
	for _, c := range bad {
		tc := TestCase{ID: "TC-R", Name: "r", Steps: []TestStep{c.st}}
		msg := tc.IsValid()
		if msg == "" || !strings.Contains(msg, c.want) {
			t.Errorf("%s: IsValid = %q, want a refusal mentioning %q", c.name, msg, c.want)
		}
	}
	good := []TestStep{
		{Action: "screenshot", Golden: "g", PHashMaxDistance: &d16},
		{Action: "keypress: X", ExpectChange: true, VisionVerify: true},
		{Action: "keypress: X", ExpectChange: true, Golden: "g"},
		{Action: "screenshot", Forbid: []string{"Error"}, Recognize: []string{"Settings"}},
		{Action: "sleep: 100", VisionVerify: true},
	}
	for i, st := range good {
		tc := TestCase{ID: "TC-G", Name: "g", Steps: []TestStep{st}}
		if msg := tc.IsValid(); msg != "" {
			t.Errorf("good[%d] refused: %s", i, msg)
		}
	}
	for _, k := range []ActionType{ActionTypeShell, ActionTypeHTTP, ActionTypeAssert} {
		if !k.IsNonScreen() {
			t.Errorf("%s must be a non-screen action", k)
		}
	}
	for _, k := range []ActionType{ActionTypeScreenshot, ActionTypeSleep, ActionTypeKeyPress, ActionTypeTap} {
		if k.IsNonScreen() {
			t.Errorf("%s must be a screen action", k)
		}
	}
}

// TestVisionFields_LoadFileAcceptsExpectChange: expect_change starts with
// "expect", so the loader's unknown-expect*-key guard must know it — a
// bank using it must load, not be refused as carrying an unknown key.
func TestVisionFields_LoadFileAcceptsExpectChange(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "vision.yaml")
	bank := `version: "1"
name: vision bank
test_cases:
  - id: TC-VIS-1
    name: vision gated
    steps:
      - name: press
        action: "keypress: KEYCODE_ENTER"
        expected: next screen
        vision_verify: true
        expect_change: true
`
	if err := os.WriteFile(p, []byte(bank), 0o600); err != nil {
		t.Fatal(err)
	}
	bf, err := LoadFile(p)
	if err != nil {
		t.Fatalf("LoadFile refused a bank using expect_change: %v", err)
	}
	if len(bf.TestCases) != 1 || !bf.TestCases[0].Steps[0].ExpectChange {
		t.Fatalf("expect_change lost on load: %+v", bf.TestCases)
	}
}
