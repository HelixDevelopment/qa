# Getting Started

This guide walks you through running your first HelixQA autonomous session, from installation through reviewing results.

## Prerequisites

| Tool | Required For | Minimum Version |
|------|-------------|----------------|
| Go | Building HelixQA | 1.24+ |
| ADB (platform-tools) | Android / Android TV testing | any |
| Node.js + Playwright | Web testing | Node 18+ |
| ffmpeg | Video recording (Desktop) | any |
| At least one LLM API key | All autonomous operations | -- |

## Build

```bash
cd HelixQA
go build -o bin/helixqa ./cmd/helixqa
export PATH="$PATH:$(pwd)/bin"
helixqa version
```

## Set an LLM Provider

```bash
# Pick one (OpenRouter recommended -- access to 100+ models)
export OPENROUTER_API_KEY="sk-or-v1-..."
# Or: ANTHROPIC_API_KEY, OPENAI_API_KEY, DEEPSEEK_API_KEY, GROQ_API_KEY
```

## Platform-Specific Quick Start

### Web Application

The web executor uses Playwright. Ensure your web application is running and accessible before starting the session.

```bash
# Start your web application (example)
cd your-project/frontend && npm run dev

# Run HelixQA against the web app
helixqa autonomous \
  --project /path/to/your/project \
  --platforms web \
  --timeout 10m \
  --env HELIX_WEB_URL=http://localhost:3000
```

Playwright launches a Chromium browser, navigates through your application, captures screenshots at every step, and records video of each test case. Console errors and failed network requests are captured automatically.

Output directory after the session:

```
qa-results/session-20260327-143201/
  screenshots/
    test-001-login-step-1.png
    test-001-login-step-2.png
    test-002-dashboard-step-1.png
    ...
  videos/
    test-001-login.webm
    test-002-dashboard.webm
    ...
  pipeline-report.json
  timeline.json
```

### Android Device

The Android executor communicates via ADB. Connect your device over USB or Wi-Fi before starting.

```bash
# Connect your Android device over Wi-Fi
adb connect 192.0.2.214:5555

# Verify the connection
adb devices

# Run HelixQA against the Android app
helixqa autonomous \
  --project /path/to/your/project \
  --platforms android \
  --timeout 15m \
  --env HELIX_ANDROID_DEVICE=192.0.2.214:5555 \
  --env HELIX_ANDROID_PACKAGE=com.your.app
```

Video recording uses scrcpy for high-quality capture across all Android SDK versions. Crash and ANR detection monitors logcat continuously throughout the session.

### Desktop Application (Tauri / Electron)

The desktop executor uses xdotool for mouse and keyboard interaction on X11 displays.

```bash
# Start your desktop application
cd your-project && npm run tauri:dev

# Run HelixQA against the desktop app
helixqa autonomous \
  --project /path/to/your/project \
  --platforms desktop \
  --timeout 10m \
  --env HELIX_DESKTOP_DISPLAY=:0
```

Video recording uses ffmpeg x11grab, and screenshots are captured with ImageMagick. For headless environments, use Xvfb to create a virtual display.

### REST API

The API executor tests HTTP endpoints without a UI. No additional tooling is required beyond HelixQA itself.

```bash
helixqa autonomous \
  --project /path/to/your/project \
  --platforms api \
  --timeout 10m \
  --env HELIX_API_BASE_URL=http://localhost:8080/api/v1
```

The API executor sends requests to discovered endpoints, validates response status codes and response times, checks JSON schema compliance, and tests authentication flows.

### All Platforms at Once

```bash
helixqa autonomous \
  --project /path/to/your/project \
  --platforms "android,web,desktop,api" \
  --timeout 30m \
  --curiosity=true \
  --curiosity-timeout 5m
```

## Your First Test Bank

Test banks are YAML files containing predefined test cases. Create a file at `challenges/helixqa-banks/smoke.yaml`:

