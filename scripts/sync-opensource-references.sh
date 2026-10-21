#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Milos Vasic
# SPDX-License-Identifier: Apache-2.0
#
# Fetch + pull every OSS reference checkout (resolved via HELIXQA_TOOLS_DIR,
# sibling <root>/submodules/<snake_name>, or legacy tools/opensource/)
# and print a diff of pinned SHA vs. upstream tip so operators can
# decide whether to bump docs/opensource-references.md.
#
# Usage:
#   scripts/sync-opensource-references.sh              # dry-run diff
#   scripts/sync-opensource-references.sh --fetch-only # fetch without pulling
#   scripts/sync-opensource-references.sh --pull       # actually pull each to remote tip
#
# Safety:
#   - Never runs with sudo; every git call uses the caller's SSH
#     identity via GIT_SSH_COMMAND="ssh -o BatchMode=yes".
#   - Refuses to touch any submodule whose working tree has local
#     modifications.
#   - Exit status: 0 = every manifest tool processed; 3 = at least one row was
#     NOT-FOUND, NOT-A-CHECKOUT, ERROR or PULL-FAILED (output table says which).
#   - Never runs git in a resolved dir that is not its own checkout (NOT-A-CHECKOUT),
#     whose origin differs from the manifest url (WRONG-REPO, ssh/https forms
#     normalised), or that is/contains the consumer root or helix_qa (WRONG-REPO).
#   - Table: MANIFEST_PIN (manifest sha), CHECKOUT_HEAD, REMOTE_HEAD; DRIFT is
#     appended to STATE when CHECKOUT_HEAD != MANIFEST_PIN.
#   - Logs pinned SHA vs. remote HEAD so a human can inspect the diff
#     before committing a SHA bump.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

MODE="diff"
if [[ "${1:-}" == "--pull" ]]; then
  MODE="pull"
elif [[ "${1:-}" == "--fetch-only" ]]; then
  MODE="fetch"
fi

export GIT_SSH_COMMAND="${GIT_SSH_COMMAND:-ssh -o BatchMode=yes}"

row() { printf '%-22s %-42s %-42s %-42s %s\n' "$@"; }
row "SUBMODULE" "MANIFEST_PIN" "CHECKOUT_HEAD" "REMOTE_HEAD" "STATE"
printf '%s\n' "$(printf '=%.0s' {1..170})"

