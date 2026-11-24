# HelixQA Architecture

## Overview

HelixQA is a QA orchestration engine built on Go 1.24+ that drives cross-platform testing with real-time crash/ANR detection, step validation, and evidence-based reporting. It composes existing vasic-digital modules (Challenges, Containers) rather than reimplementing their functionality.

## Module Dependency Graph

```
HelixQA (Orchestration Layer)
├── pkg/orchestrator  ── Main QA brain
├── pkg/testbank      ── YAML test bank management
├── pkg/detector      ── Real-time crash/ANR detection
├── pkg/validator     ── Step-by-step validation
├── pkg/evidence      ── Evidence collection (screenshots, logs, video)
├── pkg/ticket        ── Markdown ticket generation
├── pkg/reporter      ── QA report generation (MD, HTML, JSON)
├── pkg/config        ── Configuration types
└── cmd/helixqa       ── CLI entry point
    ↓ imports ↓
Challenges (Test Execution)
├── pkg/runner        ── Challenge execution engine
├── pkg/bank          ── Challenge bank loading (JSON)
├── pkg/challenge     ── Core types (Challenge, Definition, Result)
├── pkg/report        ── Report generation (MD, HTML, JSON)
├── pkg/logging       ── Structured logging
└── pkg/userflow      ── Multi-platform automation adapters
    ↓ imports ↓
Containers (Infrastructure)
├── pkg/compose       ── Container orchestration
├── pkg/runtime       ── Container runtime
└── pkg/lifecycle     ── Container lifecycle
```

## Data Flow

```
                    ┌──────────────────┐
                    │   CLI / Caller   │
                    └────────┬─────────┘
                             │ config + bank paths
                    ┌────────▼─────────┐
                    │   Orchestrator   │ ← Main brain
                    └────────┬─────────┘
                             │
              ┌──────────────┼──────────────┐
              │              │              │
     ┌────────▼────┐  ┌─────▼──────┐ ┌────▼──────┐
     │  TestBank   │  │  Detector  │ │ Validator  │
     │  Manager    │  │ (per plat) │ │ (per step) │
     └────────┬────┘  └─────┬──────┘ └────┬──────┘
              │              │              │
              │         ┌────▼──────┐      │
              │         │ Evidence  │◄─────┘
              │         │ Collector │
              │         └────┬──────┘
              │              │
     ┌────────▼──────────────▼──────┐
     │         Reporter             │
     │  (Markdown / HTML / JSON)    │
     └──────────────┬───────────────┘
                    │
              ┌─────▼──────┐
              │   Ticket   │
              │  Generator │
              └────────────┘
```

## Package Responsibilities

### pkg/orchestrator
The central coordinator. Loads test banks, iterates over platforms, runs challenges via the Challenges runner, invokes validation between steps, and produces the final report. Supports functional options for dependency injection.

### pkg/testbank
Manages QA-specific YAML test banks. Extends the Challenges JSON bank format with: platform targeting, priority levels (critical/high/medium/low), documentation references for consistency checking, and step definitions. Converts to `challenge.Definition` for execution.

### pkg/detector
Real-time crash and ANR detection per platform:
- **Android**: ADB logcat parsing, pidof process checks, screencap
- **Web**: Browser process monitoring, console error collection
- **Desktop**: Process alive checks, stderr monitoring

Uses the `CommandRunner` interface for testability.

### pkg/validator
Wraps the detector to perform pre/post-step validation. Takes screenshots before and after each step, runs crash detection, and produces `StepResult` with evidence. Prevents false positives by correlating detection with step state.

### pkg/evidence
Centralized evidence collection: screenshots (ADB screencap, Playwright, X11 import), logcat capture, video recording lifecycle, and console logs. All items are tracked with metadata (type, platform, timestamp, file size).

### pkg/ticket
Generates detailed Markdown issue tickets from failed steps or raw detections. Each ticket includes: severity, platform, reproduction steps, expected/actual behavior, stack traces, logs, and screenshot evidence. Designed to feed into AI fix pipelines.

### pkg/reporter
Produces QA reports in Markdown, HTML, or JSON. Reuses `digital.vasic.challenges/pkg/report` for individual challenge formatting. Adds QA-specific sections: platform breakdown, crash/ANR counts, step validation tables, and evidence references.

