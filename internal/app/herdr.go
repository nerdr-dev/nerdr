package app

import (
	"fmt"
	"path/filepath"
	"strconv"

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
	if m.Herdr == nil || !*m.cfg().HerdrFocusJump || m.dialog != nil || m.editor != nil {
		return nil
	}
	i, t := m.fleet.Neediest()
	if t == nil || i == m.Active || t.Attention() < 3 {
		return nil
	}
	m.setFocus(core.FocusAgent)
	return tea.Batch(m.selectTab(i), m.resize())
}

// openEditor opens the file the review panel asked for in the user's
// editor.
func (m *Model) openEditor(msg review.EditMsg) tea.Cmd {
	// The file is on the machine of the tab the panel shows.
	mc := m.machineOf(m.screenTab)
	if host := mc.Host; host != "" {
		if c, ok := editor.RemoteFileCommand(host, msg.Dir, msg.File, msg.Line); ok {
			return m.runEditor(c, "")
		}
		// A terminal editor runs where the file is, in a pane of the tab.
		t := mc.Core.TabOfDir(msg.Dir)
		if t == nil {
			t = m.screenTab
		}
		if t == nil {
			t = m.Cur()
		}
		return m.do(core.EditInTab{T: m.fleet.Ref(t), File: msg.File, Line: msg.Line, Setting: editor.File})
	}
	return m.runEditor(editor.FileCommand(msg.Dir, msg.File, msg.Line), msg.Dir)
}

// openWorktreeEditor opens the active tab's worktree in the worktree
// editor (VS Code unless set otherwise).
func (m *Model) openWorktreeEditor() tea.Cmd {
	t := m.Cur()
	if t == nil || t.Dir == "" {
		return nil
	}
	if host := m.machineOf(t).Host; host != "" {
		if c, ok := editor.RemoteDirCommand(host, t.Dir); ok {
			return m.runEditor(c, "")
		}
		return m.do(core.EditInTab{T: m.fleet.Ref(t), Setting: editor.Worktree})
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
	p, err := term.Start(c.Argv, dir, core.ChildEnv(&core.Tab{}), m.width, m.height, m.home().Core.Notify)
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

// reportHerdr tells herdr the state of the tab that most needs attention.
// herdr shows one agent per pane, and this pane has all of nerdr's.
func (m *Model) reportHerdr() {
	if m.Herdr == nil {
		return
	}
	_, t := m.fleet.Neediest()
	if t == nil {
		t = m.Cur()
	}
	tokens := m.herdrTokens()
	if t == nil {
		m.Herdr.Report("unknown", "no tabs", "nerdr", tokens)
		return
	}
	st := "idle"
	switch t.Lit() {
	case core.StWorking, core.StBackground: // idle would have herdr tell of it as done
		st = "working"
	case core.StBlocked:
		st = "blocked"
	case core.StUnknown, core.StStarting, core.StExited, core.StAsleep:
		st = "unknown"
	}
	working := 0
	for _, x := range m.fleet.Tabs() {
		if x.Lit() == core.StWorking && x != t {
			working++
		}
	}
	msg := t.Name + ": " + t.Lit().Label()
	if working > 0 {
		msg += fmt.Sprintf(" · %d more working", working)
	}
	m.Herdr.Report(st, msg, "nerdr · "+t.Name, tokens)
}

// herdrTokens sum every tab up for herdr's sidebar, which shows one agent
// per pane: $summary is e.g. "5 tabs · 2 working · 1 needs input", and
// $tabs, $working, $blocked and $done are the counts.
func (m *Model) herdrTokens() map[string]string {
	var working, blocked, done int
	for _, t := range m.fleet.Tabs() {
		switch t.Lit() {
		case core.StWorking:
			working++
		case core.StBlocked:
			blocked++
		case core.StDone:
			done++
		}
	}
	summary := core.Plural(len(m.fleet.Tabs()), "tab")
	if working > 0 {
		summary += fmt.Sprintf(" · %d working", working)
	}
	if blocked > 0 {
		summary += fmt.Sprintf(" · %d need%s input", blocked, map[bool]string{true: "s", false: ""}[blocked == 1])
	}
	if done > 0 {
		summary += fmt.Sprintf(" · %d done", done)
	}
	return map[string]string{
		"summary": summary,
		"tabs":    strconv.Itoa(len(m.fleet.Tabs())),
		"working": strconv.Itoa(working),
		"blocked": strconv.Itoa(blocked),
		"done":    strconv.Itoa(done),
	}
}
