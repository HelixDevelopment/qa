// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"digital.vasic.challenges/pkg/challenge"

	"digital.vasic.helixqa/pkg/config"
	"digital.vasic.helixqa/pkg/testbank"
)

// Review round 2, finding 1 (HIGH): on desktop the bank runner executes only
// `shell:` steps. A case whose shell steps exit 0 but which also carries a
// vision-gated (or other asserting) step the runner never executed must NOT
// be PASSED — that step's assertion never ran. A real shell failure still
// FAILS the case.
func TestDesktopShellCase_UnrunAssertingStepNotPassed(t *testing.T) {
	d4 := 4
	others := map[string]testbank.TestStep{
		"screenshot+vision_verify": {Name: "look", Action: "screenshot", Expected: "x", VisionVerify: true},
		"keypress+recognize":       {Name: "press", Action: "keypress: KEYCODE_ENTER", Expected: "x", Recognize: []string{"Login"}},
		"sleep+golden":             {Name: "wait", Action: "sleep: 10", Golden: "home", PHashMaxDistance: &d4},
		"http (unrun assertion)":   {Name: "api", Action: "http: GET /health", ExpectStatus: 200},
	}
	for name, other := range others {
		tc := &testbank.TestCase{ID: "DSK-VIS", Name: "desktop mixed", Steps: []testbank.TestStep{
			{Name: "cli", Action: "shell: true"}, other,
		}}
		d := &definitionChallenge{
			def:      &challenge.Definition{ID: "DSK-VIS", Name: "desktop mixed"},
			cfg:      &challenge.Config{Timeout: 30 * time.Second},
			platform: config.PlatformDesktop,
			testCase: tc,
		}
		res, err := d.Execute(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if res.Status == challenge.StatusPassed {
			t.Errorf("%s: case PASSED although that step never ran", name)
			continue
		}
		if res.Status != challenge.StatusSkipped || !strings.Contains(strings.Join(res.RecordedActions, "\n"), "not executed") {
			t.Errorf("%s: status=%q actions=%v — want an honest SKIPPED naming the unexecuted step", name, res.Status, res.RecordedActions)
		}
		// Promotion must not turn it into a pass either.
		promoteSkippedToPassed(res, passedStep("DSK-VIS"), tc, config.PlatformDesktop)
		if res.Status == challenge.StatusPassed {
			t.Errorf("%s: promoted to PASSED", name)
		}
	}
	// A failing shell step still FAILS the mixed case.
	tc := &testbank.TestCase{ID: "DSK-VIS2", Name: "x", Steps: []testbank.TestStep{
		{Action: "shell: exit 4"}, {Action: "screenshot", VisionVerify: true},
	}}
	d := &definitionChallenge{def: &challenge.Definition{ID: "DSK-VIS2"}, cfg: &challenge.Config{Timeout: 30 * time.Second},
		platform: config.PlatformDesktop, testCase: tc}
	res, _ := d.Execute(context.Background())
	if res.Status != challenge.StatusFailed {
		t.Fatalf("failing shell step in a mixed case: status=%q, want FAILED", res.Status)
	}
	// Golden-good: shell-only case still PASSES.
	tc = &testbank.TestCase{ID: "DSK-OK", Name: "x", Steps: []testbank.TestStep{{Action: "shell: true"}, {Action: "sleep: 10"}}}
	d = &definitionChallenge{def: &challenge.Definition{ID: "DSK-OK"}, cfg: &challenge.Config{Timeout: 30 * time.Second},
		platform: config.PlatformDesktop, testCase: tc}
	if res, _ = d.Execute(context.Background()); res.Status != challenge.StatusPassed {
		t.Fatalf("shell-only case: status=%q, want PASSED", res.Status)
	}
}
