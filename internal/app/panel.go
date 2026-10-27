package app

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/clean"
	"github.com/nerdr-dev/nerdr/internal/state"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The panel above the agent, inside its frame: what the tab is about, so
// it is in sight whenever you come to the tab, without a dialog to close.
// It shows, each when there is one:
//
//	recap: Claude's recap of where the work stands, when it has written
//	      one since you last sent it something; else
//	last: the last thing you sent the agent (or else its first task), up
//	      to lastMaxLines rows; after the one before it when it is only
//	      an answer such as "yes"
//	note: your note for the tab, one row
//	swap: that a switch of agent waits for the old one's handoff
//	rev:  the code review going on, or how the last one ended
//	msgs: the messages waiting in F3
//	pr:   that the tab's work is merged, and how to close it
//
// and a rule under it all, between it and the agent.

const (
	// lastMaxLines is the most rows the last message takes in the panel.
	lastMaxLines = 2
	// minAgentRows is what the panel leaves the agent at the least: in a
	// short window the panel gives up its rows first.
	minAgentRows = 5
	// panelLabelW is the width of the panel's labels, "status:" and the rest.
	panelLabelW = 7
)

var (
	panelStyle      = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("252"))
	panelLabelStyle = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(accent).Bold(true)
	panelDimStyle   = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("245"))
	panelRuleStyle  = lipgloss.NewStyle().Foreground(accent)
)

// panelRow is one row of the panel: its label, on the first row of an
// entry only, and its text, plain and already fitted to the width; or the
// rule under the panel.
type panelRow struct {
	label, text string
	dim, rule   bool
}

// goalWrap is the last message wrapped for one width, kept since the panel
// is measured on every resize and mouse motion, and wrapping is not free.
type goalWrap struct {
	w           int
	goal, reply string
	rows        []string
}

// briefWords and briefRunes are the most a prompt has of each to be a
// brief one: an answer such as "yes" or "ok, do it", not a request.
const (
	briefWords = 3
	briefRunes = 24
)

func brief(s string) bool {
	return len(strings.Fields(s)) <= briefWords && utf8.RuneCountInString(s) <= briefRunes
}

// beforeBrief is, when t's last prompt is a brief one, the last before it
// that was not, or else the task; "" when it is not brief or there is none.
func (t *tab) beforeBrief() string {
	last := strings.TrimSpace(t.Last)
	if !brief(last) {
		return ""
	}
	ps := t.Prompts
	if n := len(ps); n > 0 && ps[n-1].Text == last {
		ps = ps[:n-1]
	}
	for i := len(ps) - 1; i >= 0; i-- {
		if p := strings.TrimSpace(ps[i].Text); !brief(p) {
			return p
		}
	}
	// The task is what the tab is for, however short.
	if g := strings.TrimSpace(t.Goal); g != last {
		return g
	}
	return ""
}

// replyArrow is put between a prompt and the brief one that answers it.
const replyArrow = " → "

// wrapWithReply is wrapRows of text with reply after it, the reply kept
// whole at the end however much of text is cut. reply may be "".
func wrapWithReply(text, reply string, w, n int) []string {
	if reply == "" {
		return wrapRows(text, w, n)
	}
	tail := replyArrow + ansi.Truncate(strings.Join(strings.Fields(reply), " "), w/2, "…")
	if rows := wrapRows(text+tail, w, 1<<20); len(rows) <= n {
		return rows
	}
	rows := wrapRows(text, w, n)
	last := strings.TrimSuffix(rows[len(rows)-1], "…")
	rows[len(rows)-1] = ansi.Truncate(last, w-ansi.StringWidth(tail)-1, "") + "…" + tail
	return rows
}

