// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// Package infer provides a reusable Triton Inference Server client
// speaking the KServe v2 protocol (v2/models/{model}/infer). HelixQA
// Phase-2 ships pkg/vision/perceptual/dreamsim and pkg/vision/
// perceptual/lpips as model-specific clients; every future GPU
// sidecar (qa-vision-infer, qa-video-decode, qa-vulkan-compute,
// UI-TARS-via-Triton, OmniParser-via-Triton) speaks the same wire.
//
// This package factors the common wire code into one reusable
// Client that:
//
//   - Builds KServe v2 /infer request bodies with arbitrary input
//     tensors (BYTES / FP32 / INT32 / UINT8).
//   - Parses the KServe v2 response back into typed output tensors.
//   - Exposes a simple ergonomic Infer(ctx, request) method.
//
// Downstream clients (DreamSim, LPIPS, future sidecars) become
// thin wrappers around this — ~30 LoC each instead of the 200 LoC
// the current standalone DreamSim/LPIPS clients carry.
//
// See OpenClawing4.md §5.10 (GPU compute tier).
package infer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is the Triton KServe v2 inference client.
type Client struct {
	// Endpoint is the base URL of the Triton server, e.g.
	// "http://host-a.example.com:8000". Required.
	Endpoint string

	// HTTPClient is the underlying transport; default is a
	// 200-millisecond client matching the Phase-2 tier-3 budget.
	HTTPClient *http.Client

	// InferPath overrides the canonical /v2/models/{model}/infer
	// path. Zero → the canonical form. Rarely overridden — Triton's
	// path is stable across versions.
	InferPath string
}

// New returns a Client bound to the given endpoint.
func New(endpoint string) *Client {
	return &Client{Endpoint: endpoint}
}

// Sentinel errors.
var (
	ErrEmptyEndpoint = errors.New("helixqa/gpu/infer: Endpoint not set")
	ErrEmptyModel    = errors.New("helixqa/gpu/infer: model must be non-empty")
	ErrNoInputs      = errors.New("helixqa/gpu/infer: request has no inputs")
	ErrOutputMissing = errors.New("helixqa/gpu/infer: named output not present in response")
	ErrWrongDatatype = errors.New("helixqa/gpu/infer: output datatype mismatch")
)

// ---------------------------------------------------------------------------
// Request / response shapes
// ---------------------------------------------------------------------------

// Request is a KServe v2 inference request. Model selects which
// Triton-hosted model to call; Inputs are the named input tensors.
type Request struct {
	Model  string
	Inputs []Input
}

// Input is a single named input tensor. Use StringData for BYTES
// inputs (base64-encoded blobs, text, etc.), Float32Data for FP32,
// Int32Data for INT32, etc. Exactly one *Data field must be set.
type Input struct {
	Name      string
	Datatype  string // "BYTES", "FP32", "INT32", "UINT8", ...
	Shape     []int
	Parameters map[string]any

	StringData  []string
	Float32Data []float32
	Float64Data []float64
	Int32Data   []int32
	Int64Data   []int64
	Uint8Data   []uint8
	BoolData    []bool
}

// Response is the parsed KServe v2 inference response.
type Response struct {
	ModelName    string
	ModelVersion string
	Outputs      []Output
}

// Output is a single named output tensor from the response. Only
// one *Data field is populated — the one matching Datatype.
type Output struct {
	Name     string
	Datatype string
	Shape    []int

	StringData  []string
	Float32Data []float32
	Float64Data []float64
	Int32Data   []int32
	Int64Data   []int64
	Uint8Data   []uint8
	BoolData    []bool
}

// Find returns the named output by name. Returns ErrOutputMissing
// when no output matches.
func (r Response) Find(name string) (Output, error) {
	for _, o := range r.Outputs {
		if o.Name == name {
			return o, nil
		}
	}
	return Output{}, fmt.Errorf("%w: %q", ErrOutputMissing, name)
}

// FloatScalar returns the first Float32/Float64 value of the named
// output, or the best-effort float from the other *Data slices. Used
// by downstream callers that know the output is a scalar score.
func (r Response) FloatScalar(name string) (float64, error) {
	o, err := r.Find(name)
	if err != nil {
		return 0, err
	}
	switch {
	case len(o.Float64Data) > 0:
		return o.Float64Data[0], nil
	case len(o.Float32Data) > 0:
		return float64(o.Float32Data[0]), nil
	case len(o.Int32Data) > 0:
		return float64(o.Int32Data[0]), nil
	case len(o.Int64Data) > 0:
		return float64(o.Int64Data[0]), nil
	case len(o.Uint8Data) > 0:
		return float64(o.Uint8Data[0]), nil
	}
	return 0, fmt.Errorf("%w: %q is empty or non-numeric", ErrWrongDatatype, name)
}

