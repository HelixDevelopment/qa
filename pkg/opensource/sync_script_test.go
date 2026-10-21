// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package opensource

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// fakeURL is the https URL a fixture advertises as origin. The script is
// read-only and never contacts a remote, so the URL need not be reachable.
func fakeURL(bare string) string { return "https://example.test" + bare }

// useOrigin points origin at the fake https URL (no insteadOf: the script
// is read-only and never contacts the remote).
func useOrigin(t *testing.T, dir, bare string) {
	t.Helper()
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
	git(t, dir, "push", "-q", bare, "HEAD:main")
	git(t, dir, "branch", "-M", "main")
	useOrigin(t, dir, bare)
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

// snap records every file under dir (including .git) with mode, size and
// mtime so a test can prove a run changed nothing.
func snap(t *testing.T, dir string) map[string]string {
	t.Helper()
	m := map[string]string{}
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		m[p] = fmt.Sprintf("%v|%d|%d", info.Mode(), info.Size(), info.ModTime().UnixNano())
		return nil
	})
	return m
}

func sameSnap(t *testing.T, label string, a, b map[string]string) {
	t.Helper()
	for k, v := range a {
		if b[k] != v {
			t.Errorf("%s: %s changed (%q -> %q)", label, k, v, b[k])
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			t.Errorf("%s: new path %s", label, k)
		}
	}
}

func rowFor(out, name string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, name+" ") {
			return l
		}
	}
	return ""
}

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

// ---- read-only contract ----

func TestSyncScript_OnlyReadsManifestToolsAndChangesNothing(t *testing.T) {
	needGit(t)
	proj := t.TempDir()
	hq := filepath.Join(proj, "submodules", "helix_qa")
	bares := t.TempDir()
	repoWithOrigin(t, filepath.Join(proj, "submodules", "auth"), filepath.Join(bares, "auth.git"))
	repoWithOrigin(t, filepath.Join(proj, "submodules", "cache"), filepath.Join(bares, "cache.git"))
	tool := filepath.Join(hq, "tools", "opensource", "scrcpy")
	repoWithOrigin(t, tool, filepath.Join(bares, "scrcpy.git"))
	installSync(t, hq, mheader+mrow("scrcpy", fakeURL(filepath.Join(bares, "scrcpy.git")), head(t, tool)))
	// make the tool dirty and its remote unreachable: still only read.
	os.WriteFile(filepath.Join(tool, "untracked"), []byte("u"), 0o644)
	before := snap(t, proj)
	out, code := runSync(t, hq, nil)
	if code != 0 {
		t.Fatalf("script failed (%d)\n%s", code, out)
	}
	sameSnap(t, "whole project tree", before, snap(t, proj))
	for _, d := range []string{tool, filepath.Join(proj, "submodules", "auth"), filepath.Join(proj, "submodules", "cache")} {
		if _, err := os.Stat(filepath.Join(d, ".git", "FETCH_HEAD")); err == nil {
			t.Errorf("FETCH_HEAD created in %s: script must not fetch", d)
		}
	}
	if r := rowFor(out, "scrcpy"); !strings.Contains(r, "legacy") || !strings.Contains(r, "ok") {
		t.Errorf("want scrcpy row with tier legacy and state ok, got %q\n%s", r, out)
	}
	if rowFor(out, "auth") != "" || rowFor(out, "cache") != "" {
		t.Errorf("consumer submodules must not be listed\n%s", out)
	}
}

func TestSyncScript_RejectsUnknownArguments(t *testing.T) {
	f := newToolFx(t)
	f.install(t, head(t, f.tool))
	for _, a := range []string{"--pull", "--fetch-only", "--bogus", "x"} {
		before := snap(t, f.tool)
		out, code := runSync(t, f.hq, nil, a)
		if code != 2 {
			t.Errorf("%s: want exit 2, got %d\n%s", a, code, out)
		}
		sameSnap(t, a, before, snap(t, f.tool))
	}
}

