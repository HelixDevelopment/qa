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
	bares := t.TempDir()
	os.WriteFile(filepath.Join(hq, "docs", "flat-tools-manifest.tsv"),
		[]byte("old_path\tnew_name\turl\tpinned_sha\ntools/opensource/scrcpy\tscrcpy\t"+filepath.Join(bares, "scrcpy.git")+"\tabc\n"), 0o644)
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
	installSync(t, hq, "tools/opensource/scrcpy\tscrcpy\tgit@example.org:x/scrcpy.git\tabc\n")
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
	sb := filepath.Join(t.TempDir(), "s.git")
	installSync(t, hq, "tools/opensource/scrcpy\tscrcpy\t"+sb+"\tabc\ntools/opensource/ghost\tghost\tu\tdef\n")
	repoWithOrigin(t, filepath.Join(hq, "tools", "opensource", "scrcpy"), sb)
	out, code := runSync(t, hq, nil, "--fetch-only")
	if code != 3 || !strings.Contains(out, "NOT-FOUND") {
		t.Errorf("missing tool must exit 3 with NOT-FOUND, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "WARNING: legacy tools/opensource resolution in consumer layout") {
		t.Errorf("want legacy warning\n%s", out)
	}
	// all present, flat tier -> exit 0, no warning
	fb := filepath.Join(t.TempDir(), "f.git")
	installSync(t, hq, "tools/opensource/scrcpy\tscrcpy\t"+fb+"\tabc\n")
	repoWithOrigin(t, filepath.Join(proj, "submodules", "scrcpy"), fb)
	out, code = runSync(t, hq, nil, "--fetch-only")
	if code != 0 || strings.Contains(out, "WARNING") {
		t.Errorf("clean flat run must exit 0 w/o warning, got %d\n%s", code, out)
	}
}

// behindConsumer builds a consumer clone that is BEHIND its origin (a
// pull would fast-forward it) with helix_qa installed inside.
func behindConsumer(t *testing.T, manifestRows string) (consumer, hq, bares string) {
	t.Helper()
	bares = t.TempDir()
	bare := filepath.Join(bares, "consumer.git")
	seed := filepath.Join(bares, "seed")
	repoWithOrigin(t, seed, bare)
	consumer = filepath.Join(bares, "consumer")
	git(t, bares, "clone", "-q", bare, consumer)
	os.WriteFile(filepath.Join(seed, "g"), []byte("y"), 0o644)
	git(t, seed, "add", "g")
	git(t, seed, "commit", "-q", "-m", "second")
	git(t, seed, "push", "-q", "origin", "HEAD:main")
	hq = filepath.Join(consumer, "submodules", "helix_qa")
	installSync(t, hq, manifestRows)
	return
}

func TestSyncScript_PullNeverTouchesWrongRepo(t *testing.T) {
	t.Setenv("HELIXQA_TOOLS_DIR", "")
	toolURL := "git@github.com:Org/Allure2.git"
	row := "tools/opensource/allure2\tallure2\t" + toolURL + "\tabc\n"

	cases := map[string]func(consumer, hq, bares string){
		"symlink-to-consumer-root": func(consumer, hq, bares string) {
			mk(t, filepath.Join(hq, "tools", "opensource"))
			os.Symlink(consumer, filepath.Join(hq, "tools", "opensource", "allure2"))
		},
		"symlink-to-helix_qa": func(consumer, hq, bares string) {
			mk(t, filepath.Join(hq, "tools", "opensource"))
			os.Symlink(hq, filepath.Join(hq, "tools", "opensource", "allure2"))
		},
		"first-party-submodule-name-collision": func(consumer, hq, bares string) {
			// a first-party consumer submodule that happens to be named like the tool
			fp := filepath.Join(consumer, "submodules", "allure2")
			git(t, bares, "clone", "-q", filepath.Join(bares, "consumer.git"), fp)
		},
		"wrong-remote": func(consumer, hq, bares string) {
			d := filepath.Join(hq, "tools", "opensource", "allure2")
			repoWithOrigin(t, d, filepath.Join(bares, "other.git"))
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			consumer, hq, bares := behindConsumer(t, row)
			setup(consumer, hq, bares)
			before := head(t, consumer)
			var fpBefore string
			fp := filepath.Join(consumer, "submodules", "allure2")
			if name == "first-party-submodule-name-collision" {
				fpBefore = head(t, fp)
				git(t, fp, "reset", "-q", "--hard", "HEAD~1")
				fpBefore = head(t, fp)
			}
			out, code := runSync(t, hq, nil, "--pull")
			if after := head(t, consumer); after != before {
				t.Fatalf("consumer HEAD moved\n%s", out)
			}
			if fpBefore != "" && head(t, fp) != fpBefore {
				t.Fatalf("first-party submodule moved\n%s", out)
			}
			if !strings.Contains(out, "WRONG-REPO") || code != 3 {
				t.Errorf("want WRONG-REPO and exit 3, got %d\n%s", code, out)
			}
		})
	}
}

