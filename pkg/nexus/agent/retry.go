// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"errors"
	"hash/fnv"
	"math/rand"
	"strings"
	"sync"
	"time"

	"digital.vasic.helixqa/pkg/nexus"
)

// P4 ports from browser-use's error-recovery stack
// (<tools-dir>/browser-use/browser_use/agent/service.py).
// Three pluggable components:
//
//   - ExpBackoffWithJitter: caps + randomises retry delays.
//   - LoopDetector: catches 2 / 3-cycle repetitions.
//   - SelfHealer: re-invokes the planner with an explicit
//     "previous attempt failed because …" context on action error.

// ===========================================================================
// ExpBackoffWithJitter
// ===========================================================================

// BackoffPolicy configures an exponential-backoff-with-jitter retry
// strategy. Zero values resolve to safe defaults.
type BackoffPolicy struct {
	Base      time.Duration // default 1s
	Factor    float64       // default 2.0
	Max       time.Duration // default 30s
	JitterPct float64       // default 0.25 (±25%)
	MaxTries  int           // default 5
}

// Resolve returns a BackoffPolicy with any zero field filled in.
func (p BackoffPolicy) Resolve() BackoffPolicy {
	if p.Base <= 0 {
		p.Base = 1 * time.Second
	}
	if p.Factor <= 0 {
		p.Factor = 2.0
	}
	if p.Max <= 0 {
		p.Max = 30 * time.Second
	}
	if p.JitterPct < 0 {
		p.JitterPct = 0
	}
	if p.JitterPct == 0 {
		p.JitterPct = 0.25
	}
	if p.MaxTries <= 0 {
		p.MaxTries = 5
	}
	return p
}

// delayFor returns the delay to wait before retry attempt n (1-based).
// Exposed as a method so tests can assert monotonic growth without
// calling Sleep.
func (p BackoffPolicy) delayFor(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	base := float64(p.Base)
	for i := 1; i < n; i++ {
		base *= p.Factor
		if base > float64(p.Max) {
			base = float64(p.Max)
			break
		}
	}
	d := time.Duration(base)
	jitter := 1.0 + (rand.Float64()*2-1)*p.JitterPct
	d = time.Duration(float64(d) * jitter)
	if d < 0 {
		d = 0
	}
	if d > p.Max {
		d = p.Max
	}
	return d
}

// RetryWithBackoff invokes fn up to policy.MaxTries times, sleeping
// between attempts per the BackoffPolicy. The ctx deadline / cancel
// terminates the loop early even mid-sleep. Returns the last error
// when all attempts exhaust.
func RetryWithBackoff(ctx context.Context, policy BackoffPolicy, fn func() error) error {
	p := policy.Resolve()
	var lastErr error
	for attempt := 1; attempt <= p.MaxTries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt == p.MaxTries {
			break
		}
		delay := p.delayFor(attempt)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return lastErr
}

// ===========================================================================
// LoopDetector
// ===========================================================================

// LoopDetector watches a rolling buffer of recent action fingerprints
// and reports when a 2-cycle or 3-cycle has repeated past the
// configured threshold. A non-nil detector on an Agent triggers a
// stop + escalation when a loop is detected.
type LoopDetector struct {
	mu         sync.Mutex
	bufferSize int
	threshold  int
	history    []uint64
}

// NewLoopDetector returns a detector with defaults that catch 2- and
// 3-action cycles repeating >= threshold times. bufferSize ≥ 12 is
// required for reliable 3-cycle detection.
func NewLoopDetector(bufferSize, threshold int) *LoopDetector {
	if bufferSize < 12 {
		bufferSize = 12
	}
	if threshold < 2 {
		threshold = 3
	}
	return &LoopDetector{bufferSize: bufferSize, threshold: threshold}
}

// Record appends a fingerprint for the given action list onto the
// rolling buffer. Call once per Agent.Step.
func (ld *LoopDetector) Record(actions []nexus.Action) {
	ld.mu.Lock()
	defer ld.mu.Unlock()
	ld.history = append(ld.history, fingerprintActions(actions))
	if len(ld.history) > ld.bufferSize {
		ld.history = ld.history[len(ld.history)-ld.bufferSize:]
	}
}

