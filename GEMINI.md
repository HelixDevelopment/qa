# GEMINI.md — Gemini CLI context for this module

This file is read by Gemini CLI as its module-context file. It is the Gemini
CLI counterpart of CLAUDE.md and AGENTS.md for this module, and it is a
pointer: there is one canonical agent-instruction file per scope.

## Read CLAUDE.md — it is mandatory

This module's canonical agent-instruction file is CLAUDE.md in this
directory. Before doing any work in this module, open and read CLAUDE.md and
this module's CONSTITUTION.md in full. Every rule there binds Gemini CLI
exactly as it binds Claude Code.

This file is a plain-text pointer and deliberately uses no auto-import
directive — read CLAUDE.md directly.

## INHERITED FROM the Helix Constitution

This module is governed by the Helix Constitution. Gemini CLI MUST honour,
unconditionally, every rule in the constitution's CLAUDE.md and the
Constitution.md it references. Locate the constitution from any nested depth
via its `find_constitution.sh` helper — do NOT hardcode a path (this module
stays fully decoupled and project-agnostic per §11.4.28). Gemini CLI MUST NOT
weaken any inherited rule.

Canonical reference: https://github.com/HelixDevelopment/HelixConstitution

## What this module is

`digital.vasic.helixqa` is an anti-bluff QA orchestration framework (Go,
`go 1.26`). It runs YAML test banks across Android, Android TV, Web, and
Desktop targets with real-time crash/ANR detection, evidence collection,
ticket generation, and an LLM-driven Autonomous QA Session. Build with
`go build ./...` (CLI: `make build` → `bin/helixqa`); test with
`go test ./... -count=1` (or `make test`). See CLAUDE.md for full detail.

## Anti-Bluff — read first

Tests and Challenges exist for exactly one purpose: to confirm a feature
genuinely works for a real end user, end-to-end. A test that passes while
the feature is broken is a bluff test and is forbidden. CI green is
necessary, never sufficient. Because HelixQA's whole purpose is to catch
bluffs in other software, a bluff landed in HelixQA itself is the most
severe class of defect here. See this module's CLAUDE.md, AGENTS.md, and
CONSTITUTION.md for the full anti-bluff mandate. Canonical authority:
the Helix Constitution §11.4.
