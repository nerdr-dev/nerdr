package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/github"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func on() *bool      { b := true; return &b }
func num(n int) *int { return &n }

func TestPanelShowsTheLastMessageInTwoRowsAtMost(t *testing.T) {
	m := &Model{}
	m.Client = m
	tb := &core.Tab{AgentState: core.Agent{Tab: state.Tab{Goal: "the first task", Last: strings.Repeat("make the login form remember the user ", 10)}}}
	lines := m.taskLines(tb, 40, 30)
	if len(lines) != core.LastMaxLines+1 {
		t.Fatalf("want %d rows and the rule, got %d", core.LastMaxLines, len(lines))
	}
	for _, l := range lines {
		if w := lipgloss.Width(l); w != 40 {
			t.Fatalf("row is %d wide, want 40: %q", w, ansi.Strip(l))
		}
	}
	if !strings.HasPrefix(ansi.Strip(lines[0]), " last:   make") || !strings.HasSuffix(strings.TrimRight(ansi.Strip(lines[1]), " "), "…") {
		t.Fatalf("drawn %q", ansi.Strip(strings.Join(lines, "\n")))
	}
	if ansi.Strip(lines[2]) != strings.Repeat("─", 40) {
		t.Fatalf("a rule under the panel: %q", ansi.Strip(lines[2]))
	}

	tb.Last = ""
	lines = m.taskLines(tb, 40, 30)
	if len(lines) != 2 || !strings.HasPrefix(ansi.Strip(lines[0]), " last:   the first task") {
		t.Fatalf("with nothing sent since, the task: %q", ansi.Strip(strings.Join(lines, "\n")))
	}
	tb.Recap = "the login form remembers the user"
	lines = m.taskLines(tb, 40, 30)
	if !strings.HasPrefix(ansi.Strip(lines[0]), " recap:  the login form") {
		t.Fatalf("Claude's recap in the last message's place: %q", ansi.Strip(strings.Join(lines, "\n")))
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir()) // the session saved is not the user's
	m.Tabs = []*core.Tab{tb}
	m.SetLast(tb, "now make it forget")
	m.Tabs = nil
	lines = m.taskLines(tb, 40, 30)
	if !strings.HasPrefix(ansi.Strip(lines[0]), " last:   now make it forget") {
		t.Fatalf("a message sent after the recap takes its place: %q", ansi.Strip(strings.Join(lines, "\n")))
	}
	tb.Last = ""

	tb.Note = "waiting for the API answer"
	lines = m.taskLines(tb, 40, 30)
	if len(lines) != 3 || !strings.HasPrefix(ansi.Strip(lines[1]), " note:   waiting") {
		t.Fatalf("the note has a row under it: %q", ansi.Strip(strings.Join(lines, "\n")))
	}
	if n := len(m.taskRows(&core.Tab{}, 40, 30)); n != 0 {
		t.Fatalf("nothing to show, no panel: %d rows", n)
	}
	if n := len(m.taskRows(tb, 40, core.MinAgentRows+2)); n != 2 {
		t.Fatalf("in a short frame the panel leaves the agent its rows: %d rows", n)
	}
}

func TestPanelTakesRowsFromThePane(t *testing.T) {
	m := &Model{width: 100, height: 40}
	m.Client = m
	a := &core.Tab{AgentState: core.Agent{Tab: state.Tab{ID: "a", Goal: "fix it"}}}
	m.Tabs = []*core.Tab{a}
	_, full := m.agentSize()
	if _, h := m.tabSize(a); h != full-2 {
		t.Fatalf("pane height %d, want %d", h, full-2)
	}
	w, h := m.agentSize()
	if n := m.paneTop(m.focused(a), w, h); n != 2 {
		t.Fatalf("panel height %d, want 2: the task and the rule", n)
	}
}

func TestTypedLine(t *testing.T) {
	var l core.TypedLine
	l.Feed([]byte("/model\r"))
	l.Worked()
	l.Feed([]byte("fix the\x1b[Dlogn\x7fin bug"))
	l.Feed([]byte("\x1b[200~ and\nadd a test\x1b[201~\r"))
	if got := l.Take(); got != "fix thelogin bug and add a test" {
		t.Fatalf("took %q", got)
	}
	if got := l.Take(); got != "" {
		t.Fatalf("taken once, then %q", got)
	}
	l.Feed([]byte("a later line\r"))
	l.Feed([]byte("yes\r")) // a word of answer
	if got := l.Take(); got != "a later line" {
		t.Fatalf("the last line worth keeping, got %q", got)
	}
}

