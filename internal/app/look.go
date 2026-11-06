package app

import (
	"slices"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/term"
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
// terminal's.
func (m *Model) screen(t *core.Tab) *term.Pane { return m.focused(t).Term() }

// share is p's weight, 1 when it has none of its own.
func (m *Model) share(p *core.PaneTab) float64 {
	if w := m.pl(p).weight; w > 0 {
		return w
	}
	return 1
}
