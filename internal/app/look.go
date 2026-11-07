package app

import (
	"slices"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
)

// How the TUI shows the tabs and panes core has: the pane with the keyboard
// and how far back it is scrolled, each tab's; and each pane's being folded
// away, when it last had the keyboard, and its share of the room. They are the
// client's, not core's: another client lays the panes out its own way.

// tabLook is how the TUI shows a tab: on is the pane last gone to, scroll how
// many lines back into the scrollback the pane with the keyboard is shown.
type tabLook struct {
	on     *core.PaneTab
	scroll int
	// notified is the state last notified of, and when; see notify.go.
	notified   core.Status
	notifiedAt time.Time
	// goal is the task wrapped for the panel; see panel.go.
	goal *goalWrap
}

// goalWrap is the last message wrapped for one width, kept since the panel
// is measured on every resize and mouse motion, and wrapping is not free.
type goalWrap struct {
	w           int
	goal, reply string
	rows        []string
}

// paneLook is how the TUI shows a pane: small while it is minimised by hand,
// used when it last had the keyboard, for which to fold first when room runs
// out, and weight its share of the room beside the others shown, as its
// divider was dragged to; 0 is an even share.
type paneLook struct {
	small  bool
	used   time.Time
	weight float64
}

// tl is how t is shown, made the first time it is asked for.
func (m *Model) tl(t *core.Tab) *tabLook {
	if m.tabLooks == nil {
		m.tabLooks = map[*core.Tab]*tabLook{}
	}
	l := m.tabLooks[t]
	if l == nil {
		l = &tabLook{}
		m.tabLooks[t] = l
	}
	return l
}

// pl is how p is shown, made the first time it is asked for.
func (m *Model) pl(p *core.PaneTab) *paneLook {
	if m.paneLooks == nil {
		m.paneLooks = map[*core.PaneTab]*paneLook{}
	}
	l := m.paneLooks[p]
	if l == nil {
		l = &paneLook{used: p.Opened}
		m.paneLooks[p] = l
	}
	return l
}

// focused is the pane of t's the keyboard goes to while it is with the tab:
// the one last gone to, or its own agent.
func (m *Model) focused(t *core.Tab) *core.PaneTab {
	ps := t.Panes()
	if on := m.tl(t).on; on != nil && slices.Contains(ps, on) {
		return on
	}
	return ps[0]
}

// screen is the program in t's pane with the keyboard: an agent's or a
// terminal's; nil when none runs there.
func (m *Model) screen(t *core.Tab) core.Program { return m.program(m.focused(t)) }

// program is the program in pane p, as core gives it by p's PaneRef; nil
// when none runs there.
func (m *Model) program(p *core.PaneTab) core.Program {
	if p == nil {
		return nil
	}
	return m.programOf(core.ProgramRef{Pane: m.PaneRef(p)})
}

// programOf is the program r names, nil when none runs there: core's
// own, or with core's screens sent as rows, the copy kept of its screen.
func (m *Model) programOf(r core.ProgramRef) core.Program {
	p := m.Program(r)
	if p == nil || m.screens == nil {
		return p
	}
	return m.screens.Program(r)
}

// share is p's weight, 1 when it has none of its own.
func (m *Model) share(p *core.PaneTab) float64 {
	if w := m.pl(p).weight; w > 0 {
		return w
	}
	return 1
}

// SetScreens has the TUI draw core's programs from the copies c keeps of
// their screens, and send what the user does in them by c, as a client in
// a process of its own does.
func (m *Model) SetScreens(c *core.ScreenClient) { m.screens = c }
