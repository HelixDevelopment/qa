// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// Package uitars is the HelixQA client for UI-TARS-1.5-7B — the
// GUI-grounding VLM from ByteDance (ByteDance Seed, 2025). UI-TARS is
// served by llama.cpp's `llama-server` over an OpenAI-compatible
// /v1/chat/completions endpoint, extended with vision-content parts.
//
// The typical Phase-3 deployment runs llama-server on the GPU host
// (host-a.example.com:18100) with --mmproj pointing at the mmproj gguf
// companion file. See docs/OPEN_POINTS_CLOSURE.md §10.3.
//
// Phase-3 foundation: this is the first agent-brain module. It takes
// a screenshot + instruction, returns one or more agent.Action
// records that the executor layer (pkg/navigator, pkg/bridge/scrcpy,
// pkg/nexus/observe/axtree) carries out.
package uitars

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"time"

	"digital.vasic.helixqa/pkg/agent/action"
)

// Client is a UI-TARS VLM client against a llama-server OpenAI-compat
// endpoint. Zero-value fields fall back to sensible defaults.
type Client struct {
	// Endpoint is the base URL of the llama-server, e.g.
	// "http://host-a.example.com:18100". Required.
	Endpoint string

	// Model is the model identifier configured in llama-server.
	// Default: "ui-tars-1.5-7b".
	Model string

	// SystemPrompt is prepended to every /chat/completions request.
	// Zero → sensible UI-agent default.
	SystemPrompt string

	// HTTPClient is the underlying transport; default 30-second
	// timeout. UI-TARS generation is latency-bound (multi-second
	// prefill + decode on commodity GPUs), so no tight timeout.
	HTTPClient *http.Client

	// MaxTokens caps the response length. Default 256 — plenty for
	// a JSON action + reason.
	MaxTokens int

	// Temperature is the sampling temperature. Default 0.0 —
	// deterministic generation is the right default for QA.
	Temperature float64
}

// New returns a Client bound to the given endpoint, with defaults
// matching the OpenClawing4 Phase-3 deployment spec.
func New(endpoint string) *Client {
	return &Client{
		Endpoint: endpoint,
	}
}

// Sentinel errors.
var (
	ErrEmptyEndpoint    = errors.New("helixqa/agent/uitars: Endpoint not set")
	ErrEmptyInstruction = errors.New("helixqa/agent/uitars: empty instruction")
	ErrNilImage         = errors.New("helixqa/agent/uitars: nil screenshot")
	ErrNoChoices        = errors.New("helixqa/agent/uitars: response had no choices")
	ErrNoActionJSON     = errors.New("helixqa/agent/uitars: could not extract action JSON from model output")
)

// ---------------------------------------------------------------------------
// OpenAI-compatible wire structs.
// ---------------------------------------------------------------------------

type chatRequest struct {
	Model       string         `json:"model"`
	Messages    []chatMessage  `json:"messages"`
	MaxTokens   int            `json:"max_tokens,omitempty"`
	Temperature float64        `json:"temperature"`
	Stream      bool           `json:"stream"`
}

type chatMessage struct {
	Role    string            `json:"role"`
	Content []chatContentPart `json:"content"`
}

type chatContentPart struct {
	Type     string        `json:"type"` // "text" or "image_url"
	Text     string        `json:"text,omitempty"`
	ImageURL *chatImageURL `json:"image_url,omitempty"`
}

