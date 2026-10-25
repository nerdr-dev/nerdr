package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"nerdr/internal/agents"
	"nerdr/internal/codereview"
	"nerdr/internal/hooks"
	"nerdr/internal/state"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// paneModel is testModel with a terminal that starts, and room for two
// panes side by side.
func paneModel(t *testing.T) *Model {
	t.Helper()
	m := testModel(t)
	m.cfg.Shell = "cat"
	m.cfg.PaneMinWidth = 50
	m.cur().Dir = t.TempDir()
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
	t.Cleanup(m.Shutdown)
	return m
}

// TestOneAgentHasNoRow: a tab with its agent alone looks as it did, with
// the buttons for new panes in the agent's frame.
func TestOneAgentHasNoRow(t *testing.T) {
	m := paneModel(t)
	tb := m.cur()
	if m.rowShown(tb) {
		t.Fatal("no row over a lone agent")
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"+terminal", "+agent", "+review", "F1 changes"} {
		if !strings.Contains(view, want) {
			t.Fatalf("no %q on the screen:\n%s", want, view)
		}
	}
}

// TestPanesFoldTheOldestWhenNarrow: panes go side by side while they fit
// at pane_min_width; past that, the one used longest ago is minimised to
// the row, and the one with the keyboard is always shown.
func TestPanesFoldTheOldestWhenNarrow(t *testing.T) {
	m := paneModel(t)
	tb := m.cur()
	m.runCommand("t t")
	if m.rowShown(tb) || len(tb.panes()) != 2 || tb.focused().kind != paneShell {
		t.Fatal("t t should open a terminal beside the agent, with the keyboard, and no row while none is minimised")
	}
	agent := tb.panes()[0]
	if !m.paneShown(tb, agent) {
		t.Fatal("two panes fit side by side")
	}
	m.runCommand("t t")
	first, second := tb.panes()[1], tb.panes()[2]
	if m.paneShown(tb, agent) || !m.paneShown(tb, first) || !m.paneShown(tb, second) {
		t.Fatal("the agent, used longest ago, should be minimised; the two terminals shown")
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "▸claude") {
		t.Fatalf("the row should show the agent minimised:\n%s", v)
	}
	m.goToPane(tb, agent)
	if !m.paneShown(tb, agent) || m.paneShown(tb, first) {
		t.Fatal("going to the agent shows it, the terminal used longest ago folding in its place")
	}
	// The review panel takes room: only the pane with the keyboard is left.
	m.handleKey(keyMsg("f1"))
	if !m.paneShown(tb, agent) || m.paneShown(tb, second) {
		t.Fatal("beside the panel, the pane with the keyboard stays")
	}
}

// TestMinimiseAndClose: t m folds the pane with the keyboard into the row,
// the keyboard going to the pane beside it; a click on it in the row
// brings it back; t x stops a terminal and closes its pane; the tab's own
// agent cannot be closed that way.
func TestMinimiseAndClose(t *testing.T) {
	m := paneModel(t)
	tb := m.cur()
	m.runCommand("t t")
	sh := tb.focused()
	m.runCommand("t m")
	if !sh.small || tb.focused() == sh || m.paneShown(tb, sh) {
		t.Fatal("t m should minimise the terminal, the keyboard going to the agent")
	}
	m.runCommand("t m")
	if tb.panes()[0].small {
		t.Fatal("the last pane shown stays")
	}
	clickOn(t, m, "▸terminal", tea.MouseButtonLeft)
	if sh.small || tb.focused() != sh {
		t.Fatal("a click on it in the row should bring it back, with the keyboard")
	}
	m.runCommand("t x")
	if len(tb.panes()) != 1 || m.rowShown(tb) || tb.focused().a != tb {
		t.Fatal("t x should close the terminal, the keyboard back with the agent")
	}
	m.runCommand("t x")
	if len(tb.panes()) != 1 || !strings.Contains(m.toastText, "stays as long as the tab") {
		t.Fatalf("a tab keeps an agent: %q", m.toastText)
	}
}

