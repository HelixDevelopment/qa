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

func runSync(t *testing.T, hq string, env []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{filepath.Join(hq, "scripts", "sync-opensource-references.sh")}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

func installSync(t *testing.T, hq, manifestRows string) {
	t.Helper()
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
		[]byte("old_path\tnew_name\turl\tpinned_sha\n"+manifestRows), 0o644)
}

func head(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// A populated plain directory (not its own checkout) inside a consumer
// repo must never make git act on the ENCLOSING consumer repo.
func TestSyncScript_PullNeverTouchesEnclosingRepo(t *testing.T) {
	t.Setenv("HELIXQA_TOOLS_DIR", "")
	bares := t.TempDir()
	bare := filepath.Join(bares, "consumer.git")
	seed := filepath.Join(bares, "seed")
	repoWithOrigin(t, seed, bare)
	consumer := filepath.Join(bares, "consumer")
	git(t, bares, "clone", "-q", bare, consumer)
	// advance origin so the consumer is BEHIND (a pull would fast-forward it)
	os.WriteFile(filepath.Join(seed, "g"), []byte("y"), 0o644)
	git(t, seed, "add", "g")
	git(t, seed, "commit", "-q", "-m", "second")
	git(t, seed, "push", "-q", "origin", "HEAD:main")

	hq := filepath.Join(consumer, "submodules", "helix_qa")
	installSync(t, hq, "tools/opensource/scrcpy\tscrcpy\tu\tabc\n")
	fill(t, filepath.Join(consumer, "submodules", "scrcpy")) // plain dir, not a checkout
	before := head(t, consumer)

	out, code := runSync(t, hq, nil, "--pull")
	if after := head(t, consumer); after != before {
		t.Fatalf("consumer HEAD moved %s -> %s\n%s", before, after, out)
	}
	if !strings.Contains(out, "NOT-A-CHECKOUT") {
		t.Errorf("want NOT-A-CHECKOUT\n%s", out)
	}
	if code != 3 {
		t.Errorf("want exit 3, got %d\n%s", code, out)
	}
}

func TestSyncScript_ExitCodesAndLegacyWarning(t *testing.T) {
	t.Setenv("HELIXQA_TOOLS_DIR", "")
	proj := t.TempDir()
	hq := filepath.Join(proj, "submodules", "helix_qa")
	installSync(t, hq, "tools/opensource/scrcpy\tscrcpy\tu\tabc\ntools/opensource/ghost\tghost\tu\tdef\n")
	repoWithOrigin(t, filepath.Join(hq, "tools", "opensource", "scrcpy"), filepath.Join(t.TempDir(), "s.git"))
	out, code := runSync(t, hq, nil, "--fetch-only")
	if code != 3 || !strings.Contains(out, "NOT-FOUND") {
		t.Errorf("missing tool must exit 3 with NOT-FOUND, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "WARNING: legacy tools/opensource resolution in consumer layout") {
		t.Errorf("want legacy warning\n%s", out)
	}
	// all present, flat tier -> exit 0, no warning
	installSync(t, hq, "tools/opensource/scrcpy\tscrcpy\tu\tabc\n")
	repoWithOrigin(t, filepath.Join(proj, "submodules", "scrcpy"), filepath.Join(t.TempDir(), "f.git"))
	out, code = runSync(t, hq, nil, "--fetch-only")
	if code != 0 || strings.Contains(out, "WARNING") {
		t.Errorf("clean flat run must exit 0 w/o warning, got %d\n%s", code, out)
	}
}
