// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// Package testbank provides QA-specific test bank management
// with YAML-based test case definitions. It bridges to the
// Challenges framework's bank package for execution while
// adding QA metadata like platform targeting, priority, and
// documentation references.
package testbank

import (
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"digital.vasic.challenges/pkg/challenge"
	"digital.vasic.helixqa/pkg/config"
)

// Priority levels for test cases.
type Priority string

const (
	PriorityCritical Priority = "critical"
	PriorityHigh     Priority = "high"
	PriorityMedium   Priority = "medium"
	PriorityLow      Priority = "low"
)

// TestCase is a QA-specific test definition that extends the
// Challenges Definition with platform, priority, and
// documentation references.
type TestCase struct {
	// ID uniquely identifies the test case.
	ID string `yaml:"id" json:"id"`

	// Name is the human-readable test name.
	Name string `yaml:"name" json:"name"`

	// Description explains what this test validates.
	Description string `yaml:"description" json:"description"`

	// Category groups related tests (e.g., "functional",
	// "edge_case", "integration", "security").
	Category string `yaml:"category" json:"category"`

	// Priority indicates test importance for scheduling.
	Priority Priority `yaml:"priority" json:"priority"`

	// Platforms specifies which platforms this test targets.
	// Empty means all platforms.
	Platforms []config.Platform `yaml:"platforms" json:"platforms"`

	// Steps lists the ordered test steps to execute.
	Steps []TestStep `yaml:"steps" json:"steps"`

	// Dependencies lists test case IDs that must pass first.
	Dependencies []string `yaml:"dependencies" json:"dependencies"`

	// DocumentationRefs links to relevant docs for
	// inconsistency detection.
	DocumentationRefs []DocRef `yaml:"documentation_refs" json:"documentation_refs"`

	// Tags provides free-form labels for filtering.
	Tags []string `yaml:"tags" json:"tags"`

	// EstimatedDuration is the expected execution time.
	EstimatedDuration string `yaml:"estimated_duration" json:"estimated_duration"`

	// ExpectedResult describes the expected outcome.
	ExpectedResult string `yaml:"expected_result" json:"expected_result"`

	// AllowForegroundLeave opts this test out of the structured-phase
	// foreground-drift guard. Set to true for tests that intentionally
	// exercise system overlays or the launcher (voice search, tv
	// channels, watch-next-row), where drifting to `ihq`, Google
	// Katniss, IPTV Pro, RuTube, etc. is the test subject, not a
	// bug. Defaults to false — tests MUST stay in the target app.
	AllowForegroundLeave bool `yaml:"allow_foreground_leave,omitempty" json:"allow_foreground_leave,omitempty"`

	// RequiresEnv lists environment variables that MUST be set for
	// this test case to run. If any are unset or empty, the executor
	// SKIP-OKs the entire test case rather than failing — Article XI
	// §11.5 demands honest skips for env-dependent tests, not silent
	// fails (or worse, silent passes).
	//
	// Examples:
	//   requires_env: [HELIXQA_LAB_HAS_4K_MEDIA]   // for 4K-on-1080p tests
	//   requires_env: [HELIXQA_LAB_HAS_USB_KBD]    // for external-keyboard tests
	//   requires_env: [HELIXQA_LAB_HAS_SOUNDBAR]   // for audio-delay tests
	//   requires_env: [HELIXQA_LAB_FULL_HARDWARE]  // generic catch-all
	//
	// Operators set the var(s) in their .env (or helix_qa/.env) when
	// the corresponding hardware is available. Lab installs without
	// the hardware see honest SKIP-OK results — anti-bluff compliant.
	RequiresEnv []string `yaml:"requires_env,omitempty" json:"requires_env,omitempty"`

	// ChallengeID is a stable Challenge identifier the consuming
	// project assigns so a bank case can be cross-referenced from a
	// HelixQA Challenge run, a tracker ticket, or a conduit verdict
	// without relying on the (mutable) Name. When empty, ID is used.
	// Project-agnostic: the value is consumer data (any string).
	ChallengeID string `yaml:"challenge_id,omitempty" json:"challenge_id,omitempty"`

	// DispatchesTo names a consumer-owned on-device / host test
	// script (or any executable command line) that IS the body of
	// this Challenge. When set, the dispatch executor runs it via the
	// consumer-injected DeviceExec hook; its real exit code drives the
	// PASS/FAIL verdict (extends ActionTypeShell to the case level).
	// Project-agnostic: HelixQA never interprets the path — the
	// consumer supplies and resolves it (e.g. a `test_*.sh` path).
	DispatchesTo string `yaml:"dispatches_to,omitempty" json:"dispatches_to,omitempty"`

	// Domains tags this case with one or more area labels (e.g.
	// "audio", "video", "display") so a domain-scoped runner can
	// filter and a single-resource-owner session can partition work.
	// Project-agnostic: the labels are opaque strings to HelixQA.
	Domains []string `yaml:"domains,omitempty" json:"domains,omitempty"`

	// RequiredEvidence lists evidence-artefact globs/keys that MUST
	// each resolve to a real, non-empty artefact before this case may
	// score PASS (the §11.4.69 evidence-ledger gate). A glob may match
	// a captured file, or a key may name a conduit evidence_captured
	// event that carried a non-empty EvidencePath. Missing evidence →
	// FAIL with the missing list. Project-agnostic: the token
	// vocabulary is entirely consumer data.
	RequiredEvidence []string `yaml:"required_evidence,omitempty" json:"required_evidence,omitempty"`

	// Metadata holds arbitrary per-case key-value data the CONSUMER
	// attaches and the CONSUMER interprets — HelixQA never reads into
	// it (CONST-051(B) / §11.4.28). It is the structured, schema-
	// agnostic companion to DispatchesTo: where DispatchesTo is an
	// opaque command STRING, Metadata lets a case carry the same
	// options as DATA a conductor wrapper can read without parsing a
	// flag string. Example: a recording-validation case carries
	// `metadata.recvalidate_options.{reply_markers,chrome_line_patterns,
	// expected_replies}` that the wrapper maps onto a
	// pkg/recordingqa.Spec (forwarded to the Panoptic recvalidate video
	// oracle). The loader preserves it verbatim; nothing in HelixQA
	// branches on its contents.
	Metadata map[string]any `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}

// EffectiveChallengeID returns ChallengeID when set, otherwise the
// case ID — so conduit verdicts and tracker cross-references always
// have a stable handle.
func (tc *TestCase) EffectiveChallengeID() string {
	if tc.ChallengeID != "" {
		return tc.ChallengeID
	}
	return tc.ID
}

// HasDomain reports whether this case is tagged with the given domain
// label (case-insensitive). Used by domain-scoped dispatch.
func (tc *TestCase) HasDomain(domain string) bool {
	for _, d := range tc.Domains {
		if strings.EqualFold(d, domain) {
			return true
		}
	}
	return false
}

// ActionType identifies the type of action to execute.
type ActionType string

const (
	// ActionTypeDescription is a text-only action (legacy, non-executable).
	ActionTypeDescription ActionType = "description"
	// ActionTypeADBShell executes an ADB shell command.
	ActionTypeADBShell ActionType = "adb_shell"
	// ActionTypeShell executes a host shell command via os/exec on
	// the machine running the runner. Used for desktop-platform bank
	// cases (and any non-Android topology) so a bank case's
	// `action: "shell: <command>"` is genuinely run — the real exit
	// code drives the PASS/FAIL verdict. Closes HXC-011: before this
	// action type existed, desktop-platform bank cases were loaded
	// but their actions were never executed, producing hollow
	// metadata-only PASS/SKIP rows (a §11.4 / CONST-035 PASS-bluff
	// in the QA runner itself).
	ActionTypeShell ActionType = "shell"
	// ActionTypeSleep waits for a specified duration.
	ActionTypeSleep ActionType = "sleep"
	// ActionTypeScreenshot captures a screenshot.
	ActionTypeScreenshot ActionType = "screenshot"
	// ActionTypeKeyPress simulates a key press.
	ActionTypeKeyPress ActionType = "keypress"
	// ActionTypeTap taps at coordinates.
	ActionTypeTap ActionType = "tap"
	// ActionTypeSwipe performs a swipe gesture.
	ActionTypeSwipe ActionType = "swipe"
	// ActionTypeText enters text.
	ActionTypeText ActionType = "text"
	// ActionTypePlaybackCheck queries Android `dumpsys media_session`
	// and verifies that at least one media session for the given
	// package (or any package if the value is "*") is in the
	// PlaybackState PLAYING (state=3). Used to confirm a test case
	// that pressed a Play button actually caused playback to start.
	// Value format: "<package>" or "<package>:<minState>" where
	// minState is the minimum PlaybackState integer to accept
	// (default 3 = PLAYING).
	ActionTypePlaybackCheck ActionType = "playback_check"
	// ActionTypeFrameDiff captures a screenshot, waits the given
	// number of milliseconds, captures a second screenshot, and
	// returns success if the two frames differ by more than the
	// similarity threshold. Used to confirm video playback is
	// actually rendering (not a frozen first frame). Value format:
	// "<waitMs>" — defaults to 2000 ms.
	ActionTypeFrameDiff ActionType = "frame_diff"
	// ActionTypeHTTP performs an HTTP request against a backend
	// API and asserts on the response. Value format:
	// "<METHOD> <PATH>" e.g. "GET /api/v1/health" or
	// "POST /api/v1/auth/login". Body, headers, expected status,
	// and JSON-path assertions are supplied via the dedicated
	// TestStep fields (Body, Headers, ExpectStatus,
	// ExpectJSONPath, ExpectBodyContains, AuthMode). The base URL
	// comes from the executor's HTTPBaseURL config.
	//
	// Added 2026-04-29 to close the BLUFF-HELIXQA-BANKS-REWRITE-001
	// gap: prior banks (full-qa-api, full-qa-web, atmosphere) used
	// prose ActionTypeDescription strings that the executor could
	// not run, producing 4034 PROSE_HELIXQA_ACTION findings in the
	// bluff audit. With this type, those banks become structurally
	// executable.
	ActionTypeHTTP ActionType = "http"
	// ActionTypeAssert evaluates a structured assertion against
	// the most recent HTTP response, captured screenshot, or
	// device state. Value format: "<kind>: <expr>" — kinds are
	// status_eq, json_path_eq, body_contains, header_eq.
	// Used in tandem with ActionTypeHTTP for complex assertions
	// that span multiple checks; simpler one-shot expectations
	// can ride on the HTTP step's ExpectStatus / ExpectJSONPath
	// / ExpectBodyContains fields directly.
	ActionTypeAssert ActionType = "assert"
	// ActionTypePlaywright drives a web browser via the Playwright
	// adapter for deterministic UI test bank steps. Value format:
	//   "<verb> <selector|target>"
	// where verb is one of:
	//   navigate <url>            — open a URL
	//   click    <selector>       — click an element
	//   fill     <selector> <txt> — type into an input
	//   waitFor  <selector>       — wait for element
	//   assertVisible    <selector>
	//   assertNotVisible <selector>
	//   press    <key>            — keyboard press (e.g. Enter)
	//
	// Selectors are Playwright selectors (CSS, text=, role=, etc).
	//
	// Added 2026-04-29 for BLUFF-HELIXQA-BANKS-REWRITE-001 step 3
	// to bring full-qa-web.json (572 prose entries) under the
	// structured-action umbrella. The current executor stub
	// records the step as structurally valid but needs the
	// Playwright runtime wired in via PlaywrightCLIAdapter from
	// the Challenges submodule (separate PR — banks are
	// converted ahead of the runtime so a single integration
	// commit closes the gap). Until then ActionTypePlaywright
	// steps SKIP with PLAYWRIGHT-RUNTIME-PENDING rather than
	// false-PASS, keeping Article XI compliance.
	ActionTypePlaywright ActionType = "playwright"
)

// IsNonScreen reports actions whose verdict is their own deterministic
// check (exit code, HTTP response) rather than anything on the screen. A
// screenshot says nothing about them: vision keys are refused on them at
// load, and a non-gated one never consults a vision model.
func (a ActionType) IsNonScreen() bool {
	switch a {
	case ActionTypeShell, ActionTypeHTTP, ActionTypeAssert:
		return true
	}
	return false
}

// TestStep is a single step within a test case.
type TestStep struct {
	// Name identifies this step.
	Name string `yaml:"name" json:"name"`

	// Action describes what to do.
	// For executable actions, use format: "type: value"
	// Examples:
	//   "adb_shell: input keyevent KEYCODE_ENTER"
	//   "sleep: 5000" (milliseconds)
	//   "screenshot"
	//   "keypress: KEYCODE_DPAD_DOWN"
	//   "text: admin"
	Action string `yaml:"action" json:"action"`

	// Expected describes the expected outcome.
	Expected string `yaml:"expected" json:"expected"`

	// Platform limits this step to a specific platform.
	// Empty means it applies to all.
	Platform config.Platform `yaml:"platform,omitempty" json:"platform,omitempty"`

	// Timeout is the maximum time to wait for this step (in seconds).
	// Default is 30 seconds.
	Timeout int `yaml:"timeout,omitempty" json:"timeout,omitempty"`

	// VisionVerify makes this step VISION-GATED: it can PASS only when a
	// vision model positively confirms the expected state on the exact
	// after-action frame. No provider, a provider error, an ambiguous or
	// empty reply, or a blank/missing frame make the step BLOCKED
	// (could-not-determine) — never PASS. Without it, a step's vision
	// result is advisory and is reported UNVERIFIED-by-vision when the
	// model could not confirm it.
	VisionVerify bool `yaml:"vision_verify,omitempty" json:"vision_verify,omitempty"`

	// Golden names a reference image (<id>.png / .jpg / .jpeg) in the
	// goldens directory (PipelineConfig.VisionGoldensDir or
	// HELIXQA_GOLDENS_DIR). The after-action frame's pHash must be within
	// PHashMaxDistance of the golden's pHash. A deterministic oracle — it
	// needs no vision model. Setting it gates the step.
	Golden string `yaml:"golden,omitempty" json:"golden,omitempty"`

	// PHashMaxDistance is the largest Hamming distance (0..16, see
	// MaxPHashMaxDistance) between the
	// frame's and the golden's 64-bit pHash that still counts as a match.
	// Only valid together with Golden; nil means DefaultPHashMaxDistance.
	PHashMaxDistance *int `yaml:"phash_max_distance,omitempty" json:"phash_max_distance,omitempty"`

	// Recognize lists tokens that MUST appear, as whole words after
	// normalisation (lower-case, punctuation folded to spaces), within ONE
	// element of the "texts" array of an expectation-BLIND JSON reading of the frame by
	// the vision model — never in its "description" or any prose. The
	// tokens are never sent to the model; "no text" filler tokens are
	// refused. Setting it gates the step and requires a vision model.
	Recognize []string `yaml:"recognize,omitempty" json:"recognize,omitempty"`

	// RecognizeExact states the DEFAULT explicitly: each recognize token
	// must EQUAL one whole "texts" element (after normalisation), not just
	// appear inside one — so commentary such as "(no Login button visible)"
	// cannot satisfy recognize [Login]. Only valid together with Recognize;
	// contradicts RecognizeContains.
	RecognizeExact bool `yaml:"recognize_exact,omitempty" json:"recognize_exact,omitempty"`

	// RecognizeContains opts OUT of exact-element matching: a token then
	// matches when it appears as whole words INSIDE one element. Negations
	// and commentary inside an element ("Login not visible") then satisfy
	// recognize — use it only for tokens that are part of longer on-screen
	// strings. Only valid together with Recognize.
	RecognizeContains bool `yaml:"recognize_contains,omitempty" json:"recognize_contains,omitempty"`

	// Forbid lists tokens that MUST NOT appear in any "texts" element
	// (e.g. an error banner; a "description" saying "no error is visible"
	// does not count). Same matching rules as Recognize; it is
	// negative evidence only, so it must be paired with vision_verify,
	// recognize or golden. Setting it gates the step.
	Forbid []string `yaml:"forbid,omitempty" json:"forbid,omitempty"`

	// ExpectChange declares that the action must visibly change the
	// screen: an after-frame that does not differ from the before-frame
	// FAILS the step; an unusable before-frame, or frames of different
	// dimensions, make it BLOCKED. "Something changed" is not evidence of
	// the expected screen, so it must be paired with vision_verify,
	// recognize or golden. Setting it gates the step.
	ExpectChange bool `yaml:"expect_change,omitempty" json:"expect_change,omitempty"`

	// Body is the HTTP request body for ActionTypeHTTP. Strings
	// are sent verbatim; map/slice values are JSON-encoded.
	Body any `yaml:"body,omitempty" json:"body,omitempty"`

	// Headers carries extra HTTP headers for ActionTypeHTTP.
	// Common defaults (Accept, Content-Type for JSON bodies) are
	// added by the executor unless overridden here.
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`

	// AuthMode selects how authentication is applied to an
	// ActionTypeHTTP step. Recognized values:
	//   "none"      — no Authorization header (default)
	//   "admin"     — log in as admin once per session, attach
	//                 Bearer <session_token>
	//   "as:<user>" — log in as the named user (creds resolved
	//                 from the executor's UserCredentials map)
	//   "raw:<token>" — attach Bearer <token> verbatim
	AuthMode string `yaml:"auth,omitempty" json:"auth,omitempty"`

	// ExpectStatus is the expected HTTP status code (e.g. 200,
	// 201, 401). Zero means "do not check status".
	ExpectStatus int `yaml:"expect_status,omitempty" json:"expect_status,omitempty"`

	// ExpectJSONPath is a single JSON-path expression that must
	// resolve to a non-null value in the HTTP response body.
	// Example: "$.session_token". Empty means "do not check".
	ExpectJSONPath string `yaml:"expect_json_path,omitempty" json:"expect_json_path,omitempty"`

	// ExpectBodyContains is a substring that must appear in the
	// HTTP response body (case-sensitive). Empty means
	// "do not check".
	ExpectBodyContains string `yaml:"expect_body_contains,omitempty" json:"expect_body_contains,omitempty"`

	// ExpectBodyNotContains is a substring that must NOT appear in
	// the HTTP response body. It is a pointer so an explicitly empty
	// value is distinguishable from an absent one: "" would match
	// every body and make the step unable to pass, so it is refused
	// at load time (TestCase.IsValid) and again at run time. nil
	// means "do not check".
	//
	// MATCHING IS RAW: the substring is searched in the response
	// bytes exactly as received — case-SENSITIVE, and with NO
	// decoding of JSON \uXXXX escapes. A forbidden value that the
	// server may emit upper-cased, or escaped (any non-ASCII text),
	// can therefore slip past this assertion; an ASCII identifier
	// such as a ULID cannot, because JSON encoders do not escape it.
	//
	// Two load-time rules (TestCase.IsValid) keep it from passing
	// vacuously: it is accepted ONLY on an `http:` action (no other
	// executor reads it), and the step must also carry a POSITIVE
	// assertion — expect_status, expect_body_contains or
	// expect_json_path — because a negative assertion alone passes on
	// a 500, an empty body or an error envelope.
	ExpectBodyNotContains *string `yaml:"expect_body_not_contains,omitempty" json:"expect_body_not_contains,omitempty"`

	// ExpectExit is the exit code a `shell:` step's command must return.
	// It is a pointer so an explicit 0 is distinguishable from an absent
	// key: nil means "no explicit expectation", which keeps the established
	// shell verdict (the command must exit 0). A non-zero value turns a
	// failing command into the asserted outcome (e.g. a negative probe that
	// must exit 2). A signal death reports -1 and therefore never matches.
	//
	// Accepted ONLY on a `shell:` action (TestCase.IsValid): it is the only
	// action kind whose executors yield an exit code, so on any other kind
	// the assertion would be silently ignored. Valid range 0..255.
	ExpectExit *int `yaml:"expect_exit,omitempty" json:"expect_exit,omitempty"`

	// ExpectOutputContains is a substring that must appear in the
	// COMBINED stdout+stderr of a `shell:` step's command (case-sensitive,
	// raw bytes). It is a pointer so an explicitly empty value is refused
	// at load time (an empty substring matches everything, so the step
	// could never fail) and again at run time. Accepted ONLY on a `shell:`
	// action.
	ExpectOutputContains *string `yaml:"expect_output_contains,omitempty" json:"expect_output_contains,omitempty"`

	// Skip, when true, causes the runner to mark this step
	// SKIPPED with the reason in SkipReason instead of executing
	// it. Article XI §11.5: an explicit, reasoned skip is
	// strictly more honest than a test that runs and reports a
	// confusing PASS or FAIL because of bank-side limitations
	// (missing fixture data, converter limitations, destructive
	// side-effects on a shared environment).
	Skip bool `yaml:"_skip,omitempty" json:"_skip,omitempty"`

	// SkipReason carries the human-readable explanation for the
	// skip. Required when Skip is true.
	SkipReason string `yaml:"_skip_reason,omitempty" json:"_skip_reason,omitempty"`
}

