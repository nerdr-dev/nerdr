package core

import (
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/clean"
	"github.com/nerdr-dev/nerdr/internal/state"
)

const (
	// lastMaxLines is the most rows the last message takes in the panel.
	LastMaxLines = 2
	// minAgentRows is what the panel leaves the agent at the least: in a
	// short window the panel gives up its rows first.
	MinAgentRows = 5
	// panelLabelW is the width of the panel's labels, "status:" and the rest.
	PanelLabelW = 7
)

// panelRow is one row of the panel: its label, on the first row of an
// entry only, and its text, plain and already fitted to the width; or the
// rule under the panel.
type PanelRow struct {
	Label, Text string
	Dim, Rule   bool
}

// replyArrow is put between a prompt and the brief one that answers it.
const ReplyArrow = " → "

// goalMsg is the first prompt found in a tab's conversation log, only
// looked for while the tab has no task, and the end of the log: the last
// prompt and the last recap.
type GoalMsg struct {
	T      *Tab
	Goal   string
	Latest agents.Latest
}

// recapWait is how long after a turn ends the log is read again for a
// recap: Claude writes one when it has stood idle about three minutes.
const RecapWait = 4 * time.Minute

// recapMsg is the time to look for t's recap.
type RecapMsg struct{ T *Tab }

// promptWait is how long after the agent starts working its log is read
// again for the prompt it was sent: at the start, it may not be in yet.
const PromptWait = 3 * time.Second

// promptLater reads t's log again for the prompt it is working on.
func (m *Core) PromptLater(t *Tab) Cmd {
	if t.Agent != agents.Claude {
		return nil
	}
	return Tick(PromptWait, func(time.Time) Event { return RecapMsg{t} })
}

// recapLater looks for t's recap once Claude has had time to write it.
func (m *Core) RecapLater(t *Tab) Cmd {
	if t.Agent != agents.Claude {
		return nil
	}
	return Tick(RecapWait, func(time.Time) Event { return RecapMsg{t} })
}

// lookUpGoal reads t's first prompt, when t has no task yet, and the end
// of its agent's log, in the background. It runs when the agent starts
// working, as it has been sent something; when you come to the tab; and
// a while after a turn, for the recap.
func (m *Core) LookUpGoal(t *Tab) Cmd {
	if t.Agent != agents.Claude || t.SessionID == "" || t.GoalLooking {
		return nil
	}
	t.GoalLooking = true
	kind, id, dir, first := t.Agent, t.SessionID, t.Dir, t.Goal == ""
	return func() Event {
		msg := GoalMsg{T: t, Latest: agents.LatestOfIn(kind, id, dir)}
		if first {
			msg.Goal = agents.FirstPromptIn(kind, id, dir)
		}
		return msg
	}
}

// setLast notes msg as sent to t's agent just now, for the panel. Claude's
// recap is about the work before it, even when msg says the same as the
// last one ("go on").
func (m *Core) SetLast(t *Tab, msg string) {
	if !m.Live(t) || strings.TrimSpace(msg) == "" {
		return
	}
	t.Recap = ""
	t.LastAt = time.Now()
	if msg = strings.TrimSpace(clean.Text(msg)); msg != "" {
		// Sent again, "go on" is in the list twice, though Last stays.
		t.SeedPrompts()
		t.Last = msg
		t.AddPrompt(msg, t.LastAt)
	}
	m.SaveSession()
	m.Tell(PanesMsg{}) // the panel may have changed height
}

// noteLast keeps msg, which the agent's log says was sent at at, as the
// last thing sent to t's agent, and reports whether it was not already.
// What nerdr took from the typing is a guess: the log's text takes its
// place when it says the same prompt in other words.
func (m *Core) NoteLast(t *Tab, msg string, at time.Time) bool {
	// Read from the agent's log too, which the agent can write: see
	// package clean.
	msg = strings.TrimSpace(clean.Text(msg))
	if msg == "" || msg == t.Last {
		return false
	}
	t.SeedPrompts()
	t.Last = msg
	if n := len(t.Prompts); n > 0 && !t.LastAt.IsZero() && t.Prompts[n-1].At.Equal(t.LastAt) && at.Before(t.LastAt.Add(LastSkew)) {
		t.Prompts[n-1].Text = msg
	} else {
		t.AddPrompt(msg, at)
	}
	return true
}

// lastSkew is how long before nerdr saw a prompt typed the agent may have
// logged it: nerdr takes it on its next look at the tab.
const LastSkew = 5 * time.Second

// setLatest takes what the end of t's conversation log says: the last
// prompt, and the recap when it was written after it. A recap written
// before the last prompt is dropped, whether or not the prompt's text is
// new.
func (m *Core) SetLatest(t *Tab, l agents.Latest) {
	if !m.Live(t) {
		return
	}
	// The log is read as the agent starts working, which may be before it
	// has logged the prompt it was just sent: the one before that is not
	// the last, whatever the log says.
	changed := false
	if l.PromptAt.After(t.LastAt.Add(-LastSkew)) {
		changed = m.NoteLast(t, l.Prompt, l.PromptAt)
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
		m.SaveSession()
		m.Tell(PanesMsg{})
	}
}

func (m *Core) SetGoal(t *Tab, goal string) {
	if !m.Live(t) || t.Goal != "" || strings.TrimSpace(goal) == "" {
		return
	}
	t.Goal = strings.TrimSpace(clean.Text(goal))
	m.SaveSession()
	m.Tell(PanesMsg{}) // the panel takes rows above the agent
}

