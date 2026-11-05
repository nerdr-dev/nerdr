package core

import (
	"fmt"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/skills"
)

// ContextGroupNote is the group of the note nerdr itself gives the agent.
const ContextGroupNote = "Added by nerdr"

// ContextRow is a row of the context list: an agent's file or server, or
// nerdr's note, which is in no file of the user's.
type ContextRow struct {
	agents.ContextItem
	Note string
}

// ContextView is the last look at what the tab's agent reads, and the text
// of the row last shown, as the screen is drawn on every frame.
type ContextView struct {
	Key    string
	At     time.Time
	Rows   []ContextRow
	Shown  string
	Text   string
	Exists bool
}

// ContextName is a row as listed.
func ContextName(r ContextRow, home string) string {
	switch {
	case r.Note != "":
		return "nerdr's note to the agent"
	case r.Name != "":
		return skills.Printable(r.Name)
	}
	return skills.Printable(TildePath(r.Path, home))
}

// FileSize is a file's size, short.
func FileSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f KB", float64(n)/1024)
}
