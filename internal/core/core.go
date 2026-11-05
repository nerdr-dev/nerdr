package core

import (
	"sync"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/herdr"
	"github.com/nerdr-dev/nerdr/internal/plugin"
	"github.com/nerdr-dev/nerdr/internal/usage"
)

// Client is what core asks of whoever is showing it.
type Client interface {
	// Toast shows a short message to the user; Notice, one that stays until
	// it is read.
	Toast(string)
	Notice(text string)

	// DialogClosed reports whether nothing is being asked of the user now.
	DialogClosed() bool
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

	// AgentVisible reports whether agent a is on screen and being looked at:
	// only then does its work count as seen.
	AgentVisible(a *Tab) bool
	// NotifyChange and NotifyReview tell the user of a change of light, and of
	// a review that needs them.
	NotifyChange(a *Tab, visible bool, now time.Time)
	NotifyReview(a *Tab, text string)
	// Detect looks at every agent again, at once.
	Detect() Cmd

	// AgentName, PaneName, ScreenKey and KeyName are what the user knows
	// things by, for messages.
	AgentName(a *Tab) string
	PaneName(t *Tab, p *PaneTab) string
	ScreenKey(screen int) string
	KeyName(command string) string
	// SkillOn reports whether a skill is on for t.
	SkillOn(t *Tab, skill string) bool

	// EnsurePluginUI has p's screen running, as allowed with key, in t;
	// PluginEvent tells a plugin's screen of a run; ShowPluginRun shows the
	// user a run just started.
	EnsurePluginUI(t *Tab, p plugin.Plugin, key string)
	PluginEvent(t *Tab, dir string, ev map[string]any)
	ShowPluginRun(t *Tab, p plugin.Plugin, run *PluginRun) Cmd

	// SaveSession saves the tabs, with how they are laid out.
	SaveSession()

	// The layout: where a pane goes and what size a program gets.
	Resize() Cmd
	ResizePanes()
	TabSize(t *Tab) (w, h int)
	ShellSize(t *Tab, p *PaneTab) (w, h int)
	DropPane(t *Tab, p *PaneTab)
	DropReviewPane(t *Tab)
	FocusPane(t *Tab, p *PaneTab) Cmd
	FocusTerms(t *Tab)
	SetFocus(area FocusArea)
	StopScrolling()
	PinReview() Cmd
	ShareOverlaps()
}

var _ Client = NoClient{}

// Core is nerdr with no user interface: the tabs, and what goes on in them.
type Core struct {
	// Mu guards all of Core; see Loop.
	Mu sync.Mutex
	// Quitting is set once the program is on its way out.
	Quitting bool
	// Gen counts the events the loop has handled as core's: a client that
	// drew something from core's state at one Gen need not again while it
	// is the same.
	Gen uint64

	// Client is told what the user is to see; see Client.
	Client Client

	// pluginRuns is the runs of plugins by agents; see pluginruns.go.
	PluginRuns PluginRuns

	// claudeLimits is the last of what a claude agent said of its
	// account's limits, and when; see usage.go.
	ClaudeLimits *usage.Report
	ClaudeAt     time.Time
	WtRoot       string // where new worktrees are made
	// skillUse is how much the agents have used each skill, for F4; see
	// skilluse.go.
	SkillUse SkillUse
	// hookSockErr is why there is no hookSock, when there is not.
	HookSockErr string

	Cfg         config.Config
	Root        string // the folder nerdr was started in
	Herdr       *herdr.Client
	Tabs        []*Tab
	Active      int
	AgentModels map[agents.Kind]ModelList
	// IssueWatch is the watching of the issues assigned to you; see
	// issuewatch.go.
	IssueWatch IssueWatch
	// GroupNames is the group name given to each folder of worktrees in
	// several repositories, kept when its tab is closed; see addTab.
	GroupNames map[string]string
	// SandboxedDirs are the worktree folders where a sandboxed agent has
	// run, kept across tab close and reopen.
	SandboxedDirs map[string]bool
	// infoBusy is set while a tab's branches are being looked at; see
	// info.go. overlaps is, per tab id, the other tabs changing some of
	// the same files, and shared the same by file, for the review panel's
	// file list. showDetails shows that state in the tab bar.
	// hookSock is the socket herdr's integrations in the agents report to,
	// and hookBin nerdr itself, for those that report through the CLI.
	HookSock, HookBin string
	// CodexHooks are the arguments that give codex nerdr's hooks, once
	// codex trusts them, and codexAsked whether it has been asked; see
	// codexHookArgs.
	CodexHooks []string
	CodexAsked bool
	Wake       chan struct{}
}

// Toast shows a short message to the user, if a client is there to show it.
func (c *Core) Toast(s string) {
	if c.Client != nil {
		c.Client.Toast(s)
	}
}

// NoClient is a Client with nobody there: nothing is shown, and no agent is
// looked at. It is what core runs with when no client is connected.
type NoClient struct{}

func (NoClient) Toast(_ string)                           {}
func (NoClient) Notice(_ string)                          {}
func (NoClient) AgentName(_ *Tab) string                  { return "" }
func (NoClient) AgentVisible(_ *Tab) bool                 { return false }
func (NoClient) Detect() Cmd                              { return nil }
func (NoClient) DropPane(_ *Tab, _ *PaneTab)              {}
func (NoClient) DropReviewPane(_ *Tab)                    {}
func (NoClient) FocusPane(_ *Tab, _ *PaneTab) Cmd         { return nil }
func (NoClient) FocusTerms(_ *Tab)                        {}
func (NoClient) NotifyChange(_ *Tab, _ bool, _ time.Time) {}
func (NoClient) NotifyReview(_ *Tab, _ string)            {}
func (NoClient) PinReview() Cmd                           { return nil }
func (NoClient) KeyName(_ string) string                  { return "" }
func (NoClient) Resize() Cmd                              { return nil }
func (NoClient) ResizePanes()                             {}
func (NoClient) SaveSession()                             {}
func (NoClient) ScreenKey(_ int) string                   { return "" }
func (NoClient) SetFocus(_ FocusArea)                     {}
func (NoClient) ShareOverlaps()                           {}
func (NoClient) ShellSize(_ *Tab, _ *PaneTab) (int, int)  { return 0, 0 }
func (NoClient) SkillOn(_ *Tab, _ string) bool            { return false }
func (NoClient) StopScrolling()                           {}
func (NoClient) TabSize(_ *Tab) (int, int)                { return 0, 0 }

func (NoClient) AskRepoSetup(_ []string, _ func() Cmd) Cmd { return nil }

func (NoClient) DialogClosed() bool                 { return false }
func (NoClient) OpenSpinoff(_ *Tab) Cmd             { return nil }
func (NoClient) OpenSwitchAgent() Cmd               { return nil }
func (NoClient) PaneName(_ *Tab, _ *PaneTab) string { return "" }

// AskPluginRun has it ask again: only a user can say yes.
func (NoClient) AskPluginRun(_ *Tab, _ plugin.Plugin, _ map[string]any, _ string, _ bool, decide func(answer string) Cmd) {
	decide(RunBusy)
}

func (NoClient) EnsurePluginUI(_ *Tab, _ plugin.Plugin, _ string)        {}
func (NoClient) PluginEvent(_ *Tab, _ string, _ map[string]any)          {}
func (NoClient) ShowPluginRun(_ *Tab, _ plugin.Plugin, _ *PluginRun) Cmd { return nil }