// taskRows is the panel of tab t in a frame w×h inside.
func (m *Model) taskRows(t *tab, w, h int) []panelRow {
	textW := w - 2 - panelLabelW // a space either side of the label
	if t == nil || textW < 8 {
		return nil
	}
	var rows []panelRow
	label, g, reply := "last:", strings.TrimSpace(t.Last), ""
	if g == "" {
		g = strings.TrimSpace(t.Goal)
	} else if p := t.beforeBrief(); p != "" {
		// "yes" alone says nothing of the work: what it answers does.
		g, reply = p, g
	}
	if r := strings.TrimSpace(t.Recap); r != "" {
		label, g, reply = "recap:", r, ""
	}
	if g != "" {
		if c := t.goalWrap; c == nil || c.w != textW || c.goal != g || c.reply != reply {
			t.goalWrap = &goalWrap{w: textW, goal: g, reply: reply, rows: wrapWithReply(g, reply, textW, lastMaxLines)}
		}
		for i, r := range t.goalWrap.rows {
			rows = append(rows, panelRow{label: onOff(i == 0, label, ""), text: r})
		}
	}
	if s := strings.TrimSpace(t.Status); s != "" {
		if t.StatusBy > 0 {
			s = fmt.Sprintf("%d: %s", t.StatusBy, s)
		}
		rows = append(rows, panelRow{label: "status:", text: wrapRows(s, textW, 1)[0]})
	}
	if n := strings.TrimSpace(t.Note); n != "" {
		rows = append(rows, panelRow{label: "note:", text: wrapRows(n, textW, 1)[0]})
	}
	if w := t.handoff; w != nil {
		rows = append(rows, panelRow{label: "swap:", text: ansi.Truncate("waiting for "+string(t.Agent)+"'s handoff to "+string(w.to)+" · "+
			m.pk("a s")+" switches now or cancels", textW, "…"), dim: true})
	}
	if s := m.reviewLine(t); s != "" && t.reviewOf == nil {
		rows = append(rows, panelRow{label: "rev:", text: ansi.Truncate(s, textW, "…"), dim: t.review == nil})
	}
	if s := m.messagesLine(t); s != "" {
		rows = append(rows, panelRow{label: "tasks:", text: ansi.Truncate(s, textW, "…"), dim: true})
	}
	if isOn(m.cfg.CloseMerged) && t.info.merged() {
		rows = append(rows, panelRow{label: "pr:", text: ansi.Truncate(prLabel(t.info)+" · its work is in main · "+
			m.pk("x")+" closes the tab", textW, "…"), dim: true})
	}
	room := max(h-minAgentRows-1, 0) // one for the rule
	if len(rows) == 0 || room == 0 {
		return nil
	}
	return append(rows[:min(len(rows), room)], panelRow{rule: true})
}

// wrapRows wraps text, its own line breaks as spaces, into at most n rows
// of w columns, the last cut short with … when there is more.
func wrapRows(text string, w, n int) []string {
	rows := strings.Split(ansi.Wrap(strings.Join(strings.Fields(text), " "), w, ""), "\n")
	for i := range rows {
		rows[i] = strings.TrimLeft(rows[i], " ")
	}
	if len(rows) > n {
		rows = rows[:n]
		rows[n-1] = ansi.Truncate(rows[n-1], w-1, "") + "…"
	}
	return rows
}

// wrapLines wraps text, keeping its own line breaks, into at most n rows
// of w columns, the last cut short with … when there is more.
func wrapLines(text string, w, n int) []string {
	var rows []string
	for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		rows = append(rows, strings.Split(ansi.Wrap(strings.TrimRight(l, " \t"), w, ""), "\n")...)
	}
	if len(rows) > n {
		rows = rows[:n]
		rows[n-1] = ansi.Truncate(rows[n-1], w-1, "") + "…"
	}
	return rows
}

// taskLines draws the panel of tab t, w columns wide in a frame of h
// rows inside.
func (m *Model) taskLines(t *tab, w, h int) []string {
	rows := m.taskRows(t, w, h)
	out := make([]string, len(rows))
	for i, r := range rows {
		if r.rule {
			out[i] = panelRuleStyle.Render(strings.Repeat("─", w))
			continue
		}
		text := panelStyle
		if r.dim {
			text = panelDimStyle
		}
		label := panelLabelStyle.Render(padTo(r.label, panelLabelW))
		if r.label == "last:" {
			label = m.onClick(label, func(m *Model) tea.Cmd { return m.openSentOf(t) })
		}
		out[i] = panelStyle.Render(" ") + label +
			panelStyle.Render(" ") + text.Render(padTo(r.text, w-2-panelLabelW))
	}
	return out
}

// The task, when the tab was not opened with one: the first thing you ask
// the agent. Claude logs it, and it is read from there; for the other
// agents it is taken from what you type.

// goalMsg is the first prompt found in a tab's conversation log, only
// looked for while the tab has no task, and the end of the log: the last
// prompt and the last recap.
type goalMsg struct {
	t      *tab
	goal   string
	latest agents.Latest
}

// recapWait is how long after a turn ends the log is read again for a
// recap: Claude writes one when it has stood idle about three minutes.
const recapWait = 4 * time.Minute

