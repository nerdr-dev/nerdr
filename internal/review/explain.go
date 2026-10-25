package review

import (
	tea "github.com/charmbracelet/bubbletea"

	"nerdr/internal/browser"
	"nerdr/internal/git"
	"nerdr/internal/order"
	"nerdr/internal/walkthrough"
)

// ExplainMsg asks nerdr to show what was recorded to explain a file — the
// line on the part it plays, which the file list cuts to the width of the
// pane, and the notes on lines in it — whole, in a dialog.
type ExplainMsg struct {
	File  string
	Role  string // the part it plays, as the file list names it; "" for none
	Short string // what the reading order says it is for
	Notes []string
}

// explainRequest is what a asks the agent: the words nerdr-explain's skill
// is used on.
const explainRequest = "Explain the code for nerdr (the nerdr-explain skill)."

// askExplain is E: a message asking the worktree's agent to explain its
// change for the panel, with the nerdr-explain skill.
func (m *Model) askExplain() tea.Cmd {
	if !m.explainOn {
		m.setToast(m.explainOff())
		return nil
	}
	return m.askAgent(explainRequest)
}

// explainOff is what is said for E, and for an explanation missing, while
// the agents do not have the skill.
func (m Model) explainOff() string {
	return "the nerdr-explain skill is off — turn it on in " + m.screenKey(3) + " skills"
}

// askForOne is how to get an explanation that is missing.
func (m Model) askForOne() string {
	if !m.explainOn {
		return m.explainOff()
	}
	return "E asks the agent for one"
}

// explainFile is e: the explanation of the file under the cursor, whole.
func (m *Model) explainFile() tea.Cmd {
	file, ok := m.currentFile()
	if !ok {
		return nil
	}
	msg, ok := m.explanation()
	if !ok {
		m.setToast("no explanation of " + baseName(file) + " — " + m.askForOne())
		return nil
	}
	return func() tea.Msg { return msg }
}

// explanation is what was recorded to explain the file under the cursor,
// and whether there is any.
func (m *Model) explanation() (ExplainMsg, bool) {
	file, ok := m.currentFile()
	if !ok {
		return ExplainMsg{}, false
	}
	msg := ExplainMsg{File: file}
	if e, ok := m.orderIndex.For(file); ok {
		msg.Role, msg.Short = order.Label(e.Role), e.Short
	}
	for _, n := range m.whyIndex.All() {
		if n.File == file {
			msg.Notes = append(msg.Notes, n.Short)
		}
	}
	return msg, msg.Short != "" || len(msg.Notes) > 0
}

// walkthroughRequest is what g asks the agent when there is no
// walkthrough: the words the skill's second mode is used on.
const walkthroughRequest = "Make a walkthrough of the change for nerdr (the nerdr-explain skill, walkthrough mode)."

// openWalkthrough is g: the walkthrough the agent wrote of the change,
// drawn as a page from the change as it is now and opened in the browser.
// With none written yet, it asks the agent for one, as E asks for an
// explanation, and the page opens once it is written (walkArrived).
func (m *Model) openWalkthrough() tea.Cmd {
	if m.wtIdx < 0 || m.wtIdx >= len(m.worktrees) {
		return nil
	}
	wt := m.worktrees[m.wtIdx]
	if !m.walk {
		if !m.explainOn {
			m.setToast("no walkthrough of this branch — " + m.explainOff())
			return nil
		}
		if m.walkWanted == wt.Path && !m.walkNudged {
			m.walkNudged = true
			m.setToast("the agent is writing the walkthrough — it opens when it is done; g again asks anew")
			return nil
		}
		m.walkWanted, m.walkNudged = wt.Path, false
		m.setToast("asked the agent for a walkthrough — it opens when it is done")
		return m.askAgent(walkthroughRequest)
	}
	m.walkWanted = ""
	return showWalkthrough(wt)
}

// walkArrived opens the walkthrough g asked for, once it is there.
func (m *Model) walkArrived() tea.Cmd {
	if !m.walk || m.walkWanted == "" || m.wtIdx < 0 || m.wtIdx >= len(m.worktrees) ||
		m.worktrees[m.wtIdx].Path != m.walkWanted {
		return nil
	}
	m.walkWanted = ""
	return showWalkthrough(m.worktrees[m.wtIdx])
}

// showWalkthrough draws worktree wt's walkthrough and opens it.
func showWalkthrough(wt git.Worktree) tea.Cmd {
	return func() tea.Msg {
		w, ok := walkthrough.Load(wt.Path, wt.Branch)
		if !ok {
			return toastMsg{"no walkthrough of this branch any more"}
		}
		path, err := walkthrough.Write(wt.Path, w)
		if err != nil {
			return toastMsg{"walkthrough: " + err.Error()}
		}
		if err := browser.OpenFile(path); err != nil {
			return toastMsg{browser.Command(path)[0] + ": " + err.Error()}
		}
		return toastMsg{"opened the walkthrough in the browser"}
	}
}
