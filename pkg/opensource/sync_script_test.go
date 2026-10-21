// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package opensource

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var pin0 = strings.Repeat("a", 40)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "init.defaultBranch=main"}, args...)...)
	c.Env = cleanGitEnv()
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func cleanGitEnv() []string {
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "GIT_") {
			env = append(env, e)
		}
	}
	return env
}

// fakeURL is the https URL a fixture advertises as origin; git is told
// (insteadOf) to fetch from the local bare repo, so the script sees a
// normal https URL while no network is touched.
func fakeURL(bare string) string { return "https://example.test" + bare }

func useOrigin(t *testing.T, dir, bare string) {
	t.Helper()
	git(t, dir, "config", "url."+bare+".insteadOf", fakeURL(bare))
	out, _ := exec.Command("git", "-C", dir, "config", "--get", "remote.origin.url").Output()
	if strings.TrimSpace(string(out)) == "" {
		git(t, dir, "remote", "add", "origin", fakeURL(bare))
	} else {
		git(t, dir, "remote", "set-url", "origin", fakeURL(bare))
	}
}

// repoWithOrigin creates a clean git repo (branch main) with a bare origin.
func repoWithOrigin(t *testing.T, dir, bare string) {
	t.Helper()
	mk(t, dir)
	git(t, dir, "init", "-q")
	os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644)
	git(t, dir, "add", "f")
	git(t, dir, "commit", "-q", "-m", "i")
	git(t, filepath.Dir(bare), "init", "-q", "--bare", bare)
	useOrigin(t, dir, bare)
	git(t, dir, "push", "-q", "origin", "HEAD:main")
	git(t, dir, "branch", "-M", "main")
}

func cloneTool(t *testing.T, bare, dir string) {
	t.Helper()
	mk(t, filepath.Dir(dir))
	git(t, filepath.Dir(dir), "clone", "-q", bare, dir)
	useOrigin(t, dir, bare)
}

func commitFile(t *testing.T, dir, name string) {
	t.Helper()
	os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644)
	git(t, dir, "add", name)
	git(t, dir, "commit", "-q", "-m", name)
}

func head(t *testing.T, dir string) string {
	t.Helper()
	c := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	c.Env = cleanGitEnv()
	out, err := c.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func mrow(name, url, sha string) string {
	return fmt.Sprintf("tools/opensource/%s\t%s\t%s\t%s\n", name, strings.ReplaceAll(name, "-", "_"), url, sha)
}

const mheader = "old_path\tnew_name\turl\tpinned_sha\n"

// installSync copies the scripts into hq and writes the manifest.
func installSync(t *testing.T, hq, content string) {
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
	os.WriteFile(filepath.Join(hq, "docs", "flat-tools-manifest.tsv"), []byte(content), 0o644)
}

func runSync(t *testing.T, hq string, env []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{filepath.Join(hq, "scripts", "sync-opensource-references.sh")}, args...)...)
	cmd.Env = append(cleanGitEnv(), env...)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available SKIP-OK: #git-optional")
	}
	t.Setenv("HELIXQA_TOOLS_DIR", "")
}

// ---- B-1: only manifest tools are touched ----

func TestSyncScript_OnlyTouchesManifestTools(t *testing.T) {
	needGit(t)
	proj := t.TempDir()
	hq := filepath.Join(proj, "submodules", "helix_qa")
	bares := t.TempDir()
	repoWithOrigin(t, filepath.Join(proj, "submodules", "auth"), filepath.Join(bares, "auth.git"))
	repoWithOrigin(t, filepath.Join(proj, "submodules", "cache"), filepath.Join(bares, "cache.git"))
	tool := filepath.Join(hq, "tools", "opensource", "scrcpy")
	repoWithOrigin(t, tool, filepath.Join(bares, "scrcpy.git"))
	installSync(t, hq, mheader+mrow("scrcpy", fakeURL(filepath.Join(bares, "scrcpy.git")), head(t, tool)))

	out, code := runSync(t, hq, nil, "--fetch-only")
	if code != 0 {
		t.Fatalf("script failed (%d)\n%s", code, out)
	}
	fetched := func(d string) bool {
		_, err := os.Stat(filepath.Join(d, ".git", "FETCH_HEAD"))
		return err == nil
	}
	if !fetched(tool) {
		t.Errorf("scrcpy (manifest tool) must be fetched\n%s", out)
	}
	for _, n := range []string{"auth", "cache"} {
		if fetched(filepath.Join(proj, "submodules", n)) {
			t.Errorf("consumer submodule %s must never be touched\n%s", n, out)
		}
	}
}

