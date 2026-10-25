package app

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"nerdr/internal/agents"
	"nerdr/internal/codereview"
	"nerdr/internal/config"
	"nerdr/internal/hooks"
	"nerdr/internal/state"
	"nerdr/internal/term"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// TestReviewPaneOffersAgents: a review pane opened from the agent's lists
// the agents to review with, the config's choice first, and starts one;
// esc closes it, the keyboard back with the agent.
func TestReviewPaneOffersAgents(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	m.runCommand("t r")
	p := tb.focused()
	if p.kind != paneReview || p.of != tb || m.focus != focusAgent {
		t.Fatalf("t r should open a review pane with the keyboard: %+v", p)
	}
	view := ansi.Strip(m.View())
	first := string(m.reviewKinds(tb)[0])
	if first == "" || !strings.Contains(view, "Review claude's work") || !strings.Contains(view, "1  "+first) {
		t.Fatalf("the pane should list the agents, %s first:\n%s", first, view)
	}
	if m.handleKey(keyMsg("1")) == nil {
		t.Fatal("1 should start a review with the first agent")
	}
	m.handleKey(keyMsg("esc"))
	if len(tb.panes()) != 1 || tb.focused().a != tb {
		t.Fatal("esc should close the pane, the keyboard back with the agent")
	}
}

// TestReviewPaneNeedsAnAgent: a review is of an agent's work: from a
// terminal there is none to review.
func TestReviewPaneNeedsAnAgent(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	m.addPane(tb, &paneTab{kind: paneShell}, false)
	m.runCommand("t r")
	if len(tb.panes()) != 2 || !strings.Contains(m.toastText, "agent's pane") {
		t.Fatalf("no review pane from a terminal: %d panes, toast %q", len(tb.panes()), m.toastText)
	}
}

// TestReviewPaneShowsTheReviewer: while a review goes on its pane shows
// the reviewer below how it stands, gives it the keyboard, sizes it to
// fit, + reviewer opens another pane to pick one more, and its button
// stops the review.
func TestReviewPaneShowsTheReviewer(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	pane, err := term.Start([]string{"sh", "-c", "echo looking-at-the-diff; cat"}, t.TempDir(), nil, 40, 10, func() {})
	if err != nil {
		t.Fatal(err)
	}
	r := &tab{Tab: state.Tab{ID: tb.ID + "-review", Name: tb.Name + " · review", Agent: "codex"}, reviewOf: tb, pane: pane}
	tb.review = &reviewRun{seats: []*seat{{r: r}}, round: 1, rounds: 2}
	p := m.reviewPaneFor(tb, r)
	if tb.focused() == p {
		t.Fatal("a review that started by itself should leave the keyboard where it was")
	}
	m.goToPane(tb, p)
	if m.keyPane() != pane {
		t.Fatal("the reviewer's pane should give it the keyboard")
	}
	w, h := m.tabSize(r)
	if _, _, pw, ph := m.paneRect(tb, p); w != pw || h != ph {
		t.Fatalf("the reviewer is sized %d×%d, its pane has room for %d×%d", w, h, pw, ph)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(ansi.Strip(m.View()), "looking-at-the-diff") {
		if time.Now().After(deadline) {
			t.Fatalf("the reviewer's screen is not shown:\n%s", ansi.Strip(m.View()))
		}
		time.Sleep(20 * time.Millisecond)
	}
	clickOn(t, m, "+ reviewer", tea.MouseButtonLeft)
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "beside code") || strings.Contains(view, "  codex ·") || m.keyPane() != nil {
		t.Fatalf("+ reviewer should list the other agents, with the keyboard:\n%s", view)
	}
	m.handleKey(keyMsg("esc"))
	if m.keyPane() != pane {
		t.Fatal("esc should go back to the reviewer")
	}
	clickOn(t, m, "stop the review", tea.MouseButtonLeft)
	if tb.review != nil || len(tb.panes()) != 1 {
		t.Fatalf("the button should stop the review and close its pane: %d panes", len(tb.panes()))
	}
}

