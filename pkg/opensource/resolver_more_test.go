// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package opensource

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fill(t *testing.T, dir string) {
	t.Helper()
	mk(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveTool_OverrideMissingIsHardError(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "submodules", "helix_qa")
	mk(t, root)
	fill(t, filepath.Join(base, "submodules", "ui_tars")) // sibling exists
	fill(t, filepath.Join(root, "tools", "opensource", "ui-tars"))
	t.Setenv("HELIXQA_TOOLS_DIR", filepath.Join(base, "empty-override"))
	_, err := ResolveTool(root, "ui-tars")
	if err == nil || !strings.Contains(err.Error(), "HELIXQA_TOOLS_DIR") {
		t.Fatalf("want hard error naming HELIXQA_TOOLS_DIR, got %v", err)
	}
}

func TestResolveTool_EmptyDirDoesNotWin(t *testing.T) {
	t.Setenv("HELIXQA_TOOLS_DIR", "")
	base := t.TempDir()
	root := filepath.Join(base, "submodules", "helix_qa")
	mk(t, root)
	mk(t, filepath.Join(base, "submodules", "ui_tars")) // empty, uninitialised
	want := filepath.Join(root, "tools", "opensource", "ui-tars")
	fill(t, want)
	got, err := ResolveTool(root, "ui-tars")
	if err != nil || got != want {
		t.Fatalf("got %q err %v want %q", got, err, want)
	}
}

func TestResolveTool_OnlyEmptyIsError(t *testing.T) {
	t.Setenv("HELIXQA_TOOLS_DIR", "")
	root := filepath.Join(t.TempDir(), "x", "helix_qa")
	mk(t, filepath.Join(root, "tools", "opensource", "ui-tars"))
	if _, err := ResolveTool(root, "ui-tars"); err == nil {
		t.Fatal("empty dir must not resolve")
	}
}

func TestResolveTool_LegacySpellingBeforeSnakeInLegacyDir(t *testing.T) {
	t.Setenv("HELIXQA_TOOLS_DIR", "")
	root := filepath.Join(t.TempDir(), "x", "helix_qa")
	legacy := filepath.Join(root, "tools", "opensource")
	fill(t, filepath.Join(legacy, "ui-tars"))
	fill(t, filepath.Join(legacy, "ui_tars"))
	got, _ := ResolveTool(root, "ui-tars")
	if got != filepath.Join(legacy, "ui-tars") {
		t.Fatalf("legacy spelling must win in legacy dir, got %q", got)
	}
}

func TestResolveTool_SnakeBeforeLegacyInEnvDir(t *testing.T) {
	env := t.TempDir()
	fill(t, filepath.Join(env, "ui-tars"))
	fill(t, filepath.Join(env, "ui_tars"))
	t.Setenv("HELIXQA_TOOLS_DIR", env)
	got, _ := ResolveTool(t.TempDir(), "ui-tars")
	if got != filepath.Join(env, "ui_tars") {
		t.Fatalf("flat name must win in override dir, got %q", got)
	}
}

func TestResolveTool_RejectsBadNames(t *testing.T) {
	t.Setenv("HELIXQA_TOOLS_DIR", "")
	for _, n := range []string{"", "..", "a/b", "../x", "a\\b", "a..b"} {
		if _, err := ResolveTool(t.TempDir(), n); err == nil || !strings.Contains(err.Error(), "invalid tool name") {
			t.Errorf("name %q: want invalid tool name error, got %v", n, err)
		}
	}
}

func TestIsConsumerLayout(t *testing.T) {
	t.Setenv("HELIXQA_TOOLS_DIR", "")
	if IsConsumerLayout(filepath.Join("/a", "submodules", "helix_qa")) != true {
		t.Error("submodules parent is consumer layout")
	}
	if IsConsumerLayout("/a/b/helix_qa") {
		t.Error("standalone is not consumer layout")
	}
	t.Setenv("HELIXQA_TOOLS_DIR", "/z")
	if !IsConsumerLayout("/a/b/helix_qa") {
		t.Error("env override implies consumer layout")
	}
}

func TestLoadManifestAndResolveAll(t *testing.T) {
	t.Setenv("HELIXQA_TOOLS_DIR", "")
	dir := t.TempDir()
	mf := filepath.Join(dir, "m.tsv")
	os.WriteFile(mf, []byte("old_path\tnew_name\turl\tpinned_sha\n"+
		"tools/opensource/ui-tars\tui_tars\tgit@x:y/z.git\t"+strings.Repeat("a", 40)+"\n"+
		"tools/opensource/scrcpy\tscrcpy\tgit@x:y/s.git\t"+strings.Repeat("B", 40)+"\n"), 0o644)
	tools, err := LoadManifest(mf)
	if err != nil || len(tools) != 2 || tools[0].Name != "ui-tars" || tools[0].NewName != "ui_tars" {
		t.Fatalf("manifest: %v %+v", err, tools)
	}
	root := filepath.Join(dir, "p", "helix_qa")
	fill(t, filepath.Join(root, "tools", "opensource", "scrcpy"))
	found, missing := ResolveAll(root, tools)
	if len(found) != 1 || len(missing) != 1 || missing[0] != "ui-tars" {
		t.Fatalf("found=%v missing=%v", found, missing)
	}
}

func TestAuditTools_FlatSnakeLayoutAgainstLegacyKeyedDocs(t *testing.T) {
	t.Setenv("HELIXQA_TOOLS_DIR", "")
	base := t.TempDir()
	root := filepath.Join(base, "submodules", "helix_qa")
	mk(t, root)
	fill(t, filepath.Join(base, "submodules", "ui_tars"))
	ref := filepath.Join(base, "ref.md")
	lic := filepath.Join(base, "lic.md")
	os.WriteFile(ref, []byte("tools/opensource/ui-tars"), 0o644)
	os.WriteFile(lic, []byte("tools/opensource/ui-tars"), 0o644)
	res, err := AuditTools(root, ref, lic, []Tool{{Name: "ui-tars", NewName: "ui_tars"}})
	if err != nil || !res.OK() || len(res.Submodules) != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	os.WriteFile(lic, []byte("nothing"), 0o644)
	res, _ = AuditTools(root, ref, lic, []Tool{{Name: "ui-tars", NewName: "ui_tars"}})
	if res.OK() || len(res.MissingFromLicences) != 1 {
		t.Fatalf("expected licence miss, got %+v", res)
	}
	res, _ = AuditTools(root, ref, lic, []Tool{{Name: "gone", NewName: "gone"}})
	if len(res.MissingTools) != 1 || res.OK() {
		t.Fatalf("missing tool must fail audit: %+v", res)
	}
}

// Go/shell parity: the shell resolver must give the same answer as Go.
func TestShellResolverParity(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available SKIP-OK: #bash-optional")
	}
	script, _ := filepath.Abs("../../scripts/resolve_tool.sh")
	type fx struct {
		name  string
		build func(base string) (root, env string)
	}
	cases := []fx{
		{"sibling", func(b string) (string, string) {
			r := filepath.Join(b, "submodules", "helix_qa")
			mk(t, r)
			fill(t, filepath.Join(b, "submodules", "ui_tars"))
			return r, ""
		}},
		{"legacy", func(b string) (string, string) {
			r := filepath.Join(b, "o", "helix_qa")
			fill(t, filepath.Join(r, "tools", "opensource", "ui-tars"))
			return r, ""
		}},
		{"empty-sibling-loses", func(b string) (string, string) {
			r := filepath.Join(b, "submodules", "helix_qa")
			mk(t, filepath.Join(b, "submodules", "ui_tars"))
			fill(t, filepath.Join(r, "tools", "opensource", "ui-tars"))
			return r, ""
		}},
		{"env", func(b string) (string, string) {
			r := filepath.Join(b, "submodules", "helix_qa")
			mk(t, r)
			fill(t, filepath.Join(b, "e", "ui_tars"))
			fill(t, filepath.Join(b, "submodules", "ui_tars"))
			return r, filepath.Join(b, "e")
		}},
		{"env-missing", func(b string) (string, string) {
			r := filepath.Join(b, "submodules", "helix_qa")
			fill(t, filepath.Join(b, "submodules", "ui_tars"))
			mk(t, r)
			return r, filepath.Join(b, "nope")
		}},
		{"none", func(b string) (string, string) { return filepath.Join(b, "q", "helix_qa"), "" }},
	}
	for _, c := range cases {
		b := t.TempDir()
		root, env := c.build(b)
		t.Setenv("HELIXQA_TOOLS_DIR", env)
		goOut, goErr := ResolveTool(root, "ui-tars")
		cmd := exec.Command("bash", script, "ui-tars", root)
		cmd.Env = append(os.Environ(), "HELIXQA_TOOLS_DIR="+env)
		out, err := cmd.Output()
		sh := strings.TrimSpace(string(out))
		if (goErr == nil) != (err == nil) || sh != goOut {
			t.Errorf("%s: go=(%q,%v) shell=(%q,%v)", c.name, goOut, goErr, sh, err)
		}
		if env != "" && goErr != nil && c.name == "env-missing" {
			if ee, ok := err.(*exec.ExitError); !ok || !strings.Contains(string(ee.Stderr), "HELIXQA_TOOLS_DIR") {
				t.Errorf("shell override error must name HELIXQA_TOOLS_DIR")
			}
		}
	}
	for _, n := range []string{"..", "a/b", ""} {
		if err := exec.Command("bash", script, n, t.TempDir()).Run(); err == nil {
			t.Errorf("shell must reject name %q", n)
		}
	}
}

