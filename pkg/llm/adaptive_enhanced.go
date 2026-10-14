// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"digital.vasic.helixqa/pkg/learning"
)

// EnhancedAdaptiveProvider wraps providers with rate limiting,
// circuit breakers, and prompt optimization
type EnhancedAdaptiveProvider struct {
	providers   []Provider
	configs     []ProviderConfig
	costTracker *CostTracker
	phase       string
	learning    *learning.KnowledgeBase
	platforms   []string
}

// NewEnhancedAdaptiveProvider creates an enhanced adaptive provider
func NewEnhancedAdaptiveProvider(configs []ProviderConfig) (*EnhancedAdaptiveProvider, error) {
	var providers []Provider
	var validConfigs []ProviderConfig

	for _, cfg := range configs {
		if err := cfg.Validate(); err != nil {
			fmt.Printf("  [llm] skipping invalid config for %s: %v\n", cfg.Name, err)
			continue
		}

		var provider Provider
		switch cfg.Name {
		case ProviderAnthropic:
			provider = NewAnthropicProvider(cfg)
		case ProviderGoogle:
			provider = NewGoogleProvider(cfg)
		case ProviderOllama, ProviderUITars:
			provider = NewOllamaProvider(cfg)
		case "astica":
			provider = NewAsticaProvider(cfg)
		case ProviderVisionEndpoint:
			provider = NewOpenAIProvider(cfg)
		default:
			// Check registry for OpenAI-compatible providers
			if defaults, ok := providerDefaults[cfg.Name]; ok {
				if cfg.BaseURL == "" {
					cfg.BaseURL = defaults.BaseURL
				}
				if cfg.Model == "" && defaults.Model != "" {
					cfg.Model = defaults.Model
				}
				provider = NewOpenAIProvider(cfg)
			} else if cfg.Name == ProviderOpenAI {
				provider = NewOpenAIProvider(cfg)
			}
		}

		if provider != nil {
			providers = append(providers, provider)
			validConfigs = append(validConfigs, cfg)
			fmt.Printf("  [llm] registered provider: %s (model: %s)\n", cfg.Name, cfg.Model)
		}
	}

	if len(providers) == 0 {
		return nil, fmt.Errorf("no valid providers configured")
	}

	return &EnhancedAdaptiveProvider{
		providers: providers,
		configs:   validConfigs,
	}, nil
}

// Name returns the provider name
func (eap *EnhancedAdaptiveProvider) Name() string {
	return "adaptive-enhanced"
}

// SupportsVision returns true if any provider supports vision
func (eap *EnhancedAdaptiveProvider) SupportsVision() bool {
	for _, p := range eap.providers {
		if p.SupportsVision() {
			return true
		}
	}
	return false
}

// SetCostTracker sets the cost tracker
func (eap *EnhancedAdaptiveProvider) SetCostTracker(ct *CostTracker) {
	eap.costTracker = ct
}

// SetPhase sets the current phase
func (eap *EnhancedAdaptiveProvider) SetPhase(phase string) {
	eap.phase = phase
}

// SetKnowledgeBase sets the knowledge base for prompt optimization
func (eap *EnhancedAdaptiveProvider) SetKnowledgeBase(kb *learning.KnowledgeBase, platforms []string) {
	eap.learning = kb
	eap.platforms = platforms
}

