package app

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/skills"

	"github.com/charmbracelet/lipgloss"
)

// F4's context list: what the tab's agent reads when it starts beside its
// skills.

// contextItems is what t's agent reads, looked up again when the last look
// is old or was for another tab.
func (m *Model) contextItems(t *core.Tab) []core.ContextRow {
	v := &m.skills.context
	if t == nil {
		v.Rows = nil
		return nil
	}
	// The repository is core's to find, with the tab's branches: the
	// list is looked up again once it is known.
	repo := t.Info.Repo
	key := t.ID + "/" + string(t.Agent) + "/" + t.Dir + "/" + repo
	if key == v.Key && time.Since(v.At) < core.SkillsCheckEvery {
		return v.Rows
	}
	// The files are core's to read: what was read stays until core
	// answers (onContext), and another tab's shows nothing.
	s := &m.skills
	if s.ctxAsked != key || time.Since(s.ctxAskedAt) >= core.SkillsCheckEvery {
		s.ctxAsked, s.ctxAskedAt, s.ctxTab = key, time.Now(), m.Core.Ref(t)
		m.tellCore(core.ReadContext{Job: key, T: s.ctxTab})
	}
	if key != v.Key {
		return nil
	}
	return v.Rows
}

// contextRows are the rows of what t's agent reads, items, with nerdr's
// note.
func (m *Model) contextRows(t *core.Tab, items []agents.ContextItem) []core.ContextRow {
	var rows []core.ContextRow
	for _, it := range items {
		rows = append(rows, core.ContextRow{ContextItem: it})
	}
	switch t.Agent {
	case agents.Claude, agents.Codex, agents.Copilot, agents.OpenCode, agents.Antigravity:
		if note := m.Core.AgentNote(t); note != "" {
			rows = append(rows, core.ContextRow{ContextItem: agents.ContextItem{Group: core.ContextGroupNote, Size: int64(len(note))}, Note: note})
		}
	}
	return rows
}

// onContext takes what core read of what an agent reads, for the read
// asked last.
func (m *Model) onContext(ev core.ContextMsg) {
	s := &m.skills
	t := m.Core.ByRef(s.ctxTab)
	if ev.Job != s.ctxAsked || t == nil {
		return
	}
	v := &s.context
	if v.Key != ev.Job {
		v.Shown = "" // another tab's: its file is read anew
	}
	v.Key, v.At = ev.Job, time.Now()
	v.Rows = m.contextRows(t, ev.Items)
}

// onContextFile takes the text of a file core read for the row picked.
func (m *Model) onContextFile(ev core.ContextFileMsg) {
	s := &m.skills
	if ev.Job != s.fileAsked {
		return
	}
	s.fileAsked = ""
	s.context.Shown, s.context.Text, s.context.Exists = ev.Job, ev.Text, ev.Exists
}

// contextList is the list of what t's agent reads, and the text of the row
// picked.
func (m *Model) contextList(t *core.Tab, rows []core.ContextRow, textW int) (head, text []string, picked int) {
	agent := "the agent"
	if t != nil {
		agent = string(t.Agent)
	}
	if len(rows) == 0 {
		return []string{dim("  Nothing found that " + agent + " reads at start.")}, nil, 0
	}
	v := &m.skills
	v.contextCursor = min(max(v.contextCursor, 0), len(rows)-1)
	home, _ := os.UserHomeDir()
	group := ""
	bold := lipgloss.NewStyle().Bold(true)
	for i, r := range rows {
		if r.Group != group {
			group = r.Group
			head = append(head, " "+bold.Render(group))
		}
		label := core.ContextName(r, home)
		if r.Group != agents.ContextMCP {
			label += dim("  " + core.FileSize(r.Size))
		}
		if i == v.contextCursor {
			picked = len(head)
		}
		head = append(head, m.skillRow(i == v.contextCursor, "", label, nil, func(m *Model) { m.skills.contextCursor = i }))
	}

	r := rows[v.contextCursor]
	title := skills.Printable(filepath.Base(r.Path))
	if r.Note != "" {
		title = "nerdr's note"
	} else if r.Name != "" {
		title = skills.Printable(r.Name)
	}
	text = append(text, dim("── "+title+" "+strings.Repeat("─", max(textW-lipgloss.Width(title)-4, 0))))
	if !agents.ContextDocumented(t.Agent) {
		for _, l := range wrapRows("The usual places: "+agent+"'s documentation does not say all of what it reads.", textW-2, 2) {
			text = append(text, dim("  "+l))
		}
	}
	switch {
	case r.Note != "":
		for _, l := range wrapRows("Sent when the agent starts, and in no file of yours.", textW-2, 2) {
			text = append(text, dim("  "+l))
		}
		return head, append(append(text, ""), wrapLines(r.Note, textW, 1<<20)...), picked
	case r.Group == agents.ContextMCP:
		for _, l := range wrapRows("An MCP server in "+skills.Printable(core.TildePath(r.Path, home))+". Its settings are not shown, as they can hold credentials. Servers from plugins or connectors are not listed.", textW-2, 4) {
			text = append(text, dim("  "+l))
		}
		return head, text, picked
	}
	text = append(text, dim("  "+core.ContextName(r, home)))
	c := &v.context
	if c.Shown != r.Path {
		if v.fileAsked != r.Path {
			v.fileAsked = r.Path
			m.tellCore(core.ReadContextFile{Job: r.Path, File: r.Path})
		}
		if c.Shown != r.Path {
			return head, append(text, "", dim("  Reading…")), picked
		}
	}
	if !c.Exists {
		return head, append(text, "", dim("  Cannot be read.")), picked
	}
	return head, append(append(text, ""), wrapLines(c.Text, textW, 1<<20)...), picked
}
