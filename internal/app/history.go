package app

import (
	"strconv"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The review panel's F6 screen: everything nerdr has said in its bar
// since this nerdr started, the latest first, one row each with when, of
// what kind and how it starts, and below the rows the whole of the one
// picked. The bar shows only what fits; a message cut there ends with a
// link here.

// The kinds of what the bar says, by where it comes from.
const (
	catInfo      = core.HistoryInfo    // what nerdr says of what was done, a moment
	catWarning   = core.HistoryWarning // core's notices: what went wrong, or is to be fixed
	catAgents    = "agents"            // agents behind the tab shown that want a look
	catGitHub    = "github"            // news of your pull requests and issues
	catApprovals = "approvals"         // what you turned down for an agent
)

// historyCap is how many messages the history keeps: the oldest go.
const historyCap = 200

// histEntry is one message: what the bar said, of which kind, when last,
// and how many times in a row.
type histEntry struct {
	seq  int
	at   time.Time
	cat  string
	text string
	n    int
}

// historyView is the F6 screen's state: the messages, the latest first,
// the one picked (by seq, so new ones do not move it) and how far its
// text is scrolled.
type historyView struct {
	entries []histEntry
	seq     int
	picked  int
	scroll  int
}

// say shows s in the bar for d, and keeps it in the history as of kind
// cat.
func (m *Model) say(cat, s string, d time.Duration) {
	m.toastText = s
	m.toastUntil = time.Now().Add(d)
	m.toastSeq = m.history.add(cat, s, time.Now())
}

// add keeps s, and is its entry's seq: the same message again, of the
// same kind, counts on the one before rather than making another row.
func (h *historyView) add(cat, s string, now time.Time) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if len(h.entries) > 0 {
		if e := &h.entries[0]; e.cat == cat && e.text == s {
			e.n++
			e.at = now
			return e.seq
		}
	}
	h.seq++
	h.entries = append([]histEntry{{seq: h.seq, at: now, cat: cat, text: s, n: 1}}, h.entries...)
	if len(h.entries) > historyCap {
		h.entries = h.entries[:historyCap]
	}
	return h.seq
}

// index is where the entry picked is among the entries: the latest when
// none is, or it has gone.
func (h *historyView) index() int {
	for i, e := range h.entries {
		if e.seq == h.picked {
			return i
		}
	}
	return 0
}

// pick picks the i-th entry, its text from the top.
func (h *historyView) pick(i int) {
	if len(h.entries) == 0 {
		return
	}
	i = min(max(i, 0), len(h.entries)-1)
	if h.entries[i].seq != h.picked {
		h.scroll = 0
	}
	h.picked = h.entries[i].seq
}

// panelHistory reports whether the review panel shows the history.
func (m *Model) panelHistory() bool { return m.reviewOpen && m.review.History() }

// openHistory shows the history in the review panel, with the keyboard,
// with the message seq picked: the one the bar was cut short in.
func (m *Model) openHistory(seq int) tea.Cmd {
	m.reviewOpen = true
	m.saved.on = false
	if seq != 0 {
		m.history.picked, m.history.scroll = seq, 0
	}
	cmd := m.reviewKey(tea.KeyMsg{Type: tea.KeyF6})
	m.setFocus(core.FocusReview)
	return tea.Batch(cmd, m.resize())
}

// historyKey handles a key while the history has the keyboard.
func (m *Model) historyKey(msg tea.KeyMsg) tea.Cmd {
	h := &m.history
	i := h.index()
	switch msg.String() {
	case "esc", "q":
		m.setFocus(core.FocusAgent)
		return m.resize()
	case "up", "k":
		h.pick(i - 1)
	case "down", "j":
		h.pick(i + 1)
	case "home", "g":
		h.pick(0)
	case "end", "G":
		h.pick(len(h.entries) - 1)
	case "pgdown", "ctrl+d", " ":
		h.scroll += 10
	case "pgup", "b", "ctrl+u":
		h.scroll = max(h.scroll-10, 0)
	}
	return nil
}

// catStyle is how a kind of message is named in the history.
func catStyle(cat string) lipgloss.Style {
	c := map[string]lipgloss.Color{catWarning: "214", catAgents: accent, catGitHub: "75", catApprovals: "203"}[cat]
	if c == "" {
		c = "244"
	}
	return lipgloss.NewStyle().Foreground(c)
}

