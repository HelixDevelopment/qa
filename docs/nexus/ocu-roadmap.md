# OpenClaw Ultimate — Program Roadmap

Living status doc for the 8 OCU sub-projects. Program spec at
`docs/superpowers/specs/2026-04-17-openclaw-ultimate-program-design.md`.

## Status table

| Sub-project | Status | Spec | Plan | Notes |
|---|---|---|---|---|
| P0 Foundation | **CLOSED 2026-04-17** | [spec](../superpowers/specs/2026-04-17-openclaw-ultimate-program-design.md) | [plan](../superpowers/plans/2026-04-17-ocu-p0-foundation-plan.md) | Contracts + Containers GPU extension + vertical-slice CLIs shipped. All ten P0-applicable test categories green. |
| P1 Capture | **CLOSED 2026-04-18** | [spec](../superpowers/specs/2026-04-17-openclaw-ultimate-program-design.md) | [plan](../superpowers/plans/2026-04-17-ocu-p1-capture-plan.md) | Factory + 4 CaptureSource backends (web/CDP, linux/X11, android, androidtv). Stress tested under -race. Per-source bench + audit filed. Bank `ocu-capture.json` shipped. Integration smoke green. Production subprocess wiring deferred to P1.5 via injectable `newFrameProducer`. |
| P2 Vision | **CLOSED 2026-04-18** | [spec](../superpowers/specs/2026-04-17-openclaw-ultimate-program-design.md) | [plan](../superpowers/plans/2026-04-17-ocu-p2-vision-plan.md) | Pipeline + CPU backend + remote-dispatch plumbing via ocuremote.Dispatcher. Real OpenCV CUDA + TensorRT OCR deferred to P2.5 via LocalBackend interface + stub remote path. Stress -race clean. Bank `ocu-vision.json` (13 entries) shipped. Integration smoke green. |
| P3 Interact | **CLOSED 2026-04-18** | [spec](../superpowers/specs/2026-04-17-openclaw-ultimate-program-design.md) | [plan](../superpowers/plans/2026-04-17-ocu-p3-interact-plan.md) | Factory + 4 Interactor backends (linux/uinput, web/CDP, android, androidtv). Injectable newInjector sentinel; production returns ErrNotWired. Verifier hook (verify.Wrap + NoOp). 100-goroutine stress -race clean. Bench: Wrap_Click ~86 ns/op 0 allocs (i7-1165G7). Bank `ocu-interact.json` (19 entries) shipped. Integration smoke green. Real uinput/CDP/ADB wiring deferred to P3.5. |
| P4 Observe | **CLOSED 2026-04-18** | [spec](../superpowers/specs/2026-04-17-openclaw-ultimate-program-design.md) | [plan](../superpowers/plans/2026-04-17-ocu-p4-observe-plan.md) | Factory + 5 Observer backends (ld_preload, plthook, dbus, cdp, ax_tree) + shared BaseObserver + RingBuffer. Injectable producerFunc sentinel; production returns ErrNotWired. 100-goroutine stress -race clean. Bank `ocu-observe.json` (21 entries) shipped. Integration smoke green. Real shim/hook/bus wiring deferred to P4.5. |
| P5 Record | **CLOSED 2026-04-18** | [spec](../superpowers/specs/2026-04-17-openclaw-ultimate-program-design.md) | [plan](../superpowers/plans/2026-04-17-ocu-p5-record-plan.md) | Recorder + 3 encoder stubs (x264/nvenc/vaapi) + FrameRing + clipper (ND-JSON, MKV deferred to P5.5) + WebRTC/WHIP publisher off by default (ErrNotWired). Priority-drain goroutine prevents frame loss on Stop(). 100-goroutine stress -race clean. Bank `ocu-record.json` (21 entries) shipped. Integration smoke green. FFmpeg/NVENC CGO + real WHIP deferred to P5.5. |
| P6 Automation | **CLOSED 2026-04-18** | [spec](../superpowers/specs/2026-04-17-openclaw-ultimate-program-design.md) | [plan](../superpowers/plans/2026-04-17-ocu-p6-automation-plan.md) | Engine composes P1–P5 (capture/vision/interact/observe/record) behind single Perform(). Action/Result types + PixelVerifier + MultiVerifier + Bridge into pkg/nexus/agent. LLM remains sole decider — Engine is pure dispatcher, Bridge is one-liner adapter. 100-goroutine stress -race clean. Bank `ocu-automation.json` (22 entries) shipped. Integration smoke green. |
| P7 Tickets+tests | **CLOSED 2026-04-18** | [spec](../superpowers/specs/2026-04-17-openclaw-ultimate-program-design.md) | [plan](../superpowers/plans/2026-04-17-ocu-p7-tickets-plan.md) | pkg/ticket: 12 EvidenceKind constants + Evidence struct + FromAutomationResult + BuildReplayScript + .ocu-replay DSL. 4 cross-cutting banks (tickets 36, adversarial 20, cross-platform 15, fixes-validation 10 = 81 entries). 10-category campaign script green. Pre-existing vet/fmt/integration bugs fixed. v4.0.0 released. |
| P1.5/P3.5 web+android wiring | **CLOSED 2026-04-18** | — | — | Production chromedp backend for web capture (PNG→BGRA8, ErrChromeNotFound sentinel, HELIXQA_CAPTURE_WEB_STUB kill-switch) + web interact (MouseClickXY/KeyEvent/mouseWheel/drag via CDP, HELIXQA_INTERACT_WEB_STUB). Production ADB backend for android capture (screenrecord stdout pipe, H.264 NAL splitter, HELIXQA_CAPTURE_ANDROID_STUB) + android interact (tap/swipe/text/keyevent, KeyCode→Android-keycode map, HELIXQA_INTERACT_ANDROID_STUB). HELIXQA_ADB_SERIAL for multi-device. All fall back to ErrNotWired when binary absent or stub env set. 40 nexus packages -race green, vet+gofmt+govulncheck clean. Linux uinput/xwd, FFmpeg NVENC/VAAPI, LD_PRELOAD/plthook/dbus/ax_tree still stubbed. |
| P1.5/P3.5 Linux wiring | **CLOSED 2026-04-18** | — | — | Production xwd+convert pipeline for Linux capture (xwd→gnome-screenshot→grim fallback chain, BMP→BGRA8 decoder, pngToBGRA8 helper, HELIXQA_CAPTURE_LINUX_STUB kill-switch, DISPLAY/WAYLAND_DISPLAY guard). Production xdotool/ydotool backend for Linux interact (xdotool X11 preferred, ydotool Wayland fallback, KeyCode→X11-keysym map for 10 keys, HELIXQA_INTERACT_LINUX_STUB kill-switch). Raw /dev/uinput path deferred; xdotool covers 95% of QA interactions no-sudo. Operator setup in docs/ocu-udev-setup.md. 44 nexus packages -race green, vet+gofmt clean. |
| P2.5 CPU vision real Diff+Analyze | **CLOSED 2026-04-18** | — | — | Pure-Go per-pixel |Δ| diff with contiguous flood-fill into ChangeRegions (BGRA8, threshold 0.05). Sobel X+Y edge detection on luminance → contiguous UIElements (Kind "contour", Source "cv"). HELIXQA_VISION_CPU_STUB=1 restores empty-stub behaviour. Frames with nil Data degrade gracefully. No CGO. 5 new tests (SamePixels, DifferentPixels, EmptyData, EdgeDetection, StubEnv) + all prior tests green, -race clean. |
| P4.5 D-Bus + CDP observers | **CLOSED 2026-04-18** | — | — | D-Bus: godbus/dbus/v5 ConnectSessionBus + AddMatchSignal per target.Labels["interface"]; signalToEvent pure translation (Sender/Path/Name/Body→Payload). HELIXQA_OBSERVE_DBUS_STUB=1 or absent DBUS_SESSION_BUS_ADDRESS → ErrNotWired. CDP: chromedp ListenTarget subscribing Network.responseReceived + Runtime.consoleAPICalled; BrowserCandidates slice searched via exec.LookPath; HELIXQA_OBSERVE_CDP_STUB=1 or no browser → ErrNotWired. 3 new tests each; all prior mock-producer tests green, -race clean. |
| P5.5 x264 encoder via ffmpeg | **CLOSED 2026-04-18** | — | — | NewProductionEncoder spawns `ffmpeg -f rawvideo -pix_fmt bgra -c:v libx264 -preset ultrafast -f mp4 -movflags frag_keyframe+empty_moov pipe:1`; frames written to stdin; Close() closes stdin + waits. FFmpegCandidates + BuildFFmpegArgs exported for test overrides. HELIXQA_RECORD_X264_STUB=1 or absent ffmpeg → ErrNotWired. Default productionEncoder stub via factory preserves prior ErrNotWired behaviour. 3 new tests + all prior tests green, -race clean. Remaining: NVENC, VAAPI, real WHIP. |
| P4.5 AT-SPI observer | **CLOSED 2026-04-18** | — | — | Resolves AT-SPI2 bus address via org.a11y.Bus.GetAddress on session bus, dials it via dbus.Dial + Auth + Hello, subscribes to org.a11y.atspi.Event.Object + Event.Window via AddMatchSignal. signalToAXEvent pure translation (Sender/Path/Name/Body → Payload, Kind=EventKindAXTree). HELIXQA_OBSERVE_AX_STUB=1 or absent DBUS_SESSION_BUS_ADDRESS → ErrNotWired. 3 new tests (MissingBus, StubEnv, SignalMapping) + all prior mock-producer tests green, -race clean. |
| P5.5 VAAPI encoder via ffmpeg | **CLOSED 2026-04-18** | — | — | NewProductionEncoder resolves ffmpeg via FFmpegCandidates + device node via cfg.DeviceNode/HELIXQA_VAAPI_DEVICE/defaultDeviceNode(/dev/dri/renderD128). Spawns `ffmpeg -init_hw_device vaapi=intel:<device> -filter_hw_device intel -f rawvideo -pix_fmt bgra -vf format=nv12,hwupload -c:v h264_vaapi -qp 23 -f mp4 -movflags frag_keyframe+empty_moov pipe:1`; frames written to stdin; Close() closes stdin + waits. BuildFFmpegArgs + FFmpegCandidates exported for tests. HELIXQA_RECORD_VAAPI_STUB=1, absent ffmpeg, or missing device node → ErrNotWired. 4 new tests + all prior factory/stub tests green, -race clean. Remaining: NVENC (host-a.example.com GPU container), real WHIP. |
| P5.5 NVENC encoder via remote dispatch | **CLOSED 2026-04-18** | — | — | NewProductionEncoder accepts a contracts.Dispatcher; lazily resolves a KindNVENC Worker (MinVRAM 1024 MiB) on first Encode call. Per-frame call passes *structpb.Value placeholder (P5.6 replaces with generated NVENCRequest from proto/nvenc.proto). Close() closes the Worker. HELIXQA_RECORD_NVENC_STUB=1 or nil Dispatcher → ErrNotWired; Dispatcher error (no GPU host) → ErrNotWired. Default productionEncoder factory stub preserved. 6 tests (StubEnv, NilDispatcher, HappyPath 5 encodes, ResolveError, WorkerCallError, IdempotentClose) + all prior factory/stub tests green, -race clean. Real gRPC sidecar on host-a.example.com is P5.6 operator scope. |
| P4.5 LD_PRELOAD loader | **CLOSED 2026-04-18** | — | — | productionProducer.Produce resolves shim path from target.Labels["shim_path"] or HELIXQA_LD_SHIM; stat-checks it; creates FIFO via syscall.Mkfifo; launches target via exec.CommandContext with LD_PRELOAD=<shim> + HELIXQA_LD_SHIM_FIFO=<fifo>; streams JSON Lines from FIFO into EventKindHook Events. parseShimLine (exported) decodes {ts_ns,fn,arg} → Event.Payload. Observer.Start short-circuits to ErrNotWired when stub active or shim absent. fifo_unix.go wraps syscall.Mkfifo behind build tag (!windows). C shim template + operator README at docs/hooks/. 5 tests (MissingShim, StubEnv, ParseShimLine_JSON, ParseShimLine_MalformedJSON, existing MockProducesEvents) + factory/init tests green, -race clean. |

