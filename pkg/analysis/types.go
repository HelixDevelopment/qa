// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// Package analysis provides LLM-powered screenshot analysis
// types and utilities for the HelixQA autonomous agent.
// It defines the finding taxonomy, severity levels, and
// structured report types used across all vision analysis.
package analysis

// FindingCategory classifies the type of issue observed
// in a screenshot analysis.
type FindingCategory string

const (
	// CategoryVisual covers rendering defects, layout
	// breaks, misaligned elements, and visual glitches.
	CategoryVisual FindingCategory = "visual"

	// CategoryUX covers usability problems such as
	// unclear affordances, confusing navigation, or
	// poor interaction design.
	CategoryUX FindingCategory = "ux"

	// CategoryAccessibility covers contrast failures,
	// missing labels, touch target sizes, and other
	// accessibility barriers.
	CategoryAccessibility FindingCategory = "accessibility"

	// CategoryPerformance covers visible symptoms of
	// poor performance: jank, frozen frames, loading
	// spinners that never resolve, etc.
	CategoryPerformance FindingCategory = "performance"

	// CategoryFunctional covers broken or incorrect
	// behaviour: wrong data displayed, controls that
	// do not respond, missing features.
	CategoryFunctional FindingCategory = "functional"

	// CategoryBrand covers deviations from brand
	// guidelines: wrong colours, wrong fonts, or
	// incorrect logo usage.
	CategoryBrand FindingCategory = "brand"

	// CategoryContent covers copy errors, truncated
	// text, placeholder strings, or missing
	// localisation.
	CategoryContent FindingCategory = "content"

	// CategoryEnvironment records that the QA run itself could not judge
	// something (e.g. a vision-gated step BLOCKED because no vision
	// provider answered). It is NOT a defect of the app under test: it is
	// never sent to bug reproduction, never ticketed as a bug, and not
	// counted as an issue found.
	CategoryEnvironment FindingCategory = "environment"
)

// IsAppDefect reports whether a finding of this category claims a defect
// in the application under test.
func (c FindingCategory) IsAppDefect() bool {
	return c != CategoryEnvironment
}

// FindingSeverity describes how serious a finding is and
// how urgently it should be addressed.
type FindingSeverity string

const (
	// SeverityCritical is reserved for issues that
	// completely block users or cause crashes/data loss.
	SeverityCritical FindingSeverity = "critical"

	// SeverityHigh indicates a significant problem that
	// degrades the experience for most users.
	SeverityHigh FindingSeverity = "high"

	// SeverityMedium indicates a noticeable issue that
	// should be fixed but is not blocking.
	SeverityMedium FindingSeverity = "medium"

	// SeverityLow indicates a minor issue that can be
	// addressed in a normal sprint cycle.
	SeverityLow FindingSeverity = "low"

	// SeverityCosmetic indicates a purely aesthetic
	// issue with no functional impact.
	SeverityCosmetic FindingSeverity = "cosmetic"
)

// AnalysisFinding is a single issue identified by the
// LLM vision analysis of a screenshot.
type AnalysisFinding struct {
	// Category classifies the type of issue.
	Category FindingCategory `json:"category"`

	// Severity describes the impact of the issue.
	Severity FindingSeverity `json:"severity"`

	// Title is a short, human-readable summary of the
	// finding (one sentence).
	Title string `json:"title"`

	// Description explains what was observed and why
	// it is a problem.
	Description string `json:"description"`

	// ReproSteps lists the steps needed to reproduce
	// the issue (optional; may be empty for visual
	// findings apparent from the screenshot alone).
	ReproSteps string `json:"repro_steps,omitempty"`

	// Evidence is a verbatim excerpt or description
	// of the visual evidence supporting this finding.
	Evidence string `json:"evidence,omitempty"`

	// Platform identifies the target platform
	// (e.g. "android", "androidtv", "web", "desktop").
	// Set automatically by the analyser from call
	// arguments; not expected in the LLM response.
	Platform string `json:"platform,omitempty"`

	// Screen identifies the screen or view that was
	// analysed (e.g. "home", "login", "settings").
	// Set automatically by the analyser from call
	// arguments; not expected in the LLM response.
	Screen string `json:"screen,omitempty"`

	// AcceptanceCriteria defines the specific,
	// measurable conditions that must be met for this
	// finding to be considered resolved. Required for
	// every ticket to ensure fix validation.
	AcceptanceCriteria string `json:"acceptance_criteria,omitempty"`
}

// AnalysisReport aggregates all findings from a QA
// session's screenshot analysis passes.
type AnalysisReport struct {
	// SessionID links the report to the originating
	// HelixQA session.
	SessionID string `json:"session_id"`

	// Summary is a human-readable overview written by
	// the LLM (or composed from findings).
	Summary string `json:"summary"`

	// TotalAnalyzed is the number of screenshots that
	// were submitted for analysis.
	TotalAnalyzed int `json:"total_analyzed"`

	// Findings is the ordered list of issues found
	// across all analysed screenshots.
	Findings []AnalysisFinding `json:"findings"`
}

// BySeverity returns all findings whose Severity matches
// the given value. The original order is preserved.
func (r *AnalysisReport) BySeverity(
	s FindingSeverity,
) []AnalysisFinding {
	var out []AnalysisFinding
	for _, f := range r.Findings {
		if f.Severity == s {
			out = append(out, f)
		}
	}
	return out
}

// CriticalCount returns the total number of findings with
// SeverityCritical or SeverityHigh severity.
func (r *AnalysisReport) CriticalCount() int {
	count := 0
	for _, f := range r.Findings {
		if f.Severity == SeverityCritical ||
			f.Severity == SeverityHigh {
			count++
		}
	}
	return count
}
