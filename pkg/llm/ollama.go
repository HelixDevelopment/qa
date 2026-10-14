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
	ollamaDefaultModel = "minicpm-v:8b"
	ollamaHTTPTimeout  = 120 * time.Second
)

// ollamaProvider implements Provider for a self-hosted Ollama
// instance.
type ollamaProvider struct {
	baseURL string
	model   string
	client  *http.Client
}

// ollamaChatRequest is the JSON body sent to /api/chat.
type ollamaChatRequest struct {
	Model    string      `json:"model"`
	Messages []ollamaMsg `json:"messages"`
	Stream   bool        `json:"stream"`
}

// ollamaMsg is a single message in the Ollama messages array.
// Images is a slice of base64-encoded strings used for vision
// requests.
type ollamaMsg struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"`
}

// ollamaChatResponse is the JSON body returned by /api/chat.
type ollamaChatResponse struct {
	Model           string    `json:"model"`
	Message         ollamaMsg `json:"message"`
	PromptEvalCount int       `json:"prompt_eval_count"`
	EvalCount       int       `json:"eval_count"`
}

// NewOllamaProvider constructs a Provider backed by a self-hosted
// Ollama instance. Model defaults to qwen2.5. The HTTP client uses
// a 300-second timeout to accommodate slow local inference.
func NewOllamaProvider(cfg ProviderConfig) Provider {
	model := cfg.Model
	if model == "" {
		model = ollamaDefaultModel
	}
	return &ollamaProvider{
		baseURL: cfg.BaseURL,
		model:   model,
		client:  &http.Client{Timeout: ollamaHTTPTimeout},
	}
}

// Name returns the canonical provider identifier.
func (p *ollamaProvider) Name() string {
	return ProviderOllama
}

// SupportsVision reports that Ollama supports image inputs via the
// images array in the chat request.
func (p *ollamaProvider) SupportsVision() bool {
	return true
}

// Chat sends a multi-turn conversation to the Ollama /api/chat
// endpoint with streaming disabled and returns the assistant reply.
func (p *ollamaProvider) Chat(
	ctx context.Context,
	messages []Message,
) (*Response, error) {
	var msgs []ollamaMsg
	for _, m := range messages {
		msgs = append(msgs, ollamaMsg{
			Role:    m.Role,
			Content: m.Content,
		})
	}
	req := ollamaChatRequest{
		Model:    p.model,
		Messages: msgs,
		Stream:   false,
	}
	return p.doRequest(ctx, req)
}

// Vision sends a screenshot with a text prompt to the Ollama
// /api/chat endpoint. The image is passed as a base64 string in
// the images array of the user message.
func (p *ollamaProvider) Vision(
	ctx context.Context,
	image []byte,
	prompt string,
) (*Response, error) {
	encoded := base64.StdEncoding.EncodeToString(image)

	req := ollamaChatRequest{
		Model: p.model,
		Messages: []ollamaMsg{
			{
				Role:    RoleUser,
				Content: prompt,
				Images:  []string{encoded},
			},
		},
		Stream: false,
	}
	return p.doRequest(ctx, req)
}

// doRequest serialises req, POSTs to /api/chat, and parses the
// response into a *Response.
func (p *ollamaProvider) doRequest(
	ctx context.Context,
	req ollamaChatRequest,
) (*Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		p.baseURL+"/api/chat",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("ollama: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: send request: %w", scrubURLError(err))
	}
	defer httpResp.Body.Close()

	respBody, tooBig, err := readCappedBody(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("ollama: read response body: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"ollama: API error %d: %s",
			httpResp.StatusCode,
			errorBodyText(respBody),
		)
	}
	if tooBig {
		return nil, fmt.Errorf("ollama: response body exceeds %d bytes — refused", openaiMaxResponseBytes)
	}

	var apiResp ollamaChatResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("ollama: decode response: %w", err)
	}

	return &Response{
		Content:      apiResp.Message.Content,
		Model:        apiResp.Model,
		InputTokens:  apiResp.PromptEvalCount,
		OutputTokens: apiResp.EvalCount,
	}, nil
}
