// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package llmctl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// VerdictStatus is the outcome of confirming a model proposal.
type VerdictStatus string

const (
	// VerdictConfirmed: a deterministic oracle confirmed the claim.
	VerdictConfirmed VerdictStatus = "CONFIRMED"
	// VerdictRejected: a deterministic oracle contradicted the claim.
	VerdictRejected VerdictStatus = "REJECTED"
	// VerdictUnconfirmed: no usable deterministic oracle, or it
	// errored. NEVER a pass (11.4.269: the model alone is no gate).
	VerdictUnconfirmed VerdictStatus = "UNCONFIRMED"
)

// Oracle is a DETERMINISTIC confirmer (OCR, pixel, DOM, API fact).
type Oracle interface {
	// Name identifies the oracle in evidence.
	Name() string
	// Confirm reports whether claim holds, with machine-readable
	// evidence text.
	Confirm(ctx context.Context, claim string) (ok bool, evidence string, err error)
}

// modelBacked is implemented by oracles that are themselves model
// calls. They are refused: a model cannot confirm a model.
type modelBacked interface{ ModelBacked() bool }

// Verdict is the machine-readable result of Confirm.
type Verdict struct {
	Claim    string
	Status   VerdictStatus
	Oracle   string
	Evidence string
}

// Pass is true only for a deterministic confirmation.
func (v Verdict) Pass() bool { return v.Status == VerdictConfirmed }

// Confirm applies the 11.4.269 policy: a model proposal alone is
// never a verdict. With a nil, model-backed or failing oracle the
// result is VerdictUnconfirmed (and Pass() is false).
func Confirm(ctx context.Context, claim string, o Oracle) Verdict {
	v := Verdict{Claim: claim, Status: VerdictUnconfirmed}
	if o == nil {
		v.Evidence = "no deterministic oracle supplied (model proposal is advisory only)"
		return v
	}
	v.Oracle = o.Name()
	if mb, ok := o.(modelBacked); ok && mb.ModelBacked() {
		v.Evidence = "oracle is model-backed; a model cannot confirm a model (11.4.269)"
		return v
	}
	ok, ev, err := o.Confirm(ctx, claim)
	if err != nil {
		v.Evidence = "oracle error: " + err.Error()
		return v
	}
	v.Evidence = ev
	if ok {
		v.Status = VerdictConfirmed
	} else {
		v.Status = VerdictRejected
	}
	return v
}

// OCRTextOracle confirms a claim by case-insensitive containment in
// text extracted deterministically (OCR/DOM/accessibility tree).
type OCRTextOracle struct {
	// Text is the extracted screen text.
	Text string
	// Source labels where Text came from (tesseract, dom, ...).
	Source string
}

// Name implements Oracle.
func (o OCRTextOracle) Name() string {
	if o.Source == "" {
		return "ocr-text"
	}
	return "ocr-text:" + o.Source
}

// Confirm implements Oracle.
func (o OCRTextOracle) Confirm(_ context.Context, claim string) (bool, string, error) {
	c := strings.ToLower(strings.TrimSpace(claim))
	if c == "" {
		return false, "", errors.New("empty claim")
	}
	if strings.TrimSpace(o.Text) == "" {
		return false, "", errors.New("oracle text is empty (instrument blind, not an absence)")
	}
	found := strings.Contains(strings.ToLower(o.Text), c)
	return found, fmt.Sprintf("contains(%q)=%v over %d bytes", claim, found, len(o.Text)), nil
}

// ErrNonDeterministic is returned by RunDeterministic when the runs
// do not all produce the same canonical hash.
var ErrNonDeterministic = errors.New("llmctl: runs are not identical (non-deterministic)")

// DeterministicResult is the evidence of N identical runs.
type DeterministicResult struct {
	Hash    string
	Answers []string
	Runs    int
}

// CanonicalHash normalises whitespace and returns sha256 hex.
func CanonicalHash(s string) string {
	canon := strings.Join(strings.Fields(s), " ")
	sum := sha256.Sum256([]byte(canon))
	return hex.EncodeToString(sum[:])
}

// MinDeterministicRuns is the minimum N for the determinism proof.
const MinDeterministicRuns = 3

// RunDeterministic runs fn n (>= 3) times and requires identical
// canonical hashes.
func RunDeterministic(ctx context.Context, n int, fn func(context.Context) (string, error)) (DeterministicResult, error) {
	if n < MinDeterministicRuns {
		return DeterministicResult{}, fmt.Errorf("llmctl: need >= %d runs, got %d", MinDeterministicRuns, n)
	}
	res := DeterministicResult{Runs: n}
	for i := 0; i < n; i++ {
		a, err := fn(ctx)
		if err != nil {
			return res, fmt.Errorf("run %d: %w", i+1, err)
		}
		res.Answers = append(res.Answers, a)
		h := CanonicalHash(a)
		if i == 0 {
			res.Hash = h
		} else if h != res.Hash {
			return res, fmt.Errorf("%w: run %d hash %s != %s", ErrNonDeterministic, i+1, h[:12], res.Hash[:12])
		}
	}
	return res, nil
}
