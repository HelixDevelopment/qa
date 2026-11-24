# OCU P1 — GPU Capture Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: `superpowers:subagent-driven-development`. Fresh subagent per group. TDD rigid: failing test → verify fail → implement → verify pass → commit.

**Goal:** Implement four `contracts.CaptureSource` backends — Linux desktop (X11/SHM first, PipeWire later), web (CDP), Android (ADB screenrecord pipe), Android TV (ADB screenrecord) — plus a pluggable factory, latency bench per source.

**Architecture:** Each source is a package under `pkg/nexus/capture/<kind>/` implementing `contracts.CaptureSource`. A thin factory (`pkg/nexus/capture/factory.go`) selects the backend by `kind`. Each source streams frames on a buffered channel and drops under backpressure (spec §2.1 invariant). Zero new runtime deps; all bindings via existing chromedp / os/exec subprocess.

**Spec reference:** program design §1.1 (package tree), §2.1 (capture contract), §4.4 budgets (local 15ms, remote 8ms).

**Working dir:** `$WORKSPACE/Catalogizer/HelixQA` (absolute paths elsewhere noted)

**Invariants:**
- SPDX header every new `.go`
- `go 1.25.3` stays
- `GOTOOLCHAIN=local` prefix on vendor-affecting cmds
- Commits on main, no pushes until Group Z
- No sudo, no CI/CD, no TODO/FIXME
- Every public symbol tested + doc-commented

---

## Groups

### Group A — Factory + source registry

**Files:**
- Create: `pkg/nexus/capture/factory.go`
- Test: `pkg/nexus/capture/factory_test.go`

**Tasks:**

- [ ] **A1 — write failing test** Create `factory_test.go` asserting `Register("fake", fakeFactory)` + `Open(ctx, "fake", cfg)` returns a non-nil CaptureSource, and unknown kind returns error.
- [ ] **A2 — verify fail** `go test ./pkg/nexus/capture/... -run TestFactory_Register`. Must fail (package missing).
- [ ] **A3 — implement** Create `factory.go`:
  ```go
  package capture
  import (
      "context"
      "fmt"
      "sync"
      contracts "digital.vasic.helixqa/pkg/nexus/native/contracts"
  )
  type Factory func(ctx context.Context, cfg contracts.CaptureConfig) (contracts.CaptureSource, error)
  var (
      mu       sync.RWMutex
      registry = map[string]Factory{}
  )
  func Register(kind string, f Factory) {
      mu.Lock(); defer mu.Unlock()
      registry[kind] = f
  }
  func Open(ctx context.Context, kind string, cfg contracts.CaptureConfig) (contracts.CaptureSource, error) {
      mu.RLock()
      f, ok := registry[kind]
      mu.RUnlock()
      if !ok { return nil, fmt.Errorf("capture: unknown kind %q", kind) }
      return f(ctx, cfg)
  }
  func Kinds() []string {
      mu.RLock(); defer mu.RUnlock()
      out := make([]string, 0, len(registry))
      for k := range registry { out = append(out, k) }
      return out
  }
  ```
  With SPDX header + package doc comment naming P1 scope.
- [ ] **A4 — verify pass**
- [ ] **A5 — commit:** `feat(ocu/capture): add CaptureSource factory + registry`

### Group B — Web capture via CDP (Chromium/chromedp)

**Files:**
- Create: `pkg/nexus/capture/web/source.go`, `source_test.go`

**Strategy:** Wrap `chromedp` session; use `page.StartScreencast()` or repeated `page.CaptureScreenshot()` into frames. Use existing chromedp in HelixQA vendor graph. Init the source registers itself via `capture.Register("web", Open)`.

**Tasks:**

- [ ] **B1 — failing test** `TestWebSource_Construct` — open source with a stub browser (use httptest server + fake CDP), assert `Frames()` channel is non-nil, stats start at zero.

Given chromedp's real binding is heavy and tests that spin a real browser are slow, write the source with a `newBrowser` injectable function-var (mirroring probe's `execOutput` pattern). Test swaps in a fake that emits N frames then closes.

- [ ] **B2 — implement** `source.go`: struct `Source` with fields `cfg contracts.CaptureConfig, frames chan contracts.Frame, stats contracts.CaptureStats, stopCh chan struct{}`. Methods: `Name()="web"`, `Start(ctx, cfg)`, `Stop()`, `Frames()`, `Stats()`, `Close()`. Internally holds a `newBrowser` func-var returning the frame-producing goroutine handle.
- [ ] **B3 — pass + commit** `feat(ocu/capture/web): CDP-based web source`