type chatImageURL struct {
	URL string `json:"url"`
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`
}

type chatChoice struct {
	Message chatResponseMessage `json:"message"`
}

type chatResponseMessage struct {
	Content string `json:"content"`
}

// ---------------------------------------------------------------------------
// Act — the main Phase-3 entry point.
// ---------------------------------------------------------------------------

// Act sends one screenshot + instruction to UI-TARS and returns the
// parsed agent.Action. Callers that need a full chat history (multi-
// turn agent trajectories) should use ActChat instead.
func (c *Client) Act(ctx context.Context, screenshot image.Image, instruction string) (action.Action, error) {
	return c.ActChat(ctx, screenshot, instruction, nil)
}

// ActChat sends a screenshot + instruction + optional history and
// returns the next agent.Action. history is a list of previous
// (assistant-message-content) strings — supplying it lets UI-TARS
// reason about its own prior actions without the executor having to
// translate back and forth.
func (c *Client) ActChat(ctx context.Context, screenshot image.Image, instruction string, history []string) (action.Action, error) {
	if c.Endpoint == "" {
		return action.Action{}, ErrEmptyEndpoint
	}
	if screenshot == nil {
		return action.Action{}, ErrNilImage
	}
	if strings.TrimSpace(instruction) == "" {
		return action.Action{}, ErrEmptyInstruction
	}

	dataURL, err := pngDataURL(screenshot)
	if err != nil {
		return action.Action{}, fmt.Errorf("uitars: encode screenshot: %w", err)
	}

	model := c.Model
	if model == "" {
		model = "ui-tars-1.5-7b"
	}
	maxTokens := c.MaxTokens
	if maxTokens == 0 {
		maxTokens = 256
	}
	system := c.SystemPrompt
	if system == "" {
		system = defaultSystemPrompt
	}

	messages := []chatMessage{
		{Role: "system", Content: []chatContentPart{{Type: "text", Text: system}}},
	}
	for _, h := range history {
		messages = append(messages, chatMessage{
			Role:    "assistant",
			Content: []chatContentPart{{Type: "text", Text: h}},
		})
	}
	messages = append(messages, chatMessage{
		Role: "user",
		Content: []chatContentPart{
			{Type: "image_url", ImageURL: &chatImageURL{URL: dataURL}},
			{Type: "text", Text: instruction},
		},
	})

	body, err := json.Marshal(chatRequest{
		Model:       model,
		Messages:    messages,
		MaxTokens:   maxTokens,
		Temperature: c.Temperature,
	})
	if err != nil {
		return action.Action{}, fmt.Errorf("uitars: marshal request: %w", err)
	}

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return action.Action{}, fmt.Errorf("uitars: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return action.Action{}, fmt.Errorf("uitars: call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return action.Action{}, fmt.Errorf("uitars: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var out chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return action.Action{}, fmt.Errorf("uitars: decode: %w", err)
	}
	if len(out.Choices) == 0 {
		return action.Action{}, ErrNoChoices
	}

	return ExtractAction(out.Choices[0].Message.Content)
}

// defaultSystemPrompt is the UI-TARS prompt template HelixQA uses by
// default. Keep it aligned with the agent.Action vocabulary so the
// model always emits parseable JSON.
const defaultSystemPrompt = `You are UI-TARS, a GUI agent. Analyze the screenshot and emit exactly one JSON action.

Valid JSON action shapes (return EXACTLY one of these, no extra prose around the JSON):
  {"kind":"click","x":<px>,"y":<px>,"reason":"..."}
  {"kind":"type","text":"<text>","reason":"..."}
  {"kind":"scroll","dx":<px>,"dy":<px>,"reason":"..."}
  {"kind":"wait","duration_ms":<ms>,"reason":"..."}
  {"kind":"key","key":"<NAME>","reason":"..."}
  {"kind":"swipe","x":<px>,"y":<px>,"x2":<px>,"y2":<px>,"duration_ms":<ms>,"reason":"..."}
  {"kind":"open_app","target":"<package-or-url>","reason":"..."}
  {"kind":"done","reason":"<what was accomplished>"}`

// ExtractAction pulls the first JSON object out of model output and
// parses it as an agent.Action. Tolerates surrounding prose — VLMs
// often emit "Thought: ... Action: {json}" style outputs.
//
// Exported so tests and future agent modules can reuse the parser
// without going through the full HTTP path.
func ExtractAction(content string) (action.Action, error) {
	start := strings.Index(content, "{")
	if start < 0 {
		return action.Action{}, fmt.Errorf("%w: no '{' in %q", ErrNoActionJSON, truncate(content, 200))
	}
	// Brace-count to find the matching close for the first '{'. This
	// tolerates JSON that contains nested objects (unlikely for a
	// single action, but cheap safety).
	depth := 0
	end := -1
	for i := start; i < len(content); i++ {
		switch content[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i + 1
				break
			}
		}
		if end > 0 {
			break
		}
	}
	if end < 0 {
		return action.Action{}, fmt.Errorf("%w: unbalanced braces in %q", ErrNoActionJSON, truncate(content, 200))
	}

	return action.ParseJSON([]byte(content[start:end]))
}

// pngDataURL encodes img as a base64 PNG data URL — the canonical
// OpenAI image_url content-part payload shape.
func pngDataURL(img image.Image) (string, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