// ---------------------------------------------------------------------------
// Infer — the main entry point
// ---------------------------------------------------------------------------

// Infer sends req to the configured Triton endpoint and returns the
// decoded Response. Respects ctx cancellation.
func (c *Client) Infer(ctx context.Context, req Request) (Response, error) {
	if c.Endpoint == "" {
		return Response{}, ErrEmptyEndpoint
	}
	if req.Model == "" {
		return Response{}, ErrEmptyModel
	}
	if len(req.Inputs) == 0 {
		return Response{}, ErrNoInputs
	}

	body, err := json.Marshal(toWireRequest(req))
	if err != nil {
		return Response{}, fmt.Errorf("infer: marshal: %w", err)
	}

	path := c.InferPath
	if path == "" {
		path = fmt.Sprintf("/v2/models/%s/infer", req.Model)
	}
	url := c.Endpoint + path

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("infer: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 200 * time.Millisecond}
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("infer: call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return Response{}, fmt.Errorf("infer: HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var wire wireResponse
	if err := json.NewDecoder(resp.Body).Decode(&wire); err != nil {
		return Response{}, fmt.Errorf("infer: decode: %w", err)
	}
	return fromWireResponse(wire), nil
}

// ---------------------------------------------------------------------------
// Wire format translation
// ---------------------------------------------------------------------------

type wireRequest struct {
	Inputs []wireInput `json:"inputs"`
}

type wireInput struct {
	Name       string         `json:"name"`
	Datatype   string         `json:"datatype"`
	Shape      []int          `json:"shape"`
	Parameters map[string]any `json:"parameters,omitempty"`
	Data       any            `json:"data"`
}

type wireResponse struct {
	ModelName    string       `json:"model_name"`
	ModelVersion string       `json:"model_version"`
	Outputs      []wireOutput `json:"outputs"`
}

type wireOutput struct {
	Name     string          `json:"name"`
	Datatype string          `json:"datatype"`
	Shape    []int           `json:"shape"`
	Data     json.RawMessage `json:"data"`
}

func toWireRequest(req Request) wireRequest {
	out := wireRequest{Inputs: make([]wireInput, 0, len(req.Inputs))}
	for _, in := range req.Inputs {
		w := wireInput{
			Name:       in.Name,
			Datatype:   in.Datatype,
			Shape:      in.Shape,
			Parameters: in.Parameters,
		}
		switch {
		case len(in.StringData) > 0:
			w.Data = in.StringData
		case len(in.Float32Data) > 0:
			w.Data = in.Float32Data
		case len(in.Float64Data) > 0:
			w.Data = in.Float64Data
		case len(in.Int32Data) > 0:
			w.Data = in.Int32Data
		case len(in.Int64Data) > 0:
			w.Data = in.Int64Data
		case len(in.Uint8Data) > 0:
			w.Data = in.Uint8Data
		case len(in.BoolData) > 0:
			w.Data = in.BoolData
		default:
			w.Data = []any{}
		}
		out.Inputs = append(out.Inputs, w)
	}
	return out
}

func fromWireResponse(wire wireResponse) Response {
	out := Response{
		ModelName:    wire.ModelName,
		ModelVersion: wire.ModelVersion,
		Outputs:      make([]Output, 0, len(wire.Outputs)),
	}
	for _, wo := range wire.Outputs {
		o := Output{
			Name:     wo.Name,
			Datatype: wo.Datatype,
			Shape:    wo.Shape,
		}
		switch wo.Datatype {
		case "FP32":
			_ = json.Unmarshal(wo.Data, &o.Float32Data)
		case "FP64":
			_ = json.Unmarshal(wo.Data, &o.Float64Data)
		case "INT32":
			_ = json.Unmarshal(wo.Data, &o.Int32Data)
		case "INT64":
			_ = json.Unmarshal(wo.Data, &o.Int64Data)
		case "UINT8":
			_ = json.Unmarshal(wo.Data, &o.Uint8Data)
		case "BOOL":
			_ = json.Unmarshal(wo.Data, &o.BoolData)
		case "BYTES":
			_ = json.Unmarshal(wo.Data, &o.StringData)
		default:
			// Unknown datatype — best effort: try Float64Data first,
			// fall back to StringData. Lets new Triton datatypes land
			// without a code change here.
			if err := json.Unmarshal(wo.Data, &o.Float64Data); err != nil {
				_ = json.Unmarshal(wo.Data, &o.StringData)
			}
		}
		out.Outputs = append(out.Outputs, o)
	}
	return out
}
