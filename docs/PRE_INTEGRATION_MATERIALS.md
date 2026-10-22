# HelixQA — Pre-Integration Materials

**Revision:** 1
**Last modified:** 2026-07-15T11:16:54Z
**Purpose:** Consolidated pre-integration materials (gate before any integration/deployment work).

> This document is a **consolidation + verification pass** over HelixQA's
> existing rich `docs/` set. It does not restate the architecture in full — it
> cross-references the canonical docs by path and fills the one cross-cut
> (ports + health) that had no single dedicated document. Every statement below
> is grounded in files present in this repository at the revision above; items
> that could not be determined from the repository are marked `UNKNOWN:`.

---

## 1. Purpose / What it is

HelixQA is an **anti-bluff QA orchestration framework** for cross-platform
testing with real-time crash detection, step validation, evidence collection,
and automated ticket generation (`README.md` — "What this submodule is").

It is a **library-and-CLI** project, not a hosted service: the design centre is
the constitution's §11.4 Operative Rule — *"the bar for shipping is not 'tests
pass' but 'users can use the feature'"* — so every PASS it emits must carry
positive runtime evidence captured during execution (`README.md`;
`CONSTITUTION.md` — "Anti-bluff — the binding mandate for this module";
`docs/ANTI_BLUFF.md`).

HelixQA is **project-agnostic** and reusable by any consuming project
(`CONSTITUTION.md` §11.4.28 decoupling). Consumer-specific test data lives as
YAML banks under `banks/` (data owned by the caller), while the bank-loading and
orchestration machinery stays generic.

Canonical entry docs (do not duplicate — read directly):
- `README.md` — overview, features, usage, test-bank format.
- `docs/QUICK_START.md` — "running in under 5 minutes".
- `USER_GUIDE_AUTONOMOUS.md` / `docs/USER_MANUAL.md` — autonomous QA session guide.
- `CHANGELOG.md`, `CONTRIBUTING.md`.

---

## 2. Architecture overview

**Stack:** Go — `module digital.vasic.helixqa`, `go 1.26` (`go.mod`); README /
guides state a Go 1.24+ floor; the container build uses `golang:1.25-bookworm`
(`Dockerfile`). Built with `CGO_ENABLED=1` (SQLite driver
`github.com/mattn/go-sqlite3`). Test framework `github.com/stretchr/testify`;
config/data format `gopkg.in/yaml.v3`. Single-module Go workspace
(`go.work` → `use .`).

**Canonical architecture docs** (cross-referenced, not restated):
- `ARCHITECTURE.md` — module dependency graph + data-flow diagram + per-package
  responsibilities.
- `docs/architecture.md` — package table + orchestrator flow (mermaid).
- `OPENCV_INTEGRATION_ARCHITECTURE.md`, `docs/COMPREHENSIVE_VISION_INTEGRATION_PLAN.md`,
  `docs/REALTIME_VIDEO_PIPELINE_PLAN.md` — vision/video subsystems.

**Layering** (`ARCHITECTURE.md`): HelixQA (orchestration) → `Challenges` (test
execution) → `Containers` (infrastructure). HelixQA composes those modules
rather than reimplementing them.

**Real components (grounded in `pkg/`, `internal/`, `cmd/`):**

| Area | Dirs (real) |
|------|-------------|
| Orchestration brain | `pkg/orchestrator`, `pkg/autonomous` (SessionCoordinator / PlatformWorker / PhaseManager), `pkg/maestro`, `pkg/planning` |
| Test banks | `pkg/testbank`, `pkg/challengegen`; YAML banks in `banks/` |
| Detection / validation | `pkg/detector`, `pkg/validator`, `pkg/validators`, `pkg/issuedetector`, `pkg/regression` |
| Evidence / reporting | `pkg/evidence`, `pkg/reporter`, `pkg/ticket`, `pkg/recordingqa`, `pkg/session`, `pkg/replay` |
| Navigation / capture | `pkg/navigator`, `pkg/visionnav`, `pkg/capture`, `pkg/screenshot`, `pkg/controller`, `pkg/input` (via cmd) |
| Media analysis | `pkg/vision`, `pkg/visual`, `pkg/video`, `pkg/audio`, `pkg/gpu`, `pkg/gst`, `pkg/analysis` |
| LLM | `pkg/llm` (provider abstraction, adaptive fallback, cost tracking) |
| Infra / distribution | `pkg/infra`, `pkg/distributed`, `pkg/conduit`, `pkg/streaming`, `pkg/bridge`, `pkg/bridges`, `pkg/nexus` |
| Support | `pkg/config`, `pkg/types`, `pkg/memory`, `pkg/learning`, `pkg/discovery`, `pkg/observe`, `pkg/reproduce`, `pkg/i18n`, `pkg/opensource` |
| Internal | `internal/visionserver` (standalone vision HTTP server component) |

