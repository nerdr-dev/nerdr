package app

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Tooltips: a short word on what something in the bars is, shown by the
// pointer once it has rested on it a moment. A tip is a zone (see
// zones.go) with text and no handler, so it can be put round anything —
// a whole tab, or the light inside it — without changing what a click
// there does: zoneAt passes tips by, and tipAt finds the innermost one.
//
// For that the terminal has to report the pointer moving with no button
// held, which nerdr otherwise only asks for while a menu is open; such
// movement goes no further than here unless something else wants it (see
// needAllMotion).

// tipDelay is how long the pointer rests on something before its tip
// shows.
const tipDelay = 500 * time.Millisecond

// tipMsg is the pointer having rested long enough on the tip it was on
// when hover number seq began.
type tipMsg struct{ seq int }

// hover is where the pointer is, and its tip.
type hover struct {
	x, y  int
	tip   string // the tip under the pointer, "" for none
	shown bool   // tip is up
	seq   int    // counts the tips the pointer has come to
}

// tip wraps s so the pointer resting on it shows text.
func (m *Model) tip(s, text string) string {
	if !m.mouseOn() || text == "" {
		return s
	}
	s = m.zone(s, nil)
	m.zoneTips[len(m.zoneTips)-1] = text
	return s
}

// tipAt is the tip at (x, y), the innermost one there; "" when there is
// none.
func (m *Model) tipAt(x, y int) string {
	best, bestW := -1, 0
	for _, r := range m.zoneRects {
		if r.y != y || x < r.x0 || x >= r.x1 || r.id >= len(m.zoneTips) || m.zoneTips[r.id] == "" {
			continue
		}
		if w := r.x1 - r.x0; best < 0 || w < bestW || w == bestW && r.id > best {
			best, bestW = r.id, w
		}
	}
	if best < 0 {
		return ""
	}
	return m.zoneTips[best]
}

// hoverMouse follows the pointer for the tips. Moving onto a tip starts
// the wait for it; anything else — a click, the wheel — puts it away
// until the pointer comes to another.
func (m *Model) hoverMouse(ev tea.MouseMsg) tea.Cmd {
	m.hover.x, m.hover.y = ev.X, ev.Y
	if !plainMotion(ev) {
		m.hideTip()
		return nil
	}
	tip := m.tipAt(ev.X, ev.Y)
	if tip == m.hover.tip {
		return nil
	}
	m.hideTip()
	m.hover.tip = tip
	if tip == "" {
		return nil
	}
	seq := m.hover.seq
	return tea.Tick(tipDelay, func(time.Time) tea.Msg { return tipMsg{seq} })
}

// hideTip puts the tip away, and stops one on its way.
func (m *Model) hideTip() {
	m.hover.shown = false
	m.hover.seq++
}

func (m *Model) onTip(msg tipMsg) {
	if msg.seq == m.hover.seq && m.hover.tip != "" {
		m.hover.shown = true
	}
}

// plainMotion reports whether ev is the pointer moving with no button
// held.
func plainMotion(ev tea.MouseMsg) bool {
	return ev.Action == tea.MouseActionMotion && ev.Button == tea.MouseButtonNone
}

var tipStyle = lipgloss.NewStyle().Background(lipgloss.Color("237")).Foreground(lipgloss.Color("252"))

// drawTip draws the tip, if it is up, under the pointer in the drawn
// screen — or over it on the bottom row. It is not drawn when what is
// under the pointer has changed since.
func (m *Model) drawTip(lines []string) []string {
	h := m.hover
	if !h.shown || h.tip == "" || m.tipAt(h.x, h.y) != h.tip || len(lines) < 2 {
		return lines
	}
	box := tipStyle.Render(" " + ansi.Truncate(h.tip, max(m.width-2, 1), "…") + " ")
	y := h.y + 1
	if y >= len(lines) {
		y = h.y - 1
	}
	return overlayAt(lines, box, m.width, h.x, y)
}
