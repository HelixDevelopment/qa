#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Milos Vasic
# SPDX-License-Identifier: Apache-2.0
#
# prove_autonomous_honesty.sh -- paired mutation proof (constitution 1.1) for the
# `helixqa autonomous` honesty fixes: web-bridge TLS trust, zero-analysed-frames
# verdict, opt-in providers / host video, missing-banks skip.
#
# For each mutation the production source is edited in place, the test that
# guards it is run and MUST FAIL, and the file is restored. A control run on the
# unmutated tree MUST pass first. Exit: 0 every mutation caught and control green,
# 1 a mutation survived or the control failed, 2 cannot determine (missing tool).
#
# Usage:  HELIXQA_PLAYWRIGHT_NODE_PATH=<node_modules with playwright> \
#           bash scripts/testing/prove_autonomous_honesty.sh
# The bridge TLS tests SKIP (never pass) without node, chromium and that path;
# a skipped guard makes this script exit 2.
set -u
cd "$(dirname "$0")/../.."
for t in go node; do command -v "$t" >/dev/null || { echo "cannot determine: $t missing" >&2; exit 2; }; done
[ -n "${HELIXQA_PLAYWRIGHT_NODE_PATH:-}" ] || { echo "cannot determine: set HELIXQA_PLAYWRIGHT_NODE_PATH" >&2; exit 2; }
BK="$(mktemp -d)"; trap 'for f in "$BK"/*.path; do [ -e "$f" ] && cp -f "${f%.path}" "$(cat "$f")"; done; rm -rf "$BK"' EXIT
n=0
save() { n=$((n+1)); cp "$1" "$BK/$n"; echo "$1" > "$BK/$n.path"; }
run() { # pkg regexp -> prints go test output, returns its status; SKIP counts as undetermined
  out="$(go test "$1" -run "$2" -count=1 -v 2>&1)"; rc=$?
  if printf '%s' "$out" | grep -q -- '--- SKIP'; then echo "SKIP in guard $2" >&2; return 2; fi
  return $rc
}
fail=0
echo "== control (unmutated) =="
for g in "./pkg/navigator TestBridge_" "./pkg/autonomous TestSessionPipeline_ZeroFrames|TestUndeterminedNoFrames|TestStructuredPhase_Missing|TestSessionPipeline_HostVideo|TestDataValidation_|TestPartialFrames" "./cmd/helixqa TestSelectEnvProviders|TestAutonomous_|TestResolveDataValidation" "./pkg/autonomous TestHTTPExecutor_AdminAuth|TestNavigationPrompts_CarryNoCredentials|TestBuildKBContext_Never|TestResolveCredentialPlaceholders|TestShippedFiles_CarryNoDefaultCredential|TestHTTPExecutor_BodyTestVars|TestExpandTestVars" "./pkg/analysis TestAnalyzeScreenshotChecked" "./pkg/llm TestIsLocalEndpointURL"; do
  set -- $g; run "$1" "$2"; rc=$?
  [ $rc -eq 0 ] && echo "control PASS: $2" || { echo "control FAIL($rc): $2"; [ $rc -eq 2 ] && exit 2; fail=1; }
