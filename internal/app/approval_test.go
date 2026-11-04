package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/codereview"
	"github.com/nerdr-dev/nerdr/internal/config"
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
				m.cfg.Review.SendFindings = mode
				tb := m.cur()
				tb.Messages = []state.Message{{Text: "queued task"}}
				r := &tab{Tab: state.Tab{ID: "approval-reviewer", Agent: "claude"}, reviewOf: tb}
				tb.review = &reviewRun{seats: []*seat{{r: r}}, round: 1, rounds: rounds, tree: "approved-tree"}
				res := &codereview.Result{Verdict: codereview.Approve, Summary: "Approved with small remarks",
					Findings: []codereview.Finding{{File: "a.go", Severity: codereview.Low, Text: "keep this remark"}}}
				m.onReview(hooks.Report{Pane: r.hookKey(), Review: res})
				if tb.review != nil || tb.Reviewed != "approved-tree" {
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
					m.sendFindings(tb, msg)
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

func TestApprovalWithoutRemarksDoesNotQueueMessage(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	r := &tab{Tab: state.Tab{ID: "approval-reviewer", Agent: "claude"}, reviewOf: tb}
	tb.review = &reviewRun{seats: []*seat{{r: r}}, round: 1, rounds: 2}
	before := len(tb.Messages)
	m.onReview(hooks.Report{Pane: r.hookKey(), Review: &codereview.Result{Verdict: codereview.Approve, Summary: "fine"}})
	if tb.review != nil || len(tb.Messages) != before {
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
	m.cfg.Review.SendFindings = config.FindingsAuto
	tb := m.cur()
	r := &tab{Tab: state.Tab{ID: "approval-reviewer", Agent: "claude"}, reviewOf: tb}
	tb.review = &reviewRun{seats: []*seat{{r: r}}, round: 1, rounds: 2}
	m.onReview(hooks.Report{Pane: r.hookKey(), Review: &codereview.Result{Verdict: codereview.Approve, Summary: "fine"}})
	if tb.review != nil || tb.ReviewDone != nil {
		t.Fatal("with send_findings = auto, approval ends the review without a dialog")
	}
}

func TestApprovalRemarksJoinRequestedChanges(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	approved := &tab{Tab: state.Tab{ID: "approved", Agent: "claude"}, reviewOf: tb}
	changes := &tab{Tab: state.Tab{ID: "changes", Agent: "codex"}, reviewOf: tb}
	tb.review = &reviewRun{seats: []*seat{{r: approved}, {r: changes}}, round: 1, rounds: 2}
	m.onReview(hooks.Report{Pane: approved.hookKey(), Review: &codereview.Result{Verdict: codereview.Approve, Summary: "fine",
		Findings: []codereview.Finding{{File: "a.go", Severity: codereview.Low, Text: "optional remark"}}}})
	m.onReview(hooks.Report{Pane: changes.hookKey(), Review: &codereview.Result{Verdict: codereview.Changes, Summary: "fix",
		Findings: []codereview.Finding{{File: "b.go", Severity: codereview.High, Text: "required fix"}}}})
	if tb.review == nil || len(tb.review.seats) != 1 || tb.review.seats[0].r != changes || tb.review.phase != fixing {
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
		sess, err := state.Load(m.root)
		if err != nil {
			t.Fatal(err)
		}
		return New(m.cfg, m.root, nil, sess)
	}
	done := func(m *Model, tb *tab, res *codereview.Result) {
		r := &tab{Tab: state.Tab{ID: "reviewer", Agent: "codex"}, reviewOf: tb}
		tb.review = &reviewRun{seats: []*seat{{r: r}}, round: 1, rounds: 1, tree: "t1"}
		m.onReview(hooks.Report{Pane: r.hookKey(), Review: res})
	}
	findings := &codereview.Result{Verdict: codereview.Changes, Summary: "needs a fix",
		Findings: []codereview.Finding{{File: "a.go", Severity: codereview.High, Text: "bug"}}}

	t.Run("findings", func(t *testing.T) {
		m := testModel(t)
		tb := m.cur()
		id := tb.ID
		m.active = 1 // another tab shown
		done(m, tb, findings)

		m = restart(t, m)
		m.active = 1
		m.askReviewDone()
		if m.dialog != nil {
			t.Fatalf("asked in the wrong tab: %#v", m.dialog)
		}
		var back *tab
		for _, x := range m.tabs {
			if x.ID == id {
				back = x
			}
		}
		if !strings.Contains(ansi.Strip(m.tabBar()), "⚑") {
			t.Fatalf("the tab's entry should be marked: %s", ansi.Strip(m.tabBar()))
		}
		m.active = m.tabIndex(back)
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
		m.active = m.tabIndex(m.tabs[0])
		m.askReviewDone()
		if m.dialog != nil {
			t.Fatalf("asked again after another restart: %#v", m.dialog)
		}
		if len(m.tabs[0].Messages) != 1 || !m.tabs[0].findingsWait() {
			t.Fatalf("the findings should still wait in F3: %+v", m.tabs[0].Messages)
		}
	})

	t.Run("sent from F3 before the restart", func(t *testing.T) {
		m := testModel(t)
		tb := m.cur()
		m.active = 1
		done(m, tb, findings)
		tb.Messages[0].Auto = true // space in F3
		m.saveSession()

		m = restart(t, m)
		m.active = 0
		m.askReviewDone()
		if m.dialog != nil {
			t.Fatalf("findings already on their way should not be asked about: %#v", m.dialog)
		}
	})

	t.Run("approved", func(t *testing.T) {
		m := testModel(t)
		tb := m.cur()
		m.active = 1
		done(m, tb, &codereview.Result{Verdict: codereview.Approve, Summary: "fine"})

		m = restart(t, m)
		m.active = 0
		m.askReviewDone()
		if rd, ok := m.dialog.(*reviewDoneDialog); !ok || rd.asks() {
			t.Fatalf("the approval should be told of: %#v", m.dialog)
		}
	})

	t.Run("agent beside the tab's own", func(t *testing.T) {
		m := testModel(t)
		tb := m.cur()
		a := &tab{Tab: state.Tab{ID: "beside", Name: tb.Name, Agent: "codex", Dir: tb.Dir, Num: 2}, partOf: tb}
		tb.paneList = append(tb.panes(), &paneTab{kind: paneAgent, a: a})
		m.active = 1
		done(m, a, &codereview.Result{Verdict: codereview.Approve, Summary: "fine"})

		m = restart(t, m)
		m.active = 0
		m.askReviewDone()
		if rd, ok := m.dialog.(*reviewDoneDialog); !ok || rd.t.ID != "beside" {
			t.Fatalf("the review of the agent beside should be told of: %#v", m.dialog)
		}
	})
}
