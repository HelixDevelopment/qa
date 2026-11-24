# OpenClawing 4 — Phase 2 Kickoff

**Date:** 2026-04-20
**Status:** READY TO START
**Prerequisite:** Phase 1 closed — see `OpenClawing4-Phase1-Closure.md`
**Plan reference:** `OpenClawing4.md` §5.3, §5.4, §5.8, §5.9

---

## 1. Phase 2 goal (reminder)

Per `OpenClawing4.md` §8:

> Unified AX tree + perception tiers (dHash → SSIM → DreamSim) + BOCPD
> stagnation.

Translated to deliverables:

- **Deterministic target resolution** across every platform via a shared
  `Node{Role, Name, Bounds, ...}` accessibility tree.
- **Tier-1 fast change-detection**: per-frame dHash comparison in
  < 5 ms CPU (`pkg/vision/hash`).
- **Tier-2 structural verification**: SSIM / MS-SSIM on suspected
  stagnation segments in ~3 ms (`pkg/vision/perceptual`).
- **Tier-3 human-aligned tiebreaker**: DreamSim against Triton-hosted
  model (`pkg/vision/perceptual/dreamsim`).
- **Online change-point detection**: BOCPD in `pkg/autonomous/stagnation`
  extended to replace the current window-only detector.
- **Post-session segmentation**: ruptures PELT via `pkg/analysis/pelt`.
- **Visual-regression tooling**: `pkg/regression/pixelmatch` Go port +
  CIEDE2000 + HTML reporter.

## 2. Scaffolding already in place (Phase 1 M26)

Eight new packages with doc.go stubs plus planned interface names:

| Package | Planned interface | Commit |
|---|---|---|
| `pkg/vision/hash` | `Hasher{Hash, Distance}` | `c18f779` |
| `pkg/vision/perceptual` | `Comparator{Compare}` | `c18f779` |
| `pkg/vision/flow` | `Computer{Compute}` | `c18f779` |
| `pkg/vision/template` | `Matcher{Match}` | `c18f779` |
| `pkg/vision/text` | `Detector{Detect}` | `c18f779` |
| `pkg/analysis/pelt` | `Segmenter{Segment}` | `c18f779` |
| `pkg/regression` | `Differ{Diff}` | `c18f779` |
| `pkg/nexus/observe/axtree` | `Snapshotter{Snapshot}` + `Node` | `c18f779` |

All build + `go test` cleanly today. Nothing is implemented — Phase 2
starts by filling these interfaces.

## 3. Implementation sequence (recommended)

Dependencies-first ordering:

| Step | Package | Depends on | Est. weeks |
|---|---|---|---|
| 2.1 | `pkg/vision/hash/dhash.go` (primary + tests) | corona10/goimagehash | 0.5 |
| 2.2 | `pkg/autonomous/stagnation.go` BOCPD extension | `pkg/vision/hash` | 0.5 |
| 2.3 | `pkg/vision/perceptual/ssim.go` via gocv | OpenCV + gocv | 0.5 |
| 2.4 | `pkg/regression/pixelmatch.go` Go port | standalone | 0.5 |
| 2.5 | `pkg/nexus/observe/axtree/linux.go` AT-SPI2 | godbus | 0.5 |
| 2.6 | `pkg/nexus/observe/axtree/web.go` CDP | go-rod | 0.5 |
| 2.7 | `pkg/nexus/observe/axtree/android.go` UiAutomator2 | existing driver | 0.5 |
| 2.8 | `pkg/vision/perceptual/dreamsim.go` REST client | Triton-hosted model | 0.5 |
| 2.9 | `pkg/analysis/pelt/client.go` Python sidecar | ruptures | 0.5 |
| 2.10 | Phase 1 tail: `pkg/navigator/linux/libei/ei_client.go` | reis (Rust) OR pure-Go port | 2–3 |

Total Phase 2 estimate: **~6–8 weeks** (tail-heavy due to EI client).

## 4. Unblocks and blockers

### Unblocked by Phase 1

- Portal / capture / scrcpy plumbing ready — Phase 2 plugs perception
  into `Source.Frames()` pipelines without rewiring the capture path.
