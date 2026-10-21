// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"fmt"
	"strings"

	"digital.vasic.helixqa/pkg/nexus"
)

// P3 port from <tools-dir>/browser-use/browser_use/agent/
// message_manager/service.py.
//
// MessageManager shapes the message stream sent to an LLM. The
// design mirrors browser-use's approach: the system prompt is
// pinned, the most recent N turns stay verbatim, everything older
// is collapsed into a caller-supplied Digest so the prompt fits
// under the configured token budget.

// Role is the speaker of a single message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is a single LLM chat turn.
type Message struct {
	Role    Role
	Content string
	// Image bytes (PNG) attached to the message. Empty = text-only.
	Image []byte
}

// Tokenizer is the narrow counter contract the MessageManager uses.
// Implementations can wrap tiktoken / llamacpp / any model-specific
// tokenizer without pulling that dependency into the agent package.
type Tokenizer interface {
	// CountTokens returns the token count for s. Implementations
	// MUST be thread-safe — the same tokenizer is shared across
	// concurrent Step() calls.
	CountTokens(s string) int
}

// ApproxTokenizer is a zero-dependency fallback that estimates token
// count as len(s) / 4. Precise enough to bound the prompt size; swap
// for a real tiktoken-backed counter in production.
type ApproxTokenizer struct{}

// CountTokens returns len(s)/4 (+1 to avoid the zero-on-tiny-input
// edge case).
func (ApproxTokenizer) CountTokens(s string) int {
	if s == "" {
		return 0
	}
	return 1 + len(s)/4
}

// Summariser collapses older history into a compact digest. A
// production summariser typically calls a small-model endpoint; the
// DefaultSummariser below is pure string manipulation so unit tests
// stay deterministic + offline.
type Summariser interface {
	Summarise(ctx context.Context, olderTurns []Message) (string, error)
}

// DefaultSummariser concatenates messages with a short prefix. It
// never calls out to an LLM, so it is safe in tests / CI / fully
// offline environments. Operators ship their own Summariser via
// WithSummariser for production. The output is capped so the
// digest itself can never exceed DefaultSummaryMaxBytes.
type DefaultSummariser struct{}

// DefaultSummaryMaxBytes caps the DefaultSummariser output so a
// pathologically long history cannot blow the token budget even
// under degenerate fuzz inputs.
const DefaultSummaryMaxBytes = 512

// Summarise implements Summariser.
func (DefaultSummariser) Summarise(_ context.Context, turns []Message) (string, error) {
	if len(turns) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("[digest of earlier turns]\n")
	for _, m := range turns {
		b.WriteString("- ")
		b.WriteString(string(m.Role))
		b.WriteString(": ")
		if len(m.Content) > 80 {
			b.WriteString(m.Content[:80])
			b.WriteString("…")
		} else {
			b.WriteString(m.Content)
		}
		b.WriteByte('\n')
		if b.Len() > DefaultSummaryMaxBytes {
			b.WriteString("… (truncated)\n")
			break
		}
	}
	out := b.String()
	if len(out) > DefaultSummaryMaxBytes {
		out = out[:DefaultSummaryMaxBytes] + "… (truncated)\n"
	}
	return out, nil
}

// MessageManagerConfig tunes MessageManager behaviour.
type MessageManagerConfig struct {
	// TokenBudget is the max total tokens MessageManager allows in
	// the outgoing Messages() slice (system + digest + verbatim
	// window + current user turn). Zero = 8000 (safe default).
	TokenBudget int
	// VerbatimTurns is the number of most-recent (user+assistant)
	// pairs to keep unsummarised. Zero = 4.
	VerbatimTurns int
	// Tokenizer counts tokens; zero = ApproxTokenizer{}.
	Tokenizer Tokenizer
	// Summariser collapses older turns into Digest; zero = DefaultSummariser{}.
	Summariser Summariser
}

// MessageManager builds the message stream for each PlanActions
// call. Callers keep one instance per AgentState (since history is
// stored there) and call PrepareStepState + Messages each step.
type MessageManager struct {
	cfg    MessageManagerConfig
	system Message
	digest Message
	// window is the rolling verbatim tail of full-fidelity turns.
	window []Message
}

// NewMessageManager wires defaults + the pinned system prompt.
func NewMessageManager(systemPrompt string, cfg MessageManagerConfig) *MessageManager {
	if cfg.TokenBudget <= 0 {
		cfg.TokenBudget = 8000
	}
	if cfg.VerbatimTurns <= 0 {
		cfg.VerbatimTurns = 4
	}
	if cfg.Tokenizer == nil {
		cfg.Tokenizer = ApproxTokenizer{}
	}
	if cfg.Summariser == nil {
		cfg.Summariser = DefaultSummariser{}
	}
	return &MessageManager{
		cfg:    cfg,
		system: Message{Role: RoleSystem, Content: systemPrompt},
	}
}

// PrepareStepState refreshes the window with the state's history +
// latest snapshot. It is idempotent: calling it twice with the same
// state produces the same Messages() output.
func (m *MessageManager) PrepareStepState(state *AgentState) {
	if state == nil {
		return
	}
	// Build a flat message list from history so the compactor has
	// something to chew on. Each AgentStep is two messages — the
	// assistant's plan reply + the user's observation for the next
	// turn.
	var flat []Message
	for _, step := range state.History {
		flat = append(flat, Message{
			Role:    RoleAssistant,
			Content: formatAssistantPlan(step),
		})
		flat = append(flat, Message{
			Role:    RoleUser,
			Content: formatPostExecuteObservation(step),
		})
	}
	m.window = flat
}

