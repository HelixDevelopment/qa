---
schema_version: 1
constitution_rule: CONST-035
last_audit: 2026-05-01
---

# Behavior Anchor Manifest — HelixQA

Every row is a user-facing capability and the single anchor test that
proves it works end-to-end. See CONST-035 in `CONSTITUTION.md`.

## Status legend

- `active` — anchor exists and is callable; capability is verified.
- `pending-anchor` — capability declared, anchor test does not yet
  exist. Listed in `challenges/baselines/bluff-baseline.txt` Section 3.
  Reducing this state is the work of campaign sub-project 4.
- `retired` — capability removed; row kept for history.

## Path format

For Go tests: `<path>.go::<TestFuncName>`. Verifier greps for
`func <TestFuncName>\b` in the file.

## Capabilities

| id | layer | capability | anchor_test_path | verifies | status |
|----|-------|------------|------------------|----------|--------|
| CAP-001 | submodule:HelixQA | Construct orchestrator with default options | pkg/orchestrator/orchestrator_test.go::TestNew | New() returns a usable Orchestrator with default detector/validator/reporter | active |
| CAP-002 | submodule:HelixQA | Construct platform-default crash/ANR detector | pkg/detector/detector_test.go::TestNew_DefaultPlatform | Detector.New() defaults to the host's primary platform (linux/android/web) | active |
| CAP-003 | submodule:HelixQA | Detect Android process-alive without false-positive crash | pkg/detector/android_test.go::TestCheckAndroid_ProcessAlive_NoCrash | Android detector reports process alive when no crash signals are present in logcat | active |
| CAP-004 | submodule:HelixQA | Validate LLM crash-analysis output schema | pkg/detector/llm_analyzer_test.go::TestCrashAnalysis_Validate_Valid | CrashAnalysis JSON schema validation passes on well-formed LLM responses | active |
| CAP-005 | submodule:HelixQA | Construct step validator with defaults | pkg/validator/validator_test.go::TestNew_Defaults | Validator.New() returns a configured validator with default evidence collector | active |
| CAP-006 | submodule:HelixQA | Construct evidence collector with defaults | pkg/evidence/collector_test.go::TestCollector_New_Defaults | Evidence collector exposes types: screenshot, video, log, ticket | active |
| CAP-007 | submodule:HelixQA | Generate Markdown ticket from a crashing test step (with evidence path) | pkg/ticket/ticket_test.go::TestGenerator_GenerateFromStep_Crash | Ticket carries non-empty evidence_paths, session_id, step_number, repro_steps | active |
| CAP-008 | submodule:HelixQA | Construct reporter with defaults | pkg/reporter/reporter_test.go::TestNew_Defaults | Reporter.New() returns a usable reporter with default Markdown/JSON outputs | active |
| CAP-009 | submodule:HelixQA | Load test bank file (YAML/JSON) | pkg/testbank/manager_test.go::TestManager_LoadFile | TestBankManager.LoadFile() parses test bank and registers cases | active |
| CAP-010 | submodule:HelixQA | Default config exposes sensible platform/speed/format defaults | pkg/config/config_test.go::TestDefaultConfig | DefaultConfig() returns a non-empty config with required fields | active |
| CAP-011 | submodule:HelixQA | userflow-runner replay subcommand parses replay DSL from session log | cmd/helixqa/replay_test.go::TestExtractReplayDSL_Present | ExtractReplayDSL detects a present replay block in session-log Markdown | active |
| CAP-012 | submodule:HelixQA | FindingsBridge persists two distinct findings with non-empty evidence | pkg/autonomous/findings_bridge_test.go::TestFindingsBridge_Process_TwoFindings | Process() persists 2 findings as 2 tickets, each with evidence_paths | active |
| CAP-013 | submodule:HelixQA | LLM fallback chain returns first provider's success without trying next | pkg/autonomous/fallback_test.go::TestFallbackChain_FirstSucceeds | FallbackChain.Execute() short-circuits on first provider's success | active |
| CAP-014 | submodule:HelixQA | Adaptive LLM provider selects first ranked provider | pkg/llm/adaptive_test.go::TestAdaptiveProvider_SelectsFirst | AdaptiveProvider.Chat() routes to highest-scored provider | active |
| CAP-015 | submodule:HelixQA | Test plan generator produces test cases from knowledge base | pkg/planning/planner_test.go::TestTestPlanGenerator_Generate | TestPlanGenerator.Generate() returns a non-empty test plan with priority ordering | active |
| CAP-016 | submodule:HelixQA | Vision analyzer produces analysis from a screenshot | pkg/analysis/vision_test.go::TestVisionAnalyzer_AnalyzeScreenshot | VisionAnalyzer.AnalyzeScreenshot() returns AnalysisResult with non-empty findings | active |
| CAP-017 | submodule:HelixQA | scrcpy recorder reports start state correctly | pkg/video/scrcpy_test.go::TestScrcpyRecorder_StartState | ScrcpyRecorder transitions from Idle → Recording on Start() | active |
| CAP-018 | submodule:HelixQA | Geo-restriction probe registry: register endpoint + alternative pair | pkg/autonomous/geo_probe_test.go::TestRegisterEndpoint_And_Alternative | RegisterEndpoint() + RegisterAlternative() expose the registered pair via the lookup API (no project-specific defaults baked in) | active |
| CAP-019 | submodule:HelixQA | Cost tracker constructor produces a usable tracker | pkg/llm/cost_tracker_test.go::TestNewCostTracker | NewCostTracker() returns a thread-safe tracker with zero baseline cost and per-provider rate registry | active |
| CAP-020 | submodule:HelixQA | Anthropic provider exposes its canonical name | pkg/llm/anthropic_test.go::TestAnthropicProvider_Name | AnthropicProvider.Name() returns "anthropic" — used by AdaptiveProvider routing | active |
| CAP-021 | submodule:HelixQA | Full autonomous QA pipeline executes end-to-end on a single platform | pkg/orchestrator/integration_test.go::TestIntegration_FullPipeline_SinglePlatform | Pipeline runs Learn → Plan → Execute → Curiosity → Analyze → Report and produces a populated PipelineResult | active |
| CAP-022 | submodule:HelixQA | Phase manager initial state is Pending for every declared phase | pkg/autonomous/phase_test.go::TestNewPhaseManager | NewPhaseManager() creates phases (setup/doc-driven/curiosity/report) all in Pending status | active |
| CAP-023 | submodule:HelixQA | Test-feature validator accepts a well-formed feature definition | pkg/testbank/generator_test.go::TestFeature_Validate_Valid | Feature.Validate() returns nil for a feature with non-empty id/name/steps | active |
| CAP-024 | submodule:HelixQA | OCU evidence kinds are exhaustively defined (no "unknown" leaks) | pkg/ticket/ocu_evidence_test.go::TestOCUEvidenceKinds_NonEmpty | EvidenceKinds() returns a non-empty list — every ticket type has a registered evidence kind | active |
| CAP-025 | submodule:HelixQA | PELT change-point detector flags a real step change | pkg/analysis/pelt/pelt_test.go::TestPELT_DetectsStepChange | PELT.Detect() returns at least one change-point on a synthetic series with a clear step | active |
| CAP-026 | submodule:HelixQA | Frame extractor produces well-formed ffmpeg argv | pkg/video/frames_test.go::TestFrameExtractor_BuildFFmpegArgs | BuildFFmpegArgs() returns argv with -i input, -vf select filter, -y output (validated against reference) | active |
| CAP-027 | submodule:HelixQA | helixqa-x11grab CLI argv parser surfaces health subcommand | cmd/helixqa-x11grab/main_test.go::TestParseArgv_Health | ParseArgv(["health"]) returns the Health flag set without consuming additional positional args | active |
| CAP-028 | submodule:HelixQA | Vision-gated step is BLOCKED, never passed, without a usable vision verdict | pkg/autonomous/no_bluff_vision_test.go::TestNoBluffVision_GatedProviderErrorIsBlocked | a provider error on a vision_verify step yields Blocked=true, Passed=false, no action-success fallback | active |
| CAP-029 | submodule:HelixQA | Vision verdict is bound to the exact after-action frame | pkg/autonomous/no_bluff_vision_test.go::TestNoBluffVision_GatedVerifiedYesPassesAndBindsFrame | step result carries the after-frame sha256 + pHash and the model saw exactly those bytes | active |
| CAP-030 | submodule:HelixQA | recognize/forbid tokens checked on an expectation-blind transcription | pkg/autonomous/no_bluff_vision_test.go::TestNoBluffVision_RecognizeForbidTokens | missing/forbidden whole-word tokens in texts elements FAIL; tokens never reach the prompt; substring decoys do not trip | active |
| CAP-031 | submodule:HelixQA | Golden pHash comparison for vision-gated steps | pkg/autonomous/no_bluff_vision_test.go::TestNoBluffVision_GoldenPHash | identical golden passes at distance 0, different golden FAILs, missing golden BLOCKs | active |
| CAP-032 | submodule:HelixQA | Local OpenAI-compatible vision endpoint (HELIX_VISION_ENDPOINT) | pkg/llm/vision_endpoint_test.go::TestVisionEndpoint_RealHTTPRoundTrip | real HTTP round trip to an OpenAI-shaped server: image data URI, model, Bearer only when a key is set | active |
| CAP-033 | submodule:HelixQA | BLOCKED structured case reaches report, finding and process exit code 2 | pkg/autonomous/no_bluff_vision_pipeline_test.go::TestPipelineResult_StructuredBlockedReachesExitCodeAndReport | runStructuredPhase records the blocked case on PipelineResult; pipeline-report.json carries frame sha256/pHash; a finding is filed; ExitCode() is 2 | active |
| CAP-034 | submodule:HelixQA | A vision answer is exactly one standalone VERIFIED line | pkg/autonomous/no_bluff_vision_review_test.go::TestNoBluffVision_VerifiedAnswerMustBeOneStandaloneLine | echoed template, UNVERIFIED, prose and duplicate answer lines are BLOCKED; a standalone yes/no line decides | active |
| CAP-035 | submodule:HelixQA | recognize/forbid read only texts elements of a strict JSON reply | pkg/autonomous/no_bluff_vision_json_test.go::TestNoBluffVision_DescriptionProseNeverMatches | prose in description never satisfies recognize nor trips forbid; filler-only texts are empty | active |
| CAP-036 | submodule:HelixQA | Vision provider fall-through with operator endpoint first | pkg/autonomous/no_bluff_vision_review_test.go::TestNoBluffVision_ProviderFallThroughAndOrdering | ambiguous/failed provider falls through; endpoint asked first; a definite answer is final | active |
| CAP-037 | submodule:HelixQA | Vision-gated bank case is never promoted SKIPPED to PASSED | pkg/orchestrator/aggregation_test.go::TestCaseHasUnrunAssertingStep_VisionGated | a step with vision keys (any action kind) counts as an unrun asserting step, so crash-absence never promotes its case | active |
| CAP-038 | submodule:HelixQA | Desktop shell PASS never ignores an unrun gated/asserting step | pkg/orchestrator/no_bluff_vision_orch_test.go::TestDesktopShellCase_SkipReasonNamesUnexecutedSteps | shell steps exit 0 + a gated / frame_diff / playwright step -> SKIPPED whose reason names step <i> "<name>" (<action>) | active |
| CAP-039 | submodule:HelixQA | A skipped vision-gated step BLOCKS its case | pkg/autonomous/no_bluff_vision_review_test.go::TestNoBluffVision_SkippedGatedStepBlocksCase | prose action or playwright without CDP on a gated step -> case BLOCKED, exit 2 | active |
| CAP-040 | submodule:HelixQA | BLOCKED is an environment finding, never an app bug | pkg/autonomous/no_bluff_vision_findings_test.go::TestVisionFindings_BlockedIsEnvironmentNotADefect | category environment; excluded from bug reproduction, ticketing and issues_found | active |
| CAP-041 | submodule:HelixQA | Blind transcription must be exactly one strict JSON object | pkg/autonomous/no_bluff_vision_json_test.go::TestNoBluffVision_BlindReplyMustBeStrictJSON | the reviewer's 16 heading bypasses, extra/duplicate/missing keys, wrong types, trailing prose -> BLOCKED; one whole-reply fence tolerated | active |
| CAP-042 | submodule:HelixQA | Secret-bearing vision endpoint URL refused, transport errors scrubbed | pkg/llm/vision_endpoint_review_test.go::TestVisionEndpointConfig_RefusesSecretBearingURL | user info / query / fragment refused at load without echoing the value | active |
| CAP-043 | submodule:HelixQA | helixqa run three-valued exit (0 pass / 1 fail / 2 incomplete) | pkg/orchestrator/no_bluff_vision_orch_test.go::TestOrchRun_ShellPlusGatedBankNotPassed | end-to-end orch.Run: shell+vision_verify bank is not a success, 0 passed, exit 2; shell-only 0; failing shell 1 | active |
| CAP-044 | submodule:HelixQA | Capture problems and missing executors are BLOCKED environment, not app defects | pkg/autonomous/no_bluff_vision_round5_test.go::TestNoBluffVision_CaptureAndExecutorKindFailuresBlock | frame_diff screenshot failure / non-comparable frames, playback_check / adb_shell on a non-Android executor -> BLOCKED with an environment finding | active |
| CAP-045 | submodule:HelixQA | Local endpoint gets a JSON-schema response_format; cloud providers do not | pkg/llm/vision_endpoint_review_test.go::TestVisionEndpoint_ResponseSchemaOnlyForEndpoint | response_format json_object+schema only for vision-endpoint with a requested schema | active |
| CAP-046 | submodule:HelixQA | Capture failures and Android-only actions on a non-Android executor are BLOCKED; host-only steps run without an executor | pkg/autonomous/no_bluff_vision_round5_test.go::TestNoBluffVision_HostOnlyStepsRunWithoutExecutor | shell exit 1 without executor FAILS (not hidden); UI / gated steps BLOCKED naming the missing executor | active |
| CAP-047 | submodule:HelixQA | Every provider redacts its key in the full error body before truncating, with a capped read | pkg/llm/vision_endpoint_round5_test.go::TestProviders_ErrorBodyRedactedBeforeTruncation | raw / any-case / URL-escaped / base64 key forms never reach the error text; no prefix at the 512-byte cut | active |
| CAP-048 | submodule:HelixQA | Strict limits on the blind JSON reply (UTF-8, element count/length, control chars, single json fence) | pkg/autonomous/no_bluff_vision_round5_test.go::TestNoBluffVision_BlindReplyLimits | each violation makes the reply unusable | active |
| CAP-049 | submodule:HelixQA | recognize_exact: a token must equal a whole texts element | pkg/autonomous/no_bluff_vision_round5_test.go::TestNoBluffVision_RecognizeExactElement | "(no Login button visible)" does not satisfy recognize [Login] in exact mode | active |
| CAP-050 | submodule:HelixQA | NFKC + zero-width folding before token matching | pkg/autonomous/no_bluff_vision_round5_test.go::TestNoBluffVision_UnicodeFolding | zero-width splits, fullwidth, math alphanumerics, ligatures cannot hide a forbid token | active |
| CAP-051 | submodule:HelixQA | helixqa run exits 2 when nothing executed; panic exits 70 | cmd/helixqa/exit_codes_test.go::TestRunExitCode | orch.Run error (unloadable bank) -> 2; result's three-valued code otherwise | active |
| CAP-052 | submodule:HelixQA | A structured case PASSes only when an asserting step ran | pkg/autonomous/no_bluff_vision_round6_test.go::TestNoBluffVision_R6_NoAssertingStepIsBlocked | sleep-only / sleep+prose / unconfirmed-screenshot cases BLOCKED with and without a device; shell / keypress / model-confirmed screenshot cases still PASS | active |
| CAP-053 | submodule:HelixQA | VERIFIED reply honoured only as one answer line plus at most one ACTUAL line | pkg/autonomous/no_bluff_vision_round6_test.go::TestNoBluffVision_R6_VerifiedReplyStrictLines | refusal, conditional, retraction, self-contradiction, filled template, fence, two ACTUAL lines -> BLOCKED | active |
| CAP-054 | submodule:HelixQA | Empty / filler-only / refusing transcription never makes forbid "absent" | pkg/autonomous/no_bluff_vision_round6_test.go::TestNoBluffVision_R6_EmptyOrRefusingTranscriptionBlocks | texts [] / (none) / refusal sentences -> BLOCKED (also with vision_verify yes); a readable second provider is used | active |
| CAP-055 | submodule:HelixQA | Missing environment is BLOCKED, not FAILED | pkg/autonomous/no_bluff_vision_round6_test.go::TestNoBluffVision_R6_MissingEnvironmentIsBlocked | http without base URL, offline device keypress / adb_shell / playback_check, failed screenshot capture -> BLOCKED; a rejected key press still FAILS | active |
| CAP-056 | submodule:HelixQA | Configured device / display probed once per platform | pkg/autonomous/no_bluff_vision_round6_test.go::TestNoBluffVision_R6_UnreachableDeviceProbedOnce | unreachable probe -> UI steps BLOCKED naming it, host steps run, probe called once | active |
| CAP-057 | submodule:HelixQA | Session environment aborts exit 2 | pkg/autonomous/no_bluff_vision_round6_test.go::TestNoBluffVision_R6_SessionEnvironmentAbortIsBlocked | preflight / plan-phase provider error / timeout -> status blocked, exit 2; a FAILED structured case still 1 | active |
| CAP-058 | submodule:HelixQA | recognize exact by default; recognize_contains opt-in; invisible code points folded | pkg/testbank/vision_round6_test.go::TestVisionFields_R6_RecognizeContains | default exact, contains needs recognize and contradicts recognize_exact | active |
| CAP-059 | submodule:HelixQA | Echoed key forms redacted (Bearer base64, embedded base64, percent, hex, JSON escapes) | pkg/llm/vision_endpoint_round6_test.go::TestProviders_R6_RedactsEchoForms | no 12-char window of the key recoverable by base64 / hex / unescape decoding | active |
| CAP-060 | submodule:HelixQA | CLI exit wiring of the real binary | cmd/helixqa/cli_exit_test.go::TestCLIExit_RunWiring | subprocess: unloadable bank 2, pass 0, fail 1, fail + report write error 1, pass + report write error 2 | active |

(Manifest now covers core orchestrator+autonomous+LLM+evidence+
testbank+ticket+analysis+video+CLI capabilities — 27 active rows.
Long-tail: per-LLM-provider tests, video segmenting/concat,
device-preservation restore paths, websocket streaming, and the
full ocu/oclaw campaign integration.)