### pkg/config
Configuration types: platform selection, speed modes (slow/normal/fast), report formats, device targeting, and validation toggles. Supports YAML/JSON serialization.

## Design Decisions

1. **Composition over reimplementation**: HelixQA imports Challenges types directly. No wrapper types around `challenge.Definition`, `bank.Bank`, or `report.Reporter`.

2. **Functional options pattern**: All constructors use `WithX()` options for clean dependency injection and testing.

3. **CommandRunner interface**: Abstracts command execution (`adb`, `npx`, etc.) behind an interface, enabling full test coverage without real devices.

4. **Platform-agnostic orchestration**: The orchestrator runs the same pipeline for all platforms. Platform-specific behavior is encapsulated in detector and evidence packages.

5. **Evidence-first reporting**: Every failure includes evidence (screenshots, logs, traces). Tickets are self-contained for AI pipeline consumption.

## Test Coverage

| Package | Tests | Focus |
|---------|-------|-------|
| config | Unit + edge | Validation, parsing, defaults |
| detector | Unit + platform | Android/Web/Desktop detection |
| validator | Unit + concurrent | Step validation, evidence |
| reporter | Unit + format | Markdown, HTML, JSON output |
| orchestrator | Unit + edge + integration + stress | Full pipeline, cancellation |
| testbank | Unit + stress + benchmark | YAML loading, filtering |
| ticket | Unit + stress + benchmark | Markdown generation |
| evidence | Unit + stress + benchmark | Concurrent capture |

Total: **235 tests**, all passing with `-race` flag.

## CLI

```
helixqa run        --banks <paths> [--platform all] [--speed fast]
helixqa list       --banks <paths> [--platform android] [--json]
helixqa report     --input <dir>   [--format html]
helixqa autonomous --project <path> --platforms <list> --env <file> [--timeout 2h]
helixqa version
```

---

## Autonomous QA Session Architecture

The Autonomous QA Session extends HelixQA with LLM-powered autonomous testing. A `SessionCoordinator` manages 4 sequential phases, delegating platform testing to parallel `PlatformWorker` instances. Each worker gets its own LLM agent, vision analyzer, navigation engine, and crash detector.

### Component Diagram

```mermaid
graph TB
    subgraph "HelixQA (Integration Point)"
        CLI[cmd/helixqa<br/>autonomous subcommand]
        AUTO[pkg/autonomous<br/>SessionCoordinator<br/>PlatformWorker<br/>PhaseManager]
        NAV[pkg/navigator<br/>NavigationEngine<br/>ActionExecutor]
        ISS[pkg/issuedetector<br/>IssueDetector]
        SESS[pkg/session<br/>SessionRecorder<br/>Timeline]
        CFG[pkg/config]
        DET[pkg/detector]
        VAL[pkg/validator]
        EVI[pkg/evidence]
        TIK[pkg/ticket]
        RPT[pkg/reporter]
        ORC[pkg/orchestrator]
        TB[pkg/testbank]
    end

    subgraph "External Modules (Git Submodules)"
        LV[LLMsVerifier<br/>Strategy pattern<br/>Model scoring]
        LO[LLMOrchestrator<br/>Agent pool<br/>CLI adapters]
        VE[VisionEngine<br/>GoCV + LLM Vision<br/>NavigationGraph]
        DP[DocProcessor<br/>Feature maps<br/>Coverage tracking]
    end

    subgraph "Infrastructure"
        CH[Challenges<br/>Test execution]
        CO[Containers<br/>Container runtime]
    end

    CLI --> AUTO
    AUTO --> NAV
    AUTO --> ISS
    AUTO --> SESS
    AUTO --> CFG
    AUTO --> DET
    AUTO --> EVI
    AUTO --> TIK
    AUTO --> RPT

    NAV --> VE
    AUTO --> LV
    AUTO --> LO
    AUTO --> DP
    ISS --> TIK
    SESS --> EVI

    ORC --> DET
    ORC --> VAL
    ORC --> RPT
    ORC --> TB
    ORC --> CH
    CH --> CO
```

### Sequence Diagram: 4-Phase Session Lifecycle

