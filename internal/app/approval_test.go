package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/codereview"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/state"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestApprovalRemarksReachAuthor(t *testing.T) {
	for _, mode := range []string{config.FindingsAuto, "ask"} {
		for _, rounds := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/%d", mode, rounds), func(t *testing.T) {
				m := testModel(t)
				m.home().Core.Cfg.Review.SendFindings = mode
				tb := m.Cur()
				tb.Messages = []state.Message{{Text: "queued task"}}
				r := &core.Tab{AgentState: core.Agent{Tab: state.Tab{ID: "approval-reviewer", Agent: "claude"}}, ReviewOf: tb}
				tb.Review = &core.ReviewRun{Seats: []*core.Seat{{R: r}}, Round: 1, Rounds: rounds, Tree: "approved-tree"}
				res := &codereview.Result{Verdict: codereview.Approve, Summary: "Approved with small remarks",
					Findings: []codereview.Finding{{File: "a.go", Severity: codereview.Low, Text: "keep this remark"}}}
				m.home().Core.OnReview(hooks.Report{Pane: r.HookKey(), Review: res})
				if tb.Review != nil || tb.Reviewed != "approved-tree" {
					t.Fatal("approval should end the review")
				}
				if len(tb.Messages) != 2 || tb.Messages[1].Text != "queued task" {
					t.Fatalf("remarks must precede queued work: %+v", tb.Messages)
				}
				msg := tb.Messages[0]
				if !codereview.IsFixPrompt(msg.Text) || !strings.Contains(msg.Text, "keep this remark") || msg.Auto != (mode == config.FindingsAuto) {
					t.Fatalf("remarks not queued according to send_findings: %+v", msg)
				}
				if mode != config.FindingsAuto {
					if tb.ReviewDone == nil || tb.ReviewDone.Msg != msg {
						t.Fatal("no offer to send remarks")
					}
					m.home().Core.SendFindings(tb, msg)
					if !tb.Messages[0].Auto {
						t.Fatal("remarks could not be sent")
					}
				} else if tb.ReviewDone != nil {
					t.Fatal("automatic remarks should not ask")
				}
			})
		}
	}
}

// TestAskedReviewGoesToTheAgent: a review the agent asked for waits for
// every reviewer, then sends all the findings to the agent with
// send_findings = "ask" too, and ends after its round: the agent asks for
// another, with the same reviewers, when it wants one.
func TestAskedReviewGoesToTheAgent(t *testing.T) {
	m := testModel(t)
	tb := m.Cur()
	tb.Messages = []state.Message{{Text: "queued task"}}
	a := &core.Tab{AgentState: core.Agent{Tab: state.Tab{ID: "asked-a", Agent: "codex", Model: "gpt-6-astra"}}, ReviewOf: tb}
	b := &core.Tab{AgentState: core.Agent{Tab: state.Tab{ID: "asked-b", Agent: "agy"}}, ReviewOf: tb}
	tb.Review = &core.ReviewRun{Seats: []*core.Seat{{R: a}, {R: b}}, Round: 1, Rounds: 1, Asked: true, Tree: "asked-tree"}
	change := &codereview.Result{Verdict: codereview.Changes, Summary: "One bug",
		Findings: []codereview.Finding{{File: "a.go", Severity: codereview.High, Text: "the loop never ends"}}}
	m.home().Core.OnReview(hooks.Report{Pane: a.HookKey(), Review: change})
	if tb.Review == nil || len(tb.Messages) != 1 {
		t.Fatal("the review should wait for the other reviewer")
	}
	ok := &codereview.Result{Verdict: codereview.Approve, Summary: "Fine"}
	m.home().Core.OnReview(hooks.Report{Pane: b.HookKey(), Review: ok})
	if tb.Review != nil {
		t.Fatal("an asked review should end after its round")
	}
	msg := tb.Messages[0]
	if !msg.Auto || !strings.Contains(msg.Text, "the loop never ends") || tb.ReviewDone != nil {
		t.Fatalf("findings should go to the agent at once: %+v, done %+v", msg, tb.ReviewDone)
	}
	if again := `request-review --agent codex --model gpt-6-astra --agent agy`; !strings.Contains(msg.Text, again) ||
		strings.Contains(msg.Text, "nerdr then has") {
		t.Fatalf("the agent should be told how to ask again:\n%s", msg.Text)
	}
}