// TestReviewersSideBySide: with two reviewers, a round ends when both
// have sent their review; the findings go to the agent in one message, the
// one that approved is let go, and the other looks again at the fixes.
func TestReviewersSideBySide(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	reviewer := func(k agents.Kind) *tab {
		r := &tab{Tab: state.Tab{ID: tb.ID + "-review-" + string(k), Agent: k}, reviewOf: tb}
		r.hookKey()
		return r
	}
	codex, gemini := reviewer("codex"), reviewer("gemini")
	tb.review = &reviewRun{seats: []*seat{{r: codex}, {r: gemini}}, round: 1, rounds: 2, tree: "t1"}
	held := len(tb.Messages)

	m.onReview(hooks.Report{Pane: codex.hookID, Review: &codereview.Result{Verdict: codereview.Changes, Summary: "c",
		Findings: []codereview.Finding{{File: "a.go", Severity: codereview.High, Text: "wrong"}}}})
	if tb.review.phase != reviewing || len(tb.Messages) != held {
		t.Fatal("the round should wait for gemini's review")
	}
	if line := m.reviewLine(tb); !strings.Contains(line, "codex: 1 finding") || !strings.Contains(line, "gemini") {
		t.Fatalf("the line should say how each stands: %s", line)
	}

	m.onReview(hooks.Report{Pane: gemini.hookID, Review: &codereview.Result{Verdict: codereview.Approve, Summary: "fine"}})
	run := tb.review
	if run == nil || run.phase != fixing || len(run.seats) != 1 || run.seats[0].r != codex {
		t.Fatalf("codex's findings should be with the agent, and gemini let go: %+v", run)
	}
	if len(tb.Messages) != held+1 || !strings.Contains(tb.Messages[0].Text, "codex reviewed your change") {
		t.Fatalf("one message with codex's findings should go to the agent: %+v", tb.Messages)
	}
	if line := m.reviewLine(tb); !strings.Contains(line, "gemini: approved") {
		t.Fatalf("the line should keep gemini's approval: %s", line)
	}

	tb.Messages = tb.Messages[1:] // sent
	m.onTree(treeMsg{t: tb, why: treeFixed, tree: "t2"})
	if run.round != 2 || run.phase != reviewing || len(codex.Messages) != 1 || len(gemini.Messages) != 0 {
		t.Fatalf("only codex should review again, in round 2: round %d, %d, %d", run.round, len(codex.Messages), len(gemini.Messages))
	}
}

// TestReviewerLeavesTheRound: a reviewer that stops before it sends a
// review leaves the others to end the round.
func TestReviewerLeavesTheRound(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	codex := &tab{Tab: state.Tab{ID: "x-codex", Agent: "codex"}, reviewOf: tb}
	gemini := &tab{Tab: state.Tab{ID: "x-gemini", Agent: "gemini"}, reviewOf: tb}
	ok := &codereview.Result{Verdict: codereview.Approve, Summary: "fine"}
	tb.review = &reviewRun{seats: []*seat{{r: codex, res: ok}, {r: gemini}}, round: 1, rounds: 2}
	m.dropReviewer(tb, gemini, "gemini stopped", false)
	if tb.review != nil || !strings.Contains(tb.ReviewBrief, "codex: approved") {
		t.Fatalf("with gemini gone codex's approval should end the review: %+v, %q", tb.review, tb.ReviewBrief)
	}
}

