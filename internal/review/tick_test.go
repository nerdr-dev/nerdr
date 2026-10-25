package review

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// TestTickReschedulesItself covers the polling loop surviving its own first
// firing. tea.Every is a one-shot timer, so a handler that doesn't ask for
// the next tick stops the clock: live refresh dies and the "just touched"
// markers, which only decay on a tick, freeze on screen forever.
func TestTickReschedulesItself(t *testing.T) {
	old := refreshInterval
	refreshInterval = 10 * time.Millisecond
	defer func() { refreshInterval = old }()

	m := Model{focus: focusRepos}
	_, cmd := m.Update(tickMsg{})
	if cmd == nil {
		t.Fatal("a tick produced no follow-up command, so the clock stops after one tick")
	}

	// tea.Batch collapses to the single command when the others are nil,
	// so the response is either the next tick itself or a batch containing
	// it. Each command may only be run once — its timer fires one message.
	switch msg := cmd().(type) {
	case tickMsg:
		return
	case tea.BatchMsg:
		for _, c := range msg {
			if c == nil {
				continue
			}
			if _, ok := c().(tickMsg); ok {
				return
			}
		}
	}
	t.Fatal("no command in the tick's response produces another tick")
}

// TestBaseToggleDoesNotMarkEverythingAsJustTouched covers the ● marker
// meaning what it says. Switching to the base diff replaces the file list
// with a different comparison — everything the branch changed, however long
// ago — and none of that was touched by anyone just now.
func TestBaseToggleDoesNotMarkEverythingAsJustTouched(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	parent := t.TempDir()
	repo := filepath.Join(parent, "myrepo")
	runGit(t, "", "init", "-q", "-b", "master", repo)
	writeFile(t, filepath.Join(repo, "a.txt"), "hello\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "init")

	// A branch whose committed work is old news, plus one live edit.
	runGit(t, repo, "checkout", "-q", "-b", "feature")
	writeFile(t, filepath.Join(repo, "old1.txt"), "long ago\n")
	writeFile(t, filepath.Join(repo, "old2.txt"), "long ago\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "earlier work")
	writeFile(t, filepath.Join(repo, "a.txt"), "hello\nworld\n")

	m := New(parent)
	send := func(msg tea.Msg) {
		nm, _ := m.Update(msg)
		m = nm.(Model)
	}
	send(tea.KeyMsg{Type: tea.KeyRight}) // repos -> worktrees
	send(tea.KeyMsg{Type: tea.KeyRight}) // worktrees -> files
	if len(m.files) != 1 {
		t.Fatalf("expected only the live edit in the worktree diff, got %#v", m.files)
	}

	m.recent = map[string]int{}
	send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})

	if !m.showBase || len(m.files) < 3 {
		t.Fatalf("expected the base diff to list the branch's files, got showBase=%v files=%#v", m.showBase, m.files)
	}
	if len(m.recent) != 0 {
		t.Fatalf("toggling the base diff marked %d files as just touched: %v", len(m.recent), m.recent)
	}

	// And back: the worktree view must not light up either.
	send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	if len(m.recent) != 0 {
		t.Fatalf("toggling back marked %d files as just touched: %v", len(m.recent), m.recent)
	}
}

// TestWatchRefreshesTheFiles covers a change arriving through the watch on
// the worktree rather than the tick: the new file is listed without a poll.
func TestWatchRefreshesTheFiles(t *testing.T) {
	parent := setupJumpRepo(t, map[string]string{"a.go": "package a\n"}, nil)
	m := New(parent)
	m = m.send(tea.KeyMsg{Type: tea.KeyRight})
	m = m.send(loadContextCmd(m.curWT)())
	if m.watcher == nil {
		t.Skip("the kernel refused the watch")
	}
	defer m.watcher.Close()

	writeFile(t, filepath.Join(parent, "repo", "b.go"), "package a\n")
	got := make(chan tea.Msg, 1)
	go func() { got <- waitForChange(m.watcher, m.curWT)() }()
	select {
	case msg := <-got:
		nm, cmd := m.Update(msg)
		m = nm.(Model).settle(cmd)
	case <-time.After(3 * time.Second):
		t.Fatal("the watch reported nothing")
	}
	for _, f := range m.files {
		if f.Path == "b.go" {
			return
		}
	}
	t.Fatalf("b.go not listed after the watch reported it: %v", m.files)
}

// TestFilesWaitWhileF1IsNotShown: off F1, or with the panel hidden, a
// change only marks the file list stale; coming back refreshes it once.
func TestFilesWaitWhileF1IsNotShown(t *testing.T) {
	parent := setupJumpRepo(t, map[string]string{"a.go": "package a\n"}, nil)
	m := New(parent)
	m = m.send(tea.KeyMsg{Type: tea.KeyRight})
	listed := func(m Model, path string) bool {
		for _, f := range m.files {
			if f.Path == path {
				return true
			}
		}
		return false
	}

	m.setMode(modePRs)
	writeFile(t, filepath.Join(parent, "repo", "b.go"), "package a\n")
	if cmd := m.refreshIfShown(); cmd != nil || !m.filesStale {
		t.Fatal("off F1, a change should only mark the list stale")
	}
	m = m.settle(m.setMode(modeReview))
	if !listed(m, "b.go") || m.filesStale {
		t.Fatalf("back on F1, the list should be refreshed: %v", m.files)
	}

	m, _ = m.SetShown(false)
	writeFile(t, filepath.Join(parent, "repo", "c.go"), "package a\n")
	if cmd := m.refreshIfShown(); cmd != nil {
		t.Fatal("hidden, the list should not be refreshed")
	}
	m, cmd := m.SetShown(true)
	m = m.settle(cmd)
	if !listed(m, "c.go") {
		t.Fatalf("shown again, the list should be refreshed: %v", m.files)
	}
}
