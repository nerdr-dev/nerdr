package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// TestClearSandboxed: nerdr sandbox clear takes a repository's sandbox
// history out of the saved session, the others' left as they were, or all
// of it; and leaves the session alone while a nerdr has it.
func TestClearSandboxed(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	wt := filepath.Join(root, "wt")
	save := func() {
		t.Helper()
		if err := state.Save(state.Session{
			Root:                  root,
			SandboxHistoryVersion: 1,
			SandboxedDirs:         map[string]bool{wt: true},
			SandboxedRepos:        map[string]bool{a: true, b: true},
			SandboxSources:        map[string]string{a: "tab feat, " + wt},
			Tabs:                  []state.Tab{{Name: "feat", Dir: wt, Sandboxed: true, Worktrees: []workspace.Worktree{{Repo: a, Path: wt}}}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	load := func() state.Session {
		t.Helper()
		sess, err := state.Load(root)
		if err != nil {
			t.Fatal(err)
		}
		return sess
	}

	save()
	release, err := state.Lock(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clearSandboxed(root, []string{a}); err == nil || !strings.Contains(err.Error(), "Forget sandbox history") {
		t.Fatalf("cleared under a running nerdr: %v", err)
	}
	release()
	if sess := load(); !sess.SandboxedRepos[a] {
		t.Fatal("the session of a running nerdr was changed")
	}

	out, err := clearSandboxed(root, []string{a, filepath.Join(root, "c")})
	if err != nil || len(out) != 2 || !strings.Contains(out[0], "forgotten") || !strings.Contains(out[1], "no sandbox history") {
		t.Fatalf("%v, %v", out, err)
	}
	sess := load()
	if sess.SandboxedRepos[a] || !sess.SandboxedRepos[b] || sess.SandboxedDirs[wt] || sess.Tabs[0].Sandboxed || sess.SandboxSources[a] != "" {
		t.Fatalf("after clearing a: %+v", sess)
	}

	save()
	if _, err := clearSandboxed(root, nil); err != nil {
		t.Fatal(err)
	}
	if sess := load(); len(sess.SandboxedRepos) != 0 || len(sess.SandboxedDirs) != 0 || sess.Tabs[0].Sandboxed {
		t.Fatalf("after clearing all: %+v", sess)
	}
}