// ParseAction parses the action string and returns the type and value.
// Format: "type: value" or just "description" for legacy text actions.
func (ts *TestStep) ParseAction() (ActionType, string) {
	if ts.Action == "" {
		return ActionTypeDescription, ""
	}

	// Handle standalone "screenshot" keyword (no colon needed)
	trimmed := strings.TrimSpace(ts.Action)
	if strings.EqualFold(trimmed, "screenshot") {
		return ActionTypeScreenshot, ""
	}

	// Check for explicit type prefix
	if idx := strings.Index(ts.Action, ":"); idx > 0 {
		prefix := ts.Action[:idx]
		value := strings.TrimSpace(ts.Action[idx+1:])

		switch ActionType(prefix) {
		case ActionTypeADBShell:
			return ActionTypeADBShell, value
		case ActionTypeShell:
			return ActionTypeShell, value
		case ActionTypeSleep:
			return ActionTypeSleep, value
		case ActionTypeScreenshot:
			return ActionTypeScreenshot, ""
		case ActionTypeKeyPress:
			return ActionTypeKeyPress, value
		case ActionTypeTap:
			return ActionTypeTap, value
		case ActionTypeSwipe:
			return ActionTypeSwipe, value
		case ActionTypeText:
			return ActionTypeText, value
		case ActionTypePlaybackCheck:
			return ActionTypePlaybackCheck, value
		case ActionTypeFrameDiff:
			return ActionTypeFrameDiff, value
		case ActionTypeHTTP:
			return ActionTypeHTTP, value
		case ActionTypeAssert:
			return ActionTypeAssert, value
		case ActionTypePlaywright:
			return ActionTypePlaywright, value
		}
	}

	// Legacy: treat as description
	return ActionTypeDescription, ts.Action
}