// recapMsg is the time to look for t's recap.
type recapMsg struct{ t *tab }

// promptWait is how long after the agent starts working its log is read
// again for the prompt it was sent: at the start, it may not be in yet.
const promptWait = 3 * time.Second

// promptLater reads t's log again for the prompt it is working on.
func (m *Model) promptLater(t *tab) tea.Cmd {
	if t.Agent != agents.Claude {
		return nil
	}
	return tea.Tick(promptWait, func(time.Time) tea.Msg { return recapMsg{t} })
}

// recapLater looks for t's recap once Claude has had time to write it.
func (m *Model) recapLater(t *tab) tea.Cmd {
	if t.Agent != agents.Claude {
		return nil
	}
	return tea.Tick(recapWait, func(time.Time) tea.Msg { return recapMsg{t} })
}

// lookUpGoal reads t's first prompt, when t has no task yet, and the end
// of its agent's log, in the background. It runs when the agent starts
// working, as it has been sent something; when you come to the tab; and
// a while after a turn, for the recap.
func (m *Model) lookUpGoal(t *tab) tea.Cmd {
	if t.Agent != agents.Claude || t.SessionID == "" || t.goalLooking {
		return nil
	}
	t.goalLooking = true
	kind, id, dir, first := t.Agent, t.SessionID, t.Dir, t.Goal == ""
	return func() tea.Msg {
		msg := goalMsg{t: t, latest: agents.LatestOfIn(kind, id, dir)}
		if first {
			msg.goal = agents.FirstPromptIn(kind, id, dir)
		}
		return msg
	}
}

// setLast notes msg as sent to t's agent just now, for the panel. Claude's
// recap is about the work before it, even when msg says the same as the
// last one ("go on").
func (m *Model) setLast(t *tab, msg string) {
	if !m.live(t) || strings.TrimSpace(msg) == "" {
		return
	}
	t.Recap = ""
	t.lastAt = time.Now()
	if msg = strings.TrimSpace(clean.Text(msg)); msg != "" {
		// Sent again, "go on" is in the list twice, though Last stays.
		t.seedPrompts()
		t.Last = msg
		t.addPrompt(msg, t.lastAt)
	}
	m.saveSession()
	m.resizePanes() // the panel may have changed height
}

// promptsKept is how many of the prompts sent a tab keeps.
const promptsKept = 50

// seedPrompts starts t's list with Last, for a tab from before nerdr kept
// one: what a brief answer after it answers.
func (t *tab) seedPrompts() {
	if len(t.Prompts) == 0 && strings.TrimSpace(t.Last) != "" {
		t.Prompts = []state.Prompt{{Text: t.Last}}
	}
}

// addPrompt puts msg, sent at, at the end of t's prompts.
func (t *tab) addPrompt(msg string, at time.Time) {
	t.Prompts = append(t.Prompts, state.Prompt{Text: msg, At: at})
	if n := len(t.Prompts); n > promptsKept {
		t.Prompts = slices.Clone(t.Prompts[n-promptsKept:])
	}
}

// noteLast keeps msg, which the agent's log says was sent at at, as the
// last thing sent to t's agent, and reports whether it was not already.
// What nerdr took from the typing is a guess: the log's text takes its
// place when it says the same prompt in other words.
func (m *Model) noteLast(t *tab, msg string, at time.Time) bool {
	// Read from the agent's log too, which the agent can write: see
	// package clean.
	msg = strings.TrimSpace(clean.Text(msg))
	if msg == "" || msg == t.Last {
		return false
	}
	t.seedPrompts()
	t.Last = msg
	if n := len(t.Prompts); n > 0 && !t.lastAt.IsZero() && t.Prompts[n-1].At.Equal(t.lastAt) && at.Before(t.lastAt.Add(lastSkew)) {
		t.Prompts[n-1].Text = msg
	} else {
		t.addPrompt(msg, at)
	}
	return true
}

// lastSkew is how long before nerdr saw a prompt typed the agent may have
// logged it: nerdr takes it on its next look at the tab.
const lastSkew = 5 * time.Second

