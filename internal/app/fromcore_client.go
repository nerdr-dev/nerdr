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
		m.notifyChange(ev.A, ev.Visible, ev.Now)
	case core.ReviewNewsMsg:
		m.Gen++
		m.notifyReview(ev.T, ev.Text)
	case core.ReviewMsg:
		m.Gen++
		return toCore(m.pinReview())
	case core.PaneOpenedMsg:
		m.Gen++
		return toCore(m.focusPane(ev.T, ev.P))
	case core.PaneClosedMsg:
		m.Gen++
		m.paneClosed(ev.T, ev.P)
		delete(m.paneLooks, ev.P)
	case core.PanesMsg:
		m.Gen++
		m.resizePanes()
	case core.LayoutMsg:
		m.Gen++
		return toCore(m.resize())
	case core.StartedMsg:
		// Back at the top of a fresh program, which has the keyboard if its
		// pane does.
		m.tl(ev.A).scroll = 0
		if o := ev.A.Owner(); o == m.Cur() && m.focused(o) == ev.A.PaneOf() {
			if p := ev.A.Pane; p != nil {
				p.Focus(true)
			}
		}
	case core.TermsMsg:
		m.Gen++
		m.focusTerms(ev.T)
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
		if q := m.plugins.asking; q != nil && q.q == ev.Q {
			q.answered(m)
		}
	case core.SpinoffAskMsg:
		m.Gen++
		return toCore(m.openSpinoff(ev.T))
	case core.SwitchAskMsg:
		m.Gen++
		return toCore(m.openSwitchAgent())
	case core.PluginUIMsg:
		m.Gen++
		m.ensurePluginUI(ev.T, ev.P, ev.Key)
	case core.PluginEventMsg:
		m.Gen++
		m.pluginEvent(ev.T, ev.Dir, ev.Ev)
	case core.PluginRunMsg:
		m.Gen++
		return toCore(m.showPluginRun(ev.T, ev.P, ev.Run))
	}
	return nil
}

// expandKeys puts the keys in a text of core's, as this TUI has them.
func (m *Model) expandKeys(s string) string { return core.ExpandKeys(s, m.pk, m.screenKey) }

func (m *Model) Front() *core.Tab { return m.Cur() }

func (m *Model) TabShown(t *core.Tab) bool { return m.tabShown(t) }

func (m *Model) AgentVisible(a *core.Tab) bool { return m.agentVisible(a) }

func (m *Model) Layout() core.Layout { return m.layout() }

func (m *Model) Sight() core.Sight {
	wt, n := m.review.WatchChanges()
	return core.Sight{Keyboard: m.CurAgent(), Beside: m.splitTab(), IssuesShown: m.issuesShown(), PRsShown: m.prsShown(), WatchWt: wt, WatchN: n}
}

func (m *Model) ShellSize(t *core.Tab, p *core.PaneTab) (int, int) { return m.shellSize(t, p) }

func (m *Model) TabSize(t *core.Tab) (int, int) { return m.tabSize(t) }

func (m *Model) DialogClosed() bool { return m.dialogClosed() }
