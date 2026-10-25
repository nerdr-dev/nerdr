package review

// ask.go turns "a" and "s" on what is on screen — a line, a file or the
// whole change — into a message for the agent working in the worktree.
// The panel runs no agent of its own: the tab's agent is the one that may
// be wrapped in a sandbox, and it already has the worktree and the
// conversation for context.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// AgentMsg asks nerdr to put Text to the agent working in Dir. nerdr
// shows it to the user to edit and send, so nothing reaches an agent
// unseen — the text can carry what others wrote (a review comment, a CI
// log).
type AgentMsg struct {
	Dir  string
	Text string
}

// askAgent is the command that hands text to nerdr for the worktree's
// agent.
func (m *Model) askAgent(text string) tea.Cmd {
	if m.wtIdx < 0 || m.wtIdx >= len(m.worktrees) {
		return nil
	}
	msg := AgentMsg{Dir: m.worktrees[m.wtIdx].Path, Text: text}
	return func() tea.Msg { return msg }
}

// The questions "a" asks, by what it is asked on.
const (
	lineQuestion   = "Briefly explain what this line does and why it is written this way."
	fileQuestion   = "What does this change to the file do, and what should a reviewer look at closely?"
	changeQuestion = "What does this change do as a whole, and in what order should a reviewer read it?"
)

// askSelected is "a" (question set) and "s" (question empty, for the user
// to write) on whatever you are standing on: a line in the diff, the file
// in the file list, or the whole change in the worktree list. The same
// keys, a larger subject — the question you have is rarely about a line
// when you have not opened one.
func (m *Model) askSelected(ask bool) tea.Cmd {
	if m.wtIdx < 0 || m.wtIdx >= len(m.worktrees) {
		return nil // nothing to ask about, and againstBase needs a worktree
	}
	var subject, question string
	switch m.focus {
	case focusFiles:
		file, ok := m.currentFile()
		if !ok {
			return nil
		}
		subject, question = "About the change to "+file+m.againstBase(), fileQuestion
	case focusWorktrees, focusRepos:
		subject, question = "About the whole change in this worktree"+m.againstBase(), changeQuestion
	default:
		line, ok := m.lineUnderCursor()
		if !ok {
			return nil
		}
		subject, question = line, lineQuestion
	}
	if ask {
		return m.askAgent(subject + ": ")
	}
	return m.askAgent(subject + ": " + question)
}

// againstBase says what the change is compared with, so the agent reads
// the same diff as the panel shows.
func (m *Model) againstBase() string {
	if base := m.comparison().Base; base != "" {
		return " (git diff --merge-base " + base + ")"
	}
	return " (git diff HEAD)"
}

// lineUnderCursor names the code line the diff cursor is on, or reports
// why it can't be asked about.
func (m *Model) lineUnderCursor() (string, bool) {
	idx := clamp(m.diffCursor, 0, max(0, len(m.diffLines)-1))
	if idx < 0 || idx >= len(m.diffLines) {
		return "", false
	}
	dl := m.diffLines[idx]
	// Only real code lines are worth asking about — not hunk headers, our
	// own ✎/↳ annotations, or the diff's file banner.
	if dl.kind != '+' && dl.kind != '-' && dl.kind != ' ' {
		m.setToast("select a line of code to ask about")
		return "", false
	}
	code := strings.TrimSpace(strings.TrimLeft(dl.text, "+- "))
	if code == "" {
		m.setToast("empty line")
		return "", false
	}
	file, _ := m.currentFile()
	if m.peek != nil {
		file = m.peek.file
	}
	where := file
	if dl.newLine > 0 {
		where = fmt.Sprintf("%s:%d", file, dl.newLine)
	}
	if dl.kind == '-' {
		where = file + ", a removed line"
	}
	return fmt.Sprintf("About `%s` in %s", code, where), true
}
