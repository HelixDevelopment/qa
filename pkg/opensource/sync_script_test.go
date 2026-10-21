// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package opensource

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "init.defaultBranch=main"}, args...)...)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// repoWithOrigin creates a clean git repo with an origin (local bare)
// so a `git fetch origin main` against it would write .git/FETCH_HEAD.
func repoWithOrigin(t *testing.T, dir, bare string) {
	t.Helper()
	mk(t, dir)
	git(t, dir, "init", "-q")
	os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644)
	git(t, dir, "add", "f")
	git(t, dir, "commit", "-q", "-m", "i")
	git(t, filepath.Dir(bare), "init", "-q", "--bare", bare)
	git(t, dir, "remote", "add", "origin", bare)
	git(t, dir, "push", "-q", "origin", "HEAD:main")
	git(t, dir, "branch", "-M", "main")
}

// B-1: the sync script must only touch manifest tools, never the
// consumer's first-party submodules that live beside helix_qa.
func TestSyncScript_OnlyTouchesManifestTools(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available SKIP-OK: #git-optional")
	}
	t.Setenv("HELIXQA_TOOLS_DIR", "")
	proj := t.TempDir()
	hq := filepath.Join(proj, "submodules", "helix_qa")
	mk(t, filepath.Join(hq, "scripts"))
	mk(t, filepath.Join(hq, "docs"))
	for _, f := range []string{"sync-opensource-references.sh", "resolve_tool.sh"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "scripts", f))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(hq, "scripts", f), b, 0o755)
	}
	os.WriteFile(filepath.Join(hq, "docs", "flat-tools-manifest.tsv"),
		[]byte("old_path\tnew_name\turl\tpinned_sha\ntools/opensource/scrcpy\tscrcpy\tu\tabc\n"), 0o644)
	bares := t.TempDir()
	repoWithOrigin(t, filepath.Join(proj, "submodules", "auth"), filepath.Join(bares, "auth.git"))
	repoWithOrigin(t, filepath.Join(proj, "submodules", "cache"), filepath.Join(bares, "cache.git"))
	repoWithOrigin(t, filepath.Join(hq, "tools", "opensource", "scrcpy"), filepath.Join(bares, "scrcpy.git"))

	out, err := exec.Command("bash", filepath.Join(hq, "scripts", "sync-opensource-references.sh"), "--fetch-only").CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	fetched := func(d string) bool {
		_, err := os.Stat(filepath.Join(d, ".git", "FETCH_HEAD"))
		return err == nil
	}
	if !fetched(filepath.Join(hq, "tools", "opensource", "scrcpy")) {
		t.Errorf("scrcpy (manifest tool) must be fetched\n%s", out)
	}
	for _, n := range []string{"auth", "cache"} {
		if fetched(filepath.Join(proj, "submodules", n)) {
			t.Errorf("consumer submodule %s must never be touched\n%s", n, out)
		}
	}
}
