package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"nerdr/internal/agents"
	"nerdr/internal/hooks"
	"nerdr/internal/state"
	"nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
)

// ask sends report r from agent a and returns nerdr's answer, and the
// command the report gave, if any.
func askNerdr(m *Model, a *tab, r hooks.Report) (hooks.Answer, tea.Cmd) {
	reply := make(chan hooks.Answer, 1)
	r.Pane, r.Reply = a.hookKey(), reply
	_, cmd := m.Update(r)
	return <-reply, cmd
}

// TestAgentsAndTabsAsJSON: nerdr agents lists the tab's agents with their
// state, nerdr tabs every tab, the asker's marked.
func TestAgentsAndTabsAsJSON(t *testing.T) {
	m := paneModel(t)
	fakeAgent(m, agents.Codex)
	tb := m.cur()
	m.newAgent(tb, agents.Codex)
	two := tb.extraAgents()[0]
	two.status = stWorking
	a, _ := askNerdr(m, tb, hooks.Report{Method: hooks.Query, What: hooks.QueryAgents})
	var list []agentJSON
	if err := json.Unmarshal([]byte(a.Text), &list); err != nil || len(list) != 2 {
		t.Fatalf("agents %q %v", a.Text, err)
	}
	if !list[0].You || list[0].Num != 1 || list[1].Num != 2 || list[1].Kind != "codex" || list[1].State != "working" {
		t.Fatalf("agents %+v", list)
	}
	a, _ = askNerdr(m, two, hooks.Report{Method: hooks.Query, What: hooks.QueryTabs})
	var tabs []tabJSON
	if err := json.Unmarshal([]byte(a.Text), &tabs); err != nil || len(tabs) != len(m.tabs) {
		t.Fatalf("tabs %q %v", a.Text, err)
	}
	if !tabs[0].Yours || tabs[1].Yours || len(tabs[0].Agents) != 2 || tabs[0].Branches[0].Branch != "login" {
		t.Fatalf("tabs %+v", tabs[0])
	}
}

