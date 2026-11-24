# CLI Reference

Complete command-line reference for the `helixqa` binary. Every flag, default value, and usage pattern is documented here.

## Global Usage

```bash
helixqa <command> [flags]
```

Available commands:

| Command | Description |
|---------|-------------|
| `run` | Execute QA pipeline from YAML test banks |
| `autonomous` | Run a full LLM-driven autonomous QA session |
| `list` | List and filter test cases from banks |
| `report` | Generate reports from existing session results |
| `version` | Print version, build info, and detected providers |
| `help` | Show top-level help |

Run `helixqa <command> --help` for per-command flag details.

---

## `helixqa run`

Execute existing YAML test banks without the autonomous learning and planning phases. Use this when you have hand-written or previously generated test banks and want to run them directly.

```bash
helixqa run --banks <paths> [flags]
```

### Flags

| Flag | Type | Default | Required | Description |
|------|------|---------|----------|-------------|
| `--banks` | string | -- | Yes | Comma-separated paths to test bank files or directories containing `.yaml` files |
| `--platform` | string | `all` | No | Target platform: `android`, `web`, `desktop`, `all` |
| `--device` | string | -- | No | Android device or emulator ID (e.g., `192.0.2.214:5555`) |
| `--output` | string | `qa-results` | No | Directory for results, screenshots, and video recordings |
| `--speed` | string | `normal` | No | Execution pacing: `slow` (2s delay), `normal` (500ms), `fast` (0ms) |
| `--report` | string | `markdown` | No | Report output format: `markdown`, `html`, `json` |
| `--validate` | bool | `true` | No | Enable step-by-step validation with crash detection between steps |
| `--record` | bool | `true` | No | Enable video recording of test execution |
| `--verbose` | bool | `false` | No | Enable verbose logging to stdout |
| `--package` | string | -- | No | Android application package name (e.g., `com.example.myapp`) |
| `--timeout` | duration | `30m` | No | Maximum duration for the entire run |
| `--browser-url` | string | -- | No | Base URL for web platform testing (e.g., `http://localhost:3000`) |
| `--desktop-process` | string | -- | No | Process name for desktop platform testing |
| `--tickets` | bool | `true` | No | Generate markdown issue tickets for failed tests |

### Speed Modes

| Mode | Step Delay | Best For |
|------|-----------|----------|
| `slow` | 2 seconds | Debugging, visual inspection, demo recordings |
| `normal` | 500 milliseconds | Standard QA runs |
| `fast` | 0 milliseconds | CI pipelines, maximum throughput |

### Examples

```bash
# Run all banks against all platforms
helixqa run --banks challenges/helixqa-banks/ --platform all

# Run specific bank files against web
helixqa run \
  --banks banks/app-navigation.yaml,banks/edge-cases-stress.yaml \
  --platform web \
  --browser-url http://localhost:3000

# Android device testing with video recording
helixqa run \
  --banks banks/ \
  --platform android \
  --device 192.0.2.214:5555 \
  --package com.vasicdigital.catalogizer \
  --record=true \
  --output qa-results

# Fast CI run without video, markdown tickets only
helixqa run \
  --banks banks/ \
  --platform web \
  --browser-url http://localhost:3000 \
  --speed fast \
  --record=false \
  --report markdown \
  --timeout 10m

# Desktop testing with slow pacing for debugging
helixqa run \
  --banks banks/ \
  --platform desktop \
  --desktop-process catalogizer-desktop \
  --speed slow \
  --verbose
```

---

## `helixqa autonomous`

Run a full autonomous QA session. This is the primary command for fire-and-forget testing. The session proceeds through four phases (Learn, Plan, Execute, Analyze) plus an optional curiosity exploration phase. All phases are driven by the configured LLM provider.

```bash
helixqa autonomous [flags]
```

### Flags