// behindConsumer builds a consumer clone BEHIND its origin (a pull would
// fast-forward it) with helix_qa installed inside.
func behindConsumer(t *testing.T) (consumer, hq, bares string) {
	t.Helper()
	bares = t.TempDir()
	bare := filepath.Join(bares, "consumer.git")
	seed := filepath.Join(bares, "seed")
	repoWithOrigin(t, seed, bare)
	consumer = filepath.Join(bares, "consumer")
	git(t, bares, "clone", "-q", bare, consumer)
	commitFile(t, seed, "g")
	git(t, seed, "push", "-q", "origin", "HEAD:main")
	hq = filepath.Join(consumer, "submodules", "helix_qa")
	return
}

func TestSyncScript_PullNeverTouchesEnclosingRepo(t *testing.T) {
	needGit(t)
	consumer, hq, _ := behindConsumer(t)
	installSync(t, hq, mheader+mrow("scrcpy", "git@example.org:x/scrcpy.git", pin0))
	fill(t, filepath.Join(consumer, "submodules", "scrcpy")) // plain dir, not a checkout
	before := head(t, consumer)
	out, code := runSync(t, hq, nil, "--pull")
	if after := head(t, consumer); after != before {
		t.Fatalf("consumer HEAD moved\n%s", out)
	}
	if !strings.Contains(out, "NOT-A-CHECKOUT") || code != 3 {
		t.Errorf("want NOT-A-CHECKOUT exit 3, got %d\n%s", code, out)
	}
}

