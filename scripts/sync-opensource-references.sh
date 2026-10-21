#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Milos Vasic
# SPDX-License-Identifier: Apache-2.0
#
# READ-ONLY report of the third-party tool checkouts listed in
# docs/flat-tools-manifest.tsv. It never fetches, pulls, checks out, merges or
# touches the network, and it writes nothing (no ref, HEAD, index, FETCH_HEAD).
# To move a pin, use the parent project's `git submodule update`.
#
# Manifest rows: old_path must be a clean relative path containing a "/" and
# ending in the tool name; url must be an accepted https/ssh/scp form (see
# below) or the row is INVALID-ROW.
# The resolver treats <root>/<old_path> from the manifest EXACTLY as the legacy
# location (e.g. tools/test-apps/rest-demo); the sibling flat dir and
# HELIXQA_TOOLS_DIR still take precedence.
#
# SKIP policy (mirrors the Go audit): in a standalone checkout (not inside a
# "submodules" dir and no HELIXQA_TOOLS_DIR) where NO manifest tool resolves
# the script prints SKIP and exits 0; in a consumer layout that is exit 3.
# A header-only manifest (no data rows) is exit 2.
#
# Usage:
#   scripts/sync-opensource-references.sh        # print the report
#   (any argument other than -h/--help is rejected with exit 2)
#
# For every manifest row it prints: tool name, tier (flat|legacy|env), the
# MANIFEST_PIN, the CHECKOUT_HEAD (from `git rev-parse HEAD` only), a STATE
# (ok | DRIFT when the two differ | a failure state) and the resolved path.
# Tools are resolved via HELIXQA_TOOLS_DIR, the sibling
# <root>/submodules/<snake_name>, or legacy tools/opensource/.
#
# What it checks before it will run git in a directory (each is tested):
#   - the path has NO symlink component (canonical == resolved), sits under
#     the expected parent for its tier, and is neither the consumer root /
#     helix_qa nor an ancestor of them                    -> WRONG-REPO
#   - the dir is its OWN git checkout                      -> NOT-A-CHECKOUT
#   - git config is read in ALL scopes (--includes --show-scope): exactly one
#     remote.origin.url and it comes from the local scope; any url.*.insteadOf
#     / pushInsteadOf from the local, worktree or command scope is WRONG-REPO
#     (only the user's own global/system rewrites are honoured);
#     extensions.worktreeConfig being enabled is WRONG-REPO too; and the
#     EFFECTIVE origin url (`git ls-remote --get-url
#     origin`, which applies the user's own insteadOf) equals the manifest url
#     after normalisation (https / ssh / scp only; host case-insensitive, path
#     case-sensitive, userinfo ignored, a trailing dot on the host dropped,
#     ports normalised (leading zeros removed, 1-65535) with default ports
#     22/443 ignored and others kept; '_' and single-label hosts (ssh
#     aliases such as "box") are allowed; git://, http://, file://, ext:: and local paths
#     never match)                                         -> WRONG-REPO
#   - manifest rows are validated (4 TAB columns, none empty, tool name
#     [A-Za-z0-9_][A-Za-z0-9._-]*, new_name == the snake name derived from the
#     tool name, 40-hex sha); CRLF and a missing final newline are handled; the
#     processed-row count must equal the manifest data-row count -> INVALID-ROW
#
# Honest limits: git is run with core.fsmonitor=false and core.hooksPath=
# /dev/null and ambient GIT_* variables are unset, but a repository's own
# configuration is still read by git (that is why the url/insteadOf checks
# above exist). The report proves what is checked out, not that it is genuine.
#
# Exit status: 0 = all rows reported (DRIFT does not fail); 2 = bad usage or
# manifest unreadable / bad header; 3 = at least one row was NOT-FOUND,
# NOT-A-CHECKOUT, WRONG-REPO, INVALID-ROW or ERROR, or the row-count
# assertion failed.

set -euo pipefail

# Never honour inherited shell functions/aliases (exported `git`, `cd`, ...):
# first drop every function/builtin name this script relies on, then any other
# imported function. (Honest limit: an inherited function named `unset` or
# `set` itself would run once before this line; a hostile environment that
# can export functions can do much worse, e.g. PATH.)
unset -f unset builtin command unalias compgen git cd pwd head tail awk tr \
  basename dirname sed wc grep printf echo read source mapfile 2>/dev/null || true
