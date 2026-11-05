package core

import (
	"sync"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/herdr"
	"github.com/nerdr-dev/nerdr/internal/usage"
)

// Core is nerdr with no user interface: the tabs, and what goes on in them.
type Core struct {
	// Mu guards all of Core; see Loop.
	Mu sync.Mutex
	// Quitting is set once the program is on its way out.
	Quitting bool
	// Gen counts the changes that can change what a client draws: the
	// events the loop handles as core's, and the client's own state changed
	// by core through Client. A client that drew something at one Gen need
	// not draw it again while Gen is the same.
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
	// SkillLook is the last look at where an agent has the skills; see
	// SkillStates.
	SkillLook SkillLook
	// hookSockErr is why there is no hookSock, when there is not.
	HookSockErr string

	Cfg         config.Config
	Root        string // the folder nerdr was started in
	Herdr       *herdr.Client
	Tabs        []*Tab
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
func (c *Core) Toast(s string) { c.Tell(ToastMsg(s)) }
