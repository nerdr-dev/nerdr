package app

import (
	"path/filepath"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/core"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The new-tab dialog can make a repository to work on, or clone one, into
// the folder it lists repositories from (core.RepoScan.NewRepoDir):
// started in an empty folder, there is otherwise nothing to pick. Core
// makes it, on its own disk.

// newRepoForm is the new-tab dialog's form for a repository to make or
// clone.
type newRepoForm struct {
	clone bool
	// input is the name of the new repository, or what to clone.
	input textinput.Model
	// onGitHub is whether a new repository is made on GitHub too, private;
	// offered only with gh. field is 1 while the keys are on its box.
	onGitHub bool
	field    int
	gh       bool
	// job is the id of the request to make it, while it is being made.
	job string
}

// openNewRepo opens the form, to make a repository or, with clone, to
// clone one; gh is whether core has the gh CLI.
func (w *wizard) openNewRepo(clone, gh bool) tea.Cmd {
	f := newRepoForm{clone: clone, gh: gh}
	f.input = textinput.New()
	f.input.CharLimit = 300
	if clone {
		f.input.Prompt = "clone: "
		f.input.Placeholder = "owner/name, or a URL"
	} else {
		f.input.Prompt = "name: "
		f.input.Placeholder = "my-project"
	}
	w.newRepo, w.step, w.err = f, stepNewRepo, ""
	return w.newRepo.input.Focus()
}

func (w *wizard) newRepoKey(m *Model, msg tea.KeyMsg) tea.Cmd {
	f := &w.newRepo
	switch k := msg.String(); k {
	case "esc":
		w.step, w.err = stepRepos, ""
		f.input.Blur()
		return nil
	case "tab", "shift+tab":
		if !f.clone && f.gh {
			f.field = 1 - f.field
			if f.field == 1 {
				f.input.Blur()
				return nil
			}
			return f.input.Focus()
		}
		return nil
	case "enter":
		return w.makeRepo(m)
	case " ":
		if f.field == 1 {
			f.onGitHub = !f.onGitHub
			return nil
		}
	}
	if f.field == 1 {
		return nil
	}
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	return cmd
}

// makeRepo has core make or clone the repository the form says.
func (w *wizard) makeRepo(m *Model) tea.Cmd {
	f := &w.newRepo
	value := strings.TrimSpace(f.input.Value())
	w.err = ""
	f.job = core.NewJobID()
	w.busy = "making " + value + "…"
	if f.clone {
		w.busy = "cloning " + value + "…"
	}
	return m.do(core.MakeRepo{Job: f.job, Name: value, Clone: f.clone, OnGitHub: f.onGitHub && f.gh})
}

// repoMade takes the repository made or cloned: it is listed, and
// picked, in the dialog that asked for it.
func (m *Model) repoMade(msg core.RepoMadeMsg) tea.Cmd {
	if w, ok := m.dialog.(*wizard); ok && w.newRepo.job == msg.Job {
		return w.handleNewRepo(m, msg)
	}
	if msg.Path != "" {
		m.toast("made " + msg.Path)
	} else if msg.Err != "" {
		m.toast(msg.Err)
	}
	return nil
}

// handleNewRepo takes the repository made or cloned: it is listed, and
// picked.
func (w *wizard) handleNewRepo(m *Model, msg core.RepoMadeMsg) tea.Cmd {
	w.busy, w.newRepo.job = "", ""
	if msg.Path == "" {
		w.err = msg.Err
		return nil
	}
	w.newRepo.input.Blur()
	w.step = stepRepos
	w.scan(m)
	w.picked = map[int]bool{}
	for i, r := range w.repos {
		if filepath.Clean(r.Path) == filepath.Clean(msg.Path) {
			w.picked[i] = true
			w.cursor = i
		}
	}
	w.err = msg.Warning
	if msg.Err != "" {
		w.err = msg.Err
	}
	if len(w.picked) == 0 && w.err == "" {
		w.err = msg.Path + " is not among the repositories listed here"
	}
	if w.err == "" {
		m.toast("made " + msg.Path)
	}
	return nil
}

// newRepoLines is the form, for the dialog's view.
func (w *wizard) newRepoLines(m *Model, sel lipgloss.Style) []string {
	f := &w.newRepo
	var lines []string
	if f.clone {
		lines = append(lines, "Clone a repository into "+dim(w.repoDir), "",
			f.input.View(), "",
			dim("owner/name clones from GitHub, with gh when it is installed, so that"),
			dim("private repositories work too; or any URL or path git can clone."))
	} else {
		lines = append(lines, "New repository in "+dim(w.repoDir), "",
			m.onClick(f.input.View(), func(m *Model) tea.Cmd {
				f.field = 0
				return f.input.Focus()
			}), "",
			dim("Made with git init, on a main branch with an empty first commit."))
		if f.gh {
			box := "[ ]"
			if f.onGitHub {
				box = "[✓]"
			}
			lines = append(lines, "", m.onClick(cursorLine(f.field == 1, box+" Also a private repository on GitHub, as origin", sel), func(m *Model) tea.Cmd {
				f.field = 1
				f.input.Blur()
				f.onGitHub = !f.onGitHub
				return nil
			}), dim("    gh repo create --private, under the account gh is logged in as"))
		}
	}
	lines = append(lines, "")
	if w.busy != "" {
		return append(lines, warn(w.busy))
	}
	keys := []string{"enter", "make it"}
	if f.clone {
		keys = []string{"enter", "clone"}
	} else if f.gh {
		keys = append(keys, "tab", "next", " ", "tick")
	}
	return append(lines, m.keys(append(keys, "esc", "back")...))
}
