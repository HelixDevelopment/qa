# OpenClaw Ultimate Capabilities Extension — Program-Level Design

**Spec ID:** `ocu-program-design`
**Date:** 2026-04-17
**Status:** Draft — pending operator approval
**Scope:** Program-level roadmap coordinating 8 sub-project specs (P0–P7). Each sub-project gets its own later design + plan doc; this spec only covers the program spine.

## How to read this spec

This document covers the *shape* of the entire OpenClaw Ultimate (OCU) programme: decomposition, contracts, constitution compliance, sequencing, gates, risks. It does **not** cover implementation details of any single sub-project — those get their own `docs/superpowers/specs/YYYY-MM-DD-ocu-p<N>-*-design.md` + matching plan doc when we reach them.

Read order: §0 baseline → §1 architecture → §2 contracts → §3 containers extension → §4 constitution + tests + evidence → §5 sequencing + gates + risks.

## Cross-references

- Research source: `helix_qa/docs/OpenClaw_Ultimate_Capabilities_Extension.md` (4204 lines)
- Upstream context: `docs/nexus/OpenClawing2.md` (prior OSS integration wave)
- Operator brief: `docs/OPEN_POINTS_CLOSURE.md` (§6 Constitution Article VI rule)
- Submodule extension site: `containers/pkg/{remote,scheduler,health,envconfig}`
- Constitution: `CONSTITUTION.md` (main) + `helix_qa/CLAUDE.md` (HelixQA-specific rules)

---

## §0 — Locked baseline (operator-approved during brainstorm)

Recorded verbatim. These are non-negotiable for the programme; changes require a new spec.

| # | Decision | Choice |
|---|---|---|
| 1 | Scope | 8 sub-projects (P0 foundation, P1 capture, P2 vision, P3 interact, P4 observe, P5 record, P6 automation, P7 tickets+tests+challenges); brainstorm P0 first |
| 2 | Language policy | Hybrid by layer — pure-Go where viable, CGO for tight loops, sidecar/container (via containers/pkg/distribution) for CUDA + TensorRT + NVENC |
| 3 | Platform matrix | Linux desktop + web (Chromium/Firefox via CDP) + Android + Android TV |
| 4 | Native vs LLM role | Native = eyes + hands + post-action verifier; LLM remains sole decider; HelixQA constitution intact |
| 5 | Evidence default tier | Items 1–11, 15, 16 default on every ticket; items 13, 14 on demand; item 12 collected but not linked |
| 6 | Sequencing + release | Diamond with parallel middle (P0 → {P1,P2,P3,P4 parallel} → P5 → P6 → P7); single **v4.0.0 "OpenClaw Ultimate"** release after P7 |
| C | Hardware constraint | Local host has no NVIDIA GPU. Every CUDA/TensorRT/NVENC workload must run on `host-a.example.com` via `containers/pkg/distribution` with GPU label extension (§3). SSH user `workshop-admin`, passwordless key auth already configured. |

---

## §1 — Program architecture & package topology

### 1.1 New + extended package tree