# Normalise a git URL so ssh/https/scp forms of the same host/path compare
# equal: lowercase, strip scheme, userinfo, port, trailing / and .git.
norm_url() {
  local u
  u=$(printf '%s' "$1" | tr 'A-Z' 'a-z')
  if [[ "$u" == *://* ]]; then
    u="${u#*://}"
    u=$(printf '%s' "$u" | sed -E 's#^[^/@]*@##; s#^([^/:]+):[0-9]+/#\1/#')
  elif [[ "$u" == *@*:* ]]; then
    u=$(printf '%s' "$u" | sed -E 's#^[^@]*@##; s#:#/#')
  fi
  u="${u%/}"; u="${u%.git}"
  printf '%s' "$u"
}

# shellcheck source=resolve_tool.sh
source "$ROOT_DIR/scripts/resolve_tool.sh"

# Iterate ONLY the documented tool list (never glob a parent submodules/
# directory: it holds the consumer's first-party submodules).
MANIFEST="${HELIXQA_MANIFEST:-$ROOT_DIR/docs/flat-tools-manifest.tsv}"
[[ -r "$MANIFEST" ]] || { echo "manifest not readable: $MANIFEST" >&2; exit 2; }

RC=0
ROOT_REAL=$(cd "$ROOT_DIR" && pwd -P)
LEGACY_IN_CONSUMER=()
CONSUMER=0
if [[ -n "${HELIXQA_TOOLS_DIR:-}" || "$(basename "$(dirname "$ROOT_DIR")")" == "submodules" ]]; then
  CONSUMER=1
fi

while IFS=$'\t' read -r old_path _new url sha <&3; do
  [[ "$old_path" == "old_path" || -z "$old_path" ]] && continue
  name=$(basename "$old_path")
  tier_line=$(helixqa_resolve_tool_tier "$name" "$ROOT_DIR" 2>/dev/null) || {
    row "$name" "${sha:--}" "-" "-" "NOT-FOUND"
    RC=3
    continue
  }
  tier="${tier_line%%$'\t'*}"
  resolved="${tier_line#*$'\t'}"
  [[ "$tier" == "legacy" && $CONSUMER -eq 1 ]] && LEGACY_IN_CONSUMER+=("$name")
  cd "$resolved" || {
    row "$name" "${sha:--}" "-" "-" "ERROR"
    RC=3
    cd "$ROOT_DIR"
    continue
  }
  real=$(pwd -P)

  # Never act on the consumer root, helix_qa itself, or any dir containing
  # them (e.g. a symlink to the consumer root).
  if [[ "$real" == "$ROOT_REAL" || "$ROOT_REAL" == "$real"/* ]]; then
    row "$name" "${sha:--}" "-" "-" "WRONG-REPO"
    RC=3
    cd "$ROOT_DIR"
    continue
  fi

  # The resolved dir must be its OWN git checkout: otherwise git would
  # walk up and act on the enclosing (consumer) repository.
  if [[ "$(git rev-parse --show-toplevel 2>/dev/null)" != "$real" ]]; then
    row "$name" "${sha:--}" "-" "-" "NOT-A-CHECKOUT"
    RC=3
    cd "$ROOT_DIR"
    continue
  fi

  # ...and THE expected tool: origin must match the manifest URL.
  origin=$(git remote get-url origin 2>/dev/null || echo "")
  if [[ -z "$origin" || -z "$url" || "$(norm_url "$origin")" != "$(norm_url "$url")" ]]; then
    row "$name" "${sha:--}" "-" "-" "WRONG-REPO"
    RC=3
    cd "$ROOT_DIR"
    continue
  fi

  # Guard against dirty working tree.
  if [[ -n "$(git status --porcelain --untracked-files=no)" ]]; then
    row "$name" "${sha:--}" "DIRTY" "-" "SKIP"
    cd "$ROOT_DIR"
    continue
  fi

  pinned=$(git rev-parse HEAD 2>/dev/null || echo "unknown")
  remote_branch=$(git symbolic-ref --short HEAD 2>/dev/null || echo "")
  if [[ -z "$remote_branch" ]]; then
    # Detached HEAD — try the submodule's configured branch or main.
    remote_branch=$(git config --get submodule.branch 2>/dev/null || echo "main")
  fi

  git fetch --quiet origin "$remote_branch" 2>/dev/null || {
    row "$name" "${sha:--}" "$pinned" "fetch-failed" "ERROR"
    RC=3
    cd "$ROOT_DIR"
    continue
  }

  remote_tip=$(git rev-parse "origin/$remote_branch" 2>/dev/null || echo "unknown")

  state="current"
  if [[ "$pinned" != "$remote_tip" && "$remote_tip" != "unknown" ]]; then
    state="BEHIND"
  fi

  if [[ "$MODE" == "pull" && "$state" == "BEHIND" ]]; then
    git checkout --quiet "$remote_branch" 2>/dev/null || true
    git pull --quiet --ff-only origin "$remote_branch" 2>/dev/null || {
      state="PULL-FAILED"
      RC=3
    }
    if [[ "$state" != "PULL-FAILED" ]]; then
      state="PULLED"
    fi
  fi

  pinned=$(git rev-parse HEAD 2>/dev/null || echo "unknown")
  if [[ -n "$sha" && "$pinned" != "$sha"* ]]; then
    state="$state DRIFT"
  fi
  row "$name" "${sha:--}" "$pinned" "$remote_tip" "$state"
  cd "$ROOT_DIR"
done 3< "$MANIFEST"

if [[ ${#LEGACY_IN_CONSUMER[@]} -gt 0 ]]; then
  echo
  echo "WARNING: legacy tools/opensource resolution in consumer layout: ${LEGACY_IN_CONSUMER[*]}"
  echo "         (non-fatal fallback; add them flat at <root>/submodules/<snake_name>, see docs/flat-tools-manifest.tsv)"
fi

echo
echo "NEXT STEPS:"
echo "  - If any submodule is BEHIND, review its upstream changelog."
echo "  - Re-run the OpenClawing2 analysis vs. the new tip before"
echo "    bumping docs/opensource-references.md."
echo "  - Update the Pinned SHA column in opensource-references.md,"
echo "    commit the submodule bump, push to every HelixQA remote."

exit "$RC"
