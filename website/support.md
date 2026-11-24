# Support

This page covers troubleshooting common issues, understanding log output, enabling debug mode, and reporting problems.

## Troubleshooting Common Errors

### "No LLM providers discovered"

HelixQA scans environment variables at startup to find available LLM providers. If no providers are found, autonomous sessions cannot run.

**Fix**: Set at least one provider API key:

```bash
# Any one of these is sufficient
export OPENROUTER_API_KEY="sk-or-v1-..."
export ANTHROPIC_API_KEY="sk-ant-..."
export OPENAI_API_KEY="sk-..."
export DEEPSEEK_API_KEY="sk-..."
export GROQ_API_KEY="gsk_..."
```

Verify detection with:

```bash
helixqa version
```

The output lists all detected providers. If your key is set but not detected, check for typos in the environment variable name.

### "ADB device not found" or "no devices/emulators found"

The Android executor cannot connect to a device via ADB.

**Fix**: Verify ADB connectivity independently:

```bash
# Check connected devices
adb devices

# If empty, connect over Wi-Fi
adb connect 192.0.2.214:5555

# Verify the connection
adb shell echo "connected"
```

Common causes:
- Device is not on the same network as the host
- ADB debugging is not enabled on the device (Settings > Developer Options > USB Debugging)
- A firewall is blocking port 5555
- Another ADB server instance is running (kill it with `adb kill-server && adb start-server`)

### "Playwright browser not installed"

The web executor requires Playwright browsers to be installed separately from the npm package.

**Fix**: Install the required browser:

```bash
npx playwright install chromium
```

In containers, use the Playwright Docker image or install system dependencies:

```bash
npx playwright install-deps chromium
```

### "Failed to capture screenshot" (Desktop)

The desktop executor uses ImageMagick's `import` command for screenshots, which requires an active X11 display.

**Fix**: Verify the display is accessible:

```bash
# Check the DISPLAY variable
echo $DISPLAY

# Test with a manual screenshot
import -window root /tmp/test-screenshot.png
```

For headless servers, create a virtual display:

```bash
# Start Xvfb
Xvfb :99 -screen 0 1920x1080x24 &
export DISPLAY=:99

# Now run HelixQA
helixqa autonomous --project . --platforms desktop --timeout 10m
```

### "Memory database locked"

Multiple concurrent sessions writing to the same memory database can occasionally encounter lock contention despite WAL mode.

**Fix**: This usually resolves itself within seconds due to the built-in busy timeout (5 seconds). If it persists:

```bash
# Check for zombie HelixQA processes
ps aux | grep helixqa

# Kill any stuck processes
kill <pid>

# Verify the database is not corrupted
sqlite3 helixqa-memory.db "PRAGMA integrity_check;"
```

### "Context deadline exceeded" during LLM API calls

The configured timeout for LLM API calls was exceeded. This happens when a provider is slow to respond or when network connectivity is poor.

**Fix**: Increase the LLM timeout:

```bash
export HELIX_LLM_TIMEOUT=120s  # default is 60s
```

Or switch to a faster provider:

```bash
export GROQ_API_KEY="gsk_..."  # Groq has sub-second inference
```

### "scrcpy not found" or "screenrecord failed"

Video recording on Android failed because scrcpy is not installed or screenrecord encountered a device-specific error.

**Fix**: Install scrcpy:

```bash
# Ubuntu/Debian
apt install scrcpy

# Arch Linux
pacman -S scrcpy

# macOS
brew install scrcpy
```

If scrcpy is not available, HelixQA falls back to `adb shell screenrecord`. On Android 15 (SDK 35), `screenrecord` may fail with `Encoder failed (err=-38)` from ADB. In this case, HelixQA uses a screenshot-to-video approach: rapid screenshots assembled into video via ffmpeg.

### "Pipeline timeout reached"

The session exceeded its `--timeout` budget. HelixQA completes the current test, skips remaining tests, runs the analysis phase on collected evidence, and writes the session report.

**Fix**: This is normal behavior, not an error. To run more tests:
- Increase the timeout: `--timeout 30m`
- Reduce the number of generated tests by narrowing the platform or tag scope
- Run multiple passes instead of one long session

### "Permission denied" when writing results

HelixQA cannot write to the output directory.

**Fix**: Ensure the output directory exists and is writable:

```bash
mkdir -p qa-results
chmod 755 qa-results
```

In containers, ensure the volume mount has correct permissions:

```bash
podman run --rm \
  -v $(pwd)/qa-results:/output:Z \
  docker.io/vasicdigital/helixqa:latest \
  helixqa autonomous --project /project --output /output
```

The `:Z` suffix applies the correct SELinux context on systems with SELinux enabled.

## Log File Locations

HelixQA writes logs to several locations depending on the component:

