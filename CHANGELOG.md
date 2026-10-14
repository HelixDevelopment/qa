# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **No-bluff vision gate** (`pkg/autonomous/vision_gate.go`): bank steps
  with `vision_verify`, `recognize`, `forbid`, `golden` +
  `phash_max_distance`, or `expect_change` are vision-gated and PASS only on
  positive evidence bound to the exact after-action frame (sha256 + pHash
  recorded on every step result). New case verdict BLOCKED (could not
  determine) with `TestCasesBlocked` and a filed
  `Test Case BLOCKED (could not determine)` finding.
- **Process exit code** for `helixqa autonomous` via
  `PipelineResult.ExitCode()`: 1 session failed or a structured case FAILED;
  2 a case BLOCKED, cases ran but none passed, or banks could not run;
  0 otherwise. The structured outcome (per-case verdicts, per-step frame
  evidence) is persisted in `pipeline-report.json` under `structured`.
- **Bank keys** `golden`, `phash_max_distance` (0..16), `recognize`,
  `forbid`, `expect_change`, with load-time refusal of vacuous,
  contradictory or misplaced use (vision keys on shell/http/assert steps;
  `expect_change`/`forbid` without `vision_verify`/`recognize`/`golden`;
  prompt heading words as tokens).
- **Local OpenAI-compatible vision endpoint**: `HELIX_VISION_ENDPOINT`
  (+ optional `HELIX_VISION_MODEL`, `HELIX_VISION_API_KEY`) registers
  provider `vision-endpoint` (e.g. llama-server / llmctl `vision`), asked
  first, with temperature 0 and a fixed seed; empty model is omitted from
  the request; the key and URL userinfo/query are never printed.
- `challenges/scripts/no_bluff_vision_mutation_challenge.sh`: paired
  mutations run through `go test -overlay` (honours `TMPDIR`, `--only`);
  every mutant must be killed by an assertion, a build failure is
  UNDETERMINED.
- `docs/vision/NO_BLUFF_VISION.md` reference with judging state diagram.

### Changed

- **Round-5 hardening:** a `frame_diff` whose screenshot fails and
  `playback_check:` / `adb_shell:` on a non-Android executor are BLOCKED
  (environment), not FAILED app defects; with no executor, host-only steps
  (`shell:`, `http:`, `assert:`, `sleep:`) still run and decide on their own.
- **One reply helper for every provider** (`pkg/llm/http_body.go`): capped
  read (8 MiB), the provider's own key redacted in the full error body
  (any case, URL-escaped, base64) BEFORE truncation to 512 bytes. Google
  accepts a `BaseURL` override (default endpoint unchanged).
- **Blind JSON reply limits:** invalid UTF-8, >2000 elements, an element
  >200 characters or containing a line break / control character make the
  reply unusable. New bank key `recognize_exact` (token must equal a whole
  element). Token matching folds NFKC and drops zero-width / format
  characters (cross-script homoglyphs are not folded — documented).
- **Exit codes:** `helixqa run` exits 2 when nothing executed (missing or
  malformed bank; was 1); an unrecovered panic exits 70. CLI usage /
  configuration errors stay 1 (documented). `golang.org/x/text` becomes a
  direct dependency.
- **recognize / forbid redesigned (breaking):** the vision model now returns
  exactly one strict JSON object `{"texts": [...], "description": "..."}`
  (the local endpoint also gets a JSON-schema `response_format`); tokens
  match only whole words inside single `texts` elements, never the
  description. The `TEXT:`/`DESCRIPTION:` text format and all heading
  heuristics are removed; any non-JSON reply is unusable (next provider,
  then BLOCKED). Residual: commentary a model writes inside `texts` is
  treated as screen text — pair with `golden`.
- **`helixqa run` exit codes:** 0 all passed, 1 failure / crash / ANR,
  2 nothing failed but not every challenge executed (`INCOMPLETE`). A
  SKIPPED-only bank used to print `FAILED 0/1` and exit 1.
- The bank runner's SKIPPED reason names every unexecuted asserting step;
  steps pinned to another platform or `_skip`ped are not counted.
- Capture problems (`frame_diff` on undecodable / differently sized frames)
  and a missing executor (no device / display / browser) are BLOCKED
  environment findings, not FAILED app defects. Catalogizer's
  `catalogizer-androidtv-negative-paths-executable.yaml` uses `frame_diff`
  at lines 451 and 504 (measured at `d211f574`).
