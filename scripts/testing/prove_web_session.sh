#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Milos Vasic
# SPDX-License-Identifier: Apache-2.0
#
# prove_web_session.sh -- paired mutation proof (constitution 1.1) for HELIX_WEB_COOKIE_FILE:
# a console behind a passkey login can only be driven with an operator-supplied session.
# Each mutation edits production source in place, runs the guard (real bridge + Chromium +
# a real local server), which MUST FAIL, and restores the file. Control first.
# Exit: 0 all caught, 1 survived / control failed, 2 cannot determine (SKIP is never a pass).
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
run ./pkg/navigator 'TestBridge_NoCookieFile|TestBridge_CookieFile|TestBridge_CookieInstallFailure'; rc=$?
[ $rc -eq 0 ] && echo "control PASS" || { echo "control FAIL($rc)"; exit $([ $rc -eq 2 ] && echo 2 || echo 1); }
mutate() { local id="$1" f="$2" expr="$3" pkg="$4" re="$5"
  save "$f"; sed -i "$expr" "$f"
  if cmp -s "$f" "$BK/$n"; then echo "M$id: mutation did not change $f (stale proof)"; fail=1; return; fi
  run "$pkg" "$re"; rc=$?; cp -f "$BK/$n" "$f"
  case $rc in 0) echo "M$id SURVIVED: $re"; fail=1;; 2) echo "M$id undetermined"; fail=2;; *) echo "M$id caught";; esac; }
J=scripts/playwright-bridge.js; C=scripts/playwright-bridge-cookies.js; T=./pkg/navigator
mutate 1 $J "s|await cdp.send('Network.setCookies', { cookies: sessionCookies });|void 0;|" $T 'TestBridge_CookieFile_EstablishesSession'
mutate 2 $C 's|if ((st.mode \& 0o077) !== 0)|if (false)|' $T 'TestBridge_CookieFile_LooseModeRefused'
mutate 3 $C "s|if (!ALLOWED.has(k))|if (false)|" $T 'TestBridge_CookieFile_MalformedRefused'
mutate 4 $C "s|if (!Array.isArray(list) \|\| list.length === 0)|if (false)|" $T 'TestBridge_CookieFile_MalformedRefused'
mutate 5 $J 's|sessionCookies = loadCookies(process.env, cmd.url \|\| .*);|sessionCookies = null;|' $T 'TestBridge_CookieFile_EstablishesSession'
mutate 6 $C "s|has unsupported key \"' + k + '\"|has unsupported key \"' + k + '\" in ' + JSON.stringify(c) + '|" $T 'TestBridge_CookieFile_ValueNeverInError'
# bridge lifecycle: a refused launch must not orphan its browser; the CDP port must not be the fixed 9223
mutate 7 $J 's|  killBrowser(proc \&\& proc.pid);|  void 0;|' $T 'TestBridge_CookieInstallFailure_LeavesNoBrowser'
mutate 8 $J 's|CDP_PORT = await freePort();|CDP_PORT = 9223;|' $T 'TestBridge_CDPPort_IsNotTheFixedPort'
[ $fail -eq 0 ] && echo "ALL MUTATIONS CAUGHT" || echo "FAIL=$fail"
exit $fail
