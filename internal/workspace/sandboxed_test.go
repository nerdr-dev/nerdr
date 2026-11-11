package workspace

import (
	"os"
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

// TestSandboxedPushRunsNoHookAndSendsLFS: a push from a tab where a
// sandboxed agent ran runs no pre-push hook, and what Git LFS's hook
// would have done is done by nerdr: git lfs push, before the push.
func TestSandboxedPushRunsNoHookAndSendsLFS(t *testing.T) {
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

	if err := PushIn(w, false); err != nil {
		t.Fatal(err)
	}
	if marked(marks, "hook") {
		t.Fatal("the pre-push hook ran in a tab where a sandboxed agent ran")
	}
	asked, _ := os.ReadFile(filepath.Join(marks, "lfs"))
	if strings.TrimSpace(string(asked)) != "push origin feat" {
		t.Fatalf("git lfs was asked %q, not to push the branch's files", asked)
	}
	if got, want := revParse(t, a.Path, "refs/remotes/origin/feat"), revParse(t, wt, "HEAD"); got != want {
		t.Fatalf("origin has %s, not %s", got, want)
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
}