func TestSyncScript_ExitCodesAndLegacyWarning(t *testing.T) {
	needGit(t)
	proj := t.TempDir()
	hq := filepath.Join(proj, "submodules", "helix_qa")
	sb := filepath.Join(t.TempDir(), "s.git")
	tool := filepath.Join(hq, "tools", "opensource", "scrcpy")
	repoWithOrigin(t, tool, sb)
	installSync(t, hq, mheader+mrow("scrcpy", fakeURL(sb), head(t, tool))+mrow("ghost", "https://example.org/g", pin0))
	out, code := runSync(t, hq, nil, "--fetch-only")
	if code != 3 || !strings.Contains(out, "NOT-FOUND") {
		t.Errorf("missing tool must exit 3 with NOT-FOUND, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "WARNING: legacy tools/opensource resolution in consumer layout") {
		t.Errorf("want legacy warning\n%s", out)
	}
	// all present, flat tier -> exit 0, no warning
	fb := filepath.Join(t.TempDir(), "f.git")
	flat := filepath.Join(proj, "submodules", "scrcpy")
	repoWithOrigin(t, flat, fb)
	installSync(t, hq, mheader+mrow("scrcpy", fakeURL(fb), head(t, flat)))
	out, code = runSync(t, hq, nil, "--fetch-only")
	if code != 0 || strings.Contains(out, "WARNING") {
		t.Errorf("clean flat run must exit 0 w/o warning, got %d\n%s", code, out)
	}
}

// ---- class 1 (symlinks) + class 2 (git env) + wrong repo ----

func TestSyncScript_PullNeverTouchesWrongRepo(t *testing.T) {
	needGit(t)
	toolURL := "git@github.com:Org/Allure2.git"

	cases := map[string]func(consumer, hq, bares string) (url string, guard string){
		"symlink-to-consumer-root": func(consumer, hq, bares string) (string, string) {
			mk(t, filepath.Join(hq, "tools", "opensource"))
			os.Symlink(consumer, filepath.Join(hq, "tools", "opensource", "allure2"))
			return toolURL, ""
		},
		"symlink-to-helix_qa": func(consumer, hq, bares string) (string, string) {
			mk(t, filepath.Join(hq, "tools", "opensource"))
			os.Symlink(hq, filepath.Join(hq, "tools", "opensource", "allure2"))
			return toolURL, ""
		},
		"first-party-submodule-name-collision": func(consumer, hq, bares string) (string, string) {
			fp := filepath.Join(consumer, "submodules", "allure2")
			git(t, bares, "clone", "-q", filepath.Join(bares, "consumer.git"), fp)
			git(t, fp, "reset", "-q", "--hard", "HEAD~1")
			return toolURL, fp
		},
		"wrong-remote": func(consumer, hq, bares string) (string, string) {
			repoWithOrigin(t, filepath.Join(hq, "tools", "opensource", "allure2"), filepath.Join(bares, "other.git"))
			return toolURL, ""
		},
		// class 1: symlinks to an out-of-tree clone that HAS the manifest origin
		"symlink-final-component-to-outoftree-clone": func(consumer, hq, bares string) (string, string) {
			out := filepath.Join(bares, "outside", "allure2")
			bare := filepath.Join(bares, "tool.git")
			repoWithOrigin(t, filepath.Join(bares, "toolseed"), bare)
			cloneTool(t, bare, out)
			mk(t, filepath.Join(hq, "tools", "opensource"))
			os.Symlink(out, filepath.Join(hq, "tools", "opensource", "allure2"))
			return fakeURL(bare), out
		},
		"symlink-parent-dir-legacy": func(consumer, hq, bares string) (string, string) {
			bare := filepath.Join(bares, "tool.git")
			repoWithOrigin(t, filepath.Join(bares, "toolseed"), bare)
			out := filepath.Join(bares, "outside", "opensource", "allure2")
			cloneTool(t, bare, out)
			mk(t, filepath.Join(hq, "tools"))
			os.Symlink(filepath.Join(bares, "outside", "opensource"), filepath.Join(hq, "tools", "opensource"))
			return fakeURL(bare), out
		},
		"symlink-flat-sibling": func(consumer, hq, bares string) (string, string) {
			bare := filepath.Join(bares, "tool.git")
			repoWithOrigin(t, filepath.Join(bares, "toolseed"), bare)
			out := filepath.Join(bares, "outside", "allure2")
			cloneTool(t, bare, out)
			mk(t, filepath.Join(consumer, "submodules"))
			os.Symlink(out, filepath.Join(consumer, "submodules", "allure2"))
			return fakeURL(bare), out
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			consumer, hq, bares := behindConsumer(t)
			url, guard := setup(consumer, hq, bares)
			installSync(t, hq, mheader+mrow("allure2", url, pin0))
			before := head(t, consumer)
			var gBefore string
			if guard != "" {
				// make the guarded repo BEHIND its origin so a pull would move it
				if strings.Contains(name, "outoftree") || strings.Contains(name, "symlink-") && guard != "" && !strings.Contains(name, "collision") {
					seed := filepath.Join(bares, "toolseed")
					commitFile(t, seed, "n")
					git(t, seed, "push", "-q", "origin", "HEAD:main")
				}
				gBefore = head(t, guard)
			}
			out, code := runSync(t, hq, nil, "--pull")
			if after := head(t, consumer); after != before {
				t.Fatalf("consumer HEAD moved\n%s", out)
			}
			if guard != "" && head(t, guard) != gBefore {
				t.Fatalf("guarded repo %s moved\n%s", guard, out)
			}
			if !strings.Contains(out, "WRONG-REPO") || code != 3 {
				t.Errorf("want WRONG-REPO and exit 3, got %d\n%s", code, out)
			}
		})
	}
}

