package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/term"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// dialog is a modal drawn over the tabs. It gets every key until it closes
// itself by setting m.dialog to nil.
type dialog interface {
	key(m *Model, msg tea.KeyMsg) tea.Cmd
	view(m *Model, w, h int) string
}

// confirm asks a yes/no question.
type confirm struct {
	title string
	lines []string
	yes   func(m *Model) tea.Cmd
	// no, when set, is run on no as well.
	no func(m *Model) tea.Cmd
	// defaultNo makes Enter cancel. Labels default to yes/no.
	defaultNo         bool
	yesLabel, noLabel string
	// strict takes any key but yes for no: the question came from a
	// single key that may have been meant as the start of a word for the
	// agent, and the next letter of that word must not answer it.
	strict bool
	// scroll, when set, is shown below lines in a window of the height
	// left, scrolled with ↑↓, pgup/pgdown, home/end and the wheel; after
	// is below it.
	scroll, after []string
	// top is the first line of scroll shown; shown is how many the last
	// drawing showed.
	top, shown int
}

func (c *confirm) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	if c.defaultNo && msg.String() == "enter" {
		m.dialog = nil
		if c.no != nil {
			return c.no(m)
		}
		return nil
	}
	if c.scroll != nil {
		page := max(c.shown-1, 1)
		switch msg.String() {
		case "up", "k":
			c.scrollTo(c.top - 1)
			return nil
		case "down", "j":
			c.scrollTo(c.top + 1)
			return nil
		case "pgup", "b":
			c.scrollTo(c.top - page)
			return nil
		case "pgdown", " ", "f":
			c.scrollTo(c.top + page)
			return nil
		case "home", "g":
			c.scrollTo(0)
			return nil
		case "end", "G":
			c.scrollTo(len(c.scroll))
			return nil
		}
	}
	switch msg.String() {
	case "y", "Y", "enter":
		m.dialog = nil
		return c.yes(m)
	case "n", "N", "esc", "q", "ctrl+c":
		m.dialog = nil
		if c.no != nil {
			return c.no(m)
		}
	default:
		if c.strict {
			m.dialog = nil
			if c.no != nil {
				return c.no(m)
			}
		}
	}
	return nil
}

// scrollTo puts line i of scroll at the top, as far as there is more to
// show below it.
func (c *confirm) scrollTo(i int) {
	c.top = min(max(i, 0), max(len(c.scroll)-max(c.shown, 1), 0))
}

func (c *confirm) view(m *Model, w, h int) string {
	body := append([]string{}, c.lines...)
	if c.scroll != nil {
		// The box's border, title and buttons, and the line saying where
		// in scroll it is.
		c.shown = min(max(h-len(c.lines)-len(c.after)-7, 3), len(c.scroll))
		c.scrollTo(c.top)
		body = append(body, c.scroll[c.top:c.top+c.shown]...)
		if c.shown < len(c.scroll) {
			body = append(body, dim(fmt.Sprintf("lines %d–%d of %d · ↑↓ pgup pgdown scroll",
				c.top+1, c.top+c.shown, len(c.scroll))))
		}
		body = append(body, c.after...)
	}
	yes, no := c.yesLabel, c.noLabel
	if yes == "" {
		yes = "yes"
	}
	if no == "" {
		no = "no"
	}
	buttons := m.button("y", yes) + "   " + m.button("n", no)
	if c.defaultNo {
		buttons = m.button("enter", no) + "   " + m.button("y", yes)
	}
	body = append(body, "", buttons)
	return box(c.title, body, w)
}

// closeTab asks whether closing a tab should also delete its worktrees.
type closeTab struct {
	t           *core.Tab
	dirty       []string
	unpushed    []string
	mergedHeads map[string]string
	// heads is the commit each worktree had when git was asked, by path:
	// a branch is deleted only if it still points there.
	heads map[string]string
	// loaded is set once dirty and unpushed are known; they come from git,
	// in the background, and deleting waits for them.
	loaded bool
	// shown is set once closeWait has passed without them: the dialog is
	// drawn only when loaded or shown.
	shown bool
	// ask is set while the user is asked to confirm deleting what would
	// lose uncommitted changes or commits on no remote: askBranch for the
	// worktrees and branches, askWorktree for the worktrees alone.
	ask string
}

const (
	askBranch   = "branch"
	askWorktree = "worktree"
)

// closeWait is how long the close dialog waits for git and GitHub before it
// is drawn without what they say. Git alone answers well within it, and a
// pause this short still reads as the key being taken; a gh call over a slow
// network takes longer, and then the dialog says it is checking rather than
// leave the screen as it was.
const closeWait = 500 * time.Millisecond

// closeShowMsg comes closeWait after the close dialog c opened.
type closeShowMsg struct{ c *closeTab }

func (m *Model) openClose() tea.Cmd {
	return m.openCloseFor(m.Cur())
}

