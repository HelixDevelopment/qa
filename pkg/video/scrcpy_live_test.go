// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

//go:build live_device

package video

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestScrcpyRecorder_LiveSegmentLoop_MIBOX4 is the smoke test for the
// FIX-QA-2026-04-21-012 segment-loop recorder, run against a real
// Android 9 device. Short maxSecs (8s) so the test spans one full
// segment + one partial, exercising both the rollover and concat
// paths.
//
// Build tag `live_device` keeps this out of the default test run.
// Prerequisite: MIBOX4 at 192.0.2.214:5555 is ADB-reachable,
// ffmpeg is on PATH.
//
// Run with:
//
//	cd HelixQA
//	GOTOOLCHAIN=local go test -mod=vendor -tags=live_device \
//	    -run TestScrcpyRecorder_LiveSegmentLoop_MIBOX4 -v \
//	    -timeout 60s ./pkg/video/...
func TestScrcpyRecorder_LiveSegmentLoop_MIBOX4(t *testing.T) {
	const device = "192.0.2.214:5555"
	if out, err := exec.Command("adb", "-s", device, "shell", "echo", "alive").CombinedOutput(); err != nil {
		t.Skipf("device %s unreachable: %v (out: %s)", device, err, strings.TrimSpace(string(out))) // SKIP-OK: #env-device-unreachable
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not on PATH: %v", err) // SKIP-OK: #env-ffmpeg-missing
	}

	tmpFile, err := os.CreateTemp("", "helixqa-live-*.mp4")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	tmpFile.Close()
	outputPath := tmpFile.Name()
	defer os.Remove(outputPath)
	// And the segments dir
	defer os.RemoveAll(outputPath + ".segments")

	rec := NewScrcpyRecorder(device, outputPath,
		WithMethod(MethodADBScreenrecord),
		WithMaxDuration(8), // 8-sec segments
		WithBitRate(4_000_000),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()

	if err := rec.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Run for ~20 seconds → 2-3 segments.
	time.Sleep(20 * time.Second)

	if err := rec.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("output file stat: %v (did ffmpeg concat run?)", err)
	}
	if info.Size() < 30*1024 {
		t.Fatalf("output unexpectedly small (%d bytes)", info.Size())
	}
	t.Logf("recorded %d bytes to %s", info.Size(), outputPath)
}
