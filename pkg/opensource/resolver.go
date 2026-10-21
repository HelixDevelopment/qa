// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package opensource

import (
	"bufio"
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

// usable reports whether p is a populated (non-empty) directory. An
// empty directory is an uninitialised submodule and must not win.
func usable(p string) bool {
	ents, err := os.ReadDir(p)
	return err == nil && len(ents) > 0
}

// ValidName rejects names that could escape the tools directory.
func ValidName(name string) error {
	if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return fmt.Errorf("opensource: invalid tool name %q (must be non-empty, no '/', '\\' or '..')", name)
	}
	return nil
}

// IsConsumerLayout reports whether HelixQA is used as a dependency of a
// consumer project: an explicit HELIXQA_TOOLS_DIR override, or the module
// sitting inside a directory named "submodules". In that layout a missing
// tool is a hard failure, not a skip.
func IsConsumerLayout(moduleRoot string) bool {
	if os.Getenv(EnvToolsDir) != "" {
		return true
	}
	return filepath.Base(filepath.Dir(moduleRoot)) == "submodules"
}

// ToolsRoot returns the directory holding vendored tools for scans that
// need a single root: $HELIXQA_TOOLS_DIR when set, else the legacy
// <moduleRoot>/tools/opensource. Prefer ResolveTool/ResolveAll, which
// also understand the flat <root>/submodules layout.
func ToolsRoot(moduleRoot string) string {
	if v := os.Getenv(EnvToolsDir); v != "" {
		return v
	}
	return filepath.Join(moduleRoot, "tools", "opensource")
}

// ResolveTool locates a third-party tool checkout by name. A candidate
// only counts when it is a non-empty directory. Order:
//  1. $HELIXQA_TOOLS_DIR/<snake_name>, then /<name>. When the variable is
//     set it is authoritative: a miss is a hard error, no fall-through.
//  2. <parent>/<snake_name> when moduleRoot sits in a directory named
//     "submodules" (flat sibling layout)
//  3. <moduleRoot>/tools/opensource/<name>, then /<snake_name> (legacy)
//
// When nothing exists it returns an error listing every path tried.
func ResolveTool(moduleRoot, name string) (string, error) {
	if err := ValidName(name); err != nil {
		return "", err
	}
	snake := NewName(name)
	var tried []string
	try := func(p string) (string, bool) {
		tried = append(tried, p)
		return p, usable(p)
	}
	if env := os.Getenv(EnvToolsDir); env != "" {
		for _, n := range uniq(snake, name) {
			if p, ok := try(filepath.Join(env, n)); ok {
				return p, nil
			}
		}
		return "", fmt.Errorf("opensource: tool %q not found under %s=%s (override is authoritative, no fall-through); tried %s",
			name, EnvToolsDir, env, strings.Join(tried, ", "))
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

// Tool is one row of docs/flat-tools-manifest.tsv.
type Tool struct {
	Name    string // legacy directory name (basename of old_path), e.g. ui-tars
	OldPath string
	NewName string
	URL     string
	SHA     string
}

// LoadManifest parses the 4-column TSV manifest (header skipped).
func LoadManifest(path string) ([]Tool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opensource: manifest: %w", err)
	}
	defer f.Close()
	var out []Tool
	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if first {
			first = false
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		c := strings.Split(line, "\t")
		if len(c) != 4 {
			return nil, fmt.Errorf("opensource: manifest %s: bad row %q", path, line)
		}
		out = append(out, Tool{Name: filepath.Base(c[0]), OldPath: c[0], NewName: c[1], URL: c[2], SHA: c[3]})
	}
	return out, sc.Err()
}

// ResolveAll resolves every tool; found maps legacy Name to its dir,
// missing lists the names that could not be resolved.
func ResolveAll(moduleRoot string, tools []Tool) (found map[string]string, missing []string) {
	found = map[string]string{}
	for _, t := range tools {
		if p, err := ResolveTool(moduleRoot, t.Name); err == nil {
			found[t.Name] = p
		} else {
			missing = append(missing, t.Name)
		}
	}
	return found, missing
}

// Verdict values returned by ToolsVerdict.
const (
	VerdictOK   = "ok"
	VerdictFail = "fail"
	VerdictSkip = "skip"
)

// ToolsVerdict applies the audit presence rule: in a consumer layout
// (IsConsumerLayout) every tool must resolve, else FAIL; in a standalone
// checkout it SKIPs only when no tool resolves at all, otherwise OK.
func ToolsVerdict(moduleRoot string, tools []Tool) string {
	found, missing := ResolveAll(moduleRoot, tools)
	switch {
	case IsConsumerLayout(moduleRoot) && (len(found) == 0 || len(missing) > 0):
		return VerdictFail
	case len(found) == 0:
		return VerdictSkip
	}
	return VerdictOK
}
