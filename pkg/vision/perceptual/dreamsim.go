// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package perceptual

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"time"

	"digital.vasic.helixqa/pkg/gpu/infer"
)

// DreamSim is the tier-3 perceptual Comparator — a REST client
// against a Triton-hosted DreamSim (Sundar 2023) model. DreamSim
// is trained on human perceptual-similarity judgments and
// achieves ~96% agreement with human raters on structural-
// similarity tasks; see https://dreamsim-nights.github.io/.
//
// Since M60, DreamSim delegates its HTTP wire code to pkg/gpu/
// infer (the generic Triton KServe v2 client). This removes
// ~140 LoC of duplicated request/response handling that was
// otherwise copy-pasted between DreamSim and LPIPS; the two
// clients now differ only in their input/output naming
// conventions and distance→similarity mapping.
type DreamSim struct {
	// Endpoint is the Triton server URL, e.g.
	// "http://host-a.example.com:8000". Required.
	Endpoint string

	// Model is the Triton model name. Default: "dreamsim".
	Model string

	// HTTPClient is the transport. Default 200ms timeout (matches
	// the Phase-2 tier-3 budget in OpenClawing4.md §5.8).
	HTTPClient *http.Client
}

// NewDreamSim returns a tier-3 Comparator with the given endpoint.
func NewDreamSim(endpoint string) *DreamSim {
	return &DreamSim{
		Endpoint: endpoint,
		Model:    "dreamsim",
		HTTPClient: &http.Client{
			Timeout: 200 * time.Millisecond,
		},
	}
}

// Sentinel errors — preserved from the pre-M60 shape so existing
// errors.Is checks on DreamSim.Compare continue to work.
var (
	ErrDreamSimEndpoint = errors.New("helixqa/perceptual/dreamsim: Endpoint not set")
	ErrDreamSimResponse = errors.New("helixqa/perceptual/dreamsim: unexpected Triton response shape")
)

// Compare encodes a and b as PNGs and sends them through
// pkg/gpu/infer to the configured Triton endpoint, returning the
// DreamSim similarity mapped to [-1, 1].
func (d *DreamSim) Compare(ctx context.Context, a, b image.Image) (float64, error) {
	if a == nil || b == nil {
		return 0, ErrNilImage
	}
	if d.Endpoint == "" {
		return 0, ErrDreamSimEndpoint
	}

	model := d.Model
	if model == "" {
		model = "dreamsim"
	}

	encA, err := encodePNGBase64(a)
	if err != nil {
		return 0, fmt.Errorf("dreamsim: encode a: %w", err)
	}
	encB, err := encodePNGBase64(b)
	if err != nil {
		return 0, fmt.Errorf("dreamsim: encode b: %w", err)
	}

	client := &infer.Client{
		Endpoint:   d.Endpoint,
		HTTPClient: d.HTTPClient,
	}
	resp, err := client.Infer(ctx, infer.Request{
		Model: model,
		Inputs: []infer.Input{
			{Name: "IMAGE_A", Datatype: "BYTES", Shape: []int{1}, StringData: []string{encA}},
			{Name: "IMAGE_B", Datatype: "BYTES", Shape: []int{1}, StringData: []string{encB}},
		},
	})
	if err != nil {
		return 0, fmt.Errorf("dreamsim: %w", err)
	}

	similarity, err := dreamSimScalar(resp)
	if err != nil {
		return 0, err
	}
	// DreamSim raw output is in [0, 1]; canonical Comparator is
	// [-1, 1]. 1 → 1, 0.5 → 0, 0 → -1.
	return 2*similarity - 1, nil
}

// dreamSimScalar extracts a scalar similarity from the response.
// Tolerates three naming conventions the sidecar might use:
// SIMILARITY (canonical), similarity (lowercase), output (generic).
// Falls back to the first output with non-empty Float32Data /
// Float64Data.
func dreamSimScalar(r infer.Response) (float64, error) {
	for _, name := range []string{"SIMILARITY", "similarity", "output"} {
		for _, o := range r.Outputs {
			if o.Name != name {
				continue
			}
			if v, ok := firstFloat(o); ok {
				return v, nil
			}
			return 0, fmt.Errorf("%w: %q present but empty", ErrDreamSimResponse, name)
		}
	}
	// Fallback: first output with numeric data.
	for _, o := range r.Outputs {
		if v, ok := firstFloat(o); ok {
			return v, nil
		}
	}
	return 0, fmt.Errorf("%w: no SIMILARITY output found", ErrDreamSimResponse)
}

// firstFloat returns the first numeric value from whichever
// *Data slice the Output carries. Shared with LPIPS.
func firstFloat(o infer.Output) (float64, bool) {
	switch {
	case len(o.Float64Data) > 0:
		return o.Float64Data[0], true
	case len(o.Float32Data) > 0:
		return float64(o.Float32Data[0]), true
	case len(o.Int32Data) > 0:
		return float64(o.Int32Data[0]), true
	case len(o.Int64Data) > 0:
		return float64(o.Int64Data[0]), true
	case len(o.Uint8Data) > 0:
		return float64(o.Uint8Data[0]), true
	}
	return 0, false
}

// encodePNGBase64 encodes img as PNG and returns the base64-
// encoded bytes. Shared between DreamSim and LPIPS; Triton's BYTES
// datatype wraps arbitrary blobs in base64.
func encodePNGBase64(img image.Image) (string, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// Compile-time guard.
var _ Comparator = (*DreamSim)(nil)
