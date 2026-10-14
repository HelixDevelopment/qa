// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// Package llm defines the Provider interface and shared types
// for LLM integrations used by the HelixQA autonomous agent.
// Concrete implementations (Anthropic, OpenAI, Ollama, etc.)
// live in sub-packages and satisfy the Provider interface.
package llm

import (
	"context"
	"fmt"
	"strings"
)

// Provider name constants identify supported LLM backends.
const (
	ProviderAnthropic  = "anthropic"
	ProviderOpenAI     = "openai"
	ProviderGoogle     = "google"
	ProviderOllama     = "ollama"
	ProviderUITars     = "ui-tars"
	ProviderOpenRouter = "openrouter"
	ProviderDeepSeek   = "deepseek"
	ProviderGroq       = "groq"
	// ProviderLlmctl is the local llmctl `vision` profile (port 8082).
	ProviderLlmctl = "llmctl"
)

// Role constants define the participant roles in a conversation.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Provider is the interface that every LLM backend must satisfy.
// Implementations must be safe for concurrent use.
type Provider interface {
	// Chat sends a multi-turn conversation and returns the
	// assistant reply.
	Chat(ctx context.Context, messages []Message) (*Response, error)

	// Vision sends a screenshot (raw bytes) with a text prompt
	// and returns the assistant reply. Not all providers support
	// this — check SupportsVision before calling.
	Vision(ctx context.Context, image []byte, prompt string) (*Response, error)

	// Name returns the canonical provider identifier, matching
	// one of the Provider* constants.
	Name() string

	// SupportsVision reports whether this provider can process
	// image inputs via the Vision method.
	SupportsVision() bool
}

// Message represents a single turn in a conversation.
type Message struct {
	// Role identifies the author: RoleSystem, RoleUser, or
	// RoleAssistant.
	Role string `json:"role"`

	// Content is the text body of the message.
	Content string `json:"content"`
}

// Validate checks that the message is well-formed.
func (m Message) Validate() error {
	if m.Role == "" {
		return fmt.Errorf("llm: message role is required")
	}
	if m.Content == "" {
		return fmt.Errorf("llm: message content is required")
	}
	return nil
}

// Response holds the reply returned by a Provider.
type Response struct {
	// Content is the assistant's generated text.
	Content string `json:"content"`

	// Model is the model identifier used for generation.
	Model string `json:"model"`

	// InputTokens is the number of tokens in the prompt.
	InputTokens int `json:"input_tokens"`

	// OutputTokens is the number of tokens in the reply.
	OutputTokens int `json:"output_tokens"`
}

// HasContent reports whether the response contains non-whitespace
// text.
func (r Response) HasContent() bool {
	return strings.TrimSpace(r.Content) != ""
}

// ProviderConfig holds the configuration needed to construct a
// Provider. Fields required per provider:
//   - Anthropic/OpenAI/Google: APIKey
//   - Ollama/UITars: BaseURL
//   - All: Model (recommended)
type ProviderConfig struct {
	// Name is the provider identifier (one of the Provider*
	// constants).
	Name string `yaml:"name" json:"name"`

	// APIKey is the secret API key for cloud providers.
	APIKey string `yaml:"api_key" json:"api_key"`

	// BaseURL is the base HTTP URL for self-hosted providers
	// such as Ollama.
	BaseURL string `yaml:"base_url" json:"base_url"`

	// Model is the model identifier to use for requests.
	Model string `yaml:"model" json:"model"`
}

// Validate checks that the configuration is valid for the given
// provider, returning a descriptive error when fields are missing.
func (c ProviderConfig) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("llm: provider config name is required")
	}
	switch c.Name {
	case ProviderLlmctl:
		// Loopback engine: base_url defaults to http://127.0.0.1:8082
		// and no API key exists.
		return nil
	case ProviderOllama, ProviderUITars:
		if c.BaseURL == "" {
			return fmt.Errorf(
				"llm: provider %q requires base_url", c.Name,
			)
		}
	default:
		// All other providers (cloud APIs) require an API key
		if c.APIKey == "" {
			return fmt.Errorf(
				"llm: provider %q requires api_key", c.Name,
			)
		}
	}
	return nil
}
