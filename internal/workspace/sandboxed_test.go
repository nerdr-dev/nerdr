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
