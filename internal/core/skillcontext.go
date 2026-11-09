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

// What F4 reads of the files the tab's agent reads, for a client whose
// copy reads no file (Copy): read here, where they are. It names each
// read by a Job of its own, and the answer by it.

func init() { registerRequests(ReadContext{}, ReadContextFile{}) }

// ReadContext reads what tab T's agent reads when it starts (see
// agents.Context): told as a ContextMsg.
type ReadContext struct {
	Job string
	T   TabRef
}

// ContextMsg is what ReadContext of Job found.
type ContextMsg struct {
	Job   string
	Items []agents.ContextItem
}

func (q ReadContext) Do(m *Core) Cmd {
	t := m.ByRef(q.T)
	if t == nil {
		return nil
	}
	agent, dir, repo := t.Agent, t.Dir, t.Info.Repo
	return Later(func() Event {
		return ContextMsg{Job: q.Job, Items: agents.Context(agent, dir, repo)}
	})
}

// ReadContextFile reads the start of File, one ReadContext found, to show
// (see skills.ReadShown): told as a ContextFileMsg.
type ReadContextFile struct {
	Job  string
	File string
}

// ContextFileMsg is the text ReadContextFile of Job read, and whether the
// file could be read.
type ContextFileMsg struct {
	Job    string
	Text   string
	Exists bool
}

func (q ReadContextFile) Do(m *Core) Cmd {
	return Later(func() Event {
		data, ok := skills.ReadShown(q.File)
		return ContextFileMsg{Job: q.Job, Text: string(data), Exists: ok}
	})
}
