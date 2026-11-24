# Download

HelixQA can be installed from source, pulled as a container image, or built as a standalone binary. Choose the method that fits your workflow.

## Build from Source

The recommended method for development and customization.

```bash
# Clone the repository
git clone https://github.com/HelixDevelopment/HelixQA.git
cd HelixQA

# Initialize submodules (Challenges, Containers)
git submodule init && git submodule update --recursive

# Build the binary
go build -o bin/helixqa ./cmd/helixqa

# Verify
./bin/helixqa version
```

Add to your PATH for convenient access:

```bash
export PATH="$PATH:$(pwd)/bin"
```

## Install with `go install`

For users who want a single binary without cloning the repository:

```bash
# Install the latest version
go install digital.vasic.helixqa/cmd/helixqa@latest

# The binary is placed in $GOPATH/bin (or $HOME/go/bin)
helixqa version
```

Ensure `$GOPATH/bin` is in your `PATH`:

```bash
export PATH="$PATH:$(go env GOPATH)/bin"
```

### Installing a Specific Version

```bash
go install digital.vasic.helixqa/cmd/helixqa@v0.9.0
go install digital.vasic.helixqa/cmd/helixqa@v0.8.0
```

## Container Image

HelixQA ships a container image with all system dependencies pre-installed: ADB, Playwright, ffmpeg, xdotool, ImageMagick, and scrcpy. This is the recommended method for CI/CD and team-wide deployment.

### Pull with Podman (Recommended)

```bash
# Pull the latest image
podman pull docker.io/vasicdigital/helixqa:latest

# Pull a specific version
podman pull docker.io/vasicdigital/helixqa:0.9.0

# Verify
podman run --rm docker.io/vasicdigital/helixqa:latest helixqa version
```

### Run a Session in a Container

```bash
# Basic web testing session
podman run --rm \
  --network host \
  -e OPENROUTER_API_KEY="${OPENROUTER_API_KEY}" \
  -e HELIX_WEB_URL="http://localhost:3000" \
  -v $(pwd):/project:ro \
  -v $(pwd)/qa-results:/output \
  docker.io/vasicdigital/helixqa:latest \
  helixqa autonomous \
    --project /project \
    --platforms web \
    --timeout 10m \
    --output /output
```

### Android Device Testing in a Container

```bash
# Pass USB devices for ADB access
podman run --rm \
  --network host \
  --device /dev/bus/usb \
  -e OPENROUTER_API_KEY="${OPENROUTER_API_KEY}" \
  -e HELIX_ANDROID_DEVICE="192.0.2.214:5555" \
  -e HELIX_ANDROID_PACKAGE="com.your.app" \
  -v $(pwd):/project:ro \
  -v $(pwd)/qa-results:/output \
  docker.io/vasicdigital/helixqa:latest \
  helixqa autonomous \
    --project /project \
    --platforms android \
    --timeout 15m \
    --output /output
```

### Using Podman Compose

HelixQA includes a `docker-compose.qa-robot.yml` for fully orchestrated sessions:

```bash
# Start the QA robot alongside your application stack
podman-compose -f docker-compose.qa-robot.yml up

# Or combine with your application's compose file
podman-compose \
  -f docker-compose.dev.yml \
  -f docker-compose.qa-robot.yml \
  up
```

### Container Resource Limits

When running on shared machines, apply resource limits to avoid impacting other processes:

```bash
podman run --rm \
  --cpus=2 \
  --memory=4g \
  --network host \
  -e OPENROUTER_API_KEY="${OPENROUTER_API_KEY}" \
  -v $(pwd):/project:ro \
  -v $(pwd)/qa-results:/output \
  docker.io/vasicdigital/helixqa:latest \
  helixqa autonomous \
    --project /project \
    --platforms web \
    --timeout 10m \
    --output /output
```

### Building the Container Image Locally

```bash
# Build with host networking (required for SSL)
podman build \
  --network host \
  -t helixqa:local \
  -f Dockerfile .

# Verify the local build
podman run --rm helixqa:local helixqa version
```

## Version Compatibility Matrix

HelixQA depends on Go, the Challenges module, and the Containers module. The following matrix shows tested version combinations:

| HelixQA | Go | Challenges | Containers | Status |
|---------|-----|------------|-----------|--------|
| v0.9.0 | 1.24+ | v0.12.0+ | v0.6.0+ | Current |
| v0.8.0 | 1.24+ | v0.11.0+ | v0.5.0+ | Supported |
| v0.7.0 | 1.23+ | v0.10.0+ | v0.4.0+ | End of life |

### Platform Tool Requirements

| Tool | Required For | Minimum Version | Notes |
|------|-------------|----------------|-------|
| Go | Building HelixQA | 1.24+ | Set `GOTOOLCHAIN=local` in containers |
| ADB (platform-tools) | Android / Android TV | any | USB or Wi-Fi connection |
| Node.js | Web testing (Playwright) | 18+ | LTS recommended |
| Playwright | Web testing | 1.40+ | `npx playwright install chromium` |
| ffmpeg | Desktop video recording | any | x11grab support required |
| scrcpy | Android video recording | 2.0+ | Optional, falls back to screenrecord |
| xdotool | Desktop interaction | any | X11 only |
| ImageMagick | Desktop screenshots | any | `import` command required |
| SQLite | Memory store | 3.35+ | WAL mode support required |

### Operating System Support

| OS | Architecture | Status |
|----|-------------|--------|
| Linux (x86_64) | amd64 | Primary, fully tested |
| Linux (aarch64) | arm64 | Tested on Raspberry Pi 4+ |
| macOS (Apple Silicon) | arm64 | Tested, desktop executor requires XQuartz |
| macOS (Intel) | amd64 | Tested |
| Windows (WSL2) | amd64 | Works via WSL2 with X11 forwarding |

### LLM Provider SDK Versions

HelixQA uses HTTP APIs directly (no provider-specific SDKs). Any provider with an OpenAI-compatible API endpoint is supported. The adaptive provider handles differences in request/response format across providers.

## Verifying Your Installation

After installing by any method, run the following to confirm everything is correctly configured:

```bash
# Check HelixQA version and detected providers
helixqa version

# Dry-run: learn and plan only, no execution
helixqa autonomous \
  --project /path/to/your/project \
  --platforms web \
  --timeout 1m \
  --dry-run

# Run the self-validation challenges
helixqa run --challenges
```

## Next Steps

- [Getting Started](/getting-started) -- first autonomous session walkthrough
- [Installation](/installation) -- detailed installation with LLM provider setup and device configuration
- [Configuration Reference](/reference/config) -- all environment variables and config options
