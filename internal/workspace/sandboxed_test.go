package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/gitsafe"
)

// TestUnpinnedBaseIsNotTrustedWhereASandboxedAgentRan: with no origin to
// ask, the base is the local main, which a sandboxed agent that has worked
// in the repository could have moved. A tab the user asks for there gets
// neither setup nor a hook, as one the agent asked for does not.
func TestUnpinnedBaseIsNotTrustedWhereASandboxedAgentRan(t *testing.T) {
	a, _, marks, allowed := pinRepo(t)
	run(t, a.Path, "git", "remote", "remove", "origin")
	evil := evilCommit(t, a.Path, marks)
	setRef(t, a.Path, "refs/heads/main", evil)
	gitsafe.Sandboxed(a.Path)

	_, _, warnings := makeTab(t, a, false, allowed)
	if marked(marks, "setup") || marked(marks, "hook") || marked(marks, "ran") {
		t.Fatal("setup or a hook ran on a base a sandboxed agent could have moved")
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "setup not run") {
		t.Fatalf("warnings = %v", warnings)
	}
}

// LFS must not be invoked explicitly as a substitute for a disabled hook.
func TestSandboxedPushRefusesLFSWithoutRunningHostCode(t *testing.T) {
	root := t.TempDir()
	a, _ := cloneWithOrigin(t, root, "a")
	marks := t.TempDir()
	hook := "#!/bin/sh\ntouch " + filepath.Join(marks, "hook") + "\ncommand -v git-lfs >/dev/null && git lfs pre-push \"$@\"\n"
	if err := os.WriteFile(filepath.Join(a.Path, ".git", "hooks", "pre-push"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	// A git-lfs that says what it was asked.
	bin := t.TempDir()
	lfs := "#!/bin/sh\necho \"$@\" >> " + filepath.Join(marks, "lfs") + "\n"
	if err := os.WriteFile(filepath.Join(bin, "git-lfs"), []byte(lfs), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	wt := filepath.Join(root, "wt")
	run(t, a.Path, "git", "worktree", "add", "-q", "-b", "feat", wt)
	commit(t, wt, "f", "f\n")
	w := Worktree{Repo: a.Path, Path: wt, Branch: "feat"}
	Trust(&w)

	if err := PushIn(w, false); err == nil || !strings.Contains(err.Error(), "Git LFS") {
		t.Fatalf("expected LFS refusal, got %v", err)
	}
	if marked(marks, "hook") || marked(marks, "lfs") {
		t.Fatal("a hook or git-lfs ran with hooks disabled")
	}
	if out, err := exec.Command("git", "-C", a.Path, "ls-remote", "origin", "refs/heads/feat").CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "" {
		t.Fatalf("refused branch was pushed: %v %s", err, out)
	}

	// Outside a sandbox the hook is what runs, as before.
	_ = os.Remove(filepath.Join(marks, "lfs"))
	commit(t, wt, "g", "g\n")
	if err := PushIn(w, true); err != nil {
		t.Fatal(err)
	}
	if !marked(marks, "hook") {
		t.Fatal("the pre-push hook did not run for a tab with no sandboxed agent")
	}
	if err := os.Remove(filepath.Join(marks, "hook")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(marks, "lfs")); err != nil {
		t.Fatal(err)
	}
	gitsafe.Sandboxed(wt)
	if err := PushIn(w, true); err == nil || !strings.Contains(err.Error(), "Git LFS") {
		t.Fatalf("caller preference bypassed repository taint: %v", err)
	}
	if marked(marks, "hook") || marked(marks, "lfs") {
		t.Fatal("repository taint allowed host LFS execution")
	}
	// Transport is refused across the repository, including an untouched
	// tab with an ordinary hook.
	if err := os.WriteFile(filepath.Join(a.Path, ".git", "hooks", "pre-push"), []byte("#!/bin/sh\ntouch "+filepath.Join(marks, "hook")+"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	untouched := filepath.Join(root, "untouched")
	run(t, a.Path, "git", "worktree", "add", "-q", "-b", "untouched", untouched)
	commit(t, untouched, "h", "h\n")
	other := Worktree{Repo: a.Path, Path: untouched, Branch: "untouched"}
	Trust(&other)
	if err := PushIn(other, true); err == nil || !strings.Contains(err.Error(), "transport") {
		t.Fatalf("expected transport refusal: %v", err)
	}
	if marked(marks, "hook") {
		t.Fatal("refused push ran its hook")
	}
}

// TestWorktreeIsMadeWhereASandboxedAgentRan: where an agent has run in a
// sandbox and nerdr cannot fetch from origin safely (here it is a local
// path, which could be a repository of the agent's making), a new tab is
// made all the same: from the base as it was last fetched, with a warning
// that names the repository, where its history is from and how to have
// nerdr forget it. Once it is forgotten, the base is fetched and pinned
// again.
func TestWorktreeIsMadeWhereASandboxedAgentRan(t *testing.T) {
	root := t.TempDir()
	a, push := cloneWithOrigin(t, root, "a")
	had := revParse(t, a.Path, "refs/remotes/origin/main")
	newest := push()
	gitsafe.SandboxedRepoFrom(a.Path, "tab old, /somewhere/old-a")

	plan, err := MakePlan(filepath.Join(root, "worktrees"), "feat", []Repo{a})
	if err != nil {
		t.Fatal(err)
	}
	warnings, err := plan.Sync()
	if err != nil {
		t.Fatalf("no tab where a sandboxed agent ran: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v", warnings)
	}
	for _, want := range []string{"as it was last fetched", "a local path", a.Path, "tab old, /somewhere/old-a", "nerdr sandbox clear " + a.Path, "Forget sandbox history"} {
		if !strings.Contains(warnings[0], want) {
			t.Errorf("warning lacks %q: %s", want, warnings[0])
		}
	}
	wts, err := plan.Execute()
	if err != nil {
		t.Fatal(err)
	}
	if got := revParse(t, wts[0].Path, "HEAD"); got != had {
		t.Fatalf("started from %s, not the base as last fetched %s", got, had)
	}
	if plan.Items[0].Pinned != "" || len(plan.SetupIn()) != 0 {
		t.Fatal("a base nobody fetched counts as origin's")
	}

	// A fetch of something else says the same, rather than only git's
	// "transport not allowed".
	err = FetchForkPR(a.Path, 1, "pr-1")
	if err == nil || !strings.Contains(err.Error(), "nerdr sandbox clear "+a.Path) || !strings.Contains(err.Error(), "tab old") {
		t.Fatalf("fetch refusal = %v", err)
	}

	gitsafe.ForgetSandboxed(a.Path)
	plan, err = MakePlan(filepath.Join(root, "worktrees"), "next", []Repo{a})
	if err != nil {
		t.Fatal(err)
	}
	if warnings, err := plan.Sync(); err != nil || len(warnings) != 0 {
		t.Fatalf("after forgetting: %v, %v", warnings, err)
	}
	if plan.Items[0].Pinned != newest {
		t.Fatalf("pinned %q, origin has %s", plan.Items[0].Pinned, newest)
	}
}

// fakeSSH has git reach ssh:// URLs by a script that runs the command
// asked for here, as a host that is this machine would, and leaves a file
// at the path it gives each time it ran.
func fakeSSH(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ran := filepath.Join(dir, "ran")
	script := filepath.Join(dir, "ssh")
	body := "#!/bin/sh\ntouch " + ran + "\nfor a; do last=$a; done\neval \"$last\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_SSH_COMMAND", script)
	return ran
}

// TestBaseIsFetchedOverSSHWhereASandboxedAgentRan: with origin reached
// over ssh, a new tab in a repository an agent has run in a sandbox in
// starts from origin's base as it is now, pinned to the commit origin
// named, and gets its setup, as in any other repository. A push there
// stays refused, and so does the fetch once the ssh command or the URL
// could be the agent's.
func TestBaseIsFetchedOverSSHWhereASandboxedAgentRan(t *testing.T) {
	a, _, marks, allowed := pinRepo(t)
	ran := fakeSSH(t)
	origin := filepath.Join(filepath.Dir(a.Path), "origins", "a.git")
	run(t, a.Path, "git", "remote", "set-url", "origin", "ssh://elsewhere"+origin)
	other := filepath.Join(filepath.Dir(a.Path), "others", "a")
	run(t, other, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "newest")
	run(t, other, "git", "push", "-q", "origin", "HEAD:main")
	newest := revParse(t, other, "HEAD")
	gitsafe.SandboxedRepoFrom(a.Path, "tab old, /somewhere/old-a")

	plan, wts, warnings := makeTab(t, a, false, allowed)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if !marked(filepath.Dir(ran), "ran") {
		t.Fatal("origin was not asked")
	}
	if plan.Items[0].Pinned != newest || revParse(t, wts[0].Path, "HEAD") != newest {
		t.Fatalf("pinned %q, origin has %s", plan.Items[0].Pinned, newest)
	}
	if !marked(marks, "ran") {
		t.Fatal("no setup on the base origin named")
	}

	// A push runs in the worktree, on what the agent wrote: still refused.
	commit(t, wts[0].Path, "f", "f\n")
	if err := PushIn(wts[0], true); err == nil || !strings.Contains(err.Error(), "nerdr sandbox clear") {
		t.Fatalf("push = %v", err)
	}

	// An ssh command from inside the repository could be the agent's.
	run(t, a.Path, "git", "config", "core.sshCommand", filepath.Join(a.Path, "ssh.sh"))
	if _, why := gitsafe.OriginEnv(a.Path); !strings.Contains(why, "core.sshcommand") {
		t.Fatalf("ssh command in the repository: %q", why)
	}
	run(t, a.Path, "git", "config", "--unset", "core.sshCommand")
	// So could a transport a URL rewrite brings in.
	run(t, a.Path, "git", "config", "url.ext::sh -c true .insteadOf", "ssh://elsewhere")
	if _, why := gitsafe.OriginEnv(a.Path); !strings.Contains(why, "the helper ext") {
		t.Fatalf("rewritten to ext: %q", why)
	}
}
