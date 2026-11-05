package app

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/gitsafe"
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
	key := t.ID + "/" + string(t.Agent) + "/" + t.Dir
	if key == v.Key && time.Since(v.At) < core.SkillsCheckEvery {
		return v.Rows
	}
	v.Key, v.At, v.Shown = key, time.Now(), ""
	repo := ""
	if _, common, ok := gitsafe.Dirs(t.Dir); ok {
		repo = filepath.Dir(common)
	} else if out, err := gitsafe.Command(t.Dir, "rev-parse", "--path-format=absolute", "--git-common-dir").Output(); err == nil {
		repo = filepath.Dir(strings.TrimSpace(string(out)))
	}
	v.Rows = nil
	for _, it := range agents.Context(t.Agent, t.Dir, repo) {
		v.Rows = append(v.Rows, core.ContextRow{ContextItem: it})
	}
	switch t.Agent {
	case agents.Claude, agents.Codex, agents.Copilot, agents.OpenCode, agents.Antigravity:
		if note := m.AgentNote(t); note != "" {
			v.Rows = append(v.Rows, core.ContextRow{ContextItem: agents.ContextItem{Group: core.ContextGroupNote, Size: int64(len(note))}, Note: note})
		}
	}
	return v.Rows
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
		data, ok := skills.ReadShown(r.Path)
		c.Shown, c.Text, c.Exists = r.Path, string(data), ok
	}
	if !c.Exists {
		return head, append(text, "", dim("  Cannot be read.")), picked
	}
	return head, append(append(text, ""), wrapLines(c.Text, textW, 1<<20)...), picked
}
