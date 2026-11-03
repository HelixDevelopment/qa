// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// Package agent implements HelixQA's agent-step state machine,
// ported as a pattern from browser-use's Agent.step() loop (see
// <tools-dir>/browser-use/browser_use/agent/service.py).
//
// (<tools-dir> = a tool checkout resolved by opensource.ResolveTool:
// $HELIXQA_TOOLS_DIR, <root>/submodules/<snake_name>, or legacy
// tools/opensource/.)
// The loop is deliberately four explicit phases instead of one
// monolithic Step() so every transition is observable and testable:
//
//	Phase 1 — PrepareContext   : capture Snapshot + Screenshot via Adapter
//	Phase 2 — PlanActions      : single structured-output LLM call
//	Phase 3 — Execute          : dispatch each Action via the Adapter
//	Phase 4 — PostProcess      : update History, emit telemetry
//
// Concrete adapters (pkg/nexus/browser, pkg/nexus/mobile, etc.)
// slot in at construction time. Any LLMClient whose Chat method
// returns JSON the planner can parse works; the package stays
// agnostic of provider choice so the LLMsVerifier scoring stack
// can pick the best backend at runtime.
//
// Project-agnosticism: no file in this package references any
// consuming project's names, endpoints, or conventions. The
// package exports only types + one constructor + four phase
// entry points, plus a Run() convenience that composes them.
package agent
