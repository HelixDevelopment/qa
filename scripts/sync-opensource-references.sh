#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Milos Vasic
# SPDX-License-Identifier: Apache-2.0
#
# Fetch (and optionally fast-forward) every OSS reference checkout listed in
# docs/flat-tools-manifest.tsv. Each tool is resolved via HELIXQA_TOOLS_DIR,
# the sibling <root>/submodules/<snake_name>, or legacy tools/opensource/.
#
# Usage:
#   scripts/sync-opensource-references.sh              # dry-run diff
#   scripts/sync-opensource-references.sh --fetch-only # fetch without pulling
#   scripts/sync-opensource-references.sh --pull       # fast-forward BEHIND checkouts
#
# Safety (each is tested):
#   - Never runs with sudo; git uses the caller's SSH identity via
#     GIT_SSH_COMMAND="ssh -o BatchMode=yes". Ambient GIT_* variables
#     (GIT_DIR, GIT_WORK_TREE, GIT_INDEX_FILE, ...) are unset and every git
#     call is `git -C <tool dir>`, so git can never be redirected.
#   - A resolved path must contain NO symlink component (its canonical form
#     must equal its absolute form), must sit under the expected parent for
#     its tier, must not be/contain the consumer root or helix_qa, must be
#     its OWN git checkout (NOT-A-CHECKOUT) and its origin must equal the
#     manifest url (WRONG-REPO). Otherwise no git runs in it.
#   - URL comparison accepts only https / ssh / scp forms: host is
#     case-insensitive, path is case-sensitive, userinfo ignored, default
#     ports (22/443) ignored, non-default ports kept; git://, http://, file://
#     and local paths never match.
#   - --pull only ever moves HEAD with `git merge --ff-only origin/<branch>`
#     and only when HEAD is a STRICT ancestor of the fetched tip (BEHIND).
#     AHEAD / DIVERGED / DETACHED-LOCAL-COMMITS are refused (no write). A
#     dirty tree (tracked changes or non-ignored untracked files) is
#     SKIP-DIRTY (counted in the summary). A detached HEAD that is BEHIND is
#     fast-forwarded in place; no checkout is ever performed.
#   - Manifest rows are validated (4 TAB columns, no empty column, tool name
#     [A-Za-z0-9._-] not starting with '-' or '.', new_name ^[a-z0-9_]+$,
#     40-hex sha, non-empty url); bad rows are INVALID-ROW. CRLF and a
#     missing final newline are handled; the processed-row count must equal
#     the manifest data-row count.
#
# Exit status: 0 = every row processed cleanly (SKIP-DIRTY does not fail);
# 2 = manifest unreadable/bad header; 3 = at least one row was NOT-FOUND,
# NOT-A-CHECKOUT, WRONG-REPO, INVALID-ROW, ERROR, PULL-FAILED, AHEAD,
# DIVERGED or DETACHED-LOCAL-COMMITS, or the row-count assertion failed.
#
# Table: MANIFEST_PIN (manifest sha), CHECKOUT_HEAD, REMOTE_HEAD; DRIFT is
# appended to STATE when CHECKOUT_HEAD != MANIFEST_PIN.

set -euo pipefail

unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_OBJECT_DIRECTORY \
  GIT_ALTERNATE_OBJECT_DIRECTORIES GIT_COMMON_DIR GIT_NAMESPACE \
  GIT_CEILING_DIRECTORIES GIT_DISCOVERY_ACROSS_FILESYSTEM GIT_CONFIG \
  GIT_CONFIG_GLOBAL GIT_CONFIG_SYSTEM GIT_CONFIG_COUNT GIT_CONFIG_PARAMETERS \
  GIT_PREFIX GIT_EXEC_PATH GIT_REPLACE_REF_BASE 2>/dev/null || true

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "$ROOT_DIR"
ROOT_REAL="$ROOT_DIR"

MODE="diff"
if [[ "${1:-}" == "--pull" ]]; then
  MODE="pull"
elif [[ "${1:-}" == "--fetch-only" ]]; then
  MODE="fetch"
fi

export GIT_SSH_COMMAND="${GIT_SSH_COMMAND:-ssh -o BatchMode=yes}"

# An override directory is canonicalised once; the resolver then yields
# physical paths, so any symlink below it shows up as real != abs.
if [[ -n "${HELIXQA_TOOLS_DIR:-}" ]]; then
  if envreal=$(cd "$HELIXQA_TOOLS_DIR" 2>/dev/null && pwd -P); then
    export HELIXQA_TOOLS_DIR="$envreal"
  fi
fi

row() { printf '%-22s %-42s %-42s %-42s %s\n' "$@"; }
row "SUBMODULE" "MANIFEST_PIN" "CHECKOUT_HEAD" "REMOTE_HEAD" "STATE"
printf '%s\n' "$(printf '=%.0s' {1..170})"