func TestSyncScript_EnvToolSymlinkRejected(t *testing.T) {
	needGit(t)
	_, hq, bares := behindConsumer(t)
	bare := filepath.Join(bares, "tool.git")
	repoWithOrigin(t, filepath.Join(bares, "toolseed"), bare)
	out := filepath.Join(bares, "outside", "allure2")
	cloneTool(t, bare, out)
	envdir := filepath.Join(bares, "envtools")
	mk(t, envdir)
	os.Symlink(out, filepath.Join(envdir, "allure2"))
	installSync(t, hq, mheader+mrow("allure2", fakeURL(bare), pin0))
	o, code := runSync(t, hq, []string{"HELIXQA_TOOLS_DIR=" + envdir}, "--pull")
	if !strings.Contains(o, "WRONG-REPO") || code != 3 {
		t.Errorf("env symlink must be WRONG-REPO exit 3, got %d\n%s", code, o)
	}
}

// class 2: ambient git environment must never redirect git.
func TestSyncScript_GitEnvironmentIgnored(t *testing.T) {
	needGit(t)
	consumer, hq, bares := behindConsumer(t)
	bare := filepath.Join(bares, "tool.git")
	seed := filepath.Join(bares, "toolseed")
	repoWithOrigin(t, seed, bare)
	tool := filepath.Join(hq, "tools", "opensource", "allure2")
	cloneTool(t, bare, tool)
	commitFile(t, seed, "n")
	git(t, seed, "push", "-q", "origin", "HEAD:main")
	installSync(t, hq, mheader+mrow("allure2", fakeURL(bare), head(t, tool)))
	before := head(t, consumer)
	env := []string{
		"GIT_DIR=" + filepath.Join(consumer, ".git"),
		"GIT_WORK_TREE=" + consumer,
		"GIT_INDEX_FILE=" + filepath.Join(consumer, ".git", "index"),
		"GIT_CEILING_DIRECTORIES=" + consumer,
		"GIT_NAMESPACE=zz",
	}
	out, code := runSync(t, hq, env, "--pull")
	if head(t, consumer) != before {
		t.Fatalf("consumer HEAD moved\n%s", out)
	}
	if code != 0 || !strings.Contains(out, "PULLED") {
		t.Errorf("tool must be processed in its own repo: want PULLED exit 0, got %d\n%s", code, out)
	}
	if head(t, tool) != head(t, seed) {
		t.Errorf("tool not fast-forwarded to tip\n%s", out)
	}
}

func TestSyncScript_PullWorksForExpectedRemote(t *testing.T) {
	needGit(t)
	_, hq, bares := behindConsumer(t)
	bare := filepath.Join(bares, "tool.git")
	seed := filepath.Join(bares, "toolseed")
	repoWithOrigin(t, seed, bare)
	tool := filepath.Join(hq, "tools", "opensource", "allure2")
	cloneTool(t, bare, tool)
	pin := head(t, tool)
	commitFile(t, seed, "n")
	git(t, seed, "push", "-q", "origin", "HEAD:main")
	installSync(t, hq, mheader+mrow("allure2", fakeURL(bare), pin))
	out, code := runSync(t, hq, nil, "--pull")
	if code != 0 || !strings.Contains(out, "PULLED") || strings.Contains(out, "WRONG-REPO") {
		t.Fatalf("want PULLED exit 0, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "MANIFEST_PIN") || !strings.Contains(out, "DRIFT") {
		t.Errorf("header + DRIFT (checkout moved off pin)\n%s", out)
	}
}

func TestSyncScript_NoDriftWhenPinMatches(t *testing.T) {
	needGit(t)
	_, hq, bares := behindConsumer(t)
	bare := filepath.Join(bares, "tool.git")
	repoWithOrigin(t, filepath.Join(bares, "toolseed"), bare)
	tool := filepath.Join(hq, "tools", "opensource", "allure2")
	cloneTool(t, bare, tool)
	installSync(t, hq, mheader+mrow("allure2", fakeURL(bare), head(t, tool)))
	out, code := runSync(t, hq, nil, "--fetch-only")
	if code != 0 || strings.Contains(out, "DRIFT") {
		t.Fatalf("want exit 0 and no DRIFT, got %d\n%s", code, out)
	}
}

func TestSyncScript_UnreadableDirIsErrorRowNotAbort(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes SKIP-OK: #root-bypasses-perms")
	}
	needGit(t)
	proj := t.TempDir()
	hq := filepath.Join(proj, "submodules", "helix_qa")
	good := filepath.Join(t.TempDir(), "good.git")
	gt := filepath.Join(hq, "tools", "opensource", "good")
	repoWithOrigin(t, gt, good)
	locked := filepath.Join(hq, "tools", "opensource", "locked")
	fill(t, locked)
	os.Chmod(locked, 0o644) // listable but not enterable
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	installSync(t, hq, mheader+mrow("locked", "https://example.org/l", pin0)+mrow("good", fakeURL(good), head(t, gt)))
	out, code := runSync(t, hq, nil, "--fetch-only")
	if code != 3 || !strings.Contains(out, "ERROR") || !strings.Contains(out, "good") || !strings.Contains(out, "processed=2") {
		t.Errorf("want ERROR row, continue to good, exit 3; got %d\n%s", code, out)
	}
}

