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
# COMPILE is not counted as caught.
#
# A control run (no overlay) must pass first, otherwise nothing a mutant
# does can be attributed to it.
#
# Exit codes: 0 every mutant caught · 1 at least one mutant SURVIVED
#             2 could not determine (control red, anchor text missing,
#               toolchain error) — never a pass.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "${ROOT_DIR}" || exit 2

command -v go >/dev/null 2>&1 || { echo "UNDETERMINED: go not on PATH"; exit 2; }
command -v python3 >/dev/null 2>&1 || { echo "UNDETERMINED: python3 not on PATH"; exit 2; }

WORK="$(mktemp -d "${TMPDIR:-/tmp}/nbv-mutation.XXXXXX")" || exit 2
trap 'rm -rf "${WORK}"' EXIT

GATE='TestNoBluffVision|TestVisionFields|TestVisionEndpoint|TestDescribeVisionEndpoint'
PKGS=(./pkg/autonomous/ ./pkg/llm/ ./pkg/testbank/)

echo "== control (unmutated tree)"
if ! go test -count=1 -run "${GATE}" "${PKGS[@]}" >"${WORK}/control.log" 2>&1; then
  echo "UNDETERMINED: control run is RED — fix the tree before judging mutants"
  tail -20 "${WORK}/control.log"
  exit 2
fi
echo "   control GREEN"

# id | file | python-literal old | python-literal new | package
MUTANTS=$(cat <<'EOF'
M01-gating-ignored|pkg/autonomous/vision_gate.go|"r.VisionGated = step.IsVisionGated()"|"r.VisionGated = false"|./pkg/autonomous/
M02-no-provider-passes|pkg/autonomous/vision_gate.go|"block(\"no vision-capable provider is configured for a vision-gated step\")"|"r.Passed = true"|./pkg/autonomous/
M03-verify-error-falls-back-to-pass|pkg/autonomous/vision_gate.go|"block(actual)\n\t\t\t\treturn"|"r.Passed = true\n\t\t\t\treturn"|./pkg/autonomous/
M04-nongated-label-claims-fallback|pkg/autonomous/vision_gate.go|"UNVERIFIED-by-vision: \" + reason"|"vision provider error - falling back to action success: \" + reason"|./pkg/autonomous/
M05-contradictory-reply-accepted|pkg/autonomous/structured_executor.go|"case saidYes && saidNo:"|"case saidYes && saidNo && false:"|./pkg/autonomous/
M06-blank-frame-accepted|pkg/autonomous/vision_gate.go|"if !frameUsable {\n\t\tblock("|"if false {\n\t\tblock("|./pkg/autonomous/
M07-frame-hash-unbound|pkg/autonomous/vision_gate.go|"r.FrameSHA256 = frameSHA256(frame)"|"r.FrameSHA256 = frameSHA256(before)"|./pkg/autonomous/
M08-unchanged-frame-accepted|pkg/autonomous/vision_gate.go|"framesDiffer(before, frame); !changed {"|"framesDiffer(before, frame); !changed && false {"|./pkg/autonomous/
M09-golden-distance-ignored|pkg/autonomous/vision_gate.go|"if d > max {"|"if d > max+64 {"|./pkg/autonomous/
M10-forbid-ignored|pkg/autonomous/vision_gate.go|"if len(forbidden) > 0 {"|"if len(forbidden) > 99 {"|./pkg/autonomous/
M11-recognize-ignored|pkg/autonomous/vision_gate.go|"if len(missing) > 0 {"|"if len(missing) > 99 {"|./pkg/autonomous/
M12-tokens-leak-into-prompt|pkg/autonomous/vision_gate.go|"ste.vision.Vision(ctx, frame, blindDescribePrompt)"|"ste.vision.Vision(ctx, frame, blindDescribePrompt+strings.Join(step.Recognize, \" \"))"|./pkg/autonomous/
M13-forbid-substring-match|pkg/autonomous/vision_gate.go|"if k != \"\" && strings.Contains(hay, \" \"+k+\" \")"|"if k != \"\" && strings.Contains(hay, k)"|./pkg/autonomous/
M14-screenshot-judges-shell|pkg/autonomous/vision_gate.go|"return true\n\t}\n\treturn false"|"return false\n\t}\n\treturn false"|./pkg/autonomous/
M15-blocked-case-counted-passed|pkg/autonomous/structured_executor.go|"case testBlocked && testPassed:"|"case testBlocked && testPassed && false:"|./pkg/autonomous/
M16-exitcode-ignores-blocked|pkg/autonomous/vision_gate.go|"case r.TestCasesBlocked > 0, r.TestCasesPassed == 0:"|"case r.TestCasesPassed == 0:"|./pkg/autonomous/
M17-endpoint-not-vision-capable|pkg/llm/openai.go|"ProviderVisionEndpoint: true,"|"ProviderVisionEndpoint: false,"|./pkg/llm/
M18-empty-bearer-sent|pkg/llm/openai.go|"if p.apiKey != \"\" {"|"if true {"|./pkg/llm/
M19-endpoint-requires-key|pkg/llm/provider.go|"case ProviderOllama, ProviderUITars, ProviderVisionEndpoint:"|"case ProviderOllama, ProviderUITars:"|./pkg/llm/
M20-contradictory-tokens-loaded|pkg/testbank/schema.go|"if recognized[k] {"|"if recognized[k] && false {"|./pkg/testbank/
M21-phash-without-golden-loaded|pkg/testbank/schema.go|"if st.Golden == \"\" {\n\t\t\treturn fmt.Sprintf(\"test case %s step %d: phash_max_distance without golden"|"if false {\n\t\t\treturn fmt.Sprintf(\"test case %s step %d: phash_max_distance without golden"|./pkg/testbank/
EOF
)

caught=0; survived=0; undet=0
while IFS='|' read -r id file old new pkg; do
  [[ -z "${id}" ]] && continue
  mutated="${WORK}/${id}.go"
  if ! python3 -I - "${ROOT_DIR}/${file}" "${mutated}" "${old}" "${new}" <<'PY'
import ast, sys
src_path, out_path, old_lit, new_lit = sys.argv[1:5]
old, new = ast.literal_eval(old_lit), ast.literal_eval(new_lit)
src = open(src_path, encoding="utf-8").read()
n = src.count(old)
if n != 1:
    sys.stderr.write(f"anchor occurs {n} times (need exactly 1)\n")
    sys.exit(3)
open(out_path, "w", encoding="utf-8").write(src.replace(old, new))
PY
  then
    echo "UNDET    ${id}: anchor text not found exactly once in ${file} (anchor rot)"
    undet=$((undet+1)); continue
  fi
  printf '{"Replace":{"%s":"%s"}}' "${ROOT_DIR}/${file}" "${mutated}" >"${WORK}/${id}.json"
  go test -count=1 -overlay "${WORK}/${id}.json" -run "${GATE}" "${pkg}" >"${WORK}/${id}.log" 2>&1
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
done <<<"${MUTANTS}"

echo "== no_bluff_vision mutations: ${caught} caught, ${survived} survived, ${undet} undetermined"
if (( survived > 0 )); then exit 1; fi
if (( undet > 0 || caught == 0 )); then exit 2; fi
exit 0
