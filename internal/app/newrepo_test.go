package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/agents"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// noAgents makes every agent nerdr knows missing from the PATH.
func noAgents(m *Model) {
	m.cfg.Agents = map[agents.Kind]agents.Spec{}
	for _, k := range agents.All {
		m.cfg.Agents[k] = agents.Spec{Command: []string{"nerdr-test-no-such-agent"}}
	}
}

// Started in an empty folder, the new-tab dialog offers to make a
// repository, and picks the one made.
func TestWizardMakesARepository(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	m := tabsModel(t)
	m.openWizard()
	w := m.dialog.(*wizard)
	if view := ansi.Strip(w.view(m, 120, 40)); !strings.Contains(view, "Make a new one (n) or clone one (c)") {
		t.Fatalf("the empty list offers nothing to do:\n%s", view)
	}
	w.key(m, keyMsg("n"))
	if w.step != stepNewRepo {
		t.Fatalf("n: step %d", w.step)
	}
	for _, r := range "fresh" {
		w.key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	cmd := w.key(m, keyMsg("enter"))
	if cmd == nil {
		t.Fatalf("nothing made: %s", w.err)
	}
	m.Update(cmd())
	if w.step != stepRepos || w.err != "" {
		t.Fatalf("step %d err %q", w.step, w.err)
	}
	p := filepath.Join(m.root, "fresh")
	if picked := w.pickedRepos(); len(picked) != 1 || picked[0].Path != p {
		t.Fatalf("picked %+v, want %s", picked, p)
	}
	if out, err := exec.Command("git", "-C", p, "log", "--oneline").Output(); err != nil || len(out) == 0 {
		t.Fatalf("no first commit: %v", err)
	}

	// The name is taken now.
	w.key(m, keyMsg("n"))
	for _, r := range "fresh" {
		w.key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if w.key(m, keyMsg("enter")) != nil || !strings.Contains(w.err, "already exists") {
		t.Fatalf("made over an existing folder: %q", w.err)
	}
}

// With no agent on the PATH, the dialog says so at once, and goes no
// further than it can.
func TestWizardWithoutAgents(t *testing.T) {
	m := tabsModel(t)
	noAgents(m)
	if err := exec.Command("git", "init", "-q", filepath.Join(m.root, "app")).Run(); err != nil {
		t.Fatal(err)
	}
	m.openWizard()
	w := m.dialog.(*wizard)
	if view := ansi.Strip(w.view(m, 120, 40)); !strings.Contains(view, "Found no agent on PATH") ||
		!strings.Contains(view, "[agents.my-agent]") {
		t.Fatalf("view:\n%s", view)
	}
	w.key(m, keyMsg("enter"))
	if w.step != stepRepos || !strings.Contains(w.err, "no agent") {
		t.Fatalf("step %d err %q", w.step, w.err)
	}
}
