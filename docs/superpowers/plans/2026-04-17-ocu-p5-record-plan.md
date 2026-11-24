# OCU P5 — Recording & Streaming Implementation Plan

**Date**: 2026-04-17  
**Status**: IN PROGRESS  
**Spec**: `docs/superpowers/specs/2026-04-17-openclaw-ultimate-program-design.md`

## Overview

P5 delivers the recording and streaming layer that sits above the P1 CaptureSource.
A `Recorder` accepts frames from a live `CaptureSource`, stores them in a ring
buffer, and exposes three output surfaces:

- **Ring-buffered Clip** — extract ±window/2 around a timestamp and write a
  newline-delimited JSON of frame metadata to any `io.Writer`. Real MKV/MP4
  muxing deferred to P5.5.
- **Three encoder stubs** — x264 (CPU), NVENC (GPU, dispatches to host-a.example.com
  in P5.5), VAAPI (GPU, local) all return `ErrNotWired` from production
  `Encode()`. Each registers via factory `init()`.
- **WebRTC/WHIP publisher** — off by default; `LiveStream` returns `ErrNotWired`
  in P5. Operator opt-in requires `--whip-bind` + bearer token per spec §4.7.

### No CGO, no sudo

P5 has zero CGO (FFmpeg/NVENC CGO bindings land in P5.5). All file paths are
user-writable. NVENC remote dispatch reuses the existing `ocuremote.Dispatcher`
SSH trust already established by P2.

---

## Groups

### A — Recorder + Encoder interface + frame Ring

- `pkg/nexus/record/encoder/encoder.go` — `Encoder` interface + `ErrNotWired`
- `pkg/nexus/record/ring.go` — bounded frame ring-buffer (mirrors observe
  RingBuffer but holds `contracts.Frame`)
- `pkg/nexus/record/recorder.go` — `Recorder` struct implementing
  `contracts.Recorder`
- `pkg/nexus/record/recorder_test.go` + `ring_test.go`

### B — x264 encoder stub

- `pkg/nexus/record/encoder/x264/encoder.go` — local CPU stub, returns
  `ErrNotWired`; mock-injectable for tests
- `pkg/nexus/record/encoder/x264/encoder_test.go`

### C — NVENC encoder stub

- `pkg/nexus/record/encoder/nvenc/encoder.go` — GPU stub; comment notes
  remote dispatch via `ocuremote.Dispatcher` in P5.5
- `pkg/nexus/record/encoder/nvenc/encoder_test.go`

### D — VAAPI encoder stub

- `pkg/nexus/record/encoder/vaapi/encoder.go` — local GPU stub
- `pkg/nexus/record/encoder/vaapi/encoder_test.go`

### E — Clipper

- `pkg/nexus/record/clip.go` — `Recorder.Clip()` + `ClipOptions`
  + ring `SnapshotAround(at, window)`
- `pkg/nexus/record/clip_test.go`

### F — WebRTC / WHIP stub

- `pkg/nexus/record/webrtc/publisher.go` — P5 WHIP stub, off by default
- `pkg/nexus/record/webrtc/publisher_test.go`

### G — Bench + stress + security audit + bank + integration + close + push

- `pkg/nexus/record/bench_test.go` — Ring.Push, Ring.SnapshotAround, Clip
- `pkg/nexus/record/stress_test.go` — 100 concurrent Recorder cycles under
  -race
- `docs/security/ocu-p5-audit.md` — no sudo, no CGO, WHIP localhost default,
  NVENC SSH reuse
- `banks/ocu-record.json` — ≥15 entries
- `tests/integration/ocu_record_test.go` — tag-gated integration smoke
- Roadmap row P5 flipped to CLOSED
