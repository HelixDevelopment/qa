// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package opensource

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mk(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestNewName(t *testing.T) {
	cases := map[string]string{"ui-tars": "ui_tars", "kiwi-tcms": "kiwi_tcms", "Scrcpy": "scrcpy", "ui_tars": "ui_tars"}
	for in, want := range cases {
		if got := NewName(in); got != want {
			t.Errorf("NewName(%q)=%q want %q", in, got, want)
		}
	}
}

func TestResolveTool_SiblingInSubmodulesDir(t *testing.T) {
	t.Setenv("HELIXQA_TOOLS_DIR", "")
	base := t.TempDir()
	root := filepath.Join(base, "submodules", "helix_qa")
	mk(t, root)
	want := filepath.Join(base, "submodules", "ui_tars")
	fill(t, want)
	got, err := ResolveTool(root, "ui-tars")
	if err != nil || got != want {
		t.Fatalf("got %q err %v want %q", got, err, want)
	}
}

func TestResolveTool_EnvOverrideWins(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "submodules", "helix_qa")
	mk(t, root)
	fill(t, filepath.Join(base, "submodules", "ui_tars"))
	env := filepath.Join(base, "envtools")
	want := filepath.Join(env, "ui_tars")
	fill(t, want)
	t.Setenv("HELIXQA_TOOLS_DIR", env)
	got, err := ResolveTool(root, "ui-tars")
	if err != nil || got != want {
		t.Fatalf("got %q err %v want %q", got, err, want)
	}
}

func TestResolveTool_LegacyFallback(t *testing.T) {
	t.Setenv("HELIXQA_TOOLS_DIR", "")
	root := filepath.Join(t.TempDir(), "standalone", "helix_qa")
	want := filepath.Join(root, "tools", "opensource", "ui-tars")
	fill(t, want)
	got, err := ResolveTool(root, "ui-tars")
	if err != nil || got != want {
		t.Fatalf("got %q err %v want %q", got, err, want)
	}
}

func TestResolveTool_SiblingNotUsedOutsideSubmodulesDir(t *testing.T) {
	t.Setenv("HELIXQA_TOOLS_DIR", "")
	base := t.TempDir()
	root := filepath.Join(base, "other", "helix_qa")
	mk(t, root)
	fill(t, filepath.Join(base, "other", "ui_tars"))
	if _, err := ResolveTool(root, "ui-tars"); err == nil {
		t.Fatal("expected error: sibling lookup only applies inside a directory named submodules")
	}
}

func TestResolveTool_LoudErrorWhenMissing(t *testing.T) {
	t.Setenv("HELIXQA_TOOLS_DIR", "")
	root := filepath.Join(t.TempDir(), "submodules", "helix_qa")
	mk(t, root)
	_, err := ResolveTool(root, "nope-tool")
	if err == nil {
		t.Fatal("expected error")
	}
	for _, s := range []string{"nope-tool", "nope_tool", "tools/opensource", "HELIXQA_TOOLS_DIR"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error %q lacks %q", err, s)
		}
	}
}

func TestToolsRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HELIXQA_TOOLS_DIR", "")
	if got := ToolsRoot(root); got != filepath.Join(root, "tools", "opensource") {
		t.Errorf("legacy root: %q", got)
	}
	t.Setenv("HELIXQA_TOOLS_DIR", "/x/y")
	if got := ToolsRoot(root); got != "/x/y" {
		t.Errorf("env root: %q", got)
	}
}
