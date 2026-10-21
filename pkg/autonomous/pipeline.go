// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package autonomous

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	osexec "os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"digital.vasic.helixqa/pkg/analysis"
	"digital.vasic.helixqa/pkg/conduit"
	"digital.vasic.helixqa/pkg/config"
	"digital.vasic.helixqa/pkg/controller"
	"digital.vasic.helixqa/pkg/detector"
	"digital.vasic.helixqa/pkg/learning"
	"digital.vasic.helixqa/pkg/llm"
	"digital.vasic.helixqa/pkg/maestro"
	"digital.vasic.helixqa/pkg/memory"
	"digital.vasic.helixqa/pkg/navigator"
	"digital.vasic.helixqa/pkg/performance"
	"digital.vasic.helixqa/pkg/planning"
	"digital.vasic.helixqa/pkg/regression"
	"digital.vasic.helixqa/pkg/replay"
	"digital.vasic.helixqa/pkg/reproduce"
	"digital.vasic.helixqa/pkg/training"
	"digital.vasic.helixqa/pkg/video"
	"digital.vasic.helixqa/pkg/vision"
	visionremote "digital.vasic.visionengine/pkg/remote"
)

// PipelineConfig holds the parameters for a SessionPipeline
// run.
type PipelineConfig struct {
	// ProjectRoot is the absolute path to the project under
	// test.
	ProjectRoot string

	// Platforms lists the target platforms (e.g. "android",
	// "web", "desktop").
	Platforms []string

	// OutputDir is the directory where reports and evidence
	// are written.
	OutputDir string

	// IssuesDir is the directory for generated issue
	// tickets.
	IssuesDir string

	// BanksDir is the directory containing test bank YAML
	// files for reconciliation. Empty means skip
	// reconciliation.
	BanksDir string

	// VisionGoldensDir holds the reference images that bank
	// steps name with `golden: <id>` (<id>.png/.jpg/.jpeg).
	// When empty, env HELIXQA_GOLDENS_DIR is used; when both
	// are empty a golden step is BLOCKED (could not determine),
	// never passed.
	VisionGoldensDir string

	// HTTPBaseURL is the root URL the structured executor
	// uses for ActionTypeHTTP steps (e.g.
	// "http://thinker.local:8092"). When empty, the executor
	// falls back to the HELIXQA_HTTP_BASE_URL env var. When
	// both are empty, ActionTypeHTTP steps fail with a clear
	// configuration error rather than silently no-op.
	//
	// Added 2026-04-29 for BLUFF-HELIXQA-BANKS-REWRITE-001 —
	// see helix_qa/pkg/autonomous/http_executor.go.
	HTTPBaseURL string

	// PlaywrightCDPURL is the Chrome DevTools Protocol
	// WebSocket endpoint the structured executor uses for
	// ActionTypePlaywright steps (e.g. "ws://localhost:9222"
	// or "ws://playwright-container:9222"). When empty, the
	// executor falls back to the HELIXQA_PLAYWRIGHT_CDP_URL
	// env var. When both are empty, ActionTypePlaywright
	// steps SKIP with PLAYWRIGHT-RUNTIME-PENDING (no false
	// PASS, no false FAIL — Article XI §11.2.2).
	//
	// Added 2026-04-29 to wire ActionTypePlaywright through
	// digital.vasic.challenges/pkg/userflow.PlaywrightCLIAdapter.
	PlaywrightCDPURL string

	// Timeout is the maximum duration for the entire
	// pipeline run.
	Timeout time.Duration

	// PassNumber identifies this QA pass for the memory
	// store.
	PassNumber int

	// AndroidDevice is the ADB device/emulator serial
	// (e.g. "emulator-5554" or "192.168.0.214:5555").
	// For single-device mode.
	AndroidDevice string

	// AndroidDevices is a list of all ADB devices to test
	// in parallel. When non-empty, the pipeline creates one
	// executor + vision slot per device and runs curiosity
	// in parallel goroutines.
	AndroidDevices []string

	// AndroidPackage is the Android application package
	// name (e.g. "com.example.app").
	AndroidPackage string

	// CompetingAppPackages lists Android TV apps the caller
	// wants proactively force-stopped before structured and
	// curiosity phases begin. On Android TV, the home screen
	// aggregates channel rows from every installed app that
	// published TvContractCompat channels — a stray
	// DPAD_ENTER on a foreign app's channel tile can hand
	// control to the foreign app, and every subsequent
	// keypress then lands in the wrong UI. This list is
	// consumer-owned (HelixQA Constitution §1 — no
	// project-specific data baked into the library); leave
	// empty to use a generic empirically-observed default
	// of apps seen publishing channels in the wild.
	CompetingAppPackages []string

	// WebURL is the URL for web platform testing.
	WebURL string

	// DesktopDisplay is the X11 display identifier
	// (e.g. ":0").
	DesktopDisplay string

	// FFmpegPath is the path to the ffmpeg binary used
	// for video post-processing.
	FFmpegPath string

	// CuriosityEnabled controls whether the curiosity-
	// driven exploration phase is active.
	CuriosityEnabled bool

	// CuriosityTimeout is the maximum duration for the
	// curiosity-driven exploration phase.
	CuriosityTimeout time.Duration

	// VisionHost is the hostname of the remote machine
	// running Ollama for vision inference (e.g.
	// "thinker.local"). Empty disables auto-deploy.
	VisionHost string

	// VisionUser is the SSH user for the vision host.
	VisionUser string

	// VisionModel is the Ollama model to use for vision
	// (default "llava:7b").
	VisionModel string

	// UseLlamaCpp switches from Ollama to llama.cpp backend.
	// When true, HelixQA uses llama-server instances (one per
	// platform/device) for true multi-instance vision.
	UseLlamaCpp bool

	// LlamaCppModelPath is the path to the GGUF model on the
	// remote host (e.g. ~/models/llava-7b-q4.gguf).
	LlamaCppModelPath string

	// LlamaCppMMProjPath is the path to the multimodal
	// projector GGUF on the remote host.
	LlamaCppMMProjPath string

	// QACredentials holds login credentials discovered by
	// the Learn phase from .env files. Used to auto-login
	// via intent extras on Android TV.
	QACredentials map[string]string

	// LlamaCppFreeGPU stops Ollama before starting
	// llama-server to free GPU VRAM. Ollama is restored
	// after the QA session completes.
	LlamaCppFreeGPU bool

	// VisionHosts is a comma-separated list of remote hosts
	// for distributed vision inference. When set, the
	// pipeline probes each host's hardware via SSH, selects
	// the strongest model that fits the combined resources,
	// and activates distributed RPC if needed. Takes
	// precedence over VisionHost for multi-host setups.
	VisionHosts []string

	// VisionMultiUser is the SSH user for multi-host
	// probing. Falls back to VisionUser if empty.
	VisionMultiUser string

	// LlamaCppRPCModelPath is the path to the GGUF model on
	// the master host for distributed RPC inference. When
	// empty, auto-detection will not start RPC workers.
	LlamaCppRPCModelPath string

	// VisionSSHKeyPath is the absolute path to the PEM-encoded
	// SSH private key used to probe the distributed vision
	// hosts in VisionHosts. Sourced from env / config — never
	// hardcoded (CONST-042 no-secret-leak). Consumed when
	// building visionremote.SSHConfig for ProbeHosts.
	VisionSSHKeyPath string

	// VisionSSHKnownHostsPath is the absolute path to the
	// known_hosts file used to verify the remote vision
	// hosts' host keys. Required by visionremote.ProbeHosts
	// (CONST-035 anti-bluff: accepting unknown hosts is a
	// silent security PASS-bluff).
	VisionSSHKnownHostsPath string

	// VisionSSHPort is the SSH port for the distributed
	// vision hosts. Zero defaults to 22 inside
	// visionremote.SSHConfig.
	VisionSSHPort int

	// VisionRPCMinGPUMemMB is the minimum GPU memory (MB) the
	// distributed-RPC GGUF model in LlamaCppRPCModelPath
	// requires per host. Drives the visionremote.ModelSpec
	// catalogue fed to SelectStrongestModel / PlanDistribution.
	// Zero falls back to a 1 MB floor so the planner treats
	// the model as fitting any probed host.
	VisionRPCMinGPUMemMB int

	// VisionRPCMinRAMMB is the minimum system RAM (MB) the
	// distributed-RPC GGUF model requires per host. Drives
	// the visionremote.ModelSpec catalogue. Zero falls back
	// to a 1 MB floor.
	VisionRPCMinRAMMB int

	// ChatProviders holds provider configs for the chat model
	// used in Plan and Analyze phases. When non-empty, a
	// separate AdaptiveProvider is built for chat tasks
	// (reasoning, test planning, report generation) so it can
	// differ from the vision provider used in Execute and
	// Curiosity phases.
	ChatProviders []llm.ProviderConfig
}

// PipelineResult captures the outcome of a SessionPipeline
// run.
type PipelineResult struct {
	Status         SessionStatus    `json:"status"`
	SessionID      string           `json:"session_id"`
	Duration       time.Duration    `json:"duration"`
	TestsPlanned   int              `json:"tests_planned"`
	TestsRun       int              `json:"tests_run"`
	IssuesFound    int              `json:"issues_found"`
	TicketsCreated int              `json:"tickets_created"`
	CoveragePct    float64          `json:"coverage_pct"`
	Cost           *llm.CostSummary `json:"cost,omitempty"`
	Error          string           `json:"error,omitempty"`

	// Structured is the Phase 3.5 structured test-bank outcome, per case
	// and per step (frame sha256 / pHash included). Nil when the phase
	// did not run.
	Structured *StructuredExecutionResult `json:"structured,omitempty"`
	// StructuredError records why the structured phase could not run
	// (e.g. banks failed to load).
	StructuredError string `json:"structured_error,omitempty"`
}

// ExitCode is the process exit status for this session, on the
// three-valued contract: 1 when the session failed or a structured case
// FAILED; 2 when a structured case was BLOCKED (could not determine), when
// structured cases ran but none passed, when the session itself was
// BLOCKED (preflight network / VPN, provider outage in learn / plan, a
// timeout), or when the structured phase could not run its banks; 0
// otherwise. A finding (1) outranks could-not-determine
// (2). A session whose structured phase ran no case makes no structured
// claim and is judged on its other phases alone.
func (r *PipelineResult) ExitCode() int {
	if r.Status == StatusFailed {
		return 1
	}
	if r.Structured != nil && r.Structured.TestCasesRun > 0 {
		if c := r.Structured.ExitCode(); c != 0 {
			return c
		}
	}
	if r.Status == StatusBlocked {
		// The session stopped for an environment reason (network / VPN,
		// provider outage, timeout) — could not determine.
		return 2
	}
	if r.StructuredError != "" {
		return 2
	}
	return 0
}

// SessionPipeline orchestrates the four-phase autonomous QA
// pipeline: learn, plan, execute, analyze.
type SessionPipeline struct {
	// preflight replaces preflightCheck when set (tests inject the
	// network outcome instead of depending on the host's DNS).
	preflight func(context.Context) error
	config    *PipelineConfig
	provider  llm.Provider
	// chatProvider is used for reasoning-heavy phases (Plan,
	// Analyze report generation). When nil, falls back to the
	// shared provider.
	chatProvider llm.Provider
	// visionProvider is used for screenshot analysis phases
	// (Execute, Curiosity). When nil, falls back to the shared
	// provider.
	visionProvider llm.Provider
	// phaseSelector dynamically selects the best provider
	// for each pipeline phase based on capability scoring.
	// When non-nil, selectProviderForPhase uses it before
	// falling back to the dedicated chat/vision providers.
	phaseSelector *llm.PhaseModelSelector
	store         *memory.Store
	// costTracker accumulates LLM API call costs across the
	// session. Created at pipeline start and attached to all
	// adaptive providers.
	costTracker *llm.CostTracker
	// kbContext holds a summary of the Learn phase knowledge
	// base, injected into navigation prompts so the LLM
	// knows app-specific details (credentials, screens, etc.)
	// without hardcoding them in the prompt templates.
	kbContext string

	// ── Revolutionary features (all optional — nil-safe) ──

	// screenDiffer detects when a UI action had no visible
	// effect by comparing consecutive screenshots. When the
	// screen is unchanged, the LLM is told to try a
	// different action.
	screenDiffer *vision.ScreenDiffer

	// replayBuffer persists known-good action sequences to
	// SQLite so future sessions can replay them instead of
	// re-discovering navigation paths from scratch.
	replayBuffer *replay.ReplayBuffer

	// trainingCollector records screenshot + action pairs
	// during autonomous QA for future vision model
	// fine-tuning.
	trainingCollector *training.TrainingCollector

	// dualScreenCapturer captures both visual screenshots
	// and UI automator XML from Android devices to provide
	// richer context to the LLM.
	dualScreenCapturer *navigator.DualScreenCapturer

	// visualRegression compares screenshots across multiple
	// devices at the same test step to detect cross-device
	// layout inconsistencies.
	visualRegression *regression.VisualRegression

	// processController monitors step liveness and kills
	// stuck steps. When non-nil, the curiosity loop
	// registers steps and sends heartbeats.
	processController *controller.Controller

	// eventSink, when non-nil, receives structured real-time
	// events (session/phase/evidence/llm/vision/verdict) so an
	// external conductor can stay in sync with the session. Always
	// accessed through sink() which returns a no-op when nil.
	eventSink conduit.Sink
}

// NewSessionPipeline creates a SessionPipeline with the
// given configuration, LLM provider, and memory store.
// The provider is used as the default for all phases.
// Use WithChatProvider and WithVisionProvider to override
// for specific phase types. A CostTracker is automatically
// created and attached to all adaptive providers.
func NewSessionPipeline(
	cfg *PipelineConfig,
	provider llm.Provider,
	store *memory.Store,
) *SessionPipeline {
	ct := llm.NewCostTracker()
	attachCostTracker(provider, ct)
	return &SessionPipeline{
		config:      cfg,
		provider:    provider,
		store:       store,
		costTracker: ct,
	}
}

// attachCostTracker sets the cost tracker on a provider if
// it is an *AdaptiveProvider.
func attachCostTracker(p llm.Provider, ct *llm.CostTracker) {
	if ap, ok := p.(*llm.AdaptiveProvider); ok {
		ap.SetCostTracker(ct)
	}
}

// WithChatProvider sets a dedicated provider for reasoning-heavy
// phases (Plan, Analyze). This allows using a different model
// optimized for text reasoning while the default provider handles
// vision tasks. The session's cost tracker is automatically
// attached.
func (sp *SessionPipeline) WithChatProvider(p llm.Provider) *SessionPipeline {
	sp.chatProvider = p
	attachCostTracker(p, sp.costTracker)
	return sp
}

// WithVisionProvider sets a dedicated provider for screenshot
// analysis phases (Execute, Curiosity). This allows using a
// specialized vision model while the default provider handles
// chat/reasoning tasks. The session's cost tracker is
// automatically attached.
func (sp *SessionPipeline) WithVisionProvider(p llm.Provider) *SessionPipeline {
	sp.visionProvider = p
	attachCostTracker(p, sp.costTracker)
	return sp
}

// WithPhaseSelector sets the phase-aware model selector that
// dynamically picks the best provider for each pipeline phase
// based on capability scoring from the vision model registry.
// When set, selectProviderForPhase consults it before falling
// back to the dedicated chat/vision providers.
func (sp *SessionPipeline) WithPhaseSelector(
	sel *llm.PhaseModelSelector,
) *SessionPipeline {
	sp.phaseSelector = sel
	return sp
}

// WithEventSink attaches a real-time conductor event sink. When set,
// the pipeline emits structured events (session start/end, phase
// transitions, LLM/vision bridge calls, evidence captured, verdicts)
// so an external conductor can stay in sync without parsing stdout.
// Pass conduit.NopSink() or leave unset to disable.
func (sp *SessionPipeline) WithEventSink(
	sink conduit.Sink,
) *SessionPipeline {
	sp.eventSink = sink
	return sp
}

// sink returns the configured event sink, or a no-op sink when none
// is set, so call sites never need a nil check.
func (sp *SessionPipeline) sink() conduit.Sink {
	if sp.eventSink == nil {
		return conduit.NopSink()
	}
	return sp.eventSink
}

// WithController attaches a QA Process Controller that
// monitors step liveness during the curiosity phase and
// kills stuck steps. The controller runs as a background
// goroutine and is stopped when the pipeline finishes.
func (sp *SessionPipeline) WithController(
	ctrl *controller.Controller,
) *SessionPipeline {
	sp.processController = ctrl
	return sp
}

// CurrentCost returns the current cost summary for the active
// session. This can be called while the pipeline is running
// to get a real-time view of accumulated costs.
func (sp *SessionPipeline) CurrentCost() llm.CostSummary {
	if sp.costTracker == nil {
		return llm.CostSummary{}
	}
	return sp.costTracker.SummaryCompact()
}

// getChatProvider returns the provider for reasoning tasks.
// Falls back to the shared provider if no dedicated chat
// provider is configured.
func (sp *SessionPipeline) getChatProvider() llm.Provider {
	if sp.chatProvider != nil {
		return sp.chatProvider
	}
	return sp.provider
}

// getVisionProvider returns the provider for vision tasks.
// Falls back to the shared provider if no dedicated vision
// provider is configured.
func (sp *SessionPipeline) getVisionProvider() llm.Provider {
	if sp.visionProvider != nil {
		return sp.visionProvider
	}
	return sp.provider
}

// setPhase updates the phase label on all adaptive providers
// so that cost records are tagged with the correct phase.
func (sp *SessionPipeline) setPhase(phase string) {
	for _, p := range []llm.Provider{
		sp.provider, sp.chatProvider, sp.visionProvider,
	} {
		if ap, ok := p.(*llm.AdaptiveProvider); ok {
			ap.SetPhase(phase)
		}
	}
}

