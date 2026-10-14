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
	"time"
)

const (
	geminiDefaultModel = "gemini-2.5-flash"
	geminiHTTPTimeout  = 40 * time.Second
	// geminiGenerateURLFmt no longer includes the API key
	// in the URL. The key is sent via the x-goog-api-key
	// header to prevent leakage in error messages, logs,
	// and HTTP access logs.
	geminiGenerateURLFmt = "https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent"
)

// googleProvider implements Provider for the Google Gemini API.
type googleProvider struct {
	apiKey string
	model  string
	client *http.Client
}

// geminiRequest is the JSON body sent to the generateContent
// endpoint.
type geminiRequest struct {
	Contents []geminiContent `json:"contents"`
}

// geminiContent is a single turn in the Gemini conversation.
type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

// geminiPart is a content block — either text or inline image
// data.
type geminiPart struct {
	Text       string        `json:"text,omitempty"`
	InlineData *geminiInline `json:"inline_data,omitempty"`
}

// geminiInline holds base64-encoded image data for vision
// requests.
type geminiInline struct {
	MIMEType string `json:"mime_type"`
	Data     string `json:"data"`
}

// geminiResponse is the JSON body returned by generateContent.
type geminiResponse struct {
	Candidates    []geminiCandidate `json:"candidates"`
	UsageMetadata *geminiUsage      `json:"usageMetadata,omitempty"`
}

// geminiCandidate is a single completion candidate.
type geminiCandidate struct {
	Content geminiContent `json:"content"`
}

// geminiUsage holds token counts from the API response.
type geminiUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
}

// NewGoogleProvider constructs a Provider backed by the Google
// Gemini API. Model defaults to gemini-2.5-flash.
func NewGoogleProvider(cfg ProviderConfig) Provider {
	model := cfg.Model
	if model == "" {
		model = geminiDefaultModel
	}
	return &googleProvider{
		apiKey: cfg.APIKey,
		model:  model,
		// Transport-level timeout ensures TCP connections can't
		// hang indefinitely even when context cancellation fails
		// to propagate (common on some Go/Linux combinations).
		// ResponseHeaderTimeout kills stalled connections waiting
		// for server response, while context timeout handles
		// per-call budgets (45s navigate, 120s plan).
		client: &http.Client{
			Transport: &http.Transport{
				ResponseHeaderTimeout: 90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
			},
		},
	}
}

// Name returns the canonical provider identifier.
func (p *googleProvider) Name() string {
	return ProviderGoogle
}

// SupportsVision reports that Gemini supports image inputs.
func (p *googleProvider) SupportsVision() bool {
	return true
}

// Chat sends a multi-turn conversation to the Gemini
// generateContent API and returns the assistant reply.
func (p *googleProvider) Chat(
	ctx context.Context,
	messages []Message,
) (*Response, error) {
	var contents []geminiContent
	for _, m := range messages {
		role := m.Role
		if role == RoleSystem {
			// Gemini uses "user" for system-like prompts.
			role = "user"
		}
		if role == RoleAssistant {
			role = "model"
		}
		contents = append(contents, geminiContent{
			Role: role,
			Parts: []geminiPart{
				{Text: m.Content},
			},
		})
	}
	req := geminiRequest{Contents: contents}
	return p.doRequest(ctx, req)
}

// Vision sends a screenshot with a text prompt to the Gemini
// generateContent API using inline_data for the image.
func (p *googleProvider) Vision(
	ctx context.Context,
	image []byte,
	prompt string,
) (*Response, error) {
	encoded := base64.StdEncoding.EncodeToString(image)

	req := geminiRequest{
		Contents: []geminiContent{
			{
				Role: "user",
				Parts: []geminiPart{
					{
						InlineData: &geminiInline{
							MIMEType: "image/png",
							Data:     encoded,
						},
					},
					{
						Text: prompt,
					},
				},
			},
		},
	}
	return p.doRequest(ctx, req)
}

// geminiMaxRetries is the maximum number of retries for
// rate-limited requests. With 5 retries and exponential
// backoff (1s, 2s, 3s, 4s, 5s) the total retry window
// is ~15 seconds for FLASHING FAST performance.
const geminiMaxRetries = 5

// doRequest serialises req, POSTs to generateContent, and
// parses the response into a *Response. Retries on 429
// (rate limit) with exponential backoff.
func (p *googleProvider) doRequest(
	ctx context.Context,
	req geminiRequest,
) (*Response, error) {
	url := fmt.Sprintf(geminiGenerateURLFmt, p.model)
	var lastErr error
	for attempt := 0; attempt <= geminiMaxRetries; attempt++ {
		if attempt > 0 {
			// REDUCED for FLASHING FAST performance (was attempt*5s).
			backoff := time.Duration(attempt) * time.Second
			fmt.Printf(
				"  [gemini] retry %d/%d after %v\n",
				attempt, geminiMaxRetries, backoff,
			)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}
		resp, err := p.doRequestURL(ctx, req, url)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		// Only retry on rate limit errors (429 / RESOURCE_EXHAUSTED).
		if !isRateLimitError(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

// isRateLimitError checks if an error indicates a rate limit
// that may resolve with a retry.
func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return bytes.Contains([]byte(s), []byte("429")) ||
		bytes.Contains([]byte(s), []byte("RESOURCE_EXHAUSTED")) ||
		bytes.Contains([]byte(s), []byte("quota"))
}

// redactKeyFromError redacts the API key from an error message
// to prevent sensitive information leakage in logs.
func redactKeyFromError(err error, key string) error {
	if err == nil {
		return nil
	}
	if key == "" {
		return err
	}
	msg := err.Error()
	if !bytes.Contains([]byte(msg), []byte(key)) {
		return err
	}
	redacted := bytes.ReplaceAll(
		[]byte(msg), []byte(key), []byte("REDACTED"),
	)
	return fmt.Errorf("%s", redacted)
}

// doRequestURL is the internal implementation that posts the
// request to the given URL. It is split from doRequest so that
// tests can supply a test-server URL without overriding the
// production URL format.
func (p *googleProvider) doRequestURL(
	ctx context.Context,
	req geminiRequest,
	url string,
) (*Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("google: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		url,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("google: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	// API key via header — never in the URL — to prevent
	// leakage in error messages, logs, and access logs.
	httpReq.Header.Set("x-goog-api-key", p.apiKey)

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("google: send request: %w", scrubURLError(err))
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf(
			"google: read response body: %w", err,
		)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"google: API error %d: %s",
			httpResp.StatusCode,
			string(respBody),
		)
	}

	var apiResp geminiResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf(
			"google: decode response: %w", err,
		)
	}

	var content string
	if len(apiResp.Candidates) > 0 {
		for _, part := range apiResp.Candidates[0].Content.Parts {
			content += part.Text
		}
	}

	var inputTokens, outputTokens int
	if apiResp.UsageMetadata != nil {
		inputTokens = apiResp.UsageMetadata.PromptTokenCount
		outputTokens = apiResp.UsageMetadata.CandidatesTokenCount
	}

	return &Response{
		Content:      content,
		Model:        p.model,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}, nil
}