func TestSyncScript_NeverRunsHooksFsmonitorOrNetwork(t *testing.T) {
	f := newToolFx(t)
	f.install(t, head(t, f.tool))
	marker := filepath.Join(t.TempDir(), "marker")
	hooks := filepath.Join(t.TempDir(), "hooks")
	mk(t, hooks)
	evil := "#!/bin/sh\ntouch " + marker + "\n"
	for _, h := range []string{"post-checkout", "post-merge", "reference-transaction", "pre-auto-gc", "fsmonitor-watchman"} {
		os.WriteFile(filepath.Join(hooks, h), []byte(evil), 0o755)
		os.WriteFile(filepath.Join(f.tool, ".git", "hooks", h), []byte(evil), 0o755)
	}
	fsm := filepath.Join(t.TempDir(), "fsm.sh")
	os.WriteFile(fsm, []byte(evil), 0o755)
	git(t, f.tool, "config", "core.fsmonitor", fsm)
	git(t, f.tool, "config", "core.hooksPath", hooks)
	git(t, f.tool, "config", "core.sshCommand", "touch "+marker+" #")
	git(t, f.tool, "remote", "set-url", "origin", "ssh://git@example.test"+f.bare)
	installSync(t, f.hq, mheader+mrow("allure2", "ssh://git@example.test"+f.bare, head(t, f.tool)))
	before := snap(t, f.tool)
	out, code := runSync(t, f.hq, []string{"GIT_SSH_COMMAND=touch " + marker + " #"})
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("a hook / fsmonitor / ssh command ran\n%s", out)
	}
	if code != 0 {
		t.Errorf("want exit 0, got %d\n%s", code, out)
	}
	sameSnap(t, "tool repo", before, snap(t, f.tool))
}

func TestSyncScript_EvilOriginConfigIsWrongRepo(t *testing.T) {
	cases := map[string]func(t *testing.T, f toolFx){
		"tool-local-insteadOf": func(t *testing.T, f toolFx) {
			git(t, f.tool, "config", "url.https://example.test/evil/.insteadOf", "https://example.test/")
		},
		"tool-local-pushInsteadOf": func(t *testing.T, f toolFx) {
			git(t, f.tool, "config", "url.https://example.test/evil/.pushInsteadOf", "https://example.test/")
		},
		"multi-value-origin-url": func(t *testing.T, f toolFx) {
			git(t, f.tool, "config", "--add", "remote.origin.url", "https://example.test/other/tool")
		},
		"ext-transport": func(t *testing.T, f toolFx) {
			git(t, f.tool, "remote", "set-url", "origin", "ext::sh -c 'touch /tmp/x'")
		},
		"no-origin": func(t *testing.T, f toolFx) { git(t, f.tool, "remote", "remove", "origin") },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			f := newToolFx(t)
			f.install(t, head(t, f.tool))
			setup(t, f)
			before := snap(t, f.tool)
			out, code := runSync(t, f.hq, nil)
			if !strings.Contains(out, "WRONG-REPO") || code != 3 {
				t.Errorf("want WRONG-REPO exit 3, got %d\n%s", code, out)
			}
			sameSnap(t, name, before, snap(t, f.tool))
		})
	}
}

func TestSyncScript_EffectiveURLAppliesGlobalInsteadOf(t *testing.T) {
	// a USER-level insteadOf (their own config) rewrites the origin; the
	// compared URL is the effective one (git ls-remote --get-url).
	f := newToolFx(t)
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[url \"git@example.test:\"]\n\tinsteadOf = https://example.test/\n"), 0o644)
	git(t, f.tool, "remote", "set-url", "origin", "https://example.test/o/t")
	installSync(t, f.hq, mheader+mrow("allure2", "git@example.test:o/t.git", head(t, f.tool)))
	out, code := runSync(t, f.hq, []string{"HOME=" + home, "XDG_CONFIG_HOME=" + home})
	if code != 0 || strings.Contains(out, "WRONG-REPO") {
		t.Errorf("effective URL should match via user insteadOf: %d\n%s", code, out)
	}
}

func TestSyncScript_OnlyAcceptsOwnCheckout(t *testing.T) {
	needGit(t)
	consumer, hq, _ := behindConsumer(t)
	installSync(t, hq, mheader+mrow("scrcpy", "git@example.org:x/scrcpy.git", pin0))
	fill(t, filepath.Join(consumer, "submodules", "scrcpy")) // plain dir, not a checkout
	before := snap(t, consumer)
	out, code := runSync(t, hq, nil)
	sameSnap(t, "consumer", before, snap(t, consumer))
	if !strings.Contains(out, "NOT-A-CHECKOUT") || code != 3 {
		t.Errorf("want NOT-A-CHECKOUT exit 3, got %d\n%s", code, out)
	}
}

