package app

import (
	"path/filepath"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/editor"
	"github.com/nerdr-dev/nerdr/internal/launch"
	"github.com/nerdr-dev/nerdr/internal/review"
	"github.com/nerdr-dev/nerdr/internal/term"

	tea "github.com/charmbracelet/bubbletea"
)

// onHerdrFocus is called when the terminal (herdr) gives nerdr's pane
// focus. Clicking nerdr in herdr's agent list does that, and the entry
// clicked was the tab reportHerdr described — so go to it.
func (m *Model) onHerdrFocus() tea.Cmd {
	if m.Herdr == nil || !*m.Cfg.HerdrFocusJump || m.dialog != nil || m.editor != nil {
		return nil
	}
	i, t := m.Neediest()
	if t == nil || i == m.Active || t.Attention() < 3 {
		return nil
	}
	m.setFocus(core.FocusAgent)
	return tea.Batch(m.selectTab(i), m.resize())
}

// openEditor opens the file the review panel asked for in the user's
// editor.
func (m *Model) openEditor(msg review.EditMsg) tea.Cmd {
	return m.runEditor(editor.FileCommand(msg.Dir, msg.File, msg.Line), msg.Dir)
}

// openWorktreeEditor opens the active tab's worktree in the worktree
// editor (VS Code unless set otherwise).
func (m *Model) openWorktreeEditor() tea.Cmd {
	t := m.Cur()
	if t == nil || t.Dir == "" {
		return nil
	}
	return m.runEditor(editor.DirCommand(t.Dir), t.Dir)
}

// runEditor starts c in dir: an editor with its own window beside nerdr,
// a terminal one over the whole window until it exits.
func (m *Model) runEditor(c editor.Command, dir string) tea.Cmd {
	if c.Window {
		if err := launch.Detached(c.Argv); err != nil {
			m.toast(c.Argv[0] + ": " + err.Error())
		} else {
			m.toast("opened in " + filepath.Base(c.Argv[0]))
		}
		return nil
	}
	if m.editor != nil {
		return nil
	}
	p, err := term.Start(c.Argv, dir, core.ChildEnv(&core.Tab{}), m.width, m.height, m.Notify)
	if err != nil {
		m.toast(c.Argv[0] + ": " + err.Error())
		return nil
	}
	m.editor = p
	return nil
}

// checkEditor closes the editor overlay once the editor has exited.
func (m *Model) checkEditor() {
	if m.editor == nil {
		return
	}
	if exited, _, _ := m.editor.Exited(); exited {
		go m.editor.Close()
		m.editor = nil
	}
}
