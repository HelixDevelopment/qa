#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Milos Vasic
# SPDX-License-Identifier: Apache-2.0
#
# no_bluff_vision mutation challenge (§1.1 paired mutation for the
# no_bluff_vision gate, §11.4.201 / §11.4.262).
#
# For every fix that keeps HelixQA's vision "eyes" honest, this script
# re-introduces the defect (or weakens the check) in a COPY of the source
# file and runs the gate tests against it through `go test -overlay` — the
# working tree is never modified. Each mutation must make at least one
# named test FAIL by assertion ("--- FAIL:"); a mutant that only fails to
# COMPILE is reported UNDETERMINED, never counted as caught.
#
# A control run (no overlay) must pass first, otherwise nothing a mutant
# does can be attributed to it.
#
# usage: no_bluff_vision_mutation_challenge.sh [--only ID[,ID...]]
#   Temporary files go under $TMPDIR (set it to a DISK directory; /tmp may
#   be RAM-backed tmpfs). Run it inside a resource-bounded scope.
#
# Exit codes: 0 every mutant caught · 1 at least one mutant SURVIVED
#             2 could not determine (control red, anchor text missing,
#               mutant did not compile, toolchain error) — never a pass.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "${ROOT_DIR}" || exit 2

ONLY=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --only) ONLY="${2:-}"; shift 2 ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

command -v go >/dev/null 2>&1 || { echo "UNDETERMINED: go not on PATH"; exit 2; }
command -v python3 >/dev/null 2>&1 || { echo "UNDETERMINED: python3 not on PATH"; exit 2; }

WORK="$(mktemp -d "${TMPDIR:-/tmp}/nbv-mutation.XXXXXX")" || exit 2
trap 'rm -rf "${WORK}"' EXIT

GATE='TestNoBluffVision|TestVisionFields|TestVisionEndpoint|TestDescribeVisionEndpoint|TestCaseHasUnrunAssertingStep_VisionGated|TestPipelineResult_|TestScoreVisionProvider_Endpoint|TestDesktopShellCase_|TestFrameDiff_|TestVisionFindings_|TestOrchRun_|TestBankRunnerExit_|TestOtherProviders_|TestProviders_|TestAdaptiveVisionVia_|TestGoogle_|TestRunExitCode|TestGuardPanic|TestCLIExit|TestAutonomousExitCode|TestShellStep_|TestShellExec_|TestDependencyOrder_|TestDefaultStatusHTTPSteps_'
PKGS=(./pkg/autonomous/ ./pkg/llm/ ./pkg/testbank/ ./pkg/orchestrator/ ./cmd/helixqa/ ./pkg/shellexec/)

echo "== control (unmutated tree)"
if ! go test -count=1 -run "${GATE}" "${PKGS[@]}" >"${WORK}/control.log" 2>&1; then
  echo "UNDETERMINED: control run is RED — fix the tree before judging mutants"
  tail -20 "${WORK}/control.log"
  exit 2
fi
echo "   control GREEN"

