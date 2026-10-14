// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	openaiDefaultBaseURL = "https://api.openai.com"
	openaiDefaultModel   = "gpt-4o"
	openaiHTTPTimeout    = 180 * time.Second

	// openaiMaxResponseBytes caps how much of a reply is read; a larger
	// body is refused rather than buffered without limit.
	openaiMaxResponseBytes = 8 << 20
	// openaiErrorBodySnippet caps how much of an error reply is quoted in
	// the returned error (error text reaches logs and reports).
	openaiErrorBodySnippet = 512
	// visionEndpointSeed is the fixed sampling seed sent to the
	// operator-supplied vision endpoint so the same frame gets the same
	// answer (together with temperature 0).
	visionEndpointSeed = 1
)

// openaiProvider implements Provider for the OpenAI API.
type openaiProvider struct {
	apiKey       string
	baseURL      string
	model        string
	providerName string
	client       *http.Client
}

// visionCapableProviders lists OpenAI-compatible provider names
// that actually support multimodal (image) input. Providers not
// in this set will report SupportsVision() = false, preventing
// the adaptive fallback from wasting time sending images to
// text-only APIs.
var visionCapableProviders = map[string]bool{
	ProviderOpenAI:     true,
	ProviderOpenRouter: true,
	"fireworks":        true,
	"together":         true,
	"hyperbolic":       true,
	"githubmodels":     true,
	"nvidia":           true,
	"xai":              true,
	"kimi":             true, // Moonshot AI Kimi K2.5 — native vision, $0.60/1M tokens
	"qwen":             true, // Alibaba Qwen3-VL — ~90% UI grounding accuracy
	"stepfun":          true, // Stepfun Step-GUI — GUI-specialized vision
	"astica":           true, // Astica.AI — specialized vision API (captioning, OCR, object detection)
	// Operator-supplied OpenAI-compatible vision endpoint
	// (HELIX_VISION_ENDPOINT, e.g. a local llama-server).
	ProviderVisionEndpoint: true,
}

// openaiRequest is the JSON body sent to /v1/chat/completions.
type openaiRequest struct {
	// Model is omitted when empty: an OpenAI-compatible local server
	// (llama-server) then answers with the model it serves.
	Model     string         `json:"model,omitempty"`
	MaxTokens int            `json:"max_tokens"`
	Messages  []openaiReqMsg `json:"messages"`
	// Temperature / Seed are sent only to the vision endpoint (fixed,
	// deterministic sampling); other providers keep their defaults.
	Temperature *float64 `json:"temperature,omitempty"`
	Seed        *int     `json:"seed,omitempty"`
}

// openaiReqMsg is a single outbound message whose content is an
// array of typed content parts (text or image_url).
type openaiReqMsg struct {
	Role    string              `json:"role"`
	Content []openaiContentPart `json:"content"`
}

// openaiContentPart is one element of a request message content
// array.
type openaiContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *openaiImageURL `json:"image_url,omitempty"`
}

// openaiImageURL holds the URL (or base64 data URI) for an
// image_url content part.
type openaiImageURL struct {
	URL string `json:"url"`
}

// openaiMsg is a single inbound message as returned by the API.
// The OpenAI response delivers content as a plain string.
type openaiMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// openaiResponse is the JSON body returned by /v1/chat/completions.
type openaiResponse struct {
	Model   string         `json:"model"`
	Choices []openaiChoice `json:"choices"`
	Usage   openaiUsage    `json:"usage"`
}

// openaiChoice is a single completion choice in the response.
type openaiChoice struct {
	Message openaiMsg `json:"message"`
}

// openaiUsage holds token counts from the API response.
type openaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// NewOpenAIProvider constructs a Provider backed by the OpenAI API.
// BaseURL defaults to https://api.openai.com; Model defaults to
// gpt-4o.
func NewOpenAIProvider(cfg ProviderConfig) Provider {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = openaiDefaultBaseURL
	}
	model := cfg.Model
	if model == "" && cfg.Name != ProviderVisionEndpoint {
		// The vision endpoint keeps an empty model: the request then
		// omits "model" and the local server uses the one it serves.
		model = openaiDefaultModel
	}
	name := cfg.Name
	if name == "" {
		name = ProviderOpenAI
	}
	return &openaiProvider{
		apiKey:       cfg.APIKey,
		baseURL:      baseURL,
		model:        model,
		providerName: name,
		client:       &http.Client{Timeout: openaiHTTPTimeout},
	}
}

// Name returns the actual provider identifier (e.g. "deepseek",
// "openrouter") rather than always "openai", so the adaptive
// provider can distinguish between providers for fallback logic.
func (p *openaiProvider) Name() string {
	return p.providerName
}

