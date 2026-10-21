// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrCircuitOpen is returned (wrapped) by VisionVia when the member's
// circuit breaker is open: the member was not called.
var ErrCircuitOpen = errors.New("llm: circuit breaker open")

// VisionRouter is implemented by the adaptive wrappers. VisionVia asks ONE
// chosen member provider while the wrapper keeps its own bookkeeping
// (availability marking, rate limiter, circuit breaker, cost tracking).
// Callers that must pick the answering provider themselves (the no-bluff
// vision gate asks members in a deliberate order) use it instead of
// calling the member directly.
type VisionRouter interface {
	VisionVia(ctx context.Context, p Provider, image []byte, prompt string) (*Response, error)
}

// VisionVia implements VisionRouter for AdaptiveProvider.
func (a *AdaptiveProvider) VisionVia(ctx context.Context, p Provider, image []byte, prompt string) (*Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a.isUnavailable(p.Name()) {
		return nil, fmt.Errorf("llm: provider %s is marked unavailable", p.Name())
	}
	timeout := adaptivePerProviderTimeout
	if _, known := visionRegistryByProvider[p.Name()]; known {
		timeout = adaptiveVisionTimeout
	}
	pCtx, cancel := context.WithTimeout(ctx, timeout)
	resp, err := p.Vision(pCtx, image, prompt)
	cancel()
	if err == nil {
		a.recordCost(p.Name(), resp, "vision", true)
		return resp, nil
	}
	if isAuthOrCreditError(err) {
		a.markUnavailable(p.Name(), err.Error())
	}
	return nil, err
}

// VisionVia implements VisionRouter for EnhancedAdaptiveProvider.
func (eap *EnhancedAdaptiveProvider) VisionVia(ctx context.Context, p Provider, image []byte, prompt string) (*Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name := p.Name()
	for i, q := range eap.providers {
		if q == p {
			name = eap.configs[i].Name
			break
		}
	}
	rl := GetRateLimiter(name)
	if rl.isCircuitOpen() {
		return nil, fmt.Errorf("%w for %s", ErrCircuitOpen, name)
	}
	if err := rl.Wait(ctx, EstimateTokens(prompt)+len(image)/4); err != nil {
		return nil, fmt.Errorf("llm: %s: rate limit wait failed: %w", name, err)
	}
	timeout := 30 * time.Second
	if name == "google" {
		timeout = 90 * time.Second
	}
	pCtx, cancel := context.WithTimeout(ctx, timeout)
	resp, err := p.Vision(pCtx, image, prompt)
	cancel()
	if err == nil {
		rl.RecordSuccess()
		eap.recordCost(name, resp, "vision", true)
		return resp, nil
	}
	rl.RecordFailure(err)
	return nil, err
}

var (
	_ VisionRouter = (*AdaptiveProvider)(nil)
	_ VisionRouter = (*EnhancedAdaptiveProvider)(nil)
)
