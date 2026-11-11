package workspace

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The agent here leaves the refs alone and writes the objects: origin's
// commit keeps its id, and holds the agent's files.

// replaceBase has the commit origin named check out the agent's files,
// with a replace ref for its tree, once it is fetched.
func replaceBase(t *testing.T, evil string) {
	t.Helper()
	afterFetch = func(repo string) {
		tree := revParse(t, repo, "refs/remotes/origin/main^{tree}")
		run(t, repo, "git", "replace", "-f", tree, revParse(t, repo, evil+"^{tree}"))
	}
	t.Cleanup(func() { afterFetch = func(string) {} })
}

// TestReplacedBaseIsNotCheckedOutForASandboxedAgent: a replace ref gives
// origin's commit the agent's tree. The tab a sandboxed agent asks for is
// checked out from origin's own files all the same, and setup runs on
// those.
func TestReplacedBaseIsNotCheckedOutForASandboxedAgent(t *testing.T) {
	a, newest, marks, allowed := pinRepo(t)
	replaceBase(t, evilCommit(t, a.Path, marks))

	_, wts, warnings := makeTab(t, a, true, allowed)
	if got := revParse(t, wts[0].Path, "HEAD"); got != newest {
		t.Fatalf("worktree at %s, not origin's %s", got, newest)
	}
	if _, err := os.Stat(filepath.Join(wts[0].Path, "evil.sh")); err == nil {
		t.Fatal("the worktree has the agent's files")
	}
	if marked(marks, "setup") || marked(marks, "hook") {
		t.Fatal("the agent's code ran")
	}
	if !marked(marks, "ran") || len(warnings) != 0 {
		t.Fatalf("setup did not run on origin's commit: %v", warnings)
	}
}

// TestReplacedBaseWouldRun shows the test above has something to stop:
// with no sandboxed agent about, the objects are taken as they are, and
// the replaced tree is what is checked out and run.
func TestReplacedBaseWouldRun(t *testing.T) {
	a, _, marks, allowed := pinRepo(t)
	replaceBase(t, evilCommit(t, a.Path, marks))

	makeTab(t, a, false, allowed)
	if !marked(marks, "setup") {
		t.Fatal("the replaced tree was not checked out: the test above proves nothing")
	}
}

// TestOverwrittenObjectStopsTheTab: the agent writes its own content in
// the place of a file of origin's commit, under the file's id. The tab is
// not made, and nothing is left of it.
func TestOverwrittenObjectStopsTheTab(t *testing.T) {
	a, _, marks, allowed := pinRepo(t)
	// Origin gets a commit with a file, which setup would run.
	other := filepath.Join(filepath.Dir(a.Path), "others", "a")
	if err := os.WriteFile(filepath.Join(other, "evil.sh"), []byte("true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, other, "git", "add", "evil.sh")
	run(t, other, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "a file")
	run(t, other, "git", "push", "-q", "origin", "HEAD:main")

	afterFetch = func(repo string) {
		blob := revParse(t, repo, "refs/remotes/origin/main:evil.sh")
		file := filepath.Join(repo, ".git", "objects", blob[:2], blob[2:])
		if _, err := os.Stat(file); err != nil {
			t.Fatalf("the fetched file is not a loose object to write over: %v", err)
		}
		content := "touch " + filepath.Join(marks, "setup") + "\n"
		var z bytes.Buffer
		w := zlib.NewWriter(&z)
		fmt.Fprintf(w, "blob %d\x00%s", len(content), content)
		w.Close()
		_ = os.Chmod(file, 0o644)
		if err := os.WriteFile(file, z.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { afterFetch = func(string) {} })

	plan, err := MakePlan(filepath.Join(filepath.Dir(a.Path), "worktrees"), "feat", []Repo{a})
	if err != nil {
		t.Fatal(err)
	}
	plan.Sandboxed = true
	if _, err := plan.Sync(); err != nil {
		t.Fatal(err)
	}
	wts, err := plan.Execute()
	if err == nil {
		Prepare(wts, plan.SetupIn(), allowed, func(string) {})
		t.Fatalf("the tab was made from objects that are not origin's; the agent's code ran: %v", marked(marks, "setup"))
	}
	if !strings.Contains(err.Error(), "objects") {
		t.Fatalf("the error does not say why: %v", err)
	}
	if marked(marks, "setup") || marked(marks, "ran") {
		t.Fatal("setup ran")
	}
	if _, serr := os.Stat(plan.Items[0].Path); serr == nil {
		t.Fatal("the worktree was left behind")
	}
	if refExists(a.Path, "refs/heads/feat") {
		t.Fatal("the branch was left behind")
	}
}
