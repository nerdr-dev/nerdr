package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	"github.com/charmbracelet/x/ansi"
)

// TestCommitDialog covers F2's c: the dialog lists what it commits, will
// not commit without a message, and commits every change with the one
// typed.
func TestCommitDialog(t *testing.T) {
	repo := testRepo(t)
	gitRun(t, repo, "config", "commit.gpgsign", "false")
	writeRepoFile(t, repo, "a.txt", "two\n")
	writeRepoFile(t, repo, "b.txt", "new\n")
	m := tabsModel(t, state.Tab{ID: "a", Name: "feat", Dir: repo,
		Worktrees: []workspace.Worktree{{Path: repo, Repo: repo, Branch: "main"}}})

	cmd, done := m.branchKey("c")
	d, ok := m.dialog.(*commitDialog)
	if !done || !ok {
		t.Fatalf("c opens the commit dialog: %T", m.dialog)
	}
	m.Update(cmd())
	view := ansi.Strip(d.view(m, 100, 40))
	for _, want := range []string{"M a.txt", "?? b.txt"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the dialog lists %q:\n%s", want, view)
		}
	}

	d.key(m, keyMsg("enter"))
	if m.dialog != d || d.err == "" {
		t.Fatal("no commit without a message")
	}
	d.input.SetValue("Change a, add b")
	commit := d.key(m, keyMsg("enter"))
	if m.dialog != nil || commit == nil {
		t.Fatal("enter with a message commits")
	}
	if msg, ok := commit().(prActionMsg); !ok || msg.err != "" {
		t.Fatalf("commit: %+v", msg)
	}
	if got := gitRun(t, repo, "log", "-1", "--format=%s"); got != "Change a, add b" {
		t.Fatalf("last commit %q", got)
	}
	if got := gitRun(t, repo, "status", "--porcelain"); got != "" {
		t.Fatalf("left uncommitted: %q", got)
	}
}

// TestCommitAndPush covers the dialog's second choice: the commit is
// pushed once it has been made.
func TestCommitAndPush(t *testing.T) {
	repo := testRepo(t)
	gitRun(t, repo, "config", "commit.gpgsign", "false")
	origin := filepath.Join(t.TempDir(), "origin.git")
	gitRun(t, repo, "init", "-q", "--bare", origin)
	gitRun(t, repo, "remote", "add", "origin", origin)
	writeRepoFile(t, repo, "a.txt", "two\n")
	m := tabsModel(t, state.Tab{ID: "a", Name: "feat", Dir: repo,
		Worktrees: []workspace.Worktree{{Path: repo, Repo: repo, Branch: "main", Pushes: []string{origin}}}})

	cmd, _ := m.branchKey("c")
	d := m.dialog.(*commitDialog)
	m.Update(cmd())
	d.key(m, keyMsg("tab"))
	if !strings.Contains(ansi.Strip(d.view(m, 100, 40)), "enter commit + push") {
		t.Fatalf("tab picks commit + push:\n%s", ansi.Strip(d.view(m, 100, 40)))
	}
	d.input.SetValue("Change a")
	msg, ok := d.key(m, keyMsg("enter"))().(prActionMsg)
	if !ok || msg.err != "" || !strings.Contains(msg.note, "pushed") {
		t.Fatalf("commit + push: %+v", msg)
	}
	if got, want := gitRun(t, origin, "rev-parse", "main"), gitRun(t, repo, "rev-parse", "HEAD"); got != want {
		t.Fatalf("origin has %s, not the commit %s", got, want)
	}
}

// TestLookAfterCommitIsNotLost covers a look at the branches already
// under way when a commit (or push) is done: it found the changes still
// uncommitted, and must not stand for the look the commit asked for —
// else c, p and P stay on until the next look, long after.
func TestLookAfterCommitIsNotLost(t *testing.T) {
	repo := testRepo(t)
	writeRepoFile(t, repo, "a.txt", "two\n")
	m := tabsModel(t, state.Tab{ID: "a", Name: "feat", Dir: repo,
		Worktrees: []workspace.Worktree{{Path: repo, Repo: repo, Branch: "main"}}})
	tb := m.cur()
	tb.lookAgain(false)
	before := m.refreshInfo()
	if before == nil {
		t.Fatal("a look starts")
	}
	look := before()

	gitRun(t, repo, "-c", "commit.gpgsign=false", "commit", "-qam", "commit while looking")
	m.Update(prActionMsg{t: tb, note: "committed feat"})
	_, again := m.Update(look)
	if !tb.info.stale || again == nil {
		t.Fatal("the look the commit asked for is still to come")
	}
	m.Update(again())
	if tb.info.canCommit() {
		t.Fatal("after the commit there is nothing to commit")
	}
}

// testRepo is a repository with one commit, of a.txt.
func testRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	gitRun(t, dir, "config", "user.email", "test@example.com")
	gitRun(t, dir, "config", "user.name", "Test")
	writeRepoFile(t, dir, "a.txt", "one\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "-c", "commit.gpgsign=false", "commit", "-m", "initial")
	return dir
}

func writeRepoFile(t *testing.T, dir, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
}
