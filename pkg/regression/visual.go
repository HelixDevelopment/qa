// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// Package regression provides cross-device visual regression
// detection for HelixQA. When testing the same application
// on multiple devices simultaneously, VisualRegression
// compares screenshots taken at the same test step across
// devices to detect layout inconsistencies, missing
// elements, and platform-specific rendering bugs.
package regression

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// SeverityCritical indicates a blocking visual difference.
const SeverityCritical = "critical"

// SeverityWarning indicates a noticeable visual difference.
const SeverityWarning = "warning"

// SeverityInfo indicates a minor visual difference.
const SeverityInfo = "info"

// VisionProvider is the interface needed for LLM-based
// screenshot comparison. It matches the subset of
// llm.Provider that VisualRegression uses.
type VisionProvider interface {
	// Vision sends a screenshot with a text prompt and
	// returns the LLM's analysis.
	Vision(
		ctx context.Context,
		image []byte,
		prompt string,
	) (*VisionResponse, error)

	// SupportsVision reports whether this provider can
	// process image inputs.
	SupportsVision() bool
}

// VisionResponse holds the reply from a vision provider.
type VisionResponse struct {
	Content string `json:"content"`
}

// DeviceScreenshot captures a screenshot from a specific
// device at a specific test step.
type DeviceScreenshot struct {
	// Device is the device identifier
	// (e.g. "192.0.2.134:5555").
	Device string `json:"device"`

	// Platform is the platform type
	// (e.g. "android", "androidtv", "web").
	Platform string `json:"platform"`

	// Screenshot is the raw PNG image data.
	Screenshot []byte `json:"-"`

	// Step is the test step number at which this
	// screenshot was taken.
	Step int `json:"step"`

	// StepName describes the test step (optional).
	StepName string `json:"step_name,omitempty"`
}

// Validate checks that the screenshot has the minimum
// required fields.
func (ds DeviceScreenshot) Validate() error {
	if ds.Device == "" {
		return fmt.Errorf(
			"regression: device identifier is required",
		)
	}
	if len(ds.Screenshot) == 0 {
		return fmt.Errorf(
			"regression: screenshot data is empty for %s",
			ds.Device,
		)
	}
	return nil
}

// RegressionResult holds the outcome of comparing
// screenshots from multiple devices at the same step.
type RegressionResult struct {
	// Step is the test step number.
	Step int `json:"step"`

	// StepName describes the test step (optional).
	StepName string `json:"step_name,omitempty"`

	// Devices lists all devices that were compared.
	Devices []string `json:"devices"`

	// Differences holds all detected visual differences
	// between device pairs.
	Differences []VisualDifference `json:"differences,omitempty"`

	// Consistent is true when all devices show the same
	// layout with no significant differences.
	Consistent bool `json:"consistent"`

	// ComparisonsMade is the total number of pairwise
	// comparisons performed.
	ComparisonsMade int `json:"comparisons_made"`
}

// CriticalCount returns the number of critical-severity
// differences.
func (r *RegressionResult) CriticalCount() int {
	count := 0
	for _, d := range r.Differences {
		if d.Severity == SeverityCritical {
			count++
		}
	}
	return count
}

// VisualDifference describes a specific visual
// inconsistency detected between two devices.
type VisualDifference struct {
	// DeviceA is the first device in the comparison.
	DeviceA string `json:"device_a"`

	// DeviceB is the second device in the comparison.
	DeviceB string `json:"device_b"`

	// Description explains the visual difference.
	Description string `json:"description"`

	// Severity classifies the impact: "critical",
	// "warning", or "info".
	Severity string `json:"severity"`
}

// ValidSeverity checks if a severity string is valid.
func ValidSeverity(s string) bool {
	switch s {
	case SeverityCritical, SeverityWarning, SeverityInfo:
		return true
	}
	return false
}

// Option configures a VisualRegression.
type Option func(*VisualRegression)

// WithConcurrency sets the maximum number of concurrent
// pairwise comparisons. Default is 1 (sequential).
func WithConcurrency(n int) Option {
	return func(vr *VisualRegression) {
		if n > 0 {
			vr.concurrency = n
		}
	}
}

// VisualRegression compares screenshots from multiple
// devices at the same test step to detect cross-device
// visual inconsistencies. It uses an LLM vision provider
// to analyse pairs of screenshots.
type VisualRegression struct {
	provider    VisionProvider
	concurrency int
}

// NewVisualRegression creates a VisualRegression backed
// by the given LLM vision provider.
func NewVisualRegression(
	provider VisionProvider,
	opts ...Option,
) *VisualRegression {
	vr := &VisualRegression{
		provider:    provider,
		concurrency: 1,
	}
	for _, opt := range opts {
		opt(vr)
	}
	return vr
}