// Chat executes chat with rate limiting and fallback
func (eap *EnhancedAdaptiveProvider) Chat(
	ctx context.Context,
	messages []Message,
) (*Response, error) {
	if len(eap.providers) == 0 {
		return nil, fmt.Errorf("no providers available")
	}

	// Rank providers by health and rate limit availability
	ranked := eap.rankProviders()

	var errs []string
	for _, idx := range ranked {
		provider := eap.providers[idx]
		config := eap.configs[idx]

		// Get rate limiter
		rl := GetRateLimiter(config.Name)

		// Check circuit breaker
		if rl.isCircuitOpen() {
			errs = append(errs, fmt.Sprintf("%s: circuit breaker open", config.Name))
			continue
		}

		// Estimate tokens in request
		estimatedTokens := 0
		for _, m := range messages {
			estimatedTokens += EstimateTokens(m.Content)
		}

		// Wait for rate limit
		if err := rl.Wait(ctx, estimatedTokens); err != nil {
			errs = append(errs, fmt.Sprintf("%s: rate limit wait failed: %v", config.Name, err))
			continue
		}

		// Optimize prompt if we have knowledge base
		optimizedMessages := messages
		if eap.learning != nil && len(messages) > 0 {
			optimizedMessages = eap.optimizeMessagesForProvider(config.Name, messages)
		}

		// Try the provider with extended timeout
		timeout := 45 * time.Second
		if config.Name == "google" || config.Name == ProviderAnthropic {
			timeout = 120 * time.Second // Give native providers more time (Google can take 45-60s)
		}

		pCtx, cancel := context.WithTimeout(ctx, timeout)
		resp, err := provider.Chat(pCtx, optimizedMessages)
		cancel()

		if err == nil {
			rl.RecordSuccess()
			eap.recordCost(config.Name, resp, "chat", true)
			fmt.Printf("  [llm] success: %s\n", config.Name)
			return resp, nil
		}

		// Record failure
		rl.RecordFailure(err)

		// Check if it's a rate limit error
		if isRateLimitError(err) {
			retryAfter := ParseRetryAfter(err)
			if retryAfter > 0 {
				fmt.Printf("  [llm] %s rate limited, waiting %v\n", config.Name, retryAfter)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(retryAfter):
					// Retry this provider
					pCtx2, cancel2 := context.WithTimeout(ctx, timeout)
					resp, err = provider.Chat(pCtx2, optimizedMessages)
					cancel2()
					if err == nil {
						rl.RecordSuccess()
						eap.recordCost(config.Name, resp, "chat", true)
						fmt.Printf("  [llm] success after retry: %s\n", config.Name)
						return resp, nil
					}
				}
			}
		}

		errs = append(errs, fmt.Sprintf("%s: %v", config.Name, err))
		fmt.Printf("  [llm] %s failed: %v\n", config.Name, err)

		if ctx.Err() != nil {
			break
		}
	}

	return nil, fmt.Errorf("all providers failed: %s", strings.Join(errs, "; "))
}

// Vision executes vision request with rate limiting and fallback
func (eap *EnhancedAdaptiveProvider) Vision(
	ctx context.Context,
	image []byte,
	prompt string,
) (*Response, error) {
	// Get vision-capable providers
	var capable []int
	for i, p := range eap.providers {
		if p.SupportsVision() {
			capable = append(capable, i)
		}
	}

	if len(capable) == 0 {
		return nil, fmt.Errorf("no vision-capable providers")
	}

	// Rank by health
	ranked := eap.rankProviders()
	var rankedCapable []int
	for _, idx := range ranked {
		for _, c := range capable {
			if c == idx {
				rankedCapable = append(rankedCapable, idx)
				break
			}
		}
	}

	var errs []string
	for _, idx := range rankedCapable {
		provider := eap.providers[idx]
		config := eap.configs[idx]

		rl := GetRateLimiter(config.Name)
		if rl.isCircuitOpen() {
			errs = append(errs, fmt.Sprintf("%s: circuit breaker open", config.Name))
			continue
		}

		// Vision requests are more expensive
		estimatedTokens := EstimateTokens(prompt) + len(image)/4 // Image tokens
		if err := rl.Wait(ctx, estimatedTokens); err != nil {
			errs = append(errs, fmt.Sprintf("%s: rate limit wait failed", config.Name))
			continue
		}

		timeout := 30 * time.Second
		if config.Name == "google" {
			timeout = 90 * time.Second // Gemini needs more time for vision
		}

		pCtx, cancel := context.WithTimeout(ctx, timeout)
		resp, err := provider.Vision(pCtx, image, prompt)
		cancel()

		if err == nil {
			rl.RecordSuccess()
			eap.recordCost(config.Name, resp, "vision", true)
			return resp, nil
		}

		rl.RecordFailure(err)

		// Retry on rate limit
		if isRateLimitError(err) {
			retryAfter := ParseRetryAfter(err)
			if retryAfter > 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(retryAfter):
					pCtx2, cancel2 := context.WithTimeout(ctx, timeout)
					resp, err = provider.Vision(pCtx2, image, prompt)
					cancel2()
					if err == nil {
						rl.RecordSuccess()
						eap.recordCost(config.Name, resp, "vision", true)
						return resp, nil
					}
				}
			}
		}

		errs = append(errs, fmt.Sprintf("%s: %v", config.Name, err))

		if ctx.Err() != nil {
			break
		}
	}

	return nil, fmt.Errorf("all vision providers failed: %s", strings.Join(errs, "; "))
}