// TestRoundsInAReviewPane covers choosing the rounds in a review pane: -
// and + change them, in the config file too, from 1 to maxRounds, and a
// review going on never below the round it is in.
func TestRoundsInAReviewPane(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := testModel(t)
	m.cfg.Review.Rounds = 2
	tb := m.cur()
	m.runCommand("t r")
	p := tb.focused()
	m.handleKey(keyMsg("+"))
	if m.cfg.Review.Rounds != 3 {
		t.Fatalf("+ gives %d rounds", m.cfg.Review.Rounds)
	}
	if c, err := config.Load(); err != nil || c.Review.Rounds != 3 {
		t.Fatalf("saved %d rounds (%v)", c.Review.Rounds, err)
	}
	if v := ansi.Strip(strings.Join(m.reviewPaneLines(tb, p, 100, 30), "\n")); !strings.Contains(v, "Rounds:  −  3  + ") {
		t.Fatalf("the pane shows the rounds: %q", v)
	}
	for range 20 {
		m.handleKey(keyMsg("-"))
	}
	if m.cfg.Review.Rounds != 1 {
		t.Fatalf("down to %d rounds", m.cfg.Review.Rounds)
	}
	for range 20 {
		m.handleKey(keyMsg("right"))
	}
	if m.cfg.Review.Rounds != maxRounds {
		t.Fatalf("up to %d rounds", m.cfg.Review.Rounds)
	}

	tb.review = &reviewRun{round: 2, rounds: 3}
	m.setRounds(tb, -1)
	m.setRounds(tb, -1)
	if tb.review.rounds != 2 {
		t.Fatalf("a review in round 2 has %d rounds", tb.review.rounds)
	}
}

// TestReviewDoneAsks: a review that ends a round with findings for the user
// to send asks, in the tab, whether to send them now or keep them in F3 —
// at once in the tab shown, and in another tab when the user comes back to
// it.
func TestReviewDoneAsks(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	other := &tab{Tab: state.Tab{ID: "other", Name: "other", Agent: agents.Claude}}
	m.tabs = append(m.tabs, other)
	codex := &tab{Tab: state.Tab{ID: tb.ID + "-review-codex", Agent: "codex"}, reviewOf: tb}
	codex.hookKey()
	changes := &codereview.Result{Verdict: codereview.Changes, Summary: "c",
		Findings: []codereview.Finding{{File: "a.go", Severity: codereview.High, Text: "wrong"}}}

	m.active = m.tabIndex(other)
	tb.review = &reviewRun{seats: []*seat{{r: codex}}, round: 1, rounds: 2, tree: "t1"}
	m.onReview(hooks.Report{Pane: codex.hookID, Review: changes})
	m.askReviewDone()
	if m.dialog != nil {
		t.Fatal("the review of a tab not shown should not ask yet")
	}
	m.active = m.tabIndex(tb)
	m.askReviewDone()
	rd, ok := m.dialog.(*reviewDoneDialog)
	if !ok || !rd.asks() {
		t.Fatalf("coming back to the tab should ask: %#v", m.dialog)
	}
	if v := ansi.Strip(rd.view(m, 120, 40)); !strings.Contains(v, "Review of") || !strings.Contains(v, "codex 1 finding (1 high)") {
		t.Fatalf("the dialog should say what the reviewer found:\n%s", v)
	}
	rd.key(m, keyMsg("1"))
	run := tb.review
	if i := tb.findMessage(run.fixMsg); i < 0 || !tb.Messages[i].Auto {
		t.Fatalf("1 should send the findings, as the review still knows them: %+v", tb.Messages)
	}
	if line := m.reviewLine(tb); !strings.Contains(line, "is on it") {
		t.Fatalf("the line should say the agent has them: %s", line)
	}
	m.dialog = nil
	m.askReviewDone()
	if m.dialog != nil {
		t.Fatal("asked once only")
	}

	// The last round's findings wait in F3 with the review over; the brief
	// it leaves does not say they are there, since they will not stay.
	tb.Messages = nil
	m.onTree(treeMsg{t: tb, why: treeFixed, tree: "t2"})
	m.onReview(hooks.Report{Pane: codex.hookID, Review: changes})
	if tb.review != nil || strings.Contains(tb.ReviewBrief, "F3") {
		t.Fatalf("the review should be over, its brief not pointing at F3: %q", tb.ReviewBrief)
	}
	m.askReviewDone()
	rd, ok = m.dialog.(*reviewDoneDialog)
	if !ok {
		t.Fatal("the last round should ask too")
	}
	rd.key(m, keyMsg("2"))
	if len(tb.Messages) != 1 || tb.Messages[0].Auto {
		t.Fatalf("2 should keep the findings in F3: %+v", tb.Messages)
	}
}

