# HelixQA Video Course

## Course Overview

**Title:** Mastering HelixQA — Autonomous QA for Modern Software

**Duration:** 12 modules, ~4 hours total

**Audience:** QA engineers, developers, DevOps teams wanting to automate manual testing

---

## Module 1: Introduction to Autonomous QA (15 min)

### 1.1 What is HelixQA?
- The problem: manual QA doesn't scale
- Fire-and-forget philosophy
- How HelixQA replaces human QA teams
- Demo: running first autonomous session

### 1.2 Architecture Overview
- 4-phase pipeline: Learn → Plan → Execute → Analyze
- Diagram walkthrough (docs/diagrams/architecture.mmd)
- Package structure overview

### 1.3 Supported Platforms
- Android, Android TV, Web, Desktop, CLI, API
- Platform executor architecture
- When to use which executor

---

## Module 2: Quick Start (20 min)

### 2.1 Installation
- Building from source: `go build -o helixqa ./cmd/helixqa`
- Docker/Podman container setup
- Prerequisites check (ADB, Playwright, ffmpeg)

### 2.2 LLM Provider Setup
- Choosing a provider (OpenRouter recommended for beginners)
- Environment variable configuration
- Testing provider connectivity
- 40+ supported providers overview

### 2.3 First Autonomous Run
- Live demo: `helixqa autonomous --project . --platforms web --timeout 5m`
- Reading the output
- Understanding the pipeline report
- Reviewing generated issue tickets

---

## Module 3: The Learning Engine (20 min)

### 3.1 Project Knowledge Ingestion
- How HelixQA reads CLAUDE.md, docs/, codebase
- ProjectReader: documentation parsing
- CodebaseMapper: route and screen extraction
- GitAnalyzer: change history and hotspots

### 3.2 KnowledgeBase Structure
- Screens, API endpoints, components
- Constraints from CLAUDE.md
- Prior session awareness
- Code walkthrough: `pkg/learning/knowledge.go`

### 3.3 Customizing What Gets Learned
- Adding project-specific documentation
- Structuring docs/ for optimal ingestion
- CLAUDE.md best practices for QA

---

## Module 4: The Planning Engine (20 min)

### 4.1 LLM-Driven Test Generation
- How prompts are constructed from KnowledgeBase
- Test categories: functional, security, edge_case, performance
- Priority ranking system

### 4.2 Test Bank Reconciliation
- Existing YAML test bank format
- How new tests get reconciled with existing banks
- Avoiding duplicate test generation
- Code walkthrough: `pkg/planning/reconciler.go`

### 4.3 Multi-Pass Strategy
- How each pass generates different tests
- Coverage accumulation across sessions
- Pass number tracking in memory DB

---

## Module 5: Platform Executors (30 min)

### 5.1 Android/TV Executor (ADB)
- ADB connection setup
- Screenshot capture and video recording
- Crash/ANR detection via logcat
- Device-specific considerations (SDK versions)

### 5.2 Web Executor (Playwright)
- Playwright setup and configuration
- Page navigation and interaction
- Screenshot and video capture
- Console error detection

### 5.3 Desktop Executor (X11)
- xdotool-based interaction
- ImageMagick screenshot capture
- Display configuration

### 5.4 CLI/API Executors
- CLIExecutor: stdin/stdout automation
- APIExecutor: REST API testing
- When to use each executor

---

## Module 6: Evidence Collection (20 min)

### 6.1 Screenshots
- Automatic capture per test step
- Naming conventions and organization
- Before/after comparison

### 6.2 Video Recording
- scrcpy for Android (all SDK versions)
- Playwright video for web
- ffmpeg for desktop
- Frame extraction for analysis

### 6.3 Performance Metrics
- Memory monitoring (dumpsys meminfo)
- CPU tracking (dumpsys cpuinfo)
- Memory leak detection algorithm
- MetricsTimeline and LeakIndicator

### 6.4 Log Collection
- Logcat capture for Android
- Console logs for web
- Log analysis for crash patterns

---

## Module 7: LLM Vision Analysis (25 min)

### 7.1 How Vision Analysis Works
- Screenshot → LLM Vision API → Findings
- Prompt engineering for UI analysis
- Analysis categories: visual, UX, accessibility, brand, content