// setLatest takes what the end of t's conversation log says: the last
// prompt, and the recap when it was written after it. A recap written
// before the last prompt is dropped, whether or not the prompt's text is
// new.
func (m *Model) setLatest(t *tab, l agents.Latest) {
	if !m.live(t) {
		return
	}
	// The log is read as the agent starts working, which may be before it
	// has logged the prompt it was just sent: the one before that is not
	// the last, whatever the log says.
	changed := false
	if l.PromptAt.After(t.lastAt.Add(-lastSkew)) {
		changed = m.noteLast(t, l.Prompt, l.PromptAt)
	}
	r := strings.TrimSpace(clean.Text(l.Recap))
	switch {
	case r != "" && !l.RecapAt.Before(l.PromptAt) && (r != t.Recap || !l.RecapAt.Equal(t.RecapAt)):
		t.Recap, t.RecapAt = r, l.RecapAt
		changed = true
	case t.Recap != "" && t.RecapAt.Before(l.PromptAt):
		t.Recap = ""
		changed = true
	}
	if changed {
		m.saveSession()
		m.resizePanes()
	}
}

func (m *Model) setGoal(t *tab, goal string) {
	if !m.live(t) || t.Goal != "" || strings.TrimSpace(goal) == "" {
		return
	}
	t.Goal = strings.TrimSpace(clean.Text(goal))
	m.saveSession()
	m.resizePanes() // the panel takes rows above the agent
}

// typedLine follows what is typed to an agent, to catch the lines sent. It is fed from the input router's goroutine, hence the lock. It
// is a fair guess, not an editor: keys that move the cursor are skipped.
type typedLine struct {
	mu      sync.Mutex
	buf     []byte
	line    string // the last line sent, until taken
	esc     []byte // an escape sequence being read
	inPaste bool
	// command is set by a slash command sent, until the agent works: what
	// is sent meanwhile is most likely an answer to its menu (/model, a
	// model's name), and held until the agent works on it, which shows it
	// was a prompt after all.
	command bool
	held    string
}

// feed takes bytes typed to the agent.
func (l *typedLine) feed(b []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range b {
		if l.esc != nil {
			l.escape(c)
			continue
		}
		switch {
		case c == 0x1b:
			l.esc = []byte{}
		case c == '\r' || c == '\n':
			if l.inPaste {
				l.buf = append(l.buf, ' ')
				continue
			}
			l.send()
		case c == 0x7f || c == 0x08:
			if _, n := utf8.DecodeLastRune(l.buf); n > 0 {
				l.buf = l.buf[:len(l.buf)-n]
			}
		case c == 0x15 || c == 0x03: // ctrl+u, ctrl+c
			l.buf = l.buf[:0]
		case c == 0x17: // ctrl+w
			s := strings.TrimRight(string(l.buf), " ")
			l.buf = []byte(s[:strings.LastIndex(s, " ")+1])
		case c == '\t':
			l.buf = append(l.buf, ' ')
		case c >= 0x20:
			l.buf = append(l.buf, c)
		}
	}
}

// escape takes the next byte of an escape sequence: bracketed paste is
// followed, anything else skipped.
func (l *typedLine) escape(c byte) {
	l.esc = append(l.esc, c)
	switch {
	case len(l.esc) == 1 && (c == '\r' || c == '\n'): // alt+enter: a new line in the prompt
		l.buf, l.esc = append(l.buf, ' '), nil
	case len(l.esc) == 1 && c != '[' && c != 'O': // alt and a key
		l.esc = nil
	case len(l.esc) == 2 && l.esc[0] == 'O': // SS3
		l.esc = nil
	case len(l.esc) >= 2 && l.esc[0] == '[' && c >= 0x40 && c <= 0x7e: // CSI, ended
		switch string(l.esc) {
		case "[200~":
			l.inPaste = true
		case "[201~":
			l.inPaste = false
		}
		l.esc = nil
	case len(l.esc) > 32:
		l.esc = nil
	}
}

// send notes the line as sent, when it is worth keeping: a slash command,
// a shell command (!), what is chosen in a command's menu or a word of
// answer says nothing of what the agent was asked.
func (l *typedLine) send() {
	s := strings.TrimSpace(string(l.buf))
	l.buf = l.buf[:0]
	switch {
	case strings.HasPrefix(s, "/"):
		l.command, l.held = true, ""
	case len(s) < 4 || strings.HasPrefix(s, "!"):
	case l.command:
		l.held = s
	default:
		l.line = s
	}
}

// worked is the agent starting to work: a line held since a slash command
// was a prompt after all.
func (l *typedLine) worked() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.command && l.held != "" {
		l.line = l.held
	}
	l.command, l.held = false, ""
}

// take is the last line sent since it was last taken, once.
func (l *typedLine) take() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.line
	l.line = ""
	return s
}
