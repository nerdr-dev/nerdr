package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	b, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, b, err)
	}
	return strings.TrimSpace(string(b))
}

func writeIn(t *testing.T, dir, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestVisitMarksFurtherEditsAndSurvivesCommit(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	gitIn(t, dir, "init", "-b", "main")
	gitIn(t, dir, "config", "user.email", "test@example.com")
	gitIn(t, dir, "config", "user.name", "Test")
	writeIn(t, dir, "a.txt", "one\n")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "-c", "commit.gpgsign=false", "commit", "-m", "initial")
	m := &Core{Client: NoClient{}}
	m.Restore(state.Session{Tabs: []state.Tab{{ID: "a", Dir: dir, Worktrees: []workspace.Worktree{{Path: dir, Repo: dir}}}}}, time.Now())
	tb := m.Tabs[0]

	writeIn(t, dir, "a.txt", "already changed\n")
	v := &FocusVisit{}
	tb.FocusVisit = v
	before := CaptureFocus(tb, v, false)().(FocusFilesMsg)
	writeIn(t, dir, "a.txt", "changed again\n")
	writeIn(t, dir, "new.txt", "new\n")
	after := CaptureFocus(tb, v, true)().(FocusFilesMsg)
	// The return capture can finish before the departure capture.
	m.Handle(after)
	m.Handle(before)
	if !tb.Unseen[dir]["a.txt"] || !tb.Unseen[dir]["new.txt"] {
		t.Fatalf("markers: %v", tb.Unseen)
	}
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "-c", "commit.gpgsign=false", "commit", "-m", "work")
	v2 := &FocusVisit{Before: after.Files}
	tb.FocusVisit = v2
	m.Handle(CaptureFocus(tb, v2, true)().(FocusFilesMsg))
	if len(tb.Unseen[dir]) != 0 {
		t.Fatal("committing identical content should not mark files")
	}
	m.Handle(before)
	if len(tb.Unseen[dir]) != 0 {
		t.Fatal("a delayed result from another visit replaced the baseline")
	}
	if got := gitIn(t, dir, "status", "--porcelain"); got != "" {
		t.Fatalf("snapshot modified the real index: %s", got)
	}
}
