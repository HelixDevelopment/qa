# `helixqa-dreamsim`

Triton Inference Server model config for DreamSim. Consumed by `pkg/vision/perceptual/dreamsim` on the Go side.

**Status:** operator-action. Go client code-complete + 95% tested (M34).

## Triton model config

Place the DreamSim ONNX export at `/models/dreamsim/1/model.onnx` and the config below at `/models/dreamsim/config.pbtxt`:

```
name: "dreamsim"
platform: "onnxruntime_onnx"
max_batch_size: 1

input [
  {
    name: "IMAGE_A"
    data_type: TYPE_STRING
    dims: [ 1 ]
  },
  {
    name: "IMAGE_B"
    data_type: TYPE_STRING
    dims: [ 1 ]
  }
]

output [
  {
    name: "SIMILARITY"
    data_type: TYPE_FP32
    dims: [ 1 ]
  }
]

instance_group [
  { kind: KIND_GPU, count: 1 }
]
```

## Python backend alternative

DreamSim's native PyTorch interface is easier to wrap via Triton's Python backend than the raw ONNX ops. `config.pbtxt` stays identical; `model.py` contains:

```python
import triton_python_backend_utils as pb_utils
import base64, io, numpy as np, torch
from PIL import Image
from dreamsim import dreamsim  # pip install dreamsim

class TritonPythonModel:
    def initialize(self, args):
        self.model, self.preprocess = dreamsim(pretrained=True, device="cuda")

    def execute(self, requests):
        responses = []
        for req in requests:
            a = pb_utils.get_input_tensor_by_name(req, "IMAGE_A").as_numpy()
            b = pb_utils.get_input_tensor_by_name(req, "IMAGE_B").as_numpy()
            img_a = self._decode(a[0][0])
            img_b = self._decode(b[0][0])
            with torch.no_grad():
                d = self.model(img_a, img_b).item()
            # DreamSim returns distance; HelixQA Go client expects
            # similarity in [0, 1] (and re-maps to [-1, 1]).
            sim = max(0.0, min(1.0, 1.0 - d))
            out = pb_utils.Tensor("SIMILARITY", np.array([sim], dtype=np.float32))
            responses.append(pb_utils.InferenceResponse(output_tensors=[out]))
        return responses

    def _decode(self, b64_bytes):
        png = base64.b64decode(b64_bytes)
        img = Image.open(io.BytesIO(png)).convert("RGB")
        return self.preprocess(img).unsqueeze(0).cuda()
```

## Triton launch

```bash
podman run --rm -d \
  --name triton \
  --gpus all \
  --network host \
  --cpus=4 --memory=12g \
  -v ./models:/models \
  docker.io/nvcr.io/nvidia/tritonserver:24.10-py3 \
  tritonserver --model-repository=/models
```

## Acceptance

1. `GET http://host-a.example.com:8000/v2/models/dreamsim` returns `{"name":"dreamsim","ready":true}`.
2. Identical images → similarity ≥ 0.95 (client maps to canonical ≥ 0.9).
3. `PH2-DREAMSIM-INT-001` passes.
