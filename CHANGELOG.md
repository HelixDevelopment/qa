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
- `recognize` / `forbid` match only the `TEXT:` transcription of an
  expectation-blind reading, never the model's own prose.
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
