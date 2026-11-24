# Autonomous QA Sessions

This guide explains how HelixQA's autonomous mode works, how to configure and run sessions, and how to get the most out of LLM-driven quality assurance.

## What Is Autonomous QA?

Autonomous QA is HelixQA's primary operating mode. Instead of running pre-written test scripts, the system reads your project, generates test cases using an LLM, executes them across target platforms, analyzes every screenshot with AI vision, and files issue tickets -- all without human intervention.

A single command launches the entire pipeline:

```bash
helixqa autonomous --project /path/to/project --platforms web --timeout 30m
```

You walk away. When the session finishes, you review the generated issue tickets in `docs/issues/` and the session report in `qa-results/`.

## How It Works

Every autonomous session proceeds through four phases (plus an optional curiosity phase). Each phase feeds its output into the next.

### Phase 1: Learn

The learning phase builds a `KnowledgeBase` by ingesting everything it can find about your project:

- **CLAUDE.md** at the project root -- architecture constraints, tech stack, known issues
- **docs/** directory -- feature documentation, API specs, design decisions
- **Source code** -- Go route handlers, React page components, Kotlin navigation graphs
- **Git history** -- recent commits identify code change hotspots
- **Memory database** -- prior session results, known issues, coverage gaps

The more documentation your project has, the better the test plan will be. At minimum, keep a `CLAUDE.md` with your architecture overview and key constraints.

### Phase 2: Plan

The planning phase sends the `KnowledgeBase` to the LLM with a structured prompt requesting test case generation. The LLM generates test cases covering:

| Category | Examples |
|----------|---------|
| Functional | Login flow, media playback, collection CRUD |
| Security | Auth bypass, input injection, token expiry |
| Edge cases | Empty states, network failure, malformed data |
| Performance | Page load time, memory growth under load |
| Accessibility | Color contrast, touch target sizes, screen reader labels |
| Visual | Layout correctness, responsive breakpoints |

Before finalizing, the planner reconciles generated tests against any existing YAML test banks in `challenges/helixqa-banks/` to avoid duplication. Tests are then ranked:

1. Regressions from prior sessions (critical severity)
2. Recently changed code paths (from git analysis)
3. Zero-coverage or low-coverage areas (from memory database)
4. Standard functional coverage
5. Curiosity-driven exploration items

### Phase 3: Execute

Each planned test case runs on the appropriate platform executor:

| Platform | Executor | Navigation | Recording |
|----------|----------|-----------|-----------|
| Web | Playwright | Browser API | Built-in video |
| Android | ADB | `adb shell input` | scrcpy / screenrecord |
| Desktop | X11 | xdotool | ffmpeg x11grab |

For every test case, the executor:

1. Starts video recording
2. Navigates to the starting screen
3. Executes each step sequentially
4. Captures a screenshot after every step
5. Monitors for crashes and ANRs continuously
6. Records performance metrics at defined intervals
7. Stops recording and saves all evidence

### Phase 3.5: Curiosity Exploration

After planned tests complete, the optional curiosity phase performs random navigation to discover screens and states not covered by the test plan. The explorer:

- Randomly taps interactive elements (buttons, list items, navigation icons)
- Captures screenshots of every new screen reached
- Feeds discovered screens back into the knowledge base for future sessions
- Stays within the configured `--curiosity-timeout` budget

Curiosity is enabled by default. Disable it for focused regression runs:

```bash
helixqa autonomous --project . --platforms web --curiosity=false
```

### Phase 4: Analyze

The analysis phase processes all collected evidence:

- **Screenshot analysis** -- every screenshot is sent to the LLM vision model for structured analysis across visual, UX, accessibility, brand, content, and performance categories
- **Leak detection** -- memory metrics are checked for monotonically increasing heap allocations
- **Crash analysis** -- crash logs from logcat (Android) or browser console (web) are classified
- **Ticket creation** -- all findings are written as markdown tickets to `docs/issues/`

---

## Configuration

### Prerequisites

1. **Go 1.24+** installed
2. **At least one LLM provider** API key set as an environment variable
3. **Platform tooling** for your target platforms (Playwright for web, ADB for Android, xdotool for desktop)

### Environment Setup

Create a `.env` file in your project root:

```env
# LLM provider (pick one or more)
ANTHROPIC_API_KEY=sk-ant-...
# Or: OPENROUTER_API_KEY, OPENAI_API_KEY, DEEPSEEK_API_KEY, GROQ_API_KEY

# Platform targets
HELIX_WEB_URL=http://localhost:3000
HELIX_ANDROID_DEVICE=192.0.2.214:5555
HELIX_ANDROID_PACKAGE=com.example.myapp
HELIX_DESKTOP_DISPLAY=:0

# Optional
HELIX_FFMPEG_PATH=/usr/bin/ffmpeg
```

### LLM Provider Selection

HelixQA auto-discovers providers by scanning environment variables at startup. When multiple keys are set, the `AdaptiveProvider` routes requests intelligently:

- **Vision requests** (screenshot analysis) go to providers with multimodal capability (Anthropic Claude, OpenAI GPT-4o)
- **Reasoning requests** (planning, test generation) go to the fastest available provider (Groq, Cerebras)
- **Fallback** cascades to the next provider on rate limit or error

For cost-effective production use, consider a tiered strategy:

| Task | Recommended Provider | Reason |
|------|---------------------|--------|
| Planning and test generation | Groq (Llama 3.3 70B) | Fast and cheap |
| Vision analysis | Anthropic Claude | Best accuracy |
| Bulk text processing | DeepSeek | Lowest cost |

Or use **OpenRouter** for unified access to all providers with a single API key.

For air-gapped or privacy-sensitive environments, use **Ollama** with a local model:

```env
HELIX_OLLAMA_URL=http://localhost:11434
HELIX_OLLAMA_MODEL=llama3.3
```

---

## Running an Autonomous Session

### Minimal Web Session

```bash
export OPENROUTER_API_KEY="sk-or-v1-..."
export HELIX_WEB_URL="http://localhost:3000"

helixqa autonomous --project . --platforms web --timeout 15m
```

### Full Cross-Platform Session

```bash
helixqa autonomous \
  --project /path/to/project \
  --platforms "android,web,desktop" \
  --timeout 1h \
  --curiosity=true \
  --curiosity-timeout 15m \
  --coverage-target 0.9 \
  --output qa-results \
  --report markdown,html,json \
  --env .env
```

### What You See During Execution

When the session starts, HelixQA prints a bootstrap summary:

```
HelixQA Autonomous QA Session

Project:          /path/to/project
Platforms:        android,web
Env file:         .env
Timeout:          1h0m0s
Coverage target:  90%
Output:           qa-results
Report formats:   markdown,html,json
Curiosity:        true (timeout: 15m0s)
Verbose:          false

Resolved platforms: [android web]
Pass number:      2
LLM provider:     anthropic
Platforms:        [android web]
Memory DB:        /path/to/project/helix_qa/data/memory.db
```

The session then proceeds through each phase. With `--verbose`, you see detailed step-by-step output including LLM prompts, executor commands, and analysis results.

---

## Understanding Results

### Session Output Directory

After completion, the output directory contains:

```
qa-results/
  session-1711547422/
    pipeline-report.json     # Machine-readable full session data
    pipeline-report.md       # Human-readable summary
    pipeline-report.html     # Standalone HTML report
    screenshots/
      test-001-login-step-1.png
      test-001-login-step-2.png
      test-002-navigation-step-1.png
    videos/
      test-001-login.mp4
      test-002-navigation.mp4
```

### Issue Tickets

Every finding is written as a markdown file in `docs/issues/`:

```
docs/issues/
  HELIX-001-login-button-not-visible-on-dark-theme.md
  HELIX-002-memory-leak-in-media-browser.md
  HELIX-003-contrast-ratio-below-wcag-aa.md
```

Each ticket contains YAML frontmatter with structured metadata (severity, category, platform, status) and a markdown body with reproduction steps, expected vs. actual behavior, evidence links, and LLM analysis.

### Session Report

The JSON report contains machine-readable session metrics:

```json
{
  "pass_number": 2,
  "total_tests": 28,
  "passed": 25,
  "failed": 3,
  "new_findings": 2,
  "verified_fixed": 1,
  "coverage_ratio": 0.87
}
```

---

## Evidence Collection

### Screenshots

A screenshot is captured after every test step. Screenshots are named following the convention:

```
test-<NNN>-<slug>-step-<N>.png
```

Screenshots serve as the primary input for LLM vision analysis. The vision model evaluates each screenshot across six categories: visual, UX, accessibility, brand, content, and performance.

### Video Recording

Video recording captures the full execution of each test case. Recording methods are platform-specific:

- **Web**: Playwright built-in video recording
- **Android 9 and below**: `adb shell screenrecord`
- **Android 10+**: Rapid screenshot capture assembled into video via ffmpeg
- **Desktop**: ffmpeg x11grab

Videos are stored in the `videos/` subdirectory of the session output.

### Audio Recording

Audio recording is available but disabled by default. Enable it for testing media playback quality:

```yaml
autonomous:
  recording_audio: true
  recording_audio_quality: "high"
  recording_audio_format: "wav"
```

---

## Best Practices

### Project Documentation

The quality of autonomous test generation is directly proportional to the quality of your project documentation. For optimal results:

- Maintain a `CLAUDE.md` at the project root with architecture overview, tech stack, constraints, and known issues
- Keep feature documentation in `docs/` -- HelixQA reads all `.md` files recursively
- Document API endpoints in `docs/api/`
- Include screen descriptions or navigation maps

### Timeout Tuning

- **First session**: Use a generous timeout (1-2 hours) with curiosity enabled (10-15 minutes). The first pass is exploratory and benefits from time.
- **Regression passes**: Use shorter timeouts (20-30 minutes) with curiosity disabled or limited (5 minutes). The memory database already contains coverage data.
- **CI post-deploy checks**: Use 10-15 minute timeouts with `--curiosity=false`.

### Coverage Targets

The `--coverage-target` flag (default 0.9) tells HelixQA when to stop. If the memory database shows coverage at or above the target across all platforms, the session exits early.

- Start with `0.7` for early-stage projects
- Use `0.9` for mature applications
- Target `0.95` only after several passes have accumulated coverage data

### Multi-Pass Strategy

A single session provides a broad first look. Multiple passes build toward comprehensive coverage:

| Pass | Timeout | Curiosity | Goal |
|------|---------|-----------|------|
| 1 | 1-2 hours | 10-15 min | Broad coverage, initial issue discovery |
| 2 | 1-2 hours | 15-20 min | Deeper exploration, re-test failures |
| 3 | 30-60 min | 5 min | Regression check, verify fixes |

The pass number increments automatically. No special flags are needed -- just run the same command again.

### Platform-Specific Tips

**Web**: Start your web application before launching the session. Verify the URL is accessible:

```bash
curl -s -o /dev/null -w "%{http_code}" http://localhost:3000
# Should return 200
```

**Android**: Connect the device and verify ADB access:

```bash
adb connect 192.0.2.214:5555
adb devices  # should list the device
```

**Desktop**: Ensure the target application is running and the X11 display is accessible:

```bash
xdotool search --name "Catalogizer"  # should return a window ID
```

### Cost Management

LLM API costs scale with session length and screenshot count. To minimize costs:

- Use DeepSeek or Groq for planning (cheapest providers)
- Reserve Anthropic or OpenAI for vision analysis only
- Use shorter timeouts for routine regression passes
- Disable curiosity when running focused tests

## Related Pages

- [CLI Reference](/reference/cli) -- complete flag reference for `helixqa autonomous`
- [Configuration](/reference/config) -- environment variables and config fields
- [Pipeline Phases](/pipeline) -- detailed phase-by-phase walkthrough
- [Multi-Pass QA](/manual/multi-pass) -- running successive sessions
- [Memory System](/manual/memory) -- understanding session persistence
- [LLM Providers](/providers) -- full provider list and cost comparison
