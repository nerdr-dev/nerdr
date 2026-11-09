package app

import (
	tea "github.com/charmbracelet/bubbletea"
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
		if a := m.Core.ByRef(ev.A); a != nil {
			m.notifyChange(a, ev.Visible, ev.Now)
		}
	case core.ReviewNewsMsg:
		m.Core.Gen++
		if t := m.Core.ByRef(ev.T); t != nil {
			m.notifyReview(t, ev.Text)
		}
	case core.ReviewMsg:
		m.Core.Gen++
		return toCore(m.pinReview())
	case core.PaneOpenedMsg:
		m.Core.Gen++
		if t, p := m.Core.PaneByRef(ev.P); t != nil {
			return toCore(m.focusPane(t, p))
		}
	case core.PaneClosedMsg:
		m.Core.Gen++
		if t := m.Core.ByRef(ev.T); t != nil {
			m.paneClosed(t, ev.P)
		}
	case core.PanesMsg:
		m.Core.Gen++
		m.resizePanes()
	case core.LayoutMsg:
		m.Core.Gen++
		return toCore(m.resize())
	case core.SetupAnsweredMsg:
		return toCore(m.setupAnswered(ev.Token))
	case core.PluginFactsMsg:
		return toCore(m.onPluginJob(ev.Job, ev))
	case core.PluginAllowedMsg:
		return toCore(m.onPluginJob(ev.Job, ev))
	case core.UsageMsg:
		m.onUsage(ev)
	case core.PromptsMsg:
		m.onSent(ev)
	case core.ConversationsReadMsg, core.SearchHitsMsg:
		return toCore(m.onSearchAnswer(ev))
	case core.ConfigSavedMsg:
		return toCore(m.onConfigSaved(ev))
	case core.ConfigTextMsg:
		m.onConfigText(ev)
	case core.ContextMsg:
		m.onContext(ev)
	case core.ContextFileMsg:
		m.onContextFile(ev)
	case core.WatchingMsg:
		m.review = m.review.Watching(ev.Wt, ev.OK)
	case core.WorktreeChangedMsg:
		var cmd tea.Cmd
		m.review, cmd = m.review.Changed(ev.Wt)
		return toCore(cmd)
	case core.UnseenMsg:
		if t := m.Core.ByRef(ev.T); t != nil && t == m.Cur() {
			m.review = m.review.SetUnseen(t.Unseen)
		}
	case core.ChangesMsg:
		m.onCommitFiles(ev)
	case core.ReposScannedMsg:
		return toCore(m.rescanned())
	case core.RepoMadeMsg:
		return toCore(m.repoMade(ev))
	case core.RepoCheckedMsg:
		m.onRepoChecked(ev)
	case core.ReviewReadMsg:
		if m.reviewAsk != nil {
			m.reviewAsk.Answer(ev.Job, ev.A)
		}
	case core.PlanMsg, core.IssueMsg, core.SetupProgressMsg, core.WorktreesMadeMsg:
		return toCore(m.wizardAnswer(ev))
	case core.CheckoutMsg, core.ForkFetchedMsg:
		return toCore(m.prAnswer(ev))
	case core.RepoPRsMsg:
		m.onWizardPRs(ev)
	case core.PRFetchedMsg:
		m.onPRFetched(ev)
	case core.CloseInfoMsg:
		m.onCloseInfo(ev)
	case core.BranchDoneMsg:
		return toCore(m.prDone(ev))
	case core.SpinoffFailedMsg:
		if t := m.Core.ByRef(ev.T); t != nil {
			m.spinoffFailed(t, ev.Branch, ev.Err)
		}
	case core.SpinoffNextMsg:
		if t := m.Core.ByRef(ev.T); t != nil {
			return toCore(m.spinoffMoved(t, ev.Left))
		}
	case core.ReposDoneMsg:
		if t := m.Core.ByRef(ev.T); t != nil {
			return toCore(m.reposDone(t, ev.Text))
		}
	case core.TypedInMsg:
		if a := m.Core.ByRef(ev.A); a != nil {
			return toCore(m.typedIn(a, ev.Enter))
		}
	case core.StartedMsg:
		// Back at the top of a fresh program, which has the keyboard if its
		// pane does.
		a := m.Core.ByRef(ev.A)
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
		m.Core.Gen++
		if t := m.Core.ByRef(ev.T); t != nil {
			m.focusTerms(t)
		}
	case core.SwitchedMsg:
		m.Core.Gen++
		m.stopScrolling()
		m.setFocus(core.FocusAgent)
	case core.IssuesMsg:
		m.Core.Gen++
		m.applyIssues()
	case core.IssueNewsMsg:
		m.Core.Gen++
		m.tellIssueNews(ev.News)
	case core.IssuesDueMsg:
		return toCore(m.refreshAllIssues(false))
	case core.PRsMsg:
		m.Core.Gen++
		m.applyPRs()
	case core.PRNewsMsg:
		m.Core.Gen++
		m.tellPRNews(ev.News)
	case core.PRMarkMsg:
		m.Core.Gen++
		m.review.SetPRNews(len(m.Core.PRWatch.News) > 0)
	case core.OverlapsMsg:
		m.Core.Gen++
		m.shareOverlaps()
	case core.DetectStartMsg:
		m.herdrBefore = m.Herdr.State()
		m.look()
	case core.DetectDoneMsg:
		return toCore(m.detected(ev.Now))
	case core.AskMsg:
		m.Core.Gen++
		switch ev.Q.Kind {
		case core.AskRepoSetup:
			m.askRepoSetup(ev.Q)
		case core.AskPluginRun:
			m.askPluginRun(ev.Q)
		}
	case core.AnsweredMsg:
		m.Core.Gen++
		if q := m.plugins.asking; q != nil && q.q.ID == ev.ID {
			q.answered(m)
		}
	case core.SpinoffAskMsg:
		m.Core.Gen++
		if t := m.Core.ByRef(ev.T); t != nil {
			return toCore(m.openSpinoff(t))
		}
	case core.SwitchAskMsg:
		m.Core.Gen++
		return toCore(m.openSwitchAgent())
	case core.PluginUIStartedMsg:
		m.Core.Gen++
		if t := m.Core.ByRef(ev.T); t != nil && m.plugins.uiFocus == ev.Key {
			m.pluginUIFocus(t) // started again in place of the one it had
		}
	case core.PluginUIGoneMsg:
		m.Core.Gen++
		if m.plugins.uiFocus == ev.Key {
			m.plugins.uiFocus = ""
		}
	case core.PluginRunMsg:
		m.Core.Gen++
		if t, run := m.Core.ByRef(ev.T), m.Core.PluginRuns.ByID[ev.Run]; t != nil && run != nil {
			return toCore(m.showPluginRun(t, ev.P, run))
		}
	}
	return nil
}

