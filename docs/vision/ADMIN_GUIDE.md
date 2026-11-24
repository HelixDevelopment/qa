# Cheaper Vision Integration - Administrator Guide

## Table of Contents

1. [Deployment](#1-deployment)
2. [Scaling](#2-scaling)
3. [Backup and Recovery](#3-backup-and-recovery)
4. [Security](#4-security)
5. [Performance Tuning](#5-performance-tuning)
6. [Troubleshooting](#6-troubleshooting)
7. [Resource Limits](#7-resource-limits)

---

## 1. Deployment

### Bare Metal

Build the binary and run it under a user-level process manager. No root or sudo is required at any point.

```bash
cd HelixQA
go build -o bin/helixqa ./cmd/helixqa
```

Create a `.env` file beside the binary with your provider credentials:

```bash
HELIX_VISION_PROVIDER=auto
HELIX_VISION_FALLBACK_ENABLED=true
HELIX_VISION_FALLBACK_CHAIN=qwen25vl,glm4v,uitars,showui
HELIX_VISION_TIMEOUT=30s
HELIX_VISION_LEARNING_ENABLED=true
HELIX_VISION_EXACT_CACHE=true
HELIX_VISION_DIFFERENTIAL=true
HELIX_VISION_VECTOR_MEMORY=true
HELIX_VISION_FEW_SHOT=true
HELIX_VISION_PERSIST_PATH=$HOME/.local/share/helixqa/vision
HELIX_VISION_MAX_MEMORIES=100000
HELIX_VISION_CHANGE_THRESHOLD=0.05
GLM4V_API_KEY=your-key-here
```

Start the server:

```bash
./bin/helixqa server
```

To run it as a persistent background process, use user-level systemd:

```bash
mkdir -p ~/.config/systemd/user
```

Create `~/.config/systemd/user/helixqa-vision.service`:

```ini
[Unit]
Description=HelixQA Vision Server
After=network.target

[Service]
WorkingDirectory=$HOME/HelixQA
EnvironmentFile=%h/.local/share/helixqa/.env
ExecStart=$HOME/helix_qa/bin/helixqa server
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
```

Enable and start:

```bash
systemctl --user enable helixqa-vision
systemctl --user start helixqa-vision
systemctl --user status helixqa-vision
```

### Podman Container

This project uses Podman exclusively. Do not use Docker.

```bash
podman build --network host -t localhost/helixqa-vision:latest -f docker/Dockerfile .
```

Run the container with resource limits that respect the 30-40% host budget:

```bash
podman run --rm \
  --name helixqa-vision \
  --cpus=2 \
  --memory=4g \
  --network host \
  -e HELIX_VISION_PROVIDER=auto \
  -e GLM4V_API_KEY="${GLM4V_API_KEY}" \
  -e HELIX_VISION_PERSIST_PATH=/data/vision \
  -v "${HOME}/.local/share/helixqa/vision:/data/vision:Z" \
  localhost/helixqa-vision:latest
```

Key flags:
- `--network host` — required to reach local vision model servers (Qwen2.5-VL, ShowUI, OmniParser)
- `--cpus=2 --memory=4g` — mandatory resource caps
- `-v ...:Z` — the `:Z` SELinux label is required on systems with SELinux enforcing

### Podman Compose

Create a `docker-compose.vision.yml` in the project root:

```yaml
services:
  helixqa-vision:
    image: localhost/helixqa-vision:latest
    build:
      context: .
      dockerfile: docker/Dockerfile
    network_mode: host
    cpus: 2
    mem_limit: 4g
    env_file: .env
    volumes:
      - type: bind
        source: ${HOME}/.local/share/helixqa/vision
        target: /data/vision
    restart: on-failure

  qwen25vl:
    image: localhost/qwen25vl:latest
    network_mode: host
    cpus: 3
    mem_limit: 8g
    restart: on-failure
```

```bash
podman-compose -f docker-compose.vision.yml up -d
```

---

## 2. Scaling

### Horizontal Scaling with Shared Vector Memory

Multiple instances of the vision server can run in parallel as long as they share a single vector memory persistence directory. `chromem-go` writes on every `AddDocument` call, so the persistence path must be on a shared volume visible to all instances.

Nginx configuration for load balancing:

```nginx
upstream helixqa_vision {
    least_conn;
    server 127.0.0.1:8080;
    server 127.0.0.1:8081;
    server 127.0.0.1:8082;
}

server {
    listen 80;

    location /api/ {
        proxy_pass http://helixqa_vision;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_read_timeout 120s;
    }

    location /metrics {
        deny all;
    }
}
```

Start multiple instances with different port bindings:

```bash
HELIXQA_PORT=8080 HELIX_VISION_PERSIST_PATH=/shared/vision ./bin/helixqa server &
HELIXQA_PORT=8081 HELIX_VISION_PERSIST_PATH=/shared/vision ./bin/helixqa server &
HELIXQA_PORT=8082 HELIX_VISION_PERSIST_PATH=/shared/vision ./bin/helixqa server &
```

### Podman Compose with Multiple Replicas

Podman Compose does not support `deploy.replicas` natively. Use a scale loop instead:

```bash
for i in 1 2 3; do
  podman run -d \
    --name "helixqa-vision-${i}" \
    --cpus=1 --memory=2g \
    --network host \
    -e HELIXQA_PORT="808${i}" \
    -e HELIX_VISION_PERSIST_PATH=/shared/vision \
    -v /shared/vision:/shared/vision:Z \
    localhost/helixqa-vision:latest
done
```

---

## 3. Backup and Recovery

### What to Back Up

The only stateful artifact is the vector memory store. The exact and differential caches are ephemeral — they are rebuilt automatically from provider interactions and do not need to be backed up.

The persistence directory (set by `HELIX_VISION_PERSIST_PATH`) contains:
- `vision_memories/` — chromem-go persistent database files
- `vision_memories.gob` — single-file snapshot written by `VectorMemoryStore.Close()`

### Backup Script

Save as `~/.local/bin/backup-helixqa-vision.sh` and make it executable (`chmod 700`):

```bash
#!/bin/bash
set -euo pipefail

PERSIST_PATH="${HELIX_VISION_PERSIST_PATH:-${HOME}/.local/share/helixqa/vision}"
BACKUP_DIR="${HOME}/.local/share/helixqa/backups"
DATE=$(date +%Y%m%d_%H%M%S)
BACKUP_FILE="${BACKUP_DIR}/vision_memory_${DATE}.tar.gz"

mkdir -p "${BACKUP_DIR}"

tar -czf "${BACKUP_FILE}" -C "$(dirname "${PERSIST_PATH}")" "$(basename "${PERSIST_PATH}")"

# Retain backups from the last 30 days only
find "${BACKUP_DIR}" -name "vision_memory_*.tar.gz" -mtime +30 -delete

echo "Backup written to ${BACKUP_FILE}"
```

Run it on a schedule with user cron:

```bash
crontab -e
# Add: 0 3 * * * $HOME/.local/bin/backup-helixqa-vision.sh
```

### Restore Procedure

1. Stop the running server:

```bash
systemctl --user stop helixqa-vision
```

2. Replace the persistence directory with the backup contents:

```bash
PERSIST_PATH="${HELIX_VISION_PERSIST_PATH:-${HOME}/.local/share/helixqa/vision}"
BACKUP_FILE="/path/to/vision_memory_20260115_030000.tar.gz"

rm -rf "${PERSIST_PATH}"
mkdir -p "$(dirname "${PERSIST_PATH}")"
tar -xzf "${BACKUP_FILE}" -C "$(dirname "${PERSIST_PATH}")"
```

3. Verify the restored files:

```bash
ls -lh "${PERSIST_PATH}"
```

4. Restart the server:

```bash
systemctl --user start helixqa-vision
systemctl --user status helixqa-vision
```

---

## 4. Security

### API Key Management

API keys are read exclusively from environment variables. They must never appear in source code, YAML config files committed to git, or CLAUDE.md.

```bash
# Correct: set in the shell environment before starting the server
export GLM4V_API_KEY="your-key-here"
export UITARS_API_KEY="your-key-here"

# Correct: stored in a .env file that is gitignored
# Verify before every commit:
git ls-files --cached | grep "\.env"
# The above must produce no output.
```

Permissions on `.env` files:

```bash
chmod 600 .env
```

If a key is accidentally committed, revoke it immediately in the provider's dashboard and generate a new one before pushing any fix.

### Network Isolation

Local vision model servers (Qwen2.5-VL, ShowUI, OmniParser) should be accessible only from the host machine. Bind them to `127.0.0.1` explicitly when starting them, and do not expose their ports on external network interfaces.

For Podman setups where the vision server needs to reach local models, use `--network host` rather than creating a bridge network. This avoids the SSL and DNS resolution issues that Podman bridge networking introduces with local hostnames.

If the vision server must be exposed to a LAN, place it behind nginx with TLS termination:

```nginx
server {
    listen 443 ssl;
    ssl_certificate     /etc/ssl/certs/helixqa.crt;
    ssl_certificate_key /etc/ssl/private/helixqa.key;
    ssl_protocols       TLSv1.2 TLSv1.3;
    ssl_ciphers         HIGH:!aNULL:!MD5;

    location /api/ {
        proxy_pass http://127.0.0.1:8080;
    }
}
```

Generate a self-signed certificate for development:

```bash
openssl req -x509 -newkey rsa:4096 -keyout helixqa.key -out helixqa.crt \
  -days 365 -nodes -subj "/CN=helixqa.local"
```

### Secrets Never in Containers

When building container images, do not bake API keys into the image layer. Always pass them at runtime via environment variables or bind-mounted `.env` files:

```bash
podman run --rm \
  --env-file $HOME/.local/share/helixqa/.env \
  localhost/helixqa-vision:latest
```

---

## 5. Performance Tuning

### Cache TTL and Capacity

The exact cache is bounded by `HELIX_VISION_MAX_MEMORIES`. Start with the default of 100,000 and reduce if memory pressure is observed:

```bash
HELIX_VISION_MAX_MEMORIES=50000
```

The differential cache uses a fixed TTL of 5 minutes for frame states. Adjust the change threshold to control cache aggressiveness:

| `HELIX_VISION_CHANGE_THRESHOLD` | Effect |
|--------------------------------|--------|
| `0.01` | Very aggressive — reuse the previous response unless at least 1% of patches changed |
| `0.05` | Default — reuse unless 5% of patches changed |
| `0.15` | Conservative — only reuse for very similar screens |

Lower thresholds produce more cache hits but risk serving stale results for screens that changed subtly. 0.05 is appropriate for most QA workflows.

### Circuit Breaker Thresholds

The `ResilientExecutor` supports per-provider circuit breakers. Tune `CBFailureThreshold`, `CBSuccessThreshold`, and `CBTimeout` based on observed provider stability:

| Scenario | Recommended Settings |
|----------|---------------------|
| Stable cloud provider with occasional timeouts | `CBFailureThreshold=5`, `CBSuccessThreshold=2`, `CBTimeout=30s` |
| Flaky local model server | `CBFailureThreshold=3`, `CBSuccessThreshold=1`, `CBTimeout=60s` |
| Production with strict SLA | `CBFailureThreshold=2`, `CBSuccessThreshold=3`, `CBTimeout=15s` |

### Provider Priority and Weights

When using the `weighted` or `fallback` strategies, order providers from cheapest/fastest to most expensive:

```yaml
vision:
  strategy: "fallback"
  providers:
    - name: "glm-4v"         # free tier, ~1s latency
      priority: 1
    - name: "showui-2b"      # free local, ~500ms latency
      priority: 2
    - name: "qwen2.5-vl"     # free local, ~3s latency
      priority: 3
    - name: "ui-tars-1.5"    # HF inference, ~2s latency
      priority: 4
```

### Parallel Execution Concurrency

When using `first_success` or `parallel` strategies, all providers are fired concurrently. On a resource-constrained host, limit the number of simultaneous in-flight calls:

```go
executor := cheaper.NewResilientExecutor(cheaper.ExecutorConfig{
    Strategy:       cheaper.StrategyFirstSuccess,
    Providers:      providers,
    MaxConcurrency: 3,
    Timeout:        30 * time.Second,
})
```

---

## 6. Troubleshooting

### Provider Issues

| Symptom | Likely Cause | Resolution |
|---------|-------------|------------|
| `glm4v: API returned status 401` | Invalid or expired API key | Rotate `GLM4V_API_KEY` in your `.env` file |
| `uitars: API returned status 429` | Hugging Face rate limit exceeded | Switch to `fallback` strategy; add delay between calls |
| `qwen2.5-vl: health check got status 404` | Qwen server not running or wrong port | Verify the local vLLM/llama.cpp server is running on port 9192 |
| `showui: HTTP POST: connection refused` | Gradio app not started | Start the ShowUI-2B Gradio application |
| `omniparser-v2: API returned status 500` | OmniParser internal error | Check the Gradio app logs; may need to restart the service |
| Circuit breaker state = 2 (open) for a provider | Multiple consecutive failures | Wait for `CBTimeout` to elapse; or clear via `POST /api/v1/providers/{name}/enable` |

### Performance Issues

| Symptom | Likely Cause | Resolution |
|---------|-------------|------------|
| `cheaper_vision_cache_hits_total{layer="exact"}` stays at 0 | Exact cache disabled or screenshots differ every call | Verify `HELIX_VISION_EXACT_CACHE=true`; check whether screenshots are truly identical |
| High p95 latency despite cache enabled | Cache misses — screenshots differ too much for differential | Lower `HELIX_VISION_CHANGE_THRESHOLD` to 0.03 |
| Memory grows unboundedly | Vector store capacity not configured | Set `HELIX_VISION_MAX_MEMORIES=50000` |
| All providers slow simultaneously | Host CPU overloaded | Reduce `MaxConcurrency`; check `cat /proc/loadavg` |
| `first_success` no faster than single provider | Single provider returning before others are even tried | Providers are already fast; try `fallback` to avoid wasted parallel calls |

### Learning System Issues

| Symptom | Likely Cause | Resolution |
|---------|-------------|------------|
| L3 cache never hit | `HELIX_VISION_VECTOR_MEMORY=false` | Set to `true` in `.env` |
| Vector memory not persisting across restarts | `HELIX_VISION_PERSIST_PATH` not set or not writable | Set to a writable directory: `HELIX_VISION_PERSIST_PATH=$HOME/.local/share/helixqa/vision` |
| Few-shot examples not improving accuracy | Fewer than ~50 successful interactions stored | The L4 layer improves as interactions accumulate; give it more sessions |
| Provider optimizer returns empty string | All providers are stale (no calls within 10 minutes) | The optimizer requires recent activity; make at least one call to warm up metrics |

### Viewing Logs

```bash
# User systemd service logs
journalctl --user -u helixqa-vision -f

# Filter for provider errors only
journalctl --user -u helixqa-vision | grep "ERROR"

# Filter by specific provider
journalctl --user -u helixqa-vision | grep "glm4v"
```

---

## 7. Resource Limits

This project runs on a host that also serves other mission-critical processes. The vision subsystem must stay within 30-40% of total host resources.

### Recommended Container Limits

| Component | CPUs | Memory |
|-----------|------|--------|
| helixqa-vision server | 2 | 4g |
| Qwen2.5-VL (vLLM/llama.cpp) | 3 | 8g |
| ShowUI-2B (Gradio) | 1 | 2g |
| OmniParser V2 (Gradio) | 1 | 2g |
| Total budget | 4 max | 8g max |

### Monitoring Host Load

Before starting additional vision providers, verify the current host load:

```bash
cat /proc/loadavg
podman stats --no-stream
```

If the 1-minute load average exceeds 60% of total vCPU count, do not start additional providers.

### Go Runtime Limits

When running Go tests or benchmarks involving the cheaper vision package, apply the standard project resource constraints:

```bash
GOMAXPROCS=3 go test ./pkg/vision/cheaper/... -p 2 -parallel 2
```