## Contract versions

| Contract | Version | Locked by |
|---|---|---|
| capture.go | v1 | P0 |
| vision.go | v1 | P0 |
| interact.go | v1 | P0 |
| observe.go | v1 | P0 |
| record.go | v1 | P0 |
| remote.go | v1 | P0 |

## Budgets vs actuals

| Budget | Limit | P0 actual | Status |
|---|---|---|---|
| ProbeLocal | n/a (not budgeted) | ~133 µs / 366 allocs / 46 KB (laptop i7-1165G7) | informational |
| web/CDP Open→frame→Close | n/a (not budgeted) | ~10.2 ms / 10 allocs / 4758 B (laptop i7-1165G7) | mock producer; 10 ms dominated by Start() readiness sleep |
| linux/X11 Open→frame→Close | n/a (not budgeted) | ~10.5 ms / 10 allocs / 4696 B (laptop i7-1165G7) | mock producer; same sleep |
| android/ADB Open→frame→Close | n/a (not budgeted) | ~10.3 ms / 12 allocs / 5095 B (laptop i7-1165G7) | mock producer; 2 extra allocs from kind string |

| CPU Backend Analyze (stub) | n/a (not budgeted) | ~3.8 ns / 0 allocs / 0 B (laptop i7-1165G7) | pure-Go stub; real OpenCV CUDA lands in P2.5 |
| CPU Backend Diff (stub) | n/a (not budgeted) | ~48 ns / 1 alloc / 48 B (laptop i7-1165G7) | same-shape fast-path; real pixel diff in P2.5 |

| interact/verify Wrap_Click | n/a (not budgeted) | ~86 ns/op / 0 allocs / 83 B (laptop i7-1165G7) | NoOp verifier; 0 allocs in the hot path |
| interact/verify NoOp_After | n/a (not budgeted) | ~0.34 ns/op / 0 allocs / 0 B (laptop i7-1165G7) | pure no-op; effectively free |
| interact/verify Wrap_AllMethods (5 calls) | n/a (not budgeted) | ~488 ns/op / 0 allocs / 424 B (laptop i7-1165G7) | 5-method sequence through Wrap; 0 allocs |

P4–P7 append their own actuals as benches land.

## Risk register

Live copy of program-spec §5.5. Update in-place whenever likelihood or impact changes.

## Maintenance

Per Constitution Article VI: every commit that changes sub-project state must update this table in the SAME commit.
