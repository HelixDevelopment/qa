// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"context"
	"fmt"

	"digital.vasic.helixqa/pkg/llmctl"
)

// llmctlProvider implements Provider on top of the llmctl `vision`
// profile (llama-server, OpenAI-compatible, deterministic: temperature
// 0 and a fixed seed). It never starts the model: when nothing is
// serving, calls fail with llmctl.ErrNotRunning and AdaptiveProvider
// falls through to the next provider.
//
// A model answer returned by this provider is a PROPOSAL; callers
// that gate on it MUST confirm it with a deterministic oracle
// (llmctl.Confirm, Constitution 11.4.269).
type llmctlProvider struct {
	client *llmctl.VisionClient
	model  string
}

// NewLlmctlProvider constructs a Provider backed by the llmctl vision
// profile. cfg.BaseURL defaults to http://127.0.0.1:8082 and cfg.Model
// (when set) is the PINNED model id that must be served; cfg.APIKey is
// ignored (the engine is loopback-only).
func NewLlmctlProvider(cfg ProviderConfig) Provider {
	vc := llmctl.DefaultVisionConfig()
	if cfg.BaseURL != "" {
		vc.BaseURL = cfg.BaseURL
	}
	vc.ExpectModel = cfg.Model
	return &llmctlProvider{client: llmctl.NewVisionClient(vc), model: cfg.Model}
}

// Name returns the canonical provider identifier.
func (p *llmctlProvider) Name() string { return ProviderLlmctl }

// SupportsVision reports true: the point of this provider.
func (p *llmctlProvider) SupportsVision() bool { return true }

// Chat sends a text conversation.
func (p *llmctlProvider) Chat(ctx context.Context, messages []Message) (*Response, error) {
	msgs := make([]llmctl.ChatMessage, 0, len(messages))
	for _, m := range messages {
		if err := m.Validate(); err != nil {
			return nil, err
		}
		msgs = append(msgs, llmctl.ChatMessage{Role: m.Role, Content: m.Content})
	}
	a, err := p.client.Chat(ctx, msgs)
	if err != nil {
		return nil, fmt.Errorf("llmctl: chat: %w", err)
	}
	return toResponse(a), nil
}

// Vision sends a screenshot with a prompt.
func (p *llmctlProvider) Vision(ctx context.Context, image []byte, prompt string) (*Response, error) {
	a, err := p.client.Vision(ctx, image, prompt)
	if err != nil {
		return nil, fmt.Errorf("llmctl: vision: %w", err)
	}
	return toResponse(a), nil
}

func toResponse(a *llmctl.Answer) *Response {
	return &Response{
		Content:      a.Content,
		Model:        a.Model,
		InputTokens:  a.InputTokens,
		OutputTokens: a.OutputTokens,
	}
}
