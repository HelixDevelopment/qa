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

- **Final cleanup (review round 9):** load warnings are logged at WARN
  level and `_skip` default-2xx rows are printed one per line by both
  runners; "(0 in a dependency cycle)" is no longer printed; a case ID
  defined in two non-peer bank files is a WARNING naming both (not an
  error: bank directories load whole; one such ID in this repository's
  banks, none in catalogizer / boba); a shell command that exited on its
  own is scored on its outcome even if the run was cancelled meanwhile;
  the ACTUAL label is stripped by a case-insensitive prefix check. Docs:
  quoted first-person app text is BLOCKED (fail-safe), more listed ACTUAL
  misses, recall cost of the "image of …" exclusion. Mutants added.
- **Round-9 hardening (review round 8):** the interruption is structural
  — text in a step's output ("interrupted-by-run-cancellation") can no
  longer turn a FAILED challenge into SKIPPED (round 8 exited 2 for it).
  `http:` steps relying on the default 2xx are listed at load (executed:
  WARNING; `_skip`: separately); the migration recipe is documented. The
  ACTUAL inability scan needs a first-person subject (or the text start),
  ignores an app image and knows attached / provided images; a NO resting
  on "could not see the image" is BLOCKED, not an app defect. Documented
  limits: no-space-script rule (mixed-script tokens, glued Latin, Hangul,
  OCR-spaced CJK), English-only refusal detection, dependencies on cases
  outside the run ignored, SIGKILL without grace. Mutants Q01-Q08 (Q02
  equivalent, removed).
- **Round-8 hardening (review round 7):** Ctrl-C during a real `shell:`
  step is SKIPPED "interrupted" (round 7 still exited 1 for a cancel: the
  interrupted result had no assertion and the runner's anti-bluff gate
  turned it into FAILED); a step that failed before a deadline stays
  FAILED; challenges that never started are listed. `shell:` steps run in
  their own process group, killed whole on cancellation (pkg/shellexec).
  **Changed (round 7's ID order is corrected):** challenges — in `run` and
  in the autonomous structured phase — run in DEPENDENCY order, ID as the
  tie-break; a dependency cycle is reported, not run. The ACTUAL scan now
  matches only "could not see / view / read the image" wording and only for
  a yes (round 7 blocked "The screenshot shows …" and definite NOs); its
  limits are documented (waived). An empty `expected` is never sent to the
  model. **Breaking:** an `http:` step without `expect_status` must return
  a 2xx (46 bank steps declare none — all 46 are `_skip: true`, so no
  executed step changes verdict; listed at load since round 9). A refusal sentence mentioning a forbidden
  word is not a FAIL; Han / kana / Thai / Lao / Khmer / Myanmar tokens
  match as substrings; tatweel folded. `sleep:` honours cancellation;
  `_skip: true` steps are not run by the structured phase; adb get-state
  reads its state line. Waived (documented, no code): ACTUAL-hedge wording
  heuristics, frame_diff over a black DRM area under visible chrome.
  Mutants Z01-Z27 added.
- **Round-7 hardening (review round 6):** a structured case PASSes only
  when a step OBSERVED app state (vision-confirmed screen, `http:`,
  `assert:`, `shell:`, `playback_check:`, `frame_diff:`, playwright
  `assertvisible` / `waitfor`) and no step was skipped as not executable —
  an accepted `tap:` / `keypress:` is not evidence (catalogizer on a fake
  device with no app: 313 PASSED -> 0; this repository's banks 203 -> 0).
  Device loss is decided by re-running the reachability probe UNCACHED
  after any device-action failure (never by error text); the probe reads
  the last `adb get-state` line and caches a failure for 15 s only. The
  structured executor BLOCKS a step once the session context ended (before
  or during it). `forbid`: a token PRESENT fails first; an absence needs a
  matched `recognize` anchor in the same reading — **breaking:** `forbid`
  now requires `recognize` at load (no consumer bank uses `forbid`); a
  reading is a refusal only when EVERY element is the model talking. A
  `recognize` token found only inside a longer element is BLOCKED (points
  to `recognize_contains`). The `ACTUAL:` text is scanned for a second
  verdict / a refusal. Combining marks are part of a word (Arabic harakat /
  Hebrew niqqud dropped). `frame_diff` BLOCKs only on black frames.
  `assert:` reads only a response of the same case (else BLOCKED).
  `helixqa run` keeps a platform's partial result on cancellation /
  `--timeout` (a FAILED challenge exits 1; an interrupted one is SKIPPED);
  `helixqa autonomous` exits 2 when its report cannot be written.
  Challenges run in ID order. Mutants Y01-Y35 added (206 mutants, all killed by assertion).
- **Round-6 hardening (review round 5):** a structured case PASSes only
  when at least one ASSERTING step ran (sleep / screenshot / prose-only
  cases are BLOCKED — catalogizer no-executor sweep 12 PASSED -> 0);
  `VERIFIED` replies must be exactly one answer line plus at most one
  `ACTUAL:` line; an empty / filler-only / refusing blind transcription is
  unusable (forbid is never "absent" on it); U+2028 / U+2029 in an element
  make the reply unusable; invisible default-ignorable / variation-selector
  code points are dropped from tokens. **Breaking:** `recognize` matches
  whole elements by default; new key `recognize_contains` opts into
  contained-words matching. Missing environment is BLOCKED, not FAILED:
  `http:` without a base URL, black `frame_diff` frames (round 6 also
  blocked any low-contrast frame — corrected in round 7), an unreachable
  device / display (probed per platform: `adb get-state`, one capture for
  desktop / web; round 6 matched adb / X11 error TEXT, which the real
  executors never return — replaced in round 7 by an uncached re-probe
  after any device-action failure), an ungated screenshot whose capture
  failed. The autonomous session gets `status: blocked` (exit 2) for a
  failed network / VPN preflight, learn / plan errors and timeouts.
  `helixqa run`: a FAILED verdict exits 1 even when an error follows
  execution (report write; and — only since round 7 — a cancellation /
  timeout that arrives WHILE a platform runs: round 6 dropped that
  platform's partial result). Provider error bodies also redact the
  key as base64 inside larger blobs (e.g. an echoed `Bearer` header), every
  byte percent-encoded, hex and JSON `\u` escapes; astica's status text is
  redacted. Mutants X01-X46 added to the no_bluff_vision challenge (178 mutants, all killed by assertion).
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
  element; the default since round 6). Token matching folds NFKC and drops
  zero-width / format (Cf) characters — and since round 6 also the other
  default-ignorable and variation-selector code points (cross-script
  homoglyphs are not folded — documented).
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