```yaml
bank:
  name: smoke-tests
  version: "1.0"
  description: Basic smoke tests for the application

cases:
  - id: SMOKE-001
    title: "Application launches without crash"
    platform: android
    category: functional
    priority: critical
    tags: [smoke, launch]
    steps:
      - action: launch_app
        package: "${HELIX_ANDROID_PACKAGE}"
      - action: wait
        duration: 3s
      - action: screenshot
        name: "app-launched"
    expected:
      - no_crash: true
      - screen_not_blank: true

  - id: SMOKE-002
    title: "Home page loads successfully"
    platform: web
    category: functional
    priority: critical
    tags: [smoke, homepage]
    steps:
      - action: navigate
        url: "${HELIX_WEB_URL}"
      - action: wait_for_selector
        selector: "[data-testid='main-content']"
        timeout: 10s
      - action: screenshot
        name: "homepage-loaded"
    expected:
      - status_code: 200
      - no_console_errors: true

  - id: SMOKE-003
    title: "Login form is accessible"
    platform: web
    category: accessibility
    priority: high
    tags: [smoke, login, a11y]
    steps:
      - action: navigate
        url: "${HELIX_WEB_URL}/login"
      - action: screenshot
        name: "login-form"
    expected:
      - contrast_ratio_aa: true
      - form_labels_present: true
```

Run only the test bank (without LLM-generated tests):

```bash
helixqa run \
  --bank challenges/helixqa-banks/smoke.yaml \
  --platforms "android,web" \
  --timeout 5m
```

## Running Your First Autonomous Session

An autonomous session combines LLM-generated test cases with test bank cases. Here is a step-by-step walkthrough:

### Step 1: Prepare your project

Ensure your project has a `CLAUDE.md` or `README.md` at the root describing the architecture, screens, and key features. HelixQA reads this during the learning phase to understand what to test.

### Step 2: Start the target application

Start whatever application you want to test. For web applications, this means running the dev server. For Android, ensure the device is connected and the app is installed.

### Step 3: Launch the session

```bash
helixqa autonomous \
  --project /path/to/your/project \
  --platforms web \
  --timeout 10m \
  --output qa-results \
  --verbose
```

The `--verbose` flag shows detailed progress output including:
- Which documents are being read during the learning phase
- The test cases generated during the planning phase
- Each test step as it executes
- Screenshot analysis results in real time
- Issue tickets as they are created

### Step 4: Monitor progress

While the session runs, you can watch the output directory populate:

```bash
# In another terminal
watch -n 2 'ls -la qa-results/session-*/screenshots/ | tail -20'
```

### Step 5: Review results

After the session completes:

```bash
# View the session report
cat qa-results/session-*/pipeline-report.json | python3 -m json.tool

# List all issue tickets
ls docs/issues/HELIX-*.md

# Read the first ticket
cat docs/issues/HELIX-001-*.md

# View the timeline
cat qa-results/session-*/timeline.json | python3 -m json.tool | head -50
```

## Understanding the Output Directory

Every session creates a timestamped directory under the output path:

```
qa-results/
  session-20260327-143201/
    screenshots/              # All captured screenshots
      test-001-login-step-1.png
      test-001-login-step-2.png
      curiosity-screen-001.png
      curiosity-screen-002.png
    videos/                   # Video recordings per test case
      test-001-login.mp4
      test-002-dashboard.webm
    logs/                     # Device and browser logs
      logcat-filtered.txt
      console-errors.txt
    metrics/                  # Performance metric samples
      memory-timeline.json
      cpu-timeline.json
    perfetto/                 # Perfetto traces (if enabled)
      trace-test-001.perfetto-trace
    pipeline-report.json      # Session summary report
    timeline.json             # Detailed event timeline
    knowledge-base.json       # Snapshot of the knowledge base used
    test-plan.json            # The generated test plan
```

### Key Files

**pipeline-report.json** contains the session summary: tests planned, executed, passed, failed, screenshots captured, findings by severity, coverage percentage, phase durations, and LLM token usage.

**timeline.json** contains every event that occurred during the session with millisecond timestamps, enabling precise correlation between actions, screenshots, and findings.

**knowledge-base.json** is a snapshot of the knowledge base used for planning. This helps you understand what HelixQA learned about your project and how it informed test generation.

**test-plan.json** contains the full list of test cases (both LLM-generated and test bank cases) with their priority rankings and execution order.

## Next Steps

- [Pipeline Phases](/pipeline) -- understand each phase in detail
- [CLI Reference](/reference/cli) -- full command and flag reference
- [Test Bank Schema](/reference/test-bank-schema) -- YAML format for test banks
- [Multi-Pass QA](/manual/multi-pass) -- running successive sessions for cumulative coverage
- [Autonomous QA Guide](/guides/autonomous-qa) -- advanced autonomous session configuration
