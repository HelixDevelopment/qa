# `helixqa-omniparser`

Python sidecar exposing Microsoft OmniParser v2 over HTTP. Consumed by `pkg/agent/omniparser` on the Go side.

**Status:** operator-action item. The Go client is code-complete + 100% tested (M37); this sidecar is the deliverable an operator needs to build + deploy before `PH3-OMNIPARSER-INT-001` can run.

## Wire contract

`pkg/agent/omniparser/omniparser.go` speaks this wire; any sidecar that satisfies the shape below is compatible.

### Request

```
POST /parse
Content-Type: multipart/form-data; boundary=...
    image = <PNG bytes>  (form field "image")
```

### Response (200 OK, application/json)

```json
{
  "width":  1920,
  "height": 1080,
  "elements": [
    {
      "bbox":        [100, 200, 300, 260],
      "type":        "button",
      "text":        "Sign in",
      "content":     "Sign in",
      "interactive": true,
      "confidence":  0.94
    },
    ...
  ]
}
```

- `bbox` is `[x1, y1, x2, y2]` in screen pixel coordinates.
- `type` may be any string — the Go client preserves it verbatim for downstream consumers.
- `interactive=true` elements are the candidate click targets; the Grounder (`pkg/agent/ground`) prefers smallest-enclosing interactive elements for coord snapping.
- `confidence` is used by the Grounder to filter phantom detections (default floor 0.5).

### Error responses

- HTTP 4xx/5xx propagates to the Go client as `omniparser: HTTP <code>: <body>`.
- Malformed JSON → Go client returns a decode error.
- `bbox` with len() != 4 → Go client returns `ErrInvalidBBox`.

## Deployment target

Per `OpenClawing4-Phase2-Kickoff.md §8`, OmniParser v2 runs on the GPU host (host-a.example.com).

## Build recipe (reference)

Below is a reference Dockerfile that matches the contract. It uses the official OmniParser v2 weights + a thin Flask handler. Operator is free to swap in FastAPI, gRPC+REST bridge, etc., as long as the wire contract is preserved.

```dockerfile
# Reference only — operator-action per §10.3.
FROM docker.io/library/python:3.11-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    libgl1 libglib2.0-0 \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /app
RUN pip install --no-cache-dir \
    torch==2.3.0 torchvision==0.18.0 \
    transformers==4.42.0 \
    pillow flask

# Operator places the OmniParser v2 checkpoint under /models.
COPY weights /models/omniparser-v2

COPY server.py /app/

EXPOSE 7860
CMD ["python", "/app/server.py"]
```

The `server.py` — outline:

```python
from flask import Flask, request, jsonify
from PIL import Image
import io, torch
# from omniparser.v2 import infer  # operator's real import

app = Flask(__name__)

@app.route("/parse", methods=["POST"])
def parse():
    img_bytes = request.files["image"].read()
    img = Image.open(io.BytesIO(img_bytes))
    # elements = infer(img)  # returns list of {bbox, type, text, confidence}
    elements = []  # operator fills this in
    return jsonify({
        "width": img.width,
        "height": img.height,
        "elements": elements,
    })

if __name__ == "__main__":
    app.run(host="0.0.0.0", port=7860, threaded=True)
```

## Container runtime (Podman, per project CLAUDE.md)

```bash
podman run --rm -d \
  --name helixqa-omniparser \
  --network host \
  --gpus all \
  --cpus=2 --memory=8g \
  -v ./weights:/models/omniparser-v2 \
  localhost/helixqa-omniparser:latest
```

## Contract regression tests

On the Go client side, `tests/e2e/agent_stack_test.go` exercises the full wire via an httptest mock. When the live sidecar is deployed, swap the mock for the real URL — the same assertions apply.

## Acceptance

The sidecar is considered deployed when:

1. `POST http://$HOST:7860/parse` with a 1920×1080 PNG returns HTTP 200 with ≥ 1 element.
2. `PH3-OMNIPARSER-INT-001` passes (`helixqa list --banks banks/phase3-gocore.yaml` + manual run).
3. `docs/OPEN_POINTS_CLOSURE.md` §10.3's "OmniParser v2 weights + Python 3.11 env" checkbox is ticked in the same commit.
