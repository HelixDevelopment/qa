// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// Package llmctl integrates HelixQA with llmctl
// (https://github.com/vasic-digital/llmctl), the local LLM
// orchestrator. It provides:
//
//   - Detect: locate the llmctl binary (LLMCTL_BIN, then PATH, then a
//     vendored submodule checkout) and report how to set it up when it
//     is absent.
//   - ReadPlan / VisionState: read-only `llmctl plan --json` /
//     `llmctl status` introspection that distinguishes ABSENT,
//     GATED (host tier gate), NOT_RUNNING, UNHEALTHY and READY.
//   - VisionClient: a deterministic (temperature 0, fixed seed, pinned
//     model id) OpenAI-compatible image-in/text-out client for the
//     llmctl `vision` profile (port 8082).
//   - DecideClient: a client for the llmctl decision gateway
//     (HTTPS, POST /v1/systemone) with typed noul/choice/score
//     questions.
//   - Confirm / RunDeterministic: the anti-bluff policy (Constitution
//     11.4.269): a model answer is only ever a PROPOSAL, a
//     deterministic oracle (OCR / pixel / DOM) must confirm it, and a
//     repeated run must yield an identical canonical hash.
//
// This package never starts, stops, enables or downloads a model: the
// only llmctl subcommands it executes are the read-only "plan" and
// "status" (see readOnlySubcommands). Starting a model is an explicit
// operator decision made outside this package.
//
// The package depends on the standard library only.
package llmctl