```
helix_qa/
├── pkg/nexus/native/              [NEW — P0]
│   ├── bridge/                    CGO + subprocess + RPC bridge primitives
│   ├── remote/                    Thin adapter over containers/pkg/distribution
│   │                              (the parent project-style functional-options wrapper,
│   │                              <200 LOC: configure hosts, Resolve(Capability))
│   ├── budget/                    Shared CPU/GPU/RAM/latency quotas (constants
│   │                              + asserters; §4.4 encoded here)
│   ├── probe/                     Hardware-capability autodiscovery
│   │                              (local + host-a.example.com via SSH)
│   └── contracts/                 Versioned interface types (§2)
├── pkg/nexus/capture/             [NEW — P1]
│   ├── source/                    CaptureSource interface
│   ├── linux/                     KMS + DMA-BUF, PipeWire, X11-shm
│   ├── web/                       CDP Page.captureScreenshot + Page.startScreencast
│   ├── android/                   scrcpy server + ADB
│   └── tv/                        ADB screenrecord + framebuffer pull
├── pkg/nexus/vision/              [NEW — P2]
│   ├── pipeline/                  Frame-graph orchestrator (CPU/OCL/CUDA dispatch)
│   ├── opencv/                    CGO wrapper; pure-Go fallback via gocv
│   ├── ocr/                       Tesseract-local + TensorRT-sidecar-on-thinker
│   ├── template/                  Template matching (CPU + CUDA)
│   ├── diff/                      Pixel diff + change-region detector
│   └── elements/                  UI element extractor (DOM + AX + CV merge)
├── pkg/nexus/interact/            [NEW — P3]
│   ├── linux/                     evdev + uinput (no sudo — input group + udev)
│   ├── web/                       CDP Input.dispatchMouseEvent / Input.dispatchKeyEvent
│   ├── android/                   ADB input + sendevent
│   └── verify/                    "action → wait → capture → prove" verifier
├── pkg/nexus/observe/             [NEW — P4]
│   ├── ld_preload/                Per-target .so shim
│   ├── plthook/                   Runtime PLT/GOT hooking
│   ├── dbus/                      Signal subscriber
│   ├── cdp/                       Chrome DevTools Protocol event tap
│   └── ax/                        Linux AT-SPI tree walker
├── pkg/nexus/record/              [NEW — P5]
│   ├── pipeline/                  libobs-style scene graph
│   ├── ffmpeg/                    NVENC (on thinker) / VAAPI / x264 (local)
│   ├── webrtc/                    WHIP publisher (on-demand live review, off by default)
│   ├── segments/                  MKV/MP4 ring buffer with per-action index
│   └── clip/                      ±5s clipper for ticket evidence item #7
├── pkg/nexus/automation/          [NEW — P6]
│   ├── engine/                    Composes capture + vision + interact + record
│   ├── verifier/                  Post-action pixel + AX + hook-trace verifier
│   └── agent_bridge/              Glues into pkg/nexus/agent state machine
├── pkg/ticket/                    [EXTEND — P7] (new evidence types)
├── challenges/banks/ocu-*.json    [NEW — P7] (11 new bank files, ~180 entries)
└── cmd/helixqa/                   [EXTEND — P7] (replay, probe, capture-test)

containers/                        [EXTEND — P0]
├── pkg/remote/probe_gpu.go        [NEW] SSH GPU probe (nvidia-smi, rocm-smi, clinfo)
├── pkg/remote/host.go             [EXTEND] HostResources.GPU []GPUDevice
├── pkg/scheduler/requirements.go  [EXTEND] ContainerRequirements.GPU *GPURequirement
├── pkg/scheduler/scorer.go        [EXTEND] GPU-aware CanFit + Score
├── pkg/scheduler/strategies.go    [EXTEND] gpu_affinity strategy
├── pkg/health/gpu.go              [NEW] nvidia-smi-based health check
├── pkg/envconfig/remote.go        [EXTEND] GPU_AUTOPROBE, GPU labels
└── docs/gpu-scheduling.md         [NEW] host-a.example.com recipe
```

### 1.2 Distribution topology

```
┌───── orchestrator host (no NVIDIA GPU) ───────────┐        ┌──────── host-a.example.com (RTX 3060) ─────────┐
│  helixqa autonomous ...                           │        │  podman container (gpu-sidecar)           │
│    ├─ pkg/nexus/automation.Engine                 │  SSH   │    ├─ nvidia-container-toolkit            │
│    ├─ pkg/nexus/capture  (local sources)          │ ◄────► │    ├─ OpenCV-CUDA worker (gRPC listener)  │
│    ├─ pkg/nexus/vision   (CPU+OpenCL local;       │        │    ├─ TensorRT-OCR worker                 │
│    │                      CUDA via remote)        │        │    ├─ NVENC encoder                       │
│    ├─ pkg/nexus/interact (local)                  │        │    └─ Maxine enhancer (optional)          │
│    ├─ pkg/nexus/observe  (local)                  │        │                                           │
│    ├─ pkg/nexus/record   (local cap; NVENC        │        │  Dispatched + lifecycle-managed by        │
│    │                      encode remote;          │        │  containers/pkg/distribution              │
│    │                      VAAPI/x264 fallback)    │        │  + GPU-aware scheduler (§3)               │
│    └─ pkg/nexus/native/remote (the glue)          │        │                                           │
└───────────────────────────────────────────────────┘        └───────────────────────────────────────────┘
```

