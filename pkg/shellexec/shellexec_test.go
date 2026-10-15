// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

//go:build unix

package shellexec

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Review round 7, finding 11: a cancelled shell step must take its whole
// process group with it (no `sleep` orphan outliving the run), in its OWN
// group (never the caller's), and return within ~1.5 s.
func TestShellExec_CancelKillsOwnGroupOnly(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	cmd := Command(ctx, "sleep 37 & echo $! > "+pidFile+"; wait")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if pgid == syscall.Getpgrp() || pgid != cmd.Process.Pid {
		t.Fatalf("step runs in pgid %d (caller's is %d, child pid %d) — it must lead its own group", pgid, syscall.Getpgrp(), cmd.Process.Pid)
	}
	var child int
	for i := 0; i < 100 && child == 0; i++ {
		time.Sleep(20 * time.Millisecond)
		if b, err := os.ReadFile(pidFile); err == nil {
			child, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
	}
	if child == 0 {
		t.Fatal("child pid not written")
	}
	start := time.Now()
	cancel()
	_ = cmd.Wait()
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Fatalf("cancellation took %v", d)
	}
	time.Sleep(100 * time.Millisecond)
	if err := syscall.Kill(child, 0); err == nil {
		_ = syscall.Kill(child, syscall.SIGKILL)
		t.Fatalf("child %d (sleep 37) outlived the cancelled step", child)
	}
}

// The WaitDelay bound: a grandchild that left the group (setsid) keeps the
// output pipe open; Wait must still return within ~1.5 s of cancel.
func TestShellExec_WaitDelayBoundsEscapedChild(t *testing.T) {
	if _, err := os.Stat("/usr/bin/setsid"); err != nil {
		if _, err2 := os.Stat("/bin/setsid"); err2 != nil {
			t.Fatalf("setsid not available on this host: %v", err)
		}
	}
	pidFile := filepath.Join(t.TempDir(), "escaped.pid")
	t.Cleanup(func() {
		// The escaped grandchild left the group by design; do not leave it
		// running after the test.
		if b, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 1 {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	cmd := Command(ctx, "setsid sh -c 'echo $$ > "+pidFile+"; exec sleep 4' & sleep 30")
	done := make(chan struct{})
	go func() { _, _ = cmd.CombinedOutput(); close(done) }()
	time.Sleep(300 * time.Millisecond)
	start := time.Now()
	cancel()
	select {
	case <-done:
		if d := time.Since(start); d > 1500*time.Millisecond {
			t.Fatalf("cancellation took %v", d)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Wait blocked on a pipe held by an escaped grandchild")
	}
}
