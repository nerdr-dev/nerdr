package core

import (
	"sync"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/spawn"
	"github.com/nerdr-dev/nerdr/internal/usage"
)

// Core is nerdr with no user interface: the tabs, and what goes on in them.
type Core struct {
	// Mu guards all of Core; see Loop.
	Mu sync.Mutex `json:"-"`
	// Quitting is set once the program is on its way out.
	Quitting bool
	// Gen counts the changes that can change what a client draws: the
	// events the loop handles as core's, and the client's own state changed
	// by core through Client. A client that drew something at one Gen need
	// not draw it again while Gen is the same. It is not sent: a copy
	// counts its own (see Mirror.Apply), and in the part it would have
	// core's whole part sent again for every change, though nothing else
	// in it changed.
	Gen uint64 `json:"-"`

	// ID tells this core from the others a client may show, in a TabRef;
	// "" for the one on this machine.
	ID string
	// Client is told what the user is to see; see Client.
	Client Client `json:"-"`
	// Asking is what waits for the user to answer, the first asked first;
	// see Asked.
	Asking  []*Question
	lastAsk int

	// pluginRuns is the runs of plugins by agents; see pluginruns.go.
	PluginRuns PluginRuns
	// PluginUIs are the plugins' screens started, by PluginUIKey; and
	// PluginUIW and PluginUIH the size the client last drew one, for the
	// next to start at. PluginsGoneAt is when the plugins' folders were last
	// looked for, and PluginsMissing when each absent folder was first seen:
	// a plugin.toml written by rename is missing for a moment. See
	// pluginspanel.go.
	PluginUIs            map[string]*PluginUI
	PluginUIW, PluginUIH int
	PluginsGoneAt        time.Time
	PluginsMissing       map[string]time.Time

	// claudeLimits is the last of what a claude agent said of its
	// account's limits, and when; see usage.go.
	ClaudeLimits *usage.Report
	ClaudeAt     time.Time
	WtRoot       string // where new worktrees are made
	// skillUse is how much the agents have used each skill, for F4; see
	// skilluse.go.
	SkillUse SkillUse
	// SkillLooks are the last looks at where each agent has the skills,
	// by its tab's id; see skillon.go. A client is sent them in a part
	// of their own (PartSkills), not with the rest of core.
	SkillLooks map[string]SkillLook `json:"-"`
	// hookSockErr is why there is no hookSock, when there is not.
	HookSockErr string

	// Spawner starts the programs of the tabs: agents, terminals and
	// plugins. Nil is spawn.Local, or Held's holder; see Programs.
	Spawner spawn.Spawner `json:"-"`
	// Held is the holder the programs run in, when nerdr runs over one;
	// see held.go.
	Held *Held `json:"-"`

	Cfg config.Config `json:"-"`
	// ConfigFile is where Cfg's own settings are read from, on the
	// machine core runs on; see WatchConfigFile. cfgMod is when it was
	// last changed as core took it in, cfgWatch whether core watches it.
	ConfigFile string
	cfgMod     time.Time
	cfgWatch   bool
	// cfgRead is the number of the read of the file core goes by; see
	// configRead.
	cfgRead uint64
	// cfgNoted are the settings read only at start that changed in the
	// file, and to what, told once; see keepAtStart.
	cfgNoted    map[string]string
	Root        string // the folder nerdr was started in
	Tabs        []*Tab `json:"-"`
	AgentModels map[agents.Kind]ModelList
	// IssueWatch is the watching of the issues assigned to you; see
	// issuewatch.go.
	IssueWatch IssueWatch
	// Awake keeps the machine awake while keep_awake asks, made the first
	// time it is wanted; KeptAwake is set while it is asked to; AwakeErr is
	// why it could not, as last told; and LastBusy when an agent last
	// worked. See KeepAwake.
	Awake     Keeper `json:"-"`
	KeptAwake bool
	AwakeErr  string
	// Core's alone, and not sent: it changes at every look at the agents
	// while one works.
	LastBusy time.Time `json:"-"`
	// PRWatch is the watching of your pull requests; see prwatch.go.
	PRWatch PRWatchState
	// PRAll is everyone's pull requests, while a client shows them; see
	// prall.go.
	PRAll PRAll
	// Scan is the last look at the repositories; see reposcan.go.
	Scan RepoScan
	// NoGh is set when the gh CLI was not found, the last time core
	// looked; see GhMsg.
	NoGh bool
	// Agents are the agents whose commands are on the client's PATH,
	// looked for at AgentsAt; see agentson.go.
	Agents     []agents.Kind
	AgentsAt   time.Time
	agentsSoon bool
	// skillsAt is when every agent's skills were last looked at, and
	// skillsSoon has them looked at again at the next InfoTick; see
	// lookAtSkills.
	skillsAt   time.Time
	skillsSoon bool
	// convIndex is the agents' conversations read for the search; see
	// convsearch.go.
	convIndex *agents.Index
	// movesOut are the moves of tabs away from here, and movesIn the
	// folders the packs of moves to here are put in, by job; see move.go.
	movesOut map[string]*moveOut
	movesIn  map[string]string
	// Plugins are the user's plugins as last looked for, for a client's
	// copy; see LookForPlugins.
	Plugins PluginsLook
	// ReviewWatch is the watch on the worktree the review panel shows;
	// see reviewwatch.go.
	ReviewWatch reviewWatch `json:"-"`
	// GroupNames is the group name given to each folder of worktrees in
	// several repositories, kept when its tab is closed; see addTab.
	GroupNames map[string]string
	// SandboxedDirs are the worktree folders where a sandboxed agent has
	// run, kept across tab close and reopen.
	SandboxedDirs map[string]bool
	// SandboxedRepos keeps repository provenance even after all its
	// worktrees have been removed.
	SandboxedRepos map[string]bool
	// SandboxSources is where each of SandboxedRepos has its history from,
	// for the user: the tab or folder an agent ran in a sandbox in.
	SandboxSources map[string]string
	// Copy is set in a client's copy of core's state (see Mirror): what
	// core reads from disk, the copy has only as core sent it.
	Copy bool `json:"-"`
	// ClientEnv is the environment of the client last connected, nil
	// before one sends it: what the programs started since run with,
	// in place of the core's own. See BaseEnv.
	ClientEnv []string `json:"-"`
	// Unseen are the programs in the holder no tab took up, for the
	// client to show and the user to stop; see unseen.go.
	Unseen []UnseenProgram
	// hookSock is the socket herdr's integrations in the agents report to,
	// and hookBin nerdr itself, for those that report through the CLI.
	HookSock, HookBin string
	// InfoBusy is set while a tab's branches are being looked at; see
	// RefreshInfo. Overlaps is, per tab id, the other tabs changing some of
	// the same files, and Shared the same by file, for the review panel's
	// file list.
	InfoBusy bool
	Overlaps map[string][]Overlap
	Shared   map[string]map[FileKey][]string
	// CodexHooks are the arguments that give codex nerdr's hooks, once
	// codex trusts them, and codexAsked whether it has been asked; see
	// codexHookArgs.
	CodexHooks []string
	CodexAsked bool
	Wake       chan struct{} `json:"-"`
	// Screens sends the screens to a client in a process of its own, when
	// one is there; see ScreenServer.
	Screens *ScreenServer `json:"-"`
	// OnQuit is called once QuitAll has stopped everything: a core of its
	// own ends then.
	OnQuit func() `json:"-"`
	// programs are, in a copy of core's state, the programs core runs,
	// which the copy has no term.Pane of; see Runs.
	programs map[ProgramRef]bool
	// waitingStart are the agents restored, waiting to start until a
	// client says how large it shows them; see StartWhenSized.
	waitingStart *StartRestored
}

// Toast shows a short message to the user, if a client is there to show it.
func (c *Core) Toast(s string) { c.Tell(ToastMsg(s)) }

// Programs starts the programs of the tabs.
func (m *Core) Programs() spawn.Spawner {
	switch {
	case m.Spawner != nil:
		return m.Spawner
	case m.Held != nil:
		return m.Held.Client
	}
	return spawn.Local{}
}
