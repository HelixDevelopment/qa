// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	anthropicDefaultBaseURL = "https://api.anthropic.com"
	anthropicDefaultModel   = "claude-sonnet-4-20250514"
	anthropicVersion        = "2023-06-01"
	anthropicHTTPTimeout    = 45 * time.Second
)

// anthropicProvider implements Provider for Anthropic Claude API.
type anthropicProvider struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
}

// anthropicRequest is the JSON body sent to /v1/messages.
type anthropicRequest struct {
	Model     string         `json:"model"`
	MaxTokens int            `json:"max_tokens"`
	System    string         `json:"system,omitempty"`
	Messages  []anthropicMsg `json:"messages"`
}

// anthropicMsg is a single message in the Anthropic messages array.
type anthropicMsg struct {
	Role    string             `json:"role"`
	Content []anthropicContent `json:"content"`
}

// anthropicContent is a single content block inside a message.
type anthropicContent struct {
	Type   string           `json:"type"`
	Text   string           `json:"text,omitempty"`
	Source *anthropicSource `json:"source,omitempty"`
}

// anthropicSource describes an image source for vision requests.
type anthropicSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// anthropicResponse is the JSON body returned by /v1/messages.
type anthropicResponse struct {
	Model   string             `json:"model"`
	Content []anthropicContent `json:"content"`
	Usage   anthropicUsage     `json:"usage"`
}

// anthropicUsage holds token counts from the API response.
type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// NewAnthropicProvider constructs a Provider backed by the Anthropic
// Claude API. BaseURL defaults to https://api.anthropic.com; Model
// defaults to claude-sonnet-4-20250514.
func NewAnthropicProvider(cfg ProviderConfig) Provider {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = anthropicDefaultBaseURL
	}
	model := cfg.Model
	if model == "" {
		model = anthropicDefaultModel
	}
	return &anthropicProvider{
		apiKey:  cfg.APIKey,
		baseURL: baseURL,
		model:   model,
		client:  &http.Client{Timeout: anthropicHTTPTimeout},
	}
}

// Name returns the canonical provider identifier.
func (p *anthropicProvider) Name() string {
	return ProviderAnthropic
}

// SupportsVision reports that Anthropic Claude supports image inputs.
func (p *anthropicProvider) SupportsVision() bool {
	return true
}

// Chat sends a multi-turn conversation to the Anthropic Messages API
// and returns the assistant reply. System messages are extracted from
// the slice and sent in the top-level "system" field.
func (p *anthropicProvider) Chat(
	ctx context.Context,
	messages []Message,
) (*Response, error) {
	var system string
	var msgs []anthropicMsg

	for _, m := range messages {
		if m.Role == RoleSystem {
			if system != "" {
				system += "\n"
			}
			system += m.Content
			continue
		}
		msgs = append(msgs, anthropicMsg{
			Role: m.Role,
			Content: []anthropicContent{
				{Type: "text", Text: m.Content},
			},
		})
	}

	req := anthropicRequest{
		Model:     p.model,
		MaxTokens: 4096,
		System:    system,
		Messages:  msgs,
	}
	return p.doRequest(ctx, req)
}

// Vision sends a screenshot with a text prompt to the Anthropic
// Messages API and returns the assistant reply.
func (p *anthropicProvider) Vision(
	ctx context.Context,
	image []byte,
	prompt string,
) (*Response, error) {
	encoded := base64.StdEncoding.EncodeToString(image)

	req := anthropicRequest{
		Model:     p.model,
		MaxTokens: 4096,
		Messages: []anthropicMsg{
			{
				Role: RoleUser,
				Content: []anthropicContent{
					{
						Type: "image",
						Source: &anthropicSource{
							Type:      "base64",
							MediaType: "image/png",
							Data:      encoded,
						},
					},
					{
						Type: "text",
						Text: prompt,
					},
				},
			},
		},
	}
	return p.doRequest(ctx, req)
}

// doRequest serialises req, POSTs to /v1/messages, and parses the
// response into a *Response.
func (p *anthropicProvider) doRequest(
	ctx context.Context,
	req anthropicRequest,
) (*Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		p.baseURL+"/v1/messages",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("anthropic: create request: %w", err)
	}
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	httpReq.Header.Set("content-type", "application/json")

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: send request: %w", scrubURLError(err))
	}
	defer httpResp.Body.Close()

	respBody, tooBig, err := readCappedBody(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: read response body: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"anthropic: API error %d: %s",
			httpResp.StatusCode,
			errorBodyText(respBody, p.apiKey),
		)
	}
	if tooBig {
		return nil, fmt.Errorf("anthropic: response body exceeds %d bytes — refused", openaiMaxResponseBytes)
	}

	var apiResp anthropicResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("anthropic: decode response: %w", err)
	}

	var content string
	for _, block := range apiResp.Content {
		if block.Type == "text" {
			content += block.Text
		}
	}

	return &Response{
		Content:      content,
		Model:        apiResp.Model,
		InputTokens:  apiResp.Usage.InputTokens,
		OutputTokens: apiResp.Usage.OutputTokens,
	}, nil
}