// behindConsumer builds a consumer clone with helix_qa installed inside.
func behindConsumer(t *testing.T) (consumer, hq, bares string) {
	t.Helper()
	bares = t.TempDir()
	bare := filepath.Join(bares, "consumer.git")
	seed := filepath.Join(bares, "seed")
	repoWithOrigin(t, seed, bare)
	consumer = filepath.Join(bares, "consumer")
	git(t, bares, "clone", "-q", bare, consumer)
	commitFile(t, seed, "g")
	git(t, seed, "push", "-q", bare, "HEAD:main")
	hq = filepath.Join(consumer, "submodules", "helix_qa")
	return
}

func TestSyncScript_ExitCodesAndLegacyWarning(t *testing.T) {
	needGit(t)
	proj := t.TempDir()
	hq := filepath.Join(proj, "submodules", "helix_qa")
	sb := filepath.Join(t.TempDir(), "s.git")
	tool := filepath.Join(hq, "tools", "opensource", "scrcpy")
	repoWithOrigin(t, tool, sb)
	installSync(t, hq, mheader+mrow("scrcpy", fakeURL(sb), head(t, tool))+mrow("ghost", "https://example.org/g", pin0))
	out, code := runSync(t, hq, nil)
	if code != 3 || !strings.Contains(out, "NOT-FOUND") {
		t.Errorf("missing tool must exit 3 with NOT-FOUND, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "WARNING: legacy tools/opensource resolution in consumer layout") {
		t.Errorf("want legacy warning\n%s", out)
	}
	fb := filepath.Join(t.TempDir(), "f.git")
	flat := filepath.Join(proj, "submodules", "scrcpy")
	repoWithOrigin(t, flat, fb)
	installSync(t, hq, mheader+mrow("scrcpy", fakeURL(fb), head(t, flat)))
	out, code = runSync(t, hq, nil)
	if code != 0 || strings.Contains(out, "WARNING") || !strings.Contains(rowFor(out, "scrcpy"), "flat") {
		t.Errorf("clean flat run must exit 0 w/o warning and show tier flat, got %d\n%s", code, out)
	}
}

// ---- symlinks / containment / wrong repo ----

func TestSyncScript_WrongRepoCases(t *testing.T) {
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
			return toolURL, fp
		},
		"wrong-remote": func(consumer, hq, bares string) (string, string) {
			repoWithOrigin(t, filepath.Join(hq, "tools", "opensource", "allure2"), filepath.Join(bares, "other.git"))
			return toolURL, ""
		},
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
			cBefore := snap(t, consumer)
			var gBefore map[string]string
			if guard != "" {
				gBefore = snap(t, guard)
			}
			out, code := runSync(t, hq, nil)
			if !strings.Contains(out, "WRONG-REPO") || code != 3 {
				t.Errorf("want WRONG-REPO and exit 3, got %d\n%s", code, out)
			}
			sameSnap(t, "consumer", cBefore, snap(t, consumer))
			if guard != "" {
				sameSnap(t, "guarded repo", gBefore, snap(t, guard))
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
	o, code := runSync(t, hq, []string{"HELIXQA_TOOLS_DIR=" + envdir})
	if !strings.Contains(o, "WRONG-REPO") || code != 3 {
		t.Errorf("env symlink must be WRONG-REPO exit 3, got %d\n%s", code, o)
	}
}

func TestSyncScript_EnvTierReported(t *testing.T) {
	needGit(t)
	_, hq, bares := behindConsumer(t)
	bare := filepath.Join(bares, "tool.git")
	repoWithOrigin(t, filepath.Join(bares, "toolseed"), bare)
	envdir := filepath.Join(bares, "envtools")
	cloneTool(t, bare, filepath.Join(envdir, "allure2"))
	installSync(t, hq, mheader+mrow("allure2", fakeURL(bare), head(t, filepath.Join(envdir, "allure2"))))
	o, code := runSync(t, hq, []string{"HELIXQA_TOOLS_DIR=" + envdir})
	if code != 0 || !strings.Contains(rowFor(o, "allure2"), "env") {
		t.Errorf("want tier env exit 0, got %d\n%s", code, o)
	}
}

// ambient GIT_* must not redirect git.
func TestSyncScript_GitEnvironmentIgnored(t *testing.T) {
	needGit(t)
	consumer, hq, bares := behindConsumer(t)
	bare := filepath.Join(bares, "tool.git")
	repoWithOrigin(t, filepath.Join(bares, "toolseed"), bare)
	tool := filepath.Join(hq, "tools", "opensource", "allure2")
	cloneTool(t, bare, tool)
	installSync(t, hq, mheader+mrow("allure2", fakeURL(bare), head(t, tool)))
	cBefore := snap(t, consumer)
	env := []string{
		"GIT_DIR=" + filepath.Join(consumer, ".git"),
		"GIT_WORK_TREE=" + consumer,
		"GIT_INDEX_FILE=" + filepath.Join(consumer, ".git", "index"),
		"GIT_CEILING_DIRECTORIES=" + consumer,
		"GIT_NAMESPACE=zz",
	}
	out, code := runSync(t, hq, env)
	sameSnap(t, "consumer", cBefore, snap(t, consumer))
	if code != 0 || !strings.Contains(rowFor(out, "allure2"), head(t, tool)) {
		t.Errorf("tool must be reported from its own repo (exit 0, own HEAD), got %d\n%s", code, out)
	}
}

func TestSyncScript_DriftAndNoDrift(t *testing.T) {
	f := newToolFx(t)
	old := head(t, f.tool)
	commitFile(t, f.tool, "local")
	f.install(t, old)
	out, code := runSync(t, f.hq, nil)
	if code != 0 || !strings.Contains(rowFor(out, "allure2"), "DRIFT") || !strings.Contains(out, head(t, f.tool)) {
		t.Errorf("checkout off the pin: want DRIFT, exit 0, CHECKOUT_HEAD shown; got %d\n%s", code, out)
	}
	f.install(t, strings.ToUpper(head(t, f.tool))) // upper-case pin must compare equal
	out, code = runSync(t, f.hq, nil)
	if code != 0 || strings.Contains(out, "DRIFT") {
		t.Errorf("matching pin (case-insensitive): want no DRIFT, got %d\n%s", code, out)
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
	out, code := runSync(t, hq, nil)
	if code != 3 || !strings.Contains(rowFor(out, "locked"), "ERROR") || !strings.Contains(out, "good") || !strings.Contains(out, "processed=2") {
		t.Errorf("want ERROR row, continue to good, exit 3; got %d\n%s", code, out)
	}
}

func TestSyncScript_UnbornHeadIsError(t *testing.T) {
	f := newToolFx(t)
	f.install(t, pin0)
	os.WriteFile(filepath.Join(f.tool, ".git", "HEAD"), []byte("ref: refs/heads/nonexistent\n"), 0o644)
	out, code := runSync(t, f.hq, nil)
	if code != 3 || !strings.Contains(rowFor(out, "allure2"), "ERROR") {
		t.Errorf("unreadable HEAD must be ERROR exit 3, got %d\n%s", code, out)
	}
}

// ---- manifest parsing ----

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
		{"no-trailing-newline", func(f toolFx) string { return strings.TrimRight(mheader+good(f), "\n") }, 0, []string{"rows=1 processed=1", "ok"}, nil},
		{"crlf", func(f toolFx) string { return strings.ReplaceAll(mheader+good(f), "\n", "\r\n") }, 0, []string{"rows=1 processed=1"}, []string{"INVALID-ROW"}},
		{"empty-url", func(f toolFx) string { return mheader + mrow("allure2", "", pin0) }, 3, []string{"INVALID-ROW", "rows=1 processed=1"}, nil},
		{"empty-sha-column", func(f toolFx) string { return mheader + mrow("allure2", fakeURL(f.bare), "") }, 3, []string{"INVALID-ROW"}, nil},
		{"empty-middle-column", func(f toolFx) string {
			return mheader + "tools/opensource/allure2\t\t" + fakeURL(f.bare) + "\t" + pin0 + "\n"
		}, 3, []string{"INVALID-ROW"}, nil},
		{"name-starting-with-dash", func(f toolFx) string {
			return mheader + "tools/opensource/-rf\t_rf\t" + fakeURL(f.bare) + "\t" + pin0 + "\n"
		}, 3, []string{"INVALID-ROW"}, nil},
		{"new-name-bad-chars", func(f toolFx) string {
			return mheader + "tools/opensource/allure2\tAllure-2\t" + fakeURL(f.bare) + "\t" + pin0 + "\n"
		}, 3, []string{"INVALID-ROW"}, nil},
		{"new-name-not-derived", func(f toolFx) string {
			return mheader + "tools/opensource/allure2\tother\t" + fakeURL(f.bare) + "\t" + pin0 + "\n"
		}, 3, []string{"INVALID-ROW"}, nil},
		{"short-sha", func(f toolFx) string { return mheader + mrow("allure2", fakeURL(f.bare), "abc") }, 3, []string{"INVALID-ROW"}, nil},
		{"extra-column", func(f toolFx) string { return mheader + strings.TrimRight(good(f), "\n") + "\textra\n" }, 3, []string{"INVALID-ROW"}, nil},
		{"blank-lines-ignored", func(f toolFx) string { return mheader + "\n" + good(f) + "\n" }, 0, []string{"rows=1 processed=1"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newToolFx(t)
			installSync(t, f.hq, c.content(f))
			out, code := runSync(t, f.hq, nil)
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

// ---- URL normalisation ----

func TestSyncScript_URLNormalisation(t *testing.T) {
	needGit(t)
	cases := []struct {
		origin, manifest string
		wrong            bool
	}{
		{"https://GitHub.com/Org/Tool.git", "git@github.com:Org/Tool.git", false},
		{"ssh://git@github.com:22/Org/tool", "https://github.com/Org/tool.git/", false},
		{"github.com:Org/tool.git", "git@github.com:Org/tool.git", false},
		{"ssh://git@github.com/o/t", "https://user@github.com/o/t", false},
		{"ssh://git@host:2222/o/t.git", "ssh://host:2222/o/t", false},
		{"https://host:443/o/t", "https://host/o/t", false},
		{"git@my_host:o/t.git", "ssh://my_host/o/t", false},
		{"git@box:o/t", "ssh://git@BOX:22/o/t.git", false},
		{"https://h:0443/o/t", "https://h/o/t", false},
		{"ssh://git@h:0022/o/t", "git@h:o/t", false},
		{"https://GitHub.com./o/t", "https://github.com/o/t", false},
		{"https://h:70000/o/t", "https://h:70000/o/t", true},
		{"git@github.com:org/tool.git", "git@github.com:org/other.git", true},
		{"https://github.com/org/tool", "https://gitlab.com/org/tool", true},
		{"https://github.com/Org/Tool", "https://github.com/org/tool", true},
		{"ssh://git@host:2222/o/t", "ssh://git@host:2223/o/t", true},
		{"ssh://git@host:2222/o/t", "ssh://git@host/o/t", true},
		{"https://host:8443/o/t", "https://host/o/t", true},
		{"git://github.com/o/t.git", "git://github.com/o/t.git", true},
		{"http://github.com/o/t.git", "http://github.com/o/t.git", true},
		{"http://github.com/o/t.git", "https://github.com/o/t.git", true},
		{"file:///srv/x.git", "file:///srv/x.git", true},
		{"/srv/x.git", "/srv/x.git", true},
	}
	for i, c := range cases {
		proj := t.TempDir()
		hq := filepath.Join(proj, "submodules", "helix_qa")
		d := filepath.Join(hq, "tools", "opensource", "tool")
		repoWithOrigin(t, d, filepath.Join(t.TempDir(), "t.git"))
		git(t, d, "remote", "set-url", "origin", c.origin)
		installSync(t, hq, mheader+mrow("tool", c.manifest, head(t, d)))
		out, _ := runSync(t, hq, nil)
		if got := strings.Contains(out, "WRONG-REPO") || strings.Contains(out, "INVALID-ROW"); got != c.wrong {
			t.Errorf("#%d origin=%q manifest=%q wrong=%v want %v\n%s", i, c.origin, c.manifest, got, c.wrong, out)
		}
	}
}

func TestSyncScript_NonOpensourceRowResolvesAndIsOk(t *testing.T) {
	needGit(t)
	proj := t.TempDir()
	hq := filepath.Join(proj, "submodules", "helix_qa")
	bare := filepath.Join(t.TempDir(), "r.git")
	tool := filepath.Join(hq, "tools", "test-apps", "rest-demo")
	repoWithOrigin(t, tool, bare)
	installSync(t, hq, mheader+"tools/test-apps/rest-demo\trest_demo\t"+fakeURL(bare)+"\t"+head(t, tool)+"\n")
	out, code := runSync(t, hq, nil)
	r := rowFor(out, "rest-demo")
	if code != 0 || !strings.Contains(r, "legacy") || !strings.Contains(r, " ok") || strings.Contains(out, "NOT-FOUND") {
		t.Errorf("rest-demo outside tools/opensource must be ok (exit %d)\n%s", code, out)
	}
	// a symlinked tools/test-apps is still rejected
	os.RemoveAll(filepath.Join(hq, "tools"))
	outside := filepath.Join(t.TempDir(), "test-apps")
	cloneTool(t, bare, filepath.Join(outside, "rest-demo"))
	mk(t, filepath.Join(hq, "tools"))
	os.Symlink(outside, filepath.Join(hq, "tools", "test-apps"))
	out, code = runSync(t, hq, nil)
	if !strings.Contains(out, "WRONG-REPO") || code != 3 {
		t.Errorf("symlinked parent must be WRONG-REPO exit 3, got %d\n%s", code, out)
	}
}

func TestSyncScript_StandaloneNoToolsSkipsConsumerFails(t *testing.T) {
	needGit(t)
	// standalone: not in a submodules dir, nothing resolves -> SKIP, exit 0
	sa := filepath.Join(t.TempDir(), "standalone", "helix_qa")
	installSync(t, sa, mheader+mrow("scrcpy", "git@h.test:o/s.git", pin0))
	out, code := runSync(t, sa, nil)
	if code != 0 || !strings.Contains(out, "SKIP") {
		t.Errorf("standalone with no tools: want SKIP exit 0, got %d\n%s", code, out)
	}
	// standalone with one resolved and one missing: partial -> exit 3
	bare := filepath.Join(t.TempDir(), "t.git")
	tool := filepath.Join(sa, "tools", "opensource", "scrcpy")
	repoWithOrigin(t, tool, bare)
	installSync(t, sa, mheader+mrow("scrcpy", fakeURL(bare), head(t, tool))+mrow("ghost", "git@h.test:o/g.git", pin0))
	out, code = runSync(t, sa, nil)
	if code != 3 || strings.Contains(out, "SKIP") {
		t.Errorf("standalone partial: want exit 3 no SKIP, got %d\n%s", code, out)
	}
	// a bad row in a standalone checkout never hides behind SKIP
	sb := filepath.Join(t.TempDir(), "standalone", "helix_qa")
	installSync(t, sb, mheader+mrow("scrcpy", "git@h.test:o/s.git", "abc"))
	if _, code = runSync(t, sb, nil); code != 3 {
		t.Errorf("INVALID-ROW must still fail standalone, got %d", code)
	}
	// consumer layout with nothing resolved: exit 3
	proj := t.TempDir()
	hq := filepath.Join(proj, "submodules", "helix_qa")
	installSync(t, hq, mheader+mrow("scrcpy", "git@h.test:o/s.git", pin0))
	out, code = runSync(t, hq, nil)
	if code != 3 || strings.Contains(out, "SKIP") {
		t.Errorf("consumer layout with no tools: want exit 3, got %d\n%s", code, out)
	}
}

func TestSyncScript_HeaderOnlyManifestExits2(t *testing.T) {
	f := newToolFx(t)
	installSync(t, f.hq, mheader)
	if out, code := runSync(t, f.hq, nil); code != 2 {
		t.Errorf("header-only: want exit 2, got %d\n%s", code, out)
	}
	installSync(t, f.hq, mheader+"  \n\t\n")
	if out, code := runSync(t, f.hq, nil); code != 2 {
		t.Errorf("header + whitespace-only: want exit 2, got %d\n%s", code, out)
	}
}

func TestSyncScript_WhitespaceOnlyLinesNotCounted(t *testing.T) {
	f := newToolFx(t)
	installSync(t, f.hq, mheader+"   \n\t\t\n"+mrow("allure2", fakeURL(f.bare), head(t, f.tool))+" \t \n")
	out, code := runSync(t, f.hq, nil)
	if code != 0 || !strings.Contains(out, "rows=1 processed=1") {
		t.Errorf("whitespace-only lines are blank: got %d\n%s", code, out)
	}
}

func TestSyncScript_ExtAndFdTransportsRejectedEvenWhenEqual(t *testing.T) {
	needGit(t)
	for _, u := range []string{"ext::sh -c 'touch /tmp/x'", "fd::7", "ext::ssh://h/o/t", "foo:::bar", "-oProxyCommand=x:p"} {
		f := newToolFx(t)
		git(t, f.tool, "config", "remote.origin.url", u)
		installSync(t, f.hq, mheader+mrow("allure2", u, head(t, f.tool)))
		out, code := runSync(t, f.hq, nil)
		if code != 3 || !(strings.Contains(out, "WRONG-REPO") || strings.Contains(out, "INVALID-ROW")) {
			t.Errorf("%q: want rejected exit 3, got %d\n%s", u, code, out)
		}
	}
}

func TestSyncScript_IncludeInjectedConfigIsWrongRepo(t *testing.T) {
	cases := map[string]string{
		"include-insteadOf":     "[url \"https://example.test/evil/\"]\n\tinsteadOf = https://example.test/\n",
		"include-second-origin": "[remote \"origin\"]\n\turl = https://example.test/o/other\n",
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			f := newToolFx(t)
			f.install(t, head(t, f.tool))
			inc := filepath.Join(f.tool, ".git", "extra.cfg")
			os.WriteFile(inc, []byte(extra), 0o644)
			git(t, f.tool, "config", "include.path", inc)
			before := snap(t, f.tool)
			out, code := runSync(t, f.hq, nil)
			if !strings.Contains(out, "WRONG-REPO") || code != 3 {
				t.Errorf("want WRONG-REPO exit 3, got %d\n%s", code, out)
			}
			sameSnap(t, name, before, snap(t, f.tool))
		})
	}
}

func TestSyncScript_AllGitEnvScrubbedIncludingTrace(t *testing.T) {
	f := newToolFx(t)
	f.install(t, head(t, f.tool))
	dir := filepath.Join(f.tool, ".git")
	env := []string{
		"GIT_TRACE=" + filepath.Join(dir, "t1"),
		"GIT_TRACE2=" + filepath.Join(dir, "t2"),
		"GIT_TRACE2_EVENT=" + filepath.Join(dir, "t3"),
		"GIT_TRACE_PACKET=" + filepath.Join(dir, "t4"),
		"GIT_TRACE_SETUP=" + filepath.Join(dir, "t5"),
	}
	out, code := runSync(t, f.hq, env)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, n := range []string{"t1", "t2", "t3", "t4", "t5"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			t.Errorf("GIT_TRACE* file %s written: not scrubbed", n)
		}
	}
}

// N1: the script may only run these git subcommands.
func TestSyncScript_AllowedGitSubcommandsPinned(t *testing.T) {
	b, err := os.ReadFile("../../scripts/sync-opensource-references.sh")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	allowed := map[string]bool{"rev-parse": true, "config": true, "ls-remote": true}
	re := regexp.MustCompile(`\bg "\$[a-z_]+"\s+([a-z-]+)(\s+[^\n|)]*)?`)
	m := re.FindAllStringSubmatch(src, -1)
	if len(m) < 4 {
		t.Fatalf("expected several g calls, found %d", len(m))
	}
	for _, x := range m {
		if !allowed[x[1]] {
			t.Errorf("disallowed git subcommand %q in %q", x[1], x[0])
		}
		if x[1] == "ls-remote" && !strings.Contains(x[0], "--get-url") {
			t.Errorf("ls-remote must only be used with --get-url: %q", x[0])
		}
	}
	// no direct git invocation other than inside the g() wrapper
	for i, l := range strings.Split(src, "\n") {
		t2 := strings.TrimSpace(l)
		if strings.HasPrefix(t2, "#") || strings.HasPrefix(t2, "g()") || strings.HasPrefix(t2, "echo ") || strings.HasPrefix(t2, "printf ") {
			continue
		}
		if regexp.MustCompile(`(^|[^A-Za-z_-])git (-C|fetch|pull|checkout|merge|status|push|clone|reset|gc|remote|submodule)`).MatchString(l) {
			t.Errorf("line %d invokes git directly: %q", i+1, l)
		}
	}
	for _, bad := range []string{"fetch", "pull", "checkout", "merge", "status", "clone", "reset"} {
		if regexp.MustCompile(`\bg "[^"]*"\s+` + bad + `\b`).MatchString(src) {
			t.Errorf("forbidden subcommand %s", bad)
		}
	}
}

// git config scopes: system, global, local, worktree, command.
//
//	system   - same code path as global (user's own; not creatable in a test)
//	global   - user insteadOf is ALLOWED (TestSyncScript_EffectiveURLAppliesGlobalInsteadOf)
//	local    - tool-local insteadOf / pushInsteadOf / 2nd origin => WRONG-REPO (existing tests)
//	worktree - .git/config.worktree: insteadOf / 2nd origin / the extension itself => WRONG-REPO
//	command  - GIT_CONFIG_COUNT/KEY/VALUE and -c: env is scrubbed so they have no effect
func TestSyncScript_WorktreeScopeConfigIsWrongRepo(t *testing.T) {
	cases := map[string]string{
		"worktree-insteadOf":     "[url \"https://example.test/evil/\"]\n\tinsteadOf = https://example.test/\n",
		"worktree-pushInsteadOf": "[url \"https://example.test/evil/\"]\n\tpushInsteadOf = https://example.test/\n",
		"worktree-second-origin": "[remote \"origin\"]\n\turl = https://example.test/o/other\n",
		"extension-only":         "",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			f := newToolFx(t)
			f.install(t, head(t, f.tool))
			git(t, f.tool, "config", "extensions.worktreeConfig", "true")
			os.WriteFile(filepath.Join(f.tool, ".git", "config.worktree"), []byte(content), 0o644)
			before := snap(t, f.tool)
			out, code := runSync(t, f.hq, nil)
			if !strings.Contains(out, "WRONG-REPO") || code != 3 {
				t.Errorf("want WRONG-REPO exit 3, got %d\n%s", code, out)
			}
			sameSnap(t, name, before, snap(t, f.tool))
		})
	}
}

