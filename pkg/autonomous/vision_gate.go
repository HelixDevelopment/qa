// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"digital.vasic.helixqa/pkg/llm"
	"digital.vasic.helixqa/pkg/testbank"
	"digital.vasic.helixqa/pkg/vision/hash"
)

// EnvGoldensDir is the fallback for PipelineConfig.VisionGoldensDir.
const EnvGoldensDir = "HELIXQA_GOLDENS_DIR"

// blindDescribePrompt asks for an expectation-BLIND reading of the
// frame. It deliberately carries neither the step's expectation nor its
// recognize/forbid tokens, so the model cannot parrot them back.
const blindDescribePrompt = "Describe what this screenshot shows.\n" +
	"On the first line write the heading TEXT: alone. Below it, transcribe every piece of " +
	"text that is visible on the screen, verbatim, one item per line. If no text is " +
	"visible, write nothing under it.\n" +
	"Then write the heading DESCRIPTION: and describe the layout and state of the " +
	"screen in one paragraph. Use no other headings.\n" +
	"Report only what is visible; do not infer or guess anything that is not shown."

// frameSHA256 is the hex sha256 of the exact frame bytes.
func frameSHA256(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// framePHash decodes a frame and returns its 64-bit pHash.
func framePHash(b []byte) (uint64, error) {
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	return hash.PHasher{}.Hash(img)
}

// Structure of a blind reply, enforced strictly (see extractTranscription).
var (
	// a standalone TEXT: heading (optionally markdown/bold-decorated)
	transcriptionHeadRe = regexp.MustCompile(`(?i)^[ \t]*(?:#+[ \t]*)?[*_]*[ \t]*TEXT[ \t]*[*_]*[ \t]*:[ \t]*[*_]*[ \t]*$`)
	// a DESCRIPTION: heading (content may follow on the same line)
	descriptionHeadRe = regexp.MustCompile(`(?i)^[ \t]*(?:#+[ \t]*)?[*_]*[ \t]*DESCRIPTION[ \t]*[*_]*[ \t]*:`)
	// any other heading-like line: markdown heading, whole-line bold, or a
	// few words ending in ':' or a dash with nothing after them
	headingLikeRes = []*regexp.Regexp{
		regexp.MustCompile(`^[ \t]*#`),
		regexp.MustCompile(`^[ \t]*\*\*[^*]+\*\*[ \t]*:?[ \t]*$`),
		regexp.MustCompile(`^[ \t]*__[^_]+__[ \t]*:?[ \t]*$`),
		regexp.MustCompile(`^[ \t]*[A-Za-z][A-Za-z ]{0,40}[ \t]*(?::|-|–|—)[ \t]*$`),
	}
)

func isHeadingLike(line string) bool {
	for _, re := range headingLikeRes {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}

// extractTranscription returns the TEXT: section of a blind reply — the
// verbatim on-screen text — and whether the reply has the required shape:
// its first non-blank line is a standalone TEXT: heading and the section is
// closed by a DESCRIPTION: heading. Any other heading-like line before the
// DESCRIPTION: heading (markdown "#", a whole-line **bold**, "Word -",
// "Word:") makes the shape invalid, because the model's own prose could
// follow it. Only this section is evidence of what is ON the screen; the
// DESCRIPTION is the model's prose ("the Settings screen is not shown")
// and must never satisfy recognize or trip forbid. Filler lines such as
// "(none)" or "No text visible" are dropped: they mean "no text", not text.
// Recall cost: a screen whose transcription has a bare label line such as
// "Username:" is reported unusable (the step is BLOCKED), never passed.
func extractTranscription(reply string) (string, bool) {
	lines := strings.Split(strings.ReplaceAll(reply, "\r", ""), "\n")
	i := 0
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i == len(lines) || !transcriptionHeadRe.MatchString(lines[i]) {
		return "", false
	}
	var b strings.Builder
	for _, line := range lines[i+1:] {
		if descriptionHeadRe.MatchString(line) {
			return b.String(), true
		}
		if isHeadingLike(line) {
			return "", false
		}
		key := testbank.VisionTokenKey(line)
		if key == "" || testbank.IsVisionFiller(key) {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return "", false // no DESCRIPTION: heading closed the TEXT: section
}

// compareFrames decides whether two decodable frames differ. Frames that
// cannot be decoded or have different dimensions are NOT comparable: the
// caller must treat that as could-not-determine, never as "changed".
func compareFrames(a, b []byte) (changed bool, pct float64, err error) {
	ia, _, errA := image.Decode(bytes.NewReader(a))
	ib, _, errB := image.Decode(bytes.NewReader(b))
	if errA != nil || errB != nil {
		return false, 0, fmt.Errorf("frames not decodable (before: %v, after: %v)", errA, errB)
	}
	if ia.Bounds().Dx() != ib.Bounds().Dx() || ia.Bounds().Dy() != ib.Bounds().Dy() {
		return false, 0, fmt.Errorf("frames not comparable: before %dx%d, after %dx%d",
			ia.Bounds().Dx(), ia.Bounds().Dy(), ib.Bounds().Dx(), ib.Bounds().Dy())
	}
	changed, pct = framesDiffer(a, b)
	return changed, pct, nil
}

// goldensDir resolves where `golden: <id>` images live.
func (ste *StructuredTestExecutor) goldensDir() string {
	if ste.config.VisionGoldensDir != "" {
		return ste.config.VisionGoldensDir
	}
	return os.Getenv(EnvGoldensDir)
}

// loadGolden finds <dir>/<id>.{png,jpg,jpeg} and returns its pHash and path.
func (ste *StructuredTestExecutor) loadGolden(id string) (uint64, string, error) {
	if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return 0, "", fmt.Errorf("golden id %q is not a bare id", id)
	}
	dir := ste.goldensDir()
	if dir == "" {
		return 0, "", fmt.Errorf("no goldens directory configured (PipelineConfig.VisionGoldensDir / %s)", EnvGoldensDir)
	}
	for _, ext := range []string{".png", ".jpg", ".jpeg"} {
		p := filepath.Join(dir, id+ext)
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		h, err := framePHash(data)
		if err != nil {
			return 0, p, fmt.Errorf("golden %s does not decode: %w", p, err)
		}
		return h, p, nil
	}
	return 0, "", fmt.Errorf("golden %q not found in %s (.png/.jpg/.jpeg)", id, dir)
}

// judgeStep turns an action result plus the before/after frames into the
// step verdict. Invariants (pinned by no_bluff_vision_test.go):
//
//   - a failed action fails the step (unchanged behaviour);
//   - a vision-GATED step passes only when every requested check
//     positively succeeds on the bound after-frame; anything that cannot
//     be determined makes it BLOCKED, never passed;
//   - a non-gated step that the model could not confirm passes on its
//     action but is reported UNVERIFIED-by-vision;
//   - every verdict carries the after-frame's sha256 + pHash.
func (ste *StructuredTestExecutor) judgeStep(
	ctx context.Context,
	step testbank.TestStep,
	act ActionResult,
	before, after []byte,
	r *TestStepResult,
) {
	r.VisionGated = step.IsVisionGated()
	r.GoldenDistance = -1
	if len(before) > 0 {
		r.BeforeFrameSHA256 = frameSHA256(before)
	}
	// Private copy: the bytes the hash names are the bytes judged.
	frame := append([]byte(nil), after...)
	frameUsable := len(frame) > 0 && !IsBlankScreenshot(frame)
	if len(frame) > 0 {
		r.FrameSHA256 = frameSHA256(frame)
		if ph, err := framePHash(frame); err == nil {
			r.FramePHash = fmt.Sprintf("%016x", ph)
		}
	}
	cite := func() string {
		if r.FrameSHA256 == "" {
			return "[no frame]"
		}
		return fmt.Sprintf("[frame sha256:%s phash:%s]", r.FrameSHA256[:12], r.FramePHash)
	}
	block := func(reason string) {
		r.Passed, r.Blocked = false, true
		r.VisionStatus, r.VisionReason = VisionBlocked, reason
		r.Actual = "BLOCKED (could not determine): " + reason + " " + cite()
	}
	refute := func(reason string) {
		r.Passed, r.Blocked = false, false
		r.VisionStatus, r.VisionReason = VisionRefuted, reason
		r.Actual = "VISION-REFUTED: " + reason + " " + cite()
	}

	// A failed action is a determined failure whatever the screen shows.
	if !act.Success {
		r.Passed = false
		r.VisionStatus, r.VisionReason = VisionNotRequested, "action failed"
		r.Actual = act.Message
		return
	}

	kind, _ := step.ParseAction()
	if !r.VisionGated {
		ste.judgeAdvisory(ctx, step, kind, act, frame, frameUsable, cite, r)
		return
	}

	// ── vision-GATED ──────────────────────────────────────────────
	if len(frame) == 0 {
		block("after-action frame was not captured")
		return
	}
	if !frameUsable {
		block("after-action frame is blank or undecodable — nothing on it can be verified")
		return
	}
	var notes []string

	if step.ExpectChange {
		if len(before) == 0 || IsBlankScreenshot(before) {
			block("expect_change: no usable before-action frame to compare against")
			return
		}
		changed, pct, cmpErr := compareFrames(before, frame)
		if cmpErr != nil {
			block("expect_change: " + cmpErr.Error())
			return
		}
		if !changed {
			refute(fmt.Sprintf("expect_change: the screen did not change (%.1f%% of sampled points differ; before sha256:%s)",
				pct, r.BeforeFrameSHA256[:12]))
			return
		}
		notes = append(notes, "screen changed")
	}

	if step.Golden != "" {
		gh, gpath, err := ste.loadGolden(step.Golden)
		if err != nil {
			block(err.Error())
			return
		}
		if r.FramePHash == "" {
			block("frame pHash could not be computed")
			return
		}
		fh, _ := framePHash(frame)
		d := hash.PHasher{}.Distance(fh, gh)
		r.GoldenDistance = d
		max := step.EffectivePHashMaxDistance()
		if d > max {
			refute(fmt.Sprintf("golden %q (%s): pHash distance %d > %d", step.Golden, gpath, d, max))
			return
		}
		notes = append(notes, fmt.Sprintf("golden %q pHash distance %d<=%d", step.Golden, d, max))
	}

	if step.NeedsVisionModel() {
		if len(ste.visionCandidates()) == 0 {
			block("no vision-capable provider is configured for a vision-gated step")
			return
		}
		if len(step.Recognize) > 0 || len(step.Forbid) > 0 {
			desc, transcript, who, reason := ste.blindTranscription(ctx, frame)
			if who == "" {
				block(reason)
				return
			}
			r.VisionDescription = desc
			r.VisionProvider = who
			missing, forbidden := matchVisionTokens(transcript, step.Recognize, step.Forbid)
			if len(forbidden) > 0 {
				refute(fmt.Sprintf("forbidden token(s) present: %s", strings.Join(forbidden, ", ")))
				return
			}
			if len(missing) > 0 {
				refute(fmt.Sprintf("recognize token(s) absent: %s", strings.Join(missing, ", ")))
				return
			}
			notes = append(notes, fmt.Sprintf("tokens: %d recognized, %d forbidden absent", len(step.Recognize), len(step.Forbid)))
		}
		if step.VisionVerify {
			ok, actual, who, err := ste.verifyOutcome(ctx, frame, step.Expected)
			if err != nil {
				block(actual)
				return
			}
			r.VisionProvider = who
			if !ok {
				refute("model answered VERIFIED: no — " + actual)
				return
			}
			notes = append(notes, "model VERIFIED: yes — "+actual)
		}
	}

	// Bind: the bytes that were judged are still the bytes the hash names.
	if frameSHA256(frame) != r.FrameSHA256 {
		block("frame bytes changed during verification — the verdict cannot be bound to the frame")
		return
	}
	r.Passed = true
	r.VisionReason = strings.Join(notes, "; ")
	if !step.HasPositiveVisionCheck() {
		// expect_change / forbid alone: something changed, nothing
		// forbidden was read — not evidence of the EXPECTED screen.
		r.VisionStatus = VisionUnverified
		r.Actual = act.Message + " | UNVERIFIED-by-vision: no positive content check (" + r.VisionReason + ") " + cite()
		return
	}
	r.VisionStatus = VisionVerified
	r.Actual = "VISION-VERIFIED: " + r.VisionReason + " " + cite()
}

// visionCandidates lists the vision-capable providers to ask, in order.
// An adaptive provider is expanded into its members so an ambiguous reply
// from one can fall through to the next (the adaptive wrapper only falls
// through on transport errors). The operator-configured
// HELIX_VISION_ENDPOINT provider is asked first.
func (ste *StructuredTestExecutor) visionCandidates() []llm.Provider {
	if ste.vision == nil {
		return nil
	}
	members := []llm.Provider{ste.vision}
	if multi, ok := ste.vision.(interface{ Providers() []llm.Provider }); ok {
		if ps := multi.Providers(); len(ps) > 0 {
			members = ps
		}
	}
	var endpoint, rest []llm.Provider
	for _, p := range members {
		if p == nil || !p.SupportsVision() {
			continue
		}
		if p.Name() == llm.ProviderVisionEndpoint {
			endpoint = append(endpoint, p)
		} else {
			rest = append(rest, p)
		}
	}
	return append(endpoint, rest...)
}

// blindTranscription asks providers in order for the expectation-blind
// reading and returns the first reply that carries a TEXT: section. A
// provider error, an empty reply or a reply without the section moves on
// to the next provider; if none qualifies, who is "" and reason names
// every provider tried.
func (ste *StructuredTestExecutor) blindTranscription(ctx context.Context, frame []byte) (desc, transcript, who, reason string) {
	var tried []string
	for _, p := range ste.visionCandidates() {
		resp, err := p.Vision(ctx, frame, blindDescribePrompt)
		if err != nil {
			tried = append(tried, p.Name()+": blind description failed: "+err.Error())
			continue
		}
		d := ""
		if resp != nil {
			d = strings.TrimSpace(resp.Content)
		}
		if d == "" {
			tried = append(tried, p.Name()+": blind description was empty")
			continue
		}
		t, ok := extractTranscription(d)
		if !ok {
			tried = append(tried, p.Name()+": blind reply has no TEXT: transcription section — on-screen text cannot be told apart from the model's prose")
			continue
		}
		return d, t, p.Name(), ""
	}
	if len(tried) == 0 {
		return "", "", "", "no vision-capable provider is configured"
	}
	return "", "", "", strings.Join(tried, " | ")
}

// judgeAdvisory handles a NON-gated step: the action decides, the model
// can only add a confirmation, a refutation (an explicit "VERIFIED: no"
// still fails the step), or nothing — in which case the step is reported
// UNVERIFIED-by-vision.
func (ste *StructuredTestExecutor) judgeAdvisory(
	ctx context.Context,
	step testbank.TestStep,
	kind testbank.ActionType,
	act ActionResult,
	frame []byte,
	frameUsable bool,
	cite func() string,
	r *TestStepResult,
) {
	r.Passed = true
	if kind.IsNonScreen() {
		r.VisionStatus, r.VisionReason = VisionNotRequested, "non-screen action judged by its own check"
		r.Actual = act.Message
		return
	}
	unverified := func(reason string) {
		r.VisionStatus, r.VisionReason = VisionUnverified, reason
		r.Actual = act.Message + " | UNVERIFIED-by-vision: " + reason
	}
	switch {
	case len(ste.visionCandidates()) == 0:
		unverified("no vision-capable provider configured")
		return
	case len(frame) == 0:
		unverified("after-action frame not captured")
		return
	case !frameUsable:
		unverified("after-action frame is blank or undecodable")
		return
	}
	ok, actual, who, err := ste.verifyOutcome(ctx, frame, step.Expected)
	r.VisionProvider = who
	switch {
	case err != nil:
		unverified(actual)
	case !ok:
		r.Passed = false
		r.VisionStatus, r.VisionReason = VisionRefuted, actual
		r.Actual = "VISION-REFUTED: model answered VERIFIED: no — " + actual + " " + cite()
	default:
		r.VisionStatus, r.VisionReason = VisionVerified, actual
		r.Actual = "vision-verified: " + actual + " " + cite()
	}
}

// VisionStatus records what the vision layer established about a
// step's after-action frame. Only VisionVerified is positive evidence.
type VisionStatus string

const (
	// VisionVerified: a positive, frame-bound confirmation (a vision
	// model's explicit "VERIFIED: yes" plus every deterministic check, or
	// a deterministic golden/change oracle alone).
	VisionVerified VisionStatus = "verified"
	// VisionRefuted: the frame was read and contradicts the expectation.
	VisionRefuted VisionStatus = "refuted"
	// VisionUnverified: a NON-gated step whose frame the vision layer
	// could not confirm. The step's verdict rests on the action alone
	// and is reported UNVERIFIED-by-vision, never "verified".
	VisionUnverified VisionStatus = "unverified"
	// VisionBlocked: a vision-GATED step whose frame could not be judged
	// (no provider, provider error, ambiguous/empty reply, blank or
	// missing frame, missing golden). Could-not-determine — never a pass.
	VisionBlocked VisionStatus = "blocked"
	// VisionNotRequested: a non-gated, non-screen step (shell/http/assert)
	// whose verdict is its own deterministic check; no model consulted.
	VisionNotRequested VisionStatus = "not_requested"
)

// CaseVerdict is the outcome of one structured test case.
type CaseVerdict string

const (
	CaseVerdictPassed  CaseVerdict = "PASSED"
	CaseVerdictFailed  CaseVerdict = "FAILED"
	CaseVerdictBlocked CaseVerdict = "BLOCKED"
	CaseVerdictSkipped CaseVerdict = "SKIPPED"
)

// StructuredCaseResult is the per-case evidence record: the verdict and
// every executed step's result, frame hashes included.
type StructuredCaseResult struct {
	ID      string           `json:"id"`
	Name    string           `json:"name"`
	Verdict CaseVerdict      `json:"verdict"`
	Steps   []TestStepResult `json:"steps"`
}

// ExitCode maps the run onto the three-valued contract: 1 when any case
// FAILED (a finding outranks could-not-determine), 2 when any case was
// BLOCKED or nothing passed at all, 0 only when cases passed and none
// failed or were blocked.
func (r *StructuredExecutionResult) ExitCode() int {
	switch {
	case r.TestCasesFailed > 0:
		return 1
	case r.TestCasesBlocked > 0, r.TestCasesPassed == 0:
		return 2
	default:
		return 0
	}
}

// normalizeVisionText folds text for deterministic token matching:
// lower-case, every run of non-letter/non-digit characters collapsed to
// one space, trimmed. It is the same fold the bank loader validates
// recognize/forbid tokens with.
func normalizeVisionText(s string) string {
	return testbank.VisionTokenKey(s)
}

// matchVisionTokens checks recognize/forbid tokens against a description
// as WHOLE normalised words/phrases (so "sign" does not match "signal").
// It returns the normalised recognize tokens that are absent and the
// normalised forbid tokens that are present.
func matchVisionTokens(description string, recognize, forbid []string) (missing, forbidden []string) {
	hay := " " + normalizeVisionText(description) + " "
	for _, tok := range recognize {
		k := normalizeVisionText(tok)
		if k == "" || !strings.Contains(hay, " "+k+" ") {
			missing = append(missing, k)
		}
	}
	for _, tok := range forbid {
		k := normalizeVisionText(tok)
		if k != "" && strings.Contains(hay, " "+k+" ") {
			forbidden = append(forbidden, k)
		}
	}
	return missing, forbidden
}