### 1.3 Design invariants baked in

1. **Everything GPU-bound is dispatchable**, never hard-wired local. `pkg/nexus/native/remote` + Containers `pkg/distribution` decide per-call whether to run locally (CPU/OpenCL) or dispatch to thinker (CUDA/TensorRT/NVENC). If thinker is unreachable the system degrades, never silently fakes.
2. **Nothing new depends on any consumer project.** Every new package is project-agnostic (matches helix_qa/CLAUDE.md project-agnostic rule).
3. **No CI/CD files** added (HelixQA rule).
4. **No sudo / root** — Linux input uses `input` group + udev rule we ship; hooks stay per-process.
5. **Constitution intact** — LLM remains sole decider; native is perception + execution + verification only.

---

## §2 — Sub-project contracts (interface boundaries)

The diamond-parallel model (P1–P4 simultaneously) only works if each sub-project publishes a clean, stable contract up front, before any of them is implemented. These contracts land in `pkg/nexus/native/contracts/` in P0 and become unchangeable except by versioned breaking change.

### 2.1 Capture contract

```go
// pkg/nexus/native/contracts/capture.go
type CaptureSource interface {
    Name() string
    Start(ctx context.Context, cfg CaptureConfig) error
    Stop() error
    Frames() <-chan Frame
    Stats() CaptureStats
    Close() error
}

type Frame struct {
    Seq          uint64
    Timestamp    time.Time
    Width, Height int
    Stride        int
    Format        PixelFormat
    Data          FrameData
    Metadata      map[string]string
}

type FrameData interface {
    AsBytes() ([]byte, error)
    AsDMABuf() (*DMABufHandle, bool)
    Release() error
}
```

### 2.2 Vision contract

```go
// pkg/nexus/native/contracts/vision.go
type VisionPipeline interface {
    Analyze(ctx context.Context, frame Frame) (*Analysis, error)
    Match(ctx context.Context, frame Frame, tmpl Template) ([]Match, error)
    Diff(ctx context.Context, before, after Frame) (*DiffResult, error)
    OCR(ctx context.Context, frame Frame, region Rect) (OCRResult, error)
}

type Analysis struct {
    Elements        []UIElement
    TextRegions     []OCRBlock
    DetectedChanges []ChangeRegion
    Confidence      float64
    DispatchedTo    string // "local-cpu" | "local-opencl" | "thinker-cuda"
    LatencyMs       int
}
```

### 2.3 Interact contract

```go
// pkg/nexus/native/contracts/interact.go
type Interactor interface {
    Click(ctx context.Context, at Point, opts ClickOptions) error
    Type(ctx context.Context, text string, opts TypeOptions) error
    Scroll(ctx context.Context, at Point, dx, dy int) error
    Key(ctx context.Context, key KeyCode, opts KeyOptions) error
    Drag(ctx context.Context, from, to Point, opts DragOptions) error
    // Every method returns only after the verifier runs
    // (supplied via WithVerifier option).
}
```

### 2.4 Observe contract

```go
// pkg/nexus/native/contracts/observe.go
type Observer interface {
    Start(ctx context.Context, target Target) error
    Events() <-chan Event
    Snapshot(at time.Time, window time.Duration) ([]Event, error)
    Stop() error
}
```

### 2.5 Record contract

```go
// pkg/nexus/native/contracts/record.go
type Recorder interface {
    AttachSource(src CaptureSource) error
    Start(ctx context.Context, cfg RecordConfig) error
    Clip(around time.Time, window time.Duration, out io.Writer, opts ClipOptions) error
    LiveStream(ctx context.Context) (whipURL string, err error)
    Stop() error
}
```

### 2.6 Remote-dispatch contract (CUDA-on-host-a.example.com glue)

```go
// pkg/nexus/native/remote/dispatcher.go
type Dispatcher interface {
    Resolve(ctx context.Context, need Capability) (Worker, error)
}

type Capability struct {
    Kind        Kind // "cuda-opencv" | "nvenc" | "tensorrt-ocr"
    MinVRAM     int
    PreferLocal bool // default false; true only for latency-critical local-only work
}

type Worker interface {
    Call(ctx context.Context, req proto.Message, resp proto.Message) error
    Close() error
}
```

