package app

import (
	"github.com/nerdr-dev/nerdr/internal/core"
)

// What core tells and asks the TUI; see core.Client. What changes the TUI's
// own state counts in Gen, as core's changes do: see reviewReuse.

// Tell takes an event of core's, there and then, with Mu held.
func (m *Model) Tell(ev core.Event) core.Cmd {
	switch ev := ev.(type) {
	case core.ToastMsg:
		m.toast(m.expandKeys(string(ev)))
	case core.NoticeMsg:
		m.Notice(m.expandKeys(string(ev)))
	case core.LightMsg:
		if a := m.ByRef(ev.A); a != nil {
			m.notifyChange(a, ev.Visible, ev.Now)
		}
	case core.ReviewNewsMsg:
		m.Gen++
		if t := m.ByRef(ev.T); t != nil {
			m.notifyReview(t, ev.Text)
		}
	case core.ReviewMsg:
		m.Gen++
		return toCore(m.pinReview())
	case core.PaneOpenedMsg:
		m.Gen++
		if t, p := m.PaneByRef(ev.P); t != nil {
			return toCore(m.focusPane(t, p))
		}
	case core.PaneClosedMsg:
		m.Gen++
		if t := m.ByRef(ev.T); t != nil {
			m.paneClosed(t, ev.P)
		}
	case core.PanesMsg:
		m.Gen++
		m.resizePanes()
	case core.LayoutMsg:
		m.Gen++
		return toCore(m.resize())
	case core.SetupAnsweredMsg:
		return toCore(m.setupAnswered(ev.Token))
	case core.UnseenMsg:
		if t := m.ByRef(ev.T); t != nil && t == m.Cur() {
			m.review = m.review.SetUnseen(t.Unseen)
		}
	case core.ChangesMsg:
		m.onCommitFiles(ev)
	case core.ReposScannedMsg:
		return toCore(m.rescanned())
	case core.RepoMadeMsg:
		return toCore(m.repoMade(ev))
	case core.CloseInfoMsg:
		m.onCloseInfo(ev)
	case core.BranchDoneMsg:
		return toCore(m.prDone(ev))
	case core.SpinoffFailedMsg:
		if t := m.ByRef(ev.T); t != nil {
			m.spinoffFailed(t, ev.Branch, ev.Err)
		}
	case core.SpinoffNextMsg:
		if t := m.ByRef(ev.T); t != nil {
			return toCore(m.spinoffMoved(t, ev.Left))
		}
	case core.ReposDoneMsg:
		if t := m.ByRef(ev.T); t != nil {
			return toCore(m.reposDone(t, ev.Text))
		}
	case core.TypedInMsg:
		if a := m.ByRef(ev.A); a != nil {
			return toCore(m.typedIn(a, ev.Enter))
		}
	case core.StartedMsg:
		// Back at the top of a fresh program, which has the keyboard if its
		// pane does.
		a := m.ByRef(ev.A)
		if a == nil {
			break
		}
		m.tl(a).scroll = 0
		if o := a.Owner(); o == m.Cur() && m.focused(o) == a.PaneOf() {
			if pr := m.program(a.PaneOf()); pr != nil {
				pr.Focus(true)
			}
		}
	case core.TermsMsg:
		m.Gen++
		if t := m.ByRef(ev.T); t != nil {
			m.focusTerms(t)
		}
	case core.SwitchedMsg:
		m.Gen++
		m.stopScrolling()
		m.setFocus(core.FocusAgent)
	case core.IssuesMsg:
		m.Gen++
		m.applyIssues()
	case core.IssueNewsMsg:
		m.Gen++
		m.tellIssueNews(ev.News)
	case core.IssuesDueMsg:
		return toCore(m.refreshAllIssues(false))
	case core.PRsMsg:
		m.Gen++
		m.applyPRs()
	case core.PRNewsMsg:
		m.Gen++
		m.tellPRNews(ev.News)
	case core.PRMarkMsg:
		m.Gen++
		m.review.SetPRNews(len(m.PRWatch.News) > 0)
	case core.OverlapsMsg:
		m.Gen++
		m.shareOverlaps()
	case core.DetectStartMsg:
		m.herdrBefore = m.Herdr.State()
		m.look()
	case core.DetectDoneMsg:
		return toCore(m.detected(ev.Now))
	case core.AskMsg:
		m.Gen++
		switch ev.Q.Kind {
		case core.AskRepoSetup:
			m.askRepoSetup(ev.Q)
		case core.AskPluginRun:
			m.askPluginRun(ev.Q)
		}
	case core.AnsweredMsg:
		m.Gen++
		if q := m.plugins.asking; q != nil && q.q.ID == ev.ID {
			q.answered(m)
		}
	case core.SpinoffAskMsg:
		m.Gen++
		if t := m.ByRef(ev.T); t != nil {
			return toCore(m.openSpinoff(t))
		}
	case core.SwitchAskMsg:
		m.Gen++
		return toCore(m.openSwitchAgent())
	case core.PluginUIStartedMsg:
		m.Gen++
		if t := m.ByRef(ev.T); t != nil && m.plugins.uiFocus == ev.Key {
			m.pluginUIFocus(t) // started again in place of the one it had
		}
	case core.PluginUIGoneMsg:
		m.Gen++
		if m.plugins.uiFocus == ev.Key {
			m.plugins.uiFocus = ""
		}
	case core.PluginRunMsg:
		m.Gen++
		if t, run := m.ByRef(ev.T), m.PluginRuns.ByID[ev.Run]; t != nil && run != nil {
			return toCore(m.showPluginRun(t, ev.P, run))
		}
	}
	return nil
}

// expandKeys puts the keys in a text of core's, as this TUI has them.
func (m *Model) expandKeys(s string) string { return core.ExpandKeys(s, m.pk, m.screenKey) }

func (m *Model) Front() core.TabRef { return m.Ref(m.Cur()) }

func (m *Model) TabShown(r core.TabRef) bool {
	t := m.ByRef(r)
	return t != nil && m.tabShown(t)
}

func (m *Model) AgentVisible(r core.TabRef) bool {
	a := m.ByRef(r)
	return a != nil && m.agentVisible(a)
}

func (m *Model) Layout() core.Layout { return m.layout() }

func (m *Model) Sight() core.Sight {
	wt, n := m.review.WatchChanges()
	return core.Sight{Keyboard: m.Ref(m.CurAgent()), Beside: m.Ref(m.splitTab()), IssuesShown: m.issuesShown(), PRsShown: m.prsShown(), WatchWt: wt, WatchN: n}
}

func (m *Model) ShellSize(tr core.TabRef, pr core.PaneRef) (int, int) {
	t := m.ByRef(tr)
	if o, p := m.PaneByRef(pr); t != nil && o != nil {
		return m.shellSize(t, p)
	}
	return 0, 0
}

func (m *Model) TabSize(r core.TabRef) (int, int) {
	if a := m.ByRef(r); a != nil {
		return m.tabSize(a)
	}
	return 0, 0
}

func (m *Model) DialogClosed() bool { return m.dialogClosed() }
