// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

//go:build !unix

package shellexec

import "os/exec"

// ownProcessGroup is a no-op where process groups are not available: the
// context still kills the direct child and WaitDelay bounds the wait.
func ownProcessGroup(*exec.Cmd) {}