// selectProviderForPhase consults the PhaseModelSelector
// (if configured) to build a phase-aware AdaptiveProvider
// with all available providers ranked by score. This gives
// automatic fallback: if the primary provider fails, the
// next best is tried immediately — no single point of
// failure.
//
// Falls back to getChatProvider or getVisionProvider
// depending on the phase's strategy when no selector is
// configured.
func (sp *SessionPipeline) selectProviderForPhase(
	phase string,
) llm.Provider {
	if sp.phaseSelector != nil {
		if adaptive := sp.phaseSelector.SelectAdaptiveForPhase(
			phase,
		); adaptive != nil {
			// Attach cost tracker for spend visibility.
			if sp.costTracker != nil {
				adaptive.SetCostTracker(sp.costTracker)
			}
			fmt.Printf(
				"[pipeline] Phase %s: adaptive fallback "+
					"with %d providers\n",
				phase, len(adaptive.Providers()),
			)
			return adaptive
		}
	}
	// Fallback: use the dedicated provider for the
	// phase type (vision or chat), or the shared
	// provider.
	strat := llm.PhaseStrategy{}
	if sp.phaseSelector != nil {
		strat = sp.phaseSelector.Strategy(phase)
	}
	if strat.PreferVision {
		return sp.getVisionProvider()
	}
	return sp.getChatProvider()
}

// perTestTimeout is the maximum time a single test
// iteration (screenshot + crash check per platform) is
// allowed to take before being abandoned. This prevents a
// hung ADB screencap or crash-check from blocking the
// entire pipeline.
// REDUCED for aggressive performance.
const perTestTimeout = 30 * time.Second

// perMaestroFlowTimeout limits individual Maestro flow
// runs so a single stuck flow cannot consume the session.
// REDUCED for aggressive performance.
const perMaestroFlowTimeout = 1 * time.Minute

// maxVisionScreenshots caps how many screenshots are sent
// to the LLM vision API during the analysis phase.
const maxVisionScreenshots = 15

// maxVisionFrames caps how many video frames per video
// are sent to vision analysis.
const maxVisionFrames = 3

// maxCuriositySteps limits exploration steps per platform.
// 50 steps allow the agent to navigate through login,
// browse ALL content rails, open details, test favorites,
// play media, explore settings, and test edge cases —
// like a thorough human QA session.
const maxCuriositySteps = 50

// logcatTimeout limits the logcat dump so a large log
// buffer cannot stall the pipeline.
// REDUCED for aggressive performance.
const logcatTimeout = 5 * time.Second

// preflightCheck verifies that the network environment is suitable
// for LLM API calls. It detects Mullvad VPN reconnections and other
// DNS-blocking conditions so the pipeline fails fast with a clear,
// actionable error instead of a cryptic "all providers failed".
func (sp *SessionPipeline) preflightCheck(
	ctx context.Context,
) error {
	// Test DNS resolution for a well-known endpoint used by
	// multiple LLM providers.
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var dialer net.Dialer
	conn, err := dialer.DialContext(checkCtx, "udp", "8.8.8.8:53")
	if err != nil {
		// UDP 8.8.8.8:53 blocked — very likely a VPN firewall.
		return fmt.Errorf(
			"pre-flight check FAILED: cannot open UDP socket to 8.8.8.8:53 (%v). "+
				"Mullvad VPN (or another firewall) is blocking outbound DNS. "+
				"Wait for the VPN to finish reconnecting, or disconnect it temporarily.",
			err,
		)
	}
	_ = conn.Close()

	// Try an actual DNS lookup.
	_, lookupErr := net.LookupHost("generativelanguage.googleapis.com")
	if lookupErr != nil {
		// Mullvad specifically blocks local-router DNS during
		// reconnection while allowing tunnel DNS.
		mullvadActive := false
		if out, _ := osexec.Command("mullvad", "status").Output(); strings.Contains(string(out), "Connected") {
			mullvadActive = true
		}
		msg := fmt.Sprintf(
			"pre-flight check FAILED: DNS lookup failed (%v). ",
			lookupErr,
		)
		if mullvadActive {
			msg += "Mullvad VPN is active and appears to be blocking DNS queries. "
		} else {
			msg += "A VPN or firewall is blocking DNS queries. "
		}
		msg += "Wait for the VPN tunnel to stabilize, or disconnect the VPN before running QA."
		return fmt.Errorf("%s", msg)
	}

	return nil
}

