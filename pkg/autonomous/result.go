// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"time"

	"digital.vasic.helixqa/pkg/issuedetector"
	"digital.vasic.helixqa/pkg/session"
)

// SessionStatus describes the current state of a session.
type SessionStatus string

const (
	StatusIdle     SessionStatus = "idle"
	StatusRunning  SessionStatus = "running"
	StatusPaused   SessionStatus = "paused"
	StatusComplete SessionStatus = "complete"
	StatusFailed   SessionStatus = "failed"
	StatusCanceled SessionStatus = "canceled"
	// StatusBlocked: the session stopped for an environment reason (network
	// / VPN preflight, provider outage, timeout) — could not determine, never
	// a FAILED verdict about the app.
	StatusBlocked SessionStatus = "blocked"
)

// SessionResult captures the complete outcome of an autonomous
// QA session.
type SessionResult struct {
	// SessionID is the unique session identifier.
	SessionID string `json:"session_id"`

	// Status is the final session status.
	Status SessionStatus `json:"status"`

	// StartTime is when the session started.
	StartTime time.Time `json:"start_time"`

	// EndTime is when the session ended.
	EndTime time.Time `json:"end_time"`

	// Duration is the total session time.
	Duration time.Duration `json:"duration"`

	// Phases holds the phase completion details.
	Phases []Phase `json:"phases"`

	// PlatformResults holds per-platform outcomes.
	PlatformResults map[string]*PlatformResult `json:"platform_results"`

	// Issues holds all detected issues.
	Issues []issuedetector.Issue `json:"issues"`

	// Timeline holds all timeline events.
	Timeline []session.TimelineEvent `json:"timeline"`

	// CoverageOverall is the overall feature coverage.
	CoverageOverall float64 `json:"coverage_overall"`

	// Error holds any fatal error.
	Error string `json:"error,omitempty"`
}

// PlatformResult captures the outcome for a single platform.
type PlatformResult struct {
	// Platform identifier.
	Platform string `json:"platform"`

	// FeaturesVerified is how many features were verified.
	FeaturesVerified int `json:"features_verified"`

	// FeaturesFailed is how many features failed verification.
	FeaturesFailed int `json:"features_failed"`

	// IssuesFound is the total issues detected.
	IssuesFound int `json:"issues_found"`

	// ScreensDiscovered is how many unique screens were found.
	ScreensDiscovered int `json:"screens_discovered"`

	// Coverage is the navigation coverage (0-1).
	Coverage float64 `json:"coverage"`

	// Duration is the platform testing time.
	Duration time.Duration `json:"duration"`

	// Error holds any platform-specific error.
	Error string `json:"error,omitempty"`
}

// StepResult describes the outcome of a single test step
// within a platform worker.
type StepResult struct {
	// FeatureID links to the feature being tested.
	FeatureID string `json:"feature_id"`

	// StepIndex is the step number within the feature.
	StepIndex int `json:"step_index"`

	// Action describes what was done.
	Action string `json:"action"`

	// Success indicates if the step passed.
	Success bool `json:"success"`

	// Error message if the step failed.
	Error string `json:"error,omitempty"`

	// Duration of the step execution.
	Duration time.Duration `json:"duration"`

	// ScreenBefore is the pre-action screen ID.
	ScreenBefore string `json:"screen_before,omitempty"`

	// ScreenAfter is the post-action screen ID.
	ScreenAfter string `json:"screen_after,omitempty"`
}

// ProgressReport provides a real-time view of session progress.
type ProgressReport struct {
	// SessionID is the session identifier.
	SessionID string `json:"session_id"`

	// Status is the current session status.
	Status SessionStatus `json:"status"`

	// CurrentPhase is the active phase name.
	CurrentPhase string `json:"current_phase"`

	// PhaseProgress is the active phase progress (0-1).
	PhaseProgress float64 `json:"phase_progress"`

	// OverallProgress is the session-wide progress (0-1).
	OverallProgress float64 `json:"overall_progress"`

	// PlatformStatus maps platform to current status.
	PlatformStatus map[string]string `json:"platform_status"`

	// IssuesFound is the running total of issues.
	IssuesFound int `json:"issues_found"`

	// Elapsed is the time since session start.
	Elapsed time.Duration `json:"elapsed"`
}
