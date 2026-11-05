package app

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/nerdr-dev/nerdr/internal/core"
)

// The panel above the agent, inside its frame: what the tab is about, so
// it is in sight whenever you come to the tab, without a dialog to close.
// It shows, each when there is one:
//
//	recap: Claude's recap of where the work stands, when it has written
//	      one since you last sent it something; else
//	last: the last thing you sent the agent (or else its first task), up
//	      to lastMaxLines rows; after the one before it when it is only
//	      an answer such as "yes"
//	note: your note for the tab, one row
//	swap: that a switch of agent waits for the old one's handoff
//	rev:  the code review going on, or how the last one ended
//	msgs: the messages waiting in F3
//	pr:   that the tab's work is merged, and how to close it
//
// and a rule under it all, between it and the agent.

var (
	panelStyle      = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("252"))
	panelLabelStyle = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(accent).Bold(true)
	panelDimStyle   = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("245"))
	panelRuleStyle  = lipgloss.NewStyle().Foreground(accent)
)

// wrapWithReply is wrapRows of text with reply after it, the reply kept
// whole at the end however much of text is cut. reply may be "".
func wrapWithReply(text, reply string, w, n int) []string {
	if reply == "" {
		return wrapRows(text, w, n)
	}
	tail := core.ReplyArrow + ansi.Truncate(strings.Join(strings.Fields(reply), " "), w/2, "…")
	if rows := wrapRows(text+tail, w, 1<<20); len(rows) <= n {
		return rows
	}
	rows := wrapRows(text, w, n)
	last := strings.TrimSuffix(rows[len(rows)-1], "…")
	rows[len(rows)-1] = ansi.Truncate(last, w-ansi.StringWidth(tail)-1, "") + "…" + tail
	return rows
}

// taskRows is the panel of tab t in a frame w×h inside.
func (m *Model) taskRows(t *core.Tab, w, h int) []core.PanelRow {
	textW := w - 2 - core.PanelLabelW // a space either side of the label
	if t == nil || textW < 8 {
		return nil
	}
	var rows []core.PanelRow
	label, g, reply := "last:", strings.TrimSpace(t.Last), ""
	if g == "" {
		g = strings.TrimSpace(t.Goal)
	} else if p := t.BeforeBrief(); p != "" {
		// "yes" alone says nothing of the work: what it answers does.
		g, reply = p, g
	}
	if r := strings.TrimSpace(t.Recap); r != "" {
		label, g, reply = "recap:", r, ""
	}
	if g != "" {
		if c := t.GoalWrap; c == nil || c.W != textW || c.Goal != g || c.Reply != reply {
			t.GoalWrap = &core.GoalWrap{W: textW, Goal: g, Reply: reply, Rows: wrapWithReply(g, reply, textW, core.LastMaxLines)}
		}
		for i, r := range t.GoalWrap.Rows {
			rows = append(rows, core.PanelRow{Label: core.OnOff(i == 0, label, ""), Text: r})
		}
	}
	if s := strings.TrimSpace(t.Status); s != "" {
		if t.StatusBy > 0 {
			s = fmt.Sprintf("%d: %s", t.StatusBy, s)
		}
		rows = append(rows, core.PanelRow{Label: "status:", Text: wrapRows(s, textW, 1)[0]})
	}
	if s := backgroundLine(t); s != "" {
		rows = append(rows, core.PanelRow{Label: "bg:", Text: ansi.Truncate(s, textW, "…"), Dim: true})
	}
	if n := strings.TrimSpace(t.Note); n != "" {
		rows = append(rows, core.PanelRow{Label: "note:", Text: wrapRows(n, textW, 1)[0]})
	}
	if w := t.Handoff; w != nil {
		rows = append(rows, core.PanelRow{Label: "swap:", Text: ansi.Truncate("waiting for "+string(t.Agent)+"'s handoff to "+string(w.To)+" · "+
			m.pk("a s")+" switches now or cancels", textW, "…"), Dim: true})
	}
	if s := m.ReviewLine(t); s != "" && t.ReviewOf == nil {
		rows = append(rows, core.PanelRow{Label: "rev:", Text: ansi.Truncate(s, textW, "…"), Dim: t.Review == nil})
	}
	if s := m.MessagesLine(t); s != "" {
		rows = append(rows, core.PanelRow{Label: "tasks:", Text: ansi.Truncate(s, textW, "…"), Dim: true})
	}
	if core.IsOn(m.Cfg.CloseMerged) && t.Info.Merged() {
		rows = append(rows, core.PanelRow{Label: "pr:", Text: ansi.Truncate(core.PrLabel(t.Info)+" · its work is in main · "+
			m.pk("x")+" closes the tab", textW, "…"), Dim: true})
	}
	room := max(h-core.MinAgentRows-1, 0) // one for the rule
	if len(rows) == 0 || room == 0 {
		return nil
	}
	return append(rows[:min(len(rows), room)], core.PanelRow{Rule: true})
}

// wrapRows wraps text, its own line breaks as spaces, into at most n rows
// of w columns, the last cut short with … when there is more.
func wrapRows(text string, w, n int) []string {
	rows := strings.Split(ansi.Wrap(strings.Join(strings.Fields(text), " "), w, ""), "\n")
	for i := range rows {
		rows[i] = strings.TrimLeft(rows[i], " ")
	}
	if len(rows) > n {
		rows = rows[:n]
		rows[n-1] = ansi.Truncate(rows[n-1], w-1, "") + "…"
	}
	return rows
}

// wrapLines wraps text, keeping its own line breaks, into at most n rows
// of w columns, the last cut short with … when there is more.
func wrapLines(text string, w, n int) []string {
	var rows []string
	for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		rows = append(rows, strings.Split(ansi.Wrap(strings.TrimRight(l, " \t"), w, ""), "\n")...)
	}
	if len(rows) > n {
		rows = rows[:n]
		rows[n-1] = ansi.Truncate(rows[n-1], w-1, "") + "…"
	}
	return rows
}

// taskLines draws the panel of tab t, w columns wide in a frame of h
// rows inside.
func (m *Model) taskLines(t *core.Tab, w, h int) []string {
	rows := m.taskRows(t, w, h)
	out := make([]string, len(rows))
	for i, r := range rows {
		if r.Rule {
			out[i] = panelRuleStyle.Render(strings.Repeat("─", w))
			continue
		}
		text := panelStyle
		if r.Dim {
			text = panelDimStyle
		}
		label := panelLabelStyle.Render(padTo(r.Label, core.PanelLabelW))
		if r.Label == "last:" {
			label = m.onClick(label, func(m *Model) tea.Cmd { return m.openSentOf(t) })
		}
		out[i] = panelStyle.Render(" ") + label +
			panelStyle.Render(" ") + text.Render(padTo(r.Text, w-2-core.PanelLabelW))
	}
	return out
}

// The task, when the tab was not opened with one: the first thing you ask
// the agent. Claude logs it, and it is read from there; for the other
// agents it is taken from what you type.

// backgroundLine says what agent t left running in the background, or "" for
// nothing. An agent at work again does not say: claude tells nerdr of what is
// running only as a turn ends.
func backgroundLine(t *core.Tab) string {
	if t.Light != core.StBackground || t.Hook.Processes == 0 {
		return ""
	}
	n := t.Hook.Processes
	if n == 1 {
		return "1 process running in the background"
	}
	return strconv.Itoa(n) + " processes running in the background"
}