// historyBody draws the history into a w×h area: the rows, at most half
// of it, then the whole of the message picked, wrapped.
func (m *Model) historyBody(w, h int) []string {
	hv := &m.history
	keys := m.historyKeys("↑↓", "pick", "pgdown", "scroll", "b", "back", "esc", "agent")
	if len(hv.entries) == 0 {
		return fitLines([]string{"", "  Nothing said in the bar yet: what nerdr says there", "  is kept here, the latest first.", "", keys}, w, h)
	}
	picked := hv.index()
	var rows []string
	for i, e := range hv.entries {
		when := e.at.Format("15:04:05")
		if time.Since(e.at) >= 24*time.Hour {
			when = e.at.Format("Jan 2 15")
		}
		first, _, _ := strings.Cut(e.text, "\n")
		if e.n > 1 {
			first += " (×" + strconv.Itoa(e.n) + ")"
		}
		row := " " + dim(when) + " " + catStyle(e.cat).Render(padTo(e.cat, 9)) + " "
		row += ansi.Truncate(first, max(w-lipgloss.Width(row)-1, 5), "…")
		if i == picked {
			row = lipgloss.NewStyle().Reverse(true).Render(padTo(ansi.Strip(row), w))
		}
		rows = append(rows, m.onClick(row, func(m *Model) tea.Cmd {
			m.history.pick(i)
			m.setFocus(core.FocusReview)
			return nil
		}))
	}
	head := append([]string{""}, windowRows(rows, picked, max(h/2-1, 3))...)
	e := hv.entries[picked]
	text := []string{"", " " + catStyle(e.cat).Bold(true).Render(e.cat) + dim(" · "+e.at.Format("15:04:05 Jan 2"))}
	for _, l := range wrapText(e.text, max(w-2, 10)) {
		text = append(text, " "+l)
	}
	foot := []string{"", ansi.Truncate(keys, w, "…")}
	room := max(h-len(head)-len(foot), 1)
	hv.scroll = min(hv.scroll, max(len(text)-room, 0))
	shown := text[hv.scroll:]
	if len(shown) > room {
		shown = shown[:room]
	}
	return append(fitLines(append(head, shown...), w, h-len(foot)), foot...)
}

// historyKeys is the history's key hints, each a button for its key.
func (m *Model) historyKeys(pairs ...string) string {
	var out string
	for i := 0; i+1 < len(pairs); i += 2 {
		if out != "" {
			out += dim(" · ")
		}
		k, what := pairs[i], pairs[i+1]
		key := map[string]string{"↑↓": "down"}[k]
		if key == "" {
			key = k
		}
		out += m.onClick(keyHint(k)+" "+dim(what), func(m *Model) tea.Cmd { return m.historyKey(keyMsg(key)) })
	}
	return " " + out
}

// wrapText is s in lines of at most w columns, its own lines kept, broken
// at spaces, and within a word only when it is wider than w: a path in it
// stays whole where it can, to be read and copied.
func wrapText(s string, w int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		lines := wrapWords(para, w)
		if len(lines) == 0 {
			out = append(out, "")
		}
		for _, l := range lines {
			out = append(out, strings.Split(ansi.Hardwrap(l, w, false), "\n")...)
		}
	}
	return out
}

// toastShown reports whether the bar shows the toast now.
func (m *Model) toastShown() bool {
	return !m.prefix && m.toastText != "" && time.Now().Before(m.toastUntil)
}

// fitMsg is msg, what barStatus gave, cut to room columns. The toast, cut,
// ends with a button to the history, where it is whole.
func (m *Model) fitMsg(msg string, room int) string {
	room = max(room, 0)
	if lipgloss.Width(msg) <= room || !m.toastShown() {
		return ansi.Truncate(msg, room, "…")
	}
	more := " … " + m.screenKey(6) + " full "
	style := lipgloss.NewStyle().Background(tabBarBg).Foreground(lipgloss.Color("214"))
	text := ansi.Truncate(" "+m.toastText, max(room-lipgloss.Width(more), 0), "")
	seq := m.toastSeq
	link := m.tip(m.onClick(lipgloss.NewStyle().Background(tabBarBg).Foreground(accent).Underline(true).Render(more),
		func(m *Model) tea.Cmd { return m.openHistory(seq) }), "The whole message, in the history")
	return ansi.Truncate(style.Render(text)+link, room, "")
}