// Run executes the four pipeline phases in order:
//  1. Learn  — build a knowledge base from the project
//  2. Plan   — generate, reconcile, and rank test cases
//  3. Execute — run tests with video recording, screenshots, crash detection, Maestro flows
//     3.5 Curiosity — explore unknown areas via random navigation
//  4. Analyze — LLM vision analysis, memory leak detection, video frame analysis, issue tickets
//
// It creates a session in the memory store at the start and
// updates it when the pipeline completes.
func (sp *SessionPipeline) Run(
	ctx context.Context,
) (*PipelineResult, error) {
	start := time.Now()
	sessionID := fmt.Sprintf(
		"pipeline-%d", start.UnixNano(),
	)

	// Create session in memory store.
	sess := memory.Session{
		ID:         sessionID,
		StartedAt:  start,
		Platforms:  joinStrings(sp.config.Platforms),
		PassNumber: sp.config.PassNumber,
	}
	if err := sp.store.CreateSession(sess); err != nil {
		return nil, fmt.Errorf(
			"pipeline: create session: %w", err,
		)
	}

	// Announce the session on the real-time conductor channel so an
	// external orchestrator can begin tracking immediately.
	conduit.SessionStart(sp.sink(), sessionID, map[string]any{
		"platforms":   sp.config.Platforms,
		"pass_number": sp.config.PassNumber,
		"output_dir":  sp.config.OutputDir,
	})

	// Apply timeout if configured.
	if sp.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(
			ctx, sp.config.Timeout,
		)
		defer cancel()
	}

	result := &PipelineResult{
		SessionID: sessionID,
		Status:    StatusRunning,
	}

	// Emit a terminal session_end on every exit path (success,
	// failure, or panic-free early return). Using a defer guarantees
	// the conductor always sees the closing event exactly once,
	// regardless of which return statement fires.
	defer func() {
		v := conduit.VerdictPass
		switch result.Status {
		case StatusFailed:
			v = conduit.VerdictFail
		case StatusBlocked:
			v = conduit.VerdictOperatorBlocked
		}
		conduit.SessionEnd(sp.sink(), sessionID, v,
			fmt.Sprintf("status=%s", result.Status))
	}()

	// ── Pre-flight network/VPN check ────────────────────
	// Fail fast with a clear message if Mullvad or another
	// VPN is actively blocking DNS. This prevents cryptic
	// "all providers failed" errors later in the plan phase.
	preflight := sp.preflightCheck
	if sp.preflight != nil {
		preflight = sp.preflight
	}
	if err := preflight(ctx); err != nil {
		// The network / VPN is not usable: nothing about the app was
		// judged — BLOCKED (exit 2), never a FAILED verdict.
		result.Status = StatusBlocked
		result.Error = err.Error()
		result.Duration = time.Since(start)
		now := time.Now()
		dur := int(result.Duration.Seconds())
		_ = sp.store.UpdateSession(sessionID, memory.SessionUpdate{
			EndedAt:  &now,
			Duration: dur,
			Notes:    fmt.Sprintf("status=%s error=%s", result.Status, result.Error),
		})
		return result, nil
	}

	// ── Initialize revolutionary features ────────────────
	// All features are optional — nil checks protect every
	// use site so a failure here never blocks the pipeline.

	sp.screenDiffer = vision.NewScreenDiffer(0.97)

	replayDBPath := filepath.Join(
		sp.config.OutputDir, "replay.db",
	)
	if rb, rbErr := replay.NewReplayBuffer(
		replayDBPath,
	); rbErr != nil {
		fmt.Printf(
			"[pipeline] warning: replay buffer "+
				"init failed: %v\n", rbErr,
		)
	} else {
		sp.replayBuffer = rb
		defer sp.replayBuffer.Close()
		fmt.Printf(
			"[pipeline] Replay buffer: %s (%d sequences)\n",
			replayDBPath, sp.replayBuffer.Len(),
		)
	}

	trainingDir := filepath.Join(
		sp.config.OutputDir, "training-data",
	)
	sp.trainingCollector = training.NewTrainingCollector(
		trainingDir,
	)

	sp.dualScreenCapturer = navigator.NewDualScreenCapturer(
		detector.NewExecRunner(),
	)

	// ── Phase 0: Vision pool setup ───────────────────────
	// Create a VisionPool with one dedicated slot per
	// platform/device. Each slot serializes its own vision
	// calls so platforms don't contend with each other.
	var visionPool *visionremote.VisionPool

	// Track distributed RPC deployers for cleanup at
	// session end.
	var distributedDeployers []*visionremote.LlamaCppDeployer
	var distributedRPCPorts []int
	var distributedMasterDeployer *visionremote.LlamaCppDeployer
	var distributedMasterPort int

	// ── Phase 0a: Distributed vision auto-detection ─────
	// When HELIX_VISION_HOSTS is set, probe each host's
	// hardware via SSH, select the strongest model, and
	// activate distributed RPC if the model needs it.
	if len(sp.config.VisionHosts) > 0 {
		sshUser := sp.config.VisionMultiUser
		if sshUser == "" {
			sshUser = sp.config.VisionUser
		}

		fmt.Printf(
			"[pipeline] Phase 0a: Probing %d hosts "+
				"for distributed vision\n",
			len(sp.config.VisionHosts),
		)

		// Round-344: the VisionEngine remote API (post
		// round-340 superset) takes a []visionremote.SSHConfig
		// rather than (hosts []string, user string). Build the
		// SSHConfig slice from the pipeline config — SSH key /
		// known_hosts / port are config-injected per CONST-045
		// / CONST-046, never hardcoded.
		sshConfigs := make(
			[]visionremote.SSHConfig, 0,
			len(sp.config.VisionHosts),
		)
		for _, host := range sp.config.VisionHosts {
			sshConfigs = append(sshConfigs, visionremote.SSHConfig{
				Host:           host,
				Port:           sp.config.VisionSSHPort,
				User:           sshUser,
				KeyPath:        sp.config.VisionSSHKeyPath,
				KnownHostsPath: sp.config.VisionSSHKnownHostsPath,
			})
		}
		hwList, probeErr := visionremote.ProbeHosts(
			ctx, sshConfigs,
		)
		if probeErr != nil {
			// ProbeHosts joins per-host probe failures; a
			// non-nil error with a partially-populated hwList
			// is normal (some hosts reachable, others not).
			// Surface the failures but continue with whatever
			// hosts succeeded.
			fmt.Printf(
				"[pipeline] warning: distributed vision "+
					"host probe reported errors: %v\n",
				probeErr,
			)
		}

		if len(hwList) > 0 {
			// Round-344: SelectStrongestModel / PlanDistribution
			// now require a []visionremote.ModelSpec catalogue.
			// Build a single-entry catalogue from the configured
			// distributed-RPC GGUF model — its name is the file
			// path, capacity floors come from config (CONST-046:
			// no hardcoded model metadata).
			minGPU := sp.config.VisionRPCMinGPUMemMB
			if minGPU <= 0 {
				minGPU = 1
			}
			minRAM := sp.config.VisionRPCMinRAMMB
			if minRAM <= 0 {
				minRAM = 1
			}
			modelCatalog := []visionremote.ModelSpec{{
				Name:         sp.config.LlamaCppRPCModelPath,
				Size:         sp.config.VisionModel,
				Path:         sp.config.LlamaCppRPCModelPath,
				MinGPUMemMB:  minGPU,
				MinRAMMB:     minRAM,
				QualityScore: 1,
			}}
			rec, selErr := visionremote.SelectStrongestModel(
				hwList, modelCatalog,
			)
			if selErr != nil {
				fmt.Printf(
					"[pipeline] warning: distributed vision "+
						"model selection failed: %v "+
						"(falling back to single-host "+
						"config)\n",
					selErr,
				)
				rec = nil
			}
			if rec != nil {
				fmt.Printf(
					"[pipeline] Model selected: %s (%s) "+
						"across %d hosts "+
						"(GPU=%dMB, RAM=%dMB, "+
						"distributed=%v)\n",
					rec.ModelName, rec.ModelSize,
					len(rec.AllHosts),
					rec.TotalGPUMemMB,
					rec.TotalRAMMB,
					rec.NeedsDistribution,
				)

				// Override VisionModel with the auto-selected
				// model so downstream phases use it.
				sp.config.VisionModel = rec.ModelName

				if rec.NeedsDistribution &&
					sp.config.LlamaCppRPCModelPath != "" {
					// Distributed RPC mode: start rpc-server on
					// each host, then start master llama-server
					// with --rpc flag.
					//
					// Round-344: PlanDistribution (post round-340
					// superset) takes (hwList, []ModelSpec) and
					// returns (*DistributionConfig, error). The
					// GGUF model path and server port are no
					// longer call arguments — they are set on the
					// returned struct after bin-packing.
					distCfg, planErr := visionremote.PlanDistribution(
						hwList, modelCatalog,
					)
					if planErr != nil {
						fmt.Printf(
							"[pipeline] warning: distributed RPC "+
								"planning failed: %v "+
								"(falling back to single-host)\n",
							planErr,
						)
						distCfg = nil
					}
					if distCfg != nil {
						// PlanDistribution leaves ModelPath empty
						// and ServerPort zero — wire them from
						// config now.
						distCfg.ModelPath = sp.config.LlamaCppRPCModelPath
						distCfg.ServerPort = 8090
						fmt.Printf(
							"[pipeline] Distributed RPC: "+
								"master=%s, %d workers\n",
							distCfg.MasterHost,
							len(distCfg.RPCWorkers),
						)

						// Start RPC workers on each host.
						rpcBasePort := 50052
						for i, h := range hwList {
							port := rpcBasePort + i
							deployer := visionremote.NewLlamaCppDeployer(
								visionremote.LlamaCppConfig{
									Host:    h.Host,
									User:    sshUser,
									RepoDir: h.LlamaCppDir,
								},
							)
							if err := deployer.StartRPCServer(
								ctx, port,
							); err != nil {
								fmt.Printf(
									"[pipeline] warning: "+
										"RPC server on %s:%d "+
										"failed: %v\n",
									h.Host, port, err,
								)
							} else {
								fmt.Printf(
									"[pipeline] RPC worker "+
										"started on %s:%d\n",
									h.Host, port,
								)
							}
							distributedDeployers = append(
								distributedDeployers, deployer,
							)
							distributedRPCPorts = append(
								distributedRPCPorts, port,
							)
						}

						// Start master llama-server with --rpc.
						masterDeployer := visionremote.NewLlamaCppDeployer(
							visionremote.LlamaCppConfig{
								Host:        distCfg.MasterHost,
								User:        sshUser,
								RepoDir:     distCfg.MasterDir,
								ModelPath:   distCfg.ModelPath,
								BasePort:    distCfg.ServerPort,
								ContextSize: distCfg.ContextSize,
							},
						)
						if err := masterDeployer.StartWithRPC(
							ctx,
							distCfg.ModelPath,
							distCfg.RPCWorkers,
							distCfg.ServerPort,
						); err != nil {
							fmt.Printf(
								"[pipeline] warning: "+
									"distributed master "+
									"failed: %v "+
									"(falling back to "+
									"single-host)\n",
								err,
							)
						} else {
							distributedMasterDeployer = masterDeployer
							distributedMasterPort = distCfg.ServerPort
							// Override VisionHost to point to
							// the distributed master.
							sp.config.VisionHost = distCfg.MasterHost
							sp.config.UseLlamaCpp = false
							// Use Ollama-compatible endpoint
							// (llama-server serves OpenAI API).
							fmt.Printf(
								"[pipeline] Distributed "+
									"inference active at "+
									"http://%s:%d\n",
								distCfg.MasterHost,
								distCfg.ServerPort,
							)
						}
					}
				} else if !rec.NeedsDistribution &&
					len(rec.GPUHosts) > 0 {
					// Single GPU host is sufficient — use Ollama
					// on the strongest GPU host for simplicity.
					sp.config.VisionHost = rec.GPUHosts[0]
					fmt.Printf(
						"[pipeline] Single-host vision: "+
							"%s (%s on %s)\n",
						rec.ModelName, rec.ModelSize,
						rec.GPUHosts[0],
					)
				} else if len(rec.AllHosts) > 0 {
					// CPU-only or single host — use first
					// reachable host.
					sp.config.VisionHost = rec.AllHosts[0]
					fmt.Printf(
						"[pipeline] Single-host vision: "+
							"%s (%s on %s)\n",
						rec.ModelName, rec.ModelSize,
						rec.AllHosts[0],
					)
				}
			}
		} else {
			fmt.Println(
				"[pipeline] warning: all vision hosts " +
					"unreachable, falling back to " +
					"single-host config",
			)
		}
	}

	if sp.config.VisionHost != "" {
		fmt.Printf(
			"[pipeline] Phase 0: Vision pool on %s "+
				"(%d platforms)\n",
			sp.config.VisionHost,
			len(sp.config.Platforms),
		)
		poolCfg := visionremote.PoolConfig{
			Host:   sp.config.VisionHost,
			User:   sp.config.VisionUser,
			Model:  sp.config.VisionModel,
			Shared: true,
		}

		// Use llama.cpp backend when configured — provides
		// true multi-instance with one llama-server per
		// platform/device for zero contention.
		if sp.config.UseLlamaCpp {
			poolCfg.InferenceBackend = visionremote.BackendLlamaCpp
			poolCfg.Shared = false // dedicated instance per slot
			poolCfg.BasePort = 8090
			poolCfg.LlamaCpp = &visionremote.LlamaCppConfig{
				Host:        sp.config.VisionHost,
				User:        sp.config.VisionUser,
				RepoDir:     "~/llama.cpp",
				ModelPath:   sp.config.LlamaCppModelPath,
				MMProjPath:  sp.config.LlamaCppMMProjPath,
				BasePort:    8090,
				GPULayers:   -1,
				ContextSize: 8192,
			}
			fmt.Printf(
				"[pipeline] Using llama.cpp backend " +
					"(dedicated instances)\n",
			)
		}

		visionPool = visionremote.NewVisionPool(poolCfg)

		// Free GPU by stopping Ollama if configured.
		// This allows MiniCPM-V to use the full GPU.
		if sp.config.LlamaCppFreeGPU &&
			sp.config.UseLlamaCpp &&
			poolCfg.LlamaCpp != nil {
			deployer := visionremote.NewLlamaCppDeployer(
				*poolCfg.LlamaCpp,
			)
			deployer.FreeGPU(ctx)
		}

		if err := visionPool.EnsureReady(ctx); err != nil {
			fmt.Printf(
				"[pipeline] warning: vision pool "+
					"failed: %v (continuing without)\n",
				err,
			)
			visionPool = nil
		} else {
			// Build slot targets — one per device for Android,
			// one per non-Android platform.
			var targets []visionremote.SlotTarget
			for _, platform := range sp.config.Platforms {
				if (platform == "android" ||
					platform == "androidtv") &&
					len(sp.config.AndroidDevices) > 0 {
					// One slot per Android device.
					for _, dev := range sp.config.AndroidDevices {
						targets = append(targets,
							visionremote.SlotTarget{
								Platform: platform,
								Device:   dev,
							},
						)
					}
					continue
				}
				device := ""
				if platform == "android" ||
					platform == "androidtv" {
					device = sp.config.AndroidDevice
				} else if platform == "web" {
					device = sp.config.WebURL
				} else if platform == "api" {
					device = "api"
				}
				targets = append(targets,
					visionremote.SlotTarget{
						Platform: platform,
						Device:   device,
					},
				)
			}
			visionPool.AssignSlots(targets)
			fmt.Printf(
				"[pipeline] %d vision slots assigned\n",
				visionPool.Size(),
			)
		}
	}

	// ── Phase 0b: ADB reverse proxy for ALL Android devices
	// Ensure every connected device can reach the API at
	// localhost:8080 via ADB reverse proxy.
	allDevices := sp.config.AndroidDevices
	if len(allDevices) == 0 && sp.config.AndroidDevice != "" {
		allDevices = []string{sp.config.AndroidDevice}
	}

	// FIX-QA-2026-04-21-015 (device-pollution guard): snapshot the
	// sensitive system settings (font_scale, brightness, rotation…)
	// BEFORE any phase runs, and register a defer to restore them
	// verbatim when Run() returns. Addresses a 2026-04-21 operator
	// report that two consecutive sessions had left the devices
	// with system font_scale=2.0 (LLM-driven curiosity presumably
	// wandered into Settings → Accessibility). This is defence in
	// depth — the LLM still shouldn't navigate into device settings,
	// but if it does, the device never stays polluted.
	//
	// FIX-QA-2026-04-29-018 (signal-safe restore): defer alone
	// doesn't fire on SIGKILL or unrecovered panics. Register a
	// signal handler that restores on SIGINT/SIGTERM/SIGHUP too, so
	// operator Ctrl-C / `pkill -TERM helixqa` still leaves the
	// device clean. Fixes a 2026-04-29 user-reported repeat of the
	// same font_scale=2.0 pollution when an in-progress session was
	// stopped via SIGTERM.
	var snapshots []*devicePreservedSettings
	for _, device := range allDevices {
		snap, err := captureDeviceSettings(ctx, device)
		if err != nil {
			fmt.Printf(
				"[pipeline] warning: capture settings on %s: %v\n",
				device, err,
			)
			continue
		}
		snapshots = append(snapshots, snap)
		defer snap.restore(context.Background())
	}
	if len(snapshots) > 0 {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
		go func() {
			s, ok := <-sigCh
			if !ok {
				return
			}
			fmt.Printf("[pipeline] caught %s — running device-preserve restore before exit\n", s)
			for _, snap := range snapshots {
				snap.restore(context.Background())
			}
			// Re-raise the signal with the default disposition so
			// the parent shell still sees the expected exit status.
			signal.Reset(s)
			_ = syscall.Kill(syscall.Getpid(), s.(syscall.Signal))
		}()
	}

	for _, device := range allDevices {
		revCtx, revCancel := context.WithTimeout(
			ctx, 10*time.Second,
		)
		out, err := osexec.CommandContext(
			revCtx, "adb", "-s", device,
			"reverse", "tcp:8080", "tcp:8080",
		).CombinedOutput()
		revCancel()
		if err != nil {
			fmt.Printf(
				"[pipeline] warning: ADB reverse "+
					"on %s failed: %v (%s)\n",
				device, err, string(out),
			)
		} else {
			fmt.Printf(
				"[pipeline] ADB reverse proxy "+
					"set on %s\n",
				device,
			)
		}
		// Also launch the app on this device.
		if sp.config.AndroidPackage != "" {
			launchCtx, lc := context.WithTimeout(
				ctx, 10*time.Second,
			)
			// Launch with a clean intent — see structured_executor.go
			// for the full justification of why qa_username/
			// qa_password extras are forbidden (HelixQA's
			// "Fully Autonomous LLM-Driven QA" + the consuming
			// project's "Universal Solution Principle").
			args := []string{
				"-s", device, "shell", "am", "start",
				"-n", sp.config.AndroidPackage +
					"/.ui.MainActivity",
			}
			fmt.Printf(
				"[pipeline] launching %s on %s "+
					"(LLM-driven login, no bypass extras)\n",
				sp.config.AndroidPackage, device,
			)
			_, _ = osexec.CommandContext(
				launchCtx, "adb", args...,
			).CombinedOutput()
			lc()
		}
	}

	// ── Phase 1: Learn ──────────────────────────────────
	sp.setPhase("learn")
	phaseStart := time.Now()
	fmt.Println("[pipeline] Phase 1/4: Learn")
	conduit.PhaseStart(sp.sink(), "learn", "")
	kb, err := learning.BuildKnowledgeBase(
		sp.config.ProjectRoot, sp.store,
	)
	if err != nil {
		// The project could not be read: environment, not an app verdict.
		result.Status = StatusBlocked
		result.Error = fmt.Sprintf("learn phase: %v", err)
		result.Duration = time.Since(start)
		sp.updateSession(sessionID, result)
		return result, nil
	}
	fmt.Printf("[pipeline]   %s\n", kb.Summary())

	// Build knowledge context for navigation prompts.
	// This injects project-specific details (credentials,
	// screens, constraints) discovered by the Learn phase
	// into the generic navigation prompts.
	var kbParts []string
	// Credentials from .env — most important for login.
	if len(kb.Credentials) > 0 {
		kbParts = append(kbParts,
			"LOGIN CREDENTIALS (from project .env):")
		for k, v := range kb.Credentials {
			kbParts = append(kbParts,
				fmt.Sprintf("  %s = %s", k, v))
		}
		// Make it explicit for the LLM.
		user := kb.Credentials["ADMIN_USERNAME"]
		if user == "" {
			user = kb.Credentials["USERNAME"]
		}
		pass := kb.Credentials["ADMIN_PASSWORD"]
		if pass == "" {
			pass = kb.Credentials["PASSWORD"]
		}
		if user != "" && pass != "" {
			kbParts = append(kbParts,
				fmt.Sprintf(
					"USE THESE CREDENTIALS: "+
						"username='%s' password='%s'",
					user, pass))
		}
	}
	if len(kb.Constraints) > 0 {
		kbParts = append(kbParts,
			"PROJECT CONSTRAINTS:")
		for _, c := range kb.Constraints {
			kbParts = append(kbParts, "- "+c)
		}
	}
	if len(kb.Screens) > 0 {
		var screenNames []string
		for _, s := range kb.Screens {
			screenNames = append(screenNames, s.Name)
		}
		kbParts = append(kbParts,
			"KNOWN SCREENS: "+strings.Join(
				screenNames, ", "))
	}
	sp.kbContext = strings.Join(kbParts, "\n")
	// Store credentials in config for app auto-login.
	if len(kb.Credentials) > 0 {
		sp.config.QACredentials = kb.Credentials
	}
	if sp.kbContext != "" {
		fmt.Printf(
			"[pipeline]   KB context: %d chars\n",
			len(sp.kbContext),
		)
	}

	fmt.Printf(
		"[pipeline]   Learn completed in %v\n",
		time.Since(phaseStart).Round(time.Millisecond),
	)

	// Store learned knowledge in cognitive memory for future sessions
	cogMem := memory.NewCognitiveMemory(sp.store, nil) // nil provider = SQLite-only
	cogMem.Remember(ctx, memory.MemoryEntry{
		ID:      fmt.Sprintf("learn-%s", sessionID),
		Content: kb.Summary(),
		Type:    "fact",
		Source:  "learning-phase",
		Session: sessionID,
	})

	// ── Phase 2: Plan ───────────────────────────────────
	sp.setPhase("plan")
	phaseStart = time.Now()
	conduit.PhaseComplete(sp.sink(), "learn", 0)
	fmt.Println("[pipeline] Phase 2/4: Plan")
	conduit.PhaseStart(sp.sink(), "plan", "")
	gen := planning.NewTestPlanGenerator(sp.selectProviderForPhase("plan"))
	plan, err := gen.Generate(
		ctx, kb, sp.config.Platforms,
	)
	if err != nil {
		// Provider outage / timeout while planning: no test ran.
		result.Status = StatusBlocked
		result.Error = fmt.Sprintf("plan phase: %v", err)
		result.Duration = time.Since(start)
		sp.updateSession(sessionID, result)
		return result, nil
	}

	// Reconcile with bank if configured.
	if sp.config.BanksDir != "" {
		reconciler := planning.NewBankReconciler()
		if _, err := os.Stat(sp.config.BanksDir); err == nil {
			if loadErr := reconciler.LoadBankDir(
				sp.config.BanksDir,
			); loadErr == nil {
				plan.Tests = reconciler.Reconcile(plan.Tests)
			}
		}
	}

	// Rank by priority.
	ranker := planning.NewPriorityRanker(nil)
	plan.Tests = ranker.Rank(plan.Tests)

	result.TestsPlanned = len(plan.Tests)
	fmt.Printf(
		"[pipeline]   %d tests planned in %v\n",
		result.TestsPlanned,
		time.Since(phaseStart).Round(time.Millisecond),
	)

	// ── Phase 3: Execute ────────────────────────────────
	sp.setPhase("execute")
	phaseStart = time.Now()
	conduit.PhaseComplete(sp.sink(), "plan", 0)
	fmt.Println("[pipeline] Phase 3/4: Execute")
	conduit.PhaseStart(sp.sink(), "execute", "")

	// Create executor factory from config.
	// Fall back to the first auto-detected device if AndroidDevice
	// is not explicitly configured.
	androidDevice := sp.config.AndroidDevice
	if androidDevice == "" && len(sp.config.AndroidDevices) > 0 {
		androidDevice = sp.config.AndroidDevices[0]
	}
	execFactory := NewRealExecutorFactory(RealExecutorConfig{
		AndroidDevice:  androidDevice,
		AndroidPackage: sp.config.AndroidPackage,
		WebURL:         sp.config.WebURL,
		DesktopDisplay: sp.config.DesktopDisplay,
	})

	// Clear logcat for clean baseline (with timeout).
	if sp.config.AndroidDevice != "" {
		for _, platform := range sp.config.Platforms {
			if platform == "android" ||
				platform == "androidtv" {
				logcatCtx, logcatCancel :=
					context.WithTimeout(
						ctx, logcatTimeout,
					)
				_ = osexec.CommandContext(
					logcatCtx, "adb", "-s",
					sp.config.AndroidDevice,
					"logcat", "-c",
				).Run()
				logcatCancel()
				fmt.Println(
					"  [exec] logcat cleared",
				)
			}
		}
	}

	// Start video recording for ALL platforms.
	// Android uses ADB screenrecord, Web/Desktop use ffmpeg X11 capture.
	recorders := make(map[string]*video.ScrcpyRecorder)
	ffmpegRecorders := make(map[string]*video.FFmpegRecorder)

	for _, platform := range sp.config.Platforms {
		videoPath := filepath.Join(
			sp.config.OutputDir, "videos",
			platform+"-session.mp4",
		)
		if mkErr := os.MkdirAll(
			filepath.Dir(videoPath), 0o755,
		); mkErr != nil {
			fmt.Printf(
				"  [exec] mkdir for video failed: %v\n",
				mkErr,
			)
			continue
		}

		switch platform {
		case "android", "androidtv":
			// FIX-QA-2026-04-21-016: when only AndroidDevices[] is
			// populated (multi-device config via .devconnect), the
			// singular AndroidDevice is "". Passing "" to the recorder
			// resulted in `adb -s "" shell screenrecord` → "more than
			// one device/emulator" and the segment loop hit its
			// 5-failure fuse. Fall back to the first entry in the
			// detected-devices slice.
			recordDevice := sp.config.AndroidDevice
			if recordDevice == "" && len(sp.config.AndroidDevices) > 0 {
				recordDevice = sp.config.AndroidDevices[0]
			}
			if recordDevice == "" {
				fmt.Printf(
					"  [exec] video recording skipped for %s: no device\n",
					platform,
				)
				continue
			}
			rec := video.NewScrcpyRecorder(
				recordDevice, videoPath,
				video.WithMethod(
					video.MethodADBScreenrecord,
				),
			)
			if err := rec.Start(ctx); err == nil {
				recorders[platform] = rec
				fmt.Printf(
					"  [exec] video recording "+
						"started for %s (adb)\n",
					platform,
				)
			} else {
				fmt.Printf(
					"  [exec] video recording "+
						"failed for %s: %v\n",
					platform, err,
				)
			}

		case "web", "desktop", "wizard":
			// Use ffmpeg for X11 screen capture
			rec := video.NewFFmpegRecorder(videoPath)
			if err := rec.Start(ctx); err == nil {
				ffmpegRecorders[platform] = rec
				fmt.Printf(
					"  [exec] video recording "+
						"started for %s (ffmpeg)\n",
					platform,
				)
			} else {
				fmt.Printf(
					"  [exec] video recording "+
						"failed for %s: %v\n",
					platform, err,
				)
			}
		}
	}

	// Collect baseline performance metrics.
	var perfTimelines []*performance.MetricsTimeline
	for _, platform := range sp.config.Platforms {
		if platform == "android" ||
			platform == "androidtv" {
			collector := performance.New(
				sp.config.AndroidPackage, platform,
			)
			tl := &performance.MetricsTimeline{
				Platform: platform,
			}
			if snap, err := collector.CollectMemory(
				ctx,
			); err == nil {
				tl.Add(snap)
			}
			if snap, err := collector.CollectCPU(
				ctx,
			); err == nil {
				tl.Add(snap)
			}
			perfTimelines = append(perfTimelines, tl)
		}
	}

	// Run Maestro flows if available (with per-flow
	// timeout).
	var allFindings []analysis.AnalysisFinding
	maestroDir := filepath.Join(
		sp.config.ProjectRoot,
		"challenges", "helixqa-banks",
	)
	if entries, err := os.ReadDir(maestroDir); err == nil {
		runner := maestro.NewFlowRunner()
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasSuffix(name, ".yaml") &&
				!strings.HasSuffix(name, ".yml") {
				continue
			}
			flowPath := filepath.Join(
				maestroDir, name,
			)
			content, err := os.ReadFile(flowPath)
			if err != nil {
				continue
			}
			cs := string(content)
			if !strings.Contains(cs, "appId") &&
				!strings.Contains(
					cs, "- launchApp",
				) {
				continue
			}

			fmt.Printf(
				"  [exec] Maestro flow: %s\n", name,
			)
			flowCtx, flowCancel :=
				context.WithTimeout(
					ctx, perMaestroFlowTimeout,
				)
			flowResult, flowErr := runner.RunFlow(
				flowCtx, flowPath,
				sp.config.AndroidDevice,
			)
			flowCancel()

			if flowErr != nil {
				fmt.Printf(
					"  [exec] Maestro flow %s "+
						"error: %v\n",
					name, flowErr,
				)
			}
			if flowResult != nil &&
				!flowResult.Success {
				allFindings = append(
					allFindings,
					analysis.AnalysisFinding{
						Category: analysis.CategoryFunctional,
						Severity: analysis.SeverityHigh,
						Title: fmt.Sprintf(
							"Maestro flow failed: %s",
							name,
						),
						Description: fmt.Sprintf(
							"Passed: %d, Failed: %d\n"+
								"Output: %s",
							flowResult.Passed,
							flowResult.Failed,
							flowResult.Output,
						),
						Platform: "android",
					},
				)
			}
		}
	}

	// Iterate tests: take screenshots, record coverage.
	// Each test gets its own timeout so a hung ADB
	// command cannot block the entire pipeline.
	screenshotDir := filepath.Join(
		sp.config.OutputDir, "screenshots",
	)
	if mkErr := os.MkdirAll(screenshotDir, 0o755); mkErr != nil {
		fmt.Printf("  [exec] mkdir screenshots failed: %v\n", mkErr)
	}
	var allScreenshots []string

	testsRun := 0
	testsSkipped := 0
	for _, t := range plan.Tests {
		select {
		case <-ctx.Done():
			fmt.Printf(
				"  [exec] pipeline context expired "+
					"after %d/%d tests "+
					"(elapsed %v)\n",
				testsRun, len(plan.Tests),
				time.Since(start).Round(
					time.Millisecond,
				),
			)
			// The session ran out of time: the remaining tests were
			// never judged — BLOCKED, not FAILED.
			result.Status = StatusBlocked
			result.Error = fmt.Sprintf(
				"context canceled during execution "+
					"after %d/%d tests",
				testsRun, len(plan.Tests),
			)
			result.TestsRun = testsRun
			result.Duration = time.Since(start)
			sp.updateSession(sessionID, result)
			// Stop recorders before returning.
			for _, rec := range recorders {
				_ = rec.Stop()
			}
			for _, rec := range ffmpegRecorders {
				_ = rec.Stop()
			}
			return result, nil
		default:
		}

		testsRun++
		testStart := time.Now()
		fmt.Printf(
			"  [%d/%d] %s (%s) ...\n",
			testsRun, len(plan.Tests),
			t.Name, t.Category,
		)

		// Per-test timeout context.
		testCtx, testCancel := context.WithTimeout(
			ctx, perTestTimeout,
		)

		// Take screenshot for each platform this test
		// targets.
		for _, platform := range t.Platforms {
			executor, err := execFactory.Create(
				platform,
			)
			if err != nil {
				fmt.Printf(
					"    [%s] executor error: %v\n",
					platform, err,
				)
				continue
			}

			// CRITICAL: Wait for UI to render before screenshot
			// Especially important after app launch/cold start
			time.Sleep(1500 * time.Millisecond)

			ssStart := time.Now()
			screenshot, err :=
				executor.Screenshot(testCtx)
			ssDur := time.Since(ssStart)
			if err != nil {
				fmt.Printf(
					"    [%s] screenshot failed "+
						"(%v): %v\n",
					platform, ssDur.Round(
						time.Millisecond,
					), err,
				)
				testsSkipped++
				continue
			}
			if len(screenshot) == 0 {
				fmt.Printf(
					"    [%s] screenshot empty "+
						"(%v)\n",
					platform, ssDur.Round(
						time.Millisecond,
					),
				)
				continue
			}
			// Check if screenshot is blank/uniform color
			if IsBlankScreenshot(screenshot) {
				fmt.Printf(
					"    [%s] screenshot appears blank/uniform, "+
						"skipping (%v)\n",
					platform, ssDur.Round(time.Millisecond),
				)
				continue
			}
			fmt.Printf(
				"    [%s] screenshot OK "+
					"(%dKB, %v)\n",
				platform,
				len(screenshot)/1024,
				ssDur.Round(time.Millisecond),
			)

			fname := filepath.Join(
				screenshotDir,
				fmt.Sprintf(
					"%s-%03d-%s.png",
					platform,
					testsRun,
					sanitizeFilename(t.Screen),
				),
			)
			if wErr := os.WriteFile(
				fname, screenshot, 0o644,
			); wErr != nil {
				fmt.Printf(
					"    [%s] write screenshot failed: %v\n",
					platform, wErr,
				)
			}
			allScreenshots = append(
				allScreenshots, fname,
			)

			// Check for crashes on Android.
			if (platform == "android" ||
				platform == "androidtv") &&
				sp.config.AndroidPackage != "" {
				det := detector.New(
					config.PlatformAndroid,
					detector.WithCommandRunner(
						detector.NewExecRunner(),
					),
					detector.WithPackageName(
						sp.config.AndroidPackage,
					),
				)
				dr, derr := det.Check(testCtx)
				if derr == nil && dr != nil &&
					(dr.HasCrash || dr.HasANR) {
					crashType := "crash"
					if dr.HasANR {
						crashType = "ANR"
					}
					fmt.Printf(
						"    [%s] %s detected!\n",
						platform, crashType,
					)
					allFindings = append(
						allFindings,
						analysis.AnalysisFinding{
							Category: analysis.CategoryFunctional,
							Severity: analysis.SeverityCritical,
							Title: fmt.Sprintf(
								"App %s detected "+
									"during test: %s",
								crashType, t.Name,
							),
							Description: fmt.Sprintf(
								"Stack trace: %s\n"+
									"Log entries: %v",
								dr.StackTrace,
								dr.LogEntries,
							),
							Platform: platform,
							Screen:   t.Screen,
						},
					)
				} else if derr != nil {
					fmt.Printf(
						"    [%s] crash check "+
							"error: %v\n",
						platform, derr,
					)
				}
			}
		}

		testCancel()

		// Record coverage.
		screen := t.Screen
		if screen == "" {
			screen = t.Name
		}
		for _, p := range t.Platforms {
			if covErr := sp.store.RecordCoverage(
				screen, p, "executed",
			); covErr != nil {
				fmt.Printf(
					"    [coverage] record failed: %v\n",
					covErr,
				)
			}
		}

		fmt.Printf(
			"  [%d/%d] done in %v\n",
			testsRun, len(plan.Tests),
			time.Since(testStart).Round(
				time.Millisecond,
			),
		)
	}
	result.TestsRun = testsRun

	// NOTE: Video recorders are NOT stopped here. They continue
	// recording through the Curiosity phase so the video captures
	// the full QA session (Execute + Curiosity). Recorders are
	// stopped after Curiosity, before the Analyze phase.

	// Collect logcat (with dedicated timeout).
	if sp.config.AndroidDevice != "" {
		for _, platform := range sp.config.Platforms {
			if platform == "android" ||
				platform == "androidtv" {
				logcatPath := filepath.Join(
					sp.config.OutputDir, "evidence",
					platform+"-logcat.txt",
				)
				if mkErr := os.MkdirAll(
					filepath.Dir(logcatPath), 0o755,
				); mkErr != nil {
					fmt.Printf(
						"  [exec] mkdir logcat failed: %v\n",
						mkErr,
					)
				}
				lcCtx, lcCancel :=
					context.WithTimeout(
						ctx, logcatTimeout,
					)
				out, err := osexec.CommandContext(
					lcCtx, "adb", "-s",
					sp.config.AndroidDevice,
					"logcat", "-d",
				).Output()
				lcCancel()
				if err == nil {
					if wErr := os.WriteFile(
						logcatPath, out, 0o644,
					); wErr != nil {
						fmt.Printf(
							"  [exec] write logcat failed: %v\n",
							wErr,
						)
					}
					fmt.Printf(
						"  [exec] logcat saved "+
							"(%dKB)\n",
						len(out)/1024,
					)
				} else {
					fmt.Printf(
						"  [exec] logcat failed: "+
							"%v\n", err,
					)
				}
			}
		}
	}

	// Collect final performance metrics.
	for _, tl := range perfTimelines {
		collector := performance.New(
			sp.config.AndroidPackage, tl.Platform,
		)
		if snap, err := collector.CollectMemory(
			ctx,
		); err == nil {
			tl.Add(snap)
		}
		if snap, err := collector.CollectCPU(
			ctx,
		); err == nil {
			tl.Add(snap)
		}
	}

	fmt.Printf(
		"[pipeline]   %d tests executed, "+
			"%d skipped, Execute took %v\n",
		testsRun, testsSkipped,
		time.Since(phaseStart).Round(time.Millisecond),
	)

	// ── Phase 3.5: Structured Test Bank Execution ──────
	sp.setPhase("structured")
	fmt.Println("[pipeline] Phase 3.5: Structured test bank execution")
	sp.runStructuredPhase(
		ctx,
		execFactory,
		sp.selectProviderForPhase("structured"),
		result,
		func(f analysis.AnalysisFinding) {
			allFindings = append(allFindings, f)
		},
		func(platform string, data []byte) {
			allScreenshots = append(allScreenshots, platform)
			_ = data
		},
	)

	// ── Phase 3.6: Curiosity-Driven Exploration ────────
	if sp.config.CuriosityEnabled {
		sp.setPhase("curiosity")
		phaseStart = time.Now()
		curiosityBudget := sp.config.CuriosityTimeout
		fmt.Printf(
			"[pipeline] Phase 3.5: "+
				"Curiosity-driven exploration "+
				"(budget %v)\n",
			curiosityBudget,
		)
		curiosityCtx, curiosityCancel :=
			context.WithTimeout(ctx, curiosityBudget)

		// Start the process controller watchdog for the
		// curiosity phase. It monitors step heartbeats
		// and kills stuck steps independently of the
		// per-step context timeout.
		if sp.processController != nil {
			sp.processController.Start(curiosityCtx)
		}
		defer curiosityCancel()

		preCuriosityCount := len(allScreenshots)

		// Build list of curiosity targets — one entry per
		// device for Android, one per non-Android platform.
		type curiosityTarget struct {
			platform string
			device   string
			pkg      string // Android package name for foreground detection
		}
		var curTargets []curiosityTarget
		for _, platform := range sp.config.Platforms {
			if (platform == "android" ||
				platform == "androidtv") &&
				len(sp.config.AndroidDevices) > 0 {
				for _, dev := range sp.config.AndroidDevices {
					curTargets = append(curTargets,
						curiosityTarget{platform, dev, sp.config.AndroidPackage},
					)
				}
			} else {
				dev := sp.config.AndroidDevice
				if platform == "web" {
					dev = sp.config.WebURL
				} else if platform == "api" {
					dev = "api"
				}
				curTargets = append(curTargets,
					curiosityTarget{platform, dev, sp.config.AndroidPackage},
				)
			}
		}

		fmt.Printf(
			"  [curiosity] %d targets: ",
			len(curTargets),
		)
		for _, ct := range curTargets {
			fmt.Printf("%s(%s) ", ct.platform, ct.device)
		}
		fmt.Println()

		// Launch app on all Android devices with auto-login.
		// Force-stop first to ensure intent extras are read.
		for _, ct := range curTargets {
			if (ct.platform == "android" ||
				ct.platform == "androidtv") &&
				sp.config.AndroidPackage != "" {
				// Force-stop so intent extras are re-read.
				stopCtx, sc := context.WithTimeout(
					ctx, 5*time.Second,
				)
				_, _ = osexec.CommandContext(
					stopCtx, "adb", "-s", ct.device,
					"shell", "am", "force-stop",
					sp.config.AndroidPackage,
				).CombinedOutput()
				sc()
				// REDUCED for FLASHING FAST performance (was 1s).
				time.Sleep(200 * time.Millisecond)

				launchCtx, lc := context.WithTimeout(
					ctx, 10*time.Second,
				)
				// Clean launch — qa_username/qa_password
				// extras are forbidden (see structured_executor.go
				// for the full justification: HelixQA's
				// LLM-driven constitution + the app-side
				// Universal Solution Principle).
				args := []string{
					"-s", ct.device, "shell", "am", "start",
					"-n", sp.config.AndroidPackage +
						"/.ui.MainActivity",
				}
				_, _ = osexec.CommandContext(
					launchCtx, "adb", args...,
				).CombinedOutput()
				lc()
				// REDUCED for FLASHING FAST performance (was 5s).
				time.Sleep(1 * time.Second)
				fmt.Printf(
					"  [curiosity] launched %s on %s\n",
					sp.config.AndroidPackage, ct.device,
				)
			}
		}

		// Run curiosity on each target sequentially.
		// (Parallel would overload single llama-server.)
		for _, ct := range curTargets {
			platform := ct.platform
			device := ct.device

			// Create executor for this specific device.
			var executor navigator.ActionExecutor
			var err error
			if (platform == "android" ||
				platform == "androidtv") && device != "" {
				executor = navigator.NewADBExecutor(
					device,
					detector.NewExecRunner(),
				)
			} else {
				executor, err = execFactory.Create(platform)
				if err != nil {
					continue
				}
			}

			// Per-target vision provider. Uses phase-aware
			// selection (or dedicated vision provider, or
			// shared AdaptiveProvider) by default. Only
			// overrides with llama-server when explicitly
			// configured.
			platformProvider := sp.selectProviderForPhase("curiosity")
			if visionPool != nil && sp.config.UseLlamaCpp {
				slot := visionPool.GetSlot(
					platform, device,
				)
				if slot != nil && slot.Endpoint != "" {
					slotProvider := llm.NewOpenAIProvider(
						llm.ProviderConfig{
							Name:    "llamacpp-" + slot.ID,
							BaseURL: slot.Endpoint,
							Model:   "llava",
						},
					)
					platformProvider = slotProvider
					fmt.Printf(
						"  [curiosity %s] using "+
							"dedicated vision: %s\n",
						platform, slot.Endpoint,
					)
				}
			}

			// stepHistory tracks actions from previous
			// steps so the LLM avoids repeating itself.
			var stepHistory []string

			// Determine the expected package name for this target.
			// Used to detect if the app has lost focus (e.g., user
			// pressed back too many times and landed on the launcher).
			expectedPkg := ct.pkg
			if expectedPkg == "" {
				expectedPkg = sp.config.AndroidPackage
			}

			// FIX-QA-2026-04-21-018 (Controller stagnation abort):
			// count consecutive "screen unchanged" steps. If the
			// device never reacts to any action for too long, the
			// session is worthless — abort the loop and surface the
			// failure loudly instead of running the full budget on a
			// frozen screen (operator-reported on 2026-04-21: 28 min
			// of video with only app close/reopen, zero UI motion).
			const maxConsecutiveUnchanged = 8
			consecutiveUnchanged := 0

			for i := 0; i < maxCuriositySteps; i++ {
				if curiosityCtx.Err() != nil {
					break
				}

				// Guard: verify ADB connection is alive.
				// Step 17 crash was caused by ADB dropping the
				// TCP connection to a Wi-Fi device. Reconnect
				// before attempting any ADB operation.
				if (platform == "android" || platform == "androidtv") && device != "" {
					pingOut, pingErr := osexec.CommandContext(
						curiosityCtx,
						"adb", "-s", device,
						"shell", "echo", "ping",
					).CombinedOutput()
					if pingErr != nil || !strings.Contains(string(pingOut), "ping") {
						fmt.Printf(
							"  [curiosity %s #%d] "+
								"ADB connection lost, reconnecting %s\n",
							platform, i+1, device,
						)
						_ = osexec.CommandContext(
							curiosityCtx,
							"adb", "disconnect", device,
						).Run()
						time.Sleep(1 * time.Second)
						_ = osexec.CommandContext(
							curiosityCtx,
							"adb", "connect", device,
						).Run()
						time.Sleep(2 * time.Second)
						// Re-establish reverse proxy.
						_ = osexec.CommandContext(
							curiosityCtx,
							"adb", "-s", device,
							"reverse", "tcp:8080", "tcp:8080",
						).Run()
					}
				}

				// Guard: verify the target app is still in the
				// foreground. If the LLM navigated away (e.g.,
				// pressed back to the launcher), relaunch the app.
				//
				// FIX-QA-2026-04-29-019 (Settings-trap blocker): when
				// the system Settings app is in the foreground, the
				// LLM has likely proposed a DPAD action that would
				// have toggled an Accessibility setting (font_scale,
				// brightness, density, etc.). Such actions persist
				// across sessions and pollute the operator's device.
				// We hard-block any further action this iteration —
				// relaunch the target app and `continue` so the next
				// LLM call screenshots the correct app, not Settings.
				// Without this `continue`, the guard relaunched the
				// app but then dispatched a now-mis-aligned action.
				if (platform == "android" || platform == "androidtv") && device != "" && expectedPkg != "" {
					fgOut, _ := osexec.CommandContext(
						curiosityCtx,
						"adb", "-s", device,
						"shell", "dumpsys", "window", "windows",
					).CombinedOutput()
					fgStr := string(fgOut)
					// Settings package names vary by OEM — match all
					// known patterns + a generic "settings" substring.
					inSettings := strings.Contains(fgStr, "com.android.tv.settings") ||
						strings.Contains(fgStr, "com.android.settings") ||
						strings.Contains(fgStr, "com.google.android.tvlauncher") &&
							strings.Contains(fgStr, "Settings")
					driftedAway := len(fgStr) > 0 && !strings.Contains(fgStr, expectedPkg)
					if inSettings || driftedAway {
						reason := "app not in foreground"
						if inSettings {
							reason = "device Settings detected in foreground (Article VIII guard)"
						}
						fmt.Printf(
							"  [curiosity %s #%d] %s — relaunching %s and skipping this step\n",
							platform, i+1, reason, expectedPkg,
						)
						// Clean relaunch — see structured_executor.go
						// for the qa_username/qa_password ban
						// rationale.
						launchArgs := []string{
							"-s", device, "shell", "am", "start",
							"-n", expectedPkg + "/.ui.MainActivity",
						}
						_, _ = osexec.CommandContext(
							curiosityCtx,
							"adb", launchArgs...,
						).CombinedOutput()
						time.Sleep(3 * time.Second)
						// CRITICAL: skip the rest of this iteration
						// so the LLM's now-stale action (generated
						// from the wrong screenshot) is NOT
						// dispatched. The next iteration will
						// screenshot the correct app and re-plan.
						continue
					}
				}

				// Per-step watchdog: if any single curiosity step
				// takes longer than 60s, cancel it and move on.
				// This prevents Gemini API hangs from stalling
				// the entire QA session.
				// Per-step budget: 90s accommodates thinking models
				// (Gemini 2.5 Flash: 10-45s LLM + 15s actions + 15s screenshot)
				stepCtx, stepCancel := context.WithTimeout(
					curiosityCtx, 90*time.Second,
				)
				defer stepCancel() // ensure cancel on every exit path

				// Register step with process controller.
				if sp.processController != nil {
					sp.processController.RegisterStep(
						"curiosity", platform, i+1,
						fmt.Sprintf("curiosity step %d", i+1),
						stepCancel,
					)
				}

				// Step 1: Take screenshot.
				stepStart := time.Now()
				screenshot, err :=
					executor.Screenshot(stepCtx)
				if err != nil || len(screenshot) == 0 {
					fmt.Printf(
						"  [curiosity %s #%d] "+
							"screenshot failed: %v\n",
						platform, i+1, err,
					)
					// Fall back to blind navigation.
					_ = executor.KeyPress(
						stepCtx,
						"KEYCODE_DPAD_DOWN",
					)
					time.Sleep(1 * time.Second)
					if sp.processController != nil {
						sp.processController.CompleteStep(
							"curiosity", platform, i+1,
						)
					}
					stepCancel()
					continue
				}
				// Check if screenshot is blank/uniform color
				if IsBlankScreenshot(screenshot) {
					fmt.Printf(
						"  [curiosity %s #%d] "+
							"screenshot appears blank, "+
							"skipping analysis\n",
						platform, i+1,
					)
					// Fall back to blind navigation.
					_ = executor.KeyPress(
						stepCtx,
						"KEYCODE_DPAD_DOWN",
					)
					time.Sleep(1 * time.Second)
					if sp.processController != nil {
						sp.processController.CompleteStep(
							"curiosity", platform, i+1,
						)
					}
					stepCancel()
					continue
				}

				fname := filepath.Join(
					screenshotDir,
					fmt.Sprintf(
						"%s-curiosity-%03d.png",
						platform, i+1,
					),
				)
				if wErr := os.WriteFile(
					fname, screenshot, 0o644,
				); wErr != nil {
					fmt.Printf(
						"  [curiosity %s #%d] write screenshot failed: %v\n",
						platform, i+1, wErr,
					)
				}
				allScreenshots = append(
					allScreenshots, fname,
				)

				// ── ScreenDiffer: detect unchanged screens ──
				if sp.screenDiffer != nil {
					diffResult := sp.screenDiffer.Compare(
						screenshot,
					)
					if diffResult.IsSame {
						consecutiveUnchanged++
						stepHistory = append(
							stepHistory,
							"WARNING: Previous action had "+
								"NO visible effect on the "+
								"screen. Try a DIFFERENT "+
								"action.",
						)
						fmt.Printf(
							"  [curiosity %s #%d] "+
								"screen unchanged "+
								"(similarity=%.2f, "+
								"consecutive=%d/%d)\n",
							platform, i+1,
							diffResult.Similarity,
							consecutiveUnchanged,
							maxConsecutiveUnchanged,
						)
						// FIX-QA-2026-04-21-018: abort if nothing
						// on the device has reacted for many
						// consecutive steps. The session is
						// testing nothing at that point.
						if consecutiveUnchanged >= maxConsecutiveUnchanged {
							fmt.Printf(
								"  [curiosity %s] ✗ STAGNATION ABORT: "+
									"%d consecutive steps with no "+
									"screen change — device is not "+
									"reacting to any action. "+
									"Check ADB + device state.\n",
								platform,
								consecutiveUnchanged,
							)
							// Surface as a critical finding so the
							// session report flags this as infra
							// failure rather than a clean run.
							// Appended to allFindings like every other
							// finding in this phase.
							allFindings = append(allFindings,
								analysis.AnalysisFinding{
									Category: analysis.CategoryFunctional,
									Severity: analysis.SeverityCritical,
									Title: fmt.Sprintf(
										"HelixQA stagnation on %s — "+
											"device did not react to "+
											"%d consecutive actions",
										platform,
										consecutiveUnchanged,
									),
									Description: "Screen hash was identical " +
										"across maxConsecutiveUnchanged " +
										"curiosity steps. Either ADB " +
										"commands are silently failing " +
										"(e.g., `cmd input` returning " +
										"'No shell command implementation.' " +
										"on Android 9) or the app is frozen. " +
										"See FIX-QA-2026-04-21-017/018.",
									Platform: platform,
								},
							)
							break
						}
					} else {
						// Screen changed — reset the run.
						consecutiveUnchanged = 0
					}
				}

				// ── ReplayBuffer: replay known sequences ────
				if sp.replayBuffer != nil {
					screenHash := replay.ScreenHash(
						screenshot,
					)
					if seq := sp.replayBuffer.FindMatch(
						screenHash, platform,
					); seq != nil &&
						seq.SuccessCount > 2 {
						fmt.Printf(
							"  [curiosity %s #%d] "+
								"replaying known sequence "+
								"(%d successes)\n",
							platform, i+1,
							seq.SuccessCount,
						)
						for _, a := range seq.Actions {
							_ = executeAction(
								stepCtx,
								executor,
								llmAction{
									Type:  a.Type,
									Value: a.Value,
								},
							)
							time.Sleep(
								500 * time.Millisecond,
							)
						}
						_ = sp.replayBuffer.MarkSuccess(
							seq.ID,
						)
						continue
					}
				}

				// ── DualScreen: capture UI tree for Android ─
				var uiContext string
				if (platform == "android" ||
					platform == "androidtv") &&
					device != "" &&
					sp.dualScreenCapturer != nil {
					capture, capErr :=
						sp.dualScreenCapturer.CaptureDualScreen(
							stepCtx, device,
						)
					if capErr == nil &&
						capture != nil &&
						capture.Combined != "" {
						uiContext = capture.Combined
					}
				}

				// Step 2: Send resized screenshot to
				// LLM for navigation guidance.
				if !platformProvider.SupportsVision() {
					// No vision provider available —
					// skip this step entirely. HelixQA
					// is fully autonomous and MUST NOT
					// use hardcoded navigation. Without
					// vision, curiosity cannot proceed.
					fmt.Printf(
						"  [curiosity %s #%d] "+
							"no vision provider — "+
							"skipping\n",
						platform, i+1,
					)
					break
				}

				// Inject UI tree context into the
				// history so the LLM knows about visible
				// elements beyond what is in the image.
				if uiContext != "" {
					stepHistory = append(
						stepHistory,
						"\nUI TREE CONTEXT:\n"+uiContext,
					)
				}

				// Resize before sending to LLM to
				// reduce latency and token cost.
				resized := resizeScreenshot(screenshot)

				// Acquire the platform's dedicated
				// vision slot to prevent contention
				// with other platforms' calls.
				var slot *visionremote.VisionSlot
				if visionPool != nil {
					slot = visionPool.GetSlot(
						platform,
						sp.config.AndroidDevice,
					)
					if slot != nil {
						slot.Lock()
					}
				}
				visionStart := time.Now()
				actions := sp.llmNavigate(
					stepCtx,
					resized,
					platform,
					i+1,
					stepHistory,
					platformProvider,
				)

				// If llama-server crashed (connection
				// refused), try to restart it via the pool.
				if len(actions) == 0 && visionPool != nil &&
					slot != nil {
					health, _ := http.Get(
						slot.Endpoint + "/health",
					)
					if health == nil ||
						health.StatusCode != 200 {
						fmt.Printf(
							"  [curiosity %s #%d] "+
								"vision server down,"+
								" restarting\n",
							platform, i+1,
						)
						if visionPool != nil &&
							sp.config.UseLlamaCpp {
							// Attempt restart via deployer
							cfg := sp.config
							deployer := visionremote.NewLlamaCppDeployer(
								visionremote.LlamaCppConfig{
									Host:        cfg.VisionHost,
									User:        cfg.VisionUser,
									RepoDir:     "~/llama.cpp",
									ModelPath:   cfg.LlamaCppModelPath,
									MMProjPath:  cfg.LlamaCppMMProjPath,
									BasePort:    slot.Port,
									ContextSize: 8192,
								},
							)
							deployer.StartInstance(
								stepCtx, slot.Port,
							)
							time.Sleep(10 * time.Second)
						}
					}
					if health != nil {
						health.Body.Close()
					}
				}
				if slot != nil {
					slot.RecordCall(
						time.Since(visionStart),
						nil,
					)
					slot.Unlock()
				}

				// Heartbeat after vision call.
				if sp.processController != nil &&
					len(actions) > 0 {
					sp.processController.Heartbeat(
						"curiosity", platform, i+1,
					)
				}

				// Step 3: Execute LLM-suggested actions.
				// If the LLM returned no actions (parse
				// error or empty response), retry up to 3
				// times with a fresh screenshot each time.
				// HelixQA is fully autonomous — NO
				// hardcoded fallback navigation.
				if len(actions) == 0 {
					retried := false
					for retryN := 1; retryN <= 3; retryN++ {
						if stepCtx.Err() != nil {
							break
						}
						fmt.Printf(
							"  [curiosity %s #%d] "+
								"empty actions, "+
								"retrying (%d/3)\n",
							platform, i+1, retryN,
						)
						// REDUCED for FLASHING FAST performance (was N seconds).
						time.Sleep(
							time.Duration(retryN) *
								200 * time.Millisecond,
						)
						retryShot, _ :=
							executor.Screenshot(
								stepCtx,
							)
						if len(retryShot) == 0 {
							continue
						}
						actions = sp.llmNavigate(
							stepCtx,
							resizeScreenshot(retryShot),
							platform,
							i+1,
							stepHistory,
							platformProvider,
						)
						if len(actions) > 0 {
							retried = true
							break
						}
					}
					if !retried && len(actions) == 0 {
						fmt.Printf(
							"  [curiosity %s #%d] "+
								"stuck: LLM returned "+
								"no actions after 3 "+
								"retries\n",
							platform, i+1,
						)
						continue
					}
				}

				var stepActions []string
				for _, action := range actions {
					if stepCtx.Err() != nil {
						break
					}
					execErr := executeAction(
						stepCtx,
						executor,
						action,
					)
					if execErr != nil {
						fmt.Printf(
							"  [curiosity %s #%d] "+
								"action %q "+
								"failed: %v\n",
							platform, i+1,
							action.Type, execErr,
						)
					} else {
						fmt.Printf(
							"  [curiosity %s #%d] "+
								"executed: %s "+
								"(%s)\n",
							platform, i+1,
							action.Type,
							action.Reason,
						)
					}
					desc := action.Type
					if action.Value != "" {
						desc += "(" + action.Value + ")"
					}
					stepActions = append(
						stepActions, desc,
					)
					// Pause between actions. Typing
					// and keyboard dismiss need extra
					// time on Android TV.
					// REDUCED for FLASHING FAST performance.
					switch action.Type {
					case "type":
						time.Sleep(500 * time.Millisecond)
					case "back":
						time.Sleep(500 * time.Millisecond)
					default:
						time.Sleep(200 * time.Millisecond)
					}
				}
				// Record what was done for context.
				stepHistory = append(
					stepHistory,
					fmt.Sprintf(
						"Step %d: %s",
						i+1,
						strings.Join(stepActions, ", "),
					),
				)

				// Take a post-action screenshot to capture the
				// result of the executed actions. This ensures we
				// have visual evidence of BOTH the before-state
				// (captured at top of loop) and the after-state
				// for every curiosity step.
				if len(stepActions) > 0 {
					// REDUCED for FLASHING FAST performance (was 500ms).
					time.Sleep(100 * time.Millisecond)
					postShot, postErr := executor.Screenshot(
						stepCtx,
					)
					if postErr == nil && len(postShot) > 0 {
						postFname := filepath.Join(
							screenshotDir,
							fmt.Sprintf(
								"%s-curiosity-%03d-after.png",
								platform, i+1,
							),
						)
						_ = os.WriteFile(
							postFname, postShot, 0o644,
						)
						allScreenshots = append(
							allScreenshots, postFname,
						)
					}
				}

				// ── Non-blocking recording: training + replay ──
				// These write to SQLite which can lock. Run with
				// a 5s deadline to prevent blocking the step.
				recDone := make(chan struct{}, 1)
				go func() {
					defer func() { recDone <- struct{}{} }()

					// ── TrainingCollector: record pair ───────
					if sp.trainingCollector != nil &&
						len(actions) > 0 {
						tActions := make(
							[]training.Action, len(actions),
						)
						for ai, a := range actions {
							tActions[ai] = training.Action{
								Type:   a.Type,
								Value:  a.Value,
								Reason: a.Reason,
							}
						}
						if tErr := sp.trainingCollector.Record(
							screenshot,
							tActions,
							platform,
							"curiosity",
							platformProvider.Name(),
							true,
						); tErr != nil {
							fmt.Printf(
								"  [training %s #%d] "+
									"record failed: %v\n",
								platform, i+1, tErr,
							)
						}
					}

					// ── ReplayBuffer: record sequence ───────
					if sp.replayBuffer != nil &&
						len(actions) > 0 {
						screenHash := replay.ScreenHash(
							screenshot,
						)
						recActions := make(
							[]replay.RecordedAction,
							len(actions),
						)
						for ai, a := range actions {
							recActions[ai] = replay.RecordedAction{
								Type:       a.Type,
								Value:      a.Value,
								ScreenHash: screenHash,
							}
						}
						_ = sp.replayBuffer.Record(
							replay.ActionSequence{
								ID: fmt.Sprintf(
									"%s-%s-%d",
									platform, device, i+1,
								),
								Platform: platform,
								Actions:  recActions,
							},
						)
					}

				}() // end goroutine
				// Wait up to 5s for recording, then proceed regardless
				select {
				case <-recDone:
				case <-time.After(5 * time.Second):
					fmt.Printf("  [curiosity %s #%d] recording timeout (5s)\n", platform, i+1)
				}

				// Complete step in process controller.
				if sp.processController != nil {
					sp.processController.CompleteStep(
						"curiosity", platform, i+1,
					)
				}

				stepCancel() // Release per-step watchdog

				// Check if controller recommends aborting.
				if sp.processController != nil &&
					sp.processController.ShouldAbortPhase(
						"curiosity",
					) {
					fmt.Printf(
						"  [controller] Aborting "+
							"curiosity phase on %s: "+
							"too many killed steps\n",
						platform,
					)
					break
				}

				// Check if step was killed by watchdog
				if stepCtx.Err() == context.DeadlineExceeded {
					fmt.Printf(
						"  [curiosity %s #%d] "+
							"WATCHDOG: step killed after 60s\n",
						platform, i+1,
					)
				} else {
					fmt.Printf(
						"  [curiosity %s #%d] "+
							"step done in %v\n",
						platform, i+1,
						time.Since(stepStart).Round(
							time.Millisecond,
						),
					)
				}
			}
		}
		fmt.Printf(
			"  Curiosity: captured %d additional "+
				"screenshots in %v\n",
			len(allScreenshots)-preCuriosityCount,
			time.Since(phaseStart).Round(
				time.Millisecond,
			),
		)

		// ── Process Controller summary ──────────────────
		if sp.processController != nil {
			sp.processController.Stop()
			fmt.Printf("  [%s]\n",
				sp.processController.Summary(),
			)
		}

		// ── ScreenDiffer stats ──────────────────────────
		if sp.screenDiffer != nil {
			same, diff := sp.screenDiffer.Stats()
			fmt.Printf(
				"  [screen-diff] %d unchanged, "+
					"%d changed screens\n",
				same, diff,
			)
		}

		// ── VisualRegression: cross-device comparison ───
		// When multiple devices were tested, initialize
		// visual regression and log infrastructure status.
		// Full pairwise comparison requires collecting
		// matching screenshots across devices at the same
		// step, which is complex — we set up the
		// infrastructure here and log readiness.
		if len(curTargets) > 1 {
			vrProvider := sp.selectProviderForPhase(
				"analyze",
			)
			if vrProvider.SupportsVision() {
				sp.visualRegression =
					regression.NewVisualRegression(
						&visionRegressionAdapter{
							provider: vrProvider,
						},
					)
				fmt.Printf(
					"  [visual-regression] "+
						"initialized for %d devices "+
						"(ready for cross-device "+
						"comparison)\n",
					len(curTargets),
				)
			}
		}

		// ── Training data export ────────────────────────
		if sp.trainingCollector != nil &&
			sp.trainingCollector.Len() > 0 {
			exportPath := filepath.Join(
				sp.config.OutputDir,
				"training-data",
				"training.jsonl",
			)
			n, exportErr := sp.trainingCollector.Export(
				exportPath,
			)
			if exportErr != nil {
				fmt.Printf(
					"  [training] export failed: %v\n",
					exportErr,
				)
			} else {
				fmt.Printf(
					"  [training] exported %d pairs "+
						"to %s\n",
					n, exportPath,
				)
			}
		}

		// ── ReplayBuffer stats ──────────────────────────
		if sp.replayBuffer != nil {
			fmt.Printf(
				"  [replay] %d sequences stored\n",
				sp.replayBuffer.Len(),
			)
		}

		// Validate that on-screen data matches API data.
		apiFindings := sp.validateAPIData(ctx)
		if len(apiFindings) > 0 {
			allFindings = append(
				allFindings, apiFindings...,
			)
			fmt.Printf(
				"  [data-validation] %d issues found\n",
				len(apiFindings),
			)
		}
	}

	// Stop video recorders AFTER Curiosity so the video captures
	// the full QA session (Execute + Curiosity). The killall -INT
	// on the device finalizes the MP4 moov atom before pulling.
	for p, rec := range recorders {
		if err := rec.Stop(); err != nil {
			fmt.Printf(
				"  [video] stop %s: %v\n", p, err,
			)
		} else {
			fmt.Printf(
				"  [video] stopped for %s\n", p,
			)
		}
	}

	// Stop ffmpeg recorders for web/desktop platforms
	for p, rec := range ffmpegRecorders {
		if err := rec.Stop(); err != nil {
			fmt.Printf(
				"  [video] stop %s (ffmpeg): %v\n", p, err,
			)
		} else {
			fmt.Printf(
				"  [video] stopped for %s (ffmpeg)\n", p,
			)
		}
	}

	// ── Phase 4: Analyze ────────────────────────────────
	sp.setPhase("analyze")
	phaseStart = time.Now()
	conduit.PhaseComplete(sp.sink(), "execute", 0)
	fmt.Println("[pipeline] Phase 4/4: Analyze")
	conduit.PhaseStart(sp.sink(), "analyze", "")

	// Analyze screenshots with LLM vision — bounded to
	// maxVisionScreenshots to prevent timeout. We select
	// evenly spaced screenshots for best coverage.
	analyzeVisionProvider := sp.selectProviderForPhase("analyze")
	if analyzeVisionProvider.SupportsVision() &&
		len(allScreenshots) > 0 {
		visionAnalyzer := analysis.NewVisionAnalyzer(
			analyzeVisionProvider,
		)
		toAnalyze := selectEvenly(
			allScreenshots, maxVisionScreenshots,
		)
		fmt.Printf(
			"  [analyze] analysing %d/%d "+
				"screenshots via LLM vision\n",
			len(toAnalyze), len(allScreenshots),
		)
		for i, ssPath := range toAnalyze {
			if ctx.Err() != nil {
				fmt.Printf(
					"  [analyze] context expired "+
						"after %d screenshots\n",
					i,
				)
				break
			}
			imgData, err := os.ReadFile(ssPath)
			if err != nil {
				continue
			}
			// Resize to reduce LLM latency.
			imgData = resizeScreenshot(imgData)
			base := filepath.Base(ssPath)

			vStart := time.Now()
			findings, err :=
				visionAnalyzer.AnalyzeScreenshot(
					ctx, imgData, base, "",
				)
			vDur := time.Since(vStart)
			if err != nil {
				fmt.Printf(
					"  [analyze] vision %s "+
						"failed (%v): %v\n",
					base, vDur.Round(
						time.Millisecond,
					), err,
				)
				continue
			}
			fmt.Printf(
				"  [analyze] vision %s: "+
					"%d findings (%v)\n",
				base, len(findings),
				vDur.Round(time.Millisecond),
			)
			allFindings = append(
				allFindings, findings...,
			)
		}
	}

	// Extract and analyze video frames — bounded.
	ffmpegPath := sp.config.FFmpegPath
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	extractor := video.NewFrameExtractor(ffmpegPath)
	videosDir := filepath.Join(
		sp.config.OutputDir, "videos",
	)
	framesDir := filepath.Join(
		sp.config.OutputDir, "frames",
	)

	if entries, err := os.ReadDir(videosDir); err == nil {
		for _, entry := range entries {
			if ctx.Err() != nil {
				break
			}
			if entry.IsDir() ||
				!strings.HasSuffix(
					entry.Name(), ".mp4",
				) {
				continue
			}
			videoPath := filepath.Join(
				videosDir, entry.Name(),
			)
			videoFramesDir := filepath.Join(
				framesDir,
				strings.TrimSuffix(
					entry.Name(), ".mp4",
				),
			)

			frames, err := extractor.ExtractFPS(
				ctx, videoPath, videoFramesDir, 1,
			)
			if err != nil {
				fmt.Printf(
					"  [analyze] frame extract "+
						"failed for %s: %v\n",
					entry.Name(), err,
				)
				continue
			}

			limit := maxVisionFrames
			if len(frames) < limit {
				limit = len(frames)
			}
			if analyzeVisionProvider.SupportsVision() && limit > 0 {
				va := analysis.NewVisionAnalyzer(
					analyzeVisionProvider,
				)
				for _, framePath := range frames[:limit] {
					if ctx.Err() != nil {
						break
					}
					imgData, err := os.ReadFile(
						framePath,
					)
					if err != nil {
						continue
					}
					imgData = resizeScreenshot(imgData)
					findings, err :=
						va.AnalyzeScreenshot(
							ctx, imgData,
							filepath.Base(framePath),
							"video-frame",
						)
					if err == nil {
						allFindings = append(
							allFindings,
							findings...,
						)
					}
				}
			}
			fmt.Printf(
				"  [analyze] %d frames from %s\n",
				limit, entry.Name(),
			)
		}
	}

	// Check for memory leaks.
	for _, tl := range perfTimelines {
		leak := tl.DetectMemoryLeak(10.0)
		if leak != nil && leak.IsLeak {
			allFindings = append(
				allFindings,
				analysis.AnalysisFinding{
					Category: analysis.CategoryPerformance,
					Severity: analysis.SeverityHigh,
					Title: fmt.Sprintf(
						"Memory leak detected on %s",
						leak.Platform,
					),
					Description: fmt.Sprintf(
						"Memory grew %.1f%% "+
							"(%.0fKB -> %.0fKB) "+
							"over %.0fs",
						leak.GrowthPercent,
						leak.StartKB,
						leak.EndKB,
						leak.DurationSecs,
					),
					Platform: leak.Platform,
				},
			)
		}
	}

	result.IssuesFound = len(appDefectFindings(allFindings))

	// ── BugReproducer: confirm high-severity bugs ───────
	// After analysis finds issues, attempt to reproduce
	// critical and high-severity ones using the LLM vision
	// provider to confirm they are real.
	if len(allFindings) > 0 {
		reproduceProvider := sp.selectProviderForPhase(
			"analyze",
		)
		if reproduceProvider.SupportsVision() {
			highSev := findingsToReproBugs(allFindings)
			if len(highSev) > 0 {
				// Create an executor for the first
				// available Android device (reproduction
				// needs a live device).
				var reproExec navigator.ActionExecutor
				if len(allDevices) > 0 {
					reproExec = navigator.NewADBExecutor(
						allDevices[0],
						detector.NewExecRunner(),
					)
				} else if sp.config.AndroidDevice != "" {
					reproExec = navigator.NewADBExecutor(
						sp.config.AndroidDevice,
						detector.NewExecRunner(),
					)
				}

				if reproExec != nil {
					fmt.Printf(
						"  [reproduce] Attempting to "+
							"reproduce %d high-severity "+
							"bugs...\n",
						len(highSev),
					)
					reproducer := reproduce.NewBugReproducer(
						reproExec, reproduceProvider,
					)
					reproCtx, reproCancel :=
						context.WithTimeout(
							ctx, 5*time.Minute,
						)
					for _, bug := range highSev {
						reproResult, reproErr :=
							reproducer.Reproduce(
								reproCtx, bug,
							)
						if reproErr != nil {
							fmt.Printf(
								"  [reproduce] %s: "+
									"error: %v\n",
								bug.ID, reproErr,
							)
							continue
						}
						if reproResult != nil &&
							reproResult.Reproduced {
							fmt.Printf(
								"  [reproduce] %s "+
									"CONFIRMED "+
									"(reproduced in %d "+
									"attempts)\n",
								bug.ID,
								reproResult.Attempts,
							)
						} else if reproResult != nil {
							fmt.Printf(
								"  [reproduce] %s "+
									"not reproduced "+
									"(%d attempts)\n",
								bug.ID,
								reproResult.Attempts,
							)
						}
					}
					reproCancel()
				}
			}
		}
	}

	// Create tickets via FindingsBridge.
	if len(allFindings) > 0 {
		bridge := NewFindingsBridge(
			sp.store, sp.config.IssuesDir, sessionID,
		)
		ids, _ := bridge.Process(allFindings)
		result.TicketsCreated = len(ids)
		fmt.Printf(
			"  Created %d issue tickets\n",
			len(ids),
		)
	}

	fmt.Printf(
		"[pipeline]   %d issues found, "+
			"Analyze took %v\n",
		result.IssuesFound,
		time.Since(phaseStart).Round(time.Millisecond),
	)

	// ── Shutdown vision pool ────────────────────────────
	if visionPool != nil {
		visionPool.Shutdown(ctx)
	}
	// Shutdown distributed RPC workers and master if active.
	if distributedMasterDeployer != nil {
		distributedMasterDeployer.StopInstance(
			ctx, distributedMasterPort,
		)
		fmt.Printf(
			"[pipeline] distributed master stopped "+
				"(port %d)\n",
			distributedMasterPort,
		)
	}
	for i, deployer := range distributedDeployers {
		if i < len(distributedRPCPorts) {
			deployer.StopRPCServer(
				ctx, distributedRPCPorts[i],
			)
		}
	}
	if len(distributedDeployers) > 0 {
		fmt.Printf(
			"[pipeline] %d distributed RPC workers "+
				"stopped\n",
			len(distributedDeployers),
		)
	}
	// Restore Ollama if we stopped it for GPU access.
	if sp.config.LlamaCppFreeGPU && sp.config.UseLlamaCpp {
		deployer := visionremote.NewLlamaCppDeployer(
			visionremote.LlamaCppConfig{
				Host: sp.config.VisionHost,
				User: sp.config.VisionUser,
			},
		)
		deployer.RestoreOllama(ctx)
	}

	// ── Finalize ────────────────────────────────────────
	conduit.PhaseComplete(sp.sink(), "analyze", 0)
	result.Status = StatusComplete
	result.Duration = time.Since(start)

	if result.TestsPlanned > 0 {
		result.CoveragePct = float64(result.TestsRun) /
			float64(result.TestsPlanned) * 100.0
	}

	// Attach cost summary to the result.
	if sp.costTracker != nil {
		costSummary := sp.costTracker.Summary()
		result.Cost = &costSummary
	}

	sp.updateSession(sessionID, result)

	fmt.Printf(
		"[pipeline] Complete: %d/%d tests, "+
			"%.1f%% coverage, %v total\n",
		result.TestsRun,
		result.TestsPlanned,
		result.CoveragePct,
		result.Duration.Round(time.Millisecond),
	)

	// Log cost summary.
	if result.Cost != nil && result.Cost.TotalCalls > 0 {
		fmt.Printf(
			"[pipeline] LLM cost: $%.6f "+
				"(%d calls, %d input tokens, "+
				"%d output tokens)\n",
			result.Cost.TotalCostUSD,
			result.Cost.TotalCalls,
			result.Cost.TotalInputTokens,
			result.Cost.TotalOutputTokens,
		)
		for provider, pc := range result.Cost.ByProvider {
			fmt.Printf(
				"[pipeline]   %s: $%.6f "+
					"(%d calls)\n",
				provider, pc.TotalCostUSD, pc.Calls,
			)
		}
	}

	return result, nil
}