// DocRef references a documentation source for consistency
// verification.
type DocRef struct {
	// Type is the doc type (e.g., "user_guide", "api_spec",
	// "video_course", "architecture").
	Type string `yaml:"type" json:"type"`

	// Section is the specific section or page reference.
	Section string `yaml:"section" json:"section"`

	// Path is the file path or URL.
	Path string `yaml:"path,omitempty" json:"path,omitempty"`
}

// BankFile represents the YAML structure of a test bank file.
type BankFile struct {
	// Version is the bank file format version.
	Version string `yaml:"version" json:"version"`

	// Name identifies this bank.
	Name string `yaml:"name" json:"name"`

	// Description explains the bank's purpose.
	Description string `yaml:"description" json:"description"`

	// TestCases holds all test cases in this bank.
	TestCases []TestCase `yaml:"test_cases" json:"test_cases"`

	// Metadata holds arbitrary key-value pairs.
	Metadata map[string]any `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}

// ToDefinition converts a TestCase to a Challenges Definition
// for execution by the runner.
func (tc *TestCase) ToDefinition() *challenge.Definition {
	deps := make([]challenge.ID, len(tc.Dependencies))
	for i, d := range tc.Dependencies {
		deps[i] = challenge.ID(d)
	}

	return &challenge.Definition{
		ID:                challenge.ID(tc.ID),
		Name:              tc.Name,
		Description:       tc.Description,
		Category:          tc.Category,
		Dependencies:      deps,
		EstimatedDuration: tc.EstimatedDuration,
	}
}

// AppliesToPlatform returns true if this test case targets the
// given platform. An empty Platforms list means all platforms.
func (tc *TestCase) AppliesToPlatform(p config.Platform) bool {
	if len(tc.Platforms) == 0 {
		return true
	}
	for _, tp := range tc.Platforms {
		if tp == p || tp == config.PlatformAll {
			return true
		}
	}
	return false
}

// IsValid returns an error message if the test case has
// missing required fields, or empty string if valid.
func (tc *TestCase) IsValid() string {
	if tc.ID == "" {
		return "test case missing ID"
	}
	if tc.Name == "" {
		return "test case " + tc.ID + " missing name"
	}
	for i := range tc.Steps {
		st := &tc.Steps[i]
		if msg := st.validateShellAssertions(tc.ID, i); msg != "" {
			return msg
		}
		if msg := st.validateVisionAssertions(tc.ID, i); msg != "" {
			return msg
		}
		nc := st.ExpectBodyNotContains
		if nc == nil {
			continue
		}
		if *nc == "" {
			return fmt.Sprintf("test case %s step %d: expect_body_not_contains is empty — "+
				"an empty forbidden substring matches every body, so the assertion is vacuous",
				tc.ID, i)
		}
		if kind, _ := st.ParseAction(); kind != ActionTypeHTTP {
			return fmt.Sprintf("test case %s step %d: expect_body_not_contains is only read by "+
				"an http: action, but this step's action is %q — on any other kind the "+
				"assertion would be silently ignored", tc.ID, i, string(kind))
		}
		if st.ExpectStatus == 0 && st.ExpectBodyContains == "" && st.ExpectJSONPath == "" {
			return fmt.Sprintf("test case %s step %d: expect_body_not_contains has no positive "+
				"assertion beside it (expect_status, expect_body_contains or expect_json_path) — "+
				"a negative assertion alone passes on a 500, an empty body or an error envelope",
				tc.ID, i)
		}
	}
	return ""
}

// validateShellAssertions enforces the load-time rules for expect_exit and
// expect_output_contains so neither can ever be a silent no-op: both are
// accepted only on a `shell:` action (the one kind whose executors yield an
// exit code and combined output), an empty substring is refused, and an exit
// code must be a real one (0..255).
func (st *TestStep) validateShellAssertions(caseID string, idx int) string {
	if st.ExpectExit == nil && st.ExpectOutputContains == nil {
		return ""
	}
	key := "expect_exit"
	if st.ExpectExit == nil {
		key = "expect_output_contains"
	}
	if kind, _ := st.ParseAction(); kind != ActionTypeShell {
		return fmt.Sprintf("test case %s step %d: %s is only evaluated by a shell: action, "+
			"but this step's action is %q — on any other kind the assertion would be "+
			"silently ignored", caseID, idx, key, string(kind))
	}
	if st.ExpectExit != nil && (*st.ExpectExit < 0 || *st.ExpectExit > 255) {
		return fmt.Sprintf("test case %s step %d: expect_exit %d is not an exit code (0..255) — "+
			"the step could never pass", caseID, idx, *st.ExpectExit)
	}
	if st.ExpectOutputContains != nil && *st.ExpectOutputContains == "" {
		return fmt.Sprintf("test case %s step %d: expect_output_contains is empty — "+
			"an empty substring matches every output, so the assertion is vacuous", caseID, idx)
	}
	return ""
}

// DefaultPHashMaxDistance is the pHash Hamming distance accepted for a
// golden match when a step sets `golden` without `phash_max_distance`
// (8 of 64 bits: tolerant of re-encoding / anti-aliasing, far below the
// ~32 bits two unrelated frames differ by).
const DefaultPHashMaxDistance = 8

// MaxPHashMaxDistance caps phash_max_distance: two unrelated frames differ
// by ~32 of 64 bits, so a tolerance above 16 would accept a different screen.
const MaxPHashMaxDistance = 16

// visionFillerKeys are normalised phrases a model writes to say "there is
// no text". They are never screen text: dropped from a transcription and
// refused as recognize/forbid tokens.
var visionFillerKeys = map[string]bool{
	"no": true, "not": true, "none": true, "nothing": true, "empty": true,
	"blank": true, "null": true, "nil": true, "na": true, "n a": true,
	"no text": true, "no text visible": true, "no visible text": true,
	"no text is visible": true, "nothing visible": true, "nothing is visible": true,
	"there is no text": true, "no text present": true, "none visible": true,
}

// IsVisionFiller reports whether a normalised (VisionTokenKey) phrase is
// "no text" filler rather than on-screen text.
func IsVisionFiller(key string) bool { return visionFillerKeys[key] }

// HasPositiveVisionCheck reports whether the step asks for positive
// evidence of the expected screen (vision_verify, recognize or golden), as
// opposed to only negative evidence (forbid) or "something changed"
// (expect_change).
func (st TestStep) HasPositiveVisionCheck() bool {
	return st.VisionVerify || len(st.Recognize) > 0 || st.Golden != ""
}

// NeedsVisionModel reports whether the step's verdict requires a vision
// model's answer (vision_verify, recognize or forbid).
func (st TestStep) NeedsVisionModel() bool {
	return st.VisionVerify || len(st.Recognize) > 0 || len(st.Forbid) > 0
}

// IsVisionGated reports whether the step can PASS only on positive,
// frame-bound visual evidence: any vision key makes it gated.
func (st TestStep) IsVisionGated() bool {
	return st.NeedsVisionModel() || st.Golden != "" || st.ExpectChange
}

// ExactRecognize reports whether recognize tokens must equal a whole
// transcription element (the default) rather than appear inside one
// (recognize_contains).
func (st TestStep) ExactRecognize() bool { return !st.RecognizeContains }

// EffectivePHashMaxDistance returns the golden-match tolerance in bits.
func (st TestStep) EffectivePHashMaxDistance() int {
	if st.PHashMaxDistance != nil {
		return *st.PHashMaxDistance
	}
	return DefaultPHashMaxDistance
}

// VisionTokenKey folds a recognize/forbid token (or any text) to the
// form the deterministic token check compares: lower-case, every run of
// non-letter/non-digit characters collapsed to one space, trimmed.
func VisionTokenKey(s string) string {
	// NFKC folds compatibility forms (fullwidth, ligatures, mathematical
	// alphanumerics) to their plain letters; combining marks (Mn / Mc) are
	// part of the word, except the optional Arabic / Hebrew vowel marks,
	// which are dropped; format characters (zero-width
	// space / joiner, BOM, soft hyphen, tags — Unicode Cf) and the other
	// default-ignorable / variation-selector code points (isInvisibleNonFormat)
	// are dropped, so none can split or disguise a token.
	s = norm.NFKC.String(s)
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		if unicode.Is(unicode.Cf, r) || isInvisibleNonFormat(r) {
			continue
		}
		if isOptionalVowelMark(r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
			continue
		}
		space = true
	}
	return b.String()
}

// isInvisibleNonFormat reports the invisible code points OUTSIDE Unicode
// Cf that would otherwise split or disguise a token: the
// Other_Default_Ignorable_Code_Point set (combining grapheme joiner U+034F,
// Hangul fillers U+115F / U+1160 / U+3164 / U+FFA0 — letters, so not
// separators either — Khmer U+17B4 / U+17B5) and the Variation_Selector set
// (U+FE00..U+FE0F, U+E0100..U+E01EF, Mongolian U+180B..).
func isInvisibleNonFormat(r rune) bool {
	return unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) || unicode.Is(unicode.Variation_Selector, r)
}

// optionalVowelMarks are the Arabic harakat and the Hebrew niqqud /
// cantillation marks: optional vowel / reading marks that the same word is
// written with or without, so they are dropped. Every OTHER combining mark
// (Devanagari / Thai vowel signs, Latin accents without a precomposed form)
// is part of the word.
var optionalVowelMarks = &unicode.RangeTable{
	R16: []unicode.Range16{
		{Lo: 0x0591, Hi: 0x05BD, Stride: 1},
		{Lo: 0x05BF, Hi: 0x05BF, Stride: 1},
		{Lo: 0x05C1, Hi: 0x05C2, Stride: 1},
		{Lo: 0x05C4, Hi: 0x05C5, Stride: 1},
		{Lo: 0x05C7, Hi: 0x05C7, Stride: 1},
		{Lo: 0x0610, Hi: 0x061A, Stride: 1},
		{Lo: 0x064B, Hi: 0x065F, Stride: 1},
		{Lo: 0x0670, Hi: 0x0670, Stride: 1},
		{Lo: 0x06D6, Hi: 0x06DC, Stride: 1},
		{Lo: 0x06DF, Hi: 0x06E4, Stride: 1},
		{Lo: 0x06E7, Hi: 0x06E8, Stride: 1},
		{Lo: 0x06EA, Hi: 0x06ED, Stride: 1},
		{Lo: 0x08D3, Hi: 0x08FF, Stride: 1},
	},
}

// isOptionalVowelMark reports an Arabic / Hebrew optional vowel mark.
func isOptionalVowelMark(r rune) bool {
	return unicode.Is(unicode.Mn, r) && unicode.Is(optionalVowelMarks, r)
}

// validateVisionAssertions refuses vision keys that would assert nothing
// or contradict themselves, so a gated step can never pass vacuously.
func (st *TestStep) validateVisionAssertions(caseID string, idx int) string {
	if st.PHashMaxDistance != nil {
		if st.Golden == "" {
			return fmt.Sprintf("test case %s step %d: phash_max_distance without golden — "+
				"there is no reference to measure the distance to", caseID, idx)
		}
		if d := *st.PHashMaxDistance; d < 0 || d > MaxPHashMaxDistance {
			return fmt.Sprintf("test case %s step %d: phash_max_distance %d is outside 0..%d "+
				"(two unrelated frames differ by ~32 of 64 bits; a wider tolerance would "+
				"accept a different screen)", caseID, idx, d, MaxPHashMaxDistance)
		}
	}
	if st.IsVisionGated() {
		if kind, _ := st.ParseAction(); kind == ActionTypeDescription {
			return fmt.Sprintf("test case %s step %d: vision keys on a prose/description action — the "+
				"action is not executable, so the step would be skipped and its vision check never "+
				"made; use screenshot, sleep: or a real UI action", caseID, idx)
		}
		if kind, _ := st.ParseAction(); kind.IsNonScreen() {
			return fmt.Sprintf("test case %s step %d: vision keys (vision_verify, recognize, forbid, "+
				"golden, expect_change) are only meaningful on a screen action, but this step's "+
				"action is %q — its verdict is its own exit code / response, not the screen",
				caseID, idx, string(kind))
		}
		if !st.HasPositiveVisionCheck() {
			return fmt.Sprintf("test case %s step %d: expect_change / forbid alone is not a positive "+
				"check — any change (a crash dialog included) or any screen without the forbidden "+
				"text would pass; add vision_verify, recognize or golden", caseID, idx)
		}
	}
	if st.Golden != "" {
		g := st.Golden
		if strings.ContainsAny(g, `/\`) || strings.Contains(g, "..") || strings.TrimSpace(g) != g {
			return fmt.Sprintf("test case %s step %d: golden %q must be a bare id "+
				"(no path separators, no '..', no surrounding space)", caseID, idx, g)
		}
	}
	if st.RecognizeExact && len(st.Recognize) == 0 {
		return fmt.Sprintf("test case %s step %d: recognize_exact without recognize — "+
			"there is no token to match exactly", caseID, idx)
	}
	if st.RecognizeContains && len(st.Recognize) == 0 {
		return fmt.Sprintf("test case %s step %d: recognize_contains without recognize — "+
			"there is no token to match", caseID, idx)
	}
	if st.RecognizeContains && st.RecognizeExact {
		return fmt.Sprintf("test case %s step %d: recognize_contains and recognize_exact "+
			"contradict each other — set at most one", caseID, idx)
	}
	recognized := map[string]bool{}
	for _, tok := range st.Recognize {
		k := VisionTokenKey(tok)
		if k == "" {
			return fmt.Sprintf("test case %s step %d: recognize token %q is empty after "+
				"normalisation — it would match every description", caseID, idx, tok)
		}
		if IsVisionFiller(k) {
			return fmt.Sprintf("test case %s step %d: recognize token %q is filler a model writes "+
				"when there is NO text (it is dropped from the transcription)", caseID, idx, tok)
		}
		recognized[k] = true
	}
	for _, tok := range st.Forbid {
		k := VisionTokenKey(tok)
		if k == "" {
			return fmt.Sprintf("test case %s step %d: forbid token %q is empty after "+
				"normalisation — it would forbid every description", caseID, idx, tok)
		}
		if IsVisionFiller(k) {
			return fmt.Sprintf("test case %s step %d: forbid token %q is filler a model writes "+
				"when there is NO text (it is dropped from the transcription)", caseID, idx, tok)
		}
		if recognized[k] {
			return fmt.Sprintf("test case %s step %d: token %q is both recognized and "+
				"forbidden — the step could never pass", caseID, idx, tok)
		}
	}
	if len(st.Forbid) > 0 && len(st.Recognize) == 0 {
		return fmt.Sprintf("test case %s step %d: forbid without recognize — the absence of a "+
			"forbidden token is only credited when the same reading matched a recognize token "+
			"(proof the model read the screen); add recognize", caseID, idx)
	}
	return ""
}

// CheckShellOutcome is the single verdict function for a `shell:` step. Both
// shell executors (the bank-driven runner and the autonomous executor) call
// it with the command's real exit code and combined stdout+stderr, so the two
// paths cannot drift apart.
//
//   - no expect_exit: the command must exit 0 (the established behaviour);
//   - expect_exit N: the command must exit exactly N;
//   - expect_output_contains S: the combined output must contain S.
//
// It returns ok=true with an empty message, or ok=false with a message naming
// what was expected and what was observed.
func (st TestStep) CheckShellOutcome(exitCode int, output string) (bool, string) {
	want := 0
	if st.ExpectExit != nil {
		want = *st.ExpectExit
	}
	if exitCode != want {
		if st.ExpectExit == nil {
			return false, fmt.Sprintf("shell exited %d", exitCode)
		}
		return false, fmt.Sprintf("shell exited %d, expected %d", exitCode, want)
	}
	if st.ExpectOutputContains != nil {
		if *st.ExpectOutputContains == "" {
			return false, "expect_output_contains is empty — an empty substring matches everything, refusing to pass"
		}
		if !strings.Contains(output, *st.ExpectOutputContains) {
			return false, fmt.Sprintf("output does not contain %q", *st.ExpectOutputContains)
		}
	}
	return true, ""
}
