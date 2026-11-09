package core

import (
	"strconv"
	"time"

	"github.com/nerdr-dev/nerdr/internal/plugin"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// Client is whoever shows core to the user: today's TUI, or nobody
// (NoClient). Core tells it what happens with Tell, and what only the user can
// answer (AskMsg, SpinoffAskMsg, SwitchAskMsg), and asks it what it shows. What
// the client does with that, how it lays out the screen and where the keyboard
// is, is its own. Tabs, agents and panes go between them by TabRef and
// PaneRef, never by pointer: Core.ByRef and Core.PaneByRef find them.
type Client interface {
	// Tell gives the client an event of core's as it happens: one of the
	// *Msg types below. The work the client starts from it comes back as the
	// Cmd.
	Tell(ev Event) Cmd

	// What the client shows. Front is the tab in front of the user, the zero
	// TabRef when there is none; TabShown whether t is on screen, in front or
	// beside it. AgentVisible reports whether agent a is on screen and being
	// looked at: only then does its work count as seen. DialogClosed reports
	// whether nothing is being asked of the user now.
	Front() TabRef
	TabShown(t TabRef) bool
	AgentVisible(a TabRef) bool
	DialogClosed() bool
	// The size a program gets in agent a's pane, or in t's terminal pane p.
	TabSize(a TabRef) (w, h int)
	ShellSize(t TabRef, p PaneRef) (w, h int)
	// Layout is how the client lays out the tabs, for the session.
	Layout() Layout
	// Sight is what core's background work goes by of what the client shows.
	Sight() Sight
}

// Sight is what the client shows that core's background work goes by: the
// agent the keyboard is with, or the tab's own while it is with a terminal
// (Keyboard); the tab it keeps beside the one in front (Beside), shown or not;
// whether it shows the issues, or a list of the pull requests (IssuesShown,
// PRsShown).
type Sight struct {
	Keyboard    TabRef
	Beside      TabRef
	IssuesShown bool
	PRsShown    bool
}

// HasClient reports whether a client is there to show what core tells it.
func (m *Core) HasClient() bool {
	if m.Client == nil {
		return false
	}
	if c, ok := m.Client.(interface{ There() bool }); ok {
		return c.There()
	}
	_, none := m.Client.(NoClient)
	return !none
}

// Tell gives the client ev; see Client.Tell.
func (m *Core) Tell(ev Event) Cmd {
	if m.Client == nil {
		return nil
	}
	return m.Client.Tell(ev)
}

// What core tells the client; see Client.Tell. The tabs and panes they name
// are core's, to be looked up and read with Mu held.
type (
	// NoticeMsg is to be shown until it is read; a ToastMsg, for a moment.
	NoticeMsg string

	// LightMsg is a change of agent A's light, seen by the user or not.
	LightMsg struct {
		A       TabRef
		Visible bool
		Now     time.Time
	}
	// ReviewNewsMsg is news of the review in T that needs the user.
	ReviewNewsMsg struct {
		T    TabRef
		Text string
	}
	// ReviewMsg is a change in what the review panel shows of the tab in
	// front: findings, a reviewer's result.
	ReviewMsg struct{}

	// PaneOpenedMsg is P, just put in T's panes, for the keyboard to go to.
	PaneOpenedMsg struct {
		T TabRef
		P PaneRef
	}
	// PaneClosedMsg is P, just taken out of T's panes: no longer to be found.
	PaneClosedMsg struct {
		T TabRef
		P PaneRef
	}
	// PanesMsg is a change in what the agents' panes hold or how much room
	// the panel above an agent takes. LayoutMsg is a change in the tabs or
	// panes that may move everything.
	PanesMsg  struct{}
	LayoutMsg struct{}
	// StartedMsg is A's program just started, afresh or again.
	StartedMsg struct{ A TabRef }
	// TermsMsg is a change in the programs running in T's panes.
	TermsMsg struct{ T TabRef }
	// SwitchedMsg is T's agent switched to another kind.
	SwitchedMsg struct{ T TabRef }

	// SpinoffAskMsg is the parts T split off, waiting for the user to go
	// through; SwitchAskMsg the handoff T's agent wrote, for the user to
	// switch with. Both wait in T until then.
	SpinoffAskMsg struct{ T TabRef }
	SwitchAskMsg  struct{ T TabRef }

	// IssuesMsg is a change in what is known of the issues; IssueNewsMsg,
	// issues newly assigned to you, kept in IssueWatch.News too, to tell
	// of. IssuesDueMsg says it is time to ask again for what the client
	// shows of the issues.
	IssuesMsg    struct{}
	IssueNewsMsg struct{ News []state.IssueNews }
	IssuesDueMsg struct{}

	// PRsMsg is a change in what is known of your pull requests;
	// PRNewsMsg, news of them, kept in PRWatch.News too, to tell of; and
	// PRMarkMsg a change in whether there is news not seen yet.
	PRsMsg    struct{}
	PRNewsMsg struct{ News []state.PRNews }
	PRMarkMsg struct{}

	// OverlapsMsg is a change in which tabs change the same files; see
	// Core.Overlaps.
	OverlapsMsg struct{}

	// DetectStartMsg and DetectDoneMsg are the start and the end of a look at
	// every agent. On DetectDoneMsg a client calls Core.KeepAwake, with its
	// user's last input.
	DetectStartMsg struct{}
	DetectDoneMsg  struct{ Now time.Time }

	// PluginUIStartedMsg is the plugin screen of Key, by PluginUIKey, just
	// started in T; PluginUIGoneMsg, that it is gone. PluginRunMsg is the run
	// of P's of id Run, just started by an agent of T.
	PluginUIStartedMsg struct {
		T   TabRef
		Key string
	}
	PluginUIGoneMsg struct{ Key string }
	PluginRunMsg    struct {
		T   TabRef
		P   plugin.Plugin
		Run string
	}
)

// KeyRef and ScreenRef stand in a text for core for the keys of a command
// ("a s"), and of a screen of the review panel (3): the client puts in the keys
// as the user knows them. See ExpandKeys.
func KeyRef(command string) string { return keyMark + "k" + command + keyMark }
func ScreenRef(n int) string       { return keyMark + "s" + strconv.Itoa(n) + keyMark }

const keyMark = "\x1f"

// ExpandKeys is text with the keys it refers to put in by key and screen.
func ExpandKeys(text string, key func(command string) string, screen func(n int) string) string {
	var out []byte
	for {
		i := indexMark(text, 0)
		if i < 0 {
			break
		}
		j := indexMark(text, i+1)
		if j < 0 || j == i+1 {
			break
		}
		out = append(out, text[:i]...)
		ref := text[i+1 : j]
		switch ref[0] {
		case 'k':
			out = append(out, key(ref[1:])...)
		case 's':
			n, _ := strconv.Atoi(ref[1:])
			out = append(out, screen(n)...)
		}
		text = text[j+1:]
	}
	return string(append(out, text...))
}

func indexMark(s string, from int) int {
	for i := from; i < len(s); i++ {
		if s[i] == keyMark[0] {
			return i
		}
	}
	return -1
}

var _ Client = NoClient{}

// NoClient is a Client with nobody there: nothing is shown, and no agent is
// looked at. It is what core runs with when no client is connected.
type NoClient struct{}

func (NoClient) Tell(_ Event) Cmd                         { return nil }
func (NoClient) Front() TabRef                            { return TabRef{} }
func (NoClient) TabShown(_ TabRef) bool                   { return false }
func (NoClient) AgentVisible(_ TabRef) bool               { return false }
func (NoClient) DialogClosed() bool                       { return false }
func (NoClient) TabSize(_ TabRef) (int, int)              { return 0, 0 }
func (NoClient) ShellSize(_ TabRef, _ PaneRef) (int, int) { return 0, 0 }
func (NoClient) Layout() Layout                           { return Layout{} }
func (NoClient) Sight() Sight                             { return Sight{} }