// apiDataTimeout limits individual HTTP requests during
// API data validation so a slow or unreachable backend
// does not stall the pipeline.
const apiDataTimeout = 10 * time.Second

// validateAPIData makes HTTP requests to the application's
// backend API (whatever the project uses — HelixQA holds no
// project-specific URLs) to verify that data is available and
// consistent with what should appear on screen. It returns
// findings for any errors or empty responses that indicate a
// data mismatch between the API and the UI.
func (sp *SessionPipeline) validateAPIData(
	ctx context.Context,
) []analysis.AnalysisFinding {
	baseURL := "http://localhost:8080"
	if sp.config.WebURL != "" {
		baseURL = strings.TrimRight(
			sp.config.WebURL, "/",
		)
	}

	fmt.Printf(
		"[data-validation] Validating API data "+
			"at %s\n",
		baseURL,
	)

	client := &http.Client{Timeout: apiDataTimeout}
	var findings []analysis.AnalysisFinding

	// ── 0. Login first to get auth token ────────────
	var authToken string
	loginURL := baseURL + "/api/v1/auth/login"
	loginBody, _ := json.Marshal(map[string]string{
		"username": "admin",
		"password": "admin123",
	})
	loginReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, loginURL,
		bytes.NewReader(loginBody),
	)
	if err == nil {
		loginReq.Header.Set(
			"Content-Type", "application/json",
		)
		resp, err := client.Do(loginReq)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				// HXC-239: this used to decode a hardcoded
				// top-level `session_token` with no override and
				// no error path — against any server that names
				// the bearer differently (HelixCode returns it at
				// top-level `token`) authToken silently stayed
				// empty, every following request went out
				// UNAUTHENTICATED, and the resulting 401s were
				// reported as API defects with nothing in the
				// report pointing at auth. It now shares the
				// executor's candidate-based extractor and, when
				// no candidate resolves, raises a finding naming
				// every path tried and the keys actually present.
				//
				// Scope of that, precisely (§11.4.6): the
				// mismatch is now SURFACED and DIAGNOSABLE — it
				// is not prevented. Extraction failure is
				// non-fatal here: authToken stays empty, the
				// subsequent requests still go out
				// unauthenticated, and their 401s are STILL
				// filed as API defects alongside this finding.
				// The reader of a report now has the one entry
				// that explains them. Guarded by
				// TestHXC239_PipelineRaisesFindingOnUnusableToken,
				// which pins both halves.
				var decoded map[string]any
				if jErr := json.Unmarshal(
					body, &decoded,
				); jErr == nil {
					tok, tErr := extractLoginToken(
						decoded, defaultTokenField,
						defaultTokenFieldFallbacks,
					)
					if tErr == nil {
						authToken = tok
						fmt.Println(
							"[data-validation] login OK",
						)
					} else {
						fmt.Printf(
							"[data-validation] login "+
								"succeeded but no bearer "+
								"token found: %v\n", tErr,
						)
						findings = append(findings,
							analysis.AnalysisFinding{
								Category: analysis.CategoryFunctional,
								Severity: analysis.SeverityHigh,
								Title: "API login returned 200 but " +
									"no usable bearer token",
								Description: tErr.Error(),
								Platform:    "api",
							},
						)
					}
				} else {
					// HXC-267: the HXC-239 raise above lives INSIDE
					// the parse-OK branch, so the split it actually
					// covers is PARSE / NO-PARSE — not token-present
					// / token-absent. A 200 whose body does not
					// decode into a JSON object fell through here
					// with no token, no finding and no log line:
					// the pre-HXC-239 silence, one layer out. The
					// practical trigger is a proxy or gateway in
					// front of the real service answering an HTML
					// error page with status 200 — the 401s that
					// follow are then filed against the API under
					// test and the report blames the wrong system.
					//
					// Same residual behaviour as the sibling raise,
					// precisely (§11.4.6): the mismatch is SURFACED,
					// not prevented. authToken stays empty, the
					// following requests still go out
					// unauthenticated, and their 401s are STILL
					// filed as API defects — the reader of a report
					// now has the one entry that explains them.
					// Guarded by
					// TestHXC267_PipelineRaisesFindingOnUndecodableLoginBody,
					// which pins both halves.
					desc := undecodableLoginBodyDescription(
						jErr, body,
						resp.Header.Get("Content-Type"),
					)
					fmt.Printf(
						"[data-validation] login "+
							"succeeded but the response "+
							"body was unreadable: %s\n",
						desc,
					)
					findings = append(findings,
						analysis.AnalysisFinding{
							Category: analysis.CategoryFunctional,
							Severity: analysis.SeverityHigh,
							Title: "API login returned 200 but " +
								"the response body could not " +
								"be decoded",
							Description: desc,
							Platform:    "api",
						},
					)
				}
			} else {
				fmt.Printf(
					"[data-validation] login failed "+
						"with status %d\n",
					resp.StatusCode,
				)
				findings = append(findings,
					analysis.AnalysisFinding{
						Category: analysis.CategoryFunctional,
						Severity: analysis.SeverityHigh,
						Title: fmt.Sprintf(
							"API login failed with "+
								"status %d",
							resp.StatusCode,
						),
						// HXC-270 site 1 of 7, and the
						// highest-risk one by a wide margin:
						// this is a SIGN-IN reply, so whatever
						// the server put in the body was
						// recorded verbatim into a durable
						// finding. `string(body)` here echoed an
						// echoed-back password, a session cookie
						// rendered into the page, or an
						// Authorization header quoted back in a
						// stack trace, straight into a committed
						// report. Fifteen lines above, the
						// HXC-267 success path was already
						// careful — the discipline inverted
						// inside one function.
						Description: failedReplyDescription(
							"POST "+loginURL,
							resp.StatusCode, body,
							resp.Header.Get("Content-Type"),
						),
						Platform: "api",
					},
				)
			}
		} else {
			fmt.Printf(
				"[data-validation] login request "+
					"failed: %v\n", err,
			)
		}
	}

	// ── 1. Entity stats ─────────────────────────────
	statsURL := baseURL + "/api/v1/entities/stats"
	statsReq, err := http.NewRequestWithContext(
		ctx, http.MethodGet, statsURL, nil,
	)
	if err == nil {
		if authToken != "" {
			statsReq.Header.Set(
				"Authorization", "Bearer "+authToken,
			)
		}
		resp, err := client.Do(statsReq)
		if err != nil {
			fmt.Printf(
				"[data-validation] entities/stats "+
					"request failed: %v\n",
				err,
			)
			findings = append(findings,
				analysis.AnalysisFinding{
					Category: analysis.CategoryFunctional,
					Severity: analysis.SeverityHigh,
					Title: "API unreachable: " +
						"entities/stats",
					Description: fmt.Sprintf(
						"GET %s failed: %v",
						statsURL, err,
					),
					Platform: "api",
				},
			)
		} else {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				fmt.Printf(
					"[data-validation] entities/stats "+
						"returned %d\n",
					resp.StatusCode,
				)
				findings = append(findings,
					analysis.AnalysisFinding{
						Category: analysis.CategoryFunctional,
						Severity: analysis.SeverityHigh,
						Title: fmt.Sprintf(
							"API error: entities/stats "+
								"returned %d",
							resp.StatusCode,
						),
						// HXC-270 site 2 of 7. Lower risk than
						// the sign-in reply but mechanically
						// identical: the tested service's own
						// error body went verbatim into a
						// durable finding, and an error body is
						// exactly where a service tends to dump
						// request context.
						Description: failedReplyDescription(
							"GET "+statsURL,
							resp.StatusCode, body,
							resp.Header.Get("Content-Type"),
						),
						Platform: "api",
					},
				)
			} else {
				var statsResp struct {
					Total  int            `json:"total_entities"`
					ByType map[string]int `json:"by_type"`
				}
				if jErr := json.Unmarshal(
					body, &statsResp,
				); jErr == nil {
					fmt.Printf(
						"[data-validation] API has "+
							"%d entities",
						statsResp.Total,
					)
					if len(statsResp.ByType) > 0 {
						var parts []string
						for k, v := range statsResp.ByType {
							parts = append(parts,
								fmt.Sprintf("%s=%d", k, v),
							)
						}
						fmt.Printf(
							" (%s)",
							strings.Join(parts, ", "),
						)
					}
					fmt.Println()

					if statsResp.Total == 0 {
						findings = append(findings,
							analysis.AnalysisFinding{
								Category: analysis.CategoryFunctional,
								Severity: analysis.SeverityHigh,
								Title: "API returned zero " +
									"entities",
								Description: "entities/stats " +
									"reports total=0; the UI " +
									"should show data if the " +
									"backend has been populated",
								Platform: "api",
							},
						)
					}
				} else {
					// HXC-270 site 4 of 7 — MEASURED, not filed.
					// The item enumerated the three
					// `string(body)` descriptions in this
					// routine plus two in the shared HTTP
					// helper; it did not count the two decode
					// complaints sitting ten lines below two of
					// them. `%v` on a json error is the same
					// leak in cheaper clothing, measured on this
					// toolchain against THIS decode target:
					//
					//	<html>…</html>            → `invalid
					//	  character '<' looking for beginning
					//	  of value`
					//	0xff…                     → `'ÿ'`
					//	{"total_entities":1e999}  → `cannot
					//	  unmarshal number 1e999 into Go struct
					//	  field .total_entities of type
					//	  int` — a full literal, not one byte
					//	  (the decode target is an anonymous
					//	  struct, so the struct-name segment
					//	  before the dot renders empty)
					//
					// A log line is a durable sink: this is
					// stdout of an automated QA session, which
					// is captured, archived and attached to
					// reports.
					fmt.Printf(
						"[data-validation] entities/stats "+
							"JSON parse failed: %s\n",
						decodeFailureDetail(jErr, body),
					)
				}
			}
		}
	}

	// ── 2. Media search (authenticated) ────────────
	searchURL := baseURL +
		"/api/v1/media/search?limit=5"
	searchReq, err := http.NewRequestWithContext(
		ctx, http.MethodGet, searchURL, nil,
	)
	if err == nil {
		if authToken != "" {
			searchReq.Header.Set(
				"Authorization", "Bearer "+authToken,
			)
		}
		resp, err := client.Do(searchReq)
		if err != nil {
			fmt.Printf(
				"[data-validation] media/search "+
					"request failed: %v\n",
				err,
			)
			findings = append(findings,
				analysis.AnalysisFinding{
					Category: analysis.CategoryFunctional,
					Severity: analysis.SeverityHigh,
					Title: "API unreachable: " +
						"media/search",
					Description: fmt.Sprintf(
						"GET %s failed: %v",
						searchURL, err,
					),
					Platform: "api",
				},
			)
		} else {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				fmt.Printf(
					"[data-validation] media/search "+
						"returned %d\n",
					resp.StatusCode,
				)
				findings = append(findings,
					analysis.AnalysisFinding{
						Category: analysis.CategoryFunctional,
						Severity: analysis.SeverityHigh,
						Title: fmt.Sprintf(
							"API error: media/search "+
								"returned %d",
							resp.StatusCode,
						),
						// HXC-270 site 3 of 7 — same class as
						// the entity-statistics site above.
						Description: failedReplyDescription(
							"GET "+searchURL,
							resp.StatusCode, body,
							resp.Header.Get("Content-Type"),
						),
						Platform: "api",
					},
				)
			} else {
				var searchResp struct {
					Items []json.RawMessage `json:"items"`
					Total int               `json:"total"`
				}
				if jErr := json.Unmarshal(
					body, &searchResp,
				); jErr == nil {
					fmt.Printf(
						"[data-validation] search "+
							"returned %d items "+
							"(total %d)\n",
						len(searchResp.Items),
						searchResp.Total,
					)
					if len(searchResp.Items) == 0 &&
						searchResp.Total == 0 {
						findings = append(findings,
							analysis.AnalysisFinding{
								Category: analysis.CategoryFunctional,
								Severity: analysis.SeverityHigh,
								Title: "API search returned " +
									"zero results",
								Description: "media/search " +
									"returned no items; if " +
									"the backend is populated " +
									"this indicates a data " +
									"pipeline issue",
								Platform: "api",
							},
						)
					}
				} else {
					// HXC-270 site 5 of 7 — MEASURED, not
					// filed. Same leak as the entity-statistics
					// complaint above; `{"total":1e999}` against
					// THIS decode target renders the literal
					// `1e999` into the session log.
					fmt.Printf(
						"[data-validation] media/search "+
							"JSON parse failed: %s\n",
						decodeFailureDetail(jErr, body),
					)
				}
			}
		}
	}

	if len(findings) == 0 {
		fmt.Println(
			"[data-validation] all API checks passed",
		)
	}

	return findings
}

