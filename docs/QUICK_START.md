# HelixQA Quick Start Guide

Get the autonomous QA robot running in under 5 minutes.

## Prerequisites

- Go 1.24+ installed
- ADB (Android SDK platform-tools) for Android/TV testing
- Node.js 18+ and Playwright for web testing
- ffmpeg for video recording
- At least one LLM API key (OpenRouter, DeepSeek, Groq, Anthropic, OpenAI, or 35+ others)

## 1. Build

```bash
cd HelixQA
go build -o bin/helixqa ./cmd/helixqa
```

## 2. Configure LLM Provider

Set at least one API key as environment variable:

```bash
# Option A: OpenRouter (recommended — access to 100+ models)
export OPENROUTER_API_KEY="sk-or-v1-..."

# Option B: DeepSeek (cheapest)
export DEEPSEEK_API_KEY="sk-..."

# Option C: Anthropic Claude (best quality)
export ANTHROPIC_API_KEY="sk-ant-..."

# Option D: Groq (fastest)
export GROQ_API_KEY="gsk_..."

# Option E: Any of 40+ supported providers
# See: pkg/llm/providers_registry.go for full list
```

## 3. Connect Devices (Optional)

```bash
# Android/TV device via ADB
adb connect 192.0.2.214:5555
export HELIX_ANDROID_DEVICE="192.0.2.214:5555"
export HELIX_ANDROID_PACKAGE="com.your.app"

# Web app
export HELIX_WEB_URL="http://localhost:3000"
```

## 4. Run Autonomous QA

```bash
# Basic run — learns project, generates tests, executes, analyzes
helixqa autonomous --project /path/to/your/project --platforms web --timeout 10m

# Full cross-platform with curiosity exploration
helixqa autonomous \
  --project /path/to/project \
  --platforms "android,web" \
  --timeout 30m \
  --curiosity=true \
  --curiosity-timeout 5m \
  --output qa-results

# Multi-pass (each pass builds on previous knowledge)
helixqa autonomous --project . --platforms all --timeout 1h  # Pass 1
helixqa autonomous --project . --platforms all --timeout 1h  # Pass 2 (aware of Pass 1)
```

## 5. Review Results

```bash
# Issue tickets created automatically
ls docs/issues/HELIX-*.md

# Session reports
cat qa-results/session-*/pipeline-report.json

# Screenshots captured
ls qa-results/session-*/screenshots/

# Video recordings
ls qa-results/session-*/videos/
```

## 6. Containerized Run

```bash
# Using Podman
podman-compose -f docker-compose.qa-robot.yml up

# Using Docker
docker compose -f docker-compose.qa-robot.yml up
```

## What the Robot Does

1. **Learns** — Reads your CLAUDE.md, docs/, codebase, git history, prior QA sessions
2. **Plans** — LLM generates test cases covering functional, security, edge case, performance
3. **Executes** — Runs tests with screenshots, video recording, crash detection, performance metrics
4. **Explores** — Curiosity-driven random navigation discovers unplanned areas
5. **Analyzes** — LLM vision examines every screenshot for UI/UX issues
6. **Reports** — Creates detailed issue tickets in docs/issues/ with evidence

## Supported Platforms

| Platform | Executor | Video | Screenshots |
|----------|----------|-------|-------------|
| Web | Playwright | Playwright video | Playwright screenshot |
| Android | ADB | screenrecord/scrcpy | screencap |
| Android TV | ADB | screenrecord | screencap |
| Desktop | X11/xdotool | ffmpeg x11grab | ImageMagick import |
| CLI/TUI | stdin/stdout | — | — |
| REST API | HTTP client | — | — |

## Supported LLM Providers (40+)

Anthropic, OpenAI, OpenRouter, DeepSeek, Groq, Cerebras, Mistral, Fireworks, NVIDIA, HuggingFace, Together, SambaNova, SiliconFlow, xAI, Perplexity, Kimi, Hyperbolic, Venice, Cohere, Ollama (self-hosted), and more.

See `pkg/llm/providers_registry.go` for the complete list.
