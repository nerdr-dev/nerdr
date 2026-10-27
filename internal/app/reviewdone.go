package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// reviewDoneDialog tells of a review that has ended a round: how it came
// out and what each reviewer said, in a box coloured by the verdict and
// wider than the other dialogs, so it is not missed. With findings
// waiting in F3 it asks whether to send them to the agent now; with none,
// it is only to be read.
type reviewDoneDialog struct {
	t *tab
	d *reviewDone
	// top is the first line of the summaries shown; shown is how many the
	// last drawing showed.
	top, shown int
}

func (rd *reviewDoneDialog) asks() bool { return rd.d.msg.Text != "" }

func (rd *reviewDoneDialog) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	page := max(rd.shown-1, 1)
	switch k := msg.String(); k {
	case "up", "k":
		rd.top--
	case "down", "j":
		rd.top++
	case "pgup":
		rd.top -= page
	case "pgdown":
		rd.top += page
	case "home", "g":
		rd.top = 0
	case "end", "G":
		rd.top = 1 << 30
	case "1", "s", "enter":
		m.dialog = nil
		if rd.asks() {
			m.sendFindings(rd.t, rd.d.msg)
		}
	case "2", "esc", "q", "ctrl+c":
		m.dialog = nil
	}
	return nil
}

// sayLines are the reviewers' conclusions, wrapped in w columns: each
// one's name and verdict, and under it its summary.
func (rd *reviewDoneDialog) sayLines(w int) []string {
	var out []string
	for i, s := range rd.d.says {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, lipgloss.NewStyle().Bold(true).Render(s.agent)+" "+verdictStyle(s.approved).Render(s.brief))
		if s.summary != "" {
			out = append(out, wrapLines(s.summary, w, 1<<30)...)
		}
	}
	return out
}

// verdictStyle is the colour of a verdict: green for approved, orange for
// changes asked for.
func verdictStyle(approved bool) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(verdictColour(approved)).Bold(true)
}

func verdictColour(approved bool) lipgloss.Color {
	if approved {
		return lipgloss.Color("42")
	}
	return lipgloss.Color("214")
}

func (rd *reviewDoneDialog) view(m *Model, w, h int) string {
	colour, mark := verdictColour(rd.d.approved), "⚑"
	if rd.d.approved {
		mark = "✓"
	}
	inner := min(max(w*2/3, 50), boxInner(w)-2, 100)
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(colour).
		Render(" " + mark + " Review of " + rd.t.Name + " done ")
	head := []string{title, ""}
	if len(rd.d.says) != 1 {
		// All reviewers together; one says it on its own line below.
		for _, l := range wrapLines(rd.d.brief, inner, 3) {
			head = append(head, verdictStyle(rd.d.approved).Render(l))
		}
		head = append(head, "")
	}

	var foot []string
	if rd.asks() {
		foot = []string{"", "The findings wait in " + m.screenKey(3) + ".",
			"", m.button("1", "Send them to "+string(rd.t.Agent)+" now") + "    " +
				m.button("2", "Keep them in "+m.screenKey(3)+" to send later")}
	} else {
		foot = []string{"", m.button("enter", "OK")}
	}

	says := rd.sayLines(inner)
	// The border, its padding and the line saying where in says it is.
	rd.shown = min(max(h-len(head)-len(foot)-5, 3), len(says))
	rd.top = min(max(rd.top, 0), max(len(says)-rd.shown, 0))
	body := append(append([]string{}, head...), says[rd.top:rd.top+rd.shown]...)
	if rd.shown < len(says) {
		body = append(body, dim(fmt.Sprintf("lines %d–%d of %d · ↑↓ pgup pgdown scroll",
			rd.top+1, rd.top+rd.shown, len(says))))
	}
	body = append(body, foot...)
	return lipgloss.NewStyle().Border(lipgloss.ThickBorder()).BorderForeground(colour).
		Padding(1, 2).Width(inner + 4).Render(strings.Join(body, "\n"))
}

// reviewPeek is a small card of the review done in the tab walked to,
// drawn over its agent while the prefix is on: the walk shows it without
// asking, and the enter that ends the walk opens the dialog that does.
// It takes no keys and no clicks. w is the columns it may have.
func (m *Model) reviewPeek(t *tab, d *reviewDone, w int) string {
	colour, mark := verdictColour(d.approved), "⚑"
	if d.approved {
		mark = "✓"
	}
	inner := min(w-4, 60)
	if inner < 20 {
		return ""
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(colour).
		Render(" " + mark + " Review of " + t.Name + " done ")
	body := []string{title}
	for _, l := range wrapLines(d.brief, inner, 2) {
		body = append(body, verdictStyle(d.approved).Render(l))
	}
	open := "enter: open"
	if d.msg.Text != "" {
		open = "enter: open, to send the findings or keep them in " + m.screenKey(3)
	}
	body = append(body, dim(ansi.Truncate(open, inner, "…")))
	return lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(colour).
		Padding(0, 1).Width(inner + 2).Render(strings.Join(body, "\n"))
}

// reviewMark is the ⚑ or ✓ on t's entry in the tab row and the sidebar
// while a review done in it waits to be told of, on background bg; "" when
// none does.
func (m *Model) reviewMark(t *tab, bg lipgloss.Color) string {
	_, d := t.reviewToTell()
	if d == nil {
		return ""
	}
	mark, tip := "⚑", "Review done: its findings wait for you"
	switch {
	case d.approved && d.msg.Text != "":
		mark, tip = "✓", "Review done: approved, its remarks wait for you"
	case d.approved:
		mark, tip = "✓", "Review done: approved"
	}
	return m.tip(lipgloss.NewStyle().Background(bg).Foreground(verdictColour(d.approved)).Bold(true).Render(mark), tip)
}