// openCloseFor asks about closing t.
func (m *Model) openCloseFor(t *core.Tab) tea.Cmd {
	if t == nil || m.fleet.TabIndex(t) < 0 || m.machineOf(t).Core.ReposHold(t) {
		return nil
	}
	c := &closeTab{t: t}
	m.dialog = c
	return tea.Batch(m.do(core.LookBeforeClose{T: m.fleet.Ref(t)}),
		tea.Tick(closeWait, func(time.Time) tea.Msg { return closeShowMsg{c} }))
}

// onCloseInfo has the close dialog of the tab show what closing it would
// lose.
func (m *Model) onCloseInfo(msg core.CloseInfoMsg) {
	if c, ok := m.dialog.(*closeTab); ok && c.t == m.fleet.ByRef(msg.T) {
		c.dirty, c.unpushed, c.loaded = msg.Dirty, msg.Unpushed, true
		c.mergedHeads, c.heads = msg.MergedHeads, msg.Heads
	}
}

// canDeleteBranches reports whether b is offered: every worktree has a
// branch, and git said where it points.
func (c *closeTab) canDeleteBranches() bool {
	if !c.loaded || len(c.t.Worktrees) == 0 {
		return false
	}
	for _, w := range c.t.Worktrees {
		if w.Branch == "" || c.heads[w.Path] == "" {
			return false
		}
	}
	return true
}

// merged reports whether every branch's pull request is merged with the
// head it has now.
func (c *closeTab) merged() bool {
	return len(c.t.Worktrees) > 0 && len(c.mergedHeads) == len(c.t.Worktrees)
}

// branchHeads is what deleting the branches expects them to point at.
func (c *closeTab) branchHeads() map[string]string {
	heads := make(map[string]string, len(c.heads))
	for k, v := range c.heads {
		heads[k] = v
	}
	return heads
}

// hidden reports whether the dialog is still waiting to be drawn.
func (c *closeTab) hidden() bool {
	return !c.loaded && !c.shown
}

func (c *closeTab) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	if c.hidden() {
		// Nothing is on the screen to answer yet: a key typed ahead is
		// dropped, but esc still cancels.
		switch k {
		case "esc", "q", "ctrl+c":
			m.dialog = nil
		}
		return nil
	}
	if c.ask != "" {
		switch k {
		case "Y":
			m.dialog = nil
			if c.ask == askBranch {
				return m.closeTab(c.t, true, true, c.branchHeads())
			}
			return m.closeTab(c.t, true, true, nil)
		case "n", "N", "esc", "q", "y", "w", "enter":
			c.ask = ""
		}
		return nil
	}
	if k == "enter" {
		// Enter, the key pressed without reading, deletes no branch but
		// one merged on GitHub: else only the worktree, the branch kept.
		k = "w"
		if c.merged() && c.canDeleteBranches() {
			k = "y"
		}
	}
	switch k {
	case "y":
		if !c.loaded {
			return nil // not yet known whether that would lose work
		}
		if !c.canDeleteBranches() {
			// No branch to delete, or git could not say where it points:
			// y deletes the worktrees alone.
			return c.deleteWorktrees(m)
		}
		// Commits on no remote live only in the branch: deleting it loses
		// them, so that is asked first, as uncommitted changes are.
		if len(c.dirty) > 0 || len(c.unpushed) > 0 {
			c.ask = askBranch
			return nil
		}
		m.dialog = nil
		return m.closeTab(c.t, true, false, c.branchHeads())
	case "w", "d":
		if !c.loaded {
			return nil
		}
		return c.deleteWorktrees(m)
	case "k":
		m.dialog = nil
		return m.closeTab(c.t, false, false, nil)
	case "n", "N", "esc", "q", "ctrl+c":
		// n is no, as in every other question: the tab stays open.
		m.dialog = nil
	}
	return nil
}

// deleteWorktrees closes the tab and deletes its worktrees, keeping the
// branches, once the user has confirmed losing uncommitted changes.
func (c *closeTab) deleteWorktrees(m *Model) tea.Cmd {
	if len(c.dirty) > 0 {
		c.ask = askWorktree
		return nil
	}
	m.dialog = nil
	return m.closeTab(c.t, true, false, nil)
}