// TestReadAndTerminal: nerdr terminal runs a command in a new terminal
// pane, without the keyboard; nerdr read gives what it, or another agent,
// shows.
func TestReadAndTerminal(t *testing.T) {
	m := paneModel(t)
	m.cfg.Shell = "sh"
	fakeAgent(m, agents.Codex)
	tb := m.cur()
	a, _ := askNerdr(m, tb, hooks.Report{Method: hooks.Terminal, Text: "echo hello-from-nerdr"})
	if a.Err != "" || !strings.Contains(a.Text, "terminal") {
		t.Fatalf("terminal: %+v", a)
	}
	if tb.focused().a != tb {
		t.Fatal("the keyboard stays with the agent")
	}
	var text string
	for range 100 {
		a, _ = askNerdr(m, tb, hooks.Report{Method: hooks.Query, What: hooks.QueryRead, Name: "terminal"})
		if text = a.Text; strings.Count(text, "hello-from-nerdr") >= 2 { // typed, then written
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if strings.Count(text, "hello-from-nerdr") < 2 {
		t.Fatalf("read terminal: %+v", a)
	}
	if a, _ = askNerdr(m, tb, hooks.Report{Method: hooks.Query, What: hooks.QueryRead, Name: "terminal 7"}); !strings.Contains(a.Err, "its terminals are terminal") {
		t.Fatalf("no such terminal: %+v", a)
	}
	if a, _ = askNerdr(m, tb, hooks.Report{Method: hooks.Query, What: hooks.QueryRead, To: 1}); !strings.Contains(a.Err, "is you") {
		t.Fatalf("reading yourself: %+v", a)
	}
}

// TestAgentMakesATab: nerdr tab new makes a branch and worktree from the
// tab's repository and a tab with an agent given the task, and tells the
// agent that asked.
func TestAgentMakesATab(t *testing.T) {
	repo := testRepo(t)
	m := tabsModel(t, state.Tab{ID: "a", Name: "main", Agent: "codex", Dir: repo,
		Worktrees: []workspace.Worktree{{Path: repo, Repo: repo, Branch: "main"}}})
	fakeAgent(m, agents.Codex)
	m.wtRoot = t.TempDir()
	tb := m.cur()
	if a, _ := askNerdr(m, tb, hooks.Report{Method: hooks.NewTab, Branch: "bad..name", Text: "x"}); a.Err == "" {
		t.Fatal("a bad branch name is refused")
	}
	a, cmd := askNerdr(m, tb, hooks.Report{Method: hooks.NewTab, Branch: "docs-fix", Model: "gpt-5.5-mini", Text: "fix the docs"})
	if a.Err != "" || !strings.Contains(a.Text, "making the tab docs-fix") {
		t.Fatalf("answer %+v", a)
	}
	var made *tabMadeMsg
	var run func(c tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil || made != nil {
			return
		}
		switch msg := c().(type) {
		case tabMadeMsg:
			made = &msg
		case tea.BatchMsg:
			for _, c := range msg {
				run(c)
			}
		}
	}
	run(cmd)
	if made == nil {
		t.Fatal("no tab made")
	}
	if made.err != nil {
		t.Skipf("making the worktree here: %v", made.err) // no origin to fetch from, say
	}
	m.Update(*made)
	nt := m.tabs[len(m.tabs)-1]
	if nt.Name != "docs-fix" || nt.Model != "gpt-5.5-mini" || nt.Agent != agents.Codex {
		t.Fatalf("new tab %+v", nt.Tab)
	}
	if len(tb.Messages) == 0 || !strings.Contains(tb.Messages[len(tb.Messages)-1].Text, "nerdr made the tab docs-fix") {
		t.Fatalf("the agent asking is told: %+v", tb.Messages)
	}
}

// TestRestartTerminal: nerdr terminal --restart runs the command an agent
// ran in a terminal again, in the same pane, or another one given; with
// neither, it says so.
func TestRestartTerminal(t *testing.T) {
	m := paneModel(t)
	m.cfg.Shell = "sh"
	fakeAgent(m, agents.Codex)
	tb := m.cur()
	m.newShellIn(tb, true) // the user's, with no command from an agent
	if a, _ := askNerdr(m, tb, hooks.Report{Method: hooks.Terminal, Restart: true, Name: "terminal"}); !strings.Contains(a.Err, "no command") {
		t.Fatalf("nothing to run again: %+v", a)
	}
	askNerdr(m, tb, hooks.Report{Method: hooks.Terminal, Text: "echo run-$((40+2))"})
	read := func() string {
		a, _ := askNerdr(m, tb, hooks.Report{Method: hooks.Query, What: hooks.QueryRead, Name: "terminal 2"})
		return a.Text
	}
	ran := func(n int) bool {
		for i := 0; strings.Count(read(), "run-42") < n && i < 100; i++ {
			time.Sleep(20 * time.Millisecond)
		}
		return strings.Count(read(), "run-42") >= n
	}
	// Once the shell is running: ctrl+c stops a shell still starting.
	if !ran(1) {
		t.Fatalf("the command should have run: %q", read())
	}
	a, cmd := askNerdr(m, tb, hooks.Report{Method: hooks.Terminal, Restart: true, Name: "terminal 2"})
	if a.Err != "" || !strings.Contains(a.Text, "terminal 2") || cmd == nil {
		t.Fatalf("restart: %+v", a)
	}
	cmd() // waits for the shell, and types the command in again
	if !ran(2) {
		t.Fatalf("the command should have run twice: %q", read())
	}
	if p, _ := findTerminal(tb, "2"); p == nil || p.command != "echo run-$((40+2))" || len(tb.panes()) != 3 {
		t.Fatalf("the same pane, with its command: %+v", p)
	}
}

// TestAgentClosesPanes: nerdr close closes a terminal or another agent's
// pane; not the asking agent's own, nor the tab's first agent's.
func TestAgentClosesPanes(t *testing.T) {
	m := paneModel(t)
	m.cfg.Shell = "sh"
	fakeAgent(m, agents.Codex)
	tb := m.cur()
	askNerdr(m, tb, hooks.Report{Method: hooks.Terminal, Text: "true"})
	m.newAgent(tb, agents.Codex)
	two := tb.extraAgents()[0]
	if a, _ := askNerdr(m, two, hooks.Report{Method: hooks.Close, To: 1}); !strings.Contains(a.Err, "first agent") {
		t.Fatalf("the tab's first agent: %+v", a)
	}
	if a, _ := askNerdr(m, two, hooks.Report{Method: hooks.Close, To: 2}); !strings.Contains(a.Err, "is you") {
		t.Fatalf("its own: %+v", a)
	}
	if a, _ := askNerdr(m, tb, hooks.Report{Method: hooks.Close, Name: "terminal 3"}); !strings.Contains(a.Err, "its terminals are terminal") {
		t.Fatalf("no such terminal: %+v", a)
	}
	if a, _ := askNerdr(m, tb, hooks.Report{Method: hooks.Close, Name: "terminal"}); a.Err != "" || !strings.Contains(a.Text, "Closed terminal") {
		t.Fatalf("close terminal: %+v", a)
	}
	if a, _ := askNerdr(m, tb, hooks.Report{Method: hooks.Close, To: 2}); a.Err != "" {
		t.Fatalf("close agent 2: %+v", a)
	}
	if len(tb.panes()) != 1 || len(tb.extraAgents()) != 0 {
		t.Fatalf("panes left: %d", len(tb.panes()))
	}
}

// TestSendWaitTakesTheReply: nerdr send --wait takes the answer of the
// agent it sent to out of its list, and none from anyone else.
func TestSendWaitTakesTheReply(t *testing.T) {
	m := paneModel(t)
	fakeAgent(m, agents.Codex)
	tb := m.cur()
	tb.status = stWorking // running nerdr send --wait
	m.newAgent(tb, agents.Codex)
	two := tb.extraAgents()[0]
	ask := func() hooks.Answer {
		a, _ := askNerdr(m, tb, hooks.Report{Method: hooks.Query, What: hooks.QueryReply, To: 2})
		return a
	}
	if a := ask(); a.Err != "" || a.Text != "" {
		t.Fatalf("no answer yet: %+v", a)
	}
	m.addMessage(tb, "from the user", true)
	askNerdr(m, two, hooks.Report{Method: hooks.Message, To: 1, Text: "My move is c5"})
	if a := ask(); !strings.Contains(a.Text, "Message from agent 2") || !strings.Contains(a.Text, "My move is c5") {
		t.Fatalf("the answer: %+v", a)
	}
	if len(tb.Messages) != 1 || tb.Messages[0].Text != "from the user" {
		t.Fatalf("left: %+v", tb.Messages)
	}
	if a := ask(); a.Text != "" {
		t.Fatalf("taken once: %+v", a)
	}
	if a, _ := askNerdr(m, tb, hooks.Report{Method: hooks.Query, What: hooks.QueryReply, To: 5}); a.Err == "" {
		t.Fatal("no agent 5 to wait for")
	}
}