// TestTabCyclesThePanes: the prefix and tab go through the panes shown,
// then to the review panel while it is open, and back.
func TestTabCyclesThePanes(t *testing.T) {
	m := paneModel(t)
	tb := m.cur()
	m.runCommand("t t")
	m.goToPane(tb, tb.panes()[0])
	m.runCommand("tab")
	if tb.focused() != tb.panes()[1] || m.focus != focusAgent {
		t.Fatal("tab should go to the terminal")
	}
	m.runCommand("tab")
	if tb.focused() != tb.panes()[0] || m.focus != focusAgent {
		t.Fatal("with the panel closed, tab from the last pane goes round to the first")
	}
	m.runCommand("tab")
	m.reviewOpen = true
	m.cfg.PaneMinWidth = 20
	m.resize()
	m.runCommand("tab")
	if m.focus != focusReview {
		t.Fatal("tab from the last pane should go to the review panel")
	}
	m.runCommand("tab")
	if m.focus != focusAgent || tb.focused() != tb.panes()[0] {
		t.Fatal("tab from the panel should go back to the first pane")
	}
	m.runCommand("t →")
	m.runCommand("t →")
	if tb.focused() != tb.panes()[0] {
		t.Fatal("t → goes round the row")
	}
}

// fakeAgent makes kind k start as cat, so a test can run it.
func fakeAgent(m *Model, k agents.Kind) {
	if m.cfg.Agents == nil {
		m.cfg.Agents = map[agents.Kind]agents.Spec{}
	}
	m.cfg.Agents[k] = agents.Spec{Command: []string{"cat"}}
}

// TestAgentBesideTheTab: another agent starts in the tab's worktree, in a
// pane of its own; it reports as itself, raises the tab's light, gets the
// tasks written while its pane has the keyboard, and is kept with the tab
// and resumed with it.
func TestAgentBesideTheTab(t *testing.T) {
	m := paneModel(t)
	fakeAgent(m, agents.Codex)
	tb := m.cur()
	tb.status = stIdle
	m.newAgent(tb, agents.Codex)
	if len(tb.extraAgents()) != 1 {
		t.Fatal("no agent beside the tab's")
	}
	a := tb.extraAgents()[0]
	if a.partOf != tb || a.Dir != tb.Dir || a.pane == nil || tb.focused().a != a {
		t.Fatalf("the agent should run in the tab's worktree, with the keyboard: %+v", a.Tab)
	}
	if got := m.agentByHook(a.hookKey()); got != a {
		t.Fatal("it should report as itself")
	}
	if m.curAgent() != a {
		t.Fatal("its pane has the keyboard: the agent keys are for it")
	}
	a.status = stBlocked
	if tb.lit() != stBlocked {
		t.Fatalf("the tab's light should be its most urgent agent's: %s", tb.lit().label())
	}
	a.status = stIdle

	m.openMessage(false)
	d := m.dialog.(*messageDialog)
	d.input.SetValue("check the tests")
	d.key(m, keyMsg("enter"))
	if len(a.Messages) != 1 || len(tb.Messages) != 0 {
		t.Fatalf("the task should be for the agent whose pane has the keyboard: %d, %d", len(a.Messages), len(tb.Messages))
	}

	a.SessionID = "s-codex"
	m.saveSession()
	sess, err := state.Load(m.root)
	if err != nil || len(sess.Tabs) == 0 {
		t.Fatalf("no session: %v", err)
	}
	var kept *state.Tab
	for i := range sess.Tabs {
		if sess.Tabs[i].ID == tb.ID {
			kept = &sess.Tabs[i]
		}
	}
	if kept == nil || len(kept.Agents) != 1 || kept.Agents[0].SessionID != "s-codex" || kept.Agents[0].Agent != agents.Codex {
		t.Fatalf("the agent should be kept with its tab: %+v", kept)
	}
	m2 := New(m.cfg, m.root, nil, sess)
	var back *tab
	for _, x := range m2.tabs {
		if x.ID == tb.ID {
			back = x
		}
	}
	if back == nil || len(back.extraAgents()) != 1 || back.extraAgents()[0].SessionID != "s-codex" || back.extraAgents()[0].partOf != back {
		t.Fatal("a restart should put the agent back in its pane")
	}

	m.closePane(tb, a.paneOf())
	if len(tb.panes()) != 1 || m.live(a) {
		t.Fatal("closing its pane should stop the agent")
	}
}

