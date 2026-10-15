// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package llmctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// VisionProfile is the llmctl catalog profile used for HelixQA vision.
const VisionProfile = "vision"

// DefaultVisionPort is the fixed catalog port of the vision profile.
const DefaultVisionPort = 8082

// readOnlySubcommands is the closed set of llmctl subcommands this
// package may execute. Anything that starts, stops, enables, switches
// or downloads a model is deliberately absent.
var readOnlySubcommands = map[string]bool{"plan": true, "status": true}

// ErrSubcommandNotAllowed is returned when a non read-only llmctl
// subcommand is requested.
var ErrSubcommandNotAllowed = errors.New("llmctl: subcommand not allowed (read-only introspection only)")

// State is the truthful availability state of a profile.
type State string

const (
	// StateAbsent: no llmctl binary.
	StateAbsent State = "ABSENT"
	// StateGated: the host tier gate refuses the profile.
	StateGated State = "GATED"
	// StateNoFit: allowed by tier but the footprint does not fit now.
	StateNoFit State = "NO_FIT"
	// StateNotRunning: the profile could run but nothing listens.
	StateNotRunning State = "NOT_RUNNING"
	// StateUnhealthy: something listens but the health probe fails.
	StateUnhealthy State = "UNHEALTHY"
	// StateReady: health probe OK.
	StateReady State = "READY"
	// StateUnknown: the plan could not be read or parsed.
	StateUnknown State = "UNKNOWN"
)

// ProfilePlan is one profile row of `llmctl plan --json`.
type ProfilePlan struct {
	Name        string
	Port        int
	Fits        bool
	TierOK      bool
	MinTier     string
	Recommended bool
	RAMMB       int
	VRAMMB      int
}

// Plan is the parsed `llmctl plan --json` output.
type Plan struct {
	Tier     string
	RAMMB    int
	Profiles map[string]ProfilePlan
}

type planJSON struct {
	Tier    string `json:"tier"`
	Budgets struct {
		RAMMB int `json:"ram_mb"`
	} `json:"budgets"`
	Profiles map[string]struct {
		Port        int    `json:"port"`
		Fits        bool   `json:"fits"`
		TierOK      bool   `json:"tier_ok"`
		MinTier     string `json:"min_tier"`
		Recommended bool   `json:"recommended"`
		RAMMB       int    `json:"ram_mb"`
		VRAMMB      int    `json:"vram_mb"`
	} `json:"profiles"`
}

// ParsePlan parses the JSON emitted by `llmctl plan --json`.
func ParsePlan(raw []byte) (Plan, error) {
	var pj planJSON
	if err := json.Unmarshal(raw, &pj); err != nil {
		return Plan{}, fmt.Errorf("llmctl: parse plan: %w", err)
	}
	if len(pj.Profiles) == 0 {
		return Plan{}, errors.New("llmctl: plan has no profiles")
	}
	p := Plan{Tier: pj.Tier, RAMMB: pj.Budgets.RAMMB, Profiles: map[string]ProfilePlan{}}
	for name, v := range pj.Profiles {
		p.Profiles[name] = ProfilePlan{
			Name: name, Port: v.Port, Fits: v.Fits, TierOK: v.TierOK,
			MinTier: v.MinTier, Recommended: v.Recommended,
			RAMMB: v.RAMMB, VRAMMB: v.VRAMMB,
		}
	}
	return p, nil
}

// Run executes one read-only llmctl subcommand and returns stdout.
func Run(ctx context.Context, bin, subcommand string, args ...string) ([]byte, error) {
	if !readOnlySubcommands[subcommand] {
		return nil, fmt.Errorf("%w: %q", ErrSubcommandNotAllowed, subcommand)
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, append([]string{subcommand}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("llmctl %s: %w: %s", subcommand, err,
			strings.TrimSpace(truncate(errb.String(), 300)))
	}
	return out.Bytes(), nil
}

// ReadPlan runs `llmctl plan --json` and parses it.
func ReadPlan(ctx context.Context, bin string) (Plan, error) {
	raw, err := Run(ctx, bin, "plan", "--json")
	if err != nil {
		return Plan{}, err
	}
	return ParsePlan(raw)
}

// Classify maps a profile plan row to GATED / NO_FIT / "" (runnable).
// The tier gate is reported as GATED and is never overridden here.
func (p Plan) Classify(profile string) (State, string) {
	row, ok := p.Profiles[profile]
	if !ok {
		return StateUnknown, fmt.Sprintf("profile %q not in plan", profile)
	}
	if !row.TierOK {
		return StateGated, fmt.Sprintf("host tier %q below min_tier=%s (operator decision to override)", p.Tier, row.MinTier)
	}
	if !row.Fits {
		return StateNoFit, fmt.Sprintf("needs %d MiB RAM / %d MiB VRAM, does not fit the current budget (%d MiB RAM)", row.RAMMB, row.VRAMMB, p.RAMMB)
	}
	return "", ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
