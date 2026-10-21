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

printf '%-45s %-45s %-45s %s\n' "SUBMODULE" "PINNED_SHA" "REMOTE_HEAD" "STATE"
printf '%s\n' "$(printf '=%.0s' {1..160})"

# shellcheck source=resolve_tool.sh
source "$ROOT_DIR/scripts/resolve_tool.sh"
TOOLS_ROOT="${HELIXQA_TOOLS_DIR:-}"
if [[ -z "$TOOLS_ROOT" ]]; then
  if [[ "$(basename "$(dirname "$ROOT_DIR")")" == "submodules" ]]; then
    TOOLS_ROOT="$(dirname "$ROOT_DIR")"
  else
    TOOLS_ROOT="$ROOT_DIR/tools/opensource"
  fi
fi
for dir in "$TOOLS_ROOT"/*/; do
  name=$(basename "$dir")
  [[ "$dir" == "$ROOT_DIR/" ]] && continue
  resolved=$(helixqa_resolve_tool "$name" "$ROOT_DIR" 2>/dev/null) || continue
  cd "$resolved"

  # Guard against dirty working tree.
  if [[ -n "$(git status --porcelain --untracked-files=no)" ]]; then
    printf '%-45s %-45s %-45s %s\n' "$name" "DIRTY" "-" "SKIP"
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
    printf '%-45s %-45s %-45s %s\n' "$name" "$pinned" "fetch-failed" "ERROR"
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
    }
    if [[ "$state" != "PULL-FAILED" ]]; then
      state="PULLED"
    fi
  fi

  printf '%-45s %-45s %-45s %s\n' "$name" "$pinned" "$remote_tip" "$state"
  cd "$ROOT_DIR"
done

echo
echo "NEXT STEPS:"
echo "  - If any submodule is BEHIND, review its upstream changelog."
echo "  - Re-run the OpenClawing2 analysis vs. the new tip before"
echo "    bumping docs/opensource-references.md."
echo "  - Update the Pinned SHA column in opensource-references.md,"
echo "    commit the submodule bump, push to every HelixQA remote."
