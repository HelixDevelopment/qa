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

GATE='TestNoBluffVision|TestVisionFields|TestVisionEndpoint|TestDescribeVisionEndpoint|TestCaseHasUnrunAssertingStep_VisionGated|TestPipelineResult_|TestScoreVisionProvider_Endpoint|TestDesktopShellCase_|TestFrameDiff_|TestVisionFindings_'
PKGS=(./pkg/autonomous/ ./pkg/llm/ ./pkg/testbank/ ./pkg/orchestrator/)

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
A, L, T, O = "pkg/autonomous/", "pkg/llm/", "pkg/testbank/", "pkg/orchestrator/"
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
 ("M22-tokens-matched-in-model-prose", A+"vision_gate.go", A, [("matchVisionTokens(transcript, step.Recognize", "matchVisionTokens(transcript+desc, step.Recognize")]),
 ("M23-reply-without-transcription-judged", A+"vision_gate.go", A, [('if !ok {\n\t\t\ttried = append(tried, p.Name()+": blind reply has no TEXT:', 'if !ok && false {\n\t\t\ttried = append(tried, p.Name()+": blind reply has no TEXT:')]),
 ("M24-heading-word-token-loaded", T+"schema.go", T, [('if visionHeadingWords[k] {\n\t\t\treturn fmt.Sprintf("test case %s step %d: recognize token', 'if false {\n\t\t\treturn fmt.Sprintf("test case %s step %d: recognize token')]),
 ("M25-phash-cap-removed", T+"schema.go", T, [("d > MaxPHashMaxDistance {", "d > 64 {")]),
 ("M26-change-only-labelled-verified", A+"vision_gate.go", A, [("if !step.HasPositiveVisionCheck() {", "if false {")]),
 ("M27-loader-accepts-negative-only-check", T+"schema.go", T, [("if !st.HasPositiveVisionCheck() {", "if false {")]),
 ("M28-loader-accepts-vision-on-non-screen", T+"schema.go", T, [("kind.IsNonScreen() {\n\t\t\treturn fmt.Sprintf(", "kind.IsNonScreen() && false {\n\t\t\treturn fmt.Sprintf(")]),
 ("M29-gated-screenshot-promoted", O+"definition_challenge.go", O, [("if step.IsVisionGated() {\n\t\t\treturn true", "if step.IsVisionGated() && false {\n\t\t\treturn true")]),
 ("M30-endpoint-sends-cloud-model", L+"openai.go", L, [('if model == "" && cfg.Name != ProviderVisionEndpoint {', 'if model == "" {')]),
 ("M31-endpoint-sampling-not-fixed", L+"openai.go", L, [("if p.providerName == ProviderVisionEndpoint {\n\t\ttemp, seed", "if false {\n\t\ttemp, seed")]),
 ("M32-error-body-uncapped", L+"openai.go", L, [("if len(snippet) > openaiErrorBodySnippet {", "if len(snippet) > openaiErrorBodySnippet*100000 {")]),
 ("M33-oversized-body-accepted", L+"openai.go", L, [("if len(respBody) > openaiMaxResponseBytes {", "if len(respBody) > openaiMaxResponseBytes*4 {")]),
 ("M34-unparsable-url-printed", L+"vision_endpoint.go", L, [('shown := "(unparsable endpoint URL — not printed)"', "shown := cfg.BaseURL")]),
 ("M35-url-query-printed", L+"vision_endpoint.go", L, [("clean := url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}", "clean := url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path, RawQuery: u.RawQuery}")]),
 ("M36-endpoint-not-preferred", L+"vision_ranking.go", L, [("if p.Name() == ProviderVisionEndpoint {\n\t\treturn 10.0", "if false {\n\t\treturn 10.0")]),
 ("M37-ambiguous-reply-stops-the-search", A+"structured_executor.go", A, [('tried = append(tried, p.Name()+": "+actual)', 'tried = append(tried, p.Name()+": "+actual)\n\t\tbreak')]),
 ("M38-endpoint-asked-last", A+"vision_gate.go", A, [("return append(endpoint, rest...)", "return append(rest, endpoint...)")]),
 ("M39-definite-no-shopped-for-yes", A+"structured_executor.go", A, [("if err == nil {\n\t\t\treturn ok, actual, p.Name(), nil", "if err == nil && ok {\n\t\t\treturn ok, actual, p.Name(), nil")]),
 ("M40-blind-reply-without-text-stops-the-search", A+"vision_gate.go", A, [("cannot be told apart from the model's prose\")\n\t\t\tcontinue", "cannot be told apart from the model's prose\")\n\t\t\tbreak")]),
 ("M41-structured-outcome-not-on-session", A+"pipeline.go", A, [("result.Structured = structuredResult", "result.Structured = nil")]),
 ("M42-session-exit-ignores-structured", A+"pipeline.go", A, [("if c := r.Structured.ExitCode(); c != 0 {", "if c := r.Structured.ExitCode(); c != 0 && false {")]),
 ("M43-unloadable-banks-exit-0", A+"pipeline.go", A, [('if r.StructuredError != "" {\n\t\treturn 2', 'if false {\n\t\treturn 2')]),
 ("M44-blocked-case-files-no-finding", A+"structured_executor.go", A, [("\t\tresult.Findings = append(result.Findings, finding)\n\t\tif ste.onFinding != nil {\n\t\t\tste.onFinding(finding)", "\t\t_ = finding\n\t\tif false {\n\t\t\tste.onFinding(finding)")]),
 ("M45-desktop-shell-pass-ignores-unrun-steps", O+"definition_challenge.go", O, [("case allPassed && caseHasUnrunAssertingStep(d.testCase):", "case allPassed && caseHasUnrunAssertingStep(d.testCase) && false:")]),
 ("M46-skipped-gated-step-ignored", A+"structured_executor.go", A, [("\tif actionResult.Skipped {\n\t\tif step.IsVisionGated() {", "\tif actionResult.Skipped {\n\t\tif step.IsVisionGated() && false {")]),
 ("M47-loader-accepts-vision-on-prose", T+"schema.go", T, [("kind == ActionTypeDescription {\n\t\t\treturn fmt.Sprintf(\"test case %s step %d: vision keys on a prose", "kind == ActionTypeDescription && false {\n\t\t\treturn fmt.Sprintf(\"test case %s step %d: vision keys on a prose")]),
 ("M09-golden-distance-ignored", A+"vision_gate.go", A, [("if d > max {", "if d > max+64 {")]),
 ("M10-forbid-ignored", A+"vision_gate.go", A, [("if len(forbidden) > 0 {", "if len(forbidden) > 99 {")]),
 ("M11-recognize-ignored", A+"vision_gate.go", A, [("if len(missing) > 0 {", "if len(missing) > 99 {")]),
 ("M12-tokens-leak-into-prompt", A+"vision_gate.go", A, [("blindTranscription(ctx context.Context, frame []byte)", "blindTranscription(ctx context.Context, frame []byte, leak string)"), ("p.Vision(ctx, frame, blindDescribePrompt)", "p.Vision(ctx, frame, blindDescribePrompt+leak)"), ("ste.blindTranscription(ctx, frame)", 'ste.blindTranscription(ctx, frame, strings.Join(step.Recognize, " "))')]),
 ("M13-forbid-substring-match", A+"vision_gate.go", A, [('if k != "" && strings.Contains(hay, " "+k+" ")', 'if k != "" && strings.Contains(hay, k)')]),
 ("M14-screenshot-judges-shell", T+"schema.go", A, [("return true\n\t}\n\treturn false\n}\n\n// TestStep", "return false\n\t}\n\treturn false\n}\n\n// TestStep")]),
 ("M15-blocked-case-counted-passed", A+"structured_executor.go", A, [("case testBlocked && testPassed:", "case testBlocked && testPassed && false:")]),
 ("M16-exitcode-ignores-blocked", A+"vision_gate.go", A, [("case r.TestCasesBlocked > 0, r.TestCasesPassed == 0:", "case r.TestCasesPassed == 0:")]),
 ("M17-endpoint-not-vision-capable", L+"openai.go", L, [("ProviderVisionEndpoint: true,", "ProviderVisionEndpoint: false,")]),
 ("M18-empty-bearer-sent", L+"openai.go", L, [('if p.apiKey != "" {', "if true {")]),
 ("M19-endpoint-requires-key", L+"provider.go", L, [("case ProviderOllama, ProviderUITars, ProviderVisionEndpoint:", "case ProviderOllama, ProviderUITars:")]),
 ("M20-contradictory-tokens-loaded", T+"schema.go", T, [("if recognized[k] {", "if recognized[k] && false {")]),
 ("M21-phash-without-golden-loaded", T+"schema.go", T, [('if st.Golden == "" {\n\t\t\treturn fmt.Sprintf("test case %s step %d: phash_max_distance without golden', 'if false {\n\t\t\treturn fmt.Sprintf("test case %s step %d: phash_max_distance without golden')]),
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