// undecodableBodyContentTypeMax bounds the one server-controlled
// string the undecodable-body diagnosis carries, so a pathological
// Content-Type header cannot bloat a committed report.
const undecodableBodyContentTypeMax = 80

// replyBodyShapeObject is the one label replyBodyShape and
// undecodableBodyKind disagree on, named so the disagreement is a
// single comparison rather than a duplicated switch.
const replyBodyShapeObject = "JSON object"

// replyBodyShape names the SHAPE of any HTTP reply body from a CLOSED
// vocabulary derived from its first non-whitespace byte, without
// reproducing a single byte of it (§11.4.10).
//
// This is the neutral classifier, for bodies the caller never tried
// to decode — a non-2xx reply, say, whose body may be perfectly
// well-formed. undecodableBodyKind is the sibling for bodies that
// were decoded and FAILED, and differs in exactly one arm: it calls a
// '{' body "truncated or malformed" because reaching it proves the
// decode failed, which is not something this function may assume.
//
// HXC-270: the vocabulary is closed and content-free on purpose. Both
// callers below feed reports and error strings that travel into
// shared logs, so the classifier's whole contract is that its output
// is a function of one byte's CATEGORY and never of its value.
func replyBodyShape(body []byte) string {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return "empty"
	}
	switch c := trimmed[0]; {
	case c == '{':
		return replyBodyShapeObject
	case c == '[':
		return "JSON array"
	case c == '"':
		return "JSON string"
	case c == '-' || (c >= '0' && c <= '9'):
		return "JSON number"
	case c == 't' || c == 'f':
		return "JSON boolean"
	default:
		return "not JSON"
	}
}