// TestTypedLineAfterCommand: what is typed in a slash command's menu is
// not a prompt, unless the agent then works on it.
func TestTypedLineAfterCommand(t *testing.T) {
	var l core.TypedLine
	l.Feed([]byte("a real prompt\r"))
	l.Take()
	l.Feed([]byte("/model\r"))
	l.Feed([]byte("sonnet\r"))
	if got := l.Take(); got != "" {
		t.Fatalf("the menu's answer was taken as a prompt: %q", got)
	}
	l.Feed([]byte("/fast\r"))
	l.Feed([]byte("now fix the bug\r"))
	l.Worked()
	if got := l.Take(); got != "now fix the bug" {
		t.Fatalf("a line the agent worked on is a prompt, got %q", got)
	}
	l.Feed([]byte("then the docs\r"))
	if got := l.Take(); got != "then the docs" {
		t.Fatalf("after the agent worked, lines are prompts again: %q", got)
	}
}

func TestWaitingOrder(t *testing.T) {
	now := time.Now()
	mk := func(id string, s core.Status, ago time.Duration) *core.Tab {
		return &core.Tab{AgentState: core.Agent{Tab: state.Tab{ID: id, Name: id}, Light: s, Changed: now.Add(-ago)}}
	}
	doneOld := mk("done-old", core.StDone, time.Hour)
	doneNew := mk("done-new", core.StDone, time.Minute)
	blocked := mk("blocked", core.StBlocked, time.Second)
	m := &Model{Tabs: []*core.Tab{doneNew, mk("working", core.StWorking, time.Hour), doneOld, blocked}}
	m.Client = m
	var got []string
	for _, x := range m.Waiting() {
		got = append(got, x.ID)
	}
	if strings.Join(got, ",") != "blocked,done-old,done-new" {
		t.Fatalf("order %v", got)
	}
}

func TestCold(t *testing.T) {
	now := time.Now()
	m := &Model{Cfg: config.Config{ColdDays: num(5)}}
	m.Client = m
	old := &core.Tab{AgentState: core.Agent{Tab: state.Tab{LastActive: now.Add(-6 * 24 * time.Hour)}, Light: core.StIdle}}
	if !m.Cold(old, now) {
		t.Fatal("six days idle is cold")
	}
	old.Light = core.StWorking
	if m.Cold(old, now) {
		t.Fatal("a working agent is not cold")
	}
	recent := &core.Tab{AgentState: core.Agent{Tab: state.Tab{LastActive: now.Add(-24 * time.Hour)}, Light: core.StIdle}}
	if m.Cold(recent, now) {
		t.Fatal("one day is not cold")
	}
	m.Cfg.ColdDays = num(0)
	old.Light = core.StIdle
	if m.Cold(old, now) {
		t.Fatal("cold_days = 0 turns it off")
	}
}

func TestLeavingAndComingBack(t *testing.T) {
	m := &Model{lastInGroup: map[string]core.TabRef{}}
	m.Client = m
	a := &core.Tab{AgentState: core.Agent{Tab: state.Tab{ID: "a", Name: "a"}, Light: core.StIdle}}
	b := &core.Tab{AgentState: core.Agent{Tab: state.Tab{ID: "b", Name: "b"}, Light: core.StIdle}}
	m.Tabs = []*core.Tab{a, b}
	m.entered = a

	m.track() // nothing changed
	if a.Visit != nil {
		t.Fatal("staying in a tab is not leaving it")
	}
	m.Active = 1
	m.track()
	if a.Visit == nil || m.entered != b {
		t.Fatal("going to b leaves a")
	}

	// While away, a finishes a turn and asks something.
	a.Away.Done, a.Away.Blocked = 1, 1
	a.Light = core.StDone
	m.Active = 0
	cmd := m.track()
	if cmd == nil {
		t.Fatal("coming back looks up what happened")
	}
	var s core.Summary
	for _, msg := range runBatch(cmd) {
		if sm, ok := msg.(core.SummaryMsg); ok && sm.S.T == a {
			s = sm.S
		}
	}
	if !s.Finished || s.Log.Done != 1 {
		t.Fatalf("summary %+v", s)
	}
	m.OnSummary(s)
	if m.dialog != nil {
		t.Fatalf("coming back to a tab opens no dialog, got %T", m.dialog)
	}
	if a.Summary == nil {
		t.Fatal("kept for H")
	}
}

