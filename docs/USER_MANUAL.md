# HelixQA User Manual

## Overview

HelixQA is an autonomous, fire-and-forget QA system that replaces human QA teams. It learns your project, generates tests, executes them across all platforms, records video evidence, detects crashes, analyzes UI with AI vision, and creates detailed issue tickets — all without human intervention.

## Architecture

```
helixqa autonomous --project /path --platforms all
  │
  ▼
┌─────────────────────────────────────────────────┐
│              SESSION PIPELINE                     │
├──────────┬──────────┬──────────┬────────────────┤
│ Phase 1  │ Phase 2  │ Phase 3  │ Phase 4         │
│ LEARN    │ PLAN     │ EXECUTE  │ ANALYZE         │
│          │          │          │                  │
│ Read     │ LLM gen  │ Run      │ LLM vision      │
│ docs,    │ tests,   │ tests,   │ analysis,       │
│ code,    │ reconcile│ record   │ leak detect,    │
│ git,     │ with     │ video,   │ crash check,    │
│ prior    │ banks,   │ capture  │ create issue    │
│ sessions │ rank     │ screens  │ tickets         │
└──────────┴──────────┴──────────┴────────────────┘
  │                                        │
  ▼                                        ▼
┌────────────┐                    ┌────────────────┐
│ Memory DB  │                    │ docs/issues/   │
│ (SQLite)   │                    │ HELIX-NNN.md   │
└────────────┘                    └────────────────┘
```

## CLI Reference

### `helixqa autonomous`

Run a full autonomous QA session.

| Flag | Default | Description |
|------|---------|-------------|
| `--project` | `.` | Path to project root |
| `--platforms` | `android,desktop,web` | Comma-separated platforms |
| `--timeout` | `2h` | Maximum session duration |
| `--output` | `qa-results` | Output directory |
| `--curiosity` | `true` | Enable curiosity exploration |
| `--curiosity-timeout` | `30m` | Curiosity phase time limit |
| `--coverage-target` | `0.9` | Desired coverage (0-1) |
| `--report` | `markdown,html,json` | Report formats |
| `--verbose` | `false` | Verbose logging |
| `--env` | `.env` | Environment file path |

### `helixqa run`

Run existing test banks (non-autonomous).

```bash
helixqa run --banks challenges/helixqa-banks/ --platforms web --output qa-results
```

### `helixqa list`

List available test cases from banks.

```bash
helixqa list --banks challenges/helixqa-banks/ --platform android --json
```

### `helixqa report`

Generate reports from existing results.

```bash
helixqa report --input qa-results/session-* --format html
```

## Environment Variables

### LLM Providers (set at least one)

| Variable | Provider |
|----------|----------|
| `ANTHROPIC_API_KEY` | Anthropic Claude |
| `OPENAI_API_KEY` | OpenAI GPT |
| `OPENROUTER_API_KEY` | OpenRouter (100+ models) |
| `DEEPSEEK_API_KEY` | DeepSeek |
| `GROQ_API_KEY` | Groq (fast inference) |
| `CEREBRAS_API_KEY` | Cerebras |
| `MISTRAL_API_KEY` | Mistral |
| `NVIDIA_API_KEY` | NVIDIA NIM |
| `FIREWORKS_API_KEY` | Fireworks AI |
| `TOGETHER_API_KEY` | Together AI |
| `HELIX_OLLAMA_URL` | Ollama (self-hosted) |
| + 30 more | See `pkg/llm/providers_registry.go` |

### Device Configuration

| Variable | Purpose |
|----------|---------|
| `HELIX_ANDROID_DEVICE` | ADB device ID (e.g., `192.168.0.214:5555`) |
| `HELIX_ANDROID_PACKAGE` | Android app package (e.g., `com.app.name`) |
| `HELIX_WEB_URL` | Web app URL (e.g., `http://localhost:3000`) |
| `HELIX_DESKTOP_DISPLAY` | X11 display (default `:0`) |
| `HELIX_FFMPEG_PATH` | Path to ffmpeg binary |

## Photographic Memory

