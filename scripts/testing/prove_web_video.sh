#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Milos Vasic
# SPDX-License-Identifier: Apache-2.0
#
# prove_web_video.sh -- paired mutation proof (constitution 1.1) for HELIX_WEB_VIDEO_DIR (window-scoped page video).
# Each mutation edits production source in place, runs the guard (real bridge + Chromium + ffmpeg + a real local server
# whose page changes colour), which MUST FAIL, and restores the file. Control first.
# Exit: 0 all caught, 1 survived / control failed, 2 cannot determine (SKIP is never a pass).
set -u
cd "$(dirname "$0")/../.."
for t in go node ffmpeg ffprobe; do command -v "$t" >/dev/null || { echo "cannot determine: $t missing" >&2; exit 2; }; done
[ -n "${HELIXQA_PLAYWRIGHT_NODE_PATH:-}" ] || { echo "cannot determine: set HELIXQA_PLAYWRIGHT_NODE_PATH" >&2; exit 2; }
BK="$(mktemp -d)"; trap 'for f in "$BK"/*.path; do [ -e "$f" ] && cp -f "${f%.path}" "$(cat "$f")"; done; rm -rf "$BK"' EXIT
n=0
save() { n=$((n+1)); cp "$1" "$BK/$n"; echo "$1" > "$BK/$n.path"; }
run() { out="$(go test "$1" -run "$2" -count=1 -v 2>&1)"; rc=$?
  if printf '%s' "$out" | grep -q -- '--- SKIP'; then echo "SKIP in guard $2" >&2; return 2; fi; return $rc; }
fail=0
echo "== control (unmutated) =="
run ./pkg/navigator 'TestBridge_VideoDir_|TestBridge_NoVideoDir_'; rc=$?
[ $rc -eq 0 ] && echo "control PASS" || { echo "control FAIL($rc)"; exit $([ $rc -eq 2 ] && echo 2 || echo 1); }
mutate() { local id="$1" f="$2" expr="$3" pkg="$4" re="$5"
  save "$f"; sed -i "$expr" "$f"
  if cmp -s "$f" "$BK/$n"; then echo "M$id: mutation did not change $f (stale proof)"; fail=1; return; fi
  run "$pkg" "$re"; rc=$?; cp -f "$BK/$n" "$f"
  case $rc in 0) echo "M$id SURVIVED: $re"; fail=1;; 2) echo "M$id undetermined"; fail=2;; *) echo "M$id caught";; esac; }
J=scripts/playwright-bridge.js; V=scripts/playwright-bridge-video.js; T=./pkg/navigator
mutate 1 $J "s|const videoDir = process.env.HELIX_WEB_VIDEO_DIR;|const videoDir = '';|" $T 'TestBridge_VideoDir_RecordsThePageAsWebm'
mutate 2 $J "s|try { process.kill(state.videoPid, 'SIGTERM'); } catch (_) {}|void 0;|" $T 'TestBridge_VideoDir_RecordsThePageAsWebm'
mutate 3 $J "s|failLaunch(proc, 'HELIX_WEB_VIDEO_DIR: could not start|void 0; (\"HELIX_WEB_VIDEO_DIR: could not start|; s|the page recorder (' + (e \&\& e.message \|\| e) + ')');|the page recorder\");|" $T 'TestBridge_VideoDir_UnstartableRecorderAbortsLaunch'
mutate 4 $V "s|await cdp.send('Page.startScreencast', { format: 'jpeg', quality: 70, everyNthFrame: 1 });|void 0;|" $T 'TestBridge_VideoDir_RecordsThePageAsWebm'
mutate 5 $V "s|'-c:v', 'libvpx'|'-frames:v', '1', '-c:v', 'libvpx'|" $T 'TestBridge_VideoDir_RecordsThePageAsWebm'
[ $fail -eq 0 ] && echo "ALL MUTATIONS CAUGHT" || echo "FAIL=$fail"
exit $fail
