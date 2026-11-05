package core

import (
	"strconv"
	"time"

	"github.com/nerdr-dev/nerdr/internal/plugin"
)

// Client is whoever shows core to the user: today's TUI, or nobody
// (NoClient). Core tells it what happens with Tell, waits on it for what only
// the user can answer, and asks it what it shows. What the client does with
// that, how it lays out the screen and where the keyboard is, is its own.
type Client interface {
	// Tell gives the client an event of core's as it happens: one of the
	// *Msg types below. The work the client starts from it comes back as the
	// Cmd.
	Tell(ev Event) Cmd

	// AskRepoSetup runs then once the user has allowed what repos set up, and
	// never without that. With no client the question waits for one.
	AskRepoSetup(repos []string, then func() Cmd) Cmd
	// AskPluginRun asks whether agent a may run plugin p with vals, in the
	// folder dir, and decides with "allowed", RunDeclined, RunUnanswered or
	// RunBusy. confined says that a runs in a sandbox and p outside it.
	AskPluginRun(a *Tab, p plugin.Plugin, vals map[string]any, dir string, confined bool, decide func(answer string) Cmd)
	// OpenSpinoff and OpenSwitchAgent ask the user about the parts t split
	// off, and about the agent to switch the tab to.
	OpenSpinoff(t *Tab) Cmd
	OpenSwitchAgent() Cmd

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

func (NoClient) Tell(_ Event) Cmd                          { return nil }
func (NoClient) Front() *Tab                               { return nil }
func (NoClient) TabShown(_ *Tab) bool                      { return false }
func (NoClient) AgentVisible(_ *Tab) bool                  { return false }
func (NoClient) DialogClosed() bool                        { return false }
func (NoClient) TabSize(_ *Tab) (int, int)                 { return 0, 0 }
func (NoClient) ShellSize(_ *Tab, _ *PaneTab) (int, int)   { return 0, 0 }
func (NoClient) Layout() Layout                            { return Layout{} }
func (NoClient) AskRepoSetup(_ []string, _ func() Cmd) Cmd { return nil }
func (NoClient) OpenSpinoff(_ *Tab) Cmd                    { return nil }
func (NoClient) OpenSwitchAgent() Cmd                      { return nil }

// AskPluginRun has it ask again: only a user can say yes.
func (NoClient) AskPluginRun(_ *Tab, _ plugin.Plugin, _ map[string]any, _ string, _ bool, decide func(answer string) Cmd) {
	decide(RunBusy)
}
