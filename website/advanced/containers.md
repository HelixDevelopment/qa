# Containerization

HelixQA ships with container support for fully reproducible, dependency-free QA execution. The container includes all required system tools: ADB, Playwright, ffmpeg, ImageMagick, and xdotool.

## Quick Start

### Using Podman (recommended)

```bash
podman-compose -f docker-compose.qa-robot.yml up
```

### Using Docker

```bash
docker compose -f docker-compose.qa-robot.yml up
```

## docker-compose.qa-robot.yml

The QA robot compose file runs HelixQA against your application stack:

```yaml
services:
  helixqa:
    image: docker.io/vasicdigital/helixqa:latest
    network_mode: host
    environment:
      - ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY}
      - HELIX_WEB_URL=http://localhost:3000
      - HELIX_ANDROID_DEVICE=${HELIX_ANDROID_DEVICE}
      - HELIX_ANDROID_PACKAGE=${HELIX_ANDROID_PACKAGE}
    volumes:
      - ./qa-results:/app/qa-results
      - ./docs/issues:/app/docs/issues
      - ./helix_qa/data:/app/helix_qa/data
      - /dev/bus/usb:/dev/bus/usb   # ADB USB passthrough
    command: >
      autonomous
      --project /app
      --platforms "android,web"
      --timeout 1h
      --output /app/qa-results
```

## Building the Container Image

```bash
# Build with host networking (required — avoids SSL issues with package registries)
podman build --network host -t helixqa:local .

# Or with Docker
docker build --network host -t helixqa:local .
```

### Dockerfile Structure

The HelixQA Dockerfile uses a multi-stage build:

```dockerfile
# Stage 1: Builder
FROM docker.io/library/golang:1.24 AS builder
WORKDIR /build
COPY . .
RUN GOTOOLCHAIN=local go build -o bin/helixqa ./cmd/helixqa

# Stage 2: Runtime
FROM docker.io/library/ubuntu:22.04
RUN apt-get update && apt-get install -y \
    adb ffmpeg imagemagick xdotool nodejs npm \
    && rm -rf /var/lib/apt/lists/*
RUN npx playwright install --with-deps chromium
COPY --from=builder /build/bin/helixqa /usr/local/bin/helixqa
```

## Critical Container Notes

### Network Mode

Always use `network_mode: host` or `--network host` for HelixQA containers. The ADB executor, Playwright web executor, and API executor all need to reach services running on the host or on the local network:

```bash
podman run --network host helixqa:local autonomous --project /app --platforms web
```

### Android USB Device Passthrough

To connect a physical Android device over USB inside a container:

```yaml
volumes:
  - /dev/bus/usb:/dev/bus/usb
devices:
  - /dev/bus/usb
```

For Wi-Fi ADB (recommended for containers), no device passthrough is needed — just set `HELIX_ANDROID_DEVICE` to the device's IP:port.

### GOTOOLCHAIN

Set `GOTOOLCHAIN=local` when building inside containers to prevent Go from trying to download a newer toolchain version, which fails without network access to `dl.google.com`:

```bash
ENV GOTOOLCHAIN=local
```

### AppImage / Tauri

If testing a Tauri desktop application inside a container that uses AppImage bundling, set:

```bash
ENV APPIMAGE_EXTRACT_AND_RUN=1
```

This is required because FUSE is unavailable inside most containers.

## Kubernetes Deployment

### QA Job

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: helixqa-pass
spec:
  template:
    spec:
      containers:
      - name: helixqa
        image: vasicdigital/helixqa:latest
        env:
        - name: ANTHROPIC_API_KEY
          valueFrom:
            secretKeyRef:
              name: llm-keys
              key: anthropic
        - name: HELIX_WEB_URL
          value: "http://my-app-service:3000"
        args:
        - autonomous
        - --project=/app
        - --platforms=web
        - --timeout=1h
        volumeMounts:
        - name: qa-results
          mountPath: /app/qa-results
        - name: memory
          mountPath: /app/helix_qa/data
      volumes:
      - name: qa-results
        persistentVolumeClaim:
          claimName: qa-results-pvc
      - name: memory
        persistentVolumeClaim:
          claimName: helixqa-memory-pvc
      restartPolicy: Never
```

### Scheduled CronJob

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: helixqa-nightly
spec:
  schedule: "0 2 * * *"   # 2 AM daily
  jobTemplate:
    spec:
      template:
        spec:
          containers:
          - name: helixqa
            image: vasicdigital/helixqa:latest
            args:
            - autonomous
            - --project=/app
            - --platforms=web
            - --timeout=45m
```

## Resource Limits

HelixQA is a CPU and memory intensive workload during execution. Apply appropriate limits to avoid saturating the host:

```yaml
resources:
  requests:
    cpu: "1"
    memory: "2Gi"
  limits:
    cpu: "2"
    memory: "4Gi"
```

## Containerized Android Emulators

For CI environments without physical devices, use the `docker-android` open-source tool (vendored flat at `<root>/submodules/docker_android`; resolve with `scripts/resolve_tool.sh docker-android`; legacy path `tools/opensource/docker-android`):

```yaml
services:
  android-emulator:
    image: docker.io/budtmo/docker-android:emulator_14.0
    privileged: true
    environment:
      - DEVICE=Samsung Galaxy S10
    ports:
      - "5554:5554"
      - "5555:5555"

  helixqa:
    depends_on:
      - android-emulator
    environment:
      - HELIX_ANDROID_DEVICE=localhost:5555
```

## Related Pages

- [Platform Executors](/executors) — executor-specific setup requirements
- [Open-Source Tools](/advanced/tools) — docker-android and other integrated tools
- [Installation](/installation) — non-containerized setup
