# Autonomous QA Session -- User Guide

This guide walks through setting up and running an LLM-powered autonomous QA session with HelixQA. By the end, you will know how to configure the system, launch a session, and interpret the output.

## Table of Contents

1. [Prerequisites](#prerequisites)
2. [Setting Up Configuration](#setting-up-configuration)
3. [Running Your First Session](#running-your-first-session)
4. [Understanding the QA Report](#understanding-the-qa-report)
5. [Reading Tickets and Video Evidence](#reading-tickets-and-video-evidence)
6. [Customizing Behavior](#customizing-behavior)
7. [Troubleshooting](#troubleshooting)

---

## Prerequisites

### Required Tools (All Platforms)

| Tool | Version | Purpose |
|------|---------|---------|
| Go | 1.24+ | Build and run HelixQA |
| ffmpeg | 4.x+ | Video recording and processing |

### Platform-Specific Tools

**Android testing:**

| Tool | Purpose |
|------|---------|
| `adb` (Android SDK platform-tools) | Device interaction, screenshots, logcat |
| `scrcpy` (optional) | High-quality video recording alternative |

Ensure your Android device or emulator is connected and accessible via `adb devices`.

**Desktop testing (Linux):**

| Tool | Purpose |
|------|---------|
| `xdotool` or `xdo` | Mouse/keyboard automation |
| `import` (ImageMagick) | Screenshot capture |

**Web testing:**

| Tool | Purpose |
|------|---------|
| Node.js 18+ | Playwright runtime |
| Playwright | Browser automation (`npx playwright install chromium`) |

### Optional: OpenCV Vision

For full mechanical vision capabilities (SSIM diffing, edge detection, contour analysis), install OpenCV 4.x and build with the `vision` tag:

```bash
# Ubuntu/Debian
sudo apt install libopencv-dev pkg-config

# Build with vision support
go build -tags vision ./...
```

Without OpenCV, the system uses LLM Vision APIs only. This is fully functional but uses more API credits.

### LLM API Keys

You need at least one API key for LLM-powered features. Supported providers:

| Provider | Environment Variable | Vision Support |
|----------|---------------------|----------------|
| OpenAI | `OPENAI_API_KEY` | GPT-4o vision |
| Anthropic | `ANTHROPIC_API_KEY` | Claude vision |
| Google | `GOOGLE_API_KEY` | Gemini vision |
| Groq | `GROQ_API_KEY` | No |
| Mistral | `MISTRAL_API_KEY` | No |
| DeepSeek | `DEEPSEEK_API_KEY` | No |
| xAI | `XAI_API_KEY` | No |
| Together | `TOGETHER_API_KEY` | No |
| Qwen | `QWEN_API_KEY` | Qwen-VL vision |

Vision-capable providers are strongly recommended since the autonomous session relies heavily on screenshot analysis.

### CLI Agents

The system manages headless CLI agents for navigation. At least one must be installed:

| Agent | Binary | Installation |
|-------|--------|-------------|
| OpenCode | `opencode` | `go install github.com/opencode-ai/opencode@latest` |
| Claude Code | `claude` | `npm install -g @anthropic-ai/claude-code` |
| Gemini | `gemini` | `npm install -g @google/gemini-cli` |
| Junie | `junie` | Via JetBrains toolbox |
| Qwen Code | `qwen-code` | `pip install qwen-code` |

---

## Setting Up Configuration

### Step 1: Copy the Template

```bash
cd /path/to/HelixQA
cp .env.example .env
```

### Step 2: Set API Keys

Open `.env` and fill in your API keys. At minimum, set one vision-capable provider:

```bash
ANTHROPIC_API_KEY=sk-ant-your-key-here
```

### Step 3: Configure CLI Agents

Specify which agents to use and where their binaries are:

```bash
HELIX_AGENTS_ENABLED=claude-code
HELIX_AGENT_CLAUDE_PATH=/usr/local/bin/claude
HELIX_AGENT_POOL_SIZE=1
```

For parallel platform testing, increase the pool size to match the number of platforms:

```bash
HELIX_AGENTS_ENABLED=claude-code,gemini
HELIX_AGENT_POOL_SIZE=3
```

### Step 4: Configure Target Platforms

Set which platforms to test:

```bash
HELIX_AUTONOMOUS_PLATFORMS=desktop
```

For desktop testing on Linux, ensure the display is correct:

```bash
HELIX_DESKTOP_PROCESS=yole-desktop
HELIX_DESKTOP_DISPLAY=:0
```

For Android:

```bash
HELIX_ANDROID_DEVICE=emulator-5554
HELIX_ANDROID_PACKAGE=digital.vasic.yole
```

For Android TV (with Channels support):

```bash
HELIX_ANDROIDTV_DEVICE=192.168.0.214:5555
HELIX_ANDROIDTV_PACKAGE=com.catalogizer.androidtv
```

The Android TV platform automatically detects and tests Android TV Home Screen Channels features including:
- Default channel creation and content population
- Category channels (Movies, TV Shows, Music, etc.)
- Watch Next row integration (continue watching, next episode)
- Deep link handling from home screen
- Channel sync mechanisms (WorkManager, launch sync, manual)
- Channel cleanup on logout

For Web:

```bash
HELIX_WEB_URL=http://localhost:8080
HELIX_WEB_BROWSER=chromium
```

### Step 5: Set Session Parameters

```bash
HELIX_AUTONOMOUS_TIMEOUT=2h
HELIX_AUTONOMOUS_COVERAGE_TARGET=0.90
HELIX_AUTONOMOUS_CURIOSITY_ENABLED=true
HELIX_AUTONOMOUS_CURIOSITY_TIMEOUT=30m
```

### Step 6: Configure Output

```bash
HELIX_OUTPUT_DIR=./qa-results
HELIX_REPORT_FORMATS=markdown,html,json
HELIX_TICKETS_ENABLED=true
HELIX_TICKETS_MIN_SEVERITY=low
```

---

## Running Your First Session

### Start the Application Under Test

Before launching the autonomous session, start your application:

```bash
# Desktop
./gradlew :desktopApp:run &

# Web
./gradlew :webApp:wasmJsBrowserRun &

# Android (ensure emulator is running)
./gradlew :androidApp:installDebug
adb shell am start -n digital.vasic.yole/.MainActivity
```

### Launch the Session

```bash
helixqa autonomous \
  --project /path/to/your-project \
  --platforms desktop \
  --env .env \
  --timeout 30m \
  --output qa-results/
```

### What Happens During the Session

**Phase 1: Setup (typically 30-60 seconds)**

The system:
1. Loads your `.env` configuration
2. Uses LLMsVerifier to rank and select the best available LLMs
3. Reads your project documentation to build a feature map
4. Spawns CLI agents and initializes the vision engine
5. Starts video recording for each platform

You will see output like:
```
[setup] LLMsVerifier: 3 models scored, top model: claude-3.5-sonnet (0.87)
[setup] DocProcessor: 42 features extracted from 12 documents
[setup] LLMOrchestrator: 1 agent spawned (claude-code)
[setup] VisionEngine: initialized (OpenCV: enabled, LLM Vision: anthropic)
[setup] Recording started for: desktop
```

**Phase 2: Doc-Driven Verification (depends on feature count)**

For each documented feature, the worker:
1. Captures a pre-screenshot
2. Asks the LLM agent how to verify the feature
3. Executes the suggested actions (clicks, typing, scrolling)
4. Captures a post-screenshot
5. Compares before/after states to evaluate the outcome
6. Records timeline events and coverage

```
[doc-driven][desktop] Verifying: "Markdown editing" (1/42)
[doc-driven][desktop]   Step 1: Open new document -> OK
[doc-driven][desktop]   Step 2: Type markdown content -> OK
[doc-driven][desktop]   Step 3: Verify preview renders -> OK
[doc-driven][desktop] Feature verified: markdown-editing (3/3 steps passed)
```

**Phase 3: Curiosity-Driven Exploration (configurable timeout)**

Workers explore parts of the app not covered by documentation:
1. The agent examines the navigation graph for unvisited screens
2. It tries undiscovered UI elements, edge cases, and unusual inputs
3. Any issues found are classified and ticketed

```
[curiosity][desktop] Exploring unknown area: Settings > Advanced
[curiosity][desktop]   Found new screen: "Advanced Settings"
[curiosity][desktop]   Testing edge case: empty input in "Custom format"
[curiosity][desktop]   Issue detected: Input field accepts invalid regex (medium)
```

**Phase 4: Report Generation (a few seconds)**

The system:
1. Stops all video recordings
2. Aggregates coverage across platforms
3. Links ticket screenshots to video timestamps
4. Generates reports in the configured formats

```
[report] Video recordings saved: desktop (14:32)
[report] Coverage: 38/42 features verified (90.5%)
[report] Tickets: 3 issues found (0 critical, 1 high, 2 medium)
[report] Reports written to: qa-results/
```

---

## Understanding the QA Report

The QA report is generated in your chosen formats under the output directory.

### Report Structure

```
qa-results/
  qa-report.md            # Markdown report
  qa-report.html          # HTML report (if configured)
  qa-report.json          # JSON report (if configured)
  tickets/                # Individual issue tickets
    HQA-0001.md
    HQA-0002.md
    HQA-0003.md
  screenshots/            # All captured screenshots
    desktop/
      001-home-screen.png
      002-settings-before.png
      002-settings-after.png
  videos/                 # Platform recording videos
    desktop-session.mp4
  navigation/             # Navigation graph exports
    desktop-navgraph.dot
    desktop-navgraph.json
    desktop-navgraph.mermaid
  timeline.json           # Full event timeline
```

### Report Sections

The Markdown report includes:

1. **Executive Summary** -- Session duration, coverage percentage, issue counts by severity
2. **Platform Results** -- Per-platform breakdown of features verified, issues found, and coverage
3. **Feature Verification Table** -- Each feature with status (verified/failed/skipped) and evidence links
4. **Issues Found** -- Summarized list with severity, type, and ticket references
5. **Navigation Coverage** -- Mermaid diagram of discovered screens and transitions
6. **Timeline** -- Chronological list of all session events with video timestamps
7. **Recommendations** -- LLM-generated suggestions for improving test coverage

### Coverage Metrics

Coverage is tracked per-feature and per-platform:

- **Verified**: Feature test steps all passed with evidence
- **Failed**: One or more test steps failed (ticket generated)
- **Skipped**: Feature not applicable to platform or timed out
- **Unverified**: Feature not reached during the session

---

## Reading Tickets and Video Evidence

### Ticket Format

Each ticket is a self-contained Markdown file with everything needed to reproduce and fix the issue:

```markdown
# HQA-0042: Button text truncated on Android settings screen

**Severity:** Medium | **Platform:** Android | **Category:** Visual Bug

## Steps to Reproduce
1. Open app on Android device (Pixel 5)
2. Navigate: Home -> Settings (hamburger menu)
3. Scroll down to "Data Management" section

## Expected Behavior
All button text fully visible within the button bounds.

## Actual Behavior
"Export All Documents" button text is truncated to "Export All Do..."

## Evidence
- Screenshots: screenshots/android/042-settings-before.png, 042-annotated.png
- Video: videos/android-session.mp4 @ 14:32 (navigating), @ 14:47 (truncation visible)
- Logs: logs/android/042-logcat.txt

## LLM Analysis
Button uses fixed-width container (240dp). Recommended fix: wrap_content with minWidth.
```

### Video Evidence

Each platform has a continuous video recording. Ticket references include exact timestamps:

```
Video: videos/desktop-session.mp4 @ 14:32
```

To jump to a specific moment in the video:

```bash
ffplay -ss 00:14:32 qa-results/videos/desktop-session.mp4
```

### Timeline

The `timeline.json` file provides a machine-readable event log. Each event includes:

- `type`: action, screenshot, issue, phase_change, crash, navigation
- `platform`: which platform the event occurred on
- `video_offset`: offset into the platform video
- `screenshot_path`: path to the screenshot taken at that moment
- `issue_id`: linked ticket ID (if the event is an issue)
- `feature_id`: linked feature (if verifying a documented feature)

---

## Customizing Behavior

### Adjusting Coverage Target

```bash
# Require 95% feature coverage before completing
HELIX_AUTONOMOUS_COVERAGE_TARGET=0.95
```

If the coverage target is not met after doc-driven verification, the curiosity phase will prioritize navigating to unverified features.

### Disabling Curiosity Phase

```bash
HELIX_AUTONOMOUS_CURIOSITY_ENABLED=false
```

This limits the session to doc-driven verification only, which is faster and uses fewer API credits.

### Controlling Agent Selection

```bash
# Use only Claude Code
HELIX_AGENTS_ENABLED=claude-code

# Prefer a specific agent for a platform
HELIX_ANDROID_PREFERRED_AGENT=claude-code
```

### Adjusting Vision Settings

```bash
# Increase SSIM threshold (stricter screen change detection)
HELIX_VISION_SSIM_THRESHOLD=0.98

# Disable OpenCV (use LLM Vision only)
HELIX_VISION_OPENCV_ENABLED=false
```

### Filtering Ticket Severity

```bash
# Only generate tickets for high and critical issues
HELIX_TICKETS_MIN_SEVERITY=high
```

### Adjusting Timeouts

```bash
# Per-agent response timeout
HELIX_AGENT_TIMEOUT=120s

# Overall session timeout
HELIX_AUTONOMOUS_TIMEOUT=4h

# Curiosity phase timeout
HELIX_AUTONOMOUS_CURIOSITY_TIMEOUT=1h
```

### Custom Documentation Root

By default, the doc processor scans `./docs` and well-known files (README.md, *_GUIDE.md). To specify a custom root:

```bash
HELIX_DOCS_ROOT=/path/to/your-project/docs
HELIX_DOCS_AUTO_DISCOVER=true
HELIX_DOCS_FORMATS=md,yaml,html,adoc,rst
```

### Vision-Gated Structured Steps and a Local Vision Model

In the structured phase, a bank step that declares `vision_verify: true`,
`recognize: [...]`, `forbid: [...]`, `golden: <id>` (with optional
`phash_max_distance`) or `expect_change: true` is vision-gated. It passes
only when every requested check succeeds on the exact after-action frame;
otherwise it is FAILED (the frame contradicts the expectation) or BLOCKED
(could not determine: no provider gave a definite `VERIFIED: yes|no` line
or a strict JSON transcription, blank or missing frame, missing golden, a
gated step whose action was skipped, frames that cannot be compared or are
blank / black, no executor for the platform, a configured device / display
that is unreachable, an `http:` step with no base URL, or a case in which
no asserting step ran — only `sleep:` / `screenshot` / prose steps). The
summary line reads `N passed, N failed, N
blocked (could not determine), ...`. A blocked case is never counted as
passed: it files an `environment` finding (never ticketed or reproduced as
an app bug), is persisted under `structured` in `pipeline-report.json`, and
makes the session exit with code 2 (a failed case exits 1).

`recognize` / `forbid` ask the model for exactly one JSON object
`{"texts": [...], "description": "..."}` and match tokens only inside
single `texts` elements — never the description. A reply in any other shape
is unusable (the next provider is asked, then BLOCKED), and so is a reading
with no screen text (empty / filler only) or with a refusal / remark by the
model in `texts` — such a reading never proves a `forbid` token absent.
Commentary in other wording inside `texts` is not detected; pair
`recognize` with `golden` where you have a reference screenshot. A
`vision_verify` reply is honoured only when it is exactly one
`VERIFIED: yes|no` line plus at most one `ACTUAL: …` line.

`helixqa run` uses the same contract: exit 0 all passed, 1 a failure /
crash / ANR, 2 nothing failed but not everything executed (`INCOMPLETE`,
including a bank that could not be loaded, or a report that could not be
written). A failed challenge still exits 1 when an error follows it. Exit
70 means an internal HelixQA panic. Scripts must treat 2 as "not a pass",
never as a warning. `helixqa autonomous` also exits 2 when the session is
BLOCKED (network / VPN preflight, provider outage in learn / plan,
timeout), unless a structured case FAILED (1).

A `recognize` token must equal one whole on-screen element (e.g. a button
label) by default, so a model's commentary inside the transcription
("Login not visible") cannot satisfy it. Set `recognize_contains: true` on
a step only when the token is part of a longer on-screen string.

Banks that already used `vision_verify: true` are now gated: run them with
a vision provider that answers in the `VERIFIED:` format, or expect those
cases to report BLOCKED.

To use a local model served by llama-server (for example llmctl's
`vision` instance), set:

```bash
HELIX_VISION_ENDPOINT=http://127.0.0.1:8082/v1
HELIX_VISION_MODEL=            # optional; empty = server default model
HELIX_VISION_API_KEY=          # optional, never logged
HELIXQA_GOLDENS_DIR=/path/to/goldens   # only for `golden:` steps
```

At startup the session prints `Vision endpoint: <url> (model: ..., api key: set|unset)`.
The endpoint URL must not contain user info, a query, a fragment, an encoded
`?`/`#` or a `;` parameter — such a value is refused at startup (put a key in
`HELIX_VISION_API_KEY`). The local endpoint is asked with `temperature: 0`,
a fixed seed and a JSON-schema `response_format` for transcriptions.
Full semantics: [docs/vision/NO_BLUFF_VISION.md](docs/vision/NO_BLUFF_VISION.md).

---

## Troubleshooting

### Common Issues

**"No agents available"**

Check that at least one CLI agent binary is installed and its path is correct in `.env`:

```bash
which claude  # Should print the path
HELIX_AGENT_CLAUDE_PATH=/usr/local/bin/claude
```

**"Vision provider failed"**

Ensure your API key is valid for a vision-capable provider:

```bash
# Test API key
curl -H "x-api-key: $ANTHROPIC_API_KEY" https://api.anthropic.com/v1/messages -d '{}'
```

If all vision providers fail, the system falls back to GoCV-only analysis (if OpenCV is installed) or proceeds with degraded functionality.

**"ADB device not found"**

```bash
adb devices  # Should list your device
HELIX_ANDROID_DEVICE=emulator-5554  # Must match device ID
```

**"Playwright browser not found"**

```bash
npx playwright install chromium
HELIX_WEB_BROWSER=chromium
```

**"ffmpeg not found"**

```bash
which ffmpeg
HELIX_RECORDING_FFMPEG_PATH=/usr/bin/ffmpeg
```

If ffmpeg is not available, set `HELIX_RECORDING_VIDEO=false` to disable video recording (screenshots still work).

**"Session timed out with low coverage"**

Increase the session timeout or reduce the coverage target:

```bash
HELIX_AUTONOMOUS_TIMEOUT=4h
HELIX_AUTONOMOUS_COVERAGE_TARGET=0.80
```

**"Agent circuit breaker open"**

An agent crashed 3 times in a row. The system will try to acquire a replacement from the pool. If no replacement is available, increase the pool size or add more agents:

```bash
HELIX_AGENTS_ENABLED=claude-code,gemini,opencode
HELIX_AGENT_POOL_SIZE=3
HELIX_AGENT_MAX_RETRIES=5
```

### Debug Mode

For more verbose output during development:

```bash
helixqa autonomous \
  --project /path/to/your-project \
  --platforms desktop \
  --env .env \
  --timeout 30m \
  --output qa-results/ \
  --verbose
```

### Partial Results

If a session fails partway through, partial results are still saved. Check the output directory for whatever was collected before the failure. The report phase runs even after phase failures to capture as much evidence as possible.
