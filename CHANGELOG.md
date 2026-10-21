# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.0] - 2026-10-21

### Fixed

- Sync script never runs git in a resolved dir that is not its own checkout (`NOT-A-CHECKOUT`), exits 3 when any row is NOT-FOUND/NOT-A-CHECKOUT/ERROR/PULL-FAILED, and warns on legacy `tools/opensource` resolution in a consumer layout. `ResolveToolTier`/`ToolsReport` expose the tier and warnings; names starting with `.` or equal to the module name are rejected; `ToolsRoot` deprecated.
- `scripts/sync-opensource-references.sh` iterates `docs/flat-tools-manifest.tsv` instead of globbing the tools directory, so it can never fetch/pull a consumer project's first-party submodules.
- `pkg/opensource` audit resolves each manifest tool (`AuditTools`); in a consumer layout a missing tools tree fails instead of skipping.

### Changed

- Third-party tool repos are no longer nested submodules under `tools/opensource/*`; they are flattened into the consumer project's `<root>/submodules/<snake_name>/` (manifest: `docs/flat-tools-manifest.tsv`). Resolution order (`pkg/opensource.ResolveTool`, `scripts/resolve_tool.sh`; candidates must be non-empty dirs; names with `/` or `..` rejected): `$HELIXQA_TOOLS_DIR/<name>` (authoritative when set: a miss is an error), sibling `<parent>/<snake_name>` when HelixQA sits in a `submodules` dir, then legacy `tools/opensource/<name>`.

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