### 2.7 Parallel-wave corollary

```
                        ┌───────────────────────────────┐
                        │ P0: Foundation + contracts    │
                        │     + native/remote           │
                        │     + Containers GPU extension│
                        └───────────────┬───────────────┘
                                        │
               ┌─────────────┬──────────┼──────────┬────────────┐
               ▼             ▼          ▼          ▼            ▼
            P1 Capture   P2 Vision  P3 Interact  P4 Observe
                                        │
                                        ▼
                                    P5 Record
                                        │
                                        ▼
                              P6 AutomationEngine
                                        │
                                        ▼
                       P7 Tickets + tests + challenges + v4.0 release
```

Each middle sub-project consumes only its own contract and `pkg/nexus/native/{bridge,remote,budget,probe,contracts}`. Zero cross-middle dependencies.

### 2.8 Contract stability rule

Once P0 lands, contracts are **versioned**. A breaking change requires a new `v2` interface name and a transitional adapter — never in-place mutation. This is what actually makes the parallel middle work.

---

## §3 — containers/ GPU extension (upstream submodule change)

### 3.1 Additions to `pkg/remote/host.go`

```go
type HostResources struct {
    // existing fields: CPU*, Memory*, Disk*, Network*
    GPU []GPUDevice `json:"gpu,omitempty"`
}

type GPUDevice struct {
    Index             int    `json:"index"`
    Vendor            string `json:"vendor"`         // "nvidia" | "amd" | "intel"
    Model             string `json:"model"`          // "RTX 3060"
    DriverVersion     string `json:"driver_version"`
    VRAMTotalMB       int    `json:"vram_total_mb"`
    VRAMFreeMB        int    `json:"vram_free_mb"`
    UtilPercent       int    `json:"util_percent"`
    CUDASupported     bool   `json:"cuda_supported"`
    CUDAVersion       string `json:"cuda_version,omitempty"`
    ComputeCapability string `json:"compute_capability,omitempty"`
    NVENCSupported    bool   `json:"nvenc_supported"`
    NVDECSupported    bool   `json:"nvdec_supported"`
    VulkanSupported   bool   `json:"vulkan_supported"`
    OpenCLSupported   bool   `json:"opencl_supported"`
    ROCmSupported     bool   `json:"rocm_supported"`
    NVIDIARuntime     bool   `json:"nvidia_runtime"`
}
```

### 3.2 Additions to `pkg/scheduler/requirements.go`

```go
type ContainerRequirements struct {
    // existing fields unchanged
    GPU *GPURequirement `json:"gpu,omitempty"`
}

type GPURequirement struct {
    Count        int      `json:"count"`
    MinVRAMMB    int      `json:"min_vram_mb"`
    Vendor       string   `json:"vendor,omitempty"`
    MinCompute   string   `json:"min_compute,omitempty"`
    Capabilities []string `json:"capabilities,omitempty"`
}
```

### 3.3 Scheduler changes

- `CanFit()` gains GPU branch: if `req.GPU != nil`, host must have ≥ `Count` GPUs meeting vendor/vram/compute/capability constraints.
- `Score()` gains GPU-utilisation-aware component.
- Existing `affinity` strategy unchanged. `resource_aware` now incorporates GPU.
- New `gpu_affinity` strategy for GPU-required workloads.

### 3.4 GPU probing

New file `pkg/remote/probe_gpu.go`:

- `which nvidia-smi` → `nvidia-smi --query-gpu=... --format=csv`
- `which rocm-smi` → AMD branch
- `clinfo -l` → OpenCL device list
- `docker info --format '{{.Runtimes}}'` → nvidia runtime check

Results fold into `HostResources.GPU`. Runs on host-add and via existing `HostManager.ProbeAll()` cadence. Cached 60s; `ProbeGPUForce(ctx, host)` bypasses for tests.

### 3.5 Health extension

`pkg/health/gpu.go` adds `GPUHealthCheck` opt-in via `scheduler.WithGPUHealth(true)`:
- `nvidia-smi -q -d MEMORY` parsed for `vram_free_mb >= req.MinVRAMMB`
- VRAM drop triggers `HostDegraded` event over the existing event bus