// boundedContentType renders a response Content-Type for inclusion in
// a report or an error string, clipped to undecodableBodyContentTypeMax.
//
// Content-Type is reported where the body is not because it is a
// HEADER the server chose to advertise rather than content, and it is
// the single field that identifies a proxy error page. It is still
// server-controlled, hence the bound: a pathological header cannot
// bloat a committed report.
//
// The clip is by BYTES and can split a multi-byte rune. That is
// deliberate and harmless here: every caller renders the result with
// %q, which escapes an invalid trailing fragment rather than emitting
// it raw. Preserved exactly as HXC-267 shipped it so that fix's
// exact-pinned descriptions stay byte-identical.
func boundedContentType(contentType string) string {
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		return "<none>"
	}
	if len(contentType) > undecodableBodyContentTypeMax {
		return contentType[:undecodableBodyContentTypeMax] + "…"
	}
	return contentType
}

// decodeFailureDetail says why a body failed to decode using only its
// shape and, when available, the byte offset at which the decoder
// gave up — never the decoder's error text (§11.4.10).
//
// The error text is never read at all, and that rule is stronger than
// "avoid the field that leaks": see undecodableBodyKind for the two
// measured paths by which encoding/json embeds input bytes in its
// error values. Only json.SyntaxError.Offset is consulted, an int64
// that cannot carry a body byte.
//
// HXC-270: this is the replacement for every `%v`/`%w` on a decode
// error in this package. A wrapped decode complaint reads as harmless
// plumbing and is precisely the leak — `fmt.Errorf("...: %w", jErr)`
// renders the quoted input byte the moment anything prints it.
func decodeFailureDetail(jErr error, body []byte) string {
	detail := undecodableBodyKind(body)
	var syn *json.SyntaxError
	if errors.As(jErr, &syn) {
		detail = fmt.Sprintf(
			"%s, decoding gave up at byte offset %d",
			detail, syn.Offset,
		)
	}
	return detail
}