```mermaid
sequenceDiagram
    participant CLI as CLI
    participant SC as SessionCoordinator
    participant PM as PhaseManager
    participant LV as LLMsVerifier
    participant DP as DocProcessor
    participant LO as LLMOrchestrator
    participant VE as VisionEngine
    participant PW as PlatformWorker
    participant SR as SessionRecorder

    CLI->>SC: Run(ctx)
    SC->>PM: Start("setup")

    rect rgb(230, 245, 255)
        Note over SC,VE: Phase 1: Setup (Sequential)
        SC->>LV: VerifyWithStrategy(ctx, QAStrategy)
        LV-->>SC: ranked models
        SC->>DP: LoadDir + BuildFromDocs + Enrich
        DP-->>SC: FeatureMap
        SC->>LO: SpawnAgents(models)
        LO-->>SC: AgentPool
        SC->>VE: Init(config)
        VE-->>SC: Analyzer
        SC->>SR: StartRecording(platforms)
    end

    SC->>PM: Complete("setup")
    SC->>PM: Start("doc-driven")

    rect rgb(230, 255, 230)
        Note over SC,PW: Phase 2: Doc-Driven Verification (Parallel)
        par Android Worker
            SC->>PW: RunDocDriven(ctx, androidFeatures)
            PW->>PW: For each feature: screenshot, analyze, act, verify
            PW-->>SC: []StepResult
        and Desktop Worker
            SC->>PW: RunDocDriven(ctx, desktopFeatures)
            PW-->>SC: []StepResult
        and Web Worker
            SC->>PW: RunDocDriven(ctx, webFeatures)
            PW-->>SC: []StepResult
        end
    end

    SC->>PM: Complete("doc-driven")
    SC->>PM: Start("curiosity")

    rect rgb(255, 245, 230)
        Note over SC,PW: Phase 3: Curiosity-Driven Exploration (Parallel)
        par Android
            SC->>PW: RunCuriosityDriven(ctx, timeout)
            PW-->>SC: []StepResult
        and Desktop
            SC->>PW: RunCuriosityDriven(ctx, timeout)
            PW-->>SC: []StepResult
        and Web
            SC->>PW: RunCuriosityDriven(ctx, timeout)
            PW-->>SC: []StepResult
        end
    end

    SC->>PM: Complete("curiosity")
    SC->>PM: Start("report")

    rect rgb(245, 230, 255)
        Note over SC,SR: Phase 4: Report & Cleanup (Sequential)
        SC->>SR: StopRecording(platforms)
        SC->>SC: Aggregate coverage + tickets + navmaps
        SC->>SC: Generate QA report (MD + HTML + JSON)
        SC->>LO: Shutdown()
    end

    SC->>PM: Complete("report")
    SC-->>CLI: *SessionResult
```

### Class Diagram: Key Types

