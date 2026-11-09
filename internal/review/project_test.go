package review

import (
	"testing"
)

// TestProjectIsReadAgainInTheBackground covers the refresh not walking the
// worktree on the UI's goroutine: nerdr reads it again and says its
// version, and the tree and diff are read again only when it is for the
// worktree still selected and its code changed.
func TestProjectIsReadAgainInTheBackground(t *testing.T) {
	m := Model{src: Local{}, curWT: "/wt", watchAsked: "/wt", projectVer: 1, projectLoad: true}

	nm, _ := m.Update(projectMsg{wt: "elsewhere", version: 2})
	if got := nm.(Model); got.projectVer != 1 || !got.projectLoad {
		t.Fatal("a load for another worktree was taken")
	}
	nm, cmd := m.Update(projectMsg{wt: "/wt", version: 1})
	if got := nm.(Model); got.projectVer != 1 || got.projectLoad || cmd != nil {
		t.Fatal("an unchanged project was read again, or the load stayed pending")
	}
	nm, _ = m.Update(projectMsg{wt: "/wt", version: 2})
	if got := nm.(Model); got.projectVer != 2 {
		t.Fatal("a changed project was not taken")
	}
}

// TestAskWithNoWorktreeDoesNothing covers "?" on an empty worktree list,
// which used to index it to say what the change is compared with.
func TestAskWithNoWorktreeDoesNothing(t *testing.T) {
	m := Model{src: Local{}}
	if cmd := m.askSelected(false); cmd != nil {
		t.Fatal("asked about a worktree that is not there")
	}
}