unalias -a 2>/dev/null || true
for _f in $(compgen -A function); do unset -f "$_f" 2>/dev/null || true; done
unset _f

# Scrub EVERY exported GIT_* variable (GIT_DIR, GIT_TRACE*, GIT_SSH_COMMAND, ...).
for _v in $(compgen -A export | grep '^GIT_' || true); do unset "$_v"; done
unset _v

case "${1:-}" in
  "") ;;
  -h|--help) sed -n '2,/^set -euo/p' "${BASH_SOURCE[0]}" | sed '$d' | sed 's/^# \{0,1\}//'; exit 0 ;;
  *) echo "unknown argument: $1 (this script is read-only; use -h)" >&2; exit 2 ;;
esac

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "$ROOT_DIR"
ROOT_REAL="$ROOT_DIR"

# An override directory is canonicalised once; the resolver then yields
# physical paths, so any symlink below it shows up as real != resolved.
if [[ -n "${HELIXQA_TOOLS_DIR:-}" ]]; then
  if envreal=$(cd "$HELIXQA_TOOLS_DIR" 2>/dev/null && pwd -P); then
    export HELIXQA_TOOLS_DIR="$envreal"
  fi
fi

# Every git call is hardened and confined to one directory.
g() { local d="$1"; shift; git -C "$d" -c core.fsmonitor=false -c core.hooksPath=/dev/null -c protocol.ext.allow=never "$@"; }

row() { printf '%-22s %-7s %-42s %-42s %-12s %s\n' "$@"; }
row "SUBMODULE" "TIER" "MANIFEST_PIN" "CHECKOUT_HEAD" "STATE" "PATH"
printf '%s\n' "$(printf '=%.0s' {1..170})"

