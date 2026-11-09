package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/git"
)

// TestRemoteReviewReadsThroughCore: a TUI in a process of its own has its
// review panel read the repositories through core, which reads them where
// they are and answers by the read's job; and a read core will not answer,
// its connection gone, ends at once.
func TestRemoteReviewReadsThroughCore(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m, _, _, told := remoteTUI(t, "sh", "-c", "sleep 30")
	if m.reviewAsk == nil {
		t.Fatal("the remote TUI's review panel does not ask core")
	}
	type result struct {
		files []git.FileStatus
		err   error
	}
	read := func() result {
		t.Helper()
		got := make(chan result, 1)
		go func() {
			files, _, err := m.reviewAsk.Files(git.Comparison{Worktree: repo})
			got <- result{files, err}
		}()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case r := <-got:
				return r
			case msg := <-told:
				m.Update(msg)
			case <-deadline:
				t.Fatal("no answer from core")
			}
		}
	}

	r := read()
	if r.err != nil || len(r.files) != 1 || r.files[0].Path != "a.go" {
		t.Fatalf("read through core: %v %v", r.files, r.err)
	}

	m.Mu.Lock()
	m.remote.Close("")
	m.Mu.Unlock()
	if r := read(); r.err == nil {
		t.Fatalf("a read with the connection closed should fail, got %v", r.files)
	}
}

// TestRemoteF4ReadsThroughCore: a TUI in a process of its own reads none
// of the files an agent reads at start itself, for F4: core reads the
// list and the file picked, and the TUI shows them once they come.
func TestRemoteF4ReadsThroughCore(t *testing.T) {
	m, _, _, told := remoteTUI(t, "sh", "-c", "sleep 30")
	file := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(file, []byte("be brief\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	until := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			m.Mu.Lock()
			done := ok()
			m.Mu.Unlock()
			if done {
				return
			}
			select {
			case msg := <-told:
				m.Update(msg)
			case <-deadline:
				t.Fatal("core did not answer: " + what)
			}
		}
	}

	m.Mu.Lock()
	tb := m.Cur()
	if !m.Copy || m.contextItems(tb) != nil {
		m.Mu.Unlock()
		t.Fatal("the copy read the list itself")
	}
	key := m.skills.ctxAsked
	m.Mu.Unlock()
	until("the list", func() bool { return m.skills.context.Key == key })

	m.Mu.Lock()
	m.skills.fileAsked = file
	m.tellCore(core.ReadContextFile{Job: file, File: file})
	m.Mu.Unlock()
	until("the file", func() bool { return m.skills.context.Shown == file })
	if c := m.skills.context; !c.Exists || c.Text != "be brief\n" {
		t.Fatalf("read %q, %v", c.Text, c.Exists)
	}
}
