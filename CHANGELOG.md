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
  recorded on every step result). New step/case verdict BLOCKED
  (could not determine, `ExitCode()` 2) and `TestCasesBlocked`;
  `StructuredExecutionResult.Cases` keeps per-case evidence.
- **Bank keys** `golden`, `phash_max_distance`, `recognize`, `forbid`,
  `expect_change`, with load-time refusal of vacuous or contradictory use.
- **Local OpenAI-compatible vision endpoint**: `HELIX_VISION_ENDPOINT`
  (+ optional `HELIX_VISION_MODEL`, `HELIX_VISION_API_KEY`) registers provider
  `vision-endpoint` in the vision pool (e.g. llama-server / llmctl `vision`).
- `challenges/scripts/no_bluff_vision_mutation_challenge.sh`: 21 paired
  mutations run through `go test -overlay`; every one must turn the gate RED.
- `docs/vision/NO_BLUFF_VISION.md` reference with judging state diagram.

### Changed

- A vision provider error, ambiguous, self-contradictory or empty reply no
  longer "falls back to action success" for a gated step (it is BLOCKED); a
  non-gated step the model could not confirm is labelled
  `UNVERIFIED-by-vision`.
- Non-gated `shell:` / `http:` / `assert:` steps are no longer judged by a
  screenshot (previously a model's `VERIFIED: no` about an unrelated screen
  failed a passing shell step).
- The OpenAI-style provider sends no `Authorization` header when no API key
  is configured.

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
