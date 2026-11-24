#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Milos Vasic
# SPDX-License-Identifier: Apache-2.0
#
# prove_web_drive.sh -- paired mutation proof (constitution 1.1) for the two
# defects found driving `helixqa autonomous` against the real job_seeker console:
#   (a) Android KEYCODE_* names were passed to Playwright, which threw
#       "Unknown key", so select/enter/tab never reached a web page;
#   (b) five tests over five byte-identical frames printed "Complete ... 100%".
# Each mutation edits production source in place, runs the guard, which MUST
# FAIL, and restores the file. A control on the unmutated tree MUST pass first.
# Exit: 0 all caught and control green, 1 a mutation survived / control failed,
# 2 cannot determine (missing node / chromium / HELIXQA_PLAYWRIGHT_NODE_PATH, or
# a SKIPped guard -- a skipped guard is never a pass).
set -u
cd "$(dirname "$0")/../.."
for t in go node; do command -v "$t" >/dev/null || { echo "cannot determine: $t missing" >&2; exit 2; }; done
[ -n "${HELIXQA_PLAYWRIGHT_NODE_PATH:-}" ] || { echo "cannot determine: set HELIXQA_PLAYWRIGHT_NODE_PATH" >&2; exit 2; }
BK="$(mktemp -d)"; trap 'for f in "$BK"/*.path; do [ -e "$f" ] && cp -f "${f%.path}" "$(cat "$f")"; done; rm -rf "$BK"' EXIT
n=0
save() { n=$((n+1)); cp "$1" "$BK/$n"; echo "$1" > "$BK/$n.path"; }
run() { out="$(go test "$1" -run "$2" -count=1 -v 2>&1)"; rc=$?
  if printf '%s' "$out" | grep -q -- '--- SKIP'; then echo "SKIP in guard $2" >&2; return 2; fi; return $rc; }
fail=0
echo "== control (unmutated) =="
for g in "./pkg/navigator TestBridge_AndroidKeycodes|TestBridge_UnknownKey|TestPlaywrightKeyName" "./pkg/autonomous TestDistinctFrameCount|TestUndeterminedNoInteraction|TestSessionSummaryLine_NoInteraction|TestApplyFrameVerdicts"; do
  set -- $g; run "$1" "$2"; rc=$?
  [ $rc -eq 0 ] && echo "control PASS: $2" || { echo "control FAIL($rc): $2"; [ $rc -eq 2 ] && exit 2; fail=1; }
done
[ $fail -eq 0 ] || exit 1
mutate() { local id="$1" f="$2" expr="$3" pkg="$4" re="$5"
  save "$f"; sed -i "$expr" "$f"
  if cmp -s "$f" "$BK/$n"; then echo "M$id: mutation did not change $f (stale proof)"; fail=1; return; fi
  run "$pkg" "$re"; rc=$?; cp -f "$BK/$n" "$f"
  case $rc in 0) echo "M$id SURVIVED (guard did not fail): $re"; fail=1;; 2) echo "M$id undetermined"; fail=2;; *) echo "M$id caught";; esac; }
mutate 1 pkg/navigator/playwright_executor.go 's|"KEYCODE_DPAD_CENTER": "Enter", |"KEYCODE_DPAD_CENTER": "KEYCODE_DPAD_CENTER", |' ./pkg/navigator 'TestBridge_AndroidKeycodes|TestPlaywrightKeyName'
mutate 2 pkg/navigator/playwright_executor.go 's|name, ok := playwrightKeyName(key)|name, ok := key, true|' ./pkg/navigator 'TestBridge_AndroidKeycodes'
mutate 3 pkg/navigator/playwright_executor.go 's|if strings.HasPrefix(k, "KEYCODE_") {|if false {|' ./pkg/navigator 'TestPlaywrightKeyName'
mutate 4 pkg/autonomous/pipeline.go 's|seen\[sha256.Sum256(b)\] = struct{}{}|seen[[32]byte{byte(len(seen))}] = struct{}{}|' ./pkg/autonomous 'TestDistinctFrameCount'
mutate 5 pkg/autonomous/pipeline.go 's|r.FramesCaptured < 2 \|\| r.FramesDistinct != 1 {|r.FramesCaptured < 2 \|\| r.FramesDistinct != 1 \|\| true {|' ./pkg/autonomous 'TestUndeterminedNoInteraction'
mutate 6 pkg/autonomous/pipeline.go 's|if reason := undeterminedNoInteraction(result); reason != "" {|if reason := ""; reason != "" {|' ./pkg/autonomous 'TestApplyFrameVerdicts'
mutate 7 pkg/autonomous/pipeline.go 's|strings.HasPrefix(result.Error, noInteractionReasonPrefix) {|false {|' ./pkg/autonomous 'TestSessionSummaryLine_NoInteraction'
mutate 8 pkg/autonomous/pipeline.go 's|r.Structured != nil \&\& r.Structured.TestCasesRun > 0 {\n\t\treturn ""|&|; /noInteractionReasonPrefix, r.FramesCaptured/s|r.FramesCaptured, r.TestsRun, r.FramesDistinct)|r.FramesCaptured, r.TestsRun, r.FramesCaptured)|' ./pkg/autonomous 'TestUndeterminedNoInteraction'
[ $fail -eq 0 ] && echo "ALL MUTATIONS CAUGHT" || echo "FAIL=$fail"
exit $fail