// failedReplyDescriptionFormat is the description raised for a reply
// whose status says the request failed. Held as a package constant so
// the guard can pin it against a test-local copy rather than by
// calling the code under test, which would agree with any mutation of
// it (§1.1).
const failedReplyDescriptionFormat = "%s returned HTTP %d. Reply body " +
	"shape %q, length %d bytes, Content-Type %q. The body itself is " +
	"withheld on purpose: a failed reply may still carry a credential " +
	"— a sign-in route that echoes the submitted token, a session " +
	"cookie rendered into the body, or an authorization header quoted " +
	"back in diagnostic output. Status, shape, length and Content-Type " +
	"identify a wrong-system reply such as a proxy error page without " +
	"repeating what the server sent."

// failedReplyDescription renders the diagnosis for a reply whose
// status says the request failed.
//
// HXC-270: these sites used to record `string(body)` verbatim as the
// finding description. An unsuccessful reply is still a reply, and a
// sign-in route that echoes a submitted token, renders a session
// cookie into its body, or quotes an Authorization header back in its
// diagnostic output puts that value straight into a durable record
// that travels into shared logs and automation.
//
// The replacement carries the same CLASS of bounded, non-echoing
// detail the HXC-267 success path already reports, so the discipline
// no longer inverts between a function's success and failure arms:
// the endpoint, the status, the body's shape and length, and the
// Content-Type — enough to tell a proxy error page from a genuine API
// error without reproducing either.
//
// Reporting NOTHING would fail in the other direction, so the format
// is pinned by TestHXC270_FailedReplyDescriptionIsExactlyPinned.
func failedReplyDescription(
	label string,
	statusCode int,
	body []byte,
	contentType string,
) string {
	return fmt.Sprintf(
		failedReplyDescriptionFormat,
		label, statusCode, replyBodyShape(body), len(body),
		boundedContentType(contentType),
	)
}

// undecodableBodyKind classifies a response body that failed to
// decode as JSON, returning a value from a CLOSED vocabulary derived
// from the first non-whitespace byte.
//
// Named loginBodyKind when HXC-267 introduced it for the sign-in
// path; renamed by HXC-270, which widened its callers to every decode
// failure in this package (entity-statistics and media-search replies
// reach it too). The returned vocabulary is unchanged, byte for byte.
//
// The classification is deliberately hand-rolled rather than read off
// the encoding/json error (§11.4.10). Go's decoder embeds input bytes
// in its error values, and both of these were measured on the
// toolchain this package builds with:
//
//	json.SyntaxError quotes the offending character —
//	  `invalid character '<' looking for beginning of value` for an
//	  HTML body, `'ÿ'` for invalid UTF-8.
//	json.UnmarshalTypeError.Value can carry the offending literal —
//	  `{"token":1e999}` yields Value == "number 1e999", and that
//	  lands in this very branch, because the decode target here is a
//	  map[string]any and the interface path is one of the several
//	  that build "number " + <literal>.
//
// Forwarding either would echo body content into a report. Which
// error paths embed input bytes varies by body shape and by decoder
// implementation, so the safe rule is not "avoid the one that leaks"
// but "never read error text at all": only SyntaxError.Offset is
// consulted, an int64 that cannot carry a body byte. A label chosen
// from a fixed set is the other half — it reports the SHAPE of the
// body, never any byte of it.
//
// Note for anyone re-deriving this: a bare Value == "number" (no
// literal) is also reachable, for a number decoded into a non-numeric
// target. Observing that shape does NOT mean the digits form is gone;
// it means the probe picked a path that does not embed. The digits
// form is live, not historical, and an earlier revision of this
// comment got that backwards.
func undecodableBodyKind(body []byte) string {
	if shape := replyBodyShape(body); shape != replyBodyShapeObject {
		return shape
	}
	// Reaching here means the object FAILED TO DECODE — NOT
	// that it "never closed". The label names the common case
	// (truncated, cut off mid-token) and is an approximation
	// worth stating as one, because closed, brace-balanced
	// objects reach this arm too. Both measured on the
	// toolchain this package builds with:
	//
	//	{"token":1e999} — the very body the comment above
	//	  cites — is brace-balanced and json.Valid, and
	//	  still fails, as `cannot unmarshal number 1e999
	//	  into Go value of type float64`: a literal the
	//	  decode target cannot represent.
	//	a 12000-deep object closed by 12000 matching braces
	//	  fails as `invalid character '{' exceeded max
	//	  depth`: structurally complete, but past the
	//	  decoder's nesting limit.
	//
	// The approximation is structural rather than lazy: the
	// classifier reads exactly ONE byte, and the bytes that
	// would separate "truncated" from "closed but
	// unrepresentable" are the body bytes §11.4.10 forbids it
	// from reporting. Pinned by the closed-object overflow
	// cell in TestHXC267_UndecodableDescriptionIsExactlyPinned.
	//
	// This arm is the ONLY point on which this function and
	// replyBodyShape differ: everything else is delegated, so
	// the two labellings cannot drift apart.
	return "truncated or malformed JSON object"
}

// undecodableLoginBodyDescription renders the diagnosis for a login
// response that returned 200 but whose body could not be decoded
// into a JSON object.
//
// §11.4.10 — the body is never echoed, not even in part. An
// unreadable body is still a body: an HTML error page or a truncated
// JSON fragment can carry a bearer, so nothing derived from its
// content may reach a finding that ends up in a committed report.
// What the description carries instead is strictly non-content:
//
//   - the SHAPE of the body, from undecodableBodyKind's closed
//     vocabulary;
//   - the byte offset at which decoding gave up, read from
//     json.SyntaxError's numeric Offset field only — never from any
//     error's message text, which quotes an input byte;
//   - the body's length in bytes;
//   - the response Content-Type, which is a HEADER the server chose
//     to advertise rather than content, and is the single field that
//     identifies a proxy error page served with status 200.
//
// It also states outright that the body was withheld, so a reader
// does not mistake the omission for a truncated report.
func undecodableLoginBodyDescription(
	jErr error,
	body []byte,
	contentType string,
) string {
	// HXC-270 factored the two steps below into decodeFailureDetail
	// and boundedContentType, which the failure-path sites now share.
	// Both are lifts, not rewrites: the rendered description stays
	// byte-identical, which is what
	// TestHXC267_UndecodableDescriptionIsExactlyPinned proves.
	cause := decodeFailureDetail(jErr, body)
	contentType = boundedContentType(contentType)

	tried := append(
		[]string{defaultTokenField},
		defaultTokenFieldFallbacks...,
	)

	return fmt.Sprintf(
		"Login returned HTTP 200 but the response body could not "+
			"be decoded as a JSON object (%s), so no bearer token "+
			"could be looked up — tried %q. Response Content-Type "+
			"%q, body length %d bytes. The body itself is withheld "+
			"on purpose: a body this pipeline cannot read may still "+
			"carry a credential. Every request after this one goes "+
			"out UNAUTHENTICATED, so any 401 below originates here, "+
			"not in the API under test.",
		cause, tried, contentType, len(body),
	)
}

// selectEvenly returns up to max elements from the slice,
// picking elements at evenly-spaced indices for
// representative coverage. If the slice has fewer than max
// elements, all are returned.
func selectEvenly(items []string, max int) []string {
	if len(items) <= max {
		return items
	}
	step := float64(len(items)) / float64(max)
	selected := make([]string, 0, max)
	for i := 0; i < max; i++ {
		idx := int(float64(i) * step)
		if idx >= len(items) {
			idx = len(items) - 1
		}
		selected = append(selected, items[idx])
	}
	return selected
}

// runStructuredPhase runs Phase 3.5 (structured test-bank execution) and
// records its outcome on the session result.
func (sp *SessionPipeline) runStructuredPhase(
	ctx context.Context,
	execFactory ExecutorFactory,
	vision llm.Provider,
	result *PipelineResult,
	onFinding func(analysis.AnalysisFinding),
	onScreenshot func(platform string, data []byte),
) {
	structuredStart := time.Now()
	structuredExec := NewStructuredTestExecutor(
		*sp.config, execFactory, vision, onFinding, onScreenshot,
	)
	structuredResult, err := structuredExec.Execute(ctx)
	// Record the outcome on the session result: it is persisted by
	// WriteReport (per-case verdicts, per-step frame sha256 / pHash) and
	// drives the process exit code (PipelineResult.ExitCode).
	result.Structured = structuredResult
	if err != nil {
		result.StructuredError = err.Error()
		fmt.Printf(
			"  [structured] Execution error: %v\n", err,
		)
	} else {
		fmt.Printf(
			"  [structured] Completed: %d passed, %d failed, "+
				"%d blocked (could not determine), "+
				"%d skipped (bank placeholders), %d total, "+
				"%d steps executed in %v\n",
			structuredResult.TestCasesPassed,
			structuredResult.TestCasesFailed,
			structuredResult.TestCasesBlocked,
			structuredResult.TestCasesSkipped,
			structuredResult.TestCasesRun,
			structuredResult.StepsExecuted,
			time.Since(structuredStart).Round(time.Millisecond),
		)
	}
}

// WriteReport writes the PipelineResult as JSON to
// OutputDir/pipeline-report.json.
func (sp *SessionPipeline) WriteReport(
	result *PipelineResult,
) error {
	if err := os.MkdirAll(sp.config.OutputDir, 0o755); err != nil {
		return fmt.Errorf(
			"pipeline: create output dir: %w", err,
		)
	}

	path := filepath.Join(
		sp.config.OutputDir, "pipeline-report.json",
	)
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf(
			"pipeline: marshal report: %w", err,
		)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf(
			"pipeline: write report %s: %w", path, err,
		)
	}

	fmt.Printf("[pipeline] Report written: %s\n", path)

	// Create/update "latest" symlink in the parent of the
	// session directory so users can always find the most
	// recent results at qa-results/latest/.
	parentDir := filepath.Dir(sp.config.OutputDir)
	latestLink := filepath.Join(parentDir, "latest")
	_ = os.Remove(latestLink)
	sessionDir := filepath.Base(sp.config.OutputDir)
	if err := os.Symlink(sessionDir, latestLink); err != nil {
		fmt.Printf(
			"[pipeline] warning: could not create "+
				"latest symlink: %v\n", err,
		)
	} else {
		fmt.Printf(
			"[pipeline] Updated latest -> %s\n",
			sessionDir,
		)
	}

	return nil
}

// updateSession persists the pipeline result back to the
// memory store.
func (sp *SessionPipeline) updateSession(
	id string, result *PipelineResult,
) {
	now := time.Now()
	dur := int(result.Duration.Seconds())
	u := memory.SessionUpdate{
		EndedAt:       &now,
		Duration:      dur,
		TotalTests:    result.TestsPlanned,
		Passed:        result.TestsRun,
		Failed:        result.TestsPlanned - result.TestsRun,
		FindingsCount: result.IssuesFound,
		CoveragePct:   result.CoveragePct,
		Notes: fmt.Sprintf(
			"status=%s", result.Status,
		),
	}
	if err := sp.store.UpdateSession(id, u); err != nil {
		fmt.Printf("[pipeline] update session failed: %v\n", err)
	}
}

