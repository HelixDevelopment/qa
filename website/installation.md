# Installation

## Prerequisites

Before installing HelixQA, ensure the following tools are available on your system:

| Tool | Required For | Minimum Version |
|------|-------------|----------------|
| Go | Building HelixQA | 1.24+ |
| ADB (platform-tools) | Android / Android TV testing | any |
| Node.js + Playwright | Web testing | Node 18+ |
| ffmpeg | Video recording (Desktop) | any |
| At least one LLM API key | All autonomous operations | — |

## Build from Source

```bash
# Clone the repository
git clone https://github.com/HelixDevelopment/qa.git
cd HelixQA

# Build the binary
go build -o bin/helixqa ./cmd/helixqa

# Verify the build
./bin/helixqa version
```

Add `bin/` to your `PATH` for convenient access:

```bash
export PATH="$PATH:$(pwd)/bin"
```

## Containerized Setup

HelixQA ships with a `docker-compose.qa-robot.yml` for fully containerized execution. The container includes all system dependencies (ADB, Playwright, ffmpeg).

### Using Podman (recommended)

```bash
podman-compose -f docker-compose.qa-robot.yml up
```

### Using Docker

```bash
docker compose -f docker-compose.qa-robot.yml up
```

### Container Notes

- Use `--network host` for builds: `podman build --network host`
- Set `GOTOOLCHAIN=local` to prevent Go from downloading toolchain updates inside the container
- Use fully qualified image names (e.g., `docker.io/library/ubuntu:22.04`) — short names may fail without a TTY

## Configure an LLM Provider

HelixQA requires at least one LLM API key. Set the corresponding environment variable before running:

```bash
# OpenRouter — recommended for beginners (access to 100+ models)
export OPENROUTER_API_KEY="sk-or-v1-..."

# DeepSeek — lowest cost option
export DEEPSEEK_API_KEY="sk-..."

# Anthropic Claude — highest quality analysis
export ANTHROPIC_API_KEY="sk-ant-..."

# Groq — fastest inference
export GROQ_API_KEY="gsk_..."

# Ollama — fully self-hosted, no API key required
export HELIX_OLLAMA_URL="http://localhost:11434"
```

HelixQA auto-discovers available providers at startup by scanning all `*_API_KEY` environment variables. Multiple providers can be set simultaneously; the adaptive provider selects the best available one per request.

See [LLM Providers](/providers) for the complete list of 40+ supported providers.

## Connect Devices (Optional)

### Android / Android TV

```bash
# Connect over network (Wi-Fi ADB)
adb connect 192.0.2.214:5555

# Configure HelixQA
export HELIX_ANDROID_DEVICE="192.0.2.214:5555"
export HELIX_ANDROID_PACKAGE="com.your.app"
```

### Web Application

```bash
export HELIX_WEB_URL="http://localhost:3000"
```

### Desktop Application

```bash
# X11 display (default is :0)
export HELIX_DESKTOP_DISPLAY=":0"
```

## Verify Installation

Run a quick health check to confirm everything is wired correctly:

```bash
# List available platforms and providers
helixqa version

# Dry-run: learn and plan only, no execution
helixqa autonomous \
  --project /path/to/your/project \
  --platforms web \
  --timeout 1m \
  --dry-run
```

## Environment File

For persistent configuration, create a `.env` file in the project root or in `helix_qa/`:

```env
ANTHROPIC_API_KEY=sk-ant-...
HELIX_WEB_URL=http://localhost:3000
HELIX_ANDROID_DEVICE=192.0.2.214:5555
HELIX_ANDROID_PACKAGE=com.example.myapp
```

Pass it to the CLI with `--env`:

```bash
helixqa autonomous --project . --platforms all --env .env
```

## Next Steps

- [Quick Start](/quick-start) — run your first autonomous session
- [CLI Reference](/manual/cli) — full command and flag reference
- [Configuration](/manual/config) — all environment variables documented