| Flag | Type | Default | Required | Description |
|------|------|---------|----------|-------------|
| `--project` | string | `.` | No | Path to the project root directory |
| `--platforms` | string | `android,desktop,web` | No | Comma-separated list of target platforms |
| `--env` | string | `.env` | No | Path to the environment file containing API keys and configuration |
| `--timeout` | duration | `2h` | No | Maximum total session duration |
| `--coverage-target` | float | `0.9` | No | Desired feature coverage ratio (0.0 to 1.0) |
| `--output` | string | `qa-results` | No | Directory for screenshots, videos, and reports |
| `--report` | string | `markdown,html,json` | No | Comma-separated report output formats |
| `--verbose` | bool | `false` | No | Enable verbose logging |
| `--curiosity` | bool | `true` | No | Enable curiosity-driven random exploration phase |
| `--curiosity-timeout` | duration | `30m` | No | Time budget for the curiosity exploration phase |

### Platform Values

| Value | Executor | Required Environment |
|-------|----------|---------------------|
| `web` | Playwright browser automation | `HELIX_WEB_URL` |
| `android` | ADB Android device control | `HELIX_ANDROID_DEVICE`, `HELIX_ANDROID_PACKAGE` |
| `androidtv` | ADB Android TV (D-pad navigation) | `HELIX_ANDROID_DEVICE` |
| `desktop` | X11 / xdotool desktop automation | `HELIX_DESKTOP_DISPLAY` |
| `cli` | stdin/stdout process interaction | -- |
| `api` | HTTP REST client | `HELIX_API_URL` |
| `all` | All executors with configured environment | varies |

### Session Output

After a session completes, the output directory contains:

```
qa-results/
  session-<unix-timestamp>/
    pipeline-report.json
    pipeline-report.md
    pipeline-report.html
    screenshots/
      test-001-login-step-1.png
      test-001-login-step-2.png
    videos/
      test-001-login.mp4
```

Issue tickets are written to `docs/issues/` in the project root.

### Examples

```bash
# Minimal web-only session
helixqa autonomous \
  --project . \
  --platforms web \
  --timeout 10m

# Full cross-platform session with extended curiosity
helixqa autonomous \
  --project /path/to/project \
  --platforms "android,web" \
  --timeout 1h \
  --curiosity=true \
  --curiosity-timeout 15m \
  --output qa-results \
  --report markdown,json

# Quick dry preview (learn and plan only, no execution)
# Use a short timeout with curiosity disabled
helixqa autonomous \
  --project . \
  --platforms web \
  --timeout 1m \
  --curiosity=false \
  --verbose

# High-coverage session targeting 95%
helixqa autonomous \
  --project . \
  --platforms "android,web,desktop" \
  --timeout 2h \
  --coverage-target 0.95 \
  --curiosity-timeout 20m

# Self-hosted Ollama run (no cloud API keys)
# Set HELIX_OLLAMA_URL before running
helixqa autonomous \
  --project . \
  --platforms web \
  --env .env.local \
  --timeout 30m
```

---

## `helixqa list`

List test cases available in the specified test banks. Supports filtering by platform, category, priority, and tag. Useful for inspecting bank contents before a run.

```bash
helixqa list --banks <paths> [flags]
```

### Flags

| Flag | Type | Default | Required | Description |
|------|------|---------|----------|-------------|
| `--banks` | string | -- | Yes | Comma-separated paths to test bank files or directories |
| `--platform` | string | -- | No | Filter by target platform: `android`, `web`, `desktop` |
| `--category` | string | -- | No | Filter by test category (e.g., `functional`, `security`, `edge_case`) |
| `--priority` | string | -- | No | Filter by priority: `critical`, `high`, `medium`, `low` |
| `--tag` | string | -- | No | Filter by tag (e.g., `smoke`, `navigation`, `login`) |
| `--json` | bool | `false` | No | Output as JSON instead of table format |

### Table Output

The default output is a formatted table:

```
Test cases: 42

ID           NAME                                     CATEGORY     PRIORITY   PLATFORMS
------------------------------------------------------------------------------------------
NAV-MAIN-001 Navigate to Files screen                 functional   critical   android,web,desktop
NAV-MAIN-002 Navigate to Todo screen                  functional   critical   android,web,desktop
NAV-CTRL-002 Drawer menu navigation (Android)         functional   high       android
...
```

### Examples

