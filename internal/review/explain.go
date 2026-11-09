package review

import (
	_ "embed"
	"encoding/json"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nerdr-dev/nerdr/internal/browser"
	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/order"
	"github.com/nerdr-dev/nerdr/internal/walkthrough"
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

// Requests carry their own instructions, so F1 works without installed skills.
//
//go:embed prompts/explain.md
var explainRequest string

//go:embed prompts/walkthrough.md
var walkthroughRequest string

// askExplain is E: ask the worktree's agent to explain the change.
func (m *Model) askExplain() tea.Cmd {
	return m.askAgent(explainRequest)
}

// askForOne is how to get an explanation that is missing.
func (m Model) askForOne() string {
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

// openWalkthrough is g: the walkthrough the agent wrote of the change,
// drawn as a page from the change as it is now and opened in the browser.
// With none written yet, it asks the agent for one, as E asks for an
// explanation, and the page opens once it is written (walkArrived).
func (m *Model) openWalkthrough() tea.Cmd {
	if m.wtIdx < 0 || m.wtIdx >= len(m.worktrees) {
		return nil
	}
	if !m.walk {
		return m.askWalk()
	}
	if m.walkKey != m.walkBefore {
		m.walkWanted = "" // the one asked for, opened by hand
	}
	return showWalkthrough(m.src, m.worktrees[m.wtIdx], m.oldNote())
}

// oldNote is what g says after opening a walkthrough the change has moved
// on from, unless a new one is on its way.
func (m *Model) oldNote() string {
	if !m.walkOld || m.walkWanted != "" {
		return ""
	}
	return " — it is older than the change; G asks for a new one"
}

// askWalk is G, and g with no walkthrough: it asks the agent for a new
// one, which opens when it is written. Pressed again while the agent
// writes it, it says so; once more, it asks anew.
func (m *Model) askWalk() tea.Cmd {
	if m.wtIdx < 0 || m.wtIdx >= len(m.worktrees) {
		return nil
	}
	wt := m.worktrees[m.wtIdx]
	if m.walkWanted == wt.Path && !m.walkNudged {
		m.walkNudged = true
		m.setToast("the agent is writing the walkthrough — it opens when it is done; again asks anew")
		return nil
	}
	m.walkWanted, m.walkNudged, m.walkBefore = wt.Path, false, m.walkKey
	if m.walk {
		m.setToast("asked the agent for a new walkthrough — it opens when it is done")
	} else {
		m.setToast("asked the agent for a walkthrough — it opens when it is done")
	}
	return m.askAgent(walkthroughRequest)
}

// setWalk puts the walkthrough in force for the worktree shown in place:
// w, if there is one, and whether the change has moved on since (older).
func (m *Model) setWalk(w walkthrough.Walkthrough, ok, older bool) {
	m.walk, m.walkKey, m.walkOld = ok, "", false
	if !ok || m.wtIdx < 0 || m.wtIdx >= len(m.worktrees) {
		return
	}
	key, _ := json.Marshal(w)
	m.walkKey, m.walkOld = string(key), older
}

// walkArrived opens the walkthrough g or G asked for, once it is there:
// a walkthrough other than the one there was when it was asked for.
func (m *Model) walkArrived() tea.Cmd {
	if !m.walk || m.walkWanted == "" || m.walkKey == m.walkBefore || m.wtIdx < 0 || m.wtIdx >= len(m.worktrees) ||
		m.worktrees[m.wtIdx].Path != m.walkWanted {
		return nil
	}
	m.walkWanted = ""
	return showWalkthrough(m.src, m.worktrees[m.wtIdx], "")
}

// lookForWalk reads whether the walkthrough asked for is there yet,
// while the file list is off screen and so not refreshed: the agent
// writes it while you watch the agent, and it should open then, not
// once you come back to F1.
func (m *Model) lookForWalk() tea.Cmd {
	if m.f1Shown() || m.walkWanted == "" || m.wtIdx < 0 || m.wtIdx >= len(m.worktrees) {
		return nil
	}
	wt, src, paths := m.worktrees[m.wtIdx], m.src, filePaths(m.files)
	return func() tea.Msg {
		w, ok, older := src.Walkthrough(wt.Path, wt.Branch, paths)
		return walkMsg{wt: wt.Path, walk: w, ok: ok, older: older}
	}
}

// walkMsg is lookForWalk's answer for worktree wt.
type walkMsg struct {
	wt        string
	walk      walkthrough.Walkthrough
	ok, older bool
}

func (m *Model) onWalk(msg walkMsg) tea.Cmd {
	if msg.wt != m.curWT {
		return nil
	}
	m.setWalk(msg.walk, msg.ok, msg.older)
	return m.walkArrived()
}

// showWalkthrough draws worktree wt's walkthrough and opens it, saying
// note after that it did.
func showWalkthrough(src Source, wt git.Worktree, note string) tea.Cmd {
	return func() tea.Msg {
		page, ok, err := src.WalkthroughPage(wt.Path, wt.Branch)
		if !ok {
			return toastMsg{"no walkthrough of this branch any more"}
		}
		if err != nil {
			return toastMsg{"walkthrough: " + err.Error()}
		}
		// Written here, where the browser is: the worktree may be on the
		// machine of a core elsewhere.
		if err := browser.OpenPage("walkthrough-"+filepath.Base(wt.Path)+"-"+wt.Branch, page); err != nil {
			return toastMsg{browser.Command("page")[0] + ": " + err.Error()}
		}
		return toastMsg{"opened the walkthrough in the browser" + note}
	}
}
