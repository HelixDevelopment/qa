// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package llmctl

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Errors returned by VisionClient.
var (
	// ErrNotRunning: nothing accepts connections at the vision URL.
	ErrNotRunning = errors.New("llmctl: vision server not running")
	// ErrTimeout: the request exceeded its deadline.
	ErrTimeout = errors.New("llmctl: vision request timed out")
	// ErrEmptyAnswer: the server answered with no text.
	ErrEmptyAnswer = errors.New("llmctl: vision server returned an empty answer")
	// ErrModelMismatch: the served model is not the pinned model id.
	ErrModelMismatch = errors.New("llmctl: served model does not match the pinned model id")
	// ErrEmptyImage: an image-in call was made without image bytes.
	ErrEmptyImage = errors.New("llmctl: empty image")
)

// HTTPError is a non-2xx answer from the vision server. The body is
// truncated; it never contains request data.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("llmctl: vision server HTTP %d: %s", e.Status, e.Body)
}

// VisionConfig configures a VisionClient. All fields are explicit so
// that a run is reproducible: the defaults pin temperature 0 and a
// fixed seed.
type VisionConfig struct {
	// BaseURL of the llama-server, default http://127.0.0.1:8082.
	BaseURL string
	// Model is sent as the "model" field. llama-server accepts any
	// string and serves the loaded model, default "local".
	Model string
	// ExpectModel, when non-empty, is the PINNED model id: Vision and
	// Health fail with ErrModelMismatch unless /v1/models lists it.
	ExpectModel string
	// Temperature is always sent; default 0.
	Temperature float64
	// Seed is always sent; default 42.
	Seed int
	// MaxTokens bounds the answer, default 256.
	MaxTokens int
	// Timeout per request, default 120s (CPU-placed prefill is slow).
	Timeout time.Duration
	// HTTPClient overrides the transport (tests); Timeout still applies
	// through the request context.
	HTTPClient *http.Client
}

// DefaultVisionConfig returns the deterministic defaults.
func DefaultVisionConfig() VisionConfig {
	return VisionConfig{
		BaseURL:     fmt.Sprintf("http://127.0.0.1:%d", DefaultVisionPort),
		Model:       "local",
		Temperature: 0,
		Seed:        42,
		MaxTokens:   256,
		Timeout:     120 * time.Second,
	}
}

// VisionClient talks to an llmctl vision profile (llama-server,
// OpenAI-compatible API).
type VisionClient struct {
	cfg VisionConfig
	hc  *http.Client
}

// NewVisionClient builds a client, filling zero fields from
// DefaultVisionConfig. Temperature 0 and Seed are kept as given
// when BaseURL was set explicitly, so a deliberate Seed of 0 works.
func NewVisionClient(cfg VisionConfig) *VisionClient {
	def := DefaultVisionConfig()
	if cfg.BaseURL == "" {
		cfg.BaseURL = def.BaseURL
		if cfg.Seed == 0 {
			cfg.Seed = def.Seed
		}
	}
	if cfg.Model == "" {
		cfg.Model = def.Model
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = def.MaxTokens
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = def.Timeout
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{}
	}
	return &VisionClient{cfg: cfg, hc: hc}
}

// Answer is the parsed reply of one vision call.
type Answer struct {
	Content      string
	Model        string
	InputTokens  int
	OutputTokens int
}

// Health probes GET /health. READY only on HTTP 200 (and the pinned
// model, when configured, being served).
func (c *VisionClient) Health(ctx context.Context) (State, string) {
	body, status, err := c.get(ctx, "/health")
	if err != nil {
		if errors.Is(err, ErrNotRunning) {
			return StateNotRunning, "connection refused at " + c.cfg.BaseURL
		}
		return StateUnhealthy, err.Error()
	}
	if status != http.StatusOK {
		return StateUnhealthy, fmt.Sprintf("GET /health -> HTTP %d: %s", status, truncate(strings.TrimSpace(string(body)), 120))
	}
	if c.cfg.ExpectModel != "" {
		if err := c.checkModel(ctx); err != nil {
			return StateUnhealthy, err.Error()
		}
	}
	return StateReady, "GET /health -> 200"
}

// Models returns the ids listed by GET /v1/models.
func (c *VisionClient) Models(ctx context.Context) ([]string, error) {
	body, status, err := c.get(ctx, "/v1/models")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, &HTTPError{Status: status, Body: truncate(string(body), 200)}
	}
	var v struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("llmctl: parse /v1/models: %w", err)
	}
	ids := make([]string, 0, len(v.Data))
	for _, d := range v.Data {
		ids = append(ids, d.ID)
	}
	return ids, nil
}

func (c *VisionClient) checkModel(ctx context.Context) error {
	ids, err := c.Models(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if id == c.cfg.ExpectModel {
			return nil
		}
	}
	return fmt.Errorf("%w: want %q, served %v", ErrModelMismatch, c.cfg.ExpectModel, ids)
}

