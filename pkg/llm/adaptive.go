// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// adaptivePerProviderTimeout caps how long a single
// provider is allowed per call during adaptive fallback.
// This prevents N slow providers from compounding into
// N * timeout total latency.
// INCREASED to 120s: Google Gemini 2.5 Flash can take 45-60s
// for complex prompts, and we need time for fallback providers.
const adaptivePerProviderTimeout = 120 * time.Second

// adaptiveVisionTimeout is longer than the chat timeout
// to allow native vision providers (Gemini, Anthropic)
// time for their internal retry/backoff on rate limits.
const adaptiveVisionTimeout = 90 * time.Second

// AdaptiveProvider wraps a slice of Provider implementations and
// tries them in order, falling back to the next on failure. It
// satisfies the Provider interface itself, so it can be used
// anywhere a Provider is expected.
//
// Providers that return auth/credit errors (401, 403, 402,
// "insufficient", "quota", "billing") are marked as unavailable
// for the remainder of the session to avoid wasting time retrying
// providers with no credits.
type AdaptiveProvider struct {
	providers   []Provider
	costTracker *CostTracker
	phase       string
	unavailable map[string]string // provider name -> reason
}

// NewAdaptiveProvider constructs an AdaptiveProvider from an
// explicit list of already-constructed Provider instances. The
// providers are tried in the order supplied.
func NewAdaptiveProvider(providers ...Provider) *AdaptiveProvider {
	return &AdaptiveProvider{
		providers:   providers,
		unavailable: make(map[string]string),
	}
}

// isAuthOrCreditError returns true if the error indicates the
// provider's API key is invalid, expired, or the account has
// insufficient credits/quota. These errors are permanent for the
// session — retrying will not help.
func isAuthOrCreditError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	// HTTP status codes in error messages
	for _, code := range []string{"401", "402", "403"} {
		if strings.Contains(msg, "status "+code) ||
			strings.Contains(msg, "error "+code) ||
			strings.Contains(msg, code+":") {
			return true
		}
	}
	// Common credit/auth error keywords from various providers
	for _, kw := range []string{
		"unauthorized", "authentication", "invalid api key",
		"invalid_api_key", "api key", "apikey",
		"insufficient", "quota exceeded", "billing",
		"payment required", "credits", "limit exceeded",
		"rate limit", "too many requests",
		"forbidden", "access denied",
		"subscription", "plan limit",
	} {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}

// markUnavailable records that a provider should be skipped
// for the remainder of this session.
func (a *AdaptiveProvider) markUnavailable(name string, reason string) {
	if a.unavailable == nil {
		a.unavailable = make(map[string]string)
	}
	a.unavailable[name] = reason
	fmt.Printf("  [llm] provider %s marked unavailable: %s\n", name, reason)
}

// isUnavailable checks if a provider has been marked as
// unavailable due to auth/credit errors.
func (a *AdaptiveProvider) isUnavailable(name string) bool {
	if a.unavailable == nil {
		return false
	}
	_, skip := a.unavailable[name]
	return skip
}

// GetUnavailableProviders returns a copy of the unavailable
// providers map for diagnostics.
func (a *AdaptiveProvider) GetUnavailableProviders() map[string]string {
	result := make(map[string]string, len(a.unavailable))
	for k, v := range a.unavailable {
		result[k] = v
	}
	return result
}

// NewAdaptiveFromConfigs constructs an AdaptiveProvider by
// instantiating providers from the supplied ProviderConfig slice.
// Configs that fail validation or reference an unknown provider
// type are silently skipped. An error is returned only when zero
// valid providers are produced.
func NewAdaptiveFromConfigs(
	configs []ProviderConfig,
) (*AdaptiveProvider, error) {
	var providers []Provider
	for _, cfg := range configs {
		if err := cfg.Validate(); err != nil {
			continue
		}
		switch cfg.Name {
		case ProviderAnthropic:
			providers = append(providers, NewAnthropicProvider(cfg))
		case ProviderGoogle:
			providers = append(providers, NewGoogleProvider(cfg))
		case ProviderOllama, ProviderUITars:
			providers = append(providers, NewOllamaProvider(cfg))
		case "astica":
			providers = append(providers, NewAsticaProvider(cfg))
		case ProviderVisionEndpoint:
			providers = append(providers, NewOpenAIProvider(cfg))
		case ProviderLlmctl:
			providers = append(providers, NewLlmctlProvider(cfg))
		default:
			// Check registry for OpenAI-compatible providers
			if defaults, ok := providerDefaults[cfg.Name]; ok {
				if cfg.BaseURL == "" {
					cfg.BaseURL = defaults.BaseURL
				}
				if cfg.Model == "" && defaults.Model != "" {
					cfg.Model = defaults.Model
				}
				providers = append(providers, NewOpenAIProvider(cfg))
			} else if cfg.Name == ProviderOpenAI {
				providers = append(providers, NewOpenAIProvider(cfg))
			}
			// truly unknown — skip silently
		}
	}
	if len(providers) == 0 {
		return nil, fmt.Errorf(
			"llm: NewAdaptiveFromConfigs: no valid providers produced",
		)
	}
	return &AdaptiveProvider{
		providers:   providers,
		unavailable: make(map[string]string),
	}, nil
}

// SetCostTracker attaches a CostTracker to this provider.
// All subsequent Chat and Vision calls will record their
// costs in the tracker.
func (a *AdaptiveProvider) SetCostTracker(
	ct *CostTracker,
) {
	a.costTracker = ct
}