func TestSyncScript_PullWorksForExpectedRemoteAndEquivalentURL(t *testing.T) {
	t.Setenv("HELIXQA_TOOLS_DIR", "")
	_, hq, bares := behindConsumer(t, "x\tx\tx\tx\n")
	// tool whose origin is a local bare; manifest lists ssh-vs-plain equivalent forms
	bare := filepath.Join(bares, "tool.git")
	seed := filepath.Join(bares, "toolseed")
	repoWithOrigin(t, seed, bare)
	tool := filepath.Join(hq, "tools", "opensource", "allure2")
	mk(t, filepath.Dir(tool))
	git(t, bares, "clone", "-q", bare, tool)
	os.WriteFile(filepath.Join(seed, "n"), []byte("n"), 0o644)
	git(t, seed, "add", "n")
	git(t, seed, "commit", "-q", "-m", "n")
	git(t, seed, "push", "-q", "origin", "HEAD:main")
	pin := strings.TrimSpace(head(t, tool))
	installSync(t, hq, "tools/opensource/allure2\tallure2\t"+bare+"\t"+pin+"\n")
	out, code := runSync(t, hq, nil, "--pull")
	if code != 0 || !strings.Contains(out, "PULLED") {
		t.Fatalf("want PULLED exit 0, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "MANIFEST_PIN") || strings.Contains(out, "WRONG-REPO") {
		t.Errorf("header/WRONG-REPO\n%s", out)
	}
	// manifest pin is the OLD head, checkout moved -> DRIFT shown
	if !strings.Contains(out, "DRIFT") {
		t.Errorf("checkout HEAD moved away from manifest pin: want DRIFT\n%s", out)
	}
}

func TestSyncScript_NoDriftWhenPinMatches(t *testing.T) {
	t.Setenv("HELIXQA_TOOLS_DIR", "")
	_, hq, bares := behindConsumer(t, "x\tx\tx\tx\n")
	bare := filepath.Join(bares, "tool.git")
	repoWithOrigin(t, filepath.Join(bares, "toolseed"), bare)
	tool := filepath.Join(hq, "tools", "opensource", "allure2")
	mk(t, filepath.Dir(tool))
	git(t, bares, "clone", "-q", bare, tool)
	installSync(t, hq, "tools/opensource/allure2\tallure2\t"+bare+"\t"+strings.TrimSpace(head(t, tool))+"\n")
	out, code := runSync(t, hq, nil, "--fetch-only")
	if code != 0 || strings.Contains(out, "DRIFT") {
		t.Fatalf("want exit 0 and no DRIFT, got %d\n%s", code, out)
	}
}

func TestSyncScript_UnreadableDirIsErrorRowNotAbort(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes SKIP-OK: #root-bypasses-perms")
	}
	t.Setenv("HELIXQA_TOOLS_DIR", "")
	proj := t.TempDir()
	hq := filepath.Join(proj, "submodules", "helix_qa")
	bares := t.TempDir()
	good := filepath.Join(bares, "good.git")
	installSync(t, hq, "tools/opensource/locked\tlocked\tu\tabc\ntools/opensource/good\tgood\t"+good+"\tabc\n")
	locked := filepath.Join(hq, "tools", "opensource", "locked")
	fill(t, locked)
	os.Chmod(locked, 0o644) // listable but not enterable
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	repoWithOrigin(t, filepath.Join(hq, "tools", "opensource", "good"), good)
	out, code := runSync(t, hq, nil, "--fetch-only")
	if code != 3 || !strings.Contains(out, "ERROR") || !strings.Contains(out, "good") {
		t.Errorf("want ERROR row for locked, continue to good, exit 3; got %d\n%s", code, out)
	}
}

// ssh/https/scp spellings of the same host/path must compare equal (no
// WRONG-REPO); a different path must not.
func TestSyncScript_URLNormalisation(t *testing.T) {
	t.Setenv("HELIXQA_TOOLS_DIR", "")
	cases := []struct {
		origin, manifest string
		wrong            bool
	}{
		{"https://GitHub.com/Org/Tool.git", "git@github.com:org/tool.git", false},
		{"ssh://git@github.com:22/org/tool", "https://github.com/org/tool.git/", false},
		{"git@github.com:org/tool.git", "git@github.com:org/other.git", true},
		{"https://github.com/org/tool", "https://gitlab.com/org/tool", true},
	}
	for _, c := range cases {
		proj := t.TempDir()
		hq := filepath.Join(proj, "submodules", "helix_qa")
		installSync(t, hq, "tools/opensource/tool\ttool\t"+c.manifest+"\tabc\n")
		d := filepath.Join(hq, "tools", "opensource", "tool")
		repoWithOrigin(t, d, filepath.Join(t.TempDir(), "t.git"))
		git(t, d, "remote", "set-url", "origin", c.origin)
		out, _ := runSync(t, hq, nil, "--fetch-only")
		if got := strings.Contains(out, "WRONG-REPO"); got != c.wrong {
			t.Errorf("origin=%q manifest=%q wrong=%v want %v\n%s", c.origin, c.manifest, got, c.wrong, out)
		}
	}
}