func (c *closeTab) view(m *Model, w, h int) string {
	n := len(c.t.Worktrees)
	worktree, branch := "the worktree", "the branch"
	if n != 1 {
		worktree, branch = "the worktrees", "the branches"
	}
	var lines []string
	lines = append(lines, "Stops "+string(c.t.Agent)+" and closes "+c.t.Name+".", "")
	for _, wt := range c.t.Worktrees {
		l := "  " + dim(wt.Path)
		if wt.Branch != "" {
			l += "  " + dim("branch ") + wt.Branch
		}
		lines = append(lines, l)
	}
	if len(c.dirty) > 0 {
		lines = append(lines, "", warn("uncommitted changes in: "+strings.Join(c.dirty, ", ")))
	}
	if len(c.unpushed) > 0 {
		lines = append(lines, warn("commits not pushed in: "+strings.Join(c.unpushed, ", ")))
	}
	lines = append(lines, "")
	switch {
	case c.ask != "":
		if len(c.dirty) > 0 {
			lines = append(lines, warn("The uncommitted changes will be lost."))
		}
		if c.ask == askBranch && len(c.unpushed) > 0 {
			lines = append(lines, warn("The commits not pushed will be lost with "+branch+"."))
		}
		lines = append(lines, m.button("Y", "delete anyway")+"   "+m.button("n", "back"))
	case !c.loaded:
		lines = append(lines, dim("  checking for unsaved work and a merged pull request…"),
			m.button("k", "keep "+worktree+" and "+branch),
			m.button("n", "cancel, the tab stays open"))
	default:
		if c.canDeleteBranches() {
			note := ""
			switch {
			case c.merged():
				note = dim(" — merged on GitHub")
			case len(c.unpushed) > 0:
				note = warn(" — not merged, commits not pushed")
			default:
				note = dim(" — not merged")
			}
			yes, w := m.button("y", "delete "+worktree+" and "+branch)+note, m.button("w", "delete "+worktree+", keep "+branch)
			if c.merged() {
				yes += dim(" (enter)")
			} else {
				w += dim(" (enter)")
			}
			lines = append(lines, yes, w)
		} else {
			lines = append(lines, m.button("y", "delete "+worktree)+dim(" (enter)"))
		}
		lines = append(lines, m.button("k", "keep "+worktree+" and "+branch),
			m.button("n", "cancel, the tab stays open"))
	}
	return box("Close tab", lines, w)
}

// closeTab removes t from the row, stops its agent and, if asked, deletes
// its worktrees once the agent is gone. With branchHeads, it deletes the
// branches too, each only if it still points at its head there, by path.
func (m *Model) closeTab(t *core.Tab, remove, force bool, branchHeads map[string]string) tea.Cmd {
	i := m.fleet.TabIndex(t)
	if i < 0 || m.machineOf(t).Core.ReposHold(t) {
		return nil
	}
	group := m.fleet.Group(t)
	beside, wasActive, cur := m.splitTab(), i == m.Active, m.Cur()
	closed := m.do(core.CloseTab{T: m.fleet.Ref(t), Remove: remove, Force: force, BranchHeads: branchHeads})
	delete(m.panelScreens, t)
	delete(m.paneBars, t)
	for _, p := range t.Panes() {
		delete(m.paneLooks, p)
		if p.A != nil {
			delete(m.tabLooks, p.A)
		}
	}
	delete(m.tabLooks, t)
	if !wasActive {
		// Another tab closed (from the inbox, say): you stay where you are.
		m.Active = max(m.fleet.TabIndex(cur), 0)
	} else {
		m.Active = min(max(m.Active, 0), max(len(m.fleet.Tabs())-1, 0))
		// Stay in the group while it has tabs: the one after the closed
		// tab, or the one before when it was the last.
		if idx := m.fleet.GroupTabs(group); len(idx) > 0 {
			m.Active = idx[len(idx)-1]
			for _, j := range idx {
				if j >= i {
					m.Active = j
					break
				}
			}
		}
	}
	if beside != nil && wasActive {
		// The split's other tab has the window to itself.
		m.Active, m.split = m.fleet.TabIndex(beside), nil
	}
	if t == m.split {
		m.split = nil
	}
	m.resizePanes()
	m.scrolling = false
	m.saveLayout()
	cmds := []tea.Cmd{m.track(), m.pinReview(), closed}
	if len(m.fleet.Tabs()) == 0 {
		m.setFocus(core.FocusAgent)
		cmds = append(cmds, m.openWizard())
	} else if pr := m.program(m.fleet.Tabs()[m.Active].OwnPane()); pr != nil {
		pr.Focus(true)
	}
	m.reportHerdr()
	return tea.Batch(cmds...)
}

type quitReadyMsg struct{}

// quit stops the agents and exits, keeping the tabs for next time: those
// of the core in the TUI's process, and of every other machine's.
func (m *Model) quit() tea.Cmd {
	m.toast("stopping agents…")
	// The machines' cores in other processes stop theirs.
	for _, mc := range m.fleet.Live() {
		if mc.Conn != nil && !mc.Away {
			m.doOn(mc, core.QuitAll{})
		}
	}
	var panes []*term.Pane
	m.shutdown.Do(func() { panes = m.stopping() })
	h := m.Herdr
	return func() tea.Msg {
		core.StopPanes(panes)
		h.Release()
		return quitReadyMsg{}
	}
}

// box draws a titled, bordered dialog centred in w columns.
func box(title string, lines []string, w int) string {
	inner := 0
	for _, l := range lines {
		inner = max(inner, lipgloss.Width(l))
	}
	inner = min(max(inner, lipgloss.Width(title)+2, 40), boxInner(w))
	st := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("63")).
		Padding(0, 1).
		Width(inner + 2)
	head := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("110")).Render(title)
	return st.Render(head + "\n\n" + strings.Join(lines, "\n"))
}

// boxInner is the widest a box's lines can be in w columns; longer ones
// are wrapped.
func boxInner(w int) int { return max(w-6, 20) }

func keyHint(k string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true).Render(k)
}

func dim(s string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(s)
}

func warn(s string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render(s)
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