// TestReviewOfTheAgentBeside: a review pane opened from another agent's
// pane reviews that agent's work, and its findings go back to it.
func TestReviewOfTheAgentBeside(t *testing.T) {
	m := paneModel(t)
	fakeAgent(m, agents.Codex)
	fakeAgent(m, agents.Gemini)
	tb := m.cur()
	m.newAgent(tb, agents.Codex)
	a := tb.extraAgents()[0]
	m.runCommand("t r")
	p := tb.focused()
	if p.kind != paneReview || p.of != a {
		t.Fatalf("the review should be of the agent whose pane had the keyboard: %+v", p)
	}
	tb.status, a.status = stIdle, stIdle
	m.onTree(treeMsg{t: a, why: treeHand, changed: true, tree: "t1", by: reviewer{kind: agents.Gemini}})
	if a.review == nil || len(a.review.seats) != 1 || p.a != a.review.seats[0].r {
		t.Fatal("the reviewer should start in the pane it was picked in")
	}
	r := p.a
	if r.reviewOf != a || r.owner() != tb {
		t.Fatal("the reviewer reviews the agent beside, in its tab")
	}
	m.onReview(hooks.Report{Pane: r.hookKey(), Review: &codereview.Result{Verdict: codereview.Changes, Summary: "s",
		Findings: []codereview.Finding{{File: "a.go", Severity: codereview.High, Text: "wrong"}}}})
	if len(a.Messages) != 1 || len(tb.Messages) != 0 {
		t.Fatalf("the findings should go to the agent reviewed: %d, %d", len(a.Messages), len(tb.Messages))
	}
}

// TestExitClosesTheTerminal: a terminal whose shell exits goes, as a
// terminal window does, the others staying.
func TestExitClosesTheTerminal(t *testing.T) {
	m := paneModel(t)
	m.cfg.Shell = "sh"
	tb := m.cur()
	m.runCommand("t t")
	m.runCommand("t t")
	second := tb.focused()
	second.shell.Input([]byte("exit\r"))
	deadline := time.Now().Add(3 * time.Second)
	for len(tb.panes()) == 3 {
		if time.Now().After(deadline) {
			t.Fatal("the terminal did not close")
		}
		time.Sleep(20 * time.Millisecond)
		m.Update(frameMsg{})
	}
	if len(tb.panes()) != 2 || tb.panes()[1].kind != paneShell || tb.focused() != tb.panes()[1] {
		t.Fatal("the other terminal should stay, with the keyboard")
	}
}

// TestAgentsAreNumbered: each agent of a tab has a number of its own, kept
// when another goes, and named in the row.
func TestAgentsAreNumbered(t *testing.T) {
	m := paneModel(t)
	fakeAgent(m, agents.Codex)
	tb := m.cur()
	m.newAgent(tb, agents.Codex)
	m.newAgent(tb, agents.Codex)
	two, three := tb.extraAgents()[0], tb.extraAgents()[1]
	if tb.Num != 1 || two.Num != 2 || three.Num != 3 {
		t.Fatalf("numbers %d, %d, %d", tb.Num, two.Num, three.Num)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "1:claude") || !strings.Contains(v, "3:codex") {
		t.Fatalf("the row should number the agents:\n%s", v)
	}
	m.closePane(tb, two.paneOf())
	m.newAgent(tb, agents.Codex)
	if three.Num != 3 || tb.extraAgents()[1].Num != 2 {
		t.Fatal("numbers stay; a new agent gets the lowest free one")
	}
}

