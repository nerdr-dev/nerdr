package app

import (
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/nerdr-dev/nerdr/internal/editor"
	"github.com/nerdr-dev/nerdr/internal/launch"
	"github.com/nerdr-dev/nerdr/internal/review"
	"github.com/nerdr-dev/nerdr/internal/term"

	tea "github.com/charmbracelet/bubbletea"
)

// neediest is the tab that most wants the user — blocked first, then
// finished, then working, the most recent change winning a tie — or nil
// when every agent is idle.
func (m *Model) neediest() (int, *tab) {
	best, bi := (*tab)(nil), -1
	for i, t := range m.tabs {
		if t.attention() == 0 {
			continue
		}
		if best == nil || t.attention() > best.attention() ||
			(t.attention() == best.attention() && t.changed.After(best.changed)) {
			best, bi = t, i
		}
	}
	return bi, best
}

// reportHerdr tells herdr the state of the tab that most needs attention.
// herdr shows one agent per pane, and this pane has all of nerdr's.
func (m *Model) reportHerdr() {
	if m.herdr == nil {
		return
	}
	_, t := m.neediest()
	if t == nil {
		t = m.cur()
	}
	tokens := m.herdrTokens()
	if t == nil {
		m.herdr.Report("unknown", "no tabs", "nerdr", tokens)
		return
	}
	st := "idle"
	switch t.lit() {
	case stWorking:
		st = "working"
	case stBlocked:
		st = "blocked"
	case stUnknown, stStarting, stExited, stAsleep:
		st = "unknown"
	}
	working := 0
	for _, x := range m.tabs {
		if x.lit() == stWorking && x != t {
			working++
		}
	}
	msg := t.Name + ": " + t.lit().label()
	if working > 0 {
		msg += fmt.Sprintf(" · %d more working", working)
	}
	m.herdr.Report(st, msg, "nerdr · "+t.Name, tokens)
}

// herdrTokens sum every tab up for herdr's sidebar, which shows one agent
// per pane: $summary is e.g. "5 tabs · 2 working · 1 needs input", and
// $tabs, $working, $blocked and $done are the counts.
func (m *Model) herdrTokens() map[string]string {
	var working, blocked, done int
	for _, t := range m.tabs {
		switch t.lit() {
		case stWorking:
			working++
		case stBlocked:
			blocked++
		case stDone:
			done++
		}
	}
	summary := plural(len(m.tabs), "tab")
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
		"tabs":    strconv.Itoa(len(m.tabs)),
		"working": strconv.Itoa(working),
		"blocked": strconv.Itoa(blocked),
		"done":    strconv.Itoa(done),
	}
}

// onHerdrFocus is called when the terminal (herdr) gives nerdr's pane
// focus. Clicking nerdr in herdr's agent list does that, and the entry
// clicked was the tab reportHerdr described — so go to it.
func (m *Model) onHerdrFocus() tea.Cmd {
	if m.herdr == nil || !*m.cfg.HerdrFocusJump || m.dialog != nil || m.editor != nil {
		return nil
	}
	i, t := m.neediest()
	if t == nil || i == m.active || t.attention() < 2 {
		return nil
	}
	m.setFocus(focusAgent)
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
	t := m.cur()
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
	p, err := term.Start(c.Argv, dir, childEnv(&tab{}), m.width, m.height, m.notify)
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