// rankProviders ranks providers by health (failures, circuit state)
func (eap *EnhancedAdaptiveProvider) rankProviders() []int {
	type providerScore struct {
		idx   int
		score float64
	}

	var scores []providerScore
	for i, cfg := range eap.configs {
		rl := GetRateLimiter(cfg.Name)

		// Base score
		score := 100.0

		// Penalize failures
		rl.mu.RLock()
		failures := rl.failures
		circuitOpen := rl.circuitOpen
		rl.mu.RUnlock()

		score -= float64(failures) * 10
		if circuitOpen {
			score -= 50
		}

		// Bonus for reliable providers
		switch cfg.Name {
		case "google", ProviderAnthropic:
			score += 20 // Prefer native providers
		}

		scores = append(scores, providerScore{idx: i, score: score})
	}

	// Sort by score descending (bubble sort for simplicity)
	for i := 0; i < len(scores); i++ {
		for j := i + 1; j < len(scores); j++ {
			if scores[j].score > scores[i].score {
				scores[i], scores[j] = scores[j], scores[i]
			}
		}
	}

	var result []int
	for _, s := range scores {
		result = append(result, s.idx)
	}
	return result
}

// optimizeMessagesForProvider optimizes messages for a specific provider's token limit
func (eap *EnhancedAdaptiveProvider) optimizeMessagesForProvider(
	providerName string,
	messages []Message,
) []Message {
	if eap.learning == nil || len(messages) == 0 {
		return messages
	}

	// Find the system message or first user message
	var targetIdx int
	for i, m := range messages {
		if m.Role == RoleSystem {
			targetIdx = i
			break
		}
	}

	// Optimize the knowledge base prompt
	optimizedPrompt := GetOptimizedPrompt(providerName, eap.learning, eap.platforms)

	// Create new messages with optimized prompt
	optimized := make([]Message, len(messages))
	copy(optimized, messages)
	optimized[targetIdx] = Message{
		Role:    messages[targetIdx].Role,
		Content: optimizedPrompt,
	}

	return optimized
}

// Providers returns the underlying providers
func (eap *EnhancedAdaptiveProvider) Providers() []Provider {
	return eap.providers
}

// recordCost records cost in tracker
func (eap *EnhancedAdaptiveProvider) recordCost(
	providerName string,
	resp *Response,
	callType string,
	success bool,
) {
	if eap.costTracker == nil || resp == nil {
		return
	}

	inputTokens := resp.InputTokens
	outputTokens := resp.OutputTokens

	if inputTokens == 0 && outputTokens == 0 && resp.Content != "" {
		outputTokens = len(resp.Content) / 4
		if outputTokens == 0 {
			outputTokens = 1
		}
	}

	eap.costTracker.Record(
		providerName, resp.Model, eap.phase, callType,
		inputTokens, outputTokens, success,
	)
}