// TestAgentsSendEachOtherMessages: nerdr send N reaches agent N of the
// same tab as a task, saying whom it is from; a wrong number is refused
// with the agents there are.
func TestAgentsSendEachOtherMessages(t *testing.T) {
	m := paneModel(t)
	fakeAgent(m, agents.Codex)
	tb := m.cur()
	m.newAgent(tb, agents.Codex)
	two := tb.extraAgents()[0]
	send := func(from *tab, to int, text string) hooks.Answer {
		reply := make(chan hooks.Answer, 1)
		m.Update(hooks.Report{Method: hooks.Message, Pane: from.hookKey(), To: to, Text: text, Reply: reply})
		return <-reply
	}
	if a := send(tb, 2, "review parse.go"); a.Err != "" || !strings.Contains(a.Text, "Sent to agent 2") {
		t.Fatalf("answer %+v", a)
	}
	if len(two.Messages) != 1 || !two.Messages[0].Auto || !strings.Contains(two.Messages[0].Text, "Message from agent 1 (claude)") ||
		!strings.Contains(two.Messages[0].Text, "review parse.go") || !strings.Contains(two.Messages[0].Text, "nerdr send 1") {
		t.Fatalf("agent 2 should get it, from agent 1, with how to answer: %+v", two.Messages)
	}
	if a := send(two, 1, "done"); a.Err != "" || len(tb.Messages) != 1 {
		t.Fatalf("agent 2 answers agent 1: %+v, %d", a, len(tb.Messages))
	}
	if a := send(tb, 7, "hello"); !strings.Contains(a.Err, "no agent 7") || !strings.Contains(a.Err, "1:claude (you), 2:codex") {
		t.Fatalf("a wrong number: %+v", a)
	}
	if a := send(tb, 0, ""); !strings.Contains(a.Text, "1:claude (you), 2:codex") {
		t.Fatalf("no number lists them: %+v", a)
	}
}

// TestTheFirstAgentStays: the tab's first agent goes only with the tab,
// whatever other agents it has.
func TestTheFirstAgentStays(t *testing.T) {
	m := paneModel(t)
	fakeAgent(m, agents.Codex)
	tb := m.cur()
	m.newAgent(tb, agents.Codex)
	m.goToPane(tb, tb.panes()[0])
	m.runCommand("t x")
	if len(tb.panes()) != 2 || tb.Agent != agents.Claude || !strings.Contains(m.toastText, "stays as long as the tab") {
		t.Fatalf("the first agent stays: %d panes, %q", len(tb.panes()), m.toastText)
	}
}

// TestOwnConversationsOnly: an agent resumes its own conversation, never
// another's in the same worktree.
func TestOwnConversationsOnly(t *testing.T) {
	m := paneModel(t)
	fakeAgent(m, agents.Codex)
	tb := m.cur()
	tb.SessionID = "s-one"
	m.newAgent(tb, agents.Codex)
	two := tb.extraAgents()[0]
	two.SessionID = "s-two"
	two.Sessions = map[agents.Kind]string{agents.Claude: "s-two-claude"}
	skip := m.othersSessions(tb)
	if !skip["s-two"] || !skip["s-two-claude"] || skip["s-one"] {
		t.Fatalf("the tab's agent passes over the other's conversations, not its own: %v", skip)
	}
	if m.sharesKind(tb) || !m.sharesKind(two) && two.Agent == tb.Agent {
		t.Fatal("sharesKind")
	}
	m.newAgent(tb, agents.Codex)
	if !m.sharesKind(two) {
		t.Fatal("two codex agents share their kind")
	}
}

// TestSwitchTheAgentBeside: a s switches the agent whose pane has the
// keyboard, not the tab's own.
func TestSwitchTheAgentBeside(t *testing.T) {
	m := paneModel(t)
	fakeAgent(m, agents.Codex)
	fakeAgent(m, agents.Gemini)
	tb := m.cur()
	m.newAgent(tb, agents.Codex)
	two := tb.extraAgents()[0]
	two.status = stIdle
	m.switchAgent(two, agents.Gemini, "")
	if two.Agent != agents.Gemini || tb.Agent != agents.Claude || two.Sessions == nil {
		t.Fatalf("the agent beside should switch: %s, %s", two.Agent, tb.Agent)
	}
	m.runCommand("a s")
	if mu, ok := m.dialog.(*menu); !ok || !strings.Contains(mu.title, "2:gemini") {
		t.Fatalf("a s should offer to switch the agent with the keyboard: %#v", m.dialog)
	}
}

