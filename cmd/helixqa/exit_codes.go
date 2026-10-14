// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"digital.vasic.helixqa/pkg/orchestrator"
)

// Process exit codes shared by the subcommands (see
// docs/vision/NO_BLUFF_VISION.md "Exit-code contract"). 0 pass and 1 fail
// come from the result itself.
const (
	// exitCodeNotExecuted: nothing failed, but the verdict could not be
	// determined (blocked / skipped / not executed — e.g. a bank that could
	// not be loaded).
	exitCodeNotExecuted = 2
	// exitCodePanic (EX_SOFTWARE): an unrecovered Go panic in the main
	// goroutine — an internal HelixQA defect, never a verdict about the app.
	exitCodePanic = 70
)

// runExitCode maps a `helixqa run` outcome onto the contract. A FAILED
// verdict (a failed case, crash or ANR in the result's report) is 1 even
// when orch.Run also returned an error raised after execution (report write
// failure, cancellation): a finding outranks could-not-determine. Any other
// error means the run is incomplete or nothing executed -> 2; otherwise the
// result's own three-valued code.
func runExitCode(result *orchestrator.Result, err error) int {
	if result != nil && result.Report != nil && result.ExitCode() == 1 {
		return 1
	}
	if err != nil {
		return exitCodeNotExecuted
	}
	if result == nil {
		return exitCodeNotExecuted
	}
	return result.ExitCode()
}

// guardPanic runs fn and maps an unrecovered panic to exitCodePanic,
// printing the panic and stack to stderr. It only covers the calling
// goroutine: a panic in another goroutine still terminates the process
// with Go's own exit status 2 (documented).
func guardPanic(fn func()) (code int) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "internal error (panic): %v\n%s", r, debug.Stack())
			code = exitCodePanic
		}
	}()
	fn()
	return 0
}