func TestToolsVerdict(t *testing.T) {
	t.Setenv("HELIXQA_TOOLS_DIR", "")
	tools := []Tool{{Name: "a", NewName: "a"}, {Name: "b", NewName: "b"}}
	b := t.TempDir()
	consumer := filepath.Join(b, "submodules", "helix_qa")
	mk(t, consumer)
	if v := ToolsVerdict(consumer, tools); v != VerdictFail {
		t.Errorf("consumer with none: %s", v)
	}
	fill(t, filepath.Join(b, "submodules", "a"))
	if v := ToolsVerdict(consumer, tools); v != VerdictFail {
		t.Errorf("consumer with partial: %s", v)
	}
	fill(t, filepath.Join(b, "submodules", "b"))
	if v := ToolsVerdict(consumer, tools); v != VerdictOK {
		t.Errorf("consumer complete: %s", v)
	}
	sa := filepath.Join(b, "x", "helix_qa")
	mk(t, sa)
	if v := ToolsVerdict(sa, tools); v != VerdictSkip {
		t.Errorf("standalone none: %s", v)
	}
	fill(t, filepath.Join(sa, "tools", "opensource", "a"))
	if v := ToolsVerdict(sa, tools); v != VerdictOK {
		t.Errorf("standalone partial: %s", v)
	}
}