// IsLoop returns true when the most recent entries form a cycle of
// length 2 or 3 repeated ≥ threshold times.
func (ld *LoopDetector) IsLoop() bool {
	ld.mu.Lock()
	defer ld.mu.Unlock()
	h := ld.history
	for _, cycle := range []int{2, 3} {
		if len(h) < cycle*ld.threshold {
			continue
		}
		if detectCycle(h, cycle, ld.threshold) {
			return true
		}
	}
	return false
}

// Reset clears the rolling buffer — useful after a planner Escape
// succeeds.
func (ld *LoopDetector) Reset() {
	ld.mu.Lock()
	defer ld.mu.Unlock()
	ld.history = ld.history[:0]
}

// detectCycle returns true iff the last cycle*threshold entries of h
// form a repeated cycle of length `cycle`.
func detectCycle(h []uint64, cycle, threshold int) bool {
	tail := h[len(h)-cycle*threshold:]
	for i := 0; i < cycle; i++ {
		v := tail[i]
		for rep := 1; rep < threshold; rep++ {
			if tail[i+rep*cycle] != v {
				return false
			}
		}
	}
	return true
}

func fingerprintActions(actions []nexus.Action) uint64 {
	h := fnv.New64a()
	for _, a := range actions {
		_, _ = h.Write([]byte(a.Kind))
		_, _ = h.Write([]byte{'|'})
		_, _ = h.Write([]byte(a.Target))
		_, _ = h.Write([]byte{'|'})
		_, _ = h.Write([]byte(a.Text))
	}
	return h.Sum64()
}

// ===========================================================================
// SelfHealer
// ===========================================================================

// SelfHealer re-invokes the LLM with an explicit "previous attempt
// failed" context when a step's Execute phase reports an error.
// Stagehand inspired (<tools-dir>/stagehand/lib/StagehandPage.ts).
type SelfHealer struct {
	MaxAttempts int       // default 3
	Client      LLMClient // required
}

// NewSelfHealer wires a healer bound to client. MaxAttempts falls
// back to 3 on zero / negative input.
func NewSelfHealer(client LLMClient, maxAttempts int) (*SelfHealer, error) {
	if client == nil {
		return nil, errors.New("selfhealer: nil client")
	}
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	return &SelfHealer{MaxAttempts: maxAttempts, Client: client}, nil
}

// Heal re-plans the current step given the reason the previous
// attempt failed. It appends a "previous_attempt_failed_because"
// hint to the request's recent history so the LLM sees the fresh
// failure context.
func (h *SelfHealer) Heal(ctx context.Context, state *AgentState, reason string) (AgentStep, error) {
	var lastErr error
	for attempt := 1; attempt <= h.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return AgentStep{}, err
		}
		req := PlanRequest{
			TaskGoal:   state.TaskGoal,
			Snapshot:   state.Snapshot,
			Screenshot: state.Screenshot,
			RecentSteps: append([]AgentStep{{
				Evaluation: "previous_attempt_failed_because: " + reason,
				NextGoal:   "recover",
			}}, state.RecentSteps(3)...),
		}
		step, err := h.Client.PlanStep(ctx, req)
		if err == nil {
			return step, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("selfhealer: exhausted attempts")
	}
	return AgentStep{}, lastErr
}

// ===========================================================================
// Fingerprint helper for external callers
// ===========================================================================

// FingerprintActions exposes the internal fingerprint so callers
// that want to integrate their own cycle logic can reuse the hash.
func FingerprintActions(actions []nexus.Action) uint64 {
	return fingerprintActions(actions)
}

// joinErrs flattens N errors into a single summary string for use
// in SelfHealer / RetryWithBackoff error messages.
func joinErrs(errs []error) string {
	parts := make([]string, 0, len(errs))
	for _, e := range errs {
		if e == nil {
			continue
		}
		parts = append(parts, e.Error())
	}
	return strings.Join(parts, "; ")
}