// ---- class 3: --pull safety states ----

type toolFx struct {
	hq, tool, seed, bare string
}

func newToolFx(t *testing.T) toolFx {
	t.Helper()
	needGit(t)
	proj := t.TempDir()
	b := t.TempDir()
	f := toolFx{hq: filepath.Join(proj, "submodules", "helix_qa"), bare: filepath.Join(b, "tool.git"), seed: filepath.Join(b, "seed")}
	repoWithOrigin(t, f.seed, f.bare)
	f.tool = filepath.Join(f.hq, "tools", "opensource", "allure2")
	cloneTool(t, f.bare, f.tool)
	return f
}

func (f toolFx) install(t *testing.T, pin string) {
	installSync(t, f.hq, mheader+mrow("allure2", fakeURL(f.bare), pin))
}

func (f toolFx) originAdvance(t *testing.T, name string) {
	commitFile(t, f.seed, name)
	git(t, f.seed, "push", "-q", "origin", "HEAD:main")
}

func TestSyncScript_PullStates(t *testing.T) {
	type tc struct {
		name  string
		setup func(t *testing.T, f toolFx)
		state string
		code  int
		moved bool // HEAD must equal the origin tip afterwards
	}
	cases := []tc{
		{"current", func(t *testing.T, f toolFx) {}, "current", 0, false},
		{"ahead", func(t *testing.T, f toolFx) { commitFile(t, f.tool, "local") }, "AHEAD", 3, false},
		{"diverged", func(t *testing.T, f toolFx) { commitFile(t, f.tool, "local"); f.originAdvance(t, "up") }, "DIVERGED", 3, false},
		{"detached-local-commits", func(t *testing.T, f toolFx) {
			git(t, f.tool, "checkout", "-q", "--detach")
			commitFile(t, f.tool, "local")
		}, "DETACHED-LOCAL-COMMITS", 3, false},
		{"detached-diverged", func(t *testing.T, f toolFx) {
			git(t, f.tool, "checkout", "-q", "--detach")
			commitFile(t, f.tool, "local")
			f.originAdvance(t, "up")
		}, "DETACHED-LOCAL-COMMITS", 3, false},
		{"detached-behind-fast-forwards", func(t *testing.T, f toolFx) {
			git(t, f.tool, "checkout", "-q", "--detach")
			f.originAdvance(t, "up")
		}, "PULLED", 0, true},
		{"behind", func(t *testing.T, f toolFx) { f.originAdvance(t, "up") }, "PULLED", 0, true},
		{"dirty-untracked", func(t *testing.T, f toolFx) {
			os.WriteFile(filepath.Join(f.tool, "scratch"), []byte("u"), 0o644)
			f.originAdvance(t, "up")
		}, "SKIP-DIRTY", 0, false},
		{"dirty-tracked", func(t *testing.T, f toolFx) {
			os.WriteFile(filepath.Join(f.tool, "f"), []byte("changed"), 0o644)
			f.originAdvance(t, "up")
		}, "SKIP-DIRTY", 0, false},
		{"status-failure", func(t *testing.T, f toolFx) {
			os.WriteFile(filepath.Join(f.tool, ".git", "index"), []byte("garbage-not-an-index"), 0o644)
		}, "ERROR", 3, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newToolFx(t)
			f.install(t, head(t, f.tool))
			c.setup(t, f)
			before := head(t, f.tool)
			out, code := runSync(t, f.hq, nil, "--pull")
			after := head(t, f.tool)
			if !strings.Contains(out, c.state) || code != c.code {
				t.Errorf("want state %q exit %d, got exit %d\n%s", c.state, c.code, code, out)
			}
			if c.moved {
				if after != head(t, f.seed) {
					t.Errorf("HEAD must equal tip after ff\n%s", out)
				}
			} else if after != before {
				t.Errorf("HEAD must not move (before %s after %s)\n%s", before, after, out)
			}
			if strings.HasPrefix(c.name, "detached-local") || c.name == "detached-diverged" {
				if err := exec.Command("git", "-C", f.tool, "cat-file", "-e", before).Run(); err != nil {
					t.Errorf("local commit must stay reachable")
				}
			}
			if strings.HasPrefix(c.name, "dirty") && !strings.Contains(out, "skip-dirty=1") {
				t.Errorf("summary must count skip-dirty\n%s", out)
			}
		})
	}
}

