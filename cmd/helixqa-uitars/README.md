# `helixqa-uitars`

llama.cpp `llama-server` deployment recipe for UI-TARS-1.5-7B. Consumed by `pkg/agent/uitars` on the Go side.

**Status:** operator-action. Go client code-complete + 95.8% tested (M36).

## Model files

```
~/models/ui-tars/
├── ui-tars-1.5-7b.Q4_K_M.gguf   (main weights, ~4.4 GB)
└── mmproj.gguf                   (vision projection weights, ~500 MB)
```

Download from the published UI-TARS releases (ByteDance Seed) or quantize yourself from the HF repo.

## llama-server launch

```bash
cd ~/llama.cpp

./llama-server \
    -m ~/models/ui-tars/ui-tars-1.5-7b.Q4_K_M.gguf \
    --mmproj ~/models/ui-tars/mmproj.gguf \
    --host 0.0.0.0 \
    --port 18100 \
    --alias ui-tars-1.5-7b \
    --ctx-size 4096 \
    --n-gpu-layers 999 \
    --temp 0.0 \
    --threads 8
```

- `--mmproj` enables vision: UI-TARS can see images passed via
  OpenAI `image_url` content parts.
- `--alias ui-tars-1.5-7b` matches the default `uitars.Client.Model`
  value — leave unchanged unless you also update the client.
- `--temp 0.0` matches the HelixQA default for deterministic QA.
- `--ctx-size 4096` fits one screenshot (typically ~1.5k tokens
  after vision encoder) + a goal string + a 256-token response.

## Systemd user service (recommended for unattended runs)

`~/.config/systemd/user/helixqa-uitars.service`:

```ini
[Unit]
Description=HelixQA UI-TARS llama-server
After=network.target

[Service]
ExecStart=/home/%u/llama.cpp/llama-server \
    -m /home/%u/models/ui-tars/ui-tars-1.5-7b.Q4_K_M.gguf \
    --mmproj /home/%u/models/ui-tars/mmproj.gguf \
    --host 0.0.0.0 --port 18100 --alias ui-tars-1.5-7b \
    --ctx-size 4096 --n-gpu-layers 999 --temp 0.0 --threads 8
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
```

```bash
systemctl --user daemon-reload
systemctl --user enable --now helixqa-uitars
systemctl --user status helixqa-uitars
```

No "sudo" required — user systemd only (per project CLAUDE.md).

## Acceptance

1. `curl http://host-a.example.com:18100/v1/models` lists `ui-tars-1.5-7b`.
2. A bare vision+text chat request (OpenAI-compatible wire; see
   `pkg/agent/uitars/uitars.go` `chatRequest` struct) returns a
   JSON action that passes `action.Validate()`.
3. `PH3-UITARS-INT-001` passes when the HelixQA orchestrator
   points `Client.Endpoint` at `http://host-a.example.com:18100`.

## Fallback models

Any llama.cpp-servable VLM speaking the OpenAI vision contract
works — UI-TARS is the default because it's trained specifically
for GUI-agent JSON action output. Alternatives:

- **MiniCPM-V 2.6** — ~8 GB, broader vision abilities, less
  JSON-strict.
- **InternVL2-8B** — similar footprint, stronger OCR.

Swap by changing the `-m` path + `uitars.Client.Model` on the Go
side.