// TestReviewDoneWaitsForTheWalk: walking the tabs with the prefix, the
// tab of a review that is done is only passed, and the enter that ends
// the walk there does not answer the question unseen: it is asked once
// the walk is over. The review of a tab's second agent asks too.
func TestReviewDoneWaitsForTheWalk(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	other := &tab{Tab: tb.Tab} // in the same repository, so a number walks to tb
	other.ID, other.Name, other.Messages = "other", "other", nil
	m.tabs = append(m.tabs, other)
	codex := &tab{Tab: state.Tab{ID: tb.ID + "-review-codex", Agent: "codex"}, reviewOf: tb}
	codex.hookKey()
	remarks := &codereview.Result{Verdict: codereview.Approve, Summary: "ok",
		Findings: []codereview.Finding{{File: "a.go", Severity: codereview.Low, Text: "nit"}}}

	m.active = m.tabIndex(other)
	tb.review = &reviewRun{seats: []*seat{{r: codex}}, round: 1, rounds: 1, tree: "t1"}
	m.onReview(hooks.Report{Pane: codex.hookID, Review: remarks})
	if tb.review != nil || tb.reviewDone == nil {
		t.Fatalf("approval with remarks should end the review and leave them to ask about")
	}
	m.active = m.tabIndex(tb)
	n := slices.Index(m.groupTabs(m.curGroup()), m.active) + 1
	m.active = m.tabIndex(other)
	m.startPrefix()
	m.Update(keyMsg(strconv.Itoa(n)))
	if m.cur() != tb || !m.prefix {
		t.Fatalf("walked to tab %d, prefix %v", m.active, m.prefix)
	}
	if m.dialog != nil {
		t.Fatal("asked while walking")
	}
	m.Update(keyMsg("enter"))
	if _, ok := m.dialog.(*reviewDoneDialog); !ok {
		t.Fatalf("the walk over, it should ask: %#v", m.dialog)
	}
	if len(tb.Messages) != 1 || tb.Messages[0].Auto {
		t.Fatalf("the enter that ended the walk sent the remarks: %+v", tb.Messages)
	}

	m.dialog = nil
	second := &tab{Tab: state.Tab{ID: "second", Name: tb.Name, Agent: "codex"}}
	tb.paneList = append(tb.panes(), &paneTab{kind: paneAgent, a: second})
	second.reviewDone = &reviewDone{brief: "b", msg: state.Message{Text: "fix"}}
	second.Messages = []state.Message{{Text: "fix"}}
	m.askReviewDone()
	if rd, ok := m.dialog.(*reviewDoneDialog); !ok || !strings.Contains(ansi.Strip(rd.view(m, 120, 40)), "Send them to codex") {
		t.Fatalf("the second agent's review should ask: %#v", m.dialog)
	}
}

// TestReviewLineSaysF4WhileTheFindingsWait: once a review is over, rev:
// says the findings are in F3 only while they are, a brief kept by an
// older nerdr that says so included.
func TestReviewLineSaysF4WhileTheFindingsWait(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	tb.ReviewBrief = "codex: 3 findings after 2 rounds · findings in F3"
	fix := codereview.FixPrompt([]codereview.Review{{Agent: "codex", Result: codereview.Result{Summary: "s"}}}, 2, 2)
	tb.Messages = []state.Message{{Text: fix}}
	if line := m.reviewLine(tb); !strings.Contains(line, "findings in") || strings.Count(line, "findings in") != 1 {
		t.Fatalf("the findings wait: %q", line)
	}
	tb.Messages = nil // sent before nerdr kept that it was
	if line := m.reviewLine(tb); line != "codex: 3 findings after 2 rounds" {
		t.Fatalf("the findings are sent: %q", line)
	}
}

