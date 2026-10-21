// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"testing"

	"digital.vasic.helixqa/pkg/orchestrator"
	"digital.vasic.helixqa/pkg/reporter"
)

// Review round 4, finding 6: `helixqa run` — an orch.Run error (e.g. a
// missing or malformed bank) means nothing was executed: exit 2, the same
// as `autonomous`; otherwise the result's three-valued code.
func TestRunExitCode(t *testing.T) {
	if got := runExitCode(nil, errors.New("load banks: stat bank x: no such file")); got != exitCodeNotExecuted {
		t.Fatalf("unloadable bank exit = %d, want %d", got, exitCodeNotExecuted)
	}
	if exitCodeNotExecuted != 2 {
		t.Fatalf("not-executed code must be 2, is %d", exitCodeNotExecuted)
	}
	ok := &orchestrator.Result{Success: true, Report: &reporter.QAReport{TotalChallenges: 1, PassedChallenges: 1}}
	if got := runExitCode(ok, nil); got != 0 {
		t.Fatalf("all-passed run exit = %d", got)
	}
	failed := &orchestrator.Result{Report: &reporter.QAReport{TotalChallenges: 1, FailedChallenges: 1}}
	if got := runExitCode(failed, nil); got != 1 {
		t.Fatalf("failed run exit = %d", got)
	}
}

// Finding 6: an unrecovered panic in the main goroutine maps to a distinct
// documented code (70, EX_SOFTWARE) — never 2 (blocked) or 0.
func TestGuardPanic(t *testing.T) {
	if got := guardPanic(func() { panic("boom") }); got != exitCodePanic || exitCodePanic != 70 {
		t.Fatalf("panic -> %d (exitCodePanic=%d), want 70", got, exitCodePanic)
	}
	if got := guardPanic(func() {}); got != 0 {
		t.Fatalf("no panic -> %d, want 0", got)
	}
}