**Test-bank concept** (`README.md` "Test Bank Format"; `pkg/testbank`): QA test
cases are declared in YAML banks (id, name, category, priority, platform
targeting, steps with expected outcomes, documentation refs). `banks/` holds a
large set (generic + consumer-supplied, e.g. `full-qa-*`, `nexus-*`, `ocu-*`,
and consumer banks such as `atmosphere*.yaml` — consumer-owned data per §11.4.28).

**Autonomous QA session** (`pkg/autonomous`; `USER_GUIDE_AUTONOMOUS.md`;
`docs/architecture.md`): an LLM-powered session drives a target across platforms
(Android / Android TV / Web / Desktop), navigating the UI, detecting
visual/UX/accessibility/functional issues, collecting evidence (screenshots,
video, logs, traces), and generating tickets — with a PASS/FAIL verdict.

---

## 3. Dependencies

### 3.1 Own-org Go modules (dependency manifest)

`helix-deps.yaml` is the §11.4.31 submodule dependency manifest. It enumerates
**8** own-org Go modules wired via the `go.mod` `replace` block:

| Dep | Org (per manifest) | Go replace target |
|-----|--------------------|-------------------|
| Challenges | vasic-digital | `digital.vasic.challenges` |
| Containers | vasic-digital | `digital.vasic.containers` |
| DocProcessor | HelixDevelopment | `digital.vasic.docprocessor` |
| LLMOrchestrator | HelixDevelopment | `digital.vasic.llmorchestrator` |
| LLMProvider | HelixDevelopment | `digital.vasic.llmprovider` |
| LLMsVerifier | vasic-digital | `digital.vasic.llmsverifier` |
| security | vasic-digital | `digital.vasic.security` |
| VisionEngine | HelixDevelopment | `digital.vasic.visionengine` |

`transitive_handling: {recursive: true, conflict_resolution: operator-required}`
(`helix-deps.yaml`). Per §11.4.28(C) these are consumed at the **parent
project's root** (flat layout, no nested own-org `.gitmodules`).

NOTE (factual): the `go.mod` top-level `require` block lists 7 of these
(`challenges`, `containers`, `docprocessor`, `llmorchestrator`, `llmprovider`,
`security`, `visionengine`); the manifest additionally lists `LLMsVerifier`
(`digital.vasic.llmsverifier`, resolved via the `replace` block). Prerequisites
in `README.md` name the two load-bearing siblings `../challenges` and
`../containers`.

### 3.2 Third-party git submodules (`.gitmodules`)

25 third-party open-source tool submodules (flat `<root>/submodules/<snake_name>/`,
legacy `tools/opensource/`, list in `docs/flat-tools-manifest.tsv`), e.g. `scrcpy`, `appium`, `midscene`, `moondream`,
`ui-tars` / `ui-tars-desktop`, `perfetto`, `chroma`, `marker`, `docling`,
`browser-use`, `skyvern`, `stagehand`, `unstructured`, `llama-index`,
`kiwi-tcms`, `signoz`, `redroid`, `docker-android`, `leakcanary`, `allure2`,
`shortest`, `testdriverai`, `mem0`, `anthropic-quickstarts`.
These are upstream third-party repos (§11.4.28 exempt from own-org rules).
Inventory / licences: `docs/licences-inventory.md`, `docs/opensource-references.md`.

### 3.3 External Go libraries + infra

Direct external Go libs (`go.mod`): `github.com/mattn/go-sqlite3`,
`github.com/stretchr/testify`, `gopkg.in/yaml.v3` (plus many indirect —
Docker/moby client, chromedp, Prometheus client, etc.). Runtime tools the
binary/guides expect: `ffmpeg`, `adb` (android-tools-adb), Node.js + Playwright
(web), Tesseract/Whisper helpers, and at least one LLM API key
(`docs/QUICK_START.md`, `USER_GUIDE_AUTONOMOUS.md`, `.env.example`).
Optional production-stack infra: see §4.

---

## 4. Deploy / Distribution design

### 4.1 Build

- **Local binary:** `make build` → `bin/helixqa` (`Makefile`:
  `go build -o bin/helixqa ./cmd/helixqa`), or `go install
  digital.vasic.helixqa/cmd/helixqa@latest` (`README.md`), or
  `go build -o bin/helixqa ./cmd/helixqa` (`docs/QUICK_START.md`).
- **Make targets** (`Makefile`): `build`, `install`, `test`, `test-race`,
  `test-cover`, `vet`, `lint`, `anti-bluff*`, `qa-all`, `challenge`. An API-key
  loader (`scripts/load_api_keys.sh`) is sourced before build/test recipes.