done
[ $fail -eq 0 ] || exit 1
mutate() { # id file sed-expr pkg regexp
  local id="$1" f="$2" expr="$3" pkg="$4" re="$5"
  save "$f"; sed -i "$expr" "$f"
  if cmp -s "$f" "$BK/$n"; then echo "M$id: mutation did not change $f (stale proof)"; fail=1; return; fi
  run "$pkg" "$re"; rc=$?
  cp -f "$BK/$n" "$f"
  case $rc in 0) echo "M$id SURVIVED (guard did not fail): $re"; fail=1;; 2) echo "M$id undetermined"; fail=2;; *) echo "M$id caught";; esac
}
mutate 1 pkg/autonomous/pipeline.go 's|if r.Status != StatusComplete \|\| r.FramesAnalysed > 0 {|if true {|' ./pkg/autonomous 'TestSessionPipeline_ZeroFrames|TestUndeterminedNoFrames'
mutate 2 pkg/autonomous/pipeline.go 's|if !sp.config.RecordVideo {|if false {|' ./pkg/autonomous 'TestSessionPipeline_HostVideo'
mutate 3 cmd/helixqa/provider_policy.go 's|if allowCloud \|\| isLocalProvider(c) {|if true {|' ./cmd/helixqa 'TestSelectEnvProviders|TestAutonomous_DefaultIsLocalOnly'
mutate 4 cmd/helixqa/main.go 's|if !bridgedOK {|if false {|' ./cmd/helixqa 'TestAutonomous_DefaultIsLocalOnly'
mutate 5 pkg/autonomous/pipeline.go 's|if sp.config.BanksExplicit {|if false {|' ./pkg/autonomous 'TestStructuredPhase_MissingBankDir'
mutate 6 scripts/playwright-bridge-tls.js 's|rejectUnauthorized: !unverified|rejectUnauthorized: false|' ./pkg/navigator 'TestBridge_PrivateCA_WrongCA_Refused'
mutate 7 scripts/playwright-bridge-tls.js 's|if (!isPrivateHost(host)) {|if (false) {|' ./pkg/navigator 'TestBridge_InsecureTLS_RefusedForPublicHost'
mutate 8 scripts/playwright-bridge.js 's|\.\.\.tlsExtra,||' ./pkg/navigator 'TestBridge_PrivateCA_WithCAFile_RendersPage'
mutate 9 pkg/analysis/vision.go 's|return \[\]AnalysisFinding{}, false|return []AnalysisFinding{}, true|' ./pkg/analysis 'TestAnalyzeScreenshotChecked'
# Follow-ups after PR #9: default-credential probing, vision-endpoint locality,
# prose "[]" parsing, partial sessions, browser-wide insecure TLS.
mutate 10 pkg/autonomous/pipeline.go 's|if !sp.config.DataValidation {|if false {|' ./pkg/autonomous 'TestDataValidation_DefaultOff_SendsNoRequest|TestDataValidation_CredentialsAloneDoNotEnableIt'
mutate 11 pkg/autonomous/pipeline.go 's|if dvUser == "" \|\| dvPass == "" {|if false {|' ./pkg/autonomous 'TestDataValidation_OptInWithoutCredentials_SendsNoRequest'
mutate 12 cmd/helixqa/main.go 's|if !cloudOK \&\& !llm.IsLocalEndpointURL(vcfg.BaseURL) {|if false {|' ./cmd/helixqa 'TestAutonomous_VisionEndpointHosted_RefusedWithoutOptIn'
mutate 13 pkg/llm/vision_endpoint.go 's|if err != nil \|\| u.User != nil {|if err != nil {|' ./pkg/llm 'TestIsLocalEndpointURL'
mutate 14 pkg/llm/vision_endpoint.go 's|return ip.IsLoopback() .*|return true|' ./pkg/llm 'TestIsLocalEndpointURL'
mutate 15 pkg/analysis/vision.go 's|if strings.TrimSpace(content\[:start\]) != "" \|\| strings.TrimSpace(content\[end+1:\]) != "" {|if false {|' ./pkg/analysis 'TestAnalyzeScreenshotChecked'
mutate 16 pkg/analysis/vision.go 's|if strings.TrimSpace(f.Title) == "" \&\& strings.TrimSpace(f.Description) == "" {|if false {|' ./pkg/analysis 'TestAnalyzeScreenshotChecked'
mutate 17 pkg/autonomous/pipeline.go 's|if partialFrames(r) != "" {|if false {|' ./pkg/autonomous 'TestPartialFrames'
mutate 18 pkg/autonomous/pipeline.go 's|if reason := partialFrames(result); reason != "" {|if false {|' ./pkg/autonomous 'TestPartialFrames'
mutate 19 scripts/playwright-bridge-tls.js "s|args.push('--ignore-certificate-errors-spki-list=' + spki.join(','));|args.push('--ignore-certificate-errors');|" ./pkg/navigator 'TestBridge_InsecureTLS_DoesNotExtendToOtherOrigins'
mutate 20 scripts/playwright-bridge.js 's|if (!username \|\| !password) {|if (false) {|' ./pkg/navigator 'TestBridge_Launch_SendsNoDefaultCredentialLogin'
# Explicit-credentials fixes: no built-in admin pair, no credential in a prompt,
# placeholders resolved only with the opt-in, bank credentials by variable name.
mutate 21 pkg/autonomous/http_executor.go 's|if creds.Username == "" \|\| creds.Password == "" {|if false {|' ./pkg/autonomous 'TestHTTPExecutor_AdminAuth_NoBuiltInCredentials'
mutate 22 pkg/autonomous/pipeline.go 's|type the placeholder {{QA_PASSWORD}}|type "hunter2"|' ./pkg/autonomous 'TestNavigationPrompts_CarryNoCredentials'
mutate 23 pkg/autonomous/pipeline.go 's|if sp.config == nil \|\| !sp.config.DataValidation {|if false {|' ./pkg/autonomous 'TestResolveCredentialPlaceholders'
mutate 24 pkg/autonomous/pipeline.go 's|names = append(names, k)|names = append(names, k+"="+kb.Credentials[k])|' ./pkg/autonomous 'TestBuildKBContext_NeverEmbedsCredentialValues'
mutate 25 pkg/autonomous/test_vars.go 's|if v == "" {|if false {|' ./pkg/autonomous 'TestHTTPExecutor_BodyTestVarsUnset_BlocksAndSendsNothing|TestExpandTestVars'
mutate 26 pkg/autonomous/test_vars.go 's|HELIX_TEST_\[A-Z0-9_\]+|[A-Z0-9_]+|' ./pkg/autonomous 'TestExpandTestVars'
mutate 27 pkg/autonomous/http_executor.go '0,/if len(missing) > 0 {/s//if false {/' ./pkg/autonomous 'TestHTTPExecutor_BodyTestVarsUnset_BlocksAndSendsNothing'
echo "result: fail=$fail"; exit $fail
