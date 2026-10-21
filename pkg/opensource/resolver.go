// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package opensource

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	if name == "" || name == "." || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "-") || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return fmt.Errorf("opensource: invalid tool name %q (must be non-empty, not start with '.' or '-', no '/', '\\' or '..')", name)
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

// Deprecated: ToolsRoot ignores the flat <root>/submodules layout; use
// ResolveTool/ResolveToolTier per tool or AuditTools instead.
//
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

// Tier says which lookup tier produced a resolved path.
type Tier string

// Resolution tiers, in priority order (env > flat > legacy).
const (
	TierEnv    Tier = "env"
	TierFlat   Tier = "flat"
	TierLegacy Tier = "legacy"
)

// ownName reports whether name is the module's own directory name
// (helix_qa / helixqa spellings), which is never a tool.
func ownName(moduleRoot, name string) bool {
	n := strings.ReplaceAll(NewName(name), "_", "")
	return n == "helixqa" || n == strings.ReplaceAll(NewName(filepath.Base(moduleRoot)), "_", "")
}

// ResolveTool locates a third-party tool checkout by name; see
// ResolveToolTier for the order. It discards the tier.
func ResolveTool(moduleRoot, name string) (string, error) {
	p, _, err := ResolveToolTier(moduleRoot, name)
	return p, err
}

// ResolveToolTier locates a tool and reports the tier that won. A
// candidate only counts when it is a non-empty directory. Order:
//  1. $HELIXQA_TOOLS_DIR/<snake_name>, then /<name> (TierEnv). When the
//     variable is set it is authoritative: a miss is a hard error.
//  2. <parent>/<snake_name> when moduleRoot sits in a directory named
//     "submodules" (TierFlat)
//  3. <moduleRoot>/tools/opensource/<name>, then /<snake_name>
//     (TierLegacy)
//
// Invalid names (see ValidName) and the module's own name are rejected.
func ResolveToolTier(moduleRoot, name string) (string, Tier, error) {
	if err := ValidName(name); err != nil {
		return "", "", err
	}
	if ownName(moduleRoot, name) {
		return "", "", fmt.Errorf("opensource: invalid tool name %q (is the module's own name)", name)
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
				return p, TierEnv, nil
			}
		}
		return "", "", fmt.Errorf("opensource: tool %q not found under %s=%s (override is authoritative, no fall-through); tried %s",
			name, EnvToolsDir, env, strings.Join(tried, ", "))
	}
	parent := filepath.Dir(moduleRoot)
	if filepath.Base(parent) == "submodules" {
		if p, ok := try(filepath.Join(parent, snake)); ok {
			return p, TierFlat, nil
		}
	}
	legacy := filepath.Join(moduleRoot, "tools", "opensource")
	for _, n := range uniq(name, snake) {
		if p, ok := try(filepath.Join(legacy, n)); ok {
			return p, TierLegacy, nil
		}
	}
	return "", "", fmt.Errorf("opensource: tool %q (flat name %q) not found; tried %s; set %s or add it at <root>/submodules/%s or tools/opensource/%s",
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

const manifestHeader = "old_path\tnew_name\turl\tpinned_sha"

var (
	reSHA     = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	reNewName = regexp.MustCompile(`^[a-z0-9_]+$`)
)

// LoadManifest parses and strictly validates the 4-column TSV manifest:
// exact header, no empty column, 40-hex SHA (lower-cased on load),
// new_name == the snake name derived from the tool name, valid tool name,
// no duplicate old_path or new_name. CRLF and a missing final newline are
// accepted.
func LoadManifest(path string) ([]Tool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opensource: manifest: %w", err)
	}
	defer f.Close()
	var out []Tool
	seenPath, seenName := map[string]bool{}, map[string]bool{}
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimRight(sc.Text(), "\r")
		if n == 1 {
			if line != manifestHeader {
				return nil, fmt.Errorf("opensource: manifest %s: bad header %q", path, line)
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		c := strings.Split(line, "\t")
		if len(c) != 4 {
			return nil, fmt.Errorf("opensource: manifest %s:%d: want 4 columns, got %d", path, n, len(c))
		}
		for i, v := range c {
			if v == "" {
				return nil, fmt.Errorf("opensource: manifest %s:%d: empty column %d", path, n, i+1)
			}
		}
		name := filepath.Base(c[0])
		if err := ValidName(name); err != nil {
			return nil, fmt.Errorf("opensource: manifest %s:%d: %w", path, n, err)
		}
		if !reNewName.MatchString(c[1]) || c[1] != NewName(name) {
			return nil, fmt.Errorf("opensource: manifest %s:%d: new_name %q must be %q", path, n, c[1], NewName(name))
		}
		if !reSHA.MatchString(c[3]) {
			return nil, fmt.Errorf("opensource: manifest %s:%d: pinned_sha %q is not 40 hex", path, n, c[3])
		}
		if seenPath[c[0]] || seenName[c[1]] {
			return nil, fmt.Errorf("opensource: manifest %s:%d: duplicate row %q / %q", path, n, c[0], c[1])
		}
		seenPath[c[0]], seenName[c[1]] = true, true
		out = append(out, Tool{Name: name, OldPath: c[0], NewName: c[1], URL: c[2], SHA: strings.ToLower(c[3])})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, fmt.Errorf("opensource: manifest %s: empty", path)
	}
	return out, nil
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

// Verdict values returned by ToolsVerdict / ToolsReport.
const (
	VerdictOK   = "ok"
	VerdictFail = "fail"
	VerdictSkip = "skip"
)

// LegacyWarning is the visible warning text for legacy-tier resolution
// in a consumer layout.
const LegacyWarning = "WARNING: legacy tools/opensource resolution in consumer layout"

// Report is the result of ToolsReport.
type Report struct {
	Verdict  string
	Tiers    map[string]Tier // legacy Name -> tier used (resolved tools only)
	Warnings []string        // non-fatal, e.g. legacy resolution in consumer layout
}

// ToolsReport applies the audit presence rule: in a consumer layout
// (IsConsumerLayout) every tool must resolve, else FAIL; in a standalone
// checkout it SKIPs only when no tool resolves at all, otherwise OK.
// In a consumer layout, tools that only resolve via the legacy nested
// tier yield a non-fatal warning (the owner chose fallback so consumers
// keep working).
func ToolsReport(moduleRoot string, tools []Tool) Report {
	r := Report{Tiers: map[string]Tier{}}
	var missing []string
	var legacy []string
	for _, t := range tools {
		_, tier, err := ResolveToolTier(moduleRoot, t.Name)
		if err != nil {
			missing = append(missing, t.Name)
			continue
		}
		r.Tiers[t.Name] = tier
		if tier == TierLegacy {
			legacy = append(legacy, t.Name)
		}
	}
	consumer := IsConsumerLayout(moduleRoot)
	switch {
	case consumer && (len(r.Tiers) == 0 || len(missing) > 0):
		r.Verdict = VerdictFail
	case len(r.Tiers) == 0:
		r.Verdict = VerdictSkip
	default:
		r.Verdict = VerdictOK
	}
	if consumer && len(legacy) > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%s: %s", LegacyWarning, strings.Join(legacy, ", ")))
	}
	return r
}

// ToolsVerdict returns only ToolsReport's verdict.
func ToolsVerdict(moduleRoot string, tools []Tool) string {
	return ToolsReport(moduleRoot, tools).Verdict
}
