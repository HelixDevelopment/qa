# HelixQA Operator-Action Deliverables

The Go-side HelixQA runtime is feature-complete. The components
below are **external deliverables** — sidecars, model weights,
platform binaries — that an operator must build/deploy before the
corresponding integration tests can run. Each sub-directory's
`README.md` contains the full wire contract, build recipe, and
acceptance criteria.

## Status board

| Sidecar | Language | Host | Go client | Live integration test |
|---|---|---|---|---|
| [`helixqa-omniparser`](./helixqa-omniparser/README.md) | Python | GPU (host-a.example.com) | ✅ `pkg/agent/omniparser` | `PH3-OMNIPARSER-INT-001` |
| [`helixqa-text`](./helixqa-text/README.md) | Python | GPU / CPU | ✅ `pkg/vision/text` | `PH2-TEXT-INT-001` |
| [`helixqa-dreamsim`](./helixqa-dreamsim/README.md) | Python / Triton | GPU (host-a.example.com) | ✅ `pkg/vision/perceptual/dreamsim` | `PH2-DREAMSIM-INT-001` |
| [`helixqa-lpips`](./helixqa-lpips/README.md) | Python / Triton | GPU | ✅ `pkg/vision/perceptual/lpips` | (same Triton instance) |
| [`helixqa-uitars`](./helixqa-uitars/README.md) | llama.cpp | GPU (host-a.example.com:18100) | ✅ `pkg/agent/uitars` | `PH3-UITARS-INT-001` |
| [`helixqa-frida-bridge`](./helixqa-frida-bridge/README.md) | Python | Dev host + USB device | ✅ `pkg/observe/frida` | (manual) |
| [`helixqa-axtree-darwin`](./helixqa-axtree-darwin/README.md) | Swift | macOS | ✅ `pkg/nexus/observe/axtree/darwin.go` | `PH6-AXTREE-DARWIN-INT-001` |
| [`helixqa-axtree-windows`](./helixqa-axtree-windows/README.md) | Go (go-ole) / C# | Windows | ✅ `pkg/nexus/observe/axtree/windows.go` | `PH6-AXTREE-WINDOWS-INT-001` |

Existing C/Go sidecars (already shipped):

| Sidecar | Source | Status |
|---|---|---|
| `helixqa-capture-linux` | C + GStreamer + libpipewire | README recipe (M25) |
| `helixqa-kmsgrab` | C + libdrm + VA-API | README recipe (M25) |
| `helixqa-input` | Rust (reis) | README recipe (M25) |
| `helixqa-x11grab` | Go (pure) | Shipped binary |

## Ordering for a fresh deployment

The most valuable deploy order for a brand-new operator setup:

1. **`helixqa-uitars`** — the VLM that drives the whole loop.
2. **`helixqa-omniparser`** — enables the grounding layer to
   snap clicks to real UI elements.
3. **`helixqa-text`** — text-region detection + OCR for UI
   labels the VLM might miss.
4. **`helixqa-dreamsim`** (+ `helixqa-lpips` alongside) — tier-3
   perceptual similarity for stagnation detection on long
   sessions.
5. **`helixqa-frida-bridge`** — dynamic instrumentation for
   security + behavior observability.
6. Platform-specific `helixqa-axtree-*` — if QA targets cross
   macOS / Windows / iOS.

After steps 1-3 the minimum viable Phase-3 agent stack is operational
end-to-end against a live target.

## Wire contract stability

Every sidecar's wire is defined by the corresponding Go client's
test fixtures + the sidecar's `README.md`. Backwards-incompatible
wire changes require a bumped version number path
(`/v2/...`) and a compat shim on the Go side. The vision is
that `pkg/gpu/infer` eventually absorbs the Triton-backed wire
for `dreamsim` + `lpips` + any future GPU model, leaving the
pbtxt `config.pbtxt` + model files as the only per-model
deliverables.

## CI integration

Each live integration test is tagged `manual` in the Phase-2/3/6
banks — they do not run unattended. When a sidecar is deployed:

1. Update `docs/OPEN_POINTS_CLOSURE.md` §10.3 with the deployed
   endpoint URL and tick the corresponding checkbox.
2. Run the `PH*-INT-*` case manually with HelixQA pointed at the
   live endpoint.
3. Cross-reference the acceptance criteria in the sidecar's
   `README.md`.
4. Commit the OPEN_POINTS_CLOSURE.md update in the same session
   that verified the deployment.
