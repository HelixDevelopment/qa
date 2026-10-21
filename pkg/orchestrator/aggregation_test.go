// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package orchestrator

import (
	"testing"

	"digital.vasic.challenges/pkg/challenge"

	"digital.vasic.helixqa/pkg/config"
	"digital.vasic.helixqa/pkg/testbank"
	"digital.vasic.helixqa/pkg/validator"
)

// skippedWrapperResult builds a challenge.Result shaped like the one
// the definitionChallenge wrapper emits when it honestly skips a
// matching-platform case that has no directly-executable `shell:`
// step (the steps run through the step-validation path instead).
func skippedWrapperResult(id challenge.ID) *challenge.Result {
	return &challenge.Result{
		ChallengeID:   id,
		ChallengeName: string(id),
		Status:        challenge.StatusSkipped,
		RecordedActions: []string{
			"definition-loaded: id=" + string(id),
			definitionWrapperSkipSentinel + " no desktop-executable shell action",
		},
	}
}

func passedStep(id string) *validator.StepResult {
	return &validator.StepResult{StepName: id, Status: validator.StepPassed}
}

// TestPromote_MatchingPlatformAllStepsPassed is the core bug-#1
// regression: a challenge whose bank case targets the run platform and
// whose step validation PASSED must aggregate to PASSED, not SKIPPED.
// Exercised on a UI platform (web) — see TestPromote_DesktopNeverPromoted
// for why desktop is deliberately excluded.
func TestPromote_MatchingPlatformAllStepsPassed(t *testing.T) {
	cr := skippedWrapperResult("UI-001")
	tc := &testbank.TestCase{
		ID:        "UI-001",
		Platforms: []config.Platform{config.PlatformWeb},
	}
	promoteSkippedToPassed(cr, passedStep("UI-001"), tc, config.PlatformWeb)
	if cr.Status != challenge.StatusPassed {
		t.Fatalf("matching-platform challenge with PASSED step = %q, want PASSED", cr.Status)
	}
}

// TestPromote_AllPlatformsCaseStepsPassed covers the `platforms: [all]`
// case — PlatformAll matches every run platform (here a UI platform).
func TestPromote_AllPlatformsCaseStepsPassed(t *testing.T) {
	cr := skippedWrapperResult("UI-002")
	tc := &testbank.TestCase{
		ID:        "UI-002",
		Platforms: []config.Platform{config.PlatformAll},
	}
	promoteSkippedToPassed(cr, passedStep("UI-002"), tc, config.PlatformWeb)
	if cr.Status != challenge.StatusPassed {
		t.Fatalf("[all]-platform challenge with PASSED step = %q, want PASSED", cr.Status)
	}
}

// TestPromote_EmptyPlatformsCaseStepsPassed: an empty Platforms list
// means "all platforms", so it must promote on a matching UI platform.
func TestPromote_EmptyPlatformsCaseStepsPassed(t *testing.T) {
	cr := skippedWrapperResult("UI-003")
	tc := &testbank.TestCase{ID: "UI-003"} // no Platforms => all
	promoteSkippedToPassed(cr, passedStep("UI-003"), tc, config.PlatformWeb)
	if cr.Status != challenge.StatusPassed {
		t.Fatalf("empty-Platforms challenge with PASSED step = %q, want PASSED", cr.Status)
	}
}

// TestPromote_DesktopNeverPromoted is the §107 anti-bluff hard guard:
// the desktop platform has no persistent app, so the step validator only
// proves crash-absence — NOT that any CLI command ran correctly. A
// desktop SKIP with a "passed" (crash-absent) step MUST stay SKIPPED;
// desktop cases earn PASSED only through real `shell:` execution. This
// is the regression for the exact bluff the earlier promotion would have
// manufactured (promoting `pherald version` to PASSED without ever
// running it).
func TestPromote_DesktopNeverPromoted(t *testing.T) {
	cr := skippedWrapperResult("DESKTOP-PROSE-1")
	tc := &testbank.TestCase{
		ID:        "DESKTOP-PROSE-1",
		Platforms: []config.Platform{config.PlatformDesktop},
	}
	// Even with a matching platform AND a "passed" step, desktop must NOT
	// be promoted — crash-absence is not execution evidence for a CLI.
	promoteSkippedToPassed(cr, passedStep("DESKTOP-PROSE-1"), tc, config.PlatformDesktop)
	if cr.Status != challenge.StatusSkipped {
		t.Fatalf("desktop prose-only challenge = %q, want SKIPPED (crash-absence is not a CLI PASS — must use shell: execution)", cr.Status)
	}
}