// TestClaimedConversation: an agent that reports no conversation gets the
// one found for it, unless that is another agent's of the tab.
func TestClaimedConversation(t *testing.T) {
	m := paneModel(t)
	fakeAgent(m, agents.Codex)
	tb := m.cur()
	tb.SessionID = "s-one"
	m.newAgent(tb, agents.Codex)
	two := tb.extraAgents()[0]
	if two.SessionID != "" || m.claimSession(two) == nil {
		t.Fatal("codex has no id from nerdr: it is looked for after a turn")
	}
	m.Update(claimMsg{t: two, id: "s-one", pid: two.pane})
	if two.SessionID != "" {
		t.Fatal("another agent's conversation is never claimed")
	}
	m.Update(claimMsg{t: two, id: "s-two", pid: two.pane})
	if two.SessionID != "s-two" || m.claimSession(two) != nil {
		t.Fatalf("its own is kept: %q", two.SessionID)
	}
}

// TestGivenConversationChecked: an id nerdr gave an agent that may not
// have taken it (opencode) is kept once found, or replaced by the one the
// agent is in; what nerdr typed in is noted to know it by.
func TestGivenConversationChecked(t *testing.T) {
	m := paneModel(t)
	fakeAgent(m, agents.Codex)
	tb := m.cur()
	m.newAgent(tb, agents.Codex)
	two := tb.extraAgents()[0]
	two.SessionID, two.sessionUnsure = "ses_given", true
	if m.claimSession(two) == nil {
		t.Fatal("an unsure id is checked")
	}
	m.Update(claimMsg{t: two, id: "ses_given", pid: two.pane, sure: true})
	if two.sessionUnsure || two.SessionID != "ses_given" || m.claimSession(two) != nil {
		t.Fatal("found, it is sure")
	}
	two.sessionUnsure = true
	m.Update(claimMsg{t: two, id: "ses_real", pid: two.pane})
	if two.sessionUnsure || two.SessionID != "ses_real" {
		t.Fatalf("not taken, the one it is in replaces it: %q", two.SessionID)
	}
	two.Messages = []state.Message{{Text: "fix the parser"}}
	two.status = stIdle
	m.putIn(two, 0)
	if len(two.said) != 1 || two.said[0] != "fix the parser" {
		t.Fatalf("what was typed in is noted: %q", two.said)
	}
}

// TestSwitchBackToCodexBesideAnother: switching back to codex while
// another codex works in the tab carries on its own conversation, known
// by its id; a new one, with none known, leaves no old id behind.
func TestSwitchBackToCodexBesideAnother(t *testing.T) {
	m := paneModel(t)
	fakeAgent(m, agents.Codex)
	fakeAgent(m, agents.Gemini)
	tb := m.cur()
	m.newAgent(tb, agents.Codex)
	m.newAgent(tb, agents.Codex)
	two := tb.extraAgents()[0]
	two.SessionID = "codex-two"
	two.status = stIdle
	l := launched(t, m.switchAgent(two, agents.Gemini, "summary"))
	m.Update(l)
	two.status = stIdle
	l = launched(t, m.switchAgent(two, agents.Codex, "summary"))
	if !l.resume || l.l.SessionID != "codex-two" || !slices.Contains(l.l.Argv, "codex-two") {
		t.Fatalf("its own codex conversation should be carried on: %+v", l.l)
	}

	three := tb.extraAgents()[1]
	three.SessionID = "stale"
	m.spawn(three, agents.NewSession(m.cfg.Agents, agents.Codex), false)
	if three.SessionID != "" || m.claimSession(three) == nil {
		t.Fatalf("a new codex conversation leaves no old id, and is looked for: %q", three.SessionID)
	}
}