# Normalise a git URL to "host[:port]/path" or print nothing when the form is
# not accepted (https://, ssh://, scp-style only). Same rules as Go NormURL.
norm_url() {
  local LC_ALL=C
  local u="$1" scheme="" host="" port="" path=""
  local re_url='^(https|ssh)://([^/@[:space:]]+@)?([A-Za-z0-9][A-Za-z0-9._-]*)(:([0-9]+))?/([^[:space:]]+)$'
  local re_scp='^([A-Za-z0-9._-]+@)?([A-Za-z0-9][A-Za-z0-9._-]*):([^[:space:]]+)$'
  local re_foreign='^[A-Za-z0-9+.-]*::'
  [[ -z "$u" || "$u" =~ $re_foreign || "$u" =~ [[:space:]] ]] && return 0
  if [[ "$u" =~ $re_url ]]; then
    scheme="${BASH_REMATCH[1]}"; host="${BASH_REMATCH[3]}"
    port="${BASH_REMATCH[5]}"; path="${BASH_REMATCH[6]}"
    if [[ -n "$port" ]]; then
      port=$((10#$port))
      (( port >= 1 && port <= 65535 )) || return 0
    fi
    [[ "$scheme" == https && "$port" == 443 ]] && port=""
    [[ "$scheme" == ssh && "$port" == 22 ]] && port=""
  elif [[ "$u" != *://* && "$u" =~ $re_scp ]]; then
    host="${BASH_REMATCH[2]}"; path="${BASH_REMATCH[3]}"
    [[ "$path" == :* ]] && return 0
    path="${path#/}"
  else
    return 0
  fi
  host="${host%.}"
  host=$(printf '%s' "$host" | tr 'A-Z' 'a-z')
  path="${path%/}"; path="${path%.git}"; path="${path%/}"
  [[ -z "$host" || -z "$path" ]] && return 0
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
EXPECTED_ROWS=$(tail -n +2 "$MANIFEST" | tr -d '\r' | awk '$0 !~ /^[[:space:]]*$/' | wc -l | tr -d ' ')
if [[ "$EXPECTED_ROWS" -eq 0 ]]; then
  echo "manifest has no data rows" >&2
  exit 2
fi

RC=0
NONNF=0
RESOLVED=0
PROCESSED=0
LEGACY_IN_CONSUMER=()
NESTED_IN_CONSUMER=()
CONSUMER=0
if [[ -n "${HELIXQA_TOOLS_DIR:-}" || "$(basename "$(dirname "$ROOT_DIR")")" == "submodules" ]]; then
  CONSUMER=1
fi

# fail_row <name> <tier> <pin> <head> <state> [path]
fail_row() { row "$1" "${2:--}" "${3:--}" "${4:--}" "$5" "${6:--}"; RC=3; [[ "$5" == "NOT-FOUND" ]] || NONNF=1; }

lineno=0
while IFS= read -r line <&3 || [[ -n "$line" ]]; do
  lineno=$((lineno + 1))
  [[ $lineno -eq 1 ]] && continue
  line="${line%$'\r'}"
  [[ -z "${line//[[:space:]]/}" ]] && continue
  PROCESSED=$((PROCESSED + 1))

  # Split on TAB without collapsing empty fields.
  extra=""
  rest="$line"
  old_path="${rest%%$'\t'*}"; if [[ "$rest" == *$'\t'* ]]; then rest="${rest#*$'\t'}"; else rest=""; fi
  new_name="${rest%%$'\t'*}"; if [[ "$rest" == *$'\t'* ]]; then rest="${rest#*$'\t'}"; else rest=""; fi
  url="${rest%%$'\t'*}";      if [[ "$rest" == *$'\t'* ]]; then rest="${rest#*$'\t'}"; else rest=""; fi
  sha="${rest%%$'\t'*}";      if [[ "$rest" == *$'\t'* ]]; then extra="x"; fi
  name=$(basename -- "$old_path" 2>/dev/null || echo "")
  snake=$(printf '%s' "$name" | tr 'A-Z-' 'a-z_')
  sha=$(printf '%s' "$sha" | tr 'A-F' 'a-f')

  if [[ -n "$extra" || -z "$old_path" || -z "$url" || -z "$sha" || -z "$new_name" \
        || ! "$name" =~ ^[A-Za-z0-9_][A-Za-z0-9._-]*$ \
        || ! "$new_name" =~ ^[a-z0-9_]+$ || "$new_name" != "$snake" \
        || ! "$sha" =~ ^[0-9a-f]{40}$ || -z "$(norm_url "$url")" ]] \
     || ! helixqa_valid_old_path "$name" "$old_path"; then
    fail_row "${name:-?}" "-" "-" "-" "INVALID-ROW"
    continue
  fi

  tier_line=$(helixqa_resolve_tool_tier "$name" "$ROOT_DIR" "$old_path" 2>/dev/null) || {
    fail_row "$name" "-" "$sha" "-" "NOT-FOUND"
    continue
  }
  tier="${tier_line%%$'\t'*}"
  resolved="${tier_line#*$'\t'}"
  RESOLVED=$((RESOLVED + 1))
  if [[ "$tier" == "legacy" && $CONSUMER -eq 1 ]]; then
    if [[ "$(dirname -- "$old_path")" == "tools/opensource" ]]; then
      LEGACY_IN_CONSUMER+=("$name")
    else
      NESTED_IN_CONSUMER+=("$name")
    fi
  fi

  real=$(cd "$resolved" 2>/dev/null && pwd -P) || {
    fail_row "$name" "$tier" "$sha" "-" "ERROR" "$resolved"
    continue
  }

  case "$tier" in
    env) expected_parent="${HELIXQA_TOOLS_DIR:-}" ;;
    flat) expected_parent="$(dirname "$ROOT_DIR")" ;;
    *) expected_parent="$ROOT_DIR/$(dirname -- "$old_path")" ;;
  esac

  # (The "under the expected parent" clause below is defence in depth: with
  # real == resolved the resolver already builds <expected_parent>/<name>, so
  # no fixture can reach it alone; it is kept for resolver regressions.)
  # No symlink component anywhere, sits under the expected parent, and is
  # neither the consumer/helix_qa root nor an ancestor of it.
  if [[ "$real" != "$resolved" || "$real" != "$expected_parent"/* \
        || "$real" == "$ROOT_REAL" || "$ROOT_REAL" == "$real"/* ]]; then
    fail_row "$name" "$tier" "$sha" "-" "WRONG-REPO" "$resolved"
    continue
  fi

  # It must be its OWN git checkout, not a dir inside an enclosing repo.
  if [[ "$(g "$real" rev-parse --show-toplevel 2>/dev/null || true)" != "$real" ]]; then
    fail_row "$name" "$tier" "$sha" "-" "NOT-A-CHECKOUT" "$real"
    continue
  fi

  # ...and THE expected tool. Exactly one origin url, no tool-local
  # insteadOf rewrites, effective url (user-level insteadOf applied) must
  # equal the manifest url.
  # All scopes, includes followed. Entries are "<scope><TAB><value>".
  wt=$(g "$real" config --includes --bool --get extensions.worktreeConfig 2>/dev/null || true)
  url_entries=$(g "$real" config --includes --show-scope --get-all remote.origin.url 2>/dev/null || true)
  nvals=$(printf '%s' "$url_entries" | awk 'NF' | wc -l | tr -d ' ')
  nlocal=$(printf '%s\n' "$url_entries" | awk -F'\t' '$1=="local"' | wc -l | tr -d ' ')
  # insteadOf rewrites: only the user's own global/system scope is allowed.
  rewrites=$(g "$real" config --includes --show-scope --get-regexp '^url\..*\.(insteadof|pushinsteadof)$' 2>/dev/null \
    | awk -F'\t' '$1!="global" && $1!="system"' | wc -l | tr -d ' ' || true)
  origin=""
  if [[ "$wt" != "true" && "$nvals" == "1" && "$nlocal" == "1" && "$rewrites" == "0" ]]; then
    origin=$(g "$real" ls-remote --get-url origin 2>/dev/null || true)
  fi
  n_origin=$(norm_url "$origin")
  n_manifest=$(norm_url "$url")
  if [[ -z "$n_origin" || -z "$n_manifest" || "$n_origin" != "$n_manifest" ]]; then
    fail_row "$name" "$tier" "$sha" "-" "WRONG-REPO" "$real"
    continue
  fi

  if ! head_sha=$(g "$real" rev-parse --verify -q HEAD 2>/dev/null) || [[ ! "$head_sha" =~ ^[0-9a-f]{40}$ ]]; then
    fail_row "$name" "$tier" "$sha" "unreadable" "ERROR" "$real"
    continue
  fi

  state="ok"
  [[ "$head_sha" != "$sha" ]] && state="DRIFT"
  row "$name" "$tier" "$sha" "$head_sha" "$state" "$real"
done 3< "$MANIFEST"

SKIPPED=0
if [[ $CONSUMER -eq 0 && $RESOLVED -eq 0 && $NONNF -eq 0 && $PROCESSED -gt 0 ]]; then
  echo "SKIP: standalone checkout and no manifest tool is checked out (HELIXQA_TOOLS_DIR, sibling submodules/, tools/...)"
  RC=0
  SKIPPED=1
fi

if [[ "$PROCESSED" -ne "$EXPECTED_ROWS" ]]; then
  echo "ERROR: processed $PROCESSED rows but manifest has $EXPECTED_ROWS data rows" >&2
  RC=3
fi

if [[ ${#LEGACY_IN_CONSUMER[@]} -gt 0 ]]; then
  echo
  echo "WARNING: legacy tools/opensource resolution in consumer layout: ${LEGACY_IN_CONSUMER[*]}"
  echo "         (non-fatal fallback; add them flat at <root>/submodules/<snake_name>, see docs/flat-tools-manifest.tsv)"
fi

if [[ ${#NESTED_IN_CONSUMER[@]} -gt 0 ]]; then
  echo
  echo "WARNING: legacy nested resolution (old_path outside tools/opensource) in consumer layout: ${NESTED_IN_CONSUMER[*]}"
fi

echo
echo "SUMMARY: rows=$EXPECTED_ROWS processed=$PROCESSED rc=$RC"
echo "(read-only report; to move a pin use the parent project's 'git submodule update')"

exit "$RC"