// navigationPromptTemplate is the generic system prompt for
// Android/Android TV QA. It contains NO project-specific
// information — all app context comes from the screenshot
// and the LLM's visual analysis. HelixQA is decoupled and
// works with ANY app on any supported platform.
const navigationPromptTemplate = `You are an expert QA tester performing a thorough autonomous QA session on an Android TV application. You must test EVERY feature like a real human tester would — browsing content, opening details, playing media, testing all screens.

Look at the screenshot and determine:
1. What screen am I on?
2. What is the MOST VALUABLE next QA action? Prioritize UNEXPLORED features.

ANDROID TV CONTROLS:
- dpad_up/down/left/right — move focus
- dpad_center — select/activate focused element
- type — enter text (activate field with dpad_center first)
- tab — move between form fields
- back — go back
- clear — delete text in active field
- wait — pause 3 seconds

LOGIN (only when you see a login screen):
1. dpad_down to username field, dpad_center to activate
2. clear, then type the username
3. tab to password, clear, type the password
4. dpad_down to Sign In, dpad_center to click

QA TESTING PRIORITY (follow this order):
1. LOGIN first if on login screen (try admin/admin123)
2. BROWSE the home screen — scroll down and right through ALL content rows
3. OPEN detail screens — select items to see their detail/info page
4. PLAY media — find and activate play buttons for video/audio content
5. BROWSE categories — navigate to different content sections (movies, TV, music, etc.)
6. SEARCH — use search to find specific content, verify results appear
7. TEST favorites — add/remove favorites
8. EXPLORE settings — check settings/preferences screens
9. TEST collections — browse/create collections
10. NAVIGATE back — verify back button works from every screen

CRITICAL RULES:
- Do NOT stay on the same screen for more than 3 steps. MOVE to a different screen.
- NEVER type credentials into non-login fields. Understand WHICH screen you are on. If you see a search bar, type a search query relevant to the app's content. If you see login fields, type credentials.
- NEVER repeat the same action pattern 3 times in a row. If stuck, navigate somewhere NEW.
- After successful login, IMMEDIATELY explore the app — do NOT return to login.
- Read the screen carefully before acting. Different screens require different input.

TESTING PRIORITY (follow this order):
1. HAPPY PATHS FIRST — complete login, browse main content, open detail screens, interact with primary features
2. STANDARD FLOWS — use search with relevant terms from what you see on screen, browse all available sections, test navigation between screens
3. EDGE CASES — empty states, back navigation, error handling
4. Always use CONTEXT-APPROPRIATE input for each screen

If you see content items (cards, lists, grids), SELECT one to open its detail page.
If you see a play/open button, PRESS IT to test that feature.
If you see navigation elements you haven't visited, GO THERE.
For search fields: type SHORT, REAL content titles that you can SEE on screen or that were mentioned in the knowledge base context above. Look at the current screenshot — pick a title visible in a category row or detail screen. Keep search queries to 1-2 words MAX (e.g., just the first word of a movie title you see). NEVER type long strings, random characters, usernames, passwords, or "test". The 'type' action will auto-clear the field before typing.

MANDATORY: Complete ALL happy paths before testing any negative scenarios. Do NOT test error cases, invalid inputs, or edge cases until you have successfully: logged in, browsed all content sections, opened at least 3 detail pages, PLAYED media content (video or audio — press play and verify progress), and tested search with valid terms.

CRITICAL PLAYBACK VERIFICATION: When you see a Play button on a detail page, you MUST press it. After the player opens, wait 3 seconds and take note of whether the progress bar has moved (non-zero currentTime). If it hasn't, that is a BUG. Playback must actually start, not just open a player screen.

RESPONSE: Return ONLY a JSON array of 1-5 actions. No other text.
Format: [{"type":"...", "value":"...", "reason":"..."}]
Types: dpad_up, dpad_down, dpad_left, dpad_right, dpad_center, type, tab, key, back, clear, wait`

// webNavigationPromptTemplate is the prompt for web browser
// QA sessions. Uses mouse clicks and keyboard input instead of
// DPAD navigation.
// webNavigationPromptTemplate is the generic prompt for web
// browser QA. No project-specific information — the LLM
// analyzes the screenshot to determine context.
const webNavigationPromptTemplate = `You are an expert QA tester performing a FULL autonomous QA session on a web application in a headless browser (1920x1080 viewport). Test EVERY feature like a real human QA tester would.

Look at the screenshot and determine:
1. What page am I on?
2. What is the MOST VALUABLE unexplored QA action?

WEB CONTROLS:
- click — value is "x,y" pixel coordinates
- type — enter text (click input first)
- scroll_down/scroll_up — scroll page
- key — Enter, Escape, Tab, Backspace
- back — browser back
- wait — pause 3 seconds

LOGIN (only on login page):
1. Click username field, type "admin"
2. Click password field, type "admin123"
3. Click Sign In button

QA TESTING PRIORITY (follow this order):
1. LOGIN first if on login page
2. DASHBOARD — check stats, charts, activity feed
3. BROWSE MEDIA — click on media items to open details
4. PLAY CONTENT — click play buttons to test playback
5. SIDEBAR NAVIGATION — click every menu item
6. COLLECTIONS — browse and manage collections
7. FAVORITES — add/remove favorites
8. SEARCH — search for content, verify results
9. SETTINGS — check all settings pages
10. ADMIN — check admin panel if available

CRITICAL RULES:
- Do NOT stay on the same page for more than 3 steps. NAVIGATE to a different page.
- NEVER type credentials into non-login fields. Understand WHICH page you are on.
- NEVER repeat the same action pattern 3 times. If stuck, go somewhere NEW.
- After login, IMMEDIATELY explore — do NOT return to login.
- Use CONTEXT-APPROPRIATE input: search terms from visible content, not credentials.

TESTING PRIORITY:
1. HAPPY PATHS FIRST — login, explore dashboard, open detail pages, interact with features
2. PLAY MEDIA — click play buttons on detail pages, verify video/audio plays (progress bar moves)
3. STANDARD FLOWS — search with relevant terms, browse all sections, test navigation
4. EDGE CASES — empty states, back navigation, error handling

PLAYBACK VERIFICATION: When you see a Play button, CLICK IT. After the player opens, wait 3 seconds. The progress bar must show non-zero time. If it doesn't, that is a BUG.

RESPONSE: Return ONLY a JSON array of 1-5 actions. No other text.
Format: [{"type":"...", "value":"...", "reason":"..."}]
Types: click, type, scroll_down, scroll_up, key, back, wait`

// llmAction is a single navigation action suggested by the LLM.
type llmAction struct {
	Type   string `json:"type"`
	Value  string `json:"value,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// llmNavigateTimeout caps a single LLM vision call during
// curiosity navigation so one slow API response cannot
// stall the exploration phase. Reduced from 180s to 60s
// so stuck calls fail faster and the retry logic gets a
// chance to recover.
// llmNavigateTimeout caps a single provider attempt. Kept short
// Parent timeout for a single LLM navigation call. Set to 90s to
// allow the adaptive provider to try multiple providers (each capped
// at 15-20s internally). Most successful calls complete in 2-10s.
const llmNavigateTimeout = 45 * time.Second

// llmNavigate sends a (pre-resized) screenshot to the LLM
// vision endpoint and parses the response into a list of
// actions to execute. The screenshot should already be
// resized by the caller. Returns nil on any error (graceful
// degradation). A per-call timeout prevents slow API
// responses from blocking the curiosity loop.
func (sp *SessionPipeline) llmNavigate(
	ctx context.Context,
	screenshot []byte,
	platform string,
	step int,
	history []string,
	visionProvider ...llm.Provider,
) []llmAction {
	// Select the right prompt for the platform.
	var prompt string
	switch platform {
	case "web":
		prompt = webNavigationPromptTemplate
	default:
		prompt = navigationPromptTemplate
	}
	// Inject knowledge base context (credentials, screens,
	// constraints discovered during Learn phase).
	if sp.kbContext != "" {
		prompt += "\n\n" + sp.kbContext
	}
	if len(history) > 0 {
		prompt += "\n\nPREVIOUS ACTIONS IN THIS SESSION " +
			"(do NOT repeat these — move to the NEXT " +
			"logical step):\n"
		for _, h := range history {
			prompt += "- " + h + "\n"
		}
		prompt += "\nBased on the screenshot and your " +
			"previous actions, decide the NEXT step. " +
			"Do NOT repeat what you already did."
	}

	// Apply a per-call timeout on top of the parent
	// context.
	callCtx, callCancel := context.WithTimeout(
		ctx, llmNavigateTimeout,
	)
	defer callCancel()

	// Use the per-platform provider if given, otherwise
	// fall back to phase-aware selection (or dedicated
	// vision provider, or shared pipeline provider).
	vp := sp.selectProviderForPhase("curiosity")
	if len(visionProvider) > 0 && visionProvider[0] != nil {
		vp = visionProvider[0]
	}

	visionStart := time.Now()
	resp, err := vp.Vision(
		callCtx, screenshot, prompt,
	)
	visionDur := time.Since(visionStart)
	if err != nil {
		fmt.Printf(
			"  [curiosity %s #%d] LLM vision "+
				"error (%v): %v\n",
			platform, step,
			visionDur.Round(time.Millisecond), err,
		)
		return nil
	}
	fmt.Printf(
		"  [curiosity %s #%d] LLM responded in %v\n",
		platform, step,
		visionDur.Round(time.Millisecond),
	)

	content := strings.TrimSpace(resp.Content)
	if content == "" {
		return nil
	}

	// Strip markdown code fences.
	content = stripCodeFence(content)

	// Locate JSON array boundaries.
	start := strings.Index(content, "[")
	end := strings.LastIndex(content, "]")
	if start == -1 || end == -1 || end < start {
		// No JSON array found — try to extract individual JSON objects
		// from markdown bullet points or inline backticks.
		// Pattern: *   `{"type":"...", "reason":"..."}`
		// or: * {"type":"...", "reason":"..."}
		var objects []string
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			// Strip markdown bullet prefixes
			line = strings.TrimLeft(line, "*-• ")
			line = strings.TrimSpace(line)
			// Strip inline backticks
			line = strings.Trim(line, "`")
			line = strings.TrimSpace(line)
			// Check if it looks like a JSON object
			if strings.HasPrefix(line, "{") && strings.Contains(line, "}") {
				// Extract just the JSON object
				objEnd := strings.LastIndex(line, "}")
				if objEnd >= 0 {
					objects = append(objects, line[:objEnd+1])
				}
			}
		}
		if len(objects) > 0 {
			content = "[" + strings.Join(objects, ",") + "]"
			start = 0
			end = len(content) - 1
		} else {
			fmt.Printf(
				"  [curiosity %s #%d] LLM response "+
					"not JSON array: %.80s\n",
				platform, step, content,
			)
			return nil
		}
	}

	var actions []llmAction
	jsonStr := content[start : end+1]
	// Repair common LLM JSON quirks before parsing.
	jsonStr = repairLLMJSON(jsonStr)
	if err := json.Unmarshal(
		[]byte(jsonStr), &actions,
	); err != nil {
		fmt.Printf(
			"  [curiosity %s #%d] LLM JSON parse "+
				"error: %v\n",
			platform, step, err,
		)
		return nil
	}

	return actions
}

// executeAction translates an llmAction into an
// ActionExecutor method call.
func executeAction(
	ctx context.Context,
	exec navigator.ActionExecutor,
	action llmAction,
) error {
	switch action.Type {
	case "dpad_up":
		return exec.KeyPress(ctx, "KEYCODE_DPAD_UP")
	case "dpad_down":
		return exec.KeyPress(ctx, "KEYCODE_DPAD_DOWN")
	case "dpad_left":
		return exec.KeyPress(ctx, "KEYCODE_DPAD_LEFT")
	case "dpad_right":
		return exec.KeyPress(ctx, "KEYCODE_DPAD_RIGHT")
	case "dpad_center", "select", "enter":
		return exec.KeyPress(ctx, "KEYCODE_DPAD_CENTER")
	case "tab":
		return exec.KeyPress(ctx, "KEYCODE_TAB")
	case "back":
		return exec.Back(ctx)
	case "home":
		return exec.Home(ctx)
	case "tap", "click":
		var x, y int
		_, _ = fmt.Sscanf(action.Value, "%d,%d", &x, &y)
		if x == 0 && y == 0 {
			// Invalid coordinates — press center instead.
			return exec.KeyPress(
				ctx, "KEYCODE_DPAD_CENTER",
			)
		}
		return exec.Click(ctx, x, y)
	case "swipe_up", "scroll_up":
		return exec.Scroll(ctx, "up", 400)
	case "swipe_down", "scroll_down":
		return exec.Scroll(ctx, "down", 400)
	case "swipe_left", "scroll_left":
		return exec.Scroll(ctx, "left", 400)
	case "swipe_right", "scroll_right":
		return exec.Scroll(ctx, "right", 400)
	case "type":
		if action.Value == "" {
			return nil
		}
		// The LLM decides when to clear — the framework does not
		// auto-clear. HelixQA constitution requires all navigation
		// decisions to come from the LLM vision analysis.
		return exec.Type(ctx, action.Value)
	case "key":
		keyCode := action.Value
		if keyCode == "" {
			// Infer key from reason — LLMs often omit the
			// value but describe the intent in the reason.
			reason := strings.ToLower(action.Reason)
			if strings.Contains(reason, "submit") ||
				strings.Contains(reason, "login") ||
				strings.Contains(reason, "enter") ||
				strings.Contains(reason, "confirm") {
				keyCode = "KEYCODE_ENTER"
			} else {
				keyCode = "KEYCODE_ENTER"
			}
		}
		return exec.KeyPress(ctx, keyCode)
	case "wait":
		// Allow the LLM to insert deliberate pauses for
		// screen transitions, login processing, etc.
		// REDUCED for FLASHING FAST performance (was 3s).
		time.Sleep(500 * time.Millisecond)
		return nil
	case "clear":
		// Delegate to the platform-specific Clear method which
		// uses select-all + delete (reliable regardless of
		// field content length).
		return exec.Clear(ctx)
	default:
		return fmt.Errorf("unknown action type: %s", action.Type)
	}
}

// repairLLMJSON fixes common JSON formatting issues from LLM
// vision models (especially LLaVA) that return almost-valid
// JSON. Handles: trailing commas, single quotes, missing
// commas between objects, and bare string values.
func repairLLMJSON(s string) string {
	// Remove literal newlines inside string values.
	// LLaVA sometimes puts \n inside JSON strings which
	// breaks the parser.
	var result strings.Builder
	inString := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			result.WriteByte(c)
			escaped = false
			continue
		}
		if c == '\\' && inString {
			escaped = true
			result.WriteByte(c)
			continue
		}
		if c == '"' {
			inString = !inString
		}
		if c == '\n' && inString {
			result.WriteString("\\n")
			continue
		}
		result.WriteByte(c)
	}
	s = result.String()

	// Replace single quotes with double quotes (but not
	// within already double-quoted strings).
	if !strings.Contains(s, `"`) && strings.Contains(s, `'`) {
		s = strings.ReplaceAll(s, `'`, `"`)
	}

	// Remove trailing commas before ] or }.
	for _, pair := range [][2]string{
		{",]", "]"}, {",}", "}"},
		{", ]", "]"}, {", }", "}"},
	} {
		s = strings.ReplaceAll(s, pair[0], pair[1])
	}

	// Fix missing comma between adjacent objects: }{ → },{
	s = strings.ReplaceAll(s, "}{", "},{")
	s = strings.ReplaceAll(s, "}\n{", "},\n{")
	s = strings.ReplaceAll(s, "} {", "}, {")

	return s
}

// stripCodeFence removes leading/trailing markdown code-fence
// markers from a string.
func stripCodeFence(s string) string {
	for _, prefix := range []string{"```json", "```"} {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimPrefix(s, prefix)
			s = strings.TrimSpace(s)
			break
		}
	}
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	return s
}

func sanitizeFilename(s string) string {
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ToLower(s)
	if len(s) > 40 {
		s = s[:40]
	}
	if s == "" {
		s = "unknown"
	}
	return s
}

// joinStrings joins a string slice with commas.
// findingsToReproBugs converts high-severity analysis
// findings into Bug structs for the BugReproducer. Only
// findings with "critical" or "high" severity are included.
// appDefectFindings keeps only findings that claim a defect in the app
// under test (drops environment findings such as BLOCKED vision cases).
func appDefectFindings(findings []analysis.AnalysisFinding) []analysis.AnalysisFinding {
	out := make([]analysis.AnalysisFinding, 0, len(findings))
	for _, f := range findings {
		if f.Category.IsAppDefect() {
			out = append(out, f)
		}
	}
	return out
}

func findingsToReproBugs(
	findings []analysis.AnalysisFinding,
) []reproduce.Bug {
	var bugs []reproduce.Bug
	for i, f := range findings {
		if !f.Category.IsAppDefect() {
			// The QA run could not judge (e.g. vision unavailable):
			// there is no app bug to reproduce.
			continue
		}
		sev := strings.ToLower(string(f.Severity))
		if sev != "critical" && sev != "high" {
			continue
		}
		bugs = append(bugs, reproduce.Bug{
			ID: fmt.Sprintf(
				"finding-%d-%s", i+1, f.Platform,
			),
			Description: f.Title + ": " + f.Description,
			Severity:    sev,
			Platform:    f.Platform,
			// ActionSequence is empty — the reproducer
			// will use the LLM to navigate based on the
			// bug description rather than replaying
			// recorded actions.
		})
	}
	return bugs
}

// visionRegressionAdapter bridges the llm.Provider
// interface to the regression.VisionProvider interface.
// This allows the regression package to remain decoupled
// from the full llm.Provider type.
type visionRegressionAdapter struct {
	provider llm.Provider
}

func (a *visionRegressionAdapter) Vision(
	ctx context.Context,
	image []byte,
	prompt string,
) (*regression.VisionResponse, error) {
	resp, err := a.provider.Vision(ctx, image, prompt)
	if err != nil {
		return nil, err
	}
	return &regression.VisionResponse{
		Content: resp.Content,
	}, nil
}

func (a *visionRegressionAdapter) SupportsVision() bool {
	return a.provider.SupportsVision()
}

func joinStrings(ss []string) string {
	return strings.Join(ss, ",")
}