// TestPutInGoesToTheAgent: a task put in from F3 while a review pane has
// the keyboard goes to the agent it is for, and so does the keyboard.
func TestPutInGoesToTheAgent(t *testing.T) {
	m := paneModel(t)
	tb := m.cur()
	tb.pane = runningTab(t, m).pane
	tb.status = stIdle
	m.runCommand("t r")
	if tb.focused().kind != paneReview {
		t.Fatal("the review pane should have the keyboard")
	}
	tb.Messages = []state.Message{{Text: "fix it"}}
	m.handleKey(keyMsg("f3"))
	m.putIn(tb, 0)
	if tb.focused() != tb.panes()[0] || m.focus != focusAgent || m.keyPane() != tb.pane {
		t.Fatal("the keyboard should go to the agent the task was put in")
	}
}

// TestNewPanesGoLastAndMove: a new pane goes last whichever has the
// keyboard; ctrl+q t shift+arrows move the pane with the keyboard, the
// group staying on for more, and not past the ends nor before the tab's
// own agent, which stays first.
func TestNewPanesGoLastAndMove(t *testing.T) {
	m := paneModel(t)
	fakeAgent(m, agents.Codex)
	tb := m.cur()
	tb.status = stIdle
	m.newAgent(tb, agents.Codex)
	m.runCommand("t t")
	sh := tb.focused()
	m.goToPane(tb, tb.ownPane())
	m.newAgent(tb, agents.Codex)
	ps := slices.Clone(tb.panes())
	if len(ps) != 4 || ps[2] != sh || ps[3] != tb.focused() || ps[3].a.Num != 3 {
		t.Fatal("the new agent should go last, after the terminal")
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.handleKey(keyMsg("t"))
	for range 3 {
		m.handleKey(keyMsg("shift+left"))
	}
	m.handleKey(keyMsg("enter"))
	if !slices.Equal(tb.panes(), []*paneTab{ps[0], ps[3], ps[1], ps[2]}) || m.prefix {
		t.Fatal("t ⇧← ⇧← ⇧← should move the agent to second, after the tab's own, and enter end it")
	}
	if tb.focused() != ps[3] {
		t.Fatal("moving changes the order, not the keyboard")
	}
	m.goToPane(tb, ps[0])
	m.runCommand("t ⇧→")
	if tb.panes()[0] != ps[0] || !strings.Contains(m.toastText, "stays first") {
		t.Fatal("the tab's own agent stays first")
	}
}

// TestFramesNameThePanes: with more than one pane each frame names its
// own, and the row over them is there only for the minimised ones.
func TestFramesNameThePanes(t *testing.T) {
	m := paneModel(t)
	tb := m.cur()
	m.runCommand("t t")
	v := ansi.Strip(m.View())
	if m.rowShown(tb) || !strings.Contains(v, "╭─ ") || !strings.Contains(v, "terminal") || strings.Contains(v, "▸") {
		t.Fatalf("two panes shown: named in their frames, no row:\n%s", v)
	}
	m.runCommand("t m")
	v = ansi.Strip(m.View())
	if !m.rowShown(tb) || !strings.Contains(v, "▸terminal") || strings.Contains(v, "▸claude") {
		t.Fatalf("the row should have the minimised terminal alone:\n%s", v)
	}
}

// TestShellTitle: a shell's "user@host: path" title is shortened to the
// directory, as from the worktree; another program's is kept.
func TestShellTitle(t *testing.T) {
	home, _ := os.UserHomeDir()
	wt := filepath.Join(home, "develop", "wt")
	for _, c := range []struct{ title, want string }{
		{"tor@tor-xps: ~/develop/wt", ""},
		{"tor@tor-xps: ~/develop/wt/internal/app", filepath.Join("internal", "app")},
		{"tor@tor-xps: /etc/ssh", "ssh"},
		{"tor@tor-xps:~", filepath.Base(home)},
		{"vim panes.go", "vim panes.go"},
		{"htop: 3 tasks", "htop: 3 tasks"},
		{"", ""},
	} {
		if got := shellTitle(c.title, wt); got != c.want {
			t.Errorf("shellTitle(%q) = %q, want %q", c.title, got, c.want)
		}
	}
}

// TestPanelMakesRoomForPanes: the review panel kept open is made small
// while the tab's panes would not fit beside it, comes back when they
// would, and stays when opened by hand all the same.
func TestPanelMakesRoomForPanes(t *testing.T) {
	m := paneModel(t)
	tb := m.cur()
	m.setLayout("=")
	if !m.reviewOpen || m.panelSmall {
		t.Fatal("the panel should start open")
	}
	for i := 0; i < 4 && !m.panelAuto; i++ {
		m.runCommand("t t")
	}
	if !m.panelAuto || !m.panelSmall || m.reviewOpen || len(m.paneCols(tb, 1000)) < 2 {
		t.Fatal("with more panes than fit beside it, the panel should be made small")
	}
	m.handleKey(keyMsg("f1"))
	if !m.reviewOpen || m.focus != focusReview {
		t.Fatal("an F-key still opens it, for a look")
	}
	m.goToPane(tb, tb.focused())
	if m.reviewOpen {
		t.Fatal("back at the panes, it folds again")
	}
	m.togglePanelSmall()
	if !m.reviewOpen || m.panelSmall || m.panelAuto || !m.panelKept {
		t.Fatal("« keeps it open all the same")
	}
	m.togglePanelSmall()
	m.togglePanelSmall()
	m.goToPane(tb, tb.panes()[len(tb.panes())-1])
	m.runCommand("t x")
	for len(tb.panes()) > 2 {
		m.goToPane(tb, tb.panes()[len(tb.panes())-1])
		m.runCommand("t x")
	}
	if !m.reviewOpen || m.panelSmall || m.panelAuto || m.panelKept {
		t.Fatal("once the panes fit, the panel kept open is as it was")
	}
	m.runCommand("t m")
	m.runCommand("t t")
	m.runCommand("t t")
	if !m.panelAuto {
		t.Fatal("too many panes again: small again")
	}
	m.setLayout("=")
	if !m.reviewOpen || m.panelAuto {
		t.Fatal("a layout picked shows the panel as it says")
	}
}

// TestAgentStartsAnotherAgent: nerdr agent starts another agent in the
// same tab, beside the one asking, with its task as the first message and
// how to report back; the keyboard stays where it was. A kind not there is
// refused with those there are.
func TestAgentStartsAnotherAgent(t *testing.T) {
	m := paneModel(t)
	fakeAgent(m, agents.Codex)
	tb := m.cur()
	start := func(from *tab, kind, text string) hooks.Answer {
		reply := make(chan hooks.Answer, 1)
		m.Update(hooks.Report{Method: hooks.NewAgent, Pane: from.hookKey(), Kind: kind, Text: text, Reply: reply})
		return <-reply
	}
	a := start(tb, "codex", "write the tests for parse.go")
	if a.Err != "" || !strings.Contains(a.Text, "Started agent 2 (codex)") {
		t.Fatalf("answer %+v", a)
	}
	if len(tb.extraAgents()) != 1 {
		t.Fatalf("agents beside: %d", len(tb.extraAgents()))
	}
	two := tb.extraAgents()[0]
	if tb.focused().a != tb {
		t.Fatal("the keyboard should stay with the agent it was with")
	}
	// The task is waiting for it, already typed in, or given at its start
	// (agents.StartPrompt), and then its goal.
	said := strings.Join(two.said, "\n") + "\n" + two.Goal
	for _, msg := range two.Messages {
		said += "\n" + msg.Text
	}
	if !strings.Contains(said, "Task from agent 1 (claude)") || !strings.Contains(said, "write the tests for parse.go") ||
		!strings.Contains(said, "nerdr send 1") {
		t.Fatalf("agent 2 should get the task, from agent 1, with how to answer: %q", said)
	}
	if a := start(tb, "nosuch", "x"); !strings.Contains(a.Err, `no agent "nosuch"`) || !strings.Contains(a.Err, "codex") {
		t.Fatalf("an unknown kind: %+v", a)
	}
	if a := start(two, "codex", "and another"); a.Err != "" || !strings.Contains(a.Text, "Started agent 3") {
		t.Fatalf("an agent beside starts one too, in the same tab: %+v", a)
	}
}

// TestAgentStartsAnotherWithAModel: nerdr agent --model starts the new
// agent with that model, kept with it for its next start.
func TestAgentStartsAnotherWithAModel(t *testing.T) {
	m := paneModel(t)
	fakeAgent(m, agents.Codex)
	tb := m.cur()
	reply := make(chan hooks.Answer, 1)
	m.Update(hooks.Report{Method: hooks.NewAgent, Pane: tb.hookKey(), Kind: "codex", Model: "gpt-5.5-mini", Text: "look", Reply: reply})
	if a := <-reply; a.Err != "" || !strings.Contains(a.Text, "model gpt-5.5-mini") {
		t.Fatalf("answer %+v", a)
	}
	two := tb.extraAgents()[0]
	if two.Model != "gpt-5.5-mini" {
		t.Fatalf("model %q", two.Model)
	}
	args := m.specsFor(two)[agents.Codex].Args
	if strings.Join(args, " ") != "--model gpt-5.5-mini" {
		t.Fatalf("it starts with %q", args)
	}
	if m.specsFor(tb)[agents.Claude].Args != nil {
		t.Fatal("the tab's own agent keeps the config's")
	}
}

// TestTerminalLeavesAColumn: a terminal pane's program is a column
// narrower than the pane, so the cursor at the end of a long line is not
// against the frame.
func TestTerminalLeavesAColumn(t *testing.T) {
	m := paneModel(t)
	tb := m.cur()
	m.runCommand("t t")
	p := tb.focused()
	if p.kind != paneShell || p.shell == nil {
		t.Fatal("t t should open a terminal")
	}
	_, _, w, _ := m.paneRect(tb, p)
	if got, _ := p.shell.Size(); got != w-shellMargin {
		t.Fatalf("the terminal is %d columns in a pane of %d", got, w)
	}
}

// TestDragTheDividerBetweenPanes: the column between an agent and a
// terminal beside it is dragged to share the room anew, both programs
// resized to it; a right-click on it shares it evenly again.
func TestDragTheDividerBetweenPanes(t *testing.T) {
	m := paneModel(t)
	tb := m.cur()
	m.runCommand("t t")
	ax, aw, _ := m.tabArea(tb)
	cols := m.paneCols(tb, aw)
	if len(cols) != 2 {
		t.Fatalf("want the agent and the terminal side by side, got %d panes", len(cols))
	}
	gap := ax + cols[0].x + cols[0].w
	y := m.barRows() + m.rowH(tb) + 3
	mouse := func(x int, b tea.MouseButton, a tea.MouseAction) {
		m.Update(tea.MouseMsg{X: x, Y: y, Button: b, Action: a})
	}
	mouse(gap, tea.MouseButtonLeft, tea.MouseActionPress)
	mouse(gap-20, tea.MouseButtonLeft, tea.MouseActionMotion)
	mouse(gap-20, tea.MouseButtonLeft, tea.MouseActionRelease)

	after := m.paneCols(tb, aw)
	if after[0].w != cols[0].w-20 || after[1].w != cols[1].w+20 {
		t.Fatalf("widths %d and %d, want %d and %d", after[0].w, after[1].w, cols[0].w-20, cols[1].w+20)
	}
	shell := tb.panes()[1]
	if w, _ := shell.shell.Size(); w != after[1].w-2-shellMargin {
		t.Fatalf("the terminal is %d columns in a pane of %d", w, after[1].w)
	}

	mouse(ax+after[0].x+after[0].w, tea.MouseButtonRight, tea.MouseActionPress)
	if again := m.paneCols(tb, aw); again[0].w != cols[0].w {
		t.Fatalf("a right-click should share the room evenly: %d, want %d", again[0].w, cols[0].w)
	}
}