| Log | Location | Contents |
|-----|----------|----------|
| Session log | `qa-results/session-<timestamp>/session.log` | Full session output including all phase logs |
| Pipeline report | `qa-results/session-<timestamp>/pipeline-report.json` | Structured session summary |
| Timeline | `qa-results/session-<timestamp>/timeline.json` | Millisecond-precision event log |
| Android logcat | `qa-results/session-<timestamp>/logs/logcat-filtered.txt` | Filtered device logs (crashes, ANRs, errors) |
| Browser console | `qa-results/session-<timestamp>/logs/console-errors.txt` | Browser console errors and warnings |
| LLM requests | `qa-results/session-<timestamp>/logs/llm-requests.log` | All LLM API requests and responses (when `--verbose` is set) |
| Memory database | `helixqa-memory.db` | SQLite database with all session history |

### Inspecting the Memory Database

The memory database can be queried directly with sqlite3:

```bash
# List all sessions
sqlite3 helixqa-memory.db "SELECT id, started_at, platform, tests_run, findings_count FROM sessions ORDER BY started_at DESC;"

# List all open findings
sqlite3 helixqa-memory.db "SELECT id, severity, category, title FROM findings WHERE status = 'open' ORDER BY severity;"

# Check coverage by screen
sqlite3 helixqa-memory.db "SELECT screen, times_tested, last_tested FROM coverage ORDER BY times_tested ASC;"
```

## Debug Mode

Enable verbose output to see detailed information about every operation HelixQA performs:

```bash
helixqa autonomous \
  --project . \
  --platforms web \
  --timeout 10m \
  --verbose
```

The `--verbose` flag enables:

- **Learning phase**: Lists every document read, every route discovered, every screen identified
- **Planning phase**: Shows the full LLM prompt and the generated test cases before execution
- **Execution phase**: Logs every test step as it executes (action, target, duration, result)
- **Analysis phase**: Shows the LLM vision prompt and raw response for each screenshot
- **Provider selection**: Logs which LLM provider and model is selected for each request
- **Evidence collection**: Logs every screenshot, video, and metric sample with file paths

### Environment Variable Debug Flags

Additional debug flags for specific subsystems:

```bash
# Log all LLM API requests and responses (warning: large output)
export HELIX_DEBUG_LLM=true

# Log all ADB commands and their output
export HELIX_DEBUG_ADB=true

# Log all Playwright actions and browser events
export HELIX_DEBUG_PLAYWRIGHT=true

# Log memory database queries
export HELIX_DEBUG_MEMORY=true

# Enable Go race detector (development only, significant performance impact)
export GORACE="log_path=qa-results/race.log"
```

### Dry Run Mode

To test the learning and planning phases without executing any tests:

```bash
helixqa autonomous \
  --project . \
  --platforms web \
  --timeout 1m \
  --dry-run
```

Dry run mode:
- Reads all project documentation and builds the knowledge base
- Generates the test plan via LLM
- Writes the test plan to `qa-results/session-<timestamp>/test-plan.json`
- Does not launch any browsers, connect to any devices, or execute any tests
- Does not consume significant LLM tokens (only the planning phase runs)

This is useful for verifying that HelixQA understands your project correctly and generates relevant test cases before committing to a full session.

## Reporting Issues

### Before Reporting

1. **Check this page** for known issues and their fixes
2. **Run with `--verbose`** to capture detailed output
3. **Check the session log** at `qa-results/session-<timestamp>/session.log`
4. **Verify your environment**: run `helixqa version` and confirm detected providers and platform tools
5. **Check the FAQ** at [FAQ](/faq) for common questions

### What to Include in a Report

When reporting an issue, include:

- **HelixQA version**: output of `helixqa version`
- **Go version**: output of `go version`
- **Operating system**: output of `uname -a`
- **Command run**: the exact `helixqa` command that produced the issue
- **Session log**: the full `session.log` file from the affected session
- **Pipeline report**: the `pipeline-report.json` from the affected session
- **Error output**: the exact error message from the terminal

### Where to Report

- **GitHub Issues**: For bugs, feature requests, and documentation improvements
- **Email**: For security vulnerabilities or sensitive issues that should not be public

### Sensitive Data

Before submitting logs, review them for sensitive data:

- API keys are automatically redacted in log output (`sk-ant-***`, `sk-or-v1-***`)
- Screenshots may contain application data -- review before attaching
- The memory database contains session history -- do not share if it contains sensitive test data
- LLM request logs (when `HELIX_DEBUG_LLM=true`) contain full prompts which may include project source code

## Getting Help

- [Documentation](/documentation) -- complete table of contents for all guides and references
- [FAQ](/faq) -- frequently asked questions and answers
- [Video Course](/course) -- 12-module course covering all HelixQA capabilities
- [Quick Start](/quick-start) -- get running in 5 minutes
- [Architecture](/architecture) -- understand how HelixQA works internally
