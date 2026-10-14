// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"digital.vasic.challenges/pkg/challenge"

	"digital.vasic.helixqa/pkg/testbank"
)

// Operator decision 2026-10-08: expect_exit / expect_output_contains on a
// `shell:` step are evaluated by the bank-driven runner, not ignored. Before,
// a step carrying them was scored on "exit 0" alone, so 34 shipped assertions
// asserted nothing. Each case below runs a REAL `sh -c` command and reads the
// verdict from the real exit code and combined output.

func runShellSteps(t *testing.T, steps ...testbank.TestStep) *challenge.Result {
	t.Helper()
	d := &definitionChallenge{
		def: &challenge.Definition{ID: "SHA-1", Name: "shell assertions"},
		cfg: &challenge.Config{Timeout: 30 * time.Second},
	}
	return d.executeDesktopShellSteps(context.Background(), time.Now(), steps)
}

func ip(i int) *int       { return &i }
func sp(s string) *string { return &s }

func TestShellStep_ExpectExitNonZeroPassesWhenTheCommandExitsThatCode(t *testing.T) {
	res := runShellSteps(t, testbank.TestStep{Action: "shell: exit 3", ExpectExit: ip(3)})
	assert.Equal(t, challenge.StatusPassed, res.Status, res.Error)
}

func TestShellStep_ExpectExitZeroFailsOnANonZeroExit(t *testing.T) {
	res := runShellSteps(t, testbank.TestStep{Action: "shell: exit 3", ExpectExit: ip(0)})
	assert.Equal(t, challenge.StatusFailed, res.Status)
	assert.Contains(t, res.Error, "expected 0")
}

func TestShellStep_ExpectExitNonZeroFailsWhenTheCommandSucceeds(t *testing.T) {
	res := runShellSteps(t, testbank.TestStep{Action: "shell: exit 0", ExpectExit: ip(3)})
	assert.Equal(t, challenge.StatusFailed, res.Status)
	assert.Contains(t, res.Error, "expected 3")
}

func TestShellStep_OutputContainsIsReadFromCombinedStdoutAndStderr(t *testing.T) {
	res := runShellSteps(t,
		testbank.TestStep{Action: "shell: echo from-stdout", ExpectOutputContains: sp("from-stdout")},
		testbank.TestStep{Action: "shell: echo from-stderr 1>&2", ExpectOutputContains: sp("from-stderr")},
	)
	assert.Equal(t, challenge.StatusPassed, res.Status, res.Error)
}

func TestShellStep_OutputContainsMissFailsEvenWhenTheCommandExitsZero(t *testing.T) {
	res := runShellSteps(t, testbank.TestStep{Action: "shell: echo present", ExpectOutputContains: sp("absent")})
	assert.Equal(t, challenge.StatusFailed, res.Status)
	assert.Contains(t, res.Error, "does not contain")
}

func TestShellStep_NoKeysKeepsTheLegacyExitZeroVerdict(t *testing.T) {
	ok := runShellSteps(t, testbank.TestStep{Action: "shell: exit 0"})
	assert.Equal(t, challenge.StatusPassed, ok.Status)
	bad := runShellSteps(t, testbank.TestStep{Action: "shell: exit 1"})
	assert.Equal(t, challenge.StatusFailed, bad.Status)
}

// One failing step fails the case, whatever the other steps assert.
func TestShellStep_OneMismatchFailsTheWholeCase(t *testing.T) {
	res := runShellSteps(t,
		testbank.TestStep{Action: "shell: exit 0", ExpectExit: ip(0)},
		testbank.TestStep{Action: "shell: echo x", ExpectOutputContains: sp("y")},
	)
	assert.Equal(t, challenge.StatusFailed, res.Status)
}

// The recorded assertion rows must show what was expected vs observed, so a
// report carries evidence of the check, not just a verdict.
func TestShellStep_AssertionRowsCarryExpectedAndActual(t *testing.T) {
	res := runShellSteps(t, testbank.TestStep{
		Action: "shell: echo hello; exit 2", ExpectExit: ip(2), ExpectOutputContains: sp("hello"),
	})
	require.Equal(t, challenge.StatusPassed, res.Status, res.Error)
	var sawExit, sawOut bool
	for _, a := range res.Assertions {
		switch a.Type {
		case "shell-exit":
			sawExit = true
			assert.Equal(t, "2", a.Expected)
			assert.Equal(t, "2", a.Actual)
			assert.True(t, a.Passed)
		case "shell-output-contains":
			sawOut = true
			assert.Equal(t, "hello", a.Expected)
			assert.True(t, a.Passed)
		}
	}
	assert.True(t, sawExit, "an exit-code assertion row must be recorded")
	assert.True(t, sawOut, "an output-contains assertion row must be recorded")
}

// A command that NEVER STARTED must satisfy nothing. With an already-cancelled
// context exec refuses to start the process, ProcessState stays nil and the
// recorded exit code defaults to 0 — without the explicit guard that would
// read as "exited 0" and PASS a step that did not run (a hollow PASS).
func TestShellStep_ACommandThatNeverStartedFailsEvenWhenExit0IsExpected(t *testing.T) {
	d := &definitionChallenge{
		def: &challenge.Definition{ID: "SHA-NS", Name: "never started"},
		cfg: &challenge.Config{Timeout: 30 * time.Second},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, step := range []testbank.TestStep{
		{Action: "shell: echo would-have-run"},
		{Action: "shell: echo would-have-run", ExpectExit: ip(0)},
		{Action: "shell: echo would-have-run", ExpectExit: ip(0), ExpectOutputContains: sp("")},
	} {
		res := d.executeDesktopShellSteps(ctx, time.Now(), []testbank.TestStep{step})
		// Round 7: the RUN's context ended, so the case was not executed —
		// never PASSED, and not a failure of the app either (SKIPPED with
		// the wrapper's skip sentinel naming the interruption).
		assert.Equal(t, challenge.StatusSkipped, res.Status,
			"a step whose command never started must not pass: %+v", step.Action)
		assert.True(t, hasSkipSentinel(res), "the interruption must carry the skip sentinel")
	}
}