### 3.6 Env-config additions

```bash
CONTAINERS_REMOTE_HOST_1_NAME=host-a.example.com
CONTAINERS_REMOTE_HOST_1_ADDRESS=host-a.example.com
CONTAINERS_REMOTE_HOST_1_USER=workshop-admin
CONTAINERS_REMOTE_HOST_1_LABELS=gpu=true,gpu_vendor=nvidia,gpu_model=rtx3060,cuda=12.2,nvenc=true,vulkan=true
CONTAINERS_REMOTE_HOST_1_GPU_AUTOPROBE=true
```

`GPU_AUTOPROBE=true` (default): probe fills `HostResources.GPU` directly; env labels become hints. `false`: hosts trusted as labelled.

### 3.7 Tests shipping with the extension

- Unit: scorer GPU branch (16 table-driven cases), probe parser (synthetic nvidia-smi / rocm-smi output).
- Integration: fake SSH server (existing pattern in `ssh_executor_test.go`).
- Regression: no-GPU host + no-GPU requirement schedules exactly as today (backward-compat proof).

### 3.8 Release

- `containers/ARCHITECTURE.md` section for GPU scheduling
- `containers/docs/gpu-scheduling.md` with host-a.example.com recipe
- `containers/CHANGELOG.md` minor bump (backward-compat, additive only)
- Tagged after merge

### 3.9 Rationale for placing extension in containers/

- the parent project benefits (can move off legacy `DB_HOST=host-a.example.com`)
- Future consumers get GPU-aware distribution for free
- Keeps HelixQA `pkg/nexus/native/remote` adapter thin (~200 LOC)

---

## §4 — Constitution compliance, test surface, evidence spine

### 4.1 Constitution compliance matrix

| Constitution rule | Where enforced in design |
|---|---|
| HelixQA: Project-agnostic | Zero consumer imports in any new `pkg/nexus/*`. All host-a.example.com specifics in operator `.env`. |
| HelixQA: LLM sole decider (supreme) | P6 `AutomationEngine` receives actions from the Agent; native never synthesises actions. Vision returns signals, not decisions. Verifier reports true/false; on false asks Agent to decide, never auto-retries. |
| HelixQA: video geo-restriction probe | Unchanged — pipeline wraps existing probe verbatim. |
| HelixQA: QA priority order | P7 bank `OCU-*` ordered happy → edge → adversarial. |
| HelixQA: No CI/CD pipelines | No `.github/`, no `.gitlab-ci.yml`. Test runners are Makefile targets + scripts. |
| HelixQA: screenshot/video validation | P7 verifier asserts every action produces a non-identical post-frame; P5 records min-bitrate clips. |
| HelixQA: Evidence-backed tickets | Default evidence tier (§4.5). |
| HelixQA: No sudo/root | P3 Linux interactor uses `udev` rule + `input` group. Installer ships the rule + per-user activation hint; refuses if group missing. |
| HelixQA: API keys & secrets | SSH key auth only; no passwords stored. `.env.example` with `GPU_AUTOPROBE=true`. `gpu_capability.json` gitignored. |
| Main CONSTITUTION §V (100% coverage, 10 categories) | §4.2 |
| Main CONSTITUTION Article VI (closure brief) | Each sub-project ticks the relevant `OPEN_POINTS_CLOSURE.md` items atomically in the same commit. New operator items (e.g., host-a.example.com SSH key quarterly rotation) land in the brief in the same commit that surfaces them. |

### 4.2 Test coverage targets — 10 categories per sub-project