// CreateStateMessages returns the "current step" user turn the
// planner should see: task goal + snapshot summary + screenshot
// (when present). This is the only message whose content changes
// per step.
func (m *MessageManager) CreateStateMessages(state *AgentState) Message {
	var b strings.Builder
	fmt.Fprintf(&b, "TASK_GOAL: %s\n", state.TaskGoal)
	if state.Snapshot != nil {
		fmt.Fprintf(&b, "CURRENT_URL_OR_CONTEXT: %s\n", snapshotLabel(state.Snapshot))
		if len(state.Snapshot.Elements) > 0 {
			b.WriteString("INTERACTABLE_ELEMENTS:\n")
			for _, el := range state.Snapshot.Elements {
				fmt.Fprintf(&b, "  %s: role=%q name=%q\n", el.Ref, el.Role, el.Name)
			}
		}
	}
	msg := Message{Role: RoleUser, Content: b.String()}
	if len(state.Screenshot) > 0 {
		msg.Image = state.Screenshot
	}
	return msg
}

// Compact walks older turns out of the window + into the digest
// until the total token count fits under the configured budget.
// Returns true when at least one turn was digested (useful for
// tests + telemetry).
func (m *MessageManager) Compact(ctx context.Context, currentStep Message) (bool, error) {
	total := m.countTokens(currentStep)
	if len(m.window) == 0 {
		return false, nil
	}
	compacted := false
	// Keep peeling from the head of the window until we fit.
	for m.shouldCompact(total, currentStep) {
		trim := m.trimBatchSize()
		if trim <= 0 {
			break
		}
		batch := m.window[:trim]
		m.window = m.window[trim:]
		// Fold the current digest (if any) into the batch so the
		// summariser emits a single fresh digest each pass. Without
		// this, repeated compaction grows the digest unbounded.
		payload := batch
		if m.digest.Content != "" {
			payload = append([]Message{{Role: m.digest.Role, Content: m.digest.Content}}, batch...)
		}
		digestTxt, err := m.cfg.Summariser.Summarise(ctx, payload)
		if err != nil {
			return compacted, fmt.Errorf("messages: summarise: %w", err)
		}
		m.digest.Role = RoleUser
		m.digest.Content = digestTxt
		compacted = true
		total = m.countTokens(currentStep)
	}
	return compacted, nil
}

// Messages returns the outgoing message stream for the LLM call:
// system → (optional) digest → verbatim window → current turn.
func (m *MessageManager) Messages(currentStep Message) []Message {
	out := []Message{m.system}
	if m.digest.Content != "" {
		out = append(out, m.digest)
	}
	out = append(out, m.window...)
	out = append(out, currentStep)
	return out
}

// TotalTokens is an observability helper: the token count of the
// current outgoing message stream.
func (m *MessageManager) TotalTokens(currentStep Message) int {
	return m.countTokens(currentStep)
}

// Digest returns the current digest for introspection.
func (m *MessageManager) Digest() Message { return m.digest }

// WindowSize returns the number of verbatim turns retained.
func (m *MessageManager) WindowSize() int { return len(m.window) }

// --- internals -----------------------------------------------------------

func (m *MessageManager) shouldCompact(totalSoFar int, currentStep Message) bool {
	if totalSoFar > m.cfg.TokenBudget {
		return true
	}
	// Keep at most VerbatimTurns * 2 messages (one user + one
	// assistant per turn) in the window so self-healing prompts
	// don't drown in history.
	if len(m.window) > m.cfg.VerbatimTurns*2 {
		return true
	}
	return false
}

func (m *MessageManager) trimBatchSize() int {
	// Compact in pairs so we never lose one half of a turn.
	if len(m.window) < 2 {
		return len(m.window)
	}
	return 2
}

func (m *MessageManager) countTokens(currentStep Message) int {
	total := m.cfg.Tokenizer.CountTokens(m.system.Content)
	if m.digest.Content != "" {
		total += m.cfg.Tokenizer.CountTokens(m.digest.Content)
	}
	for _, t := range m.window {
		total += m.cfg.Tokenizer.CountTokens(t.Content)
	}
	total += m.cfg.Tokenizer.CountTokens(currentStep.Content)
	return total
}

func formatAssistantPlan(step AgentStep) string {
	if step.Evaluation == "" && step.NextGoal == "" && len(step.Actions) == 0 {
		return "(no plan)"
	}
	var b strings.Builder
	if step.Evaluation != "" {
		fmt.Fprintf(&b, "EVAL: %s\n", step.Evaluation)
	}
	if step.NextGoal != "" {
		fmt.Fprintf(&b, "NEXT_GOAL: %s\n", step.NextGoal)
	}
	if len(step.Actions) > 0 {
		b.WriteString("ACTIONS:\n")
		for _, a := range step.Actions {
			fmt.Fprintf(&b, "  %s %s %s\n", a.Kind, a.Target, a.Text)
		}
	}
	return b.String()
}

func formatPostExecuteObservation(step AgentStep) string {
	if len(step.Results) == 0 {
		return "(no observation)"
	}
	var b strings.Builder
	for _, r := range step.Results {
		if r.Success {
			fmt.Fprintf(&b, "OK: %s %s (%s)\n", r.Action.Kind, r.Action.Target, r.Duration)
		} else {
			fmt.Fprintf(&b, "ERR: %s %s — %s\n", r.Action.Kind, r.Action.Target, r.Err)
		}
	}
	return b.String()
}

func snapshotLabel(s *nexus.Snapshot) string {
	if s == nil {
		return ""
	}
	if s.Tree != "" {
		// Single-line summary so the prompt stays readable.
		first := s.Tree
		if idx := strings.IndexByte(first, '\n'); idx >= 0 {
			first = first[:idx]
		}
		if len(first) > 200 {
			first = first[:200] + "…"
		}
		return first
	}
	return "(snapshot captured)"
}