func TestSyncScript_CommandScopeConfigFromEnvHasNoEffect(t *testing.T) {
	f := newToolFx(t)
	f.install(t, head(t, f.tool))
	out, code := runSync(t, f.hq, []string{
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=url.https://example.test/evil/.insteadOf",
		"GIT_CONFIG_VALUE_0=https://example.test/", "GIT_CONFIG_PARAMETERS='url.https://x/.insteadof=https://example.test/'",
	})
	if code != 0 || strings.Contains(out, "WRONG-REPO") {
		t.Errorf("env command-scope config must be scrubbed: %d\n%s", code, out)
	}
}

func TestSyncScript_ExportedShellFunctionsAreNotHonoured(t *testing.T) {
	f := newToolFx(t)
	f.install(t, head(t, f.tool))
	marker := filepath.Join(t.TempDir(), "marker")
	body := "() {  touch " + marker + "; command git \"$@\"; }"
	var env []string
	for _, fn := range []string{"git", "cd", "pwd", "head", "tail", "awk", "tr", "basename", "dirname", "sed", "wc", "grep", "source", "compgen"} {
		env = append(env, "BASH_FUNC_"+fn+"%%="+body)
	}
	out, code := runSync(t, f.hq, env)
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("an exported function hijacked the script\n%s", out)
	}
	if code != 0 {
		t.Errorf("exit %d\n%s", code, out)
	}
}