| Category | P0 | P1 | P2 | P3 | P4 | P5 | P6 | P7 | How measured |
|---|---|---|---|---|---|---|---|---|---|
| Unit | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | `go test -cover`; ≥ 95% lines, 100% of exported API |
| Integration | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | Cross-package, real subprocess where possible |
| E2E | — | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | Full user journey; no mocks at boundary |
| Full automation | — | — | — | — | — | — | ✓ | ✓ | `make ocu-e2e-all` on `.devconnect` devices + compose stack |
| Stress | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | 60-min soak; 4K@60 for P1/P5; 1000 actions/min for P3; memory ceiling asserted |
| Security | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | govulncheck clean; Semgrep/Gosec; hook audit; known_hosts only SSH |
| DDoS / rate | — | ✓ | — | ✓ | ✓ | ✓ | ✓ | ✓ | Capture backpressure; interact rate-cap; recorder queue-bounded; hook-storm survivable |
| Benchmarking | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | Baseline + regression vs `docs/benchmarks/ocu-baseline-2026-04-17.md` |
| Challenges | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ≥ 1 registered `digital.vasic.challenges` per feature. Bank `challenges/banks/ocu-*.json`. |
| HelixQA autonomous | — | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | Bank entry + session in `qa-results/` under the `ocu` campaign. |