// TestPromote_NonMatchingPlatformStaysSkipped is the anti-bluff guard:
// a case pinned to a platform OTHER than the one being run must stay
// SKIPPED and must NEVER be promoted to PASSED — even if a (spurious)
// step result passed.
func TestPromote_NonMatchingPlatformStaysSkipped(t *testing.T) {
	cr := skippedWrapperResult("ANDROID-ONLY-1")
	tc := &testbank.TestCase{
		ID:        "ANDROID-ONLY-1",
		Platforms: []config.Platform{config.PlatformAndroid},
	}
	promoteSkippedToPassed(cr, passedStep("ANDROID-ONLY-1"), tc, config.PlatformWeb)
	if cr.Status != challenge.StatusSkipped {
		t.Fatalf("non-matching-platform challenge = %q, want SKIPPED (must not be promoted)", cr.Status)
	}
}

// TestPromote_NoStepResultStaysSkipped: with no step validation, there
// is no positive evidence, so the skip stands (anti-bluff — never
// invent a pass). Exercised on web so the nil-step branch is actually
// reached (desktop would short-circuit at the desktop guard).
func TestPromote_NoStepResultStaysSkipped(t *testing.T) {
	cr := skippedWrapperResult("UI-004")
	tc := &testbank.TestCase{ID: "UI-004", Platforms: []config.Platform{config.PlatformWeb}}
	promoteSkippedToPassed(cr, nil, tc, config.PlatformWeb)
	if cr.Status != challenge.StatusSkipped {
		t.Fatalf("challenge with no step result = %q, want SKIPPED", cr.Status)
	}
}

// TestPromote_FailedStepStaysSkipped: a failing step result must NOT
// be promoted. Exercised on web so the failed-step branch is reached.
func TestPromote_FailedStepStaysSkipped(t *testing.T) {
	cr := skippedWrapperResult("UI-005")
	tc := &testbank.TestCase{ID: "UI-005", Platforms: []config.Platform{config.PlatformWeb}}
	sr := &validator.StepResult{StepName: "UI-005", Status: validator.StepFailed}
	promoteSkippedToPassed(cr, sr, tc, config.PlatformWeb)
	if cr.Status != challenge.StatusSkipped {
		t.Fatalf("challenge with FAILED step = %q, want SKIPPED", cr.Status)
	}
}

// TestPromote_NonWrapperSkipUntouched: a SKIPPED result that does NOT
// carry the wrapper sentinel (some other component's deliberate skip)
// must be left untouched.
func TestPromote_NonWrapperSkipUntouched(t *testing.T) {
	cr := &challenge.Result{
		ChallengeID: "EXT-1",
		Status:      challenge.StatusSkipped,
		RecordedActions: []string{
			"external-skip: env not provisioned",
		},
	}
	tc := &testbank.TestCase{ID: "EXT-1", Platforms: []config.Platform{config.PlatformWeb}}
	promoteSkippedToPassed(cr, passedStep("EXT-1"), tc, config.PlatformWeb)
	if cr.Status != challenge.StatusSkipped {
		t.Fatalf("non-wrapper skip = %q, want SKIPPED (untouched)", cr.Status)
	}
}

// TestPromote_RealPassWrapperResultUntouched: a wrapper result that
// REALLY passed (executed shell steps) is already StatusPassed and
// must not be re-touched.
func TestPromote_RealPassUntouched(t *testing.T) {
	cr := &challenge.Result{ChallengeID: "SHELL-1", Status: challenge.StatusPassed}
	tc := &testbank.TestCase{ID: "SHELL-1", Platforms: []config.Platform{config.PlatformDesktop}}
	promoteSkippedToPassed(cr, passedStep("SHELL-1"), tc, config.PlatformDesktop)
	if cr.Status != challenge.StatusPassed {
		t.Fatalf("real-pass challenge = %q, want PASSED", cr.Status)
	}
}