```bash
# List all test cases across all banks
helixqa list --banks banks/

# List only Android tests
helixqa list --banks banks/ --platform android

# List critical-priority tests as JSON
helixqa list --banks banks/ --priority critical --json

# List tests tagged "smoke"
helixqa list --banks banks/ --tag smoke

# List security tests from a specific bank file
helixqa list --banks banks/edge-cases-stress.yaml --category security

# Pipe JSON output to jq for custom filtering
helixqa list --banks banks/ --json | jq '.[] | select(.priority == "critical")'
```

---

## `helixqa report`

Generate reports from existing session result directories. Use this when you want to convert a JSON session report into a different format, or regenerate a report after editing session data.

```bash
helixqa report [flags]
```

### Flags

| Flag | Type | Default | Required | Description |
|------|------|---------|----------|-------------|
| `--input` | string | `qa-results` | No | Path to session directory containing `qa-report.json` |
| `--format` | string | `markdown` | No | Output format: `markdown`, `html`, `json` |
| `--output` | string | same as `--input` | No | Directory or file path for the generated report |

### Report Formats

| Format | File | Description |
|--------|------|-------------|
| `markdown` | `pipeline-report.md` | Human-readable markdown with tables and finding summaries |
| `html` | `pipeline-report.html` | Standalone HTML page with embedded CSS, suitable for sharing |
| `json` | `pipeline-report.json` | Machine-readable JSON with full session data |

### Examples

```bash
# Generate HTML report from the latest session
helixqa report \
  --input qa-results/session-1711547422 \
  --format html

# Generate all three formats
helixqa report \
  --input qa-results/session-1711547422 \
  --format markdown
helixqa report \
  --input qa-results/session-1711547422 \
  --format html
helixqa report \
  --input qa-results/session-1711547422 \
  --format json

# Write report to a custom location
helixqa report \
  --input qa-results/session-1711547422 \
  --format html \
  --output /tmp/qa-report
```

---

## `helixqa version`

Print the HelixQA version string.

```bash
helixqa version
```

Sample output:

```
helixqa v0.2.0
```

When LLM providers are configured via environment variables, the autonomous command prints additional discovery information at startup including detected providers, resolved platforms, memory database path, and pass number.

---

## Common Workflows

### First-Time Setup

```bash
# Build the binary
cd HelixQA
go build -o bin/helixqa ./cmd/helixqa

# Set an API key
export OPENROUTER_API_KEY="sk-or-v1-..."

# Set platform environment
export HELIX_WEB_URL="http://localhost:3000"

# Run first autonomous session
./bin/helixqa autonomous --project /path/to/project --platforms web --timeout 15m
```

### Multi-Pass Regression Testing

```bash
# Pass 1: broad exploration
helixqa autonomous --project . --platforms "android,web" --timeout 1h --curiosity-timeout 10m

# Fix reported issues...

# Pass 2: deeper coverage + regression check
helixqa autonomous --project . --platforms "android,web" --timeout 1h --curiosity-timeout 15m

# Pass 3: verify fixes
helixqa autonomous --project . --platforms "android,web" --timeout 30m --curiosity-timeout 5m
```

### Bank-Driven Testing (No LLM Required)

```bash
# Run hand-written test banks without autonomous mode
helixqa run \
  --banks banks/app-navigation.yaml \
  --platform android \
  --device emulator-5554 \
  --package com.example.app \
  --output qa-results \
  --verbose
```

### Inspecting Available Tests

```bash
# Count tests per platform
helixqa list --banks banks/ --platform android --json | jq length
helixqa list --banks banks/ --platform web --json | jq length

# Export full test inventory
helixqa list --banks banks/ --json > test-inventory.json
```

## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | All tests passed, no issues detected |
| `1` | Issues detected, configuration error, or runtime failure |

## Related Pages

- [Configuration](/reference/config) -- environment variables and config file options
- [Test Bank Schema](/reference/test-bank-schema) -- YAML test bank format
- [Autonomous QA Guide](/guides/autonomous-qa) -- detailed autonomous session walkthrough
- [Test Banks Guide](/guides/test-banks) -- how to write effective test banks