### Group C — Linux desktop capture via X11 SHM

**Files:**
- Create: `pkg/nexus/capture/linux/source.go`, `source_test.go`

**Strategy:** P1 scope = X11 SHM via `xwd` subprocess pipe (no cgo in P1 — PipeWire/cgo land in P1.5 or P2). Fast enough for orchestrator-local capture at 1080p-ish. Uses `os/exec` with a context-bound `xwd -root -silent`.

**Tasks:**

- [ ] **C1 — failing test** `TestLinuxSource_Construct` with mocked `runCapture` func-var. Source emits frames with correct Seq / Timestamp / Width / Height when mock produces bytes.
- [ ] **C2 — implement** `source.go` — `Source` struct, `Name()="linux-x11"`, injects `runCapture` so tests don't spawn `xwd`. Registers itself: `init() { capture.Register("linux-x11", Open) }`.
- [ ] **C3 — commit** `feat(ocu/capture/linux): X11 SHM-based desktop source (P1 scope)`

### Group D — Android / Android TV via ADB screenrecord pipe

**Files:**
- Create: `pkg/nexus/capture/android/source.go`, `source_test.go`

**Strategy:** `adb -s <serial> shell screenrecord --output-format=h264 --size 1280x720 -` streams H.264 to stdout. Source wraps the subprocess, reads NAL units, emits H264-format Frames. Same backend drives Android phone + Android TV — kind string distinguishes (`"android"` vs `"androidtv"`) for factory lookup, but the code path is shared.

**Tasks:**

- [ ] **D1 — failing test** with mocked `runScreenRecord` func. Emits 3 H.264 frames; source forwards them.
- [ ] **D2 — implement** `source.go` with `Source`, `Open`, `init()` registering both kinds.
- [ ] **D3 — commit** `feat(ocu/capture/android): ADB screenrecord H.264 source`

### Group E — Latency bench per source

**Files:**
- Create: `pkg/nexus/capture/bench_test.go`

All benches use the mocked `run*` func-vars so they measure only the frame-channel plumbing overhead, not the actual capture backend. Real-backend latency lives behind integration tests in Group G.

- [ ] **E1 — BenchmarkWebSource_FrameThroughput** asserting <1ms per frame in-process; `budget.AssertWithin("web-capture-plumb", <measured>, budget.CaptureRemote)`.
- [ ] **E2 — BenchmarkLinuxSource** same pattern.
- [ ] **E3 — BenchmarkAndroidSource** same pattern.
- [ ] **E4 — commit** `bench(ocu/capture): per-source frame-channel throughput`

### Group F — Stress + security + challenges

- [ ] **F1 stress** — `TestStress_AllSources_100Clients` 100 goroutines each pulling frames; assert no data race, no drop beyond documented backpressure.
- [ ] **F2 security audit** — `docs/security/ocu-p1-audit.md` confirming: no sudo, no CGO, subprocess arg lists hardcoded, no shell indirection, no secret in logs.
- [ ] **F3 bank** — `banks/ocu-capture.json` — 15 entries covering each source's happy-path + edge (zero-frame / EOF / slow consumer drop) + adversarial (broken subprocess exit, invalid pixel format).
- [ ] **F4 commit** `test(ocu/capture): stress + security + challenge bank`

### Group G — Integration test (build-tag'd)

- [ ] **G1** — `tests/integration/ocu_capture_test.go` with `//go:build integration`. Runs a short `scrcpy-server` or `xwd -root` against local X, asserts ≥1 frame received. Skip gracefully if binary absent.
- [ ] **G2 commit** `test(ocu/capture): integration smoke across platforms`

### Group H — Close, docs, push

- [ ] **H1** Full HelixQA test suite green (OCU packages); `go vet` / `govulncheck` clean.
- [ ] **H2** Update `docs/nexus/ocu-roadmap.md`: P1 row → "CLOSED <date>"; bench table append capture latency actuals.
- [ ] **H3** Update `docs/OPEN_POINTS_CLOSURE.md` §5: add P1 ticked row.
- [ ] **H4** Commit in HelixQA; bump submodule pointer in main; push all upstreams.

---

## Contract stability

No changes to `pkg/nexus/native/contracts/capture.go`. If anything needs to change, that's a Group-A-level Constitution violation — STOP and escalate.

## Exit gate (Wave 2 partial credit)

P1 is closed when: every CaptureSource test green under `-race`, integration test green with build-tag, benches measured + baseline appended, audit filed, bank entries all green.

---

*Plan written by Opus 4.7, 2026-04-17.*
