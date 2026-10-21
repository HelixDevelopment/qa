// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package opensource

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvToolsDir overrides every other lookup: <HELIXQA_TOOLS_DIR>/<name>.
const EnvToolsDir = "HELIXQA_TOOLS_DIR"

// NewName converts a legacy tool directory name (ui-tars) to the flat
// lowercase snake_case name (ui_tars) used under <root>/submodules.
func NewName(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, "-", "_"))
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// ToolsRoot returns the directory holding vendored tools for scans that
// need a single root: $HELIXQA_TOOLS_DIR when set, else the legacy
// <moduleRoot>/tools/opensource.
func ToolsRoot(moduleRoot string) string {
	if v := os.Getenv(EnvToolsDir); v != "" {
		return v
	}
	return filepath.Join(moduleRoot, "tools", "opensource")
}

// ResolveTool locates a third-party tool checkout by name. Order:
//  1. $HELIXQA_TOOLS_DIR/<name> (snake_case, then legacy spelling)
//  2. <parent>/<snake_name> when moduleRoot sits in a directory named
//     "submodules" (flat sibling layout)
//  3. <moduleRoot>/tools/opensource/<legacy name> (legacy fallback)
//
// When nothing exists it returns an error listing every path tried.
func ResolveTool(moduleRoot, name string) (string, error) {
	snake := NewName(name)
	var tried []string
	try := func(p string) (string, bool) {
		tried = append(tried, p)
		return p, isDir(p)
	}
	if env := os.Getenv(EnvToolsDir); env != "" {
		for _, n := range uniq(snake, name) {
			if p, ok := try(filepath.Join(env, n)); ok {
				return p, nil
			}
		}
	}
	parent := filepath.Dir(moduleRoot)
	if filepath.Base(parent) == "submodules" {
		if p, ok := try(filepath.Join(parent, snake)); ok {
			return p, nil
		}
	}
	legacy := filepath.Join(moduleRoot, "tools", "opensource")
	for _, n := range uniq(name, snake) {
		if p, ok := try(filepath.Join(legacy, n)); ok {
			return p, nil
		}
	}
	return "", fmt.Errorf("opensource: tool %q (flat name %q) not found; tried %s; set %s or add it at <root>/submodules/%s or tools/opensource/%s",
		name, snake, strings.Join(tried, ", "), EnvToolsDir, snake, name)
}

func uniq(a, b string) []string {
	if a == b {
		return []string{a}
	}
	return []string{a, b}
}