- `pkg/bridge/dbusportal` ready for AT-SPI2 a11y-bus access in
  `pkg/nexus/observe/axtree/linux.go` (separate bus; see
  `gitlab.gnome.org/GNOME/at-spi2-core` docs).
- `pkg/capture/android.DirectSource` ready; Android AX snapshotter just
  wraps the existing UiAutomator2 driver.

### Still blocked on external work

- DreamSim deployment to Triton on the GPU host (operator action).
- ruptures Python sidecar container build (operator action).
- `reis` cargo install for the EI wire client (operator action).

### New Phase 2-specific blockers

- OpenCV dev headers on the build host (for `gocv` CGO path). Same story
  as Phase 1 native sidecars — documented in each package's future
  README.md.

## 5. Test/bank/challenge expectations

Every Phase 2 commit follows the Phase 1 pattern:

- **Banks**: new entries in `banks/phase2-gocore.yaml` (to be created)
  mirror `banks/phase1-gocore.yaml` structure.
- **Feature banks**: `banks/stagnation.yaml`, `banks/regression.yaml`,
  `banks/axtree.yaml`.
- **Challenges**: `HQA-PHASE2-GOCORE-001` + per-component challenges in
  `challenges/config/helixqa-validation.yaml`.
- **Fixes**: Any bug discovered → unit test + fixes-validation entry +
  HelixQA bank entry + challenge (Article VII 4-artefact rule).

## 6. Acceptance criteria (Article V per component)

Same bar as Phase 1:

1. Unit ≥ 95% branch coverage in the new package.
2. Integration: at least one test using real fixtures (e.g. canned 1080p
   PNGs for perceptual; captured AX tree dumps for axtree).
3. E2E: Phase 2 pipeline integrated into `pkg/autonomous/coordinator.go`
   at least once.
4. Full automation: every test invokable via `go test`.
5. Stress: ≥ 1 M hash comparisons / 10 k SSIM computations benchmark.
6. Security: `govulncheck` + no-sudo hook green.
7. DDoS / rate-limit: k6 saturation of any long-running sidecar.
8. Benchmarking: dHash < 5 ms / frame @ 1080p CPU; SSIM < 5 ms / 480p
   luma; DreamSim < 200 ms GPU round-trip.
9. Challenges: registered in `helixqa-validation.yaml`.
10. HelixQA: bank entries per component.

## 7. First concrete task for the next session

```
pkg/vision/hash/dhash.go
pkg/vision/hash/dhash_test.go
```

Wraps `corona10/goimagehash` dHash-64 + dHash-256 with:

```go
type DHashKind int
const (
    DHash64 DHashKind = iota
    DHash256
)

type DHasher struct { Kind DHashKind }

func (h DHasher) Hash(img image.Image) (uint64, error)    // for DHash64
func (h DHasher) Hash256(img image.Image) (*BigHash, error) // for DHash256
func (h DHasher) Distance(a, b uint64) int
```

Tests: canned 1080p PNGs (identical / shifted-by-1-pixel / completely
different) + micro-benchmark asserting < 5 ms / frame on CPU.

Reference: `OpenClawing4.md` §5.8 tier-1 primitive.

## 8. Operator actions queued for Phase 2 start

- [ ] Build `cmd/helixqa-capture-linux`, `-kmsgrab`, `-input` on target
      hosts (READMEs in each directory).
- [ ] Run `scripts/fetch-scrcpy-server.sh` after setting the real
      SCRCPY_SHA256 value in the script.
- [ ] Deploy DreamSim ONNX to the Triton instance on host-a.example.com.
- [ ] Install `ruptures` Python package + expose via a small gRPC
      sidecar.
- [ ] `cargo install reis` on a Rust-capable build host (or pull the
      binary from a distribution image).

These are tracked in `docs/OPEN_POINTS_CLOSURE.md` §10 (operator-action
items) after this commit refreshes the doc.

## 9. Sign-off

Phase 2 is **ready to start** from `c18f779` (scaffolds in place) onwards.
The next Claude / operator session picks one of the Step 2.x items from
§3 and runs it per the Phase-1 commit-per-milestone cadence.