// expandKeys puts the keys in a text of core's, as this TUI has them.
func (m *Model) expandKeys(s string) string { return core.ExpandKeys(s, m.pk, m.screenKey) }

func (m *Model) Front() core.TabRef { return m.Core.Ref(m.Cur()) }

func (m *Model) TabShown(r core.TabRef) bool {
	t := m.Core.ByRef(r)
	return t != nil && m.tabShown(t)
}

func (m *Model) AgentVisible(r core.TabRef) bool {
	a := m.Core.ByRef(r)
	return a != nil && m.agentVisible(a)
}

func (m *Model) Layout() core.Layout { return m.layout() }

func (m *Model) Sight() core.Sight {
	return core.Sight{Keyboard: m.Core.Ref(m.CurAgent()), Beside: m.Core.Ref(m.splitTab()), IssuesShown: m.issuesShown(), PRsShown: m.prsShown()}
}

func (m *Model) ShellSize(tr core.TabRef, pr core.PaneRef) (int, int) {
	t := m.Core.ByRef(tr)
	if o, p := m.Core.PaneByRef(pr); t != nil && o != nil {
		return m.shellSize(t, p)
	}
	return 0, 0
}

func (m *Model) TabSize(r core.TabRef) (int, int) {
	if a := m.Core.ByRef(r); a != nil {
		return m.tabSize(a)
	}
	return 0, 0
}

func (m *Model) DialogClosed() bool { return m.dialogClosed() }