```mermaid
classDiagram
    class SessionCoordinator {
        -config *SessionConfig
        -verifier LLMsVerifierClient
        -docProcessor DocProcessorClient
        -orchestrator AgentPool
        -visionEngine Analyzer
        -featureMap *FeatureMap
        -workers map~string,*PlatformWorker~
        -phaseManager *PhaseManager
        -session *SessionRecorder
        -mu sync.Mutex
        +Run(ctx) (*SessionResult, error)
        +Pause(ctx) error
        +Resume(ctx) error
        +Cancel(ctx) error
        +Status() SessionStatus
        +Progress() ProgressReport
    }

    class PlatformWorker {
        -platform string
        -agent Agent
        -analyzer Analyzer
        -navigator *NavigationEngine
        -issueDetector *IssueDetector
        -coverage CoverageTracker
        -navGraph NavigationGraph
        -detector CrashDetector
        -session *SessionRecorder
        -executor ActionExecutor
        -mu sync.Mutex
        +RunDocDriven(ctx, features) ([]StepResult, error)
        +RunCuriosityDriven(ctx, timeout) ([]StepResult, error)
    }

    class PhaseManager {
        -phases []Phase
        -current int
        -listeners []PhaseListener
        -mu sync.Mutex
        +Start(name) error
        +Complete(name) error
        +Fail(name, err) error
        +Skip(name) error
        +Current() Phase
        +All() []Phase
    }

    class NavigationEngine {
        -agent Agent
        -analyzer Analyzer
        -executor ActionExecutor
        -graph NavigationGraph
        -state *StateTracker
        +NavigateTo(ctx, target) error
        +PerformAction(ctx, action) (*ActionResult, error)
        +ExploreUnknown(ctx) (*ExploreResult, error)
        +CurrentScreen(ctx) (*ScreenAnalysis, error)
        +GoBack(ctx) error
        +GoHome(ctx) error
    }

    class IssueDetector {
        -agent Agent
        -analyzer Analyzer
        -ticketGen *Generator
        -session *SessionRecorder
        +AnalyzeAction(ctx, before, after, action) ([]Issue, error)
        +AnalyzeUX(ctx, navGraph) ([]Issue, error)
        +AnalyzeAccessibility(ctx, screen) ([]Issue, error)
        +CreateTicket(ctx, issue) (*Ticket, error)
    }

    class SessionRecorder {
        -sessionID string
        -outputDir string
        -videos map~string,*VideoManager~
        -timeline *Timeline
        -screenshotIdx int
        -mu sync.Mutex
        +StartRecording(ctx, platform) error
        +StopRecording(ctx, platform) (string, error)
        +CaptureScreenshot(ctx, platform, name) (Screenshot, error)
        +RecordEvent(event TimelineEvent)
        +VideoTimestamp(platform) time.Duration
        +ExportTimeline() []TimelineEvent
    }

    class ActionExecutor {
        <<interface>>
        +Click(ctx, x, y) error
        +Type(ctx, text) error
        +Scroll(ctx, direction, amount) error
        +LongPress(ctx, x, y) error
        +Swipe(ctx, fromX, fromY, toX, toY) error
        +KeyPress(ctx, key) error
        +Back(ctx) error
        +Home(ctx) error
        +Screenshot(ctx) ([]byte, error)
    }

    SessionCoordinator "1" --> "*" PlatformWorker : manages
    SessionCoordinator "1" --> "1" PhaseManager : tracks phases
    SessionCoordinator "1" --> "1" SessionRecorder : records
    PlatformWorker "1" --> "1" NavigationEngine : navigates
    PlatformWorker "1" --> "1" IssueDetector : detects issues
    PlatformWorker "1" --> "1" SessionRecorder : captures evidence
    NavigationEngine "1" --> "1" ActionExecutor : executes actions
```

### State Diagram: PhaseManager

```mermaid
stateDiagram-v2
    [*] --> Setup_Pending

    state "Phase 1: Setup" as P1 {
        Setup_Pending --> Setup_Running : Start("setup")
        Setup_Running --> Setup_Completed : Complete("setup")
        Setup_Running --> Setup_Failed : Fail("setup", err)
    }

    state "Phase 2: Doc-Driven" as P2 {
        DocDriven_Pending --> DocDriven_Running : Start("doc-driven")
        DocDriven_Running --> DocDriven_Completed : Complete("doc-driven")
        DocDriven_Running --> DocDriven_Failed : Fail("doc-driven", err)
        DocDriven_Pending --> DocDriven_Skipped : Skip("doc-driven")
    }

    state "Phase 3: Curiosity" as P3 {
        Curiosity_Pending --> Curiosity_Running : Start("curiosity")
        Curiosity_Running --> Curiosity_Completed : Complete("curiosity")
        Curiosity_Running --> Curiosity_Failed : Fail("curiosity", err)
        Curiosity_Pending --> Curiosity_Skipped : Skip("curiosity")
    }

    state "Phase 4: Report" as P4 {
        Report_Pending --> Report_Running : Start("report")
        Report_Running --> Report_Completed : Complete("report")
        Report_Running --> Report_Failed : Fail("report", err)
    }

    Setup_Completed --> DocDriven_Pending
    Setup_Failed --> [*]

    DocDriven_Completed --> Curiosity_Pending
    DocDriven_Failed --> Report_Pending
    DocDriven_Skipped --> Curiosity_Pending

    Curiosity_Completed --> Report_Pending
    Curiosity_Failed --> Report_Pending
    Curiosity_Skipped --> Report_Pending

    Report_Completed --> [*]
    Report_Failed --> [*]
```

### Flowchart: Navigation Engine Decision Flow

