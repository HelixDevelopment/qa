// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// Package primitives implements HelixQA's Stagehand-inspired hybrid
// primitives: act / extract / observe / agent.
//
// These four entry points give integrators deterministic AI spots
// inside otherwise scripted Playwright / mobile / desktop flows.
// Each primitive maps to exactly one narrow LLM capability:
//
//	Act     — natural language → single nexus.Action
//	Extract — schema + prompt   → typed struct value
//	Observe — descriptor        → nexus.ElementRef(s)
//	Agent   — scoped goal       → full agent state-machine run
//
// Ported as a pattern from <tools-dir>/stagehand/lib/v3/
// (act.ts, extract.ts, observe.ts, agent.ts). HelixQA re-implements
// in Go from first principles — no Stagehand source is linked into
// the build graph.
//
// Project-agnosticism: no file in this package references any
// consuming project. Callers register their own selectors /
// schemas / goals; the primitives hand the request to any
// LLMClient that satisfies the agent package's narrow contract.
package primitives
