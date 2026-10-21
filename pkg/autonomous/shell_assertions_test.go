// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"digital.vasic.helixqa/pkg/testbank"
)

// Operator decision 2026-10-08: the autonomous executor evaluates
// expect_exit / expect_output_contains on `shell:` steps with the SAME
// verdict function the bank-driven runner uses (testbank.TestStep.
// CheckShellOutcome), instead of scoring on exit 0 alone.

func ipA(i int) *int       { return &i }
func spA(s string) *string { return &s }

func TestPerformAction_ShellExpectExitNonZero(t *testing.T) {
	ste := &StructuredTestExecutor{}
	res := ste.performAction(context.Background(), &noopExecutor{},
		testbank.TestStep{Action: "shell: exit 3", ExpectExit: ipA(3)})
	require.True(t, res.Success, res.Message)
}

func TestPerformAction_ShellExpectExitMismatchFails(t *testing.T) {
	ste := &StructuredTestExecutor{}
	res := ste.performAction(context.Background(), &noopExecutor{},
		testbank.TestStep{Action: "shell: exit 0", ExpectExit: ipA(3)})
	require.False(t, res.Success)
	require.Contains(t, res.Message, "expected 3")
}

func TestPerformAction_ShellOutputContains(t *testing.T) {
	ste := &StructuredTestExecutor{}
	ok := ste.performAction(context.Background(), &noopExecutor{},
		testbank.TestStep{Action: "shell: echo needle-here 1>&2", ExpectOutputContains: spA("needle-here")})
	require.True(t, ok.Success, ok.Message)

	miss := ste.performAction(context.Background(), &noopExecutor{},
		testbank.TestStep{Action: "shell: echo something", ExpectOutputContains: spA("needle")})
	require.False(t, miss.Success)
	require.Contains(t, miss.Message, "does not contain")
}

// No keys: the established behaviour (exit 0 passes, anything else fails)
// is unchanged.
func TestPerformAction_ShellWithoutKeysKeepsExitZeroVerdict(t *testing.T) {
	ste := &StructuredTestExecutor{}
	require.True(t, ste.performAction(context.Background(), &noopExecutor{},
		testbank.TestStep{Action: "shell: exit 0"}).Success)
	bad := ste.performAction(context.Background(), &noopExecutor{},
		testbank.TestStep{Action: "shell: exit 7"})
	require.False(t, bad.Success)
	require.Contains(t, bad.Message, "exited 7")
}

// A command that never started (already-cancelled context) must FAIL, never
// read as "exited 0": ProcessState stays nil and the exit code defaults to 0.
func TestPerformAction_ShellThatNeverStartedFails(t *testing.T) {
	ste := &StructuredTestExecutor{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := ste.performAction(ctx, &noopExecutor{},
		testbank.TestStep{Action: "shell: echo would-have-run", ExpectExit: ipA(0)})
	require.False(t, res.Success, "a command that never started must not pass")
	require.Contains(t, res.Message, "shell error")
}
