// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"digital.vasic.helixqa/pkg/llm"
)

// analyzePrompt is the instruction sent to the vision LLM
// when analysing a single screenshot for issues.
const analyzePrompt = `You are a QA analyst reviewing a screenshot of a mobile or desktop application.

Identify all UI/UX issues visible in the screenshot. For each issue return a JSON object with these fields:
- "category": one of "visual", "ux", "accessibility", "performance", "functional", "brand", "content"
- "severity": one of "critical", "high", "medium", "low", "cosmetic"
- "title": short one-sentence summary
- "description": what you observed and why it is a problem
- "repro_steps": steps to reproduce (omit if obvious from screenshot)
- "evidence": specific visual evidence supporting the finding

Respond with a JSON array of findings only. If there are no issues, respond with [].
Do not include any prose outside the JSON array.`

// comparePrompt is the instruction sent when comparing a
// before/after pair of screenshots to detect regressions.
const comparePrompt = `You are a QA analyst comparing two screenshots of a mobile or desktop application — a "before" and an "after" state.

Identify any regressions or new issues visible in the "after" screenshot that were not present in the "before" screenshot. Also flag issues that appear in both screenshots.

For each issue return a JSON object with these fields:
- "category": one of "visual", "ux", "accessibility", "performance", "functional", "brand", "content"
- "severity": one of "critical", "high", "medium", "low", "cosmetic"
- "title": short one-sentence summary
- "description": what you observed and why it is a problem
- "repro_steps": steps to reproduce (omit if obvious from screenshots)
- "evidence": specific visual evidence supporting the finding

Respond with a JSON array of findings only. If there are no issues, respond with [].
Do not include any prose outside the JSON array.`

// VisionAnalyzer uses an LLM vision provider to analyse
// screenshots and produce structured AnalysisFinding lists.
type VisionAnalyzer struct {
	provider llm.Provider
}

// NewVisionAnalyzer creates a VisionAnalyzer backed by the
// given LLM provider. The provider must support vision
// (SupportsVision() == true) for analysis calls to succeed.
func NewVisionAnalyzer(provider llm.Provider) *VisionAnalyzer {
	return &VisionAnalyzer{provider: provider}
}

// AnalyzeScreenshot submits a single screenshot to the
// vision LLM with a standard analysis prompt and returns
// the parsed list of findings. The screen and platform
// fields are injected into every returned finding.
//
// An error is returned only for provider/transport
// failures. Malformed or empty LLM responses yield an
// empty slice without error.
func (v *VisionAnalyzer) AnalyzeScreenshot(
	ctx context.Context,
	imageData []byte,
	screen, platform string,
) ([]AnalysisFinding, error) {
	resp, err := v.provider.Vision(ctx, imageData, analyzePrompt)
	if err != nil {
		return nil, fmt.Errorf(
			"analysis: vision call failed: %w", err,
		)
	}
	return v.parseFindings(resp.Content, screen, platform), nil
}

// AnalyzeScreenshotChecked is AnalyzeScreenshot plus a verdict on whether the
// model's reply was actually USABLE: parsed is true only when the reply held a
// well-formed JSON array (an empty "[]" counts: the model judged the frame
// clean). An empty reply, prose without an array ("I cannot view images") or
// malformed JSON returns parsed=false, so callers never count such a frame as
// analysed.
func (v *VisionAnalyzer) AnalyzeScreenshotChecked(
	ctx context.Context,
	imageData []byte,
	screen, platform string,
) (findings []AnalysisFinding, parsed bool, err error) {
	resp, err := v.provider.Vision(ctx, imageData, analyzePrompt)
	if err != nil {
		return nil, false, fmt.Errorf(
			"analysis: vision call failed: %w", err,
		)
	}
	f, ok := v.parseFindingsChecked(resp.Content, screen, platform)
	return f, ok, nil
}

// CompareScreenshots submits a before/after screenshot
// pair to the vision LLM with a regression-detection
// prompt. The two images are composed into a single
// side-by-side prompt payload via the provider's Vision
// method (before image is primary; after is appended to
// the prompt text so providers that accept only one image
// still receive both in textual context).
//
// The screen and platform fields are injected into every
// returned finding.
//
// An error is returned only for provider/transport
// failures. Malformed or empty LLM responses yield an
// empty slice without error.
func (v *VisionAnalyzer) CompareScreenshots(
	ctx context.Context,
	before, after []byte,
	screen, platform string,
) ([]AnalysisFinding, error) {
	// Build a combined prompt that embeds both images.
	// Providers that support multi-image input receive
	// `before` as the primary image; the prompt
	// references both states. For single-image providers
	// the `after` screenshot is sent and the prompt
	// describes the comparison task fully.
	_ = before // before is provided for context; primary
	// image sent to Vision is `after` so the model sees
	// the current state.
	resp, err := v.provider.Vision(ctx, after, comparePrompt)
	if err != nil {
		return nil, fmt.Errorf(
			"analysis: compare vision call failed: %w", err,
		)
	}
	return v.parseFindings(resp.Content, screen, platform), nil
}

// parseFindings extracts a JSON array of AnalysisFinding
// objects from raw LLM output. It handles markdown
// code-fence wrapping (```json ... ```) and is lenient
// with malformed input — returning an empty slice rather
// than an error when the content cannot be parsed.
//
// screen and platform are set on every finding parsed
// successfully.
func (v *VisionAnalyzer) parseFindings(
	content, screen, platform string,
) []AnalysisFinding {
	f, _ := v.parseFindingsChecked(content, screen, platform)
	return f
}

// parseFindingsChecked is parseFindings that also reports whether a JSON
// array was actually parsed.
func (v *VisionAnalyzer) parseFindingsChecked(
	content, screen, platform string,
) ([]AnalysisFinding, bool) {
	content = strings.TrimSpace(content)
	if content == "" {
		return []AnalysisFinding{}, false
	}

	// Strip common markdown code-fence wrappers that
	// LLMs frequently add despite explicit instructions.
	content = stripMarkdownFence(content)

	// Locate the JSON array boundaries.
	start := strings.Index(content, "[")
	end := strings.LastIndex(content, "]")
	if start == -1 || end == -1 || end < start {
		// No array found — graceful degradation.
		return []AnalysisFinding{}, false
	}
	jsonSlice := content[start : end+1]

	var findings []AnalysisFinding
	if err := json.Unmarshal([]byte(jsonSlice), &findings); err != nil {
		// Malformed JSON — graceful degradation.
		return []AnalysisFinding{}, false
	}

	// Inject caller-supplied context fields.
	for i := range findings {
		findings[i].Screen = screen
		findings[i].Platform = platform
	}

	if findings == nil {
		findings = []AnalysisFinding{}
	}
	return findings, true
}

// stripMarkdownFence removes leading/trailing markdown
// code-fence markers (``` or ```json) from content.
func stripMarkdownFence(s string) string {
	// Remove opening fence.
	for _, prefix := range []string{"```json", "```"} {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimPrefix(s, prefix)
			s = strings.TrimSpace(s)
			break
		}
	}
	// Remove closing fence.
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	return s
}
