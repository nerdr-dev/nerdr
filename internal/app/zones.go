package app

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Zones are how a click finds what it landed on. Whatever can be clicked
// is wrapped, as it is drawn, in a pair of invisible markers; once the
// whole screen is drawn, View finds the markers, notes where each zone
// ended up and takes the markers out again. So the geometry is never
// worked out twice — a click hits what was actually drawn there, however
// the bars were truncated or a dialog's lines wrapped. herdr keeps its
// hit areas the same way, from the drawing.
//
// A marker is ESC [ <id> z (start) or ESC [ <id> ; 1 z (end): a control
// sequence no terminal acts on, zero cells wide to lipgloss and ansi, and
// kept by their truncation. None of them ever reaches the terminal.

// zoneFn handles a mouse event on a zone.
type zoneFn func(m *Model, ev tea.MouseMsg) tea.Cmd

// zoneRect is where part of a zone was drawn: row y, columns x0 to x1
// (exclusive). A zone drawn over several lines has one per line.
type zoneRect struct {
	id        int
	y, x0, x1 int
}

// zone wraps s so a mouse event on it calls fn. Each line of s is marked
// on its own, so a zone over several lines is hit anywhere on them.
func (m *Model) zone(s string, fn zoneFn) string {
	id := len(m.zoneFns)
	m.zoneFns = append(m.zoneFns, fn)
	m.zoneTips = append(m.zoneTips, "")
	start := "\x1b[" + strconv.Itoa(id) + "z"
	end := "\x1b[" + strconv.Itoa(id) + ";1z"
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = start + l + end
	}
	return strings.Join(lines, "\n")
}

// scanZones finds the zone markers in a drawn screen and takes them out.
func scanZones(frame string) (string, []zoneRect) {
	if !strings.Contains(frame, "z") {
		return frame, nil
	}
	var out strings.Builder
	out.Grow(len(frame))
	var rects []zoneRect
	lines := strings.Split(frame, "\n")
	for y, line := range lines {
		if y > 0 {
			out.WriteByte('\n')
		}
		if !hasMarker(line) {
			out.WriteString(line) // most of a screen: the panes' lines
			continue
		}
		open := map[int]int{} // zone id -> column it started at
		var order []int
		col := 0
		var state byte
		for len(line) > 0 {
			seq, width, n, newState := ansi.DecodeSequence(line, state, nil)
			state = newState
			if id, end, ok := zoneMarker(seq); ok {
				if !end {
					if _, dup := open[id]; !dup {
						order = append(order, id)
					}
					open[id] = col
				} else if x0, ok := open[id]; ok {
					if col > x0 {
						rects = append(rects, zoneRect{id: id, y: y, x0: x0, x1: col})
					}
					delete(open, id)
				}
			} else {
				out.WriteString(seq)
				col += width
			}
			line = line[n:]
		}
		// A zone cut off by the end of the line (truncated) ends there.
		for _, id := range order {
			if x0, ok := open[id]; ok && col > x0 {
				rects = append(rects, zoneRect{id: id, y: y, x0: x0, x1: col})
			}
		}
	}
	return out.String(), rects
}

// hasMarker reports whether line may have a zone marker in it: an ESC [
// followed by digits, perhaps ";1", and z.
func hasMarker(line string) bool {
	for {
		i := strings.Index(line, "\x1b[")
		if i < 0 {
			return false
		}
		line = line[i+2:]
		j := 0
		for j < len(line) && (line[j] >= '0' && line[j] <= '9' || line[j] == ';') {
			j++
		}
		if j > 0 && j < len(line) && line[j] == 'z' {
			return true
		}
	}
}

// zoneMarker reads a zone marker: its id, and whether it ends the zone.
func zoneMarker(seq string) (id int, end, ok bool) {
	if len(seq) < 4 || seq[0] != 0x1b || seq[1] != '[' || seq[len(seq)-1] != 'z' {
		return 0, false, false
	}
	body := seq[2 : len(seq)-1]
	if b, isEnd := strings.CutSuffix(body, ";1"); isEnd {
		body, end = b, true
	}
	n, err := strconv.Atoi(body)
	if err != nil || n < 0 {
		return 0, false, false
	}
	return n, end, true
}

// zoneAt is the zone at (x, y), the innermost one when zones are nested
// (a button in a dialog): the narrowest of those hit. Tips are passed by
// (see tooltip.go). It returns -1 when there is none.
func (m *Model) zoneAt(x, y int) int {
	best, bestW := -1, 0
	for _, r := range m.zoneRects {
		if r.y != y || x < r.x0 || x >= r.x1 || r.id >= len(m.zoneFns) || m.zoneFns[r.id] == nil {
			continue
		}
		if w := r.x1 - r.x0; best < 0 || w < bestW || w == bestW && r.id > best {
			best, bestW = r.id, w
		}
	}
	return best
}