// Leaving a tab, however long you worked in it, asks nothing: the walk
// with the prefix goes straight on.
func TestLeavingAsksNothing(t *testing.T) {
	m := testModel(t)
	login, logout := m.Tabs[0], m.Tabs[1]
	m.entered = login

	m.Update(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.Update(tea.KeyMsg{Type: tea.KeyDown}) // to the tab row
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.dialog != nil || m.Cur() != logout {
		t.Fatalf("no dialog on leaving: %T, on %s", m.dialog, m.Cur().Name)
	}
	if !m.prefix || !m.nav {
		t.Fatal("the walk goes on")
	}
}

func TestMergedTabSaysSoWithoutADialog(t *testing.T) {
	m := &Model{Cfg: config.Config{CloseMerged: on(), Prefix: "ctrl+b"}, lastInGroup: map[string]core.TabRef{}}
	m.Client = m
	a := &core.Tab{AgentState: core.Agent{Tab: state.Tab{ID: "a", Name: "a", Worktrees: []workspace.Worktree{{Path: "/x"}}}}}
	b := &core.Tab{AgentState: core.Agent{Tab: state.Tab{ID: "b", Name: "b"}}}
	a.Info.Wts = []core.WtInfo{{HasPR: true, Pr: github.Status{Number: 3, State: "MERGED"}}}
	a.Info.At = time.Now()
	m.Tabs = []*core.Tab{b, a}
	m.entered = b
	m.Active = 1
	m.track()
	if m.dialog != nil {
		t.Fatalf("going to a merged tab opens no dialog, got %T", m.dialog)
	}
	rows := m.taskRows(a, 80, 30)
	if len(rows) != 2 || rows[0].Label != "pr:" || !strings.Contains(rows[0].Text, "#3 merged") {
		t.Fatalf("the panel says it is merged: %+v", rows)
	}
}

func TestTabLimit(t *testing.T) {
	m := &Model{Cfg: config.Config{MaxTabs: num(2)}, width: 100, height: 40}
	m.Client = m
	m.Tabs = []*core.Tab{{}, {}}
	m.openWizard()
	if _, ok := m.dialog.(*menu); !ok {
		t.Fatalf("at the limit a new tab is refused, got %T", m.dialog)
	}
}

// runBatch runs cmd, and the commands of a batch, for their messages.
func runBatch(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, runBatch(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// TestSummaryShowsTheAgentAsItIsNow covers the summary opened again with
// H after the agent has stopped: it no longer says it is working, and it
// has Claude's recap when one was written after you left.
func TestSummaryShowsTheAgentAsItIsNow(t *testing.T) {
	m := &Model{}
	m.Client = m
	left := time.Now().Add(-time.Hour)
	tb := &core.Tab{AgentState: core.Agent{Tab: state.Tab{Name: "login", Agent: "claude"}, Light: core.StWorking}}
	d := &summaryDialog{s: core.Summary{T: tb, Since: left, Away: 30 * time.Minute}}
	if v := ansi.Strip(d.view(m, 100, 40)); !strings.Contains(v, "working now") {
		t.Fatalf("working when opened: %q", v)
	}
	tb.Light = core.StIdle
	tb.Recap, tb.RecapAt = "The login form remembers the user; nothing is committed.", left.Add(10*time.Minute)
	v := ansi.Strip(d.view(m, 100, 40))
	if strings.Contains(v, "working") || !strings.Contains(v, "nothing is committed") {
		t.Fatalf("opened again after it stopped: %q", v)
	}
	tb.RecapAt = left.Add(-time.Minute)
	if v := ansi.Strip(d.view(m, 100, 40)); strings.Contains(v, "nothing is committed") {
		t.Fatalf("a recap from before you left is not about this time away: %q", v)
	}
}

// TestRecapGoesWithTheNextMessage covers a message sent after Claude's
// recap, with the same text as the one before it ("go on"): the recap is
// about the work before it, and gives way, whether the message is seen
// as it is sent or read from the conversation log.
func TestRecapGoesWithTheNextMessage(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir()) // the session saved is not the user's
	m := &Model{}
	m.Client = m
	at := time.Now()
	tb := &core.Tab{AgentState: core.Agent{Tab: state.Tab{Last: "go on", Recap: "half done", RecapAt: at}}}
	m.Tabs = []*core.Tab{tb}

	m.SetLatest(tb, agents.Latest{Prompt: "go on", PromptAt: at.Add(time.Minute), Recap: "half done", RecapAt: at})
	if tb.Recap != "" {
		t.Fatalf("the log has go on again after the recap, and the recap stays: %q", tb.Recap)
	}

	tb.Recap, tb.RecapAt = "half done", at
	m.SetLast(tb, "go on")
	if tb.Recap != "" || tb.Last != "go on" {
		t.Fatalf("go on sent again, and the recap stays: %q", tb.Recap)
	}

	// The log read again: the recap is from before the prompt, and does
	// not come back; a later one does.
	m.SetLatest(tb, agents.Latest{Prompt: "go on", PromptAt: at.Add(time.Minute), Recap: "half done", RecapAt: at})
	if tb.Recap != "" {
		t.Fatalf("an old recap came back: %q", tb.Recap)
	}
	m.SetLatest(tb, agents.Latest{Prompt: "go on", PromptAt: at.Add(time.Minute), Recap: "all done", RecapAt: at.Add(5 * time.Minute)})
	if tb.Recap != "all done" {
		t.Fatalf("the new recap: %q", tb.Recap)
	}
}

// TestLogDoesNotPutBackAnOlderPrompt: the log read as the agent starts may
// not have the prompt it was just sent; the one before it does not take
// the last one's place, and a later one does.
func TestLogDoesNotPutBackAnOlderPrompt(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := &Model{}
	m.Client = m
	tb := &core.Tab{}
	m.Tabs = []*core.Tab{tb}
	m.SetLast(tb, "the newest prompt")
	m.SetLatest(tb, agents.Latest{Prompt: "the one before", PromptAt: time.Now().Add(-time.Minute)})
	if tb.Last != "the newest prompt" {
		t.Fatalf("an older prompt from the log took its place: %q", tb.Last)
	}
	m.SetLatest(tb, agents.Latest{Prompt: "the newest prompt, as logged", PromptAt: time.Now().Add(-time.Second)})
	if tb.Last != "the newest prompt, as logged" {
		t.Fatalf("the log's own copy of it should be taken: %q", tb.Last)
	}
	m.SetLatest(tb, agents.Latest{Prompt: "a later one", PromptAt: time.Now().Add(time.Minute)})
	if tb.Last != "a later one" {
		t.Fatalf("a later prompt: %q", tb.Last)
	}
	var texts []string
	for _, p := range tb.Prompts {
		texts = append(texts, p.Text)
	}
	if got := strings.Join(texts, " | "); got != "the newest prompt, as logged | a later one" {
		t.Fatalf("the log's copy takes the typed one's place in the list, and a later one goes after: %q", got)
	}
}

// TestPanelShowsWhatABriefAnswerAnswers: "yes" alone says nothing of the
// work, so the panel shows the prompt before it, with the answer after.
func TestPanelShowsWhatABriefAnswerAnswers(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := &Model{}
	m.Client = m
	tb := &core.Tab{AgentState: core.Agent{Tab: state.Tab{Goal: "the first task"}}}
	m.Tabs = []*core.Tab{tb}
	draw := func(w int) string {
		lines := m.taskLines(tb, w, 30)
		return ansi.Strip(strings.Join(lines[:len(lines)-1], "\n"))
	}

	m.SetLast(tb, "yes")
	if got := draw(60); !strings.HasPrefix(got, " last:   the first task → yes") {
		t.Fatalf("with nothing before it, what it answers is the task: %q", got)
	}
	m.SetLast(tb, "make the login form remember the user")
	m.SetLast(tb, "ja")
	m.SetLast(tb, "ok, do it")
	if got := draw(60); !strings.HasPrefix(got, " last:   make the login form remember the user → ok, do it") {
		t.Fatalf("the last request before the answers: %q", got)
	}
	tb.Prompts[len(tb.Prompts)-3].Text = strings.Repeat("make the login form remember the user ", 10)
	lines := m.taskLines(tb, 40, 30)
	if len(lines) != core.LastMaxLines+1 || !strings.HasSuffix(strings.TrimRight(ansi.Strip(lines[1]), " "), "… → ok, do it") {
		t.Fatalf("a long request is cut, the answer kept: %q", draw(40))
	}
	for _, l := range lines {
		if w := lipgloss.Width(l); w != 40 {
			t.Fatalf("row is %d wide, want 40: %q", w, ansi.Strip(l))
		}
	}
	m.SetLast(tb, "now make it forget the user too")
	if got := draw(60); !strings.HasPrefix(got, " last:   now make it forget the user too") || strings.Contains(got, "→") {
		t.Fatalf("a request of its own is shown alone: %q", got)
	}
	m.SetLast(tb, "go on")
	m.SetLast(tb, "go on")
	if n := len(tb.Prompts); n != 7 {
		t.Fatalf("go on sent twice is in the list twice: %d prompts", n)
	}
	for range core.PromptsKept {
		m.SetLast(tb, "again")
	}
	if n := len(tb.Prompts); n != core.PromptsKept {
		t.Fatalf("the list keeps %d, has %d", core.PromptsKept, n)
	}
}

// TestBriefAnswerInATabFromBefore: a tab saved before nerdr kept a list
// has only Last; an answer after it is shown after it, not the task.
func TestBriefAnswerInATabFromBefore(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := &Model{}
	m.Client = m
	tb := &core.Tab{AgentState: core.Agent{Tab: state.Tab{Goal: "build the login page", Last: "fix the password reset redirect"}}}
	m.Tabs = []*core.Tab{tb}
	m.SetLast(tb, "yes")
	lines := m.taskLines(tb, 80, 30)
	if got := ansi.Strip(lines[0]); !strings.HasPrefix(got, " last:   fix the password reset redirect → yes") {
		t.Fatalf("drawn %q", got)
	}
	tb.Prompts, tb.Last = nil, "fix the password reset redirect"
	m.SetLatest(tb, agents.Latest{Prompt: "yes", PromptAt: time.Now()})
	if len(tb.Prompts) != 2 || tb.Prompts[0].Text != "fix the password reset redirect" {
		t.Fatalf("read from the log, the same: %+v", tb.Prompts)
	}
}

func TestSentDialogListsTheLatestFirst(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := &Model{}
	m.Client = m
	tb := &core.Tab{AgentState: core.Agent{Tab: state.Tab{Name: "login", Agent: agents.Codex}}}
	m.Tabs = []*core.Tab{tb}
	m.SetLast(tb, "make the login form remember the user\nand add a test")
	m.SetLast(tb, "yes")
	m.openSentOf(tb)
	d, ok := m.dialog.(*sentDialog)
	if !ok {
		t.Fatalf("no dialog: %T", m.dialog)
	}
	view := ansi.Strip(d.view(m, 80, 40))
	if i, j := strings.Index(view, "❯ "), strings.Index(view, "make the login"); i < 0 || j < i || !strings.Contains(view[i:j], "yes") {
		t.Fatalf("the latest first, picked: %s", view)
	}
	d.key(m, tea.KeyMsg{Type: tea.KeyDown})
	view = ansi.Strip(d.view(m, 80, 40))
	if !strings.Contains(view, "and add a test") {
		t.Fatalf("the one picked in full, its own lines kept: %s", view)
	}
	long := ""
	for i := range 30 {
		long += fmt.Sprintf("line %d of a long request\n", i+1)
	}
	m.SetLast(tb, long)
	d.list, d.pick = tb.Prompts, 0
	view = ansi.Strip(d.view(m, 80, 40))
	if strings.Contains(view, "line 11 ") || !strings.Contains(view, "20 rows more") {
		t.Fatalf("under the list, the first rows and how many more: %s", view)
	}
	d.key(m, tea.KeyMsg{Type: tea.KeyEnter})
	d.key(m, tea.KeyMsg{Type: tea.KeyEnd})
	view = ansi.Strip(d.view(m, 80, 20))
	if !d.full || !strings.Contains(view, "line 30 ") || !strings.Contains(view, "of 30") {
		t.Fatalf("enter shows it alone, scrolled to its end: %s", view)
	}
	d.key(m, tea.KeyMsg{Type: tea.KeyEsc})
	if d.full || m.dialog == nil {
		t.Fatal("esc goes back to the list, not out")
	}
	m.onSent(sentMsg{t: tb, list: []agents.Prompt{{Text: "typed in claude itself"}}})
	if len(d.list) != 1 || d.pick != 0 {
		t.Fatalf("the log's list takes the place of nerdr's: %+v at %d", d.list, d.pick)
	}
	d.key(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog != nil {
		t.Fatal("esc closes it")
	}
}
