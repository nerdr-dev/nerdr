package core

import (
	"strconv"
	"time"

	"github.com/nerdr-dev/nerdr/internal/plugin"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// Client is whoever shows core to the user: today's TUI, or nobody
// (NoClient). Core tells it what happens with Tell, and what only the user can
// answer (AskMsg, SpinoffAskMsg, SwitchAskMsg), and asks it what it shows. What the client does with
// that, how it lays out the screen and where the keyboard is, is its own.
type Client interface {
	// Tell gives the client an event of core's as it happens: one of the
	// *Msg types below. The work the client starts from it comes back as the
	// Cmd.
	Tell(ev Event) Cmd

	// What the client shows. Front is the tab in front of the user, nil when
	// there is none; TabShown whether t is on screen, in front or beside it.
	// AgentVisible reports whether agent a is on screen and being looked at:
	// only then does its work count as seen. DialogClosed reports whether
	// nothing is being asked of the user now.
	Front() *Tab
	TabShown(t *Tab) bool
	AgentVisible(a *Tab) bool
	DialogClosed() bool
	// The size a program gets in t's pane, or in its terminal pane p.
	TabSize(t *Tab) (w, h int)
	ShellSize(t *Tab, p *PaneTab) (w, h int)
	// Layout is how the client lays out the tabs, for the session.
	Layout() Layout
	// Sight is what core's background work goes by of what the client shows.
	Sight() Sight
}

// Sight is what the client shows that core's background work goes by: the
// tab it keeps beside the one in front (Beside), shown or not; whether it
// shows the issues, or a list of the pull requests (IssuesShown, PRsShown); the worktree
// whose files it watches for changes (WatchWt), and how many it has seen
// (WatchN).
type Sight struct {
	Beside      *Tab
	IssuesShown bool
	PRsShown    bool
	WatchWt     string
	WatchN      int
}

// Tell gives the client ev; see Client.Tell.
func (m *Core) Tell(ev Event) Cmd {
	if m.Client == nil {
		return nil
	}
	return m.Client.Tell(ev)
}

// What core tells the client; see Client.Tell. A *Tab or *PaneTab in them is
// core's, to be read with Mu held.
type (
	// NoticeMsg is to be shown until it is read; a ToastMsg, for a moment.
	NoticeMsg string

	// LightMsg is a change of agent A's light, seen by the user or not.
	LightMsg struct {
		A       *Tab
		Visible bool
		Now     time.Time
	}
	// ReviewNewsMsg is news of the review in T that needs the user.
	ReviewNewsMsg struct {
		T    *Tab
		Text string
	}
	// ReviewMsg is a change in what the review panel shows of the tab in
	// front: findings, a reviewer's result.
	ReviewMsg struct{}

	// PaneOpenedMsg is P, just put in T's panes, for the keyboard to go to.
	PaneOpenedMsg struct {
		T *Tab
		P *PaneTab
	}
	// PaneClosedMsg is P, just taken out of T's panes.
	PaneClosedMsg struct {
		T *Tab
		P *PaneTab
	}
	// PanesMsg is a change in what the agents' panes hold or how much room
	// the panel above an agent takes. LayoutMsg is a change in the tabs or
	// panes that may move everything.
	PanesMsg  struct{}
	LayoutMsg struct{}
	// TermsMsg is a change in the programs running in T's panes.
	TermsMsg struct{ T *Tab }
	// SwitchedMsg is T's agent switched to another kind.
	SwitchedMsg struct{ T *Tab }

	// SpinoffAskMsg is the parts T split off, waiting for the user to go
	// through; SwitchAskMsg the handoff T's agent wrote, for the user to
	// switch with. Both wait in T until then.
	SpinoffAskMsg struct{ T *Tab }
	SwitchAskMsg  struct{ T *Tab }

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
	// every agent.
	DetectStartMsg struct{}
	DetectDoneMsg  struct{ Now time.Time }

	// PluginUIMsg is plugin P's screen wanted in T, as allowed with Key.
	// PluginEventMsg is Ev for the plugin screens of the plugin in Dir; and
	// PluginRunMsg a run of P's just started by an agent of T.
	PluginUIMsg struct {
		T   *Tab
		P   plugin.Plugin
		Key string
	}
	PluginEventMsg struct {
		T   *Tab
		Dir string
		Ev  map[string]any
	}
	PluginRunMsg struct {
		T   *Tab
		P   plugin.Plugin
		Run *PluginRun
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

func (NoClient) Tell(_ Event) Cmd                        { return nil }
func (NoClient) Front() *Tab                             { return nil }
func (NoClient) TabShown(_ *Tab) bool                    { return false }
func (NoClient) AgentVisible(_ *Tab) bool                { return false }
func (NoClient) DialogClosed() bool                      { return false }
func (NoClient) TabSize(_ *Tab) (int, int)               { return 0, 0 }
func (NoClient) ShellSize(_ *Tab, _ *PaneTab) (int, int) { return 0, 0 }
func (NoClient) Layout() Layout                          { return Layout{} }
func (NoClient) Sight() Sight                            { return Sight{} }
