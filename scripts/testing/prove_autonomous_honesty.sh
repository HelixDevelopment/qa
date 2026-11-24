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
for g in "./pkg/navigator TestBridge_" "./pkg/autonomous TestSessionPipeline_ZeroFrames|TestUndeterminedNoFrames|TestStructuredPhase_Missing|TestSessionPipeline_HostVideo" "./cmd/helixqa TestSelectEnvProviders|TestAutonomous_"; do
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
mutate 6 scripts/playwright-bridge-tls.js 's|rejectUnauthorized: true|rejectUnauthorized: false|' ./pkg/navigator 'TestBridge_PrivateCA_WrongCA_Refused'
mutate 7 scripts/playwright-bridge-tls.js 's|if (!isPrivateHost(host)) {|if (false) {|' ./pkg/navigator 'TestBridge_InsecureTLS_RefusedForPublicHost'
mutate 8 scripts/playwright-bridge.js 's|\.\.\.tlsExtra,||' ./pkg/navigator 'TestBridge_PrivateCA_WithCAFile_RendersPage'
echo "result: fail=$fail"; exit $fail
