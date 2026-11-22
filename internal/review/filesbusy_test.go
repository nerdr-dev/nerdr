package review

import (
	"path/filepath"
	"testing"
)

// TestRefreshGoesOnAfterAPinSwitch: a read of the file list still going
// when [ or ] moves to another pinned worktree is let go when it comes;
// the file list of the worktree moved to is still read again when it
// changes, not left waiting for that read for good.
func TestRefreshGoesOnAfterAPinSwitch(t *testing.T) {
	a := filepath.Join(setupJumpRepo(t, map[string]string{"a.go": "package a\n"}, nil), "repo")
	b := filepath.Join(setupJumpRepo(t, map[string]string{"b.go": "package b\n"}, nil), "repo")
	m := pinNow(t, NewEmbedded(Local{}), []Pin{{Repo: a, Path: a}, {Repo: b, Path: b}})

	stale := m.refreshFiles() // a change in a, read in the background
	if stale == nil || !m.filesBusy {
		t.Fatal("the refresh did not start")
	}
	m = settlePin(m, m.cyclePin(1))
	if m.curWT != b {
		t.Fatalf("shows %q, not %q", m.curWT, b)
	}
	m = m.send(stale()) // a's answer, overtaken

	_, cmd := m.Changed(b)
	var read bool
	for _, msg := range runCmds(cmd) {
		if f, ok := msg.(filesMsg); ok && f.wt == b {
			read = true
		}
	}
	if !read {
		t.Fatal("a change in the worktree moved to reads no file list")
	}
}

// TestRefreshGoesOnAfterAPin: as above, the tab switched, which pins the
// panel afresh.
func TestRefreshGoesOnAfterAPin(t *testing.T) {
	a := filepath.Join(setupJumpRepo(t, map[string]string{"a.go": "package a\n"}, nil), "repo")
	b := filepath.Join(setupJumpRepo(t, map[string]string{"b.go": "package b\n"}, nil), "repo")
	m := pinNow(t, NewEmbedded(Local{}), []Pin{{Repo: a, Path: a}})
	stale := m.refreshFiles()
	m = pinNow(t, m, []Pin{{Repo: b, Path: b}})
	m = m.send(stale())
	if m.refreshFiles() == nil {
		t.Fatal("the file list of the tab switched to is not read again")
	}
}