// SupportsVision reports whether this provider actually handles
// multimodal image inputs. Many OpenAI-compatible providers
// (DeepSeek, Groq, Cerebras, etc.) are text-only and will error
// on image_url content parts.
func (p *openaiProvider) SupportsVision() bool {
	if visionCapableProviders[p.providerName] {
		return true
	}
	// llama.cpp per-slot providers have dynamic names like
	// "llamacpp-androidtv-192.168.0.134:5555". All
	// llamacpp providers support vision.
	if len(p.providerName) > 8 &&
		p.providerName[:8] == "llamacpp" {
		return true
	}
	return false
}

// Chat sends a multi-turn conversation to the OpenAI chat
// completions API and returns the assistant reply.
func (p *openaiProvider) Chat(
	ctx context.Context,
	messages []Message,
) (*Response, error) {
	var msgs []openaiReqMsg
	for _, m := range messages {
		msgs = append(msgs, openaiReqMsg{
			Role: m.Role,
			Content: []openaiContentPart{
				{Type: "text", Text: m.Content},
			},
		})
	}
	req := openaiRequest{
		Model:     p.model,
		MaxTokens: 4096,
		Messages:  msgs,
	}
	return p.doRequest(ctx, req)
}

// Vision sends a screenshot with a text prompt to the OpenAI chat
// completions API using an image_url content part carrying a
// base64 data URI.
func (p *openaiProvider) Vision(
	ctx context.Context,
	image []byte,
	prompt string,
) (*Response, error) {
	encoded := base64.StdEncoding.EncodeToString(image)
	dataURI := "data:image/png;base64," + encoded

	msgs := []openaiReqMsg{
		{
			Role: RoleUser,
			Content: []openaiContentPart{
				{
					Type:     "image_url",
					ImageURL: &openaiImageURL{URL: dataURI},
				},
				{
					Type: "text",
					Text: prompt,
				},
			},
		},
	}

	// For llama.cpp (llamacpp-*) providers, add a system
	// message that enforces JSON-only output. Without this,
	// LLaVA models return natural language descriptions
	// instead of structured JSON actions.
	if len(p.providerName) > 8 &&
		p.providerName[:8] == "llamacpp" {
		sysmsg := openaiReqMsg{
			Role: RoleSystem,
			Content: []openaiContentPart{{
				Type: "text",
				Text: "You are a QA tester robot. " +
					"Respond with ONLY a JSON array. " +
					"No markdown, no explanation. " +
					"Example: [{\"type\":\"dpad_center\"," +
					"\"reason\":\"select\"}]\n" +
					"Output: [{...},{...}]",
			}},
		}
		msgs = append([]openaiReqMsg{sysmsg}, msgs...)
	}

	req := openaiRequest{
		Model:     p.model,
		MaxTokens: 4096,
		Messages:  msgs,
	}
	return p.doRequest(ctx, req)
}

// doRequest serialises req, POSTs to /v1/chat/completions, and
// parses the response into a *Response.
func (p *openaiProvider) doRequest(
	ctx context.Context,
	req openaiRequest,
) (*Response, error) {
	if p.providerName == ProviderVisionEndpoint {
		temp, seed := 0.0, visionEndpointSeed
		req.Temperature, req.Seed = &temp, &seed
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal request: %w", err)
	}

	// Build the endpoint URL. Most providers use /v1/, but
	// some (e.g. Z.AI/zhipu) use /v4/. If the baseURL already
	// ends with a version path, append only /chat/completions.
	endpoint := p.baseURL + "/v1/chat/completions"
	for _, vp := range []string{"/v1", "/v2", "/v3", "/v4", "/v5"} {
		if strings.HasSuffix(p.baseURL, vp) {
			endpoint = p.baseURL + "/chat/completions"
			break
		}
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("openai: create request: %w", err)
	}
	// A keyless self-hosted endpoint gets no Authorization header
	// rather than a malformed "Bearer " one.
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: send request: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(httpResp.Body, openaiMaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("openai: read response body: %w", err)
	}
	if httpResp.StatusCode != http.StatusOK {
		snippet := respBody
		if len(snippet) > openaiErrorBodySnippet {
			snippet = append(append([]byte(nil), snippet[:openaiErrorBodySnippet]...), "…(truncated)"...)
		}
		return nil, fmt.Errorf(
			"openai: API error %d: %s",
			httpResp.StatusCode,
			string(snippet),
		)
	}
	if len(respBody) > openaiMaxResponseBytes {
		return nil, fmt.Errorf("openai: response body exceeds %d bytes — refused", openaiMaxResponseBytes)
	}

	var apiResp openaiResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("openai: decode response: %w", err)
	}

	var content string
	if len(apiResp.Choices) > 0 {
		content = apiResp.Choices[0].Message.Content
	}

	return &Response{
		Content:      content,
		Model:        apiResp.Model,
		InputTokens:  apiResp.Usage.PromptTokens,
		OutputTokens: apiResp.Usage.CompletionTokens,
	}, nil
}