HelixQA maintains a SQLite database (`helix_qa/data/memory.db`) that persists across sessions:

- **Sessions** — every QA run with timestamps, coverage, pass/fail counts
- **Findings** — every issue discovered with lifecycle tracking
- **Coverage** — which screens/platforms have been tested and how many times
- **Knowledge** — learned project facts (screen count, endpoint count, etc.)

Each new session has full awareness of all previous sessions. The robot:
- Avoids re-testing areas with full coverage
- Prioritizes areas with recent changes or prior failures
- Re-verifies previously fixed issues for regressions
- Tracks coverage trends across passes

## Issue Ticket Format

Tickets are created in `docs/issues/HELIX-NNN-slug.md`:

```yaml
---
id: HELIX-042
severity: high        # critical | high | medium | low | cosmetic
category: visual      # visual | ux | accessibility | performance | functional | brand
platform: android
screen: media-detail
status: open          # open | in_progress | fixed | verified | reopened | wontfix
found_date: 2026-03-27
---

# Title describing the issue

Detailed description of what was found.

## Steps to Reproduce

1. Step one
2. Step two

## Evidence

Screenshot/video references.
```

## Multi-Pass Strategy

Run multiple passes for thorough coverage:

```bash
# Pass 1: Initial scan
helixqa autonomous --project . --platforms all --timeout 1h

# Pass 2: Deeper exploration (knows Pass 1 results)
helixqa autonomous --project . --platforms all --timeout 1h --curiosity-timeout 15m

# Pass 3: Regression check (verifies fixes from Passes 1-2)
helixqa autonomous --project . --platforms all --timeout 30m
```

Each pass generates different test cases, explores different paths, and accumulates knowledge.

## Package Structure

| Package | Purpose |
|---------|---------|
| `pkg/llm/` | 40+ LLM providers with adaptive selection |
| `pkg/memory/` | SQLite photographic memory (sessions, findings, coverage) |
| `pkg/learning/` | Project knowledge ingestion (docs, code, git) |
| `pkg/planning/` | LLM-driven test plan generation with bank reconciliation |
| `pkg/performance/` | ADB-based metrics collection, memory leak detection |
| `pkg/video/` | scrcpy/screenrecord video + ffmpeg frame extraction |
| `pkg/maestro/` | Maestro YAML mobile flow execution |
| `pkg/analysis/` | LLM vision analysis of screenshots and video frames |
| `pkg/autonomous/` | Pipeline orchestration, executor factory, findings bridge |
| `pkg/navigator/` | Platform executors (ADB, Playwright, X11) |
| `pkg/detector/` | Real-time crash/ANR detection |
| `pkg/evidence/` | Screenshot, video, log evidence collection |
| `pkg/testbank/` | YAML test bank management |
| `pkg/ticket/` | Markdown issue ticket generation |
| `pkg/reporter/` | Report generation (MD, HTML, JSON) |

## Integrated Open-Source Tools

26 open-source tools (plus 1 test app, `rest-demo`, per the manifest) available as submodules (flat `<root>/submodules/<snake_name>/`, see `docs/flat-tools-manifest.tsv`; legacy fallback `tools/opensource/<name>`):

| Tool | Purpose |
|------|---------|
| scrcpy | Android screen mirroring + recording |
| appium | Cross-platform mobile automation |
| midscene | Vision-driven UI automation |
| allure2 | Test reporting |
| leakcanary | Android memory leak detection |
| docker-android | Containerized Android emulators |
| ui-tars | GUI-specialized vision model |
| moondream | Lightweight vision model |
| mem0 | Agent memory system |
| chroma | Vector database |
| perfetto | Android system tracing |
| shortest | Natural language E2E testing |
| stagehand | AI browser automation |
| testdriverai | OS-level AI testing |
| kiwi-tcms | Test case management |
| unstructured | Document parsing |
| marker | PDF to markdown |
| docling | Document understanding |
| llama-index | RAG framework |
| signoz | Open-source APM |
| redroid | Android in container |
| appcrawler | Android app crawler |
