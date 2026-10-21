# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.0] - 2026-10-21

### Fixed

- Sync script reads git config in ALL scopes (`--includes --show-scope`): exactly one `remote.origin.url` from the local scope, any local/worktree/command-scope `url.*.insteadOf`/`pushInsteadOf` and `extensions.worktreeConfig` are WRONG-REPO (only the user's own global/system rewrites count). `old_path` must contain a `/`. Hosts may contain `_` and single-label hosts (ssh aliases such as `box`) are ALLOWED in both Go `NormURL` and the shell; ports are normalised (`:0443` == default, 1-65535) and a trailing dot on the host is dropped. A partial standalone checkout now FAILS in the Go audit as it does in the script (SKIP only when nothing is checked out). The script unsets inherited shell functions, uses `LC_ALL=C` for URL matching and words nested (`tools/test-apps/...`) legacy resolution separately.
- Manifest rows outside `tools/opensource` (e.g. `tools/test-apps/rest-demo`) now resolve: `ResolveToolAt` / `resolve_tool.sh <name> <root> <old_path>` treat `<root>/<old_path>` as the exact legacy location; Go audit and the script no longer disagree. Standalone checkouts with no tools print SKIP and exit 0 (consumer layout still exit 3); header-only manifest exits 2; whitespace-only lines are blank in Go and shell; `ext::`/`fd::`/non-host scp forms are rejected (Go `NormURL`, validated in `LoadManifest`); `git config --includes` catches include-injected insteadOf / second origin; every exported `GIT_*` variable is scrubbed.
- **Sync script is now READ-ONLY (structural replacement).** `scripts/sync-opensource-references.sh` no longer fetches, pulls, checks out, merges or contacts the network; `--pull` / `--fetch-only` are removed (unknown arguments exit 2). It reports tier, MANIFEST_PIN, CHECKOUT_HEAD and DRIFT per manifest row using only hardened `git rev-parse`/`config`/`ls-remote --get-url`, requires exactly one `remote.origin.url`, treats tool-local `url.*.insteadOf` as WRONG-REPO and compares the EFFECTIVE origin url to the manifest url. To move a pin use the parent's `git submodule update`.
- `LoadManifest` validates the header, empty columns, 40-hex SHA (lower-cased), `new_name` == derived snake name, tool name and duplicate rows; `AuditTools` matches doc entries on token boundaries (`ui-tars` no longer satisfied by `ui-tars-desktop`).
- Sync script hardening (class-wide): no symlink component allowed in a resolved tool path and path must sit under the expected parent; ambient `GIT_*` env unset and all git via `git -C`; `--pull` only `merge --ff-only` from a strict-ancestor HEAD (AHEAD/DIVERGED/DETACHED-LOCAL-COMMITS refused, SKIP-DIRTY counted in the summary, no checkout); strict manifest parsing (CRLF, no final newline, empty columns, 40-hex sha, name validation, row-count assertion); URL comparison accepts only https/ssh/scp forms, keeps non-default ports and path case.
- Sync script verifies each resolved dir is THE expected tool: origin must match the manifest url (ssh/https/scp-normalised) else `WRONG-REPO`; dirs that are/contain the consumer root or helix_qa are rejected (symlink to consumer root no longer fast-forwarded); unenterable dirs give an `ERROR` row instead of aborting; table shows MANIFEST_PIN, CHECKOUT_HEAD, REMOTE_HEAD and flags `DRIFT`.
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