- Endpoint secrets: encoded `?`/`#` and `;` parameters in the path are
  refused; an error body that echoes the provider's own key is redacted;
  all providers scrub transport errors; a base URL already ending in
  `/chat/completions` is no longer doubled. Plain path-segment secrets are
  not detectable (documented).
- `EnhancedAdaptiveProvider.VisionVia` returns `llm.ErrCircuitOpen` when the
  member's circuit is open and a cancelled context's error unchanged.
- **Exit-code contract (consumers must branch on it):** 0 pass, 1 fail,
  2 blocked / nothing passed / banks not runnable. Exit 2 is not a warning.
  Catalogizer wrappers that need updating (not changed here; measured at
  `d211f574`): `scripts/helixqa-orchestrator.sh:314` (reports 2 as
  "failed"), `scripts/run-helixqa-all.sh:68` (2 lands in the failure
  branch), `scripts/run-helixqa-web.sh:131-133` (1 and 2 swallowed as a
  warning, then "complete"). See `docs/vision/NO_BLUFF_VISION.md`.
  **Open question for the operator:** an all-skipped structured run exits 2.
- A vision-gated step whose action is skipped (prose action, `playwright:`
  without CDP) BLOCKS its case; vision keys are refused on prose steps.
- The desktop bank runner no longer PASSES a case whose `shell:` steps
  passed while a vision-gated or other asserting step was never executed
  (honest SKIPPED instead).
- Filler tokens (`no`, `none`, `n/a`, …) are refused at load.
- BLOCKED findings are category `environment` (new
  `analysis.CategoryEnvironment`): never reproduced as a bug, never
  ticketed, not counted in `issues_found`.
- `HELIX_VISION_ENDPOINT` with user info, a query or a fragment is refused
  at startup (error never echoes it); transport errors are scrubbed; the
  request path is appended to the URL path.
- Member-by-member vision calls go through the wrapper
  (`llm.VisionRouter`) so availability, rate limit, circuit breaker and
  cost accounting apply; a cancelled context stops the provider loop.
- `frame_diff` FAILS on undecodable or differently sized frames instead of
  reporting motion (pre-existing defect).
- **Behaviour change for existing banks:** steps that already carried
  `vision_verify: true` were ignored and are now gated. In the catalogizer
  checkout that is 27 steps across 4 banks (measured 2026-10-14); without a
  vision provider answering in the `VERIFIED:` format those cases now
  report BLOCKED (exit 2) instead of PASSED.
- A vision answer is exactly one standalone `VERIFIED: yes|no` line; an
  echoed template, `UNVERIFIED: yes`, the phrase in prose, or several answer
  lines are ambiguous. An error / ambiguous reply moves on to the next
  vision provider; a definite answer is final; if none answers the gated
  step is BLOCKED (no more "falling back to action success"). A non-gated
  step the model could not confirm is labelled `UNVERIFIED-by-vision`.
- `recognize` / `forbid` match only the transcribed on-screen text of an
  expectation-blind reading (now the `texts` array of a strict JSON reply),
  never the model's own prose.
- Non-gated `shell:` / `http:` / `assert:` steps are no longer judged by a
  screenshot (previously a model's `VERIFIED: no` about an unrelated screen
  failed a passing shell step).
- Bank-runner promotion (SKIPPED -> PASSED on crash-absence) never applies
  to a case with a vision-gated step.
- The OpenAI-style provider sends no `Authorization` header without a key,
  reads at most 8 MiB of a reply and quotes at most 512 bytes of an error
  reply.

## [0.1.0] - 2026-03-18

### Added

- **Orchestrator**: Composable QA brain reusing Challenges runner and bank loader
- **Detector**: Real-time crash/ANR detection for Android (ADB), Web (pgrep), Desktop (pgrep/kill)
- **Validator**: Step-by-step validation with evidence collection (screenshots, logs)
- **Reporter**: Evidence-based QA reports in Markdown, HTML, and JSON formats
- **CLI**: `helixqa` command with platform, device, speed, output, and report flags
- **Configuration**: Type-safe config with validation, platform expansion, speed modes
- Comprehensive test suite with 150+ tests across all packages
- CLAUDE.md, AGENTS.md, CONTRIBUTING.md, and README.md documentation
- Makefile with build, test, lint, and coverage targets
