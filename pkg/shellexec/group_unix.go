// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

//go:build unix

package shellexec

import (
	"os/exec"
	"syscall"
)

// ownProcessGroup makes the command lead a NEW process group (pgid = its
// pid) and makes cancellation kill exactly that group.
func ownProcessGroup(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if c.Process == nil {
			return nil
		}
		// Negative pid = the group led by the child: its own group,
		// never the caller's.
		return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
}
