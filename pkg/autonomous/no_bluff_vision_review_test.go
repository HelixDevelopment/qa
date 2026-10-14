// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// no_bluff_vision_review_test.go extends the no_bluff_vision gate with the
// findings of the independent review of 8627c0d/4ccbd03. Each test is a
// golden-good / golden-bad pair for one finding and is paired with a
// mutant in challenges/scripts/no_bluff_vision_mutation_challenge.sh.

package autonomous

import (
	"testing"
)

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