```mermaid
flowchart TD
    A[Receive navigation target<br/>or explore command] --> B{Target screen<br/>known in graph?}

    B -->|Yes| C[Compute shortest path<br/>via BFS on NavigationGraph]
    B -->|No| D[Ask LLM agent:<br/>How to reach target?]

    C --> E[Execute path actions<br/>step by step]
    D --> F[LLM suggests<br/>navigation actions]
    F --> E

    E --> G[Capture screenshot<br/>after action]
    G --> H[VisionEngine:<br/>Analyze screen]

    H --> I{Screen matches<br/>expected target?}
    I -->|Yes| J[Update NavigationGraph<br/>Mark screen visited]
    I -->|No| K{Retry count<br/>exceeded?}

    K -->|No| L[Adjust strategy:<br/>GoBack + try alternate path]
    L --> E
    K -->|Yes| M[Log failure<br/>Record partial evidence]

    J --> N{Issues detected<br/>on screen?}
    N -->|Yes| O[IssueDetector:<br/>Classify + create ticket]
    N -->|No| P[Continue to<br/>next target/action]

    O --> P
    M --> P

    P --> Q{More targets<br/>or exploration budget?}
    Q -->|Yes| A
    Q -->|No| R[Return results<br/>to PlatformWorker]

    style A fill:#e1f5fe
    style R fill:#e8f5e9
    style M fill:#fff3e0
    style O fill:#fce4ec
```

### Vision Provider Architecture

HelixQA uses a dual-model architecture for autonomous QA sessions:

```
                    ┌─────────────────────────────┐
                    │     LLMsVerifier             │
                    │  (Dynamic Model Selection)   │
                    └──────────┬──────────────────┘
                               │ probe + score + rank
                    ┌──────────▼──────────────────┐
                    │     Available Providers       │
                    ├──────────────────────────────┤
                    │  Vision Models:               │
                    │  ├── Astica.AI (specialized)  │
                    │  ├── Gemini 2.0 Flash         │
                    │  ├── OpenAI GPT-4o            │
                    │  ├── Ollama (local, free)     │
                    │  └── llama.cpp RPC (distrib.) │
                    │                               │
                    │  Chat Models:                 │
                    │  ├── Any text-capable cloud   │
                    │  └── Local Ollama text models  │
                    └──────────┬──────────────────┘
                               │ best model per phase
              ┌────────────────┼────────────────┐
              │                │                │
     ┌────────▼────┐  ┌───────▼───────┐ ┌──────▼──────┐
     │  Learn/Plan  │  │Execute/Curiosity│ │  Analyze    │
     │  (Chat)      │  │   (Vision)      │ │  (Chat)     │
     └─────────────┘  └────────────────┘ └─────────────┘
```

**Key design decisions:**
- No hardcoded model preferences. All selection is score-based via LLMsVerifier.
- Astica.AI is a specialized vision API that competes on score alongside general-purpose providers.
- Local Ollama models get cost=1.0 (free) and compete on quality/speed/reliability.
- Distributed llama.cpp RPC splits large models across host-a.example.com (GPU) + host-b.example.com (CPU).
- FallbackProvider in VisionEngine chains multiple providers for resilience.

### Bridge Adapter Pattern

HelixQA acts as the sole integration point. External modules (LLMsVerifier, LLMOrchestrator, VisionEngine, DocProcessor) define their own interfaces with no cross-dependencies. HelixQA bridges them via adapter implementations:

```
LLMOrchestrator.Agent ──► agentLLMAdapter ──► DocProcessor.LLMAgent
LLMOrchestrator.Agent ──► visionAgentAdapter ──► VisionEngine.VisionProvider
VisionEngine.NavigationGraph ◄── NavigationEngine holds reference
LLMsVerifier.StrategyScore ──► ModelInfo ──► LLMOrchestrator
```

### Resilience Architecture

The system implements 5 degradation levels:

1. **Full capability** -- All LLM + Vision working: full autonomous session
2. **Degraded vision** -- LLM Vision fails: GoCV-only mechanical analysis
3. **Degraded navigation** -- Agent failures: collect partial evidence, generate partial report
4. **Session abort** -- Unrecoverable errors: clean shutdown with error report
5. **Per-agent circuit breaker** -- 3 consecutive failures mark agent unhealthy, replacement acquired from pool

Every LLM call uses: exponential backoff (1s/2s/4s), malformed JSON fallback with re-prompt, and prompt injection sanitization.