// TestReviewLineGoesWhenTheFindingsAreDone: findings sent from F3 are
// said to be, and the brief goes once the agent's turn on them ends.
func TestReviewLineGoesWhenTheFindingsAreDone(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	tb.ReviewBrief = "codex: 3 findings after 1 round"
	fix := codereview.FixPrompt([]codereview.Review{{Agent: "codex", Result: codereview.Result{Summary: "s"}}}, 1, 1)
	tb.Messages = []state.Message{{Text: fix, Auto: true}}
	if line := m.reviewLine(tb); line != "codex: 3 findings after 1 round · sent to claude" {
		t.Fatalf("set to go: %q", line)
	}
	m.findingsDone(tb)
	if tb.ReviewBrief == "" {
		t.Fatal("the brief stays while the findings have not gone in")
	}
	tb.Messages, tb.FindingsSent = nil, true // typed in: see deliverQueue
	if line := m.reviewLine(tb); !strings.HasSuffix(line, " · sent to claude") {
		t.Fatalf("being worked on: %q", line)
	}
	m.findingsDone(tb)
	if tb.ReviewBrief != "" || tb.FindingsSent || m.reviewLine(tb) != "" {
		t.Fatalf("after the turn on them: %q, sent %v", tb.ReviewBrief, tb.FindingsSent)
	}
}

// TestTabLightCountsItsReviewers: a tab's light in the bars is its
// agent's raised by its reviewers': red over yellow over green.
func TestTabLightCountsItsReviewers(t *testing.T) {
	tb := &tab{status: stDone}
	a, b := &tab{status: stIdle, reviewOf: tb}, &tab{status: stIdle, reviewOf: tb}
	tb.review = &reviewRun{seats: []*seat{{r: a}, {r: b}}}
	for _, c := range []struct {
		own, a, b, want status
	}{
		{stDone, stIdle, stIdle, stDone},
		{stDone, stWorking, stIdle, stWorking},
		{stIdle, stIdle, stWorking, stWorking},
		{stWorking, stBlocked, stIdle, stBlocked},
		{stBlocked, stWorking, stIdle, stBlocked},
	} {
		tb.status, a.status, b.status = c.own, c.a, c.b
		if got := tb.lit(); got != c.want {
			t.Errorf("agent %s, reviewers %s and %s: light %s, want %s", c.own.label(), c.a.label(), c.b.label(), got.label(), c.want.label())
		}
	}
	tb.review = nil
	if tb.status = stDone; tb.lit() != stDone {
		t.Error("with no review the light is the agent's")
	}
}

// TestCodexHooksGoBeforeItsPrompt: a codex reviewer has its task on the
// command line after "--"; nerdr's hooks go before it, or codex takes them
// for words of the task and stops at once.
func TestCodexHooksGoBeforeItsPrompt(t *testing.T) {
	argv := append([]string{"codex", "--no-daemon"}, agents.StartPrompt(agents.Codex, "review this")...)
	got := beforePrompt(argv, hooks.CodexArgs(), 1)
	dash := slices.Index(got, "--")
	if dash < 0 || slices.Index(got, "-c") > dash || !slices.Equal(got[dash:], []string{"--", "review this"}) {
		t.Fatalf("hooks after the prompt: %q", got)
	}
	if got := beforePrompt([]string{"codex"}, []string{"-c", "x"}, 1); !slices.Equal(got, []string{"codex", "-c", "x"}) {
		t.Fatalf("no prompt: %q", got)
	}
}

func TestCodexHooksStayInsideWrapper(t *testing.T) {
	command := []string{"outer", "--", "sandbox", "--", "codex"}
	for _, tail := range [][]string{
		{"--no-daemon"},
		{"--no-daemon", "--", "review this"},
		{"resume", "--no-daemon", "session-id"},
		{"--no-daemon", "--", "--"},
	} {
		argv := slices.Concat(command, tail)
		got := beforePrompt(argv, []string{"-c", "hooks=true"}, len(command))
		at := slices.Index(tail, "--")
		if at < 0 {
			at = len(tail)
		}
		want := slices.Concat(command, tail[:at], []string{"-c", "hooks=true"}, tail[at:])
		if !slices.Equal(got, want) {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}