// GetCostTracker returns the attached CostTracker, or nil
// if none has been set.
func (a *AdaptiveProvider) GetCostTracker() *CostTracker {
	return a.costTracker
}

// SetPhase sets the current pipeline phase label used for
// cost tracking (e.g. "plan", "execute", "curiosity",
// "analyze").
func (a *AdaptiveProvider) SetPhase(phase string) {
	a.phase = phase
}

// Name returns the canonical identifier for the adaptive provider.
func (a *AdaptiveProvider) Name() string {
	return "adaptive"
}

// SupportsVision reports true when at least one wrapped provider
// supports vision inputs.
func (a *AdaptiveProvider) SupportsVision() bool {
	for _, p := range a.providers {
		if p.SupportsVision() {
			return true
		}
	}
	return false
}

// Chat tries each provider in order and returns the first successful
// response. If every provider returns an error the combined errors
// are returned in a single diagnostic message. Each provider call is
// capped at adaptivePerProviderTimeout.
func (a *AdaptiveProvider) Chat(
	ctx context.Context,
	messages []Message,
) (*Response, error) {
	if len(a.providers) == 0 {
		return nil, fmt.Errorf("llm: all providers failed: no providers configured")
	}
	var errs []string
	for _, p := range a.providers {
		if a.isUnavailable(p.Name()) {
			continue
		}
		pCtx, pCancel := context.WithTimeout(
			ctx, adaptivePerProviderTimeout,
		)
		resp, err := p.Chat(pCtx, messages)
		pCancel()
		if err == nil {
			a.recordCost(
				p.Name(), resp, "chat", true,
			)
			return resp, nil
		}
		if isAuthOrCreditError(err) {
			a.markUnavailable(p.Name(), err.Error())
		}
		errs = append(errs, fmt.Sprintf("%s: %v", p.Name(), err))
		if ctx.Err() != nil {
			break
		}
	}
	return nil, fmt.Errorf(
		"llm: all providers failed: %s",
		strings.Join(errs, "; "),
	)
}

// Vision tries each vision-capable provider in order and returns
// the first successful response. Providers that do not support
// vision are skipped entirely. If no vision-capable provider is
// registered a descriptive error is returned immediately.
//
// Provider ordering is determined dynamically by
// rankVisionProviders, which scores each provider based on
// quality, reliability, cost, and API key availability from the
// vision model registry. This replaces the former hardcoded
// priority list.
//
// Each provider call is capped at adaptivePerProviderTimeout to
// prevent N slow providers from compounding into N * timeout
// total latency.
func (a *AdaptiveProvider) Vision(
	ctx context.Context,
	image []byte,
	prompt string,
) (*Response, error) {
	capable := rankVisionProviders(a.providers)
	if len(capable) == 0 {
		// List all providers to help debug configuration.
		var names []string
		for _, p := range a.providers {
			names = append(names, p.Name())
		}
		return nil, fmt.Errorf(
			"llm: no vision-capable providers among %v",
			names,
		)
	}
	// Log which providers will be attempted for observability.
	{
		var names []string
		for _, p := range capable {
			names = append(names, p.Name())
		}
		fmt.Printf("  [llm] vision providers (ranked): %v\n", names)
	}
	var errs []string
	for _, p := range capable {
		if a.isUnavailable(p.Name()) {
			continue
		}
		// Providers with known registry entries get the longer
		// vision timeout to allow for internal retry/backoff.
		timeout := adaptivePerProviderTimeout
		if _, known := visionRegistryByProvider[p.Name()]; known {
			timeout = adaptiveVisionTimeout
		}
		pCtx, pCancel := context.WithTimeout(ctx, timeout)
		resp, err := p.Vision(pCtx, image, prompt)
		pCancel()
		if err == nil {
			a.recordCost(
				p.Name(), resp, "vision", true,
			)
			return resp, nil
		}
		if isAuthOrCreditError(err) {
			a.markUnavailable(p.Name(), err.Error())
		}
		errs = append(errs, fmt.Sprintf("%s: %v", p.Name(), err))
		// If the parent context is done, stop trying.
		if ctx.Err() != nil {
			break
		}
	}
	return nil, fmt.Errorf(
		"llm: all providers failed: %s",
		strings.Join(errs, "; "),
	)
}

// Providers returns the underlying Provider slice. This is
// used by the PhaseModelSelector to score individual
// providers for phase-specific selection.
func (a *AdaptiveProvider) Providers() []Provider {
	return a.providers
}

// recordCost records a cost entry in the attached tracker.
// It is a no-op when no cost tracker is set.
func (a *AdaptiveProvider) recordCost(
	providerName string,
	resp *Response,
	callType string,
	success bool,
) {
	if a.costTracker == nil || resp == nil {
		return
	}
	inputTokens := resp.InputTokens
	outputTokens := resp.OutputTokens
	// If the provider did not report token counts, estimate
	// from content length (1 token ~ 4 characters).
	if inputTokens == 0 && outputTokens == 0 &&
		resp.Content != "" {
		outputTokens = len(resp.Content) / 4
		if outputTokens == 0 {
			outputTokens = 1
		}
	}
	a.costTracker.Record(
		providerName, resp.Model, a.phase, callType,
		inputTokens, outputTokens, success,
	)
}