# Normalise a git URL to "host[:port]/path" or print nothing when the form is
# not accepted (https://, ssh://, scp-style only).
norm_url() {
  local u="$1" scheme="" host="" port="" path=""
  local re_url='^(https|ssh)://([^/@]+@)?([^/:@]+)(:([0-9]+))?/(.+)$'
  local re_scp='^([^/@:]+@)?([^/:@]+):(.+)$'
  if [[ "$u" =~ $re_url ]]; then
    scheme="${BASH_REMATCH[1]}"; host="${BASH_REMATCH[3]}"
    port="${BASH_REMATCH[5]}"; path="${BASH_REMATCH[6]}"
    [[ "$scheme" == https && "$port" == 443 ]] && port=""
    [[ "$scheme" == ssh && "$port" == 22 ]] && port=""
  elif [[ "$u" != *://* && "$u" =~ $re_scp ]]; then
    host="${BASH_REMATCH[2]}"; path="${BASH_REMATCH[3]}"
    path="${path#/}"
  else
    return 0
  fi
  host=$(printf '%s' "$host" | tr 'A-Z' 'a-z')
  path="${path%/}"; path="${path%.git}"; path="${path%/}"
  [[ -z "$path" ]] && return 0
  printf '%s%s/%s' "$host" "${port:+:$port}" "$path"
}

# shellcheck source=resolve_tool.sh
source "$ROOT_DIR/scripts/resolve_tool.sh"

# Iterate ONLY the documented tool list (never glob a parent submodules/
# directory: it holds the consumer's first-party submodules).
MANIFEST="${HELIXQA_MANIFEST:-$ROOT_DIR/docs/flat-tools-manifest.tsv}"
[[ -r "$MANIFEST" ]] || { echo "manifest not readable: $MANIFEST" >&2; exit 2; }

HEADER=$'old_path\tnew_name\turl\tpinned_sha'
first_line=$(head -n 1 "$MANIFEST" | tr -d '\r' || true)
if [[ "$first_line" != "$HEADER" ]]; then
  echo "manifest header must be: old_path<TAB>new_name<TAB>url<TAB>pinned_sha" >&2
  exit 2
fi
# data rows = non-empty lines after the header (CR stripped; counts a final
# line without trailing newline)
EXPECTED_ROWS=$(tail -n +2 "$MANIFEST" | tr -d '\r' | awk 'length($0)>0' | wc -l | tr -d ' ')

RC=0
PROCESSED=0
SKIP_DIRTY=0
LEGACY_IN_CONSUMER=()
CONSUMER=0
if [[ -n "${HELIXQA_TOOLS_DIR:-}" || "$(basename "$(dirname "$ROOT_DIR")")" == "submodules" ]]; then
  CONSUMER=1
fi

fail_row() { row "$1" "${2:--}" "${3:--}" "${4:--}" "$5"; RC=3; }

lineno=0
while IFS= read -r line <&3 || [[ -n "$line" ]]; do
  lineno=$((lineno + 1))
  [[ $lineno -eq 1 ]] && continue
  line="${line%$'\r'}"
  [[ -z "$line" ]] && continue
  PROCESSED=$((PROCESSED + 1))

  # Split on TAB without collapsing empty fields.
  old_path="" new_name="" url="" sha="" extra=""
  rest="$line"
  old_path="${rest%%$'\t'*}"; [[ "$rest" == *$'\t'* ]] && rest="${rest#*$'\t'}" || rest=""
  new_name="${rest%%$'\t'*}"; [[ "$rest" == *$'\t'* ]] && rest="${rest#*$'\t'}" || rest=""
  url="${rest%%$'\t'*}";      [[ "$rest" == *$'\t'* ]] && rest="${rest#*$'\t'}" || rest=""
  sha="${rest%%$'\t'*}";      [[ "$rest" == *$'\t'* ]] && extra="x"
  name=$(basename -- "$old_path" 2>/dev/null || echo "")

  if [[ -n "$extra" || -z "$old_path" || -z "$url" || -z "$sha" || -z "$new_name" \
        || ! "$name" =~ ^[A-Za-z0-9_][A-Za-z0-9._-]*$ \
        || ! "$new_name" =~ ^[a-z0-9_]+$ \
        || ! "$sha" =~ ^[0-9a-fA-F]{40}$ ]]; then
    fail_row "${name:-?}" "-" "-" "-" "INVALID-ROW"
    continue
  fi

  tier_line=$(helixqa_resolve_tool_tier "$name" "$ROOT_DIR" 2>/dev/null) || {
    fail_row "$name" "$sha" "-" "-" "NOT-FOUND"
    continue
  }
  tier="${tier_line%%$'\t'*}"
  resolved="${tier_line#*$'\t'}"
  [[ "$tier" == "legacy" && $CONSUMER -eq 1 ]] && LEGACY_IN_CONSUMER+=("$name")

  real=$(cd "$resolved" 2>/dev/null && pwd -P) || {
    fail_row "$name" "$sha" "-" "-" "ERROR"
    continue
  }

  case "$tier" in
    env) expected_parent="${HELIXQA_TOOLS_DIR:-}" ;;
    flat) expected_parent="$(dirname "$ROOT_DIR")" ;;
    *) expected_parent="$ROOT_DIR/tools/opensource" ;;
  esac

  # No symlink component anywhere, sits under the expected parent, and is
  # neither the consumer/helix_qa root nor an ancestor of it.
  if [[ "$real" != "$resolved" || "$real" != "$expected_parent"/* \
        || "$real" == "$ROOT_REAL" || "$ROOT_REAL" == "$real"/* ]]; then
    fail_row "$name" "$sha" "-" "-" "WRONG-REPO"
    continue
  fi

  # It must be its OWN git checkout, not a dir inside an enclosing repo.
  if [[ "$(git -C "$real" rev-parse --show-toplevel 2>/dev/null || true)" != "$real" ]]; then
    fail_row "$name" "$sha" "-" "-" "NOT-A-CHECKOUT"
    continue
  fi

  # ...and THE expected tool: raw origin url must match the manifest url.
  origin=$(git -C "$real" config --get remote.origin.url 2>/dev/null || true)
  n_origin=$(norm_url "$origin")
  n_manifest=$(norm_url "$url")
  if [[ -z "$n_origin" || -z "$n_manifest" || "$n_origin" != "$n_manifest" ]]; then
    fail_row "$name" "$sha" "-" "-" "WRONG-REPO"
    continue
  fi

  # Dirty tree (tracked changes or non-ignored untracked files) => skip.
  if ! status_out=$(git -C "$real" status --porcelain 2>/dev/null); then
    fail_row "$name" "$sha" "-" "status-failed" "ERROR"
    continue
  fi
  if [[ -n "$status_out" ]]; then
    SKIP_DIRTY=$((SKIP_DIRTY + 1))
    row "$name" "$sha" "DIRTY" "-" "SKIP-DIRTY"
    continue
  fi

  head_sha=$(git -C "$real" rev-parse HEAD 2>/dev/null || echo "unknown")
  detached=0
  if ! branch=$(git -C "$real" symbolic-ref --short -q HEAD 2>/dev/null); then
    detached=1
    branch=$(git -C "$real" config --get submodule.branch 2>/dev/null || echo "main")
  fi

  if ! git -C "$real" fetch --quiet origin "$branch" 2>/dev/null; then
    fail_row "$name" "$sha" "$head_sha" "fetch-failed" "ERROR"
    continue
  fi
  tip=$(git -C "$real" rev-parse "refs/remotes/origin/$branch" 2>/dev/null || echo "")
  if [[ -z "$tip" || "$head_sha" == "unknown" ]]; then
    fail_row "$name" "$sha" "$head_sha" "unknown" "ERROR"
    continue
  fi

  if [[ "$head_sha" == "$tip" ]]; then
    state="current"
  elif git -C "$real" merge-base --is-ancestor "$head_sha" "$tip" 2>/dev/null; then
    state="BEHIND"
  elif [[ $detached -eq 1 ]]; then
    state="DETACHED-LOCAL-COMMITS"; RC=3
  elif git -C "$real" merge-base --is-ancestor "$tip" "$head_sha" 2>/dev/null; then
    state="AHEAD"; RC=3
  else
    state="DIVERGED"; RC=3
  fi

  if [[ "$MODE" == "pull" && "$state" == "BEHIND" ]]; then
    if git -C "$real" merge --quiet --ff-only "refs/remotes/origin/$branch" >/dev/null 2>&1; then
      state="PULLED"
    else
      state="PULL-FAILED"; RC=3
    fi
  fi

  now_head=$(git -C "$real" rev-parse HEAD 2>/dev/null || echo "unknown")
  if [[ "$now_head" != "$sha" ]]; then
    state="$state DRIFT"
  fi
  row "$name" "$sha" "$now_head" "$tip" "$state"
done 3< "$MANIFEST"

if [[ "$PROCESSED" -ne "$EXPECTED_ROWS" ]]; then
  echo "ERROR: processed $PROCESSED rows but manifest has $EXPECTED_ROWS data rows" >&2
  RC=3
fi

if [[ ${#LEGACY_IN_CONSUMER[@]} -gt 0 ]]; then
  echo
  echo "WARNING: legacy tools/opensource resolution in consumer layout: ${LEGACY_IN_CONSUMER[*]}"
  echo "         (non-fatal fallback; add them flat at <root>/submodules/<snake_name>, see docs/flat-tools-manifest.tsv)"
fi

echo
echo "SUMMARY: rows=$EXPECTED_ROWS processed=$PROCESSED skip-dirty=$SKIP_DIRTY rc=$RC"
echo
echo "NEXT STEPS:"
echo "  - If any submodule is BEHIND, review its upstream changelog."
echo "  - Re-run the OpenClawing2 analysis vs. the new tip before"
echo "    bumping docs/opensource-references.md."
echo "  - Update the Pinned SHA column in opensource-references.md,"
echo "    commit the submodule bump, push to every HelixQA remote."

exit "$RC"