// Vision sends one image + prompt and returns the model's text. The
// answer is a PROPOSAL: callers must confirm it with a deterministic
// oracle (see Confirm) before treating it as a verdict.
func (c *VisionClient) Vision(ctx context.Context, image []byte, prompt string) (*Answer, error) {
	if len(image) == 0 {
		return nil, ErrEmptyImage
	}
	mime := http.DetectContentType(image)
	if !strings.HasPrefix(mime, "image/") {
		return nil, fmt.Errorf("llmctl: payload is not an image (detected %s)", mime)
	}
	reqBody := map[string]any{
		"model":       c.cfg.Model,
		"temperature": c.cfg.Temperature,
		"seed":        c.cfg.Seed,
		"max_tokens":  c.cfg.MaxTokens,
		"stream":      false,
		"messages": []any{map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "text", "text": prompt},
				map[string]any{"type": "image_url", "image_url": map[string]any{
					"url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(image),
				}},
			},
		}},
	}
	raw, _ := json.Marshal(reqBody)
	return c.complete(ctx, raw)
}

func (c *VisionClient) complete(ctx context.Context, raw []byte) (*Answer, error) {
	if c.cfg.ExpectModel != "" {
		if err := c.checkModel(ctx); err != nil {
			return nil, err
		}
	}
	body, status, err := c.do(ctx, http.MethodPost, "/v1/chat/completions", raw)
	if err != nil {
		return nil, err
	}
	if status < 200 || status > 299 {
		return nil, &HTTPError{Status: status, Body: truncate(strings.TrimSpace(string(body)), 200)}
	}
	var resp struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			Prompt     int `json:"prompt_tokens"`
			Completion int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("llmctl: parse completion: %w", err)
	}
	if len(resp.Choices) == 0 || strings.TrimSpace(resp.Choices[0].Message.Content) == "" {
		return nil, ErrEmptyAnswer
	}
	return &Answer{
		Content:      strings.TrimSpace(resp.Choices[0].Message.Content),
		Model:        resp.Model,
		InputTokens:  resp.Usage.Prompt,
		OutputTokens: resp.Usage.Completion,
	}, nil
}

// ChatMessage is one text turn for Chat.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Chat sends a text-only conversation to the same llama-server with
// the same deterministic parameters.
func (c *VisionClient) Chat(ctx context.Context, msgs []ChatMessage) (*Answer, error) {
	if len(msgs) == 0 {
		return nil, errors.New("llmctl: no messages")
	}
	raw, _ := json.Marshal(map[string]any{
		"model": c.cfg.Model, "temperature": c.cfg.Temperature, "seed": c.cfg.Seed,
		"max_tokens": c.cfg.MaxTokens, "stream": false, "messages": msgs,
	})
	return c.complete(ctx, raw)
}

func (c *VisionClient) get(ctx context.Context, path string) ([]byte, int, error) {
	return c.do(ctx, http.MethodGet, path, nil)
}

func (c *VisionClient) do(ctx context.Context, method, path string, body []byte) ([]byte, int, error) {
	cctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(cctx, method, c.cfg.BaseURL+path, rd)
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, 0, classifyNetErr(cctx, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, classifyNetErr(cctx, err)
	}
	return b, resp.StatusCode, nil
}

func classifyNetErr(ctx context.Context, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrTimeout
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return ErrTimeout
	}
	var oe *net.OpError
	if errors.As(err, &oe) && oe.Op == "dial" {
		return fmt.Errorf("%w: %v", ErrNotRunning, err)
	}
	return err
}

// ResolveOptions parameterises ResolveVision.
type ResolveOptions struct {
	Detect DetectOptions
	Vision VisionConfig
}

// Status is the full truthful picture of the vision path.
type Status struct {
	State     State
	Detail    string
	Detection Detection
	BaseURL   string
}

// ResolveVision answers "can HelixQA use llmctl vision right now?"
// without starting anything. A running, healthy server wins over the
// plan (a gate only matters when nothing is serving).
func ResolveVision(ctx context.Context, opts ResolveOptions) Status {
	c := NewVisionClient(opts.Vision)
	st := Status{BaseURL: c.cfg.BaseURL}

	det, derr := Detect(opts.Detect)
	st.Detection = det

	state, detail := c.Health(ctx)
	if state == StateReady {
		st.State, st.Detail = StateReady, detail
		return st
	}
	if derr != nil {
		st.State = StateAbsent
		st.Detail = derr.Error() + "; setup: clone https://github.com/vasic-digital/llmctl and run `llmctl setup`, or set " + EnvBin
		return st
	}
	plan, perr := ReadPlan(ctx, det.Bin)
	if perr != nil {
		st.State, st.Detail = StateUnknown, "cannot read llmctl plan: "+perr.Error()
		return st
	}
	if gs, why := plan.Classify(VisionProfile); gs != "" {
		st.State, st.Detail = gs, why
		return st
	}
	st.State, st.Detail = state, detail
	return st
}