func TestResolveToolTier(t *testing.T) {
	t.Setenv("HELIXQA_TOOLS_DIR", "")
	b := t.TempDir()
	root := filepath.Join(b, "submodules", "helix_qa")
	fill(t, filepath.Join(root, "tools", "opensource", "ui-tars"))
	p, tier, err := ResolveToolTier(root, "ui-tars")
	if err != nil || tier != TierLegacy || p != filepath.Join(root, "tools", "opensource", "ui-tars") {
		t.Fatalf("legacy: %q %q %v", p, tier, err)
	}
	flat := filepath.Join(b, "submodules", "ui_tars")
	fill(t, flat)
	p, tier, _ = ResolveToolTier(root, "ui-tars")
	if tier != TierFlat || p != flat {
		t.Fatalf("flat must win when populated: %q %q", p, tier)
	}
	env := filepath.Join(b, "env")
	fill(t, filepath.Join(env, "ui_tars"))
	t.Setenv("HELIXQA_TOOLS_DIR", env)
	if _, tier, _ = ResolveToolTier(root, "ui-tars"); tier != TierEnv {
		t.Fatalf("env tier: %q", tier)
	}
}

func TestToolsReport_WarnsOnLegacyInConsumerLayout(t *testing.T) {
	t.Setenv("HELIXQA_TOOLS_DIR", "")
	b := t.TempDir()
	root := filepath.Join(b, "submodules", "helix_qa")
	fill(t, filepath.Join(root, "tools", "opensource", "a"))
	fill(t, filepath.Join(b, "submodules", "bb"))
	tools := []Tool{{Name: "a", NewName: "a"}, {Name: "bb", NewName: "bb"}}
	r := ToolsReport(root, tools)
	if r.Verdict != VerdictOK || len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "legacy tools/opensource resolution in consumer layout") || !strings.Contains(r.Warnings[0], "a") {
		t.Fatalf("%+v", r)
	}
	if r.Tiers["bb"] != TierFlat || r.Tiers["a"] != TierLegacy {
		t.Fatalf("tiers %+v", r.Tiers)
	}
	// standalone: legacy is the normal tier, no warning
	sa := filepath.Join(b, "x", "helix_qa")
	fill(t, filepath.Join(sa, "tools", "opensource", "a"))
	if r := ToolsReport(sa, tools[:1]); len(r.Warnings) != 0 {
		t.Fatalf("standalone must not warn: %+v", r)
	}
}