### 4.2 Container image (root `Dockerfile`)

Two-stage: builder on `golang:1.25-bookworm` (`CGO_ENABLED=1 go build -o
/helixqa ./cmd/helixqa`) → runtime on `debian:bookworm-slim` with `ffmpeg` +
`android-tools-adb` + `ca-certificates`. Runs as **unprivileged** user
`10001:10001` (DS-0002). `ENTRYPOINT ["helixqa"]`, `CMD ["autonomous",
"--project", "/project", "--platforms", "all"]`. The header comment states the
binary "serves no privileged port" and operates on the bind-mounted `/project`
tree. (Rootless container runtime is mandated per constitution §11.4.161.)

### 4.3 Production stack (`docker-compose.stack.yml`)

An optional full pipeline: `mediamtx` (streaming), `nats` (JetStream state),
`ollama` (LLM serving), `helixqa-api`, `helixqa-vision`, `prometheus`,
`grafana`, `redis`. Each service declares explicit `mem_limit` / `pids_limit` /
`oom_score_adj` and healthchecks. Supporting Dockerfiles present in-tree:
`docker/mediamtx/`, `docker/monitoring/`, `docker/base-opencv-gstreamer/`.

> **Gap (factual, not a guess):** the compose file references build contexts
> `docker/api/Dockerfile` (service `helixqa-api`) and `docker/vision/Dockerfile`
> (service `helixqa-vision`), and **neither `docker/api/` nor `docker/vision/`
> exists in this repository at this revision**. Those two stack services cannot
> be built from the repo as-is; the primary supported distribution is the root
> `Dockerfile` (single `helixqa` CLI) and the `make build` binary.

### 4.4 Distribution slice

The distributable artifact is the single `helixqa` CLI binary (§7). The repo
additionally builds **~45 auxiliary command binaries** under `cmd/` (e.g.
`helixqa-bridge`, `recording-analyzer`, `qa-audio-analyze`/`qa-audio-probe`,
`helixqa-concrete-runner`, `helixqa-bank-session`, `ocu-probe`, and a family of
`helixqa-verify-*` validation harnesses); several `cmd/*` dirs are
README-only / platform-specific stubs (`helixqa-axtree-*`, `helixqa-kmsgrab`,
`helixqa-omniparser`, `helixqa-uitars`, etc.).

---

## 5. Ports