// TestPromote_AssertingStepCaseNotPromoted is the §107 wider-guard
// regression (parity-audit 2026-05-30): the bank-runner path only executes
// `shell:` steps, so a case with an `http:`/`assert:`/`tap:`/… asserting step
// had that assertion NEVER run — promoting its SKIP to PASSED on crash-absence
// would manufacture a pass for an assertion that never executed. Such a case
// must stay SKIPPED on every platform.
func TestPromote_AssertingStepCaseNotPromoted(t *testing.T) {
	cr := skippedWrapperResult("UI-HTTP-1")
	tc := &testbank.TestCase{
		ID:        "UI-HTTP-1",
		Platforms: []config.Platform{config.PlatformWeb},
		Steps:     []testbank.TestStep{{Action: "http: GET /v1/health", ExpectStatus: 200}},
	}
	promoteSkippedToPassed(cr, passedStep("UI-HTTP-1"), tc, config.PlatformWeb)
	if cr.Status != challenge.StatusSkipped {
		t.Fatalf("case with an http: asserting step = %q, want SKIPPED (the assertion never ran — crash-absence is not evidence)", cr.Status)
	}
}

// TestPromote_ObservationalStepCasePromoted: a case whose steps are PURELY
// observational (screenshot/sleep/description) on a UI platform — where
// post-action crash-absence IS the intended validation — may still be
// promoted (no asserting step was skipped).
func TestPromote_ObservationalStepCasePromoted(t *testing.T) {
	cr := skippedWrapperResult("UI-OBS-1")
	tc := &testbank.TestCase{
		ID:        "UI-OBS-1",
		Platforms: []config.Platform{config.PlatformWeb},
		Steps:     []testbank.TestStep{{Action: "screenshot"}, {Action: "sleep: 500"}},
	}
	promoteSkippedToPassed(cr, passedStep("UI-OBS-1"), tc, config.PlatformWeb)
	if cr.Status != challenge.StatusPassed {
		t.Fatalf("purely-observational UI case = %q, want PASSED", cr.Status)
	}
}

// TestCaseHasUnrunAssertingStep_VisionGated is review finding 3 (no-bluff
// vision): a `screenshot`/`sleep` step carrying vision_verify / recognize /
// forbid / golden / expect_change ASSERTS what the screen shows. The bank
// runner never runs that vision check, so crash-absence must not promote
// such a case from SKIPPED to PASSED. Golden-good: the same steps without
// vision keys are still observational and still promoted.
func TestCaseHasUnrunAssertingStep_VisionGated(t *testing.T) {
	d := 4
	gated := []testbank.TestStep{
		{Action: "screenshot", VisionVerify: true},
		{Action: "screenshot", Recognize: []string{"Settings"}},
		{Action: "sleep: 500", Golden: "home", PHashMaxDistance: &d},
		{Action: "screenshot", Forbid: []string{"Error"}, VisionVerify: true},
		{Action: "description of the screen", ExpectChange: true, VisionVerify: true},
	}
	for i, st := range gated {
		cr := skippedWrapperResult("UI-VIS-1")
		tc := &testbank.TestCase{ID: "UI-VIS-1", Platforms: []config.Platform{config.PlatformWeb},
			Steps: []testbank.TestStep{{Action: "sleep: 100"}, st}}
		if !caseHasUnrunAssertingStep(tc, config.PlatformWeb) {
			t.Errorf("gated[%d]: vision-gated step not treated as asserting", i)
		}
		promoteSkippedToPassed(cr, passedStep("UI-VIS-1"), tc, config.PlatformWeb)
		if cr.Status != challenge.StatusSkipped {
			t.Errorf("gated[%d]: case = %q, want SKIPPED (the vision check never ran)", i, cr.Status)
		}
	}
	plain := &testbank.TestCase{ID: "UI-OBS-2", Platforms: []config.Platform{config.PlatformWeb},
		Steps: []testbank.TestStep{{Action: "screenshot"}, {Action: "sleep: 100"}}}
	if caseHasUnrunAssertingStep(plain, config.PlatformWeb) {
		t.Fatal("plain screenshot/sleep steps must stay observational")
	}
}