"—" marks are back-filled in P6/P7 because they require downstream sub-projects. Hardware-gated items (macOS/iOS capture that isn't in scope) marked skip-with-reason, never fake-pass.

### 4.3 New challenge bank `challenges/banks/ocu-*.json`

```
ocu-foundation.json        P0: distribution, probe, dispatch
ocu-capture.json           P1: per-source latency, frame-order, DMABuf reuse
ocu-vision.json            P2: ocr accuracy, template FP/FN, diff precision
ocu-interact.json          P3: click precision, verifier soundness, rate cap
ocu-observe.json           P4: hook coverage, ring-buffer correctness
ocu-record.json            P5: clip fidelity, NVENC-vs-x264 equivalence, live-stream latency
ocu-automation.json        P6: end-to-end actions under LLM direction
ocu-tickets.json           P7: every evidence item renders, clips playable, replay scripts run
ocu-adversarial.json       P7: malformed sources, misbehaving LLM, thinker unreachable,
                                slow disk, low-VRAM, pathological frame rates
ocu-cross-platform.json    P6/P7: same test on web + Linux + Android + Android TV
ocu-fixes-validation.json  P7: every OCU bug fixed during development gets regression entry
```

Target: ~180 new entries across all banks.

### 4.4 Non-functional spine — latency + resource budgets

Encoded as asserted constants in `pkg/nexus/native/budget/`:

| Budget | Value | Checked by |
|---|---|---|
| Screen capture local (CPU) | ≤ 15 ms/frame | P1 bench |
| Screen capture remote (DMABuf / scrcpy H264) | ≤ 8 ms/frame | P1 bench |
| Vision local (CPU OpenCV) | ≤ 25 ms/frame | P2 bench |
| Vision remote (thinker CUDA sidecar) | ≤ 8 ms + RTT ≤ 3 ms | P2 bench |
| Interact → verified (local) | ≤ 20 ms | P3 bench |
| Record clip (±5s) extraction | ≤ 200 ms | P5 bench |
| End-to-end action cycle | ≤ 100 ms p50, ≤ 200 ms p95 | P6 bench |
| Orchestrator-host memory ceiling (any HelixQA host, no-GPU path) | ≤ 1.5 GB RSS | soak tests |
| Thinker container memory ceiling | ≤ 4 GB RSS + ≤ 4 GB VRAM | soak tests |

Regressions block PR merge (benchmark bank entry is part of the challenge suite).

### 4.5 Evidence storage layout

```
qa-results/
  session-<unix-ts>/
    pipeline-report.json
    screenshots/                pre- + post-action PNGs (existing)
    videos/                     full session MP4s (existing)
    evidence/                   hook traces, logcat, CDP events, AT-SPI dumps
    frames/                     video-extracted frames (existing)
    ocu/                        [NEW — all OpenClaw Ultimate artifacts]
      captures/                 raw frame ring-buffer samples
      clips/                    ticket-linked ±5s MP4 clips w/ burnt-in timestamps + action arrow
      diffs/                    PNG overlays w/ red change regions
      ocr/                      full-screen OCR JSON + annotated PNG
      elements/                 detected UI element trees (JSON)
      traces/                   per-window hook-event snapshots
      replays/                  re-executable action-chain scripts (.ocu-replay)
      webrtc/                   on-demand live-stream dumps (off by default)
      bench/                    latency measurements per action
```

Retention: existing `FileEvidenceStore.Sweep(RetentionPolicy)` from P4 closure (MaxAge + MaxItems + MaxBytes) applies. New rule: `ocu/webrtc/` rotates at 24h regardless of policy.

### 4.6 Default evidence tier (operator decision §5 of brainstorm)

| # | Artifact | Tier |
|---|---|---|
| 1 | Pre-action screenshot | default |
| 2 | Post-action screenshot | default |
| 3 | Pixel-diff overlay | default |
| 4 | OCR text dump | default |
| 5 | Detected UI-element tree (JSON) | default |
| 6 | Full session video | default |
| 7 | Clipped video segment (±5s, burnt-in timestamp + action arrow) | **killer default** |
| 8 | Hook trace (±5s) | default |
| 9 | Action-chain replay script | default |
| 10 | LLM reasoning excerpt | default |
| 11 | Perf metrics window (CPU/RAM/GPU/net ±10s) | default |
| 12 | Full session hook trace | collected, not linked |
| 13 | Raw DMA-BUF captures | on demand |
| 14 | WebRTC live-stream recording | on demand |
| 15 | Before/after AX tree diff | default |
| 16 | Network request log (HAR) ±5s | default |

### 4.7 Security posture additions

| Vector | Mitigation |
|---|---|
| CUDA sidecar runs with `--gpus=all` | Runs only on host-a.example.com (trusted host); container reuses canonical `security/pkg/ssrf` guard on every outbound URL |
| SSH to thinker uses agent-forwarded keys | No password ever stored; key presence probed at startup with actionable error |
| LD_PRELOAD shim | Compiled per-target; installed to user path; never system dirs; never requires CAP_SYS_PTRACE |
| plthook attack surface | Registrations allowlisted (hardcoded symbol list). Runtime additions require signed orchestrator config |
| WebRTC live stream | WHIP bound to 127.0.0.1 only by default; LAN opt-in requires explicit `--whip-bind=0.0.0.0` + bearer token |
| Clip sensitivity | Evidence store marks clips with sensitivity level; retention respects it; redaction pass for OCR-detected PII via opt-in `--redact-pii` |

---

## §5 — Sequencing, gates, roadmap, risks

### 5.1 Wave sequence

**Wave 1 — Foundation (serial)**

| # | Scope | Exit gate |
|---|---|---|
| P0 | `pkg/nexus/native/{bridge,remote,budget,probe,contracts}` + Containers GPU extension | Contracts compile; `pkg/nexus/native/probe` reports host-a.example.com GPU correctly; Containers backward-compat regression green; vertical-slice dispatch (no-op CUDA call → thinker) works |

**Wave 2 — Middle diamond (parallel)**

| # | Scope | Runs alongside | Exit gate |
|---|---|---|---|
| P1 | Capture (4 sources) | P2, P3, P4 | Each source hits §4.4 latency budget; contract §2.1 satisfied; cross-platform bank green |
| P2 | Vision + CUDA sidecar | P1, P3, P4 | OCR/template/diff within budget local & remote; sidecar image built + pushed |
| P3 | Interact (4 platforms) | P1, P2, P4 | No-sudo install docs shipped; verifier post-action proof rate ≥ 99%; contract §2.3 satisfied |
| P4 | Observe (5 sources) | P1, P2, P3 | Zero-overhead-when-idle assertion holds; ring-buffer snapshot API tested; allowlist enforced |

**Wave 3 — Composition (serial)**

| # | Scope | Exit gate |
|---|---|---|
| P5 | Record + clip + live WebRTC | Segment fidelity bit-identical to raw capture; clip extraction ≤ 200ms; WebRTC off-by-default works |
| P6 | AutomationEngine + Agent bridge | End-to-end action cycle ≤ 100ms p50; LLM never bypassed in any test; legacy pipeline still green as fallback |
| P7 | Tickets + tests + challenges + release | All ten test categories green; 180 new OCU-* bank entries; default evidence tier produced on every ticket; two consecutive green campaign runs; closure-brief items ticked |

### 5.2 Per-sub-project handoff protocol

Each sub-project:
1. **Brainstorm** → `docs/superpowers/specs/YYYY-MM-DD-ocu-p<N>-<topic>-design.md`
2. **Plan** → `docs/superpowers/plans/YYYY-MM-DD-ocu-p<N>-implementation-plan.md` via `writing-plans`
3. **Implement** — TDD per the rigid skill; one failing unit test → pass → next
4. **Review** — `code-reviewer` subagent against the plan + Constitution
5. **Tick closure brief** — any operator-action items closed get ticked
6. **Commit + push all upstreams** — six-remote sweep for main, four for HelixQA, two for Containers

No sub-project begins until the previous wave's exit gate is green and P0 contracts are unchanged.

### 5.3 Release gates

- After Wave 1: tag `v4.0.0-dev.p0`
- After Wave 2: tag `v4.0.0-dev.p1-p4`
- After Wave 3: release `v4.0.0` ("OpenClaw Ultimate")

Each tag must have:
- `CHANGELOG.md` entry listing all sub-project commits
- Updated `OPEN_POINTS_CLOSURE.md` (ticked items, new operator items, Last refresh bumped)
- Two consecutive green `make ocu-e2e-all` captured in `qa-results/`
- Updated `docs/nexus/remaining-work.md`
- Grafana panel updates for new benchmarks

**v4.0.0 final gate (stricter)**:
- govulncheck zero for HelixQA, Containers, Security, catalog-api
- npm audit zero production vulns for catalog-web, desktop, installer
- All ten test categories green for every OCU-* bank
- Full 180-entry challenge suite passes twice consecutively
- Operator-review signoff on closure brief §1–§4 — every OCU-relevant checkbox ticked or explicitly deferred with reason

### 5.4 Program-wide roadmap doc

Location: `helix_qa/docs/nexus/ocu-roadmap.md` (created during P0).

Contents:
- Wave/sub-project table (pending / in-progress / exit-gate-green / closed)
- Contract version table
- Latency budget table with per-release actuals
- Risk register (§5.5 duplicated and kept live)
- Links to each sub-project's spec + plan
- Campaign pointers: `qa-results/` IDs per release tag

Maintenance rule: every commit that changes sub-project state updates this table in the same commit (mirror of Article VI closure-brief rule, scoped to OCU).

### 5.5 Risk register

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| host-a.example.com unreachable during release | medium | high | Local CPU/OpenCL fallback in P2/P5; degraded-mode path exercised by nightly bank entry; release gate requires thinker-on + thinker-off runs both green |
| CUDA container image drift | medium | high | Image pins CUDA 12.2 + runtime validates host driver ≥ 525; mismatch is actionable error, not silent fail |
| go-opencv CGO compile fragility | high | medium | All GPU paths run in container; local dev gets `build tag opencv_stub` so the tree builds on any dev machine without OpenCV installed; test matrix covers both tags |
| Contract churn during P1–P4 parallel wave | medium | high | §2.8 stability rule; any change requires v2 adapter |
| LD_PRELOAD shim break under glibc upgrade | medium | medium | Shim built against widest-compat glibc; per-target e2e; plthook fallback when LD_PRELOAD refuses |
| Evidence-storage disk pressure | high | medium | Retention from P4 closure + new 24h webrtc rule; 48h soak test |
| Constitution drift (someone lets native decide) | low | critical | P6 verifier enforces "every action originates from an LLM call_id"; unit test fails on synthetic actions without one; PR audit-review checklist item |
| Operator SSH key rotation breaks dispatch | low | medium | Probe startup prints actionable error with exact rotation command; closure-brief §1 tracks 90-day rotation |
| Cost blowout (richer LLM prompts) | medium | medium | Existing `pkg/llm/cost_tracker.go` unchanged; P6 adds per-call ceiling; over-budget session downgrades prompt richness, never fails silently |

### 5.6 Immediate next step after spec approval

Per the brainstorming skill's terminal rule: only `writing-plans` is invoked next. That creates the **P0 implementation plan**. P1–P7 get their own later brainstorm + plan cycles.

---

## Sign-off

| Reviewer | Timestamp | Outcome |
|---|---|---|
| Operator (workshop-admin) | 2026-04-17 brainstorm session | All six §0 decisions locked |
| Operator (workshop-admin) | pending | Spec doc review — pending |

When operator approves the spec, `writing-plans` produces `docs/superpowers/plans/2026-04-17-ocu-p0-foundation-plan.md` and P0 implementation begins.

*End of program-level design.*