The primary `helixqa` CLI binary is a **client/runner** and does **not** bind a
long-running server port (root `Dockerfile` header: "serves no privileged
port"). The `helixqa http` subcommand is an HTTP **client** against an
operator-supplied `-base-url` (example `http://127.0.0.1:8080`,
`cmd/helixqa/http.go`), never a hardcoded server.

Server-side listeners that DO exist in-tree:

| Component | Default listen | Config source | Cite |
|-----------|----------------|---------------|------|
| `internal/visionserver` (vision HTTP server) | `:8090` | env `HELIX_VISION_LISTEN_ADDR` | `internal/visionserver/config.go:97`, `server.go` |
| `cmd/helixqa-bridge` (loopback bridge) | `127.0.0.1:7842` | `-listen` flag (loopback-validated) | `cmd/helixqa-bridge/main.go:72,118` |

Production-stack service ports (`docker-compose.stack.yml`, only if the stack is
run): mediamtx `8554/1935/8888/8889/8189/9998`; nats `4222/8222/6222`; ollama
`11434`; `helixqa-api` `8080`; prometheus `9090`; grafana `3000`; redis `6379`.
(The `helixqa-api` :8080 mapping depends on the missing `docker/api` build
context — see §4.3.)

`UNKNOWN:` no importer of `internal/visionserver` was found in `cmd/` or `pkg/`
(grep of `*.go`), so which entrypoint launches the `:8090` vision server from
this repo is undetermined here.

---

## 6. Health

- **Server-side health endpoint (in-tree):** `internal/visionserver` serves
  `GET /health` (`internal/visionserver/server.go:27`, `HandleHealth`) alongside
  `/analyze`, `/providers`, `/learning/stats`, `/learning/clear`.
- **CLI self-check:** `cmd/helixqa-bridge` runs a preflight check
  (whisper / tesseract / analyzer binary / record script / evidence dir) and
  returns a 0/1 exit before serving (`cmd/helixqa-bridge/main.go`).
- **Health as a client (probes HelixQA issues):** default health path `/health`
  is used across `pkg/infra/qa_infra.go:76` (`apiHealthPath` default `"/health"`),
  `pkg/navigator/api_executor.go:136`, `pkg/autonomous/pipeline.go:2419`,
  `pkg/audio/whisper_client.go:122`, `pkg/audio/tesseract_client.go:99`,
  `pkg/vision/cheaper/adapters/uitars/uitars.go:208`.
- **Stack healthchecks** (`docker-compose.stack.yml`): `helixqa-api`
  `http://localhost:8080/health`; mediamtx `:9998/metrics`; nats
  `:8222/healthz`; ollama `:11434/api/tags`.

---

## 7. How it boots

Real entrypoint: `cmd/helixqa/main.go` (`package main`, `const version =
"0.2.0"`). Dispatch is `os.Args[1]` over a subcommand switch:

```
helixqa run        --banks <paths> [--platform <p>] [--device ...] [--package ...]
helixqa list       --banks <paths> [--platform <p>]
helixqa report     --input <dir>   [--format md|html|json]
helixqa autonomous --project <dir> [--platforms all|...]
helixqa http       --banks <paths> --base-url <url>      # LLM-free HTTP-client bank runner
helixqa replay     ...
helixqa signoff    ...
helixqa version | help | -h | --help
```

- **Default container boot:** `helixqa autonomous --project /project --platforms
  all` (root `Dockerfile` CMD).
- **Local boot:** `make build` then `./bin/helixqa <subcommand>`
  (`docs/QUICK_START.md`, `README.md` Usage).
- It is a **CLI / autonomous-session runner**, not a daemon: `run`/`list`/
  `report`/`autonomous`/`http` execute and exit with a meaningful exit code (CI
  / release gates depend on it — `cmd/helixqa/http.go` header).

---

## 8. Materials status (verify pass)

| Gate material | Status | Evidence |
|---------------|--------|----------|
| Purpose / what-it-is | PRESENT (existing) | `README.md`, `CONSTITUTION.md`, `docs/ANTI_BLUFF.md` |
| Architecture overview | PRESENT (existing) | `ARCHITECTURE.md`, `docs/architecture.md`, `OPENCV_INTEGRATION_ARCHITECTURE.md` |
| Stack / language | PRESENT (existing) | `go.mod` (`go 1.26`), `go.work`, `Dockerfile`, `Makefile` |
| Dependency manifest (own-org) | PRESENT (existing) | `helix-deps.yaml`, `go.mod` replace block |
| Third-party submodules | PRESENT (existing) | `.gitmodules`, `docs/licences-inventory.md`, `docs/opensource-references.md` |
| Build / distribution design | PRESENT (existing) | `Makefile`, `Dockerfile`, `docs/QUICK_START.md`, `cmd/DELIVERABLES.md` |
| Container image | PRESENT (existing) | root `Dockerfile`, `docker/` (mediamtx, monitoring, base-opencv-gstreamer) |
| Production stack (compose) | PRESENT (existing) — with build-context gap | `docker-compose.stack.yml` (see §4.3: `docker/api`, `docker/vision` absent) |
| Ports (consolidated cross-cut) | PRESENT (this doc) | §5 — `internal/visionserver` `:8090`, `helixqa-bridge` `127.0.0.1:7842`, stack ports |
| Health (consolidated cross-cut) | PRESENT (this doc) | §6 — `internal/visionserver` `/health`, `pkg/infra` default `/health`, stack healthchecks |
| How it boots | PRESENT (existing) | `cmd/helixqa/main.go`, root `Dockerfile` CMD, `docs/QUICK_START.md`, `USER_GUIDE_AUTONOMOUS.md` |
| Test-type / coverage posture | PRESENT (existing) | `docs/test-coverage.md`, `Makefile` (`test`, `qa-all`, `anti-bluff*`) |
| Governance / anti-bluff | PRESENT (existing) | `CONSTITUTION.md`, `CLAUDE.md`, `AGENTS.md`, `QWEN.md`, `docs/ANTI_BLUFF.md` |
| License | PRESENT (existing) | `LICENSE` (Apache-2.0 per SPDX headers), `docs/licences-inventory.md` |
| Vision-server launcher (which cmd starts `:8090`) | UNKNOWN | no `internal/visionserver` importer found in `cmd/` or `pkg/` |

**Verdict: HAS-VERIFIED.** All pre-integration gate materials already exist
across HelixQA's rich `docs/` and repository root and are verified present at
this revision; this document consolidates them and additionally fills the two
cross-cut materials (Ports §5, Health §6) that had no single dedicated existing
doc, grounded in real source. Two factual items to carry into any integration
work: (a) `docker-compose.stack.yml` references two build contexts
(`docker/api/`, `docker/vision/`) that are absent from the tree; (b) the
launcher of the `internal/visionserver` `:8090` HTTP server is `UNKNOWN:` from
this repository (no in-tree importer found).
