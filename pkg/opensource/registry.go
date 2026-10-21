// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// Package opensource exposes a small, well-tested helper that walks
// the tools root (see ToolsRoot/ResolveTool: $HELIXQA_TOOLS_DIR, flat
// <root>/submodules/<snake_name>, legacy tools/opensource/) and asserts every
// vendored submodule has a matching row in docs/opensource-references.md + a licence entry in
// docs/licences-inventory.md. This package is the enforcement point
// behind OpenClawing2 Phase 1's P1.T9 / P1.T10 regression tests.
//
// The helper is project-agnostic by construction — it takes the
// filesystem root + the two doc paths as arguments so any project
// using HelixQA's tools/opensource/ convention can reuse the check.
package opensource

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// AuditResult summarises the state of the OSS vendoring audit.
type AuditResult struct {
	// Submodules lists every directory under the scanned root.
	Submodules []string
	// MissingFromReferences names directories that do not appear in
	// opensource-references.md.
	MissingFromReferences []string
	// MissingFromLicences names directories that do not appear in
	// licences-inventory.md.
	MissingFromLicences []string
	// MissingTools names manifest tools that could not be resolved
	// (AuditTools only).
	MissingTools []string
}

// OK reports whether every submodule is documented in both files.
func (r AuditResult) OK() bool {
	return len(r.MissingFromReferences) == 0 && len(r.MissingFromLicences) == 0 && len(r.MissingTools) == 0
}

// Audit walks scannedRoot for immediate subdirectories (each taken
// to be an OSS submodule), then checks both docs files for a
// "tools/opensource/<name>" reference. Missing submodules are
// collected into the result so callers can render a precise error.
func Audit(scannedRoot, referencesDoc, licencesDoc string) (AuditResult, error) {
	var res AuditResult

	entries, err := os.ReadDir(scannedRoot)
	if err != nil {
		return res, fmt.Errorf("opensource audit: read %s: %w", scannedRoot, err)
	}

	refData, err := os.ReadFile(referencesDoc)
	if err != nil {
		return res, fmt.Errorf("opensource audit: read %s: %w", referencesDoc, err)
	}
	licData, err := os.ReadFile(licencesDoc)
	if err != nil {
		return res, fmt.Errorf("opensource audit: read %s: %w", licencesDoc, err)
	}

	refText := string(refData)
	licText := string(licData)

	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		name := e.Name()
		res.Submodules = append(res.Submodules, name)

		if !DocMentions(refText, name) {
			res.MissingFromReferences = append(res.MissingFromReferences, name)
		}
		if !DocMentions(licText, name) {
			res.MissingFromLicences = append(res.MissingFromLicences, name)
		}
	}
	return res, nil
}

// DocMentions reports whether doc names "tools/opensource/<name>" as a
// whole token: the name must not continue with [A-Za-z0-9_-] or with
// ".<alnum>", so "ui-tars" does not match "ui-tars-desktop" or "ui-tars.md".
func DocMentions(doc, name string) bool {
	re := regexp.MustCompile(regexp.QuoteMeta("tools/opensource/"+name) + `(?:$|[^A-Za-z0-9_.\-]|\.(?:$|[^A-Za-z0-9]))`)
	return re.MatchString(doc)
}

// AuditTools is the layout-independent audit: each manifest tool is
// resolved with ResolveTool (flat snake_case sibling, env override or
// legacy tools/opensource) and its legacy name is checked against the
// docs, which stay keyed by "tools/opensource/<legacy-name>". Tools that
// cannot be resolved are reported in MissingTools (and fail OK()).
func AuditTools(moduleRoot, referencesDoc, licencesDoc string, tools []Tool) (AuditResult, error) {
	var res AuditResult
	refData, err := os.ReadFile(referencesDoc)
	if err != nil {
		return res, fmt.Errorf("opensource audit: read %s: %w", referencesDoc, err)
	}
	licData, err := os.ReadFile(licencesDoc)
	if err != nil {
		return res, fmt.Errorf("opensource audit: read %s: %w", licencesDoc, err)
	}
	for _, t := range tools {
		if _, err := ResolveTool(moduleRoot, t.Name); err != nil {
			res.MissingTools = append(res.MissingTools, t.Name)
			continue
		}
		res.Submodules = append(res.Submodules, t.Name)
		if !DocMentions(string(refData), t.Name) {
			res.MissingFromReferences = append(res.MissingFromReferences, t.Name)
		}
		if !DocMentions(string(licData), t.Name) {
			res.MissingFromLicences = append(res.MissingFromLicences, t.Name)
		}
	}
	return res, nil
}

// LocateLicenceFile returns the most likely licence file path inside
// the submodule directory, or "" when none was found. Used by the
// future OC-REF-001 challenge to confirm every submodule ships a
// parseable licence.
func LocateLicenceFile(submoduleDir string) string {
	candidates := []string{"LICENSE", "LICENSE.md", "LICENSE.txt", "COPYING", "COPYING.md", "LICENCE", "LICENCE.md"}
	for _, c := range candidates {
		path := filepath.Join(submoduleDir, c)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

// WalkGitDirs iterates every submodule directory under root and
// calls fn once per entry. Skips hidden dirs + non-directories.
func WalkGitDirs(root string, fn func(name, path string) error) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if err := fn(e.Name(), filepath.Join(root, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// statIsFile is a tiny helper kept here to avoid importing os.Stat
// result plumbing into test files.
func statIsFile(info fs.FileInfo, err error) bool {
	return err == nil && info != nil && !info.IsDir()
}