### 7.2 Video Frame Analysis
- ffmpeg frame extraction
- Key frame selection strategies
- Batch analysis with rate limiting

### 7.3 Understanding Findings
- Severity levels: critical → cosmetic
- Category classification
- False positive management
- Code walkthrough: `pkg/analysis/vision.go`

---

## Module 8: Photographic Memory (20 min)

### 8.1 SQLite Memory Store
- Database schema (7 tables)
- Session persistence
- Coverage tracking across passes
- Knowledge accumulation

### 8.2 Issue Lifecycle
- Ticket creation in docs/issues/
- Status tracking: open → fixed → verified → reopened
- Finding deduplication
- Regression detection

### 8.3 Multi-Pass Intelligence
- How Pass N+1 uses Pass N results
- Coverage gap prioritization
- Performance trend tracking
- Code walkthrough: `pkg/memory/store.go`

---

## Module 9: Issue Ticket System (15 min)

### 9.1 Ticket Format
- YAML frontmatter structure
- Markdown body with evidence references
- Screenshot and video linking

### 9.2 FindingsBridge
- Analysis → Memory → Markdown pipeline
- Automatic severity classification
- Evidence path resolution

### 9.3 Integrating with External Systems
- Converting HELIX tickets to Jira/Linear/GitHub Issues
- Webhook notifications
- CI/CD pipeline integration

---

## Module 10: Containerized Deployment (20 min)

### 10.1 Dockerfile
- Multi-stage build (builder + runtime)
- Required system dependencies
- Image optimization

### 10.2 Docker Compose
- QA robot service configuration
- Device passthrough (/dev/bus/usb)
- Volume mounting for results
- Environment variable injection

### 10.3 Kubernetes Deployment
- Pod configuration for QA jobs
- CronJob for scheduled QA passes
- Resource limits and quotas

---

## Module 11: Advanced Configuration (20 min)

### 11.1 Curiosity-Driven Exploration
- How random navigation discovers unknown screens
- Timeout configuration
- Screenshot capture during exploration

### 11.2 Custom Test Banks
- YAML bank format
- Creating domain-specific test cases
- Bank loading and filtering

### 11.3 LLM Provider Optimization
- Choosing between cloud/hybrid/self-hosted
- Cost optimization strategies
- Model selection for vision vs reasoning
- Fallback chain configuration

---

## Module 12: Real-World Case Study (30 min)

### 12.1 Testing Catalogizer
- Multi-platform media management system
- 7 components, 275+ API endpoints
- Android TV + Web + Desktop targets

### 12.2 Running the Full Suite
- HelixQA native: 834 steps
- Autonomous robot: 30 tests per pass
- 4-pass progression with cumulative learning

### 12.3 Issue Discovery and Resolution
- 16 real issues found by LLM vision
- Contrast, accessibility, and UX problems
- Fixing issues and re-verification

### 12.4 Results and Metrics
- 558/558 native tests passing
- 124/132 API tests passing
- 0 crashes, 0 ANRs across all devices
- 883+ Go tests in HelixQA itself

---

## Appendix A: Integrated Open-Source Tools

26 open-source tools (plus 1 test app, `rest-demo`, per `docs/flat-tools-manifest.tsv`) available as submodules:
scrcpy, appium, midscene, allure2, leakcanary, docker-android,
ui-tars, moondream, mem0, chroma, perfetto, shortest, stagehand,
testdriverai, kiwi-tcms, unstructured, marker, docling,
llama-index, signoz, redroid, appcrawler

## Appendix B: All 40+ Supported LLM Providers

Anthropic, OpenAI, OpenRouter, DeepSeek, Groq, Cerebras, Mistral,
Fireworks, NVIDIA, HuggingFace, Together, SambaNova, SiliconFlow,
xAI, Perplexity, Kimi, Hyperbolic, Venice, Cohere, Ollama, and more.

## Appendix C: Challenge Reference

30 self-validation challenges (HQA-001 to HQA-081) covering:
LLM providers, memory, learning, planning, execution, curiosity,
analysis, pipeline, and containers.