// briefWords and briefRunes are the most a prompt has of each to be a
// brief one: an answer such as "yes" or "ok, do it", not a request.
const (
	BriefWords = 3
	BriefRunes = 24
)

func Brief(s string) bool {
	return len(strings.Fields(s)) <= BriefWords && utf8.RuneCountInString(s) <= BriefRunes
}

// BeforeBrief is, when t's last prompt is a brief one, the last before it
// that was not, or else the task; "" when it is not brief or there is none.
func (t *Tab) BeforeBrief() string {
	last := strings.TrimSpace(t.Last)
	if !Brief(last) {
		return ""
	}
	ps := t.Prompts
	if n := len(ps); n > 0 && ps[n-1].Text == last {
		ps = ps[:n-1]
	}
	for i := len(ps) - 1; i >= 0; i-- {
		if p := strings.TrimSpace(ps[i].Text); !Brief(p) {
			return p
		}
	}
	// The task is what the tab is for, however short.
	if g := strings.TrimSpace(t.Goal); g != last {
		return g
	}
	return ""
}

// PromptsKept is how many of the prompts sent a tab keeps.
const PromptsKept = 50

// SeedPrompts starts t's list with Last, for a tab from before nerdr kept
// one: what a brief answer after it answers.
func (t *Tab) SeedPrompts() {
	if len(t.Prompts) == 0 && strings.TrimSpace(t.Last) != "" {
		t.Prompts = []state.Prompt{{Text: t.Last}}
	}
}

// AddPrompt puts msg, sent at, at the end of t's prompts.
func (t *Tab) AddPrompt(msg string, at time.Time) {
	t.Prompts = append(t.Prompts, state.Prompt{Text: msg, At: at})
	if n := len(t.Prompts); n > PromptsKept {
		t.Prompts = slices.Clone(t.Prompts[n-PromptsKept:])
	}
}

// TypedLine follows what is typed to an agent, to catch the lines sent. It is fed from the input router's goroutine, hence the lock. It
// is a fair guess, not an editor: keys that move the cursor are skipped.
type TypedLine struct {
	Mu      sync.Mutex
	Buf     []byte
	Line    string // the last line sent, until taken
	Esc     []byte // an escape sequence being read
	InPaste bool
	// Command is set by a slash command sent, until the agent works: what
	// is sent meanwhile is most likely an answer to its menu (/model, a
	// model's name), and held until the agent works on it, which shows it
	// was a prompt after all.
	Command bool
	Held    string
}

// Feed takes bytes typed to the agent.
func (l *TypedLine) Feed(b []byte) {
	l.Mu.Lock()
	defer l.Mu.Unlock()
	for _, c := range b {
		if l.Esc != nil {
			l.Escape(c)
			continue
		}
		switch {
		case c == 0x1b:
			l.Esc = []byte{}
		case c == '\r' || c == '\n':
			if l.InPaste {
				l.Buf = append(l.Buf, ' ')
				continue
			}
			l.Send()
		case c == 0x7f || c == 0x08:
			if _, n := utf8.DecodeLastRune(l.Buf); n > 0 {
				l.Buf = l.Buf[:len(l.Buf)-n]
			}
		case c == 0x15 || c == 0x03: // ctrl+u, ctrl+c
			l.Buf = l.Buf[:0]
		case c == 0x17: // ctrl+w
			s := strings.TrimRight(string(l.Buf), " ")
			l.Buf = []byte(s[:strings.LastIndex(s, " ")+1])
		case c == '\t':
			l.Buf = append(l.Buf, ' ')
		case c >= 0x20:
			l.Buf = append(l.Buf, c)
		}
	}
}

// Escape takes the next byte of an escape sequence: bracketed paste is
// followed, anything else skipped.
func (l *TypedLine) Escape(c byte) {
	l.Esc = append(l.Esc, c)
	switch {
	case len(l.Esc) == 1 && (c == '\r' || c == '\n'): // alt+enter: a new line in the prompt
		l.Buf, l.Esc = append(l.Buf, ' '), nil
	case len(l.Esc) == 1 && c != '[' && c != 'O': // alt and a key
		l.Esc = nil
	case len(l.Esc) == 2 && l.Esc[0] == 'O': // SS3
		l.Esc = nil
	case len(l.Esc) >= 2 && l.Esc[0] == '[' && c >= 0x40 && c <= 0x7e: // CSI, ended
		switch string(l.Esc) {
		case "[200~":
			l.InPaste = true
		case "[201~":
			l.InPaste = false
		}
		l.Esc = nil
	case len(l.Esc) > 32:
		l.Esc = nil
	}
}

// Send notes the line as sent, when it is worth keeping: a slash command,
// a shell command (!), what is chosen in a command's menu or a word of
// answer says nothing of what the agent was asked.
func (l *TypedLine) Send() {
	s := strings.TrimSpace(string(l.Buf))
	l.Buf = l.Buf[:0]
	switch {
	case strings.HasPrefix(s, "/"):
		l.Command, l.Held = true, ""
	case len(s) < 4 || strings.HasPrefix(s, "!"):
	case l.Command:
		l.Held = s
	default:
		l.Line = s
	}
}

// Worked is the agent starting to work: a line held since a slash command
// was a prompt after all.
func (l *TypedLine) Worked() {
	l.Mu.Lock()
	defer l.Mu.Unlock()
	if l.Command && l.Held != "" {
		l.Line = l.Held
	}
	l.Command, l.Held = false, ""
}

// Take is the last line sent since it was last taken, once.
func (l *TypedLine) Take() string {
	l.Mu.Lock()
	defer l.Mu.Unlock()
	s := l.Line
	l.Line = ""
	return s
}