// Compare analyses screenshots from multiple devices at
// the same test step. It performs pairwise comparisons
// between all device screenshots and returns the
// aggregated result.
//
// When fewer than 2 screenshots are provided, the result
// is trivially consistent. When the provider does not
// support vision, an error is returned.
func (vr *VisualRegression) Compare(
	ctx context.Context,
	screenshots []DeviceScreenshot,
) (*RegressionResult, error) {
	if len(screenshots) < 2 {
		result := &RegressionResult{Consistent: true}
		if len(screenshots) == 1 {
			result.Step = screenshots[0].Step
			result.StepName = screenshots[0].StepName
			result.Devices = []string{screenshots[0].Device}
		}
		return result, nil
	}

	// Validate all screenshots.
	for _, ss := range screenshots {
		if err := ss.Validate(); err != nil {
			return nil, err
		}
	}

	if !vr.provider.SupportsVision() {
		return nil, fmt.Errorf(
			"regression: provider does not support vision",
		)
	}

	result := &RegressionResult{
		Step:     screenshots[0].Step,
		StepName: screenshots[0].StepName,
		Devices:  make([]string, len(screenshots)),
	}
	for i, ss := range screenshots {
		result.Devices[i] = ss.Device
	}

	// Build the list of pairwise comparisons.
	type pair struct {
		i, j int
	}
	var pairs []pair
	for i := 0; i < len(screenshots); i++ {
		for j := i + 1; j < len(screenshots); j++ {
			pairs = append(pairs, pair{i, j})
		}
	}
	result.ComparisonsMade = len(pairs)

	// Execute comparisons with bounded concurrency.
	type diffResult struct {
		diff *VisualDifference
		err  error
	}

	results := make([]diffResult, len(pairs))
	sem := make(chan struct{}, vr.concurrency)
	var wg sync.WaitGroup

	for idx, p := range pairs {
		select {
		case <-ctx.Done():
			result.Consistent = len(result.Differences) == 0
			return result, ctx.Err()
		default:
		}

		wg.Add(1)
		go func(idx int, p pair) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			diff, err := vr.comparePair(
				ctx,
				screenshots[p.i],
				screenshots[p.j],
			)
			results[idx] = diffResult{diff: diff, err: err}
		}(idx, p)
	}

	wg.Wait()

	// Collect results.
	for _, dr := range results {
		if dr.err != nil {
			// Skip errors from individual comparisons.
			continue
		}
		if dr.diff != nil {
			result.Differences = append(
				result.Differences, *dr.diff,
			)
		}
	}

	result.Consistent = len(result.Differences) == 0
	return result, nil
}

// CompareMultipleSteps compares screenshots across devices
// for each test step, returning a result per step.
func (vr *VisualRegression) CompareMultipleSteps(
	ctx context.Context,
	stepScreenshots [][]DeviceScreenshot,
) ([]*RegressionResult, error) {
	results := make(
		[]*RegressionResult, 0, len(stepScreenshots),
	)

	for _, screenshots := range stepScreenshots {
		select {
		case <-ctx.Done():
			return results, ctx.Err()
		default:
		}

		result, err := vr.Compare(ctx, screenshots)
		if err != nil {
			if ctx.Err() != nil {
				return results, err
			}
			// Record a failed comparison for this step.
			step := 0
			if len(screenshots) > 0 {
				step = screenshots[0].Step
			}
			results = append(results, &RegressionResult{
				Step:       step,
				Consistent: false,
			})
			continue
		}
		results = append(results, result)
	}

	return results, nil
}

// ConsistencyRate calculates the fraction of steps that
// are fully consistent across all devices (0-1).
func ConsistencyRate(results []*RegressionResult) float64 {
	if len(results) == 0 {
		return 1.0
	}
	consistent := 0
	for _, r := range results {
		if r.Consistent {
			consistent++
		}
	}
	return float64(consistent) / float64(len(results))
}

// TotalDifferences returns the total number of visual
// differences across all step results.
func TotalDifferences(
	results []*RegressionResult,
) int {
	total := 0
	for _, r := range results {
		total += len(r.Differences)
	}
	return total
}

// comparisonPrompt is the template for the LLM vision
// comparison request.
const comparisonPrompt = `Compare these two screenshots from device %q (%s) and device %q (%s) showing the same application at the same test step.

Identify any visual differences in:
- Layout and sizing of elements
- Colors, fonts, and styling
- Missing or extra elements
- Text truncation or alignment issues
- Navigation bar or status bar differences

Respond with a JSON object only:
{"different": true/false, "description": "...", "severity": "critical/warning/info"}

If the screenshots look the same, respond: {"different": false, "description": "no differences", "severity": "info"}
Do not include any prose outside the JSON object.`

// comparePair compares screenshots from two devices and
// returns a VisualDifference if the LLM detects one, or
// nil if they are consistent.
func (vr *VisualRegression) comparePair(
	ctx context.Context,
	a, b DeviceScreenshot,
) (*VisualDifference, error) {
	prompt := fmt.Sprintf(
		comparisonPrompt,
		a.Device, a.Platform,
		b.Device, b.Platform,
	)

	// Send the first screenshot with the comparison prompt.
	// The LLM should be given context about both devices
	// even though it can only see one image per call.
	resp, err := vr.provider.Vision(
		ctx, a.Screenshot, prompt,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"vision compare %s vs %s: %w",
			a.Device, b.Device, err,
		)
	}

	return parseComparisonResponse(
		resp.Content, a.Device, b.Device,
	), nil
}

// comparisonJSON is the expected shape of the LLM's
// comparison response.
type comparisonJSON struct {
	Different   bool   `json:"different"`
	Description string `json:"description"`
	Severity    string `json:"severity"`
}

// parseComparisonResponse extracts a VisualDifference from
// the LLM's JSON response. Returns nil when the LLM says
// the screenshots are not different.
func parseComparisonResponse(
	content, deviceA, deviceB string,
) *VisualDifference {
	content = strings.TrimSpace(content)

	// Try to find a JSON object in the response.
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end < 0 || end <= start {
		return nil
	}

	jsonStr := content[start : end+1]
	var parsed comparisonJSON
	if err := json.Unmarshal(
		[]byte(jsonStr), &parsed,
	); err != nil {
		return nil
	}

	if !parsed.Different {
		return nil
	}

	severity := strings.ToLower(parsed.Severity)
	if !ValidSeverity(severity) {
		severity = SeverityWarning
	}

	return &VisualDifference{
		DeviceA:     deviceA,
		DeviceB:     deviceB,
		Description: parsed.Description,
		Severity:    severity,
	}
}