func TestValidName_DotsAndOwnName(t *testing.T) {
	t.Setenv("HELIXQA_TOOLS_DIR", "")
	root := filepath.Join(t.TempDir(), "submodules", "helix_qa")
	for _, n := range []string{".", ".hidden", "helix_qa", "helix-qa", "helixqa", "HelixQA"} {
		if _, err := ResolveTool(root, n); err == nil || !strings.Contains(err.Error(), "invalid tool name") {
			t.Errorf("%q must be rejected, got %v", n, err)
		}
	}
}

func TestShellRejectsInvalidNames(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available SKIP-OK: #bash-optional")
	}
	script, _ := filepath.Abs("../../scripts/resolve_tool.sh")
	root := filepath.Join(t.TempDir(), "submodules", "helix_qa")
	mk(t, root)
	for _, n := range []string{"/", "a/b", "a\\b", "..", ".", "", ".hidden", "helix_qa", "helix-qa", "helixqa"} {
		out, err := exec.Command("bash", script, n, root).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "invalid tool name") {
			t.Errorf("shell must reject %q (err=%v out=%q)", n, err, out)
		}
	}
}

func TestLoadManifest_Validation(t *testing.T) {
	h := "old_path\tnew_name\turl\tpinned_sha\n"
	sha := strings.Repeat("a", 40)
	row := func(p, n, u, s string) string { return p + "\t" + n + "\t" + u + "\t" + s + "\n" }
	bad := map[string]string{
		"bad-header":        "old\tnew\turl\tsha\n" + row("tools/opensource/a", "a", "u", sha),
		"empty-url":         h + row("tools/opensource/a", "a", "", sha),
		"empty-new-name":    h + row("tools/opensource/a", "", "u", sha),
		"empty-sha":         h + row("tools/opensource/a", "a", "u", ""),
		"short-sha":         h + row("tools/opensource/a", "a", "u", "abc"),
		"non-hex-sha":       h + row("tools/opensource/a", "a", "u", strings.Repeat("g", 40)),
		"new-name-mismatch": h + row("tools/opensource/ui-tars", "tars", "u", sha),
		"new-name-dashes":   h + row("tools/opensource/ui-tars", "ui-tars", "u", sha),
		"dash-leading-name": h + row("tools/opensource/-rf", "_rf", "u", sha),
		"duplicate-row":     h + row("tools/opensource/a", "a", "u", sha) + row("tools/opensource/a", "a", "u", sha),
		"duplicate-newname": h + row("tools/opensource/a-b", "a_b", "u", sha) + row("tools/x/a_b", "a_b", "u", sha),
		"extra-column":      h + "tools/opensource/a\ta\tu\t" + sha + "\textra\n",
		"empty-file":        "",
	}
	for name, content := range bad {
		f := filepath.Join(t.TempDir(), "m.tsv")
		os.WriteFile(f, []byte(content), 0o644)
		if _, err := LoadManifest(f); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	ok := map[string]string{
		"crlf-no-final-newline": strings.ReplaceAll(h+row("tools/opensource/ui-tars", "ui_tars", "u", strings.ToUpper(sha)), "\n", "\r\n"),
		"uppercase-sha":         h + row("tools/opensource/a", "a", "u", strings.ToUpper(sha)),
	}
	for name, content := range ok {
		f := filepath.Join(t.TempDir(), "m.tsv")
		os.WriteFile(f, []byte(strings.TrimRight(content, "\r\n")), 0o644)
		tools, err := LoadManifest(f)
		if err != nil || len(tools) != 1 || tools[0].SHA != sha {
			t.Errorf("%s: %v %+v (sha must be lowercased)", name, err, tools)
		}
	}
}

func TestRealManifestIsValid(t *testing.T) {
	tools, err := LoadManifest("../../docs/flat-tools-manifest.tsv")
	if err != nil || len(tools) != 27 {
		t.Fatalf("real manifest: %v (%d rows)", err, len(tools))
	}
}

func TestAuditTools_DelimiterBoundedMatch(t *testing.T) {
	t.Setenv("HELIXQA_TOOLS_DIR", "")
	base := t.TempDir()
	root := filepath.Join(base, "submodules", "helix_qa")
	mk(t, root)
	fill(t, filepath.Join(base, "submodules", "ui_tars"))
	tools := []Tool{{Name: "ui-tars", NewName: "ui_tars"}}
	ref := filepath.Join(base, "ref.md")
	lic := filepath.Join(base, "lic.md")
	// only the longer sibling is documented: ui-tars must be MISSING
	os.WriteFile(ref, []byte("| `tools/opensource/ui-tars-desktop` | x |"), 0o644)
	os.WriteFile(lic, []byte("tools/opensource/ui-tars-desktop"), 0o644)
	res, _ := AuditTools(root, ref, lic, tools)
	if len(res.MissingFromReferences) != 1 || len(res.MissingFromLicences) != 1 {
		t.Fatalf("prefix collision must not satisfy the audit: %+v", res)
	}
	for _, doc := range []string{"`tools/opensource/ui-tars`", "tools/opensource/ui-tars.", "see tools/opensource/ui-tars, and", "tools/opensource/ui-tars", "| tools/opensource/ui-tars |", "(tools/opensource/ui-tars/)"} {
		os.WriteFile(ref, []byte(doc), 0o644)
		os.WriteFile(lic, []byte(doc), 0o644)
		res, _ = AuditTools(root, ref, lic, tools)
		if !res.OK() {
			t.Errorf("doc %q should satisfy the audit: %+v", doc, res)
		}
	}
	os.WriteFile(ref, []byte("tools/opensource/ui-tars.md tools/opensource/ui-tars_x"), 0o644)
	os.WriteFile(lic, []byte("tools/opensource/ui-tars"), 0o644)
	if res, _ = AuditTools(root, ref, lic, tools); len(res.MissingFromReferences) != 1 {
		t.Errorf("file-extension / underscore continuation is not the tool: %+v", res)
	}
}
