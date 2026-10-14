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
	asticaDefaultEndpoint     = "https://vision.astica.ai/describe"
	asticaDefaultModelVer     = "2.5_full"
	asticaHTTPTimeout         = 60 * time.Second
	asticaDefaultVisionParams = "describe,objects,faces,text"
)

// asticaProvider implements Provider for the Astica.AI Vision API.
// Unlike OpenAI-compatible providers, Astica uses a native JSON
// format with token-in-body authentication and a dedicated vision
// endpoint.
type asticaProvider struct {
	apiKey   string
	endpoint string
	modelVer string
	client   *http.Client
}

// asticaRequest is the JSON body sent to Astica's /describe endpoint.
type asticaRequest struct {
	Token        string `json:"tkn"`
	ModelVersion string `json:"modelVersion"`
	Input        string `json:"input"`
	VisionParams string `json:"visionParams"`
	GPTPrompt    string `json:"gpt_prompt"`
}

// asticaResponse is the JSON body returned by Astica's /describe endpoint.
type asticaResponse struct {
	Status     string `json:"status"`
	CaptionGPT string `json:"caption_GPTS"`
	Caption    struct {
		Text       string  `json:"text"`
		Confidence float64 `json:"confidence"`
	} `json:"caption"`
}

// NewAsticaProvider constructs a Provider backed by the Astica.AI
// Vision API. The endpoint defaults to https://vision.astica.ai/describe;
// model version defaults to 2.5_full.
func NewAsticaProvider(cfg ProviderConfig) Provider {
	endpoint := cfg.BaseURL
	if endpoint == "" {
		endpoint = asticaDefaultEndpoint
	}
	modelVer := cfg.Model
	if modelVer == "" {
		modelVer = asticaDefaultModelVer
	}
	return &asticaProvider{
		apiKey:   cfg.APIKey,
		endpoint: endpoint,
		modelVer: modelVer,
		client:   &http.Client{Timeout: asticaHTTPTimeout},
	}
}

// Name returns the canonical provider identifier.
func (p *asticaProvider) Name() string {
	return "astica"
}

// SupportsVision reports that Astica supports image inputs.
// Vision is the primary purpose of this provider.
func (p *asticaProvider) SupportsVision() bool {
	return true
}

// Chat sends a text-only prompt to Astica using the gpt_prompt
// field. Since Astica is a vision-first API, chat sends a
// minimal request without image data and returns the GPT
// caption as the response content.
func (p *asticaProvider) Chat(
	ctx context.Context,
	messages []Message,
) (*Response, error) {
	// Concatenate all user messages into a single prompt.
	var prompt string
	for _, m := range messages {
		if m.Role == RoleUser || m.Role == RoleSystem {
			if prompt != "" {
				prompt += "\n"
			}
			prompt += m.Content
		}
	}
	if prompt == "" {
		return nil, fmt.Errorf("astica: no user content in messages")
	}

	req := asticaRequest{
		Token:        p.apiKey,
		ModelVersion: p.modelVer,
		VisionParams: asticaDefaultVisionParams,
		GPTPrompt:    prompt,
	}
	return p.doRequest(ctx, req)
}

// Vision sends a screenshot with a text prompt to the Astica
// Vision API using base64-encoded image data.
func (p *asticaProvider) Vision(
	ctx context.Context,
	image []byte,
	prompt string,
) (*Response, error) {
	encoded := base64.StdEncoding.EncodeToString(image)
	dataURI := "data:image/png;base64," + encoded

	req := asticaRequest{
		Token:        p.apiKey,
		ModelVersion: p.modelVer,
		Input:        dataURI,
		VisionParams: asticaDefaultVisionParams,
		GPTPrompt:    prompt,
	}
	return p.doRequest(ctx, req)
}

// doRequest serialises req, POSTs to the Astica endpoint, and
// parses the response into a *Response.
func (p *asticaProvider) doRequest(
	ctx context.Context,
	req asticaRequest,
) (*Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("astica: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		p.endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("astica: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("astica: send request: %w", scrubURLError(err))
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("astica: read response body: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"astica: API error %d: %s",
			httpResp.StatusCode,
			string(respBody),
		)
	}

	var apiResp asticaResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("astica: decode response: %w", err)
	}

	if apiResp.Status != "success" {
		return nil, fmt.Errorf(
			"astica: API returned status %q",
			apiResp.Status,
		)
	}

	// Prefer the detailed GPT-powered caption; fall back to
	// the standard caption if the GPT field is empty.
	content := apiResp.CaptionGPT
	if content == "" {
		content = apiResp.Caption.Text
	}

	return &Response{
		Content: content,
		Model:   p.modelVer,
	}, nil
}