# The mutant table lives in Python so anchors may contain any character.
# Each mutant: (id, file, package, [(old, new), ...]); every `old` must occur
# exactly once in the file. Output: one TAB-separated manifest line per
# mutant: id, package, overlay-json-path or "ANCHOR:<reason>".
python3 -I - "${ROOT_DIR}" "${WORK}" "${ONLY}" >"${WORK}/manifest.tsv" <<'PY'
import json, os, sys
root, work, only = sys.argv[1], sys.argv[2], sys.argv[3]
A, L, T, O, C, S = "pkg/autonomous/", "pkg/llm/", "pkg/testbank/", "pkg/orchestrator/", "cmd/helixqa/", "pkg/shellexec/"
M = [
 ("M01-gating-ignored", A+"vision_gate.go", A, [("r.VisionGated = step.IsVisionGated()", "r.VisionGated = false")]),
 ("M02-no-provider-passes", A+"vision_gate.go", A, [('block("no vision-capable provider is configured for a vision-gated step")', "r.Passed = true")]),
 ("M03-verify-error-falls-back-to-pass", A+"vision_gate.go", A, [("block(actual)\n\t\t\t\treturn", "r.Passed = true\n\t\t\t\treturn")]),
 ("M04-nongated-label-claims-fallback", A+"vision_gate.go", A, [('UNVERIFIED-by-vision: " + reason', 'vision provider error - falling back to action success: " + reason')]),
 ("M05-echoed-template-is-an-answer", A+"structured_executor.go", A, [("regexp.MustCompile(`(?im)^[ \\t]*VERIFIED:[ \\t]*(yes|no)[ \\t]*\\.?[ \\t]*\\r?$`)", "regexp.MustCompile(`(?i)verified:\\s*(yes|no)\\b`)")]),
 ("M05b-several-answer-lines-accepted", A+"structured_executor.go", A, [("case len(answers) > 1:", "case false:"), ("if len(answers) == 1 {", "if len(answers) >= 1 {")]),
 ("M06-blank-frame-accepted", A+"vision_gate.go", A, [("if !frameUsable {\n\t\tblock(", "if false {\n\t\tblock(")]),
 ("M07-frame-hash-unbound", A+"vision_gate.go", A, [("r.FrameSHA256 = frameSHA256(frame)", "r.FrameSHA256 = frameSHA256(before)")]),
 ("M08-unchanged-frame-accepted", A+"vision_gate.go", A, [("\t\tif !changed {\n\t\t\trefute(", "\t\tif !changed && false {\n\t\t\trefute(")]),
 ("M08b-incomparable-frames-judged", A+"vision_gate.go", A, [('if cmpErr != nil {\n\t\t\tblock("expect_change: " + cmpErr.Error())', 'if false {\n\t\t\tblock("expect_change: " + cmpErr.Error())')]),
 ("M22-tokens-matched-in-model-prose", A+"vision_gate.go", A, [("matchTokensInTexts(texts, step.Recognize, step.Forbid, step.ExactRecognize())", "matchTokensInTexts(append(texts, desc), step.Recognize, step.Forbid, step.ExactRecognize())")]),
 ("M23-reply-without-transcription-judged", A+"vision_gate.go", A, [("if perr != nil {\n\t\t\ttried = append(tried, p.Name()+\": \"+perr.Error())", "if perr != nil && false {\n\t\t\ttried = append(tried, p.Name()+\": \"+perr.Error())")]),
 ("M25-phash-cap-removed", T+"schema.go", T, [("d > MaxPHashMaxDistance {", "d > 64 {")]),
 ("M26-change-only-labelled-verified", A+"vision_gate.go", A, [("if !step.HasPositiveVisionCheck() {", "if false {")]),
 ("M27-loader-accepts-negative-only-check", T+"schema.go", T, [("if !st.HasPositiveVisionCheck() {", "if false {")]),
 ("M28-loader-accepts-vision-on-non-screen", T+"schema.go", T, [("kind.IsNonScreen() {\n\t\t\treturn fmt.Sprintf(", "kind.IsNonScreen() && false {\n\t\t\treturn fmt.Sprintf(")]),
 ("M29-gated-screenshot-promoted", O+"definition_challenge.go", O, [("gated := step.IsVisionGated()", "gated := false")]),
 ("M30-endpoint-sends-cloud-model", L+"openai.go", L, [('if model == "" && cfg.Name != ProviderVisionEndpoint {', 'if model == "" {')]),
 ("M31-endpoint-sampling-not-fixed", L+"openai.go", L, [("if p.providerName == ProviderVisionEndpoint {\n\t\ttemp, seed", "if false {\n\t\ttemp, seed")]),
 ("M32-error-body-uncapped", L+"http_body.go", L, [("if len(text) > openaiErrorBodySnippet {", "if len(text) > openaiErrorBodySnippet*100000 {")]),
 ("M33-oversized-body-accepted", L+"openai.go", L, [('if tooBig {\n\t\treturn nil, fmt.Errorf("openai: response body exceeds', 'if tooBig && false {\n\t\treturn nil, fmt.Errorf("openai: response body exceeds')]),
 ("M34-unparsable-url-printed", L+"vision_endpoint.go", L, [('shown := "(unparsable endpoint URL — not printed)"', "shown := cfg.BaseURL")]),
 ("M35-url-query-printed", L+"vision_endpoint.go", L, [("clean := url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}", "clean := url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path, RawQuery: u.RawQuery}")]),
 ("M36-endpoint-not-preferred", L+"vision_ranking.go", L, [("if p.Name() == ProviderVisionEndpoint {\n\t\treturn 10.0", "if false {\n\t\treturn 10.0")]),
 ("M37-ambiguous-reply-stops-the-search", A+"structured_executor.go", A, [('tried = append(tried, p.Name()+": "+actual)', 'tried = append(tried, p.Name()+": "+actual)\n\t\tbreak')]),
 ("M38-endpoint-asked-last", A+"vision_gate.go", A, [("return append(endpoint, rest...)", "return append(rest, endpoint...)")]),
 ("M39-definite-no-shopped-for-yes", A+"structured_executor.go", A, [("if err == nil {\n\t\t\treturn ok, actual, p.Name(), nil", "if err == nil && ok {\n\t\t\treturn ok, actual, p.Name(), nil")]),
 ("M40-blind-reply-without-text-stops-the-search", A+"vision_gate.go", A, [("tried = append(tried, p.Name()+\": \"+perr.Error())\n\t\t\tcontinue", "tried = append(tried, p.Name()+\": \"+perr.Error())\n\t\t\tbreak")]),
 ("M41-structured-outcome-not-on-session", A+"pipeline.go", A, [("result.Structured = structuredResult", "result.Structured = nil")]),
 ("M42-session-exit-ignores-structured", A+"pipeline.go", A, [("if c := r.Structured.ExitCode(); c != 0 {", "if c := r.Structured.ExitCode(); c != 0 && false {")]),
 ("M43-unloadable-banks-exit-0", A+"pipeline.go", A, [('if r.StructuredError != "" {\n\t\treturn 2', 'if false {\n\t\treturn 2')]),
 ("M44-blocked-case-files-no-finding", A+"structured_executor.go", A, [("\t\tresult.Findings = append(result.Findings, finding)\n\t\tif ste.onFinding != nil {\n\t\t\tste.onFinding(finding)", "\t\t_ = finding\n\t\tif false {\n\t\t\tste.onFinding(finding)")]),
 ("M45-desktop-shell-pass-ignores-unrun-steps", O+"definition_challenge.go", O, [("case allPassed && caseHasUnrunAssertingStep(d.testCase, d.platform):", "case allPassed && caseHasUnrunAssertingStep(d.testCase, d.platform) && false:")]),
 ("M46-skipped-gated-step-ignored", A+"structured_executor.go", A, [("\tif actionResult.Skipped {\n\t\tif step.IsVisionGated() {", "\tif actionResult.Skipped {\n\t\tif step.IsVisionGated() && false {")]),
 ("M47-loader-accepts-vision-on-prose", T+"schema.go", T, [("kind == ActionTypeDescription {\n\t\t\treturn fmt.Sprintf(\"test case %s step %d: vision keys on a prose", "kind == ActionTypeDescription && false {\n\t\t\treturn fmt.Sprintf(\"test case %s step %d: vision keys on a prose")]),
 ("M51-filler-kept-as-screen-text", A+"vision_transcript.go", A, [("if k == \"\" || testbank.IsVisionFiller(k) {\n\t\t\tcontinue\n\t\t}\n\t\tkeys = append", "if k == \"\" {\n\t\t\tcontinue\n\t\t}\n\t\tkeys = append")]),
 ("M52-loader-accepts-filler-token", T+"schema.go", T, [('if IsVisionFiller(k) {\n\t\t\treturn fmt.Sprintf("test case %s step %d: recognize token', 'if false {\n\t\t\treturn fmt.Sprintf("test case %s step %d: recognize token')]),
 ("M53-endpoint-userinfo-accepted", L+"vision_endpoint.go", L, [('case u.User != nil:\n\t\treturn refuse("it carries user info")', 'case u.User != nil && false:\n\t\treturn refuse("it carries user info")')]),
 ("M54-endpoint-query-accepted", L+"vision_endpoint.go", L, [("case u.RawQuery != \"\" || u.ForceQuery:", "case false:")]),
 ("M55-transport-error-not-scrubbed", L+"openai.go", L, [("fmt.Errorf(\"openai: send request: %w\", scrubURLError(err))", "fmt.Errorf(\"openai: send request: %w\", err)")]),
 ("M56-path-appended-after-query", L+"openai.go", L, [("endpoint := chatCompletionsURL(p.baseURL)", "endpoint := p.baseURL + \"/chat/completions\"")]),
 ("M57-response-read-uncapped", L+"http_body.go", L, [("io.ReadAll(io.LimitReader(r, openaiMaxResponseBytes+1))", "io.ReadAll(r)")]),
 ("M58-blocked-filed-as-functional-bug", A+"structured_executor.go", A, [("Category: analysis.CategoryEnvironment,", "Category: analysis.CategoryFunctional,")]),
 ("M59-environment-counts-as-app-defect", "pkg/analysis/types.go", A, [("return c != CategoryEnvironment", "return true")]),
 ("M60-reproducer-gets-environment-findings", A+"pipeline.go", A, [("if !f.Category.IsAppDefect() {\n\t\t\t// The QA run could not judge", "if false {\n\t\t\t// The QA run could not judge")]),
 ("M61-environment-finding-ticketed", A+"findings_bridge.go", A, [("if !af.Category.IsAppDefect() {\n\t\t\tcontinue", "if false {\n\t\t\tcontinue")]),
 ("M62-issue-count-includes-environment", A+"pipeline.go", A, [("if f.Category.IsAppDefect() {\n\t\t\tout = append(out, f)", "if true {\n\t\t\tout = append(out, f)")]),
 ("M63-members-asked-bypassing-wrapper", A+"vision_gate.go", A, [("if router != nil && p != ste.vision {", "if false && router != nil && p != ste.vision {")]),
 ("M64-routed-vision-cost-not-recorded", L+"vision_via.go", A, [('a.recordCost(p.Name(), resp, "vision", true)', "_ = resp")]),
 ("M65-routed-auth-failure-not-marked", L+"vision_via.go", A, [("if isAuthOrCreditError(err) {\n\t\ta.markUnavailable(p.Name(), err.Error())", "if false {\n\t\ta.markUnavailable(p.Name(), err.Error())")]),
 ("M66-verify-loop-ignores-cancelled-ctx", A+"structured_executor.go", A, [('if ctx.Err() != nil {\n\t\t\ttried = append(tried, "context cancelled before asking "+p.Name())', 'if false {\n\t\t\ttried = append(tried, "context cancelled before asking "+p.Name())')]),
 ("M67-blind-loop-ignores-cancelled-ctx", A+"vision_gate.go", A, [('if ctx.Err() != nil {\n\t\t\ttried = append(tried, "context cancelled before asking "+p.Name())', 'if false {\n\t\t\ttried = append(tried, "context cancelled before asking "+p.Name())')]),
 ("M68-enhanced-routed-cost-not-recorded", L+"vision_via.go", L, [('eap.recordCost(name, resp, "vision", true)', "_ = resp")]),
 ("M69-frame-diff-noncomparable-is-motion", A+"structured_executor.go", A, [("return ActionResult{Blocked: true, Message: fmt.Sprintf(\"frame_diff: %v", "return ActionResult{Success: true, Message: fmt.Sprintf(\"frame_diff: %v")]),
 ("J01-trailing-content-accepted", A+"vision_transcript.go", A, [("if _, terr := dec.Token(); terr != io.EOF {", "if _, terr := dec.Token(); terr != io.EOF && false {")]),
 ("J02-unknown-key-accepted", A+"vision_transcript.go", A, [('if key != "texts" && key != "description" {', "if false {")]),
 ("J03-duplicate-key-accepted", A+"vision_transcript.go", A, [("if seen[key] {", "if false {")]),
 ("J04-non-string-element-accepted", A+"vision_transcript.go", A, [("if len(e) == 0 || e[0] != '\"' || json.Unmarshal(e, &v) != nil {", "if json.Unmarshal(e, &v) != nil && false {")]),
 ("J05-elements-joined", A+"vision_transcript.go", A, [('keys = append(keys, " "+k+" ")', 'if len(keys) == 0 {\n\t\t\tkeys = append(keys, " ")\n\t\t}\n\t\tkeys[0] += k + " "')]),
 ("J06-endpoint-gets-no-response-format", L+"openai.go", A, [("if schema := responseSchemaFrom(ctx); schema != nil {", "if schema := responseSchemaFrom(ctx); schema != nil && false {")]),
 ("J07-schema-not-attached", A+"vision_gate.go", A, [("sctx := llm.WithResponseSchema(ctx, blindTranscriptSchema)", "sctx := ctx")]),
 ("J08-missing-key-accepted", A+"vision_transcript.go", A, [('if !seen["texts"] || !seen["description"] {', "if false {")]),
 ("J09-description-type-unchecked", A+"vision_transcript.go", A, [("if len(raw) == 0 || raw[0] != '\"' || json.Unmarshal(raw, &description) != nil {", "if json.Unmarshal(raw, &description) != nil && false {")]),
 ("J10-response-format-sent-to-cloud", L+"openai.go", L, [("\tif p.providerName == ProviderVisionEndpoint {\n\t\tif schema := responseSchemaFrom(ctx); schema != nil {", "\tif true {\n\t\tif schema := responseSchemaFrom(ctx); schema != nil {")]),
 ("N12-desktop-playwright-framediff-observational", O+"definition_challenge.go", O, [("testbank.ActionTypeShell:\n\t\t\t\tcontinue", "testbank.ActionTypeShell, testbank.ActionTypePlaywright, testbank.ActionTypeFrameDiff:\n\t\t\t\tcontinue")]),
 ("N32-desktop-skip-sentinel-dropped", O+"definition_challenge.go", O, [('definitionWrapperSkipSentinel+" shell step(s) passed, but these', '"note: shell step(s) passed, but these')]),
 ("K01-run-exit-skipped-is-pass", O+"orchestrator.go", O, [("if q.TotalChallenges == 0 || q.PassedChallenges < q.TotalChallenges {", "if q.TotalChallenges == 0 {")]),
 ("K02-run-exit-nothing-executed-is-pass", O+"orchestrator.go", O, [("if q.TotalChallenges == 0 || q.PassedChallenges < q.TotalChallenges {", "if q.PassedChallenges < q.TotalChallenges {")]),
 ("K03-step-platform-ignored", O+"definition_challenge.go", O, [("if step.Platform != \"\" && step.Platform != platform {", "if false {")]),
 ("K04-step-skip-ignored", O+"definition_challenge.go", O, [("\t\tif step.Skip {\n\t\t\tcontinue\n\t\t}\n\t\tif step.Platform != \"\" && step.Platform != platform {", "\t\tif false {\n\t\t\tcontinue\n\t\t}\n\t\tif step.Platform != \"\" && step.Platform != platform {")]),
 ("K05-skip-reason-unnamed", O+"definition_challenge.go", O, [('"the bank runner: "+strings.Join(unrunAssertingSteps(d.testCase, d.platform), "; ")+', '"the bank runner"+')]),
 ("N13-skip-branch-only-model-gated", A+"structured_executor.go", A, [("\tif actionResult.Skipped {\n\t\tif step.IsVisionGated() {", "\tif actionResult.Skipped {\n\t\tif step.NeedsVisionModel() {")]),
 ("K06-frame-diff-capture-problem-is-fail", A+"structured_executor.go", A, [("return ActionResult{Blocked: true, Message: fmt.Sprintf(\"frame_diff: %v", "return ActionResult{Success: false, Message: fmt.Sprintf(\"frame_diff: %v")]),
 ("K07-blocked-action-ignored", A+"structured_executor.go", A, [("\tif actionResult.Blocked {\n\t\tresult.Blocked = true", "\tif actionResult.Blocked && false {\n\t\tresult.Blocked = true")]),
 ("K08-executor-failure-is-app-fail", A+"structured_executor.go", A, [("\t\t// defect — BLOCKED (filed as an environment finding).\n\t\tresult.Blocked = true", "\t\t// defect — BLOCKED (filed as an environment finding).\n\t\tresult.Blocked = false")]),
 ("N19-circuit-breaker-ignored", L+"vision_via.go", L, [("if rl.isCircuitOpen() {", "if false && rl.isCircuitOpen() {")]),
 ("N20-failure-not-recorded", L+"vision_via.go", L, [("\trl.RecordFailure(err)\n", "")]),
 ("N34-enhanced-ctx-check-dropped", L+"vision_via.go", L, [("func (eap *EnhancedAdaptiveProvider) VisionVia(ctx context.Context, p Provider, image []byte, prompt string) (*Response, error) {\n\tif err := ctx.Err(); err != nil {\n\t\treturn nil, err\n\t}", "func (eap *EnhancedAdaptiveProvider) VisionVia(ctx context.Context, p Provider, image []byte, prompt string) (*Response, error) {")]),
 ("S01-encoded-path-secret-accepted", L+"vision_endpoint.go", L, [('return refuse("its path carries an encoded', 'return cfg, true, nil\n\t\t_ = refuse\n\t\treturn refuse("its path carries an encoded')]),
 ("S02-own-key-echo-not-redacted", L+"openai.go", L, [("errorBodyText(respBody, p.apiKey)", "string(respBody)")]),
 ("S03-chat-completions-double-appended", L+"openai.go", L, [('if strings.HasSuffix(path, "/chat/completions") {\n\t\t// The base', 'if false {\n\t\t// The base')]),
 ("S04-anthropic-transport-unscrubbed", L+"anthropic.go", L, [('fmt.Errorf("anthropic: send request: %w", scrubURLError(err))', 'fmt.Errorf("anthropic: send request: %w", err)')]),
 ("S05-ollama-transport-unscrubbed", L+"ollama.go", L, [('fmt.Errorf("ollama: send request: %w", scrubURLError(err))', 'fmt.Errorf("ollama: send request: %w", err)')]),
 ("S06-astica-transport-unscrubbed", L+"astica.go", L, [('fmt.Errorf("astica: send request: %w", scrubURLError(err))', 'fmt.Errorf("astica: send request: %w", err)')]),
 ("T01-frame-diff-first-capture-fails-app", A+"structured_executor.go", A, [('return ActionResult{Blocked: true, Message: fmt.Sprintf("frame_diff: first screenshot failed', 'return ActionResult{Success: false, Message: fmt.Sprintf("frame_diff: first screenshot failed')]),
 ("T02-frame-diff-second-capture-fails-app", A+"structured_executor.go", A, [('return ActionResult{Blocked: true, Message: fmt.Sprintf("frame_diff: second screenshot failed', 'return ActionResult{Success: false, Message: fmt.Sprintf("frame_diff: second screenshot failed')]),
 ("T03-playback-check-wrong-executor-fails-app", A+"structured_executor.go", A, [('return ActionResult{Blocked: true, Message: "playback_check requires', 'return ActionResult{Success: false, Message: "playback_check requires')]),
 ("T04-adb-shell-wrong-executor-fails-app", A+"structured_executor.go", A, [('return ActionResult{Blocked: true, Message: "adb_shell requires', 'return ActionResult{Success: false, Message: "adb_shell requires')]),
 ("T05-host-only-steps-blocked-without-executor", A+"structured_executor.go", A, [("if err != nil && isHostOnlyStep(step) {", "if err != nil && false && isHostOnlyStep(step) {")]),
 ("T06-gated-host-step-runs-without-frame", A+"structured_executor.go", A, [("\tif step.IsVisionGated() {\n\t\treturn false\n\t}\n\tkind, _ := step.ParseAction()\n\treturn kind.IsNonScreen()", "\tif false {\n\t\treturn false\n\t}\n\tkind, _ := step.ParseAction()\n\treturn kind.IsNonScreen()")]),
 ("T07-ui-step-treated-host-only", A+"structured_executor.go", A, [("return kind.IsNonScreen() || kind == testbank.ActionTypeSleep", "return true || kind.IsNonScreen()")]),
 ("R10-adaptive-visionvia-ctx-dropped", L+"vision_via.go", L, [("func (a *AdaptiveProvider) VisionVia(ctx context.Context, p Provider, image []byte, prompt string) (*Response, error) {\n\tif err := ctx.Err(); err != nil {\n\t\treturn nil, err\n\t}", "func (a *AdaptiveProvider) VisionVia(ctx context.Context, p Provider, image []byte, prompt string) (*Response, error) {")]),
 ("R20-google-transport-unscrubbed", L+"google.go", L, [('fmt.Errorf("google: send request: %w", scrubURLError(err))', 'fmt.Errorf("google: send request: %w", err)')]),
 ("R27-empty-fragment-accepted", L+"vision_endpoint.go", L, [('case u.Fragment != "" || strings.Contains(cfg.BaseURL, "#"):', 'case u.Fragment != "":')]),
 ("R28-empty-query-accepted", L+"vision_endpoint.go", L, [('case u.RawQuery != "" || u.ForceQuery:', 'case u.RawQuery != "":')]),
 ("U01-redaction-case-sensitive", L+"http_body.go", L, [("regexp.MustCompile(`(?i)`+regexp.QuoteMeta(f))", "regexp.MustCompile(regexp.QuoteMeta(f))")]),
 ("U02-truncate-before-redact", L+"http_body.go", L, [("\ttext := string(body)\n\tfor _, s := range secrets {", "\ttext := string(body)\n\tif len(text) > openaiErrorBodySnippet {\n\t\ttext = text[:openaiErrorBodySnippet]\n\t}\n\tfor _, s := range secrets {")]),
 ("U03-base64-forms-not-redacted", L+"http_body.go", L, [("base64.StdEncoding.EncodeToString(raw),", "strings.Repeat(base64.StdEncoding.EncodeToString(raw), 0),"), ("base64.URLEncoding.EncodeToString(raw),", "strings.Repeat(base64.URLEncoding.EncodeToString(raw), 0),"), ("base64.RawStdEncoding.EncodeToString(raw),", "strings.Repeat(base64.RawStdEncoding.EncodeToString(raw), 0),"), ("base64.RawURLEncoding.EncodeToString(raw),", "strings.Repeat(base64.RawURLEncoding.EncodeToString(raw), 0),")]),
 ("U04-escaped-forms-not-redacted", L+"http_body.go", L, [("url.QueryEscape(secret),", "strings.Repeat(url.QueryEscape(secret), 0),"), ("url.PathEscape(secret),", "strings.Repeat(url.PathEscape(secret), 0),")]),
 ("U05a-anthropic-bypasses-redaction", L+"anthropic.go", L, [("errorBodyText(respBody, p.apiKey)", "string(respBody)")]),
 ("U05b-astica-bypasses-redaction", L+"astica.go", L, [("errorBodyText(respBody, p.apiKey)", "string(respBody)")]),
 ("U05c-google-bypasses-redaction", L+"google.go", L, [("errorBodyText(respBody, p.apiKey)", "string(respBody)")]),
 ("U06a-anthropic-over-cap-accepted", L+"anthropic.go", L, [('if tooBig {\n\t\treturn nil, fmt.Errorf("anthropic: response body exceeds', 'if tooBig && false {\n\t\treturn nil, fmt.Errorf("anthropic: response body exceeds')]),
 ("U06b-ollama-over-cap-accepted", L+"ollama.go", L, [('if tooBig {\n\t\treturn nil, fmt.Errorf("ollama: response body exceeds', 'if tooBig && false {\n\t\treturn nil, fmt.Errorf("ollama: response body exceeds')]),
 ("U07-short-secret-floor-lifted", L+"http_body.go", L, [("const minRedactableSecret = 8", "const minRedactableSecret = 1000")]),
 ("R06-left-word-boundary-dropped", A+"vision_transcript.go", A, [('if strings.Contains(k, " "+tok+" ") {', 'if strings.Contains(k, tok+" ") {')]),
 ("R13-prose-after-closing-fence", A+"vision_transcript.go", A, [('rest := s[nl+1:]\n', 'rest := s[nl+1:]\n\tif i := strings.LastIndex(rest, "```"); i >= 0 {\n\t\trest = rest[:i+3]\n\t}\n')]),
 ("R13b-any-fence-language", A+"vision_transcript.go", A, [('if opener != "```" && !strings.EqualFold(opener, "```json") {', 'if !strings.HasPrefix(opener, "```") {')]),
 ("R14-texts-cap-removed", A+"vision_transcript.go", A, [("if len(elems) > maxBlindTexts {", "if len(elems) > maxBlindTexts*1000 {")]),
 ("V01-element-length-cap-removed", A+"vision_transcript.go", A, [("if utf8.RuneCountInString(v) > maxBlindTextLen {", "if utf8.RuneCountInString(v) > maxBlindTextLen*100000 {")]),
 ("V02-control-chars-in-element-accepted", A+"vision_transcript.go", A, [("if strings.IndexFunc(v, unicode.IsControl) >= 0 {", "if strings.IndexFunc(v, unicode.IsControl) >= 0 && false {")]),
 ("V03-invalid-utf8-accepted", A+"vision_transcript.go", A, [("if !utf8.ValidString(reply) {", "if !utf8.ValidString(reply) && false {")]),
 ("V04-exact-mode-ignored", A+"vision_transcript.go", A, [("found = equals(k)", "found = equals(k) || has(k)")]),
 ("V05-nfkc-dropped", T+"schema.go", A, [("s = norm.NFKC.String(s)", "s = norm.NFKC.String(\"\") + s")]),
 ("V06-zero-width-kept", T+"schema.go", A, [("if unicode.Is(unicode.Cf, r) || isInvisibleNonFormat(r) {", "if isInvisibleNonFormat(r) {")]),
 ("V07-recognize-exact-alone-loaded", T+"schema.go", T, [("if st.RecognizeExact && len(st.Recognize) == 0 {", "if false && st.RecognizeExact {")]),
 ("W01-run-load-error-exits-1", C+"exit_codes.go", C, [("if err != nil {\n\t\treturn exitCodeNotExecuted", "if err != nil {\n\t\treturn 1")]),
 ("W02-panic-swallowed", C+"exit_codes.go", C, [("if r := recover(); r != nil {", "if r := recover(); r != nil && false {")]),
 ("W03-panic-exits-blocked-code", C+"exit_codes.go", C, [("exitCodePanic = 70", "exitCodePanic = 2")]),
 ("M09-golden-distance-ignored", A+"vision_gate.go", A, [("if d > max {", "if d > max+64 {")]),
 ("M10-forbid-ignored", A+"vision_gate.go", A, [("if len(forbidden) > 0 {", "if len(forbidden) > 99 {")]),
 ("M11-recognize-ignored", A+"vision_gate.go", A, [("if len(missing) > 0 {", "if len(missing) > 99 {")]),
 ("M12-tokens-leak-into-prompt", A+"vision_gate.go", A, [("blindTranscription(ctx context.Context, frame []byte, forbid []string)", "blindTranscription(ctx context.Context, frame []byte, forbid []string, leak string)"), ("p.Vision(sctx, frame, blindDescribePrompt)", "p.Vision(sctx, frame, blindDescribePrompt+leak)"), ("ste.blindTranscription(ctx, frame, step.Forbid)", 'ste.blindTranscription(ctx, frame, step.Forbid, strings.Join(step.Recognize, " "))')]),
 ("M13-forbid-substring-match", A+"vision_transcript.go", A, [('if strings.Contains(k, " "+tok+" ") {', "if strings.Contains(k, tok) {")]),
 ("M14-screenshot-judges-shell", T+"schema.go", A, [("return true\n\t}\n\treturn false\n}\n\n// TestStep", "return false\n\t}\n\treturn false\n}\n\n// TestStep")]),
 ("M15-blocked-case-counted-passed", A+"structured_executor.go", A, [("case testBlocked && testPassed:", "case testBlocked && testPassed && false:")]),
 ("M16-exitcode-ignores-blocked", A+"vision_gate.go", A, [("case r.TestCasesBlocked > 0, r.TestCasesPassed == 0:", "case r.TestCasesPassed == 0:")]),
 ("M17-endpoint-not-vision-capable", L+"openai.go", L, [("ProviderVisionEndpoint: true,", "ProviderVisionEndpoint: false,")]),
 ("M18-empty-bearer-sent", L+"openai.go", L, [('if p.apiKey != "" {\n\t\thttpReq.Header.Set(', "if true {\n\t\thttpReq.Header.Set(")]),
 ("M19-endpoint-requires-key", L+"provider.go", L, [("case ProviderOllama, ProviderUITars, ProviderVisionEndpoint:", "case ProviderOllama, ProviderUITars:")]),
 ("M20-contradictory-tokens-loaded", T+"schema.go", T, [("if recognized[k] {", "if recognized[k] && false {")]),
 ("M21-phash-without-golden-loaded", T+"schema.go", T, [('if st.Golden == "" {\n\t\t\treturn fmt.Sprintf("test case %s step %d: phash_max_distance without golden', 'if false {\n\t\t\treturn fmt.Sprintf("test case %s step %d: phash_max_distance without golden')]),
 # ── review round 5 (fix round 6) ──
 ("X04-vision-verified-not-asserting", A+"structured_executor.go", A, [("if r.VisionStatus == VisionVerified {\n\t\treturn true", "if false {\n\t\treturn true")]),
 ("X05-verified-stray-lines-ignored", A+"structured_executor.go", A, [('case len(answers) == 1 && strayReplyLine(response) != "":', "case false:")]),
 ("X06-second-actual-line-allowed", A+"structured_executor.go", A, [("if actuals++; actuals == 1 {", "if actuals++; actuals >= 1 {")]),
 ("X07-empty-transcription-usable", A+"vision_transcript.go", A, [("if screenText == 0 {", "if false {")]),
 ("X08-refusal-element-usable", A+"vision_transcript.go", A, [("if modelStatementRe.MatchString(k) {", "if false {")]),
 ("X09-transcript-usability-unchecked", A+"vision_gate.go", A, [("\t\t\tperr = transcriptUsable(t)", "\t\t\t_ = transcriptUsable")]),
 ("X10-line-separator-accepted", A+"vision_transcript.go", A, [("if strings.IndexFunc(v, isLineOrParagraphSeparator) >= 0 {", "if strings.IndexFunc(v, isLineOrParagraphSeparator) >= 0 && false {")]),
 ("X11-recognize-contains-by-default", T+"schema.go", A, [("return !st.RecognizeContains", "return st.RecognizeExact")]),
 ("X12-contains-without-recognize-loaded", T+"schema.go", T, [("if st.RecognizeContains && len(st.Recognize) == 0 {", "if false {")]),
 ("X13-contains-and-exact-loaded", T+"schema.go", T, [("if st.RecognizeContains && st.RecognizeExact {", "if false {")]),
 ("X14-default-ignorables-kept", T+"schema.go", T, [("if unicode.Is(unicode.Cf, r) || isInvisibleNonFormat(r) {", "if unicode.Is(unicode.Cf, r) {")]),
 ("X15-variation-selectors-kept", T+"schema.go", T, [(" || unicode.Is(unicode.Variation_Selector, r)", "")]),
 ("X16-http-without-base-url-fails", A+"structured_executor.go", A, [('return ActionResult{Blocked: true, Message: "http: no base URL configured', 'return ActionResult{Success: false, Message: "http: no base URL configured')]),
 ("X21-failed-screenshot-capture-passes", A+"structured_executor.go", A, [("kind == testbank.ActionTypeScreenshot && !step.IsVisionGated() && executor != nil && len(afterSS) == 0 {", "kind == testbank.ActionTypeScreenshot && !step.IsVisionGated() && executor != nil && len(afterSS) == 0 && false {")]),
 ("X22-blank-frames-judged", A+"structured_executor.go", A, [("if isBlackFrame(first) || isBlackFrame(second) {", "if false {")]),
 ("X23-one-blank-frame-judged", A+"structured_executor.go", A, [("if isBlackFrame(first) || isBlackFrame(second) {", "if isBlackFrame(first) && isBlackFrame(second) {")]),
 ("X24-reachability-probe-ignored", A+"structured_executor.go", A, [("if perr := ste.probeReachable(ctx, platform); perr != nil {", "if perr := ste.probeReachable(ctx, platform); perr != nil && false {")]),
 ("X25-probe-not-cached", A+"structured_executor.go", A, [("if done && (pr.err == nil || time.Since(pr.at) < failedProbeTTL) {", "if false && done {")]),
 ("X26-adb-state-not-checked", A+"executor_factory.go", A, [('if err != nil || state != "device" {', "if err != nil {")]),
 ("X27-capture-probe-error-ignored", A+"executor_factory.go", A, [('if err != nil {\n\t\t\treturn fmt.Errorf("%s capture probe failed: %w", platform, err)', 'if false {\n\t\t\treturn fmt.Errorf("%s capture probe failed: %w", platform, err)')]),
 ("X28-empty-capture-probe-accepted", A+"executor_factory.go", A, [('if len(shot) == 0 {\n\t\t\treturn fmt.Errorf("%s capture probe returned no image data"', 'if len(shot) == 0 && false {\n\t\t\treturn fmt.Errorf("%s capture probe returned no image data"')]),
 ("X29-real-factory-never-probes", A+"real_executor.go", A, [("return probePlatformReachable(ctx, platform, f.config.AndroidDevice, runner, f.Create)", "_ = runner\n\treturn nil")]),
 ("X30-preflight-failure-is-failed", A+"pipeline.go", A, [("// judged — BLOCKED (exit 2), never a FAILED verdict.\n\t\tresult.Status = StatusBlocked", "// judged — BLOCKED (exit 2), never a FAILED verdict.\n\t\tresult.Status = StatusFailed")]),
 ("X31-plan-error-is-failed", A+"pipeline.go", A, [("// Provider outage / timeout while planning: no test ran.\n\t\tresult.Status = StatusBlocked", "// Provider outage / timeout while planning: no test ran.\n\t\tresult.Status = StatusFailed")]),
 ("X32-timeout-is-failed", A+"pipeline.go", A, [("// never judged — BLOCKED, not FAILED.\n\t\t\tresult.Status = StatusBlocked", "// never judged — BLOCKED, not FAILED.\n\t\t\tresult.Status = StatusFailed")]),
 ("X33-blocked-session-exits-0", A+"pipeline.go", A, [("if r.Status == StatusBlocked {\n\t\t// The session stopped", "if false {\n\t\t// The session stopped")]),
 ("X34-blocked-outranks-failed-case", A+"pipeline.go", A, [("\tif r.Status == StatusFailed {\n\t\treturn 1\n\t}\n\tif r.Structured", "\tif r.Status == StatusFailed {\n\t\treturn 1\n\t}\n\tif r.Status == StatusBlocked {\n\t\treturn 2\n\t}\n\tif r.Structured")]),
 ("X35-astica-status-unredacted", L+"astica.go", L, [("errorBodyText([]byte(apiResp.Status), p.apiKey),", "apiResp.Status,")]),
 ("X36-embedded-base64-not-redacted", L+"http_body.go", L, [("forms = append(forms, embeddedBase64Cores(raw)...)", "_ = embeddedBase64Cores")]),
 ("X37-embedded-base64-one-alignment", L+"http_body.go", L, [("skip := (3 - lead) % 3", "skip := 0 * lead")]),
 ("X38-percent-all-not-redacted", L+"http_body.go", L, [("percentEncodeAll(raw),", "strings.Repeat(percentEncodeAll(raw), 0),")]),
 ("X39-hex-not-redacted", L+"http_body.go", L, [("hex.EncodeToString(raw),", "strings.Repeat(hex.EncodeToString(raw), 0),")]),
 ("X40-json-escapes-not-redacted", L+"http_body.go", L, [("jsonUnicodeEscapes(secret),", "strings.Repeat(jsonUnicodeEscapes(secret), 0),")]),
 ("X41-orch-post-exec-error-drops-verdicts", O+"orchestrator.go", O, [('return result, fmt.Errorf("write report: %w", err)', 'return nil, fmt.Errorf("write report: %w", err)')]),
 ("X42-error-outranks-failed-verdict", C+"exit_codes.go", C, [("if result != nil && result.Report != nil && result.ExitCode() == 1 {", "if false {")]),
 ("X43-main-run-error-exits-1", C+"main.go", C, [("os.Exit(runExitCode(result, err))", "os.Exit(1)")]),
 ("X44-main-run-error-drops-result", C+"main.go", C, [("os.Exit(runExitCode(result, err))", "os.Exit(runExitCode(nil, err))")]),
 ("X46-filler-counted-as-screen-text", A+"vision_transcript.go", A, [("if k == \"\" || testbank.IsVisionFiller(k) {\n\t\t\tcontinue\n\t\t}\n\t\tscreenText++", "if k == \"\" {\n\t\t\tcontinue\n\t\t}\n\t\tscreenText++")]),
 # ── review round 6 (fix round 7) ──
 ("Y01-ctx-before-step-unchecked", A+"structured_executor.go", A, [("if cerr := ctx.Err(); cerr != nil {\n\t\t\t// The session ended (timeout / cancel) before this step", "if cerr := ctx.Err(); cerr != nil && false {\n\t\t\t// The session ended (timeout / cancel) before this step")]),
 ("Y02-ctx-during-action-is-failure", A+"structured_executor.go", A, [("if cerr := ctx.Err(); cerr != nil {\n\t\t\t// The session ended DURING the action", "if cerr := ctx.Err(); cerr != nil && false {\n\t\t\t// The session ended DURING the action")]),
 ("Y03-no-reprobe-after-action-failure", A+"structured_executor.go", A, [("if perr := ste.reprobe(ctx, platform); perr != nil {", "if perr := ste.reprobe(ctx, platform); perr != nil && false {")]),
 ("Y04-reprobe-answered-from-cache", A+"structured_executor.go", A, [("if perr := ste.reprobe(ctx, platform); perr != nil {", "if perr := ste.probeReachable(ctx, platform); perr != nil {")]),
 ("Y05-failed-probe-cached-forever", A+"structured_executor.go", A, [("if done && (pr.err == nil || time.Since(pr.at) < failedProbeTTL) {", "if done && (true || time.Since(pr.at) < failedProbeTTL) {")]),
 ("Y06-probe-cache-shared-across-platforms", A+"structured_executor.go", A, [("pr, done := ste.reach[platform]", 'pr, done := ste.reach[""]'), ("ste.reach[platform] = probeResult{", 'ste.reach[""] = probeResult{')]),
 ("Y08-any-step-observes", A+"structured_executor.go", A, [('return verb == "assertvisible" || verb == "waitfor"\n\t}\n\treturn false', 'return verb == "assertvisible" || verb == "waitfor"\n\t}\n\treturn true')]),
 ("Y09-skipped-step-case-passes", A+"structured_executor.go", A, [("case skippedSteps > 0:", "case false:")]),
 ("Y10-no-observing-step-passes", A+"structured_executor.go", A, [("case observingSteps == 0:", "case false:")]),
 ("Y11-sleep-observes", A+"structured_executor.go", A, [("case testbank.ActionTypeShell, testbank.ActionTypeHTTP, testbank.ActionTypeAssert,", "case testbank.ActionTypeSleep, testbank.ActionTypeShell, testbank.ActionTypeHTTP, testbank.ActionTypeAssert,")]),
 ("Y12-tap-observes", A+"structured_executor.go", A, [("case testbank.ActionTypeShell, testbank.ActionTypeHTTP, testbank.ActionTypeAssert,", "case testbank.ActionTypeTap, testbank.ActionTypeShell, testbank.ActionTypeHTTP, testbank.ActionTypeAssert,")]),
 ("Y13-assert-reuses-other-case-response", A+"structured_executor.go", A, [("if ste.http == nil || ste.caseHTTPSteps == 0 {", "if ste.http == nil {")]),
 ("Y14-assert-without-http-fails", A+"structured_executor.go", A, [('return ActionResult{Blocked: true, Message: "assert: no http: step ran earlier', 'return ActionResult{Success: false, Message: "assert: no http: step ran earlier')]),
 ("Y15-case-http-counter-not-reset", A+"structured_executor.go", A, [("\tste.caseHTTPSteps = 0\n", "")]),
 ("Y16-black-threshold-raised", A+"structured_executor.go", A, [("if r>>8 > 16 || g>>8 > 16 || bl>>8 > 16 {", "if r>>8 > 200 || g>>8 > 200 || bl>>8 > 200 {")]),
 ("Y17-presence-not-checked-first", A+"vision_gate.go", A, [("if present := forbiddenPresent(t, forbid); len(present) > 0 {", "if false {")]),
 ("Y18-forbid-absence-without-anchor", A+"vision_gate.go", A, [("if len(step.Forbid) > 0 && len(step.Recognize) == 0 {", "if false {")]),
 ("Y19-contained-not-equal-refuted", A+"vision_gate.go", A, [("if in := containedOnly(texts, missing); len(in) > 0 && step.ExactRecognize() {", "if in := containedOnly(texts, missing); len(in) > 0 && false {")]),
 ("Y20-all-statement-rule-dropped", A+"vision_transcript.go", A, [("if statements == screenText {", "if false {")]),
 ("Y21-any-statement-blocks", A+"vision_transcript.go", A, [("if statements == screenText {", "if statements > 0 {")]),
 ("Y22-image-remark-branch-dropped", A+"vision_transcript.go", A, [("`(the|this) (image|screenshot|picture) (is|shows|appears|contains|does))( |$)`", "`(zzzz))( |$)`")]),
 ("Y23-actual-line-not-scanned", A+"structured_executor.go", A, [('case saidYes && suspectActual(actual) != "":', "case false:")]),
 ("Y24-actual-verdict-not-scanned", A+"structured_executor.go", A, [("case verdictInTextRe.MatchString(text):", "case false:")]),
 ("Y25-combining-marks-split-words", T+"schema.go", T, [("if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) {", "if unicode.IsLetter(r) || unicode.IsDigit(r) {")]),
 ("Y26-optional-vowel-marks-kept", T+"schema.go", T, [("if isOptionalVowelMark(r) || r == '\\u0640' {", "if r == '\\u0640' {")]),
 ("Y27-loader-forbid-without-recognize", T+"schema.go", T, [("if len(st.Forbid) > 0 && len(st.Recognize) == 0 {", "if false {")]),
 ("Y28-orch-cancel-drops-partial-platform", O+"orchestrator.go", O, [("if pr == nil {\n\t\t\t\tpr = &reporter.PlatformResult{", "if true {\n\t\t\t\tpr = &reporter.PlatformResult{")]),
 ("Y30-interrupted-challenge-counted-failed", O+"orchestrator.go", O, [("if ctx.Err() != nil && (hasInterruptedMarker(cr) || interruptedOutcome(cr, err)) {", "if false && (hasInterruptedMarker(cr) || interruptedOutcome(cr, err)) {")]),
 ("Y31-interrupted-shell-is-failure", O+"definition_challenge.go", O, [("if ctx.Err() != nil && allPassed {", "if false && allPassed {")]),
 ("Y32-autonomous-report-error-ignored", C+"exit_codes.go", C, [("if reportErr != nil && code == 0 {", "if false {")]),
 ("Y33-crash-not-outranking-error", C+"exit_codes.go", C, [("if result != nil && result.Report != nil && result.ExitCode() == 1 {", "if result != nil && result.Report != nil && result.Report.FailedChallenges > 0 {")]),
 ("Y34-learn-error-is-failed", A+"pipeline.go", A, [("// The project could not be read: environment, not an app verdict.\n\t\tresult.Status = StatusBlocked", "// The project could not be read: environment, not an app verdict.\n\t\tresult.Status = StatusFailed")]),
 # ── review round 7 (fix round 8) ──
 ("Z01-shell-not-own-group", S+"shellexec.go", S, [("\townProcessGroup(c)\n", "\t_ = ownProcessGroup\n")]),
 ("Z02-cancel-kills-only-child", S+"group_unix.go", S, [("return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)", "return c.Process.Kill()")]),
 ("Z03-waitdelay-removed", S+"shellexec.go", S, [("\tc.WaitDelay = WaitDelay\n", "\t_ = WaitDelay\n")]),
 ("Z04-interrupted-result-fails-antibluff", O+"definition_challenge.go", O, [('Expected: string(d.def.ID), Actual: string(d.def.ID), Passed: true,\n\t\t\t\t\tMessage: "bank-loaded definition bridged to Challenge interface; interrupted', 'Expected: string(d.def.ID), Actual: string(d.def.ID), Passed: false,\n\t\t\t\t\tMessage: "bank-loaded definition bridged to Challenge interface; interrupted')]),
 ("Z05-interrupted-marker-ignored", O+"orchestrator.go", O, [("if ctx.Err() != nil && (hasInterruptedMarker(cr) || interruptedOutcome(cr, err)) {", "if ctx.Err() != nil && interruptedOutcome(cr, err) {")]),
 ("Z06-failure-before-deadline-lost", O+"orchestrator.go", O, [("if lr := w.lastResult(); lr != nil && lr.Status == challenge.StatusFailed {", "if lr := w.lastResult(); lr != nil && false {")]),
 ("Z07-not-started-unlisted", O+"orchestrator.go", O, [("\t\tif cancelled {\n\t\t\tseen :=", "\t\tif false {\n\t\t\tseen :=")]),
 ("Z08-cycle-unreported", O+"orchestrator.go", O, [("if len(cyclic) == 0 && !cancelled {\n\t\treturn prs", "if !cancelled {\n\t\treturn prs")]),
 ("Z09-last-challenge-cancel-unsurfaced", O+"orchestrator.go", O, [("if runErr == nil && ctx.Err() != nil {", "if false {")]),
 ("Z10-dependencies-ignored", T+"schema.go", T, [("\t\t\tindeg[id]++\n", "")]),
 ("Z11-orch-order-ignores-dependencies", O+"orchestrator.go", O, [("definitions, cyclic := orderDefinitions(o.bank.All())", "definitions, cyclic := o.bank.All(), []*challenge.Definition(nil)")]),
 ("Z12-structured-order-ignores-dependencies", A+"structured_executor.go", A, [("order, cyclic := testbank.DependencyOrder(keys, deps)", "order, cyclic := keys, []string(nil)")]),
 ("Z13-structured-cycle-not-blocked", A+"structured_executor.go", A, [("for _, k := range cyclic {\n\t\tste.blockCaseUnrun(", "for _, k := range cyclic[:0] {\n\t\tste.blockCaseUnrun(")]),
 ("Z14-actual-scanned-on-no", A+"structured_executor.go", A, [('case saidYes && suspectActual(actual) != "":', 'case len(answers) == 1 && suspectActual(actual) != "":')]),
 ("Z15-actual-scan-uses-refusal-pattern", A+"structured_executor.go", A, [("case saysCannotSeeImage(text):", "case modelStatementRe.MatchString(testbank.VisionTokenKey(text)) || saysCannotSeeImage(text):")]),
 ("Z16-verdict-scan-any-case", A+"structured_executor.go", A, [("regexp.MustCompile(`\\bVERIFIED\\s*:\\s*(?i:yes|no)\\b`)", "regexp.MustCompile(`(?i)\\bverified\\s*:\\s*(yes|no)\\b`)")]),
 ("Z17-empty-expected-asked", A+"vision_gate.go", A, [("case strings.TrimSpace(step.Expected) == \"\":\n\t\t// No expected state", "case false:\n\t\t// No expected state")]),
 ("Z18-gated-blank-expected-asked", A+"vision_gate.go", A, [("if strings.TrimSpace(step.Expected) == \"\" {\n\t\t\t\t// A \"yes\" to an empty question", "if false {\n\t\t\t\t// A \"yes\" to an empty question")]),
 ("Z19-http-any-status-passes", A+"http_executor.go", A, [("if step.ExpectStatus == 0 && (resp.StatusCode < 200 || resp.StatusCode > 299) {", "if false {")]),
 ("Z20-refusal-forbidden-substring", A+"vision_transcript.go", A, [("if !allModelStatements(texts) {\n\t\t_, f :=", "if true {\n\t\t_, f :=")]),
 ("Z21-no-space-scripts-word-only", A+"vision_transcript.go", A, [("if sub && strings.Contains(k, tok) {", "if sub && false && strings.Contains(k, tok) {")]),
 ("Z22-tatweel-kept", T+"schema.go", T, [("if isOptionalVowelMark(r) || r == '\\u0640' {", "if isOptionalVowelMark(r) {")]),
 ("Z23-sleep-ignores-cancel", A+"structured_executor.go", A, [('case <-ctx.Done():\n\t\t\treturn ActionResult{Success: false, Message: fmt.Sprintf("sleep', 'case <-make(chan struct{}):\n\t\t\treturn ActionResult{Success: false, Message: fmt.Sprintf("sleep')]),
 ("Z24-skip-steps-run", A+"structured_executor.go", A, [("\t\tif step.Skip {\n\t\t\t// `_skip: true`", "\t\tif false {\n\t\t\t// `_skip: true`")]),
 ("Z25-adb-state-any-line", A+"executor_factory.go", A, [("if l := strings.TrimSpace(lines[i]); adbStates[l] {", 'if l := strings.TrimSpace(lines[i]); l != "" {')]),
 ("Z26-playwright-any-verb-observes", A+"structured_executor.go", A, [('return verb == "assertvisible" || verb == "waitfor"', 'return verb != ""')]),
 ("Z27-host-step-failure-reprobed", A+"structured_executor.go", A, [("} else if executor != nil && !isHostOnlyStep(step) {", "} else if executor != nil {")]),
 # ── review round 8 (fix round 9) ──
 ("Q01-interruption-marker-substring", O+"definition_challenge.go", O, [('if strings.HasPrefix(a, definitionWrapperSkipSentinel+" "+interruptedMarker) {', "if strings.Contains(a, interruptedMarker) {")]),
 ("Q03-skip-sentinel-substring", O+"definition_challenge.go", O, [("if strings.HasPrefix(action, definitionWrapperSkipSentinel) {\n\t\t\tr.Status = challenge.StatusSkipped", "if strings.Contains(action, definitionWrapperSkipSentinel) {\n\t\t\tr.Status = challenge.StatusSkipped")]),
 ("Q04-default-2xx-list-mixes-skip", T+"schema.go", O, [("if st.Skip {\n\t\t\t\tskipped = append(skipped, row)", "if false {\n\t\t\t\tskipped = append(skipped, row)")]),
 ("Q05-inability-without-subject", A+"structured_executor.go", A, [("`(?:^|(?:^| )(?:as an ai )?(?:i|i m|i am|we|we re|we are) )`", "`(?:^| )`")]),
 ("Q06-app-image-not-excluded", A+"structured_executor.go", A, [('if !strings.HasPrefix(k[m[1]:], "of ") {', 'if !strings.HasPrefix(k[m[1]:], "of ") || true {')]),
 ("Q07-attached-image-unknown", A+"structured_executor.go", A, [("`(?:(?:attached|provided|uploaded|given|shared) )?(?:images?|screenshots?|pictures?|attachments?)(?: |$)`", "`(?:images?|screenshots?|pictures?|attachments?)(?: |$)`")]),
 ("Q08-no-with-inability-refuted", A+"structured_executor.go", A, [("case saidNo && saysCannotSeeImage(actualText(actual)):", "case false:")]),
 ("X45-main-without-guard-panic", C+"main.go", C, [("if code := guardPanic(realMainFn); code != 0 {", "if code := func() int { realMainFn(); return 0 }(); code != 0 {")]),
]
os.makedirs(work, exist_ok=True)
want = set(x for x in only.split(",") if x)
for mid, f, pkg, reps in M:
    if want and mid not in want:
        continue
    src_path = os.path.join(root, f)
    try:
        src = open(src_path, encoding="utf-8").read()
    except OSError as e:
        print(f"{mid}\t./{pkg}\tANCHOR:cannot read {f}: {e}"); continue
    bad = None
    for old, new in reps:
        n = src.count(old)
        if n != 1:
            bad = f"anchor occurs {n} times in {f} (need exactly 1): {old[:60]!r}"; break
        src = src.replace(old, new)
    if bad:
        print(f"{mid}\t./{pkg}\tANCHOR:{bad}"); continue
    mpath = os.path.join(work, mid + ".go")
    open(mpath, "w", encoding="utf-8").write(src)
    ov = os.path.join(work, mid + ".json")
    json.dump({"Replace": {src_path: mpath}}, open(ov, "w"))
    print(f"{mid}\t./{pkg}\t{ov}")
