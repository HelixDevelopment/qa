// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// Package budget holds the shared non-functional invariants of the
// OCU pipeline. Every budget has a corresponding regression test or
// benchmark that fails if the invariant is violated. See the
// program-level spec §4.4.
package budget

import "time"

// Latency budgets. Each value is the maximum allowed latency for
// the described operation. Exceeding any value constitutes a
// regression and MUST block PR merge.
const (
	// CaptureLocal — CPU-path single-frame capture on the orchestrator host.
	CaptureLocal = 15 * time.Millisecond
	// CaptureRemote — single-frame capture from a device source
	// (DMA-BUF / scrcpy H264 stream).
	CaptureRemote = 8 * time.Millisecond
	// VisionLocal — CPU OpenCV full Analyze on a 1080p frame.
	VisionLocal = 25 * time.Millisecond
	// VisionRemoteCompute — time the remote CUDA worker spends
	// executing, excluding network RTT.
	VisionRemoteCompute = 8 * time.Millisecond
	// VisionRemoteRTT — network RTT orchestrator ↔ host-a.example.com.
	VisionRemoteRTT = 3 * time.Millisecond
	// InteractVerified — action dispatch + post-action verification.
	InteractVerified = 20 * time.Millisecond
	// ClipExtract — ±5s clip extraction from the recording ring buffer.
	ClipExtract = 200 * time.Millisecond
	// ActionCycleP50 — p50 end-to-end action cycle.
	ActionCycleP50 = 100 * time.Millisecond
	// ActionCycleP95 — p95 end-to-end action cycle.
	ActionCycleP95 = 200 * time.Millisecond
)

// Resource ceilings. Soak tests assert these.
const (
	// MaxHostRSSMB is the RSS ceiling for the orchestrator process
	// on a no-GPU host running the full pipeline (excluding
	// recording buffers, which are measured separately).
	MaxHostRSSMB uint64 = 1_500
	// MaxSidecarRSSMB is the RSS ceiling for the CUDA sidecar
	// container on host-a.example.com.
	MaxSidecarRSSMB uint64 = 4_096
	// MaxSidecarVRAMMB is the VRAM ceiling for the CUDA sidecar.
	MaxSidecarVRAMMB uint64 = 4_096
)
