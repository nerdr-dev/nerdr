package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/skills"

	"github.com/charmbracelet/lipgloss"
)

// F4's context list: what the tab's agent reads when it starts beside its
// skills.

// contextGroupNote is the group of the note nerdr itself gives the agent.
const contextGroupNote = "Added by nerdr"

// contextRow is a row of the context list: an agent's file or server, or
// nerdr's note, which is in no file of the user's.
type contextRow struct {
	agents.ContextItem
	note string
}

// contextView is the last look at what the tab's agent reads, and the text
// of the row last shown, as the screen is drawn on every frame.
type contextView struct {
	key    string
	at     time.Time
	rows   []contextRow
	shown  string
	text   string
	exists bool
}

// contextItems is what t's agent reads, looked up again when the last look
// is old or was for another tab.
func (m *Model) contextItems(t *tab) []contextRow {
	v := &m.skills.context
	if t == nil {
		v.rows = nil
		return nil
	}
	key := t.ID + "/" + string(t.Agent) + "/" + t.Dir
	if key == v.key && time.Since(v.at) < skillsCheckEvery {
		return v.rows
	}
	v.key, v.at, v.shown = key, time.Now(), ""
	repo := ""
	if _, common, ok := gitsafe.Dirs(t.Dir); ok {
		repo = filepath.Dir(common)
	} else if out, err := gitsafe.Command(t.Dir, "rev-parse", "--path-format=absolute", "--git-common-dir").Output(); err == nil {
		repo = filepath.Dir(strings.TrimSpace(string(out)))
	}
	v.rows = nil
	for _, it := range agents.Context(t.Agent, t.Dir, repo) {
		v.rows = append(v.rows, contextRow{ContextItem: it})
	}
	switch t.Agent {
	case agents.Claude, agents.Codex, agents.Copilot, agents.OpenCode, agents.Antigravity:
		if note := m.agentNote(t); note != "" {
			v.rows = append(v.rows, contextRow{ContextItem: agents.ContextItem{Group: contextGroupNote, Size: int64(len(note))}, note: note})
		}
	}
	return v.rows
}

// contextName is a row as listed.
func contextName(r contextRow, home string) string {
	switch {
	case r.note != "":
		return "nerdr's note to the agent"
	case r.Name != "":
		return skills.Printable(r.Name)
	}
	return skills.Printable(tildePath(r.Path, home))
}

// fileSize is a file's size, short.
func fileSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f KB", float64(n)/1024)
}

// contextList is the list of what t's agent reads, and the text of the row
// picked.
func (m *Model) contextList(t *tab, rows []contextRow, textW int) (head, text []string, picked int) {
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
		label := contextName(r, home)
		if r.Group != agents.ContextMCP {
			label += dim("  " + fileSize(r.Size))
		}
		if i == v.contextCursor {
			picked = len(head)
		}
		head = append(head, m.skillRow(i == v.contextCursor, "", label, nil, func(m *Model) { m.skills.contextCursor = i }))
	}

	r := rows[v.contextCursor]
	title := skills.Printable(filepath.Base(r.Path))
	if r.note != "" {
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
	case r.note != "":
		for _, l := range wrapRows("Sent when the agent starts, and in no file of yours.", textW-2, 2) {
			text = append(text, dim("  "+l))
		}
		return head, append(append(text, ""), wrapLines(r.note, textW, 1<<20)...), picked
	case r.Group == agents.ContextMCP:
		for _, l := range wrapRows("An MCP server in "+skills.Printable(tildePath(r.Path, home))+". Its settings are not shown, as they can hold credentials. Servers from plugins or connectors are not listed.", textW-2, 4) {
			text = append(text, dim("  "+l))
		}
		return head, text, picked
	}
	text = append(text, dim("  "+contextName(r, home)))
	c := &v.context
	if c.shown != r.Path {
		data, ok := skills.ReadShown(r.Path)
		c.shown, c.text, c.exists = r.Path, string(data), ok
	}
	if !c.exists {
		return head, append(text, "", dim("  Cannot be read.")), picked
	}
	return head, append(append(text, ""), wrapLines(c.text, textW, 1<<20)...), picked
}