func TestSyncScript_BehindLabelOnlyForStrictAncestor(t *testing.T) {
	f := newToolFx(t)
	f.install(t, head(t, f.tool))
	commitFile(t, f.tool, "local")
	f.originAdvance(t, "up")
	out, _ := runSync(t, f.hq, nil, "--fetch-only")
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "allure2") && strings.Contains(l, "BEHIND") {
			t.Errorf("diverged must not be labelled BEHIND\n%s", out)
		}
	}
	if !strings.Contains(out, "DIVERGED") {
		t.Errorf("want DIVERGED\n%s", out)
	}
}

// ---- class 4: manifest parsing ----

func TestSyncScript_ManifestParsing(t *testing.T) {
	good := func(f toolFx) string { return mrow("allure2", fakeURL(f.bare), head(t, f.tool)) }
	type tc struct {
		name    string
		content func(f toolFx) string
		code    int
		want    []string
		notWant []string
	}
	cases := []tc{
		{"no-trailing-newline", func(f toolFx) string { return strings.TrimRight(mheader+good(f), "\n") }, 0, []string{"rows=1 processed=1", "current"}, nil},
		{"crlf", func(f toolFx) string {
			return strings.ReplaceAll(mheader+good(f), "\n", "\r\n")
		}, 0, []string{"rows=1 processed=1", "current"}, []string{"INVALID-ROW"}},
		{"empty-url", func(f toolFx) string { return mheader + mrow("allure2", "", pin0) }, 3, []string{"INVALID-ROW", "rows=1 processed=1"}, nil},
		{"empty-sha-column", func(f toolFx) string { return mheader + mrow("allure2", fakeURL(f.bare), "") }, 3, []string{"INVALID-ROW"}, nil},
		{"empty-middle-column", func(f toolFx) string {
			return mheader + "tools/opensource/allure2\t\t" + fakeURL(f.bare) + "\t" + pin0 + "\n"
		}, 3, []string{"INVALID-ROW"}, nil},
		{"name-starting-with-dash", func(f toolFx) string {
			return mheader + "tools/opensource/-rf\t-rf\t" + fakeURL(f.bare) + "\t" + pin0 + "\n"
		}, 3, []string{"INVALID-ROW"}, nil},
		{"new-name-bad-chars", func(f toolFx) string {
			return mheader + "tools/opensource/allure2\tAllure-2\t" + fakeURL(f.bare) + "\t" + pin0 + "\n"
		}, 3, []string{"INVALID-ROW"}, nil},
		{"short-sha", func(f toolFx) string { return mheader + mrow("allure2", fakeURL(f.bare), "abc") }, 3, []string{"INVALID-ROW"}, nil},
		{"extra-column", func(f toolFx) string {
			return mheader + strings.TrimRight(good(f), "\n") + "\textra\n"
		}, 3, []string{"INVALID-ROW"}, nil},
		{"blank-lines-ignored", func(f toolFx) string { return mheader + "\n" + good(f) + "\n" }, 0, []string{"rows=1 processed=1"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newToolFx(t)
			installSync(t, f.hq, c.content(f))
			out, code := runSync(t, f.hq, nil, "--fetch-only")
			if code != c.code {
				t.Errorf("want exit %d got %d\n%s", c.code, code, out)
			}
			for _, w := range c.want {
				if !strings.Contains(out, w) {
					t.Errorf("missing %q\n%s", w, out)
				}
			}
			for _, w := range c.notWant {
				if strings.Contains(out, w) {
					t.Errorf("unexpected %q\n%s", w, out)
				}
			}
		})
	}
}

