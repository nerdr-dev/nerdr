package app

import (
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
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
	tb := &tab{Tab: state.Tab{Goal: "the first task", Last: strings.Repeat("make the login form remember the user ", 10)}}
	lines := m.taskLines(tb, 40, 30)
	if len(lines) != lastMaxLines+1 {
		t.Fatalf("want %d rows and the rule, got %d", lastMaxLines, len(lines))
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
	m.tabs = []*tab{tb}
	m.setLast(tb, "now make it forget")
	m.tabs = nil
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
	if n := len(m.taskRows(&tab{}, 40, 30)); n != 0 {
		t.Fatalf("nothing to show, no panel: %d rows", n)
	}
	if n := len(m.taskRows(tb, 40, minAgentRows+2)); n != 2 {
		t.Fatalf("in a short frame the panel leaves the agent its rows: %d rows", n)
	}
}

func TestPanelTakesRowsFromThePane(t *testing.T) {
	m := &Model{width: 100, height: 40}
	a := &tab{Tab: state.Tab{ID: "a", Goal: "fix it"}}
	m.tabs = []*tab{a}
	_, full := m.agentSize()
	if _, h := m.tabSize(a); h != full-2 {
		t.Fatalf("pane height %d, want %d", h, full-2)
	}
	w, h := m.agentSize()
	if n := m.paneTop(a.focused(), w, h); n != 2 {
		t.Fatalf("panel height %d, want 2: the task and the rule", n)
	}
}

func TestTypedLine(t *testing.T) {
	var l typedLine
	l.feed([]byte("/model\r"))
	l.worked()
	l.feed([]byte("fix the\x1b[Dlogn\x7fin bug"))
	l.feed([]byte("\x1b[200~ and\nadd a test\x1b[201~\r"))
	if got := l.take(); got != "fix thelogin bug and add a test" {
		t.Fatalf("took %q", got)
	}
	if got := l.take(); got != "" {
		t.Fatalf("taken once, then %q", got)
	}
	l.feed([]byte("a later line\r"))
	l.feed([]byte("yes\r")) // a word of answer
	if got := l.take(); got != "a later line" {
		t.Fatalf("the last line worth keeping, got %q", got)
	}
}

// TestTypedLineAfterCommand: what is typed in a slash command's menu is
// not a prompt, unless the agent then works on it.
func TestTypedLineAfterCommand(t *testing.T) {
	var l typedLine
	l.feed([]byte("a real prompt\r"))
	l.take()
	l.feed([]byte("/model\r"))
	l.feed([]byte("sonnet\r"))
	if got := l.take(); got != "" {
		t.Fatalf("the menu's answer was taken as a prompt: %q", got)
	}
	l.feed([]byte("/fast\r"))
	l.feed([]byte("now fix the bug\r"))
	l.worked()
	if got := l.take(); got != "now fix the bug" {
		t.Fatalf("a line the agent worked on is a prompt, got %q", got)
	}
	l.feed([]byte("then the docs\r"))
	if got := l.take(); got != "then the docs" {
		t.Fatalf("after the agent worked, lines are prompts again: %q", got)
	}
}

func TestWaitingOrder(t *testing.T) {
	now := time.Now()
	mk := func(id string, s status, ago time.Duration) *tab {
		return &tab{Tab: state.Tab{ID: id, Name: id}, status: s, changed: now.Add(-ago)}
	}
	doneOld := mk("done-old", stDone, time.Hour)
	doneNew := mk("done-new", stDone, time.Minute)
	blocked := mk("blocked", stBlocked, time.Second)
	m := &Model{tabs: []*tab{doneNew, mk("working", stWorking, time.Hour), doneOld, blocked}}
	var got []string
	for _, x := range m.waiting() {
		got = append(got, x.ID)
	}
	if strings.Join(got, ",") != "blocked,done-old,done-new" {
		t.Fatalf("order %v", got)
	}
}

func TestCold(t *testing.T) {
	now := time.Now()
	m := &Model{cfg: config.Config{ColdDays: num(5)}}
	old := &tab{Tab: state.Tab{LastActive: now.Add(-6 * 24 * time.Hour)}, status: stIdle}
	if !m.cold(old, now) {
		t.Fatal("six days idle is cold")
	}
	old.status = stWorking
	if m.cold(old, now) {
		t.Fatal("a working agent is not cold")
	}
	recent := &tab{Tab: state.Tab{LastActive: now.Add(-24 * time.Hour)}, status: stIdle}
	if m.cold(recent, now) {
		t.Fatal("one day is not cold")
	}
	m.cfg.ColdDays = num(0)
	old.status = stIdle
	if m.cold(old, now) {
		t.Fatal("cold_days = 0 turns it off")
	}
}

func TestLeavingAndComingBack(t *testing.T) {
	m := &Model{lastInGroup: map[string]string{}}
	a := &tab{Tab: state.Tab{ID: "a", Name: "a"}, status: stIdle}
	b := &tab{Tab: state.Tab{ID: "b", Name: "b"}, status: stIdle}
	m.tabs = []*tab{a, b}
	m.entered = a

	m.track() // nothing changed
	if a.visit != nil {
		t.Fatal("staying in a tab is not leaving it")
	}
	m.active = 1
	m.track()
	if a.visit == nil || m.entered != b {
		t.Fatal("going to b leaves a")
	}

	// While away, a finishes a turn and asks something.
	a.away.done, a.away.blocked = 1, 1
	a.status = stDone
	m.active = 0
	cmd := m.track()
	if cmd == nil {
		t.Fatal("coming back looks up what happened")
	}
	var s summary
	for _, msg := range runBatch(cmd) {
		if sm, ok := msg.(summaryMsg); ok && sm.s.t == a {
			s = sm.s
		}
	}
	if !s.finished || s.log.done != 1 {
		t.Fatalf("summary %+v", s)
	}
	m.onSummary(s)
	if m.dialog != nil {
		t.Fatalf("coming back to a tab opens no dialog, got %T", m.dialog)
	}
	if a.summary == nil {
		t.Fatal("kept for H")
	}
}

// Leaving a tab, however long you worked in it, asks nothing: the walk
// with the prefix goes straight on.
func TestLeavingAsksNothing(t *testing.T) {
	m := testModel(t)
	login, logout := m.tabs[0], m.tabs[1]
	m.entered = login

	m.Update(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.Update(tea.KeyMsg{Type: tea.KeyDown}) // to the tab row
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.dialog != nil || m.cur() != logout {
		t.Fatalf("no dialog on leaving: %T, on %s", m.dialog, m.cur().Name)
	}
	if !m.prefix || !m.nav {
		t.Fatal("the walk goes on")
	}
}

func TestMergedTabSaysSoWithoutADialog(t *testing.T) {
	m := &Model{cfg: config.Config{CloseMerged: on(), Prefix: "ctrl+b"}, lastInGroup: map[string]string{}}
	a := &tab{Tab: state.Tab{ID: "a", Name: "a", Worktrees: []workspace.Worktree{{Path: "/x"}}}}
	b := &tab{Tab: state.Tab{ID: "b", Name: "b"}}
	a.info.wts = []wtInfo{{hasPR: true, pr: github.Status{Number: 3, State: "MERGED"}}}
	a.info.at = time.Now()
	m.tabs = []*tab{b, a}
	m.entered = b
	m.active = 1
	m.track()
	if m.dialog != nil {
		t.Fatalf("going to a merged tab opens no dialog, got %T", m.dialog)
	}
	rows := m.taskRows(a, 80, 30)
	if len(rows) != 2 || rows[0].label != "pr:" || !strings.Contains(rows[0].text, "#3 merged") {
		t.Fatalf("the panel says it is merged: %+v", rows)
	}
}

func TestTabLimit(t *testing.T) {
	m := &Model{cfg: config.Config{MaxTabs: num(2)}, width: 100, height: 40}
	m.tabs = []*tab{{}, {}}
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
	left := time.Now().Add(-time.Hour)
	tb := &tab{Tab: state.Tab{Name: "login", Agent: "claude"}, status: stWorking}
	d := &summaryDialog{s: summary{t: tb, since: left, away: 30 * time.Minute}}
	if v := ansi.Strip(d.view(m, 100, 40)); !strings.Contains(v, "working now") {
		t.Fatalf("working when opened: %q", v)
	}
	tb.status = stIdle
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
	at := time.Now()
	tb := &tab{Tab: state.Tab{Last: "go on", Recap: "half done", RecapAt: at}}
	m.tabs = []*tab{tb}

	m.setLatest(tb, agents.Latest{Prompt: "go on", PromptAt: at.Add(time.Minute), Recap: "half done", RecapAt: at})
	if tb.Recap != "" {
		t.Fatalf("the log has go on again after the recap, and the recap stays: %q", tb.Recap)
	}

	tb.Recap, tb.RecapAt = "half done", at
	m.setLast(tb, "go on")
	if tb.Recap != "" || tb.Last != "go on" {
		t.Fatalf("go on sent again, and the recap stays: %q", tb.Recap)
	}

	// The log read again: the recap is from before the prompt, and does
	// not come back; a later one does.
	m.setLatest(tb, agents.Latest{Prompt: "go on", PromptAt: at.Add(time.Minute), Recap: "half done", RecapAt: at})
	if tb.Recap != "" {
		t.Fatalf("an old recap came back: %q", tb.Recap)
	}
	m.setLatest(tb, agents.Latest{Prompt: "go on", PromptAt: at.Add(time.Minute), Recap: "all done", RecapAt: at.Add(5 * time.Minute)})
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
	tb := &tab{}
	m.tabs = []*tab{tb}
	m.setLast(tb, "the newest prompt")
	m.setLatest(tb, agents.Latest{Prompt: "the one before", PromptAt: time.Now().Add(-time.Minute)})
	if tb.Last != "the newest prompt" {
		t.Fatalf("an older prompt from the log took its place: %q", tb.Last)
	}
	m.setLatest(tb, agents.Latest{Prompt: "the newest prompt, as logged", PromptAt: time.Now().Add(-time.Second)})
	if tb.Last != "the newest prompt, as logged" {
		t.Fatalf("the log's own copy of it should be taken: %q", tb.Last)
	}
	m.setLatest(tb, agents.Latest{Prompt: "a later one", PromptAt: time.Now().Add(time.Minute)})
	if tb.Last != "a later one" {
		t.Fatalf("a later prompt: %q", tb.Last)
	}
}
