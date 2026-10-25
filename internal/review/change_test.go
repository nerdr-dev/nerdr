package review

import (
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// A refresh begun before b switched the comparison is of the other one:
// it does not take the place of the list b loaded.
func TestBaseToggleOvertakesARefreshUnderWay(t *testing.T) {
	parent := t.TempDir()
	repo := filepath.Join(parent, "repo")
	runGit(t, "", "init", "-q", "-b", "main", repo)
	writeFile(t, filepath.Join(repo, "a.txt"), "initial\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init")
	runGit(t, repo, "checkout", "-qb", "feature")
	writeFile(t, filepath.Join(repo, "committed.txt"), "committed\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "feature")
	writeFile(t, filepath.Join(repo, "a.txt"), "changed\n")
	m := New(parent)
	m = m.send(tea.KeyMsg{Type: tea.KeyRight})
	m = m.send(tea.KeyMsg{Type: tea.KeyRight})
	pending := m.refreshFiles()
	if pending == nil {
		t.Fatal("no refresh")
	}
	msg := pending().(filesMsg)
	m.toggleBase()
	if !m.showBase || len(m.files) != 2 {
		t.Fatalf("against the base: %v %v", m.showBase, m.files)
	}
	m.onFiles(msg)
	if len(m.files) != 2 {
		t.Errorf("the refresh begun before took its place: %v", m.files)
	}
}
