#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Milos Vasic
# SPDX-License-Identifier: Apache-2.0
#
# helixqa_resolve_tool <name> [module_root]
# Prints the checkout directory of a third-party tool. Order (mirrors
# pkg/opensource.ResolveTool):
#   1. $HELIXQA_TOOLS_DIR/<snake_name>  (then legacy spelling)
#   2. <parent>/<snake_name> when module_root sits in a dir named "submodules"
#   3. <module_root>/tools/opensource/<name>  (legacy fallback)
# Exits 1 with the tried paths on stderr when none exists.
# Source this file, or run it: scripts/resolve_tool.sh ui-tars

helixqa_resolve_tool() {
  local name="$1"
  local root="${2:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
  local snake
  snake=$(printf '%s' "$name" | tr 'A-Z-' 'a-z_')
  local tried=() cand
  if [[ -n "${HELIXQA_TOOLS_DIR:-}" ]]; then
    for cand in "$HELIXQA_TOOLS_DIR/$snake" "$HELIXQA_TOOLS_DIR/$name"; do
      tried+=("$cand"); [[ -d "$cand" ]] && { printf '%s\n' "$cand"; return 0; }
    done
  fi
  local parent; parent=$(dirname "$root")
  if [[ "$(basename "$parent")" == "submodules" ]]; then
    cand="$parent/$snake"; tried+=("$cand")
    [[ -d "$cand" ]] && { printf '%s\n' "$cand"; return 0; }
  fi
  for cand in "$root/tools/opensource/$name" "$root/tools/opensource/$snake"; do
    tried+=("$cand"); [[ -d "$cand" ]] && { printf '%s\n' "$cand"; return 0; }
  done
  printf 'helixqa_resolve_tool: %q not found; tried: %s; set HELIXQA_TOOLS_DIR or add <root>/submodules/%s\n' \
    "$name" "${tried[*]}" "$snake" >&2
  return 1
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  helixqa_resolve_tool "$@"
fi