func TestApprovalWithoutRemarksDoesNotQueueMessage(t *testing.T) {
	m := testModel(t)
	tb := m.Cur()
	r := &core.Tab{AgentState: core.Agent{Tab: state.Tab{ID: "approval-reviewer", Agent: "claude"}}, ReviewOf: tb}
	tb.Review = &core.ReviewRun{Seats: []*core.Seat{{R: r}}, Round: 1, Rounds: 2}
	before := len(tb.Messages)
	m.home().Core.OnReview(hooks.Report{Pane: r.HookKey(), Review: &codereview.Result{Verdict: codereview.Approve, Summary: "fine"}})
	if tb.Review != nil || len(tb.Messages) != before {
		t.Fatal("approval without remarks should simply end the review")
	}
	if tb.ReviewDone == nil || tb.ReviewDone.Msg.Text != "" {
		t.Fatal("with send_findings = ask, the approval is told in a dialog")
	}
	m.askReviewDone()
	rd, ok := m.dialog.(*reviewDoneDialog)
	if !ok || rd.asks() {
		t.Fatalf("no dialog of the approval: %#v", m.dialog)
	}
	if v := ansi.Strip(rd.view(m, 120, 40)); !strings.Contains(v, "claude approved") || !strings.Contains(v, "fine") {
		t.Fatalf("the dialog should show the reviewer's summary:\n%s", v)
	}
	m.View()
	m.Update(tea.MouseMsg{X: 0, Y: 29, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if m.dialog != rd {
		t.Fatal("a click outside closed the dialog, which is not shown again")
	}
	rd.key(m, keyMsg("enter"))
	if m.dialog != nil {
		t.Fatal("enter closes it")
	}
}

func TestAutoApprovalWithoutRemarksDoesNotAsk(t *testing.T) {
	m := testModel(t)
	m.home().Core.Cfg.Review.SendFindings = config.FindingsAuto
	tb := m.Cur()
	r := &core.Tab{AgentState: core.Agent{Tab: state.Tab{ID: "approval-reviewer", Agent: "claude"}}, ReviewOf: tb}
	tb.Review = &core.ReviewRun{Seats: []*core.Seat{{R: r}}, Round: 1, Rounds: 2}
	m.home().Core.OnReview(hooks.Report{Pane: r.HookKey(), Review: &codereview.Result{Verdict: codereview.Approve, Summary: "fine"}})
	if tb.Review != nil || tb.ReviewDone != nil {
		t.Fatal("with send_findings = auto, approval ends the review without a dialog")
	}
}

func TestApprovalRemarksJoinRequestedChanges(t *testing.T) {
	m := testModel(t)
	tb := m.Cur()
	approved := &core.Tab{AgentState: core.Agent{Tab: state.Tab{ID: "approved", Agent: "claude"}}, ReviewOf: tb}
	changes := &core.Tab{AgentState: core.Agent{Tab: state.Tab{ID: "changes", Agent: "codex"}}, ReviewOf: tb}
	tb.Review = &core.ReviewRun{Seats: []*core.Seat{{R: approved}, {R: changes}}, Round: 1, Rounds: 2}
	m.home().Core.OnReview(hooks.Report{Pane: approved.HookKey(), Review: &codereview.Result{Verdict: codereview.Approve, Summary: "fine",
		Findings: []codereview.Finding{{File: "a.go", Severity: codereview.Low, Text: "optional remark"}}}})
	m.home().Core.OnReview(hooks.Report{Pane: changes.HookKey(), Review: &codereview.Result{Verdict: codereview.Changes, Summary: "fix",
		Findings: []codereview.Finding{{File: "b.go", Severity: codereview.High, Text: "required fix"}}}})
	if tb.Review == nil || len(tb.Review.Seats) != 1 || tb.Review.Seats[0].R != changes || tb.Review.Phase != core.Fixing {
		t.Fatal("only the reviewer requesting changes should stay")
	}
	if len(tb.Messages) != 1 || !strings.Contains(tb.Messages[0].Text, "optional remark") || !strings.Contains(tb.Messages[0].Text, "required fix") {
		t.Fatalf("both reviews must reach the author: %+v", tb.Messages)
	}
}

// TestReviewDoneOutlivesARestart: a review done in a tab not shown is still
// told of when the user comes to the tab after nerdr has been restarted,
// with its findings to send, and only once.
func TestReviewDoneOutlivesARestart(t *testing.T) {
	restart := func(t *testing.T, m *Model) *Model {
		t.Helper()
		sess, err := state.Load(m.home().Core.Root)
		if err != nil {
			t.Fatal(err)
		}
		return New(m.home().Core.Cfg, m.home().Core.Root, nil, sess)
	}
	done := func(m *Model, tb *core.Tab, res *codereview.Result) {
		r := &core.Tab{AgentState: core.Agent{Tab: state.Tab{ID: "reviewer", Agent: "codex"}}, ReviewOf: tb}
		tb.Review = &core.ReviewRun{Seats: []*core.Seat{{R: r}}, Round: 1, Rounds: 1, Tree: "t1"}
		m.home().Core.OnReview(hooks.Report{Pane: r.HookKey(), Review: res})
	}
	findings := &codereview.Result{Verdict: codereview.Changes, Summary: "needs a fix",
		Findings: []codereview.Finding{{File: "a.go", Severity: codereview.High, Text: "bug"}}}

	t.Run("findings", func(t *testing.T) {
		m := testModel(t)
		tb := m.Cur()
		id := tb.ID
		m.Active = 1 // another tab shown
		done(m, tb, findings)

		m = restart(t, m)
		m.Active = 1
		m.askReviewDone()
		if m.dialog != nil {
			t.Fatalf("asked in the wrong tab: %#v", m.dialog)
		}
		var back *core.Tab
		for _, x := range m.home().Core.Tabs {
			if x.ID == id {
				back = x
			}
		}
		if !strings.Contains(ansi.Strip(m.tabBar()), "⚑") {
			t.Fatalf("the tab's entry should be marked: %s", ansi.Strip(m.tabBar()))
		}
		m.Active = m.home().Core.TabIndex(back)
		m.askReviewDone()
		rd, ok := m.dialog.(*reviewDoneDialog)
		if !ok || !rd.asks() {
			t.Fatalf("coming to the tab should ask about the findings: %#v", m.dialog)
		}
		if v := ansi.Strip(rd.view(m, 120, 40)); !strings.Contains(v, "needs a fix") || !strings.Contains(v, "codex") {
			t.Fatalf("the dialog should show what the reviewer said:\n%s", v)
		}

		// Told of once: another restart does not ask again.
		m = restart(t, m)
		m.Active = m.home().Core.TabIndex(m.home().Core.Tabs[0])
		m.askReviewDone()
		if m.dialog != nil {
			t.Fatalf("asked again after another restart: %#v", m.dialog)
		}
		if len(m.home().Core.Tabs[0].Messages) != 1 || !m.home().Core.Tabs[0].FindingsWait() {
			t.Fatalf("the findings should still wait in F3: %+v", m.home().Core.Tabs[0].Messages)
		}
	})

	t.Run("sent from F3 before the restart", func(t *testing.T) {
		m := testModel(t)
		tb := m.Cur()
		m.Active = 1
		done(m, tb, findings)
		tb.Messages[0].Auto = true // space in F3
		m.home().Core.SaveSession()

		m = restart(t, m)
		m.Active = 0
		m.askReviewDone()
		if m.dialog != nil {
			t.Fatalf("findings already on their way should not be asked about: %#v", m.dialog)
		}
	})

	t.Run("approved", func(t *testing.T) {
		m := testModel(t)
		tb := m.Cur()
		m.Active = 1
		done(m, tb, &codereview.Result{Verdict: codereview.Approve, Summary: "fine"})

		m = restart(t, m)
		m.Active = 0
		m.askReviewDone()
		if rd, ok := m.dialog.(*reviewDoneDialog); !ok || rd.asks() {
			t.Fatalf("the approval should be told of: %#v", m.dialog)
		}
	})

	t.Run("agent beside the tab's own", func(t *testing.T) {
		m := testModel(t)
		tb := m.Cur()
		a := &core.Tab{AgentState: core.Agent{Tab: state.Tab{ID: "beside", Name: tb.Name, Agent: "codex", Dir: tb.Dir, Num: 2}}, PartOf: tb}
		tb.PaneList = append(tb.Panes(), &core.PaneTab{Kind: core.PaneAgent, A: a})
		m.Active = 1
		done(m, a, &codereview.Result{Verdict: codereview.Approve, Summary: "fine"})

		m = restart(t, m)
		m.Active = 0
		m.askReviewDone()
		if rd, ok := m.dialog.(*reviewDoneDialog); !ok || rd.t.ID != "beside" {
			t.Fatalf("the review of the agent beside should be told of: %#v", m.dialog)
		}
	})
}