// ---- class 5: URL normalisation ----

func TestSyncScript_URLNormalisation(t *testing.T) {
	needGit(t)
	cases := []struct {
		origin, manifest string
		wrong            bool
	}{
		// equal
		{"https://GitHub.com/Org/Tool.git", "git@github.com:Org/Tool.git", false},
		{"ssh://git@github.com:22/Org/tool", "https://github.com/Org/tool.git/", false},
		{"github.com:Org/tool.git", "git@github.com:Org/tool.git", false}, // user-less scp
		{"ssh://git@github.com/o/t", "https://user@github.com/o/t", false},
		{"ssh://git@host:2222/o/t.git", "ssh://host:2222/o/t", false},
		{"https://host:443/o/t", "https://host/o/t", false},
		// unequal
		{"git@github.com:org/tool.git", "git@github.com:org/other.git", true},
		{"https://github.com/org/tool", "https://gitlab.com/org/tool", true},
		{"https://github.com/Org/Tool", "https://github.com/org/tool", true}, // path is case-sensitive
		{"ssh://git@host:2222/o/t", "ssh://git@host:2223/o/t", true},         // non-default port kept
		{"ssh://git@host:2222/o/t", "ssh://git@host/o/t", true},
		{"https://host:8443/o/t", "https://host/o/t", true},
		{"git://github.com/o/t.git", "git://github.com/o/t.git", true},    // git:// refused
		{"http://github.com/o/t.git", "http://github.com/o/t.git", true},  // http:// refused
		{"http://github.com/o/t.git", "https://github.com/o/t.git", true}, // never equal across http
		{"file:///srv/x.git", "file:///srv/x.git", true},                  // file:// refused
		{"/srv/x.git", "/srv/x.git", true},                                // bare local path refused
	}
	for i, c := range cases {
		proj := t.TempDir()
		hq := filepath.Join(proj, "submodules", "helix_qa")
		d := filepath.Join(hq, "tools", "opensource", "tool")
		bare := filepath.Join(t.TempDir(), "t.git")
		repoWithOrigin(t, d, bare)
		git(t, d, "remote", "set-url", "origin", c.origin)
		git(t, d, "config", "url."+bare+".insteadOf", c.origin) // keep fetches local
		installSync(t, hq, mheader+mrow("tool", c.manifest, head(t, d)))
		out, _ := runSync(t, hq, nil, "--fetch-only")
		if got := strings.Contains(out, "WRONG-REPO"); got != c.wrong {
			t.Errorf("#%d origin=%q manifest=%q wrong=%v want %v\n%s", i, c.origin, c.manifest, got, c.wrong, out)
		}
	}
}