PY
[[ $? -eq 0 ]] || { echo "UNDETERMINED: mutant generator failed"; exit 2; }

caught=0; survived=0; undet=0
while IFS=$'\t' read -r id pkg ov; do
  [[ -z "${id}" ]] && continue
  if [[ "${ov}" == ANCHOR:* ]]; then
    echo "UNDET    ${id}: ${ov#ANCHOR:} (anchor rot)"
    undet=$((undet+1)); continue
  fi
  go test -count=1 -overlay "${ov}" -run "${GATE}" "${pkg}" >"${WORK}/${id}.log" 2>&1
  rc=$?
  if [[ ${rc} -ne 0 ]] && grep -q -- '--- FAIL:' "${WORK}/${id}.log"; then
    first="$(grep -m1 -- '--- FAIL:' "${WORK}/${id}.log" | sed 's/ (.*//')"
    echo "CAUGHT   ${id}: ${first#--- FAIL: }"
    caught=$((caught+1))
  elif [[ ${rc} -ne 0 ]]; then
    echo "UNDET    ${id}: mutant did not run to an assertion (build/toolchain error)"
    tail -5 "${WORK}/${id}.log" | sed 's/^/         /'
    undet=$((undet+1))
  else
    echo "SURVIVED ${id}: gate stayed GREEN with the defect re-introduced"
    survived=$((survived+1))
  fi
done <"${WORK}/manifest.tsv"

echo "== no_bluff_vision mutations: ${caught} caught, ${survived} survived, ${undet} undetermined"
if (( survived > 0 )); then exit 1; fi
if (( undet > 0 || caught == 0 )); then exit 2; fi
exit 0
