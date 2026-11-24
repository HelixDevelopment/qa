# OCU P5 — Security Audit

**Date**: 2026-04-18  
**Scope**: `pkg/nexus/record/` (Recorder, FrameRing, encoder stubs, WebRTC stub)  
**Author**: vasic-digital  
**Status**: PASSED

---

## 1. Privilege escalation

**Finding**: No `sudo`, no `setuid`, no capability-raising calls anywhere in
`pkg/nexus/record/`. All file paths in `RecordConfig.OutputDir` are
operator-supplied and consumed only by code in P5.5 (not yet active).  
**Risk**: None in P5.  
**Action**: None required.

---

## 2. CGO surface

**Finding**: Zero CGO in P5. All encoder sub-packages (`x264`, `nvenc`, `vaapi`)
are pure-Go stubs. FFmpeg / libx264 / NVENC CGO bindings are explicitly deferred
to P5.5 and will be audited separately at that time.  
**Risk**: None in P5.  
**Action**: P5.5 CGO audit scheduled.

---

## 3. WebRTC / WHIP bind address

**Finding**: `webrtc.Publisher.BindAddr` defaults to `"127.0.0.1"` (loopback).
`NewPublisher()` sets this default explicitly. Binding to `"0.0.0.0"` is not
possible without explicit operator action (`--whip-bind` flag + bearer token,
per program spec §4.7). The P5 `Publish()` always returns `ErrNotWired`
regardless of `OptIn`, so no socket is ever opened in this phase.  
**Risk**: None in P5.  
**Action**: P5.5 must validate that `BindAddr == "0.0.0.0"` requires both
`BearerTok != ""` and an explicit CLI flag before binding.

---

## 4. NVENC remote dispatch

**Finding**: The `nvenc` encoder stub includes a documented plan to dispatch
encode jobs to `host-a.example.com` via `ocuremote.Dispatcher` in P5.5. This reuses
the SSH trust and key-based auth established by P2 — no new credential, no new
firewall rule, no new attack surface is introduced in P5.  
**Risk**: None in P5 (stub returns ErrNotWired; no network call made).  
**Action**: P5.5 must confirm the Dispatcher path validates the remote host
against the allow-list in `ocuremote` config before dispatching.

---

## 5. Clipper output — secrets

**Finding**: `clipWrite` serialises `frameMetadata` (seq, timestamp, width,
height). No pixel data, no API keys, no file paths, no environment variables
are written. The `ClipOptions.Annotation` field is operator-controlled; it is
written verbatim but carries no automatic secret extraction.  
**Risk**: Low. Callers must not pass secrets as annotations.  
**Action**: Document in `ClipOptions.Annotation` godoc that the value is
written to the output writer verbatim — no sanitisation.

---

## 6. Race safety

**Finding**: `FrameRing` uses `sync.Mutex` on every `Push` and `SnapshotAround`.
`Recorder` guards `src`, `publisher`, and `encErr` with `sync.Mutex`. `Start`
uses `sync.Once`; `Stop` uses a separate `sync.Once` to prevent double-close of
`stopCh`. The stress test (`TestStress_Record_100Concurrent`) passes cleanly
under `-race`.  
**Risk**: None.  
**Action**: None.

---

## 7. Denial of service — ring capacity

**Finding**: `FrameRing` has a fixed capacity set at construction time
(`NewRecorder` defaults to 1024). Once full, Push silently evicts the oldest
frame — no unbounded allocation, no goroutine leak.  
**Risk**: Oldest frames are lost when the ring overflows. Acceptable trade-off
for a bounded buffer.  
**Action**: Operator should size ring capacity based on expected clip window
duration × frame rate. Document in `NewRecorder` godoc.

---

## Summary

| Check | Result |
|---|---|
| No sudo / no root | PASS |
| No CGO in P5 | PASS |
| WebRTC binds to 127.0.0.1 by default | PASS |
| NVENC reuses existing SSH trust (no new credential) | PASS |
| Clipper does not leak secrets | PASS |
| Race-safe under -race | PASS |
| Bounded ring — no unbounded allocation | PASS |
