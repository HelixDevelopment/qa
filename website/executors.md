# Platform Executors

HelixQA uses a dedicated executor for each target platform. Each executor handles navigation, screenshot capture, video recording, and crash detection using the native tooling for that platform.

## Executor Overview

| Executor | Platform | Navigation | Video | Screenshots | Crash Detection |
|----------|----------|-----------|-------|-------------|----------------|
| ADB | Android / Android TV | adb shell input | scrcpy / screenrecord | adb shell screencap | logcat |
| Playwright | Web | Playwright API | Playwright video | Playwright screenshot | console errors |
| X11 | Desktop (Linux) | xdotool | ffmpeg x11grab | ImageMagick import | process exit code |
| CLI | CLI / TUI | stdin/stdout | — | — | exit code |
| API | REST API | HTTP client | — | — | HTTP status codes |

## Android / Android TV Executor

The ADB executor controls Android and Android TV devices over the Android Debug Bridge.

### Setup

```bash
# Connect a device over Wi-Fi
adb connect 192.0.2.214:5555
adb devices   # confirm connection

# Set environment variables
export HELIX_ANDROID_DEVICE="192.0.2.214:5555"
export HELIX_ANDROID_PACKAGE="com.your.app"
```

### Navigation

The executor sends input events via `adb shell input`:

```bash
adb shell input tap 540 960          # tap at coordinates
adb shell input keyevent KEYCODE_BACK
adb shell input keyevent KEYCODE_DPAD_RIGHT
adb shell input swipe 100 900 100 200 300   # scroll up
```

For Android TV, D-pad navigation is used exclusively:

```bash
adb shell input keyevent KEYCODE_DPAD_UP
adb shell input keyevent KEYCODE_DPAD_CENTER   # select
```

### Video Recording

HelixQA selects the recording method based on Android SDK version:

| SDK Version | Method | Command |
|-------------|--------|---------|
| Android 9 and below | `screenrecord` | `adb shell screenrecord --bit-rate 4000000 /sdcard/qa.mp4` |
| Android 10+ | Screenshot sequence + ffmpeg | `adb shell screencap` assembled via ffmpeg |
| Any version | scrcpy (preferred) | `scrcpy --record output.mp4 --no-display` |

After recording, the file is pulled from the device:

```bash
adb pull /sdcard/qa.mp4 qa-results/session-XXX/videos/
```

### Crash and ANR Detection

A background goroutine monitors `adb logcat` throughout test execution:

```bash
adb logcat -v threadtime | grep -E "FATAL EXCEPTION|ANR|Force Close"
```

Any crash triggers an immediate screenshot capture and finding creation.

### Device-Specific Notes

- **Android 15 (SDK 35)**: `screenrecord` fails with `Encoder failed (err=-38)` — use the screenshot-to-video approach
- **Mi Box (Android 9)**: Native `screenrecord` works; use `--bit-rate 4000000 --time-limit 120`
- **Emulators**: Use `docker-android` (see [Open-Source Tools](/advanced/tools)) for containerized emulators

## Web Executor (Playwright)

The Playwright executor controls a Chromium, Firefox, or WebKit browser for web application testing.

### Setup

```bash
# Install Playwright browsers
npx playwright install chromium

export HELIX_WEB_URL="http://localhost:3000"
```

### Navigation

The executor uses the Playwright API for all browser interactions:

```go
page.Goto("http://localhost:3000/login")
page.Fill("#username", "admin")
page.Click("button[type=submit]")
page.WaitForSelector(".dashboard")
```

### Video Recording

Playwright's built-in video recording captures the full session:

```go
context, _ := browser.NewContext(playwright.BrowserNewContextOptions{
    RecordVideo: &playwright.RecordVideo{Dir: "qa-results/videos/"},
})
```

### Error Detection

The executor listens for browser console errors throughout execution:

```go
page.On("console", func(msg playwright.ConsoleMessage) {
    if msg.Type() == "error" {
        // record as finding
    }
})
```

Failed network requests (4xx, 5xx) are also captured as findings per the Zero Warning Policy.

## Desktop Executor (X11)

The X11 executor automates Linux desktop applications using `xdotool` for input and `ffmpeg` for recording.

### Setup

```bash
export HELIX_DESKTOP_DISPLAY=":0"   # default X11 display
export HELIX_FFMPEG_PATH="/usr/bin/ffmpeg"
```

### Navigation

```bash
# Click at screen coordinates
xdotool mousemove 540 300 click 1

# Type text
xdotool type "search query"

# Press key
xdotool key Return
```

### Video Recording

```bash
ffmpeg -video_size 1920x1080 \
       -framerate 30 \
       -f x11grab \
       -i :0.0 \
       -codec:v libx264 \
       output.mp4
```

### Screenshots

```bash
import -window root screenshot.png    # ImageMagick
```

## CLI Executor

The CLI executor interacts with command-line or terminal UI applications via stdin/stdout.

```bash
# Example: testing a CLI tool
helixqa autonomous \
  --project . \
  --platforms cli \
  --timeout 5m
```

Test steps send input strings and assert expected output patterns.

## API Executor

The API executor tests REST API endpoints directly using an HTTP client, without a UI layer.

```bash
export HELIX_API_URL="http://localhost:8080"
export HELIX_API_TOKEN="jwt-token-here"
```

Test cases can include:
- Authentication flows
- CRUD operations with schema validation
- Error response handling
- Rate limiting behaviour
- Response time assertions

## Selecting Platforms

Use the `--platforms` flag to specify which executors to activate:

```bash
# Single platform
helixqa autonomous --project . --platforms web

# Multiple platforms
helixqa autonomous --project . --platforms "android,web"

# All configured platforms
helixqa autonomous --project . --platforms all
```

The `all` value activates every executor for which the required environment variables are set.

## Related Pages

- [Pipeline Phases](/pipeline) — how executors fit into the execution phase
- [Installation](/installation) — setting up ADB, Playwright, and ffmpeg
- [Advanced: Containers](/advanced/containers) — running executors inside containers
