# Open-Source Tools

HelixQA integrates 26 open-source tools (plus 1 test app, `rest-demo`, per `docs/flat-tools-manifest.tsv`) as git submodules. They are vendored flat in the consumer project at `<root>/submodules/<snake_name>/` (list and pinned SHAs: `docs/flat-tools-manifest.tsv`); the legacy `tools/opensource/<name>` location is still honoured as a fallback. Each tool addresses a specific aspect of QA automation — device recording, vision-driven interaction, memory leak detection, test reporting, and more.

## Tool Index

| Tool | Category | Purpose |
|------|----------|---------|
| scrcpy | Android | Screen mirroring and video recording |
| appium | Mobile automation | Cross-platform mobile test automation |
| midscene | Vision automation | Vision-driven UI interaction and testing |
| allure2 | Reporting | Rich test report generation |
| leakcanary | Android memory | Runtime memory leak detection |
| docker-android | Emulation | Containerised Android emulators |
| ui-tars | Vision model | GUI-specialized vision model for analysis |
| moondream | Vision model | Lightweight on-device vision model |
| mem0 | Agent memory | Agent memory layer with vector search |
| chroma | Vector database | Embedding storage and similarity search |
| perfetto | Android tracing | System-level performance tracing |
| shortest | E2E testing | Natural language E2E test authoring |
| stagehand | Browser automation | AI-driven browser automation |
| testdriverai | OS automation | OS-level AI testing (mouse, keyboard) |
| kiwi-tcms | Test management | Open-source test case management system |
| unstructured | Document parsing | Parsing PDFs, docs, and unstructured data |
| marker | Document conversion | PDF to markdown conversion |
| docling | Document understanding | Structured document understanding |
| llama-index | RAG | Retrieval-augmented generation framework |
| signoz | Observability | Open-source APM and distributed tracing |
| redroid | Android emulation | Android in a container (no hardware required) |
| appcrawler | Android crawling | Automated Android app crawling |

## Submodule Location

```
<root>/submodules/   (legacy fallback: tools/opensource/, names with dashes: ui-tars/)
├── scrcpy/
├── appium/
├── midscene/
├── allure2/
├── leakcanary/
├── docker-android/
├── ui-tars/
├── moondream/
├── mem0/
├── chroma/
├── perfetto/
├── shortest/
├── stagehand/
├── testdriverai/
├── kiwi-tcms/
├── unstructured/
├── marker/
├── docling/
├── llama-index/
├── signoz/
├── redroid/
└── appcrawler/
```

Initialise the tool submodules from the consumer project root, one per row of `docs/flat-tools-manifest.tsv` (flat name `new_name`, same pinned SHA):

```bash
git submodule add <url> submodules/<new_name>   # then: git -C submodules/<new_name> checkout <pinned_sha>
git submodule update --init submodules/<new_name>
```

Locate a tool from code or scripts with `pkg/opensource.ResolveTool` or `scripts/resolve_tool.sh <name>`; set `HELIXQA_TOOLS_DIR` to override (authoritative: a miss is an error).

## Key Tools in Detail

### scrcpy — Android Recording

scrcpy provides high-quality Android screen recording and mirroring without root access. HelixQA uses it as the preferred recording method for all Android SDK versions:

```bash
# Record to file, no display window
scrcpy --record qa-session.mp4 --no-display

# With bitrate control
scrcpy --record qa-session.mp4 --no-display --video-bit-rate 4M
```

scrcpy handles all SDK versions uniformly, unlike `adb shell screenrecord` which fails on Android 15 (SDK 35).

### docker-android — Containerised Emulators

docker-android runs Android emulators inside containers without requiring KVM or hardware virtualisation on the host:

```bash
podman run --privileged \
  -e DEVICE="Samsung Galaxy S10" \
  -p 5554:5554 \
  -p 5555:5555 \
  docker.io/budtmo/docker-android:emulator_14.0
```

After the emulator boots, connect via ADB:

```bash
adb connect localhost:5555
export HELIX_ANDROID_DEVICE="localhost:5555"
```

See [Containerization](/advanced/containers) for a full compose example with HelixQA and docker-android together.

### redroid — Android in Container

redroid provides a lighter-weight alternative to docker-android for Android-in-container scenarios, suitable for CI environments:

```bash
podman run -itd --rm --privileged \
  -p 5555:5555 \
  docker.io/redroid/redroid:13.0.0-latest
```

### leakcanary — Memory Leak Detection

leakcanary detects memory leaks in Android apps at runtime. HelixQA's performance monitoring integrates with leakcanary reports to correlate heap growth with specific UI actions.

Add leakcanary to your Android app's debug build:

```kotlin
// app/build.gradle.kts
debugImplementation("com.squareup.leakcanary:leakcanary-android:2.14")
```

HelixQA reads leakcanary output from logcat and incorporates it into the performance findings.

### moondream — Lightweight Vision Analysis

moondream is a small vision model that can run locally on CPU. It is used as a fallback when no cloud vision provider is configured, or as a cost-effective supplement for screenshot pre-screening:

```bash
# Run moondream locally via Ollama
ollama pull moondream
export HELIX_OLLAMA_URL="http://localhost:11434"
```

### ui-tars — GUI-Specialised Vision

ui-tars is a vision model specifically trained on GUI screenshots, providing more accurate analysis of UI elements than general-purpose vision models. Used when available for the screenshot analysis phase.

### allure2 — Test Reports

allure2 generates rich interactive HTML test reports. HelixQA can export results in allure format:

```bash
helixqa report \
  --input qa-results/session-* \
  --format allure

# Open the report
allure serve qa-results/allure-results/
```

### perfetto — Android System Tracing

perfetto captures detailed system-level performance traces on Android, including CPU scheduling, memory allocation, and I/O activity. HelixQA triggers perfetto traces around performance-sensitive test steps:

```bash
adb shell perfetto \
  -c - --txt \
  -o /data/misc/perfetto-traces/trace.perfetto-trace
```

### signoz — APM Integration

signoz provides open-source application performance monitoring. Connect HelixQA sessions to your signoz instance to correlate QA test execution with backend service metrics:

```bash
export HELIX_SIGNOZ_URL="http://localhost:3301"
```

## Adding Custom Tools

To integrate an additional open-source tool:

1. Add it as a flat git submodule at `<root>/submodules/<snake_name>/` and add a row to `docs/flat-tools-manifest.tsv`
2. Implement the relevant interface in `pkg/navigator/` (for executors) or `pkg/analysis/` (for analysis tools)
3. Wire it into the executor factory in `pkg/autonomous/`

## Related Pages

- [Platform Executors](/executors) — how executors use these tools
- [Pipeline Phases](/pipeline) — where each tool category fits in the pipeline
- [Containerization](/advanced/containers) — running tools in containers
