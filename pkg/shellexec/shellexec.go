// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// Package shellexec runs a bank `shell:` step so that a cancellation (Ctrl-C,
// --timeout) stops ALL of it: the command runs in its OWN process group,
// cancelling the context kills that group (never the caller's), and Wait
// returns at most WaitDelay after the kill even when a descendant that left
// the group still holds the output pipe.
package shellexec

import (
	"context"
	"os/exec"
	"time"
)

// WaitDelay bounds how long Wait waits for the output pipes after the
// context killed the step (a descendant that escaped the group may hold
// them open).
const WaitDelay = 500 * time.Millisecond

// Command returns `sh -c script` bound to ctx as described in the package
// documentation.
func Command(ctx context.Context, script string) *exec.Cmd {
	c := exec.CommandContext(ctx, "sh", "-c", script)
	c.WaitDelay = WaitDelay
	ownProcessGroup(c)
	return c
}