func TestSyncScript_OldPathWithoutSlashIsInvalidRow(t *testing.T) {
	f := newToolFx(t)
	installSync(t, f.hq, mheader+"allure2\tallure2\t"+fakeURL(f.bare)+"\t"+head(t, f.tool)+"\n")
	out, code := runSync(t, f.hq, nil)
	if code != 3 || !strings.Contains(out, "INVALID-ROW") || strings.Contains(out, "WRONG-REPO") {
		t.Errorf("old_path without '/' must be INVALID-ROW, got %d\n%s", code, out)
	}
}

func TestSyncScript_NestedLegacyWarningWording(t *testing.T) {
	needGit(t)
	proj := t.TempDir()
	hq := filepath.Join(proj, "submodules", "helix_qa")
	b := t.TempDir()
	a := filepath.Join(hq, "tools", "opensource", "scrcpy")
	r := filepath.Join(hq, "tools", "test-apps", "rest-demo")
	repoWithOrigin(t, a, filepath.Join(b, "a.git"))
	repoWithOrigin(t, r, filepath.Join(b, "r.git"))
	installSync(t, hq, mheader+mrow("scrcpy", fakeURL(filepath.Join(b, "a.git")), head(t, a))+
		"tools/test-apps/rest-demo\trest_demo\t"+fakeURL(filepath.Join(b, "r.git"))+"\t"+head(t, r)+"\n")
	out, code := runSync(t, hq, nil)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "WARNING: legacy tools/opensource resolution in consumer layout: scrcpy\n") {
		t.Errorf("tools/opensource warning must list only scrcpy\n%s", out)
	}
	if !strings.Contains(out, "legacy nested resolution") || !strings.Contains(out, "rest-demo") {
		t.Errorf("rest-demo needs the nested-resolution wording\n%s", out)
	}
}
