// Package config reads ~/.config/nerdr/config.toml. Every setting has a
// default, so the file is optional.
package config

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/denied"

	"github.com/BurntSushi/toml"
)

// Config is the user's settings. Each is a client's or a core's (its scope
// tag; see Merge): how nerdr looks and tells you things is the client's,
// from the file on the machine you sit at; what runs, and where, is the
// core's, from the file on the machine its agents run on.
type Config struct {
	// Prefix is the key that starts a nerdr command, as "ctrl+<letter>".
	// Everything else typed goes to the agent in the active tab.
	Prefix string `toml:"prefix" scope:"client"`
	// RepoDirs are more folders to look for repositories in, besides the
	// one nerdr was started in: each is a repository or has them right
	// under it. "~/" is the home folder; a relative path is taken from the
	// folder nerdr was started in.
	RepoDirs []string `toml:"repo_dirs" scope:"core"`
	// WorktreesDir is where new worktrees go: DefaultWorktreesDir when
	// the file does not say. "~/" is the home folder; a relative path is
	// taken from the folder nerdr was started in, beside it when that is
	// a repository.
	WorktreesDir string `toml:"worktrees_dir" scope:"core"`
	// Agents overrides how each agent kind is started.
	Agents map[agents.Kind]agents.Spec `toml:"agents" scope:"core"`
	// Wrappers are scripts kept in the config, by name, for an agent's
	// command or check_command to start with: [wrappers.<name>]. See
	// Wrapper. Load has already put them in place in Agents.
	Wrappers map[string]Wrapper `toml:"wrappers" scope:"core"`
	// Notify is when an agent's tab tells you it needs you: a desktop
	// notification while nerdr's window does not have focus, a message in
	// the bar while you are in another tab. "blocked" (an agent needs
	// input), "done" (an agent finished), "all" (both) or "off".
	Notify string `toml:"notify" scope:"client"`
	// PRNotify watches your open pull requests (those P lists) and tells
	// you when a check fails or someone reviews one: in the bar, with a
	// mark that stays until you open the list, and on the desktop while
	// nerdr's window does not have focus. Off, GitHub is not asked.
	PRNotify *bool `toml:"pr_notify" scope:"client"`
	// IssueNotify watches the open issues assigned to you (those I lists
	// first) and tells you when one is newly assigned, the way PRNotify
	// tells of pull requests. Off, GitHub is not asked.
	IssueNotify *bool `toml:"issue_notify" scope:"client"`
	// ClaudeStatusLine starts claude with nerdr's status line, which tells
	// nerdr the account's rate limits (claude gives them to its status
	// line alone) and then shows the user's own status line, if set.
	ClaudeStatusLine *bool `toml:"claude_status_line" scope:"core"`
	// TabDetails shows each tab's change, PR and CI state in the tab bar.
	TabDetails *bool `toml:"tab_details" scope:"client"`
	// BottomBar is a row along the bottom of the screen: what wants a look
	// in all the tabs, the active tab's branch, changes and pull request,
	// and its tasks and the saved prompts, each a button for its screen.
	BottomBar *bool `toml:"bottom_bar" scope:"client"`
	// TabBar is where the repositories and their tabs are shown: "top",
	// two rows over the agent, or "left", two lists beside it, the
	// repositories over the active one's tabs; "left-small" is those
	// lists made small, to their lights.
	TabBar string `toml:"tab_bar" scope:"client"`
	// FunctionKeys is whether the review panel's menu and nerdr's hints
	// name its screens by F1-F4. Off, they name the prefix keys that do
	// the same (the prefix and g c, g g, e, k, h), for a keyboard where the F-keys are
	// hard to reach: a Mac laptop's are brightness and volume unless fn
	// is held. F1-F4 work either way.
	FunctionKeys *bool `toml:"function_keys" scope:"client"`
	// KeyPanel is whether a narrow column on the right shows the prefix's
	// keys all the time, each with what it does, a group opening below
	// itself when its key is pressed. Off, they show in a box in the middle
	// while nerdr waits for the key after the prefix (prefix_hint).
	KeyPanel *bool `toml:"key_panel" scope:"client"`
	// PrefixHint is whether a box in the middle of the screen lists the
	// keys after the prefix, when nerdr has waited a moment for the next.
	PrefixHint *bool `toml:"prefix_hint" scope:"client"`
	// PaneMinWidth is the narrowest, in columns, a tab's panes are shown
	// side by side: past it, the ones used longest ago are minimised to
	// the row over them.
	PaneMinWidth int `toml:"pane_min_width" scope:"client"`
	// DiffColors is the colour scheme F1 shows changes in: "github",
	// GitHub's dark diff, layout and syntax colours, or "nerdr", its own
	// soft tints.
	DiffColors string `toml:"diff_colors" scope:"client"`
	// Mouse is whether nerdr takes the mouse: clicks on tabs, menus and
	// buttons, the wheel, selecting an agent's output. Off, the terminal
	// keeps its own selection without shift.
	Mouse *bool `toml:"mouse" scope:"client"`
	// Shell is what a terminal pane (the prefix + t t) runs in a tab's
	// worktree; $SHELL when empty.
	Shell string `toml:"shell" scope:"core"`
	// Browser is the command a URL is opened with, the URL added last
	// ("firefox", "open -a Safari"); $BROWSER, then the system default,
	// when empty.
	Browser string `toml:"browser" scope:"client"`
	// Editor is what o in the review panel opens a file in ("code",
	// "nvim"); $VISUAL, then $EDITOR, then vim when empty. See
	// internal/editor for how the line is passed.
	Editor string `toml:"editor" scope:"client"`
	// WorktreeEditor is what the prefix + o opens the tab's worktree in,
	// the folder added last; VS Code ("code") when empty.
	WorktreeEditor string `toml:"worktree_editor" scope:"client"`
	// Install is where nerdr may write what it gives the agents — its
	// skills, and the plugins and hooks that tell it what an agent is
	// doing: "home" (the agents' own folders in the home folder),
	// "worktree" (the folder each tab's agent starts in, for agents in a
	// sandbox) or "off". Empty until the first start asks. Older config
	// files call it skills, still read where install is not set.
	Install       string   `toml:"install" scope:"core"`
	EnabledSkills []string `toml:"enabled_skills" scope:"core"`
	// DataDir is where nerdr keeps what agents record about a change —
	// explanations, code reviews, handoffs: empty for
	// the repository's git directory, a relative path for that folder
	// inside each worktree, an absolute one for one folder for all
	// repositories. See internal/store.
	DataDir string `toml:"data_dir" scope:"core"`

	// PortBase is the first port of the ranges given to tabs, and
	// PortsPerTab how many ports each tab is given, so a dev server in one
	// tab does not take the port of the same server in another: its agents
	// ask for them (nerdr ports), and its programs are told them in
	// NERDR_PORT and NERDR_PORT_END. 0 ports a tab gives tabs none.
	PortBase    int  `toml:"port_base" scope:"core"`
	PortsPerTab *int `toml:"ports_per_tab" scope:"core"`

	// MaxTabs is how many tabs may be open at once; a new one is refused
	// beyond it. 0 for no limit.
	MaxTabs *int `toml:"max_tabs" scope:"core"`
	// CloseMerged says in a tab's panel when its pull requests are merged,
	// and how to close it.
	CloseMerged *bool `toml:"close_merged" scope:"core"`
	// ColdDays marks a tab cold after this many days without the agent
	// working or you looking at it. 0 for never.
	ColdDays *int `toml:"cold_days" scope:"core"`
	// SleepAfter is how many minutes an agent that is done, and seen, is
	// left running in a tab nobody looks at before nerdr stops it, to
	// carry its conversation on when the tab is visited. 0 for never.
	SleepAfter int `toml:"sleep_after" scope:"core"`
	// HeadlessCols and HeadlessRows are the size, in columns and rows,
	// an agent or terminal starts at that no window shows yet, such as
	// one started from the API with nerdr's window closed. 0 for 120×40.
	HeadlessCols int `toml:"headless_cols" scope:"core"`
	HeadlessRows int `toml:"headless_rows" scope:"core"`
	// StatusLights is how the status lights are drawn: "dots", coloured
	// circles, or "symbols", a shape of its own for each state, for
	// those who cannot tell the colours apart.
	StatusLights string `toml:"status_lights" scope:"client"`
	// FPS is the most times a second the screen is drawn, from 1 to 120.
	// Fewer take less CPU, more show typing sooner. Read only at start.
	FPS int `toml:"fps" scope:"client"`
	// Servers are the other machines whose cores the TUI shows beside
	// this one's, each reached over ssh: [[servers]], in the order their
	// tabs are shown, after this machine's.
	Servers []Server `toml:"servers" scope:"client"`
	// GitHubRefresh is how many seconds apart the pull request of the tab
	// on screen is asked about; nerdr's other questions to GitHub are
	// spaced the same share longer or shorter than at the default.
	GitHubRefresh int `toml:"github_refresh" scope:"core"`
	// Forges says which service a host nerdr cannot tell by its name
	// runs: [forges] "git.example.com" = "gitlab". github.com and
	// gitlab.com need no line, nor a host with gitlab in its name; any
	// other is taken for GitHub Enterprise.
	Forges map[string]string `toml:"forges" scope:"core"`
	// RepoHost is the host a repository typed as owner/name is cloned
	// from, and a new one is made on: github.com when the file does not
	// say.
	RepoHost string `toml:"repo_host" scope:"core"`

	// KeepAwake keeps the machine from going to sleep: "busy" while an
	// agent works (and a minute after), "always" while nerdr runs, "off"
	// never. A locked screen stops no agent; a machine asleep stops all.
	KeepAwake string `toml:"keep_awake" scope:"core"`
	// KeepScreenOn keeps the screen from going dark and locking for being
	// idle as well, when the machine is kept awake: the screen of the
	// machine the TUI runs on, which keeps it on itself.
	KeepScreenOn *bool `toml:"keep_screen_on" scope:"client"`
	// KeepScreenMax is the most minutes the screen is kept on after the
	// last key or click in nerdr; then the desktop's own timeout holds
	// again, so a screen nobody looks at is not left lit (OLED burn-in).
	KeepScreenMax int `toml:"keep_screen_max" scope:"client"`

	// ConfirmPluginRuns has the user say yes to every run of a plugin an
	// agent asks for, seeing what it gives it; off, only to those of a
	// plugin whose plugin.toml says confirm.
	ConfirmPluginRuns bool `toml:"confirm_plugin_runs" scope:"core"`
	// OpenGlobalPlugins has the global plugins' dialog open by itself when
	// an agent runs one, on the plugin, unless its plugin.toml says open =
	// false; off, a note says so instead.
	OpenGlobalPlugins *bool `toml:"open_global_plugins" scope:"client"`

	// Review is the code review a second agent does of a tab's work; see
	// internal/codereview.
	Review Review `toml:"review" scope:"core"`

	// Auto is what nerdr asks a tab's agent by itself when something
	// happens to the tab's pull request; see Auto and core's auto.go.
	Auto Auto `toml:"auto" scope:"core"`

	// dataDirSet is whether the file sets data_dir, so the first-start
	// setup does not write it a second time; worktreesDirSet the same for
	// worktrees_dir.
	dataDirSet, worktreesDirSet bool
}

// Wrapper is a [wrappers.<name>] table: a script for the wrapper an
// agent is started through, such as one that puts it in a sandbox, kept in
// the config rather than in a file of its own. An agent's command or
// check_command whose first word is the wrapper's name runs
//
//	<shell> -c <script> <name> <the rest of the command> <nerdr's arguments>
//
// so the script has its name as $0, and the rest, with what nerdr adds
// after it, as "$@".
type Wrapper struct {
	Script string `toml:"script"`
	// Shell is the program that runs Script: DefaultWrapperShell when
	// empty. A name on PATH or an absolute path, of a shell that takes -c.
	Shell string `toml:"shell"`
}

// DefaultWrapperShell is a wrapper's shell when it names none.
const DefaultWrapperShell = "bash"

// wrapperName is what a wrapper's name, or its shell when not a path, may
// be: one plain word, as a command's first word on PATH is.
var wrapperName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

// Wrapped is argv with c's wrapper in place of its first word, when that
// is a wrapper's name; argv as it is otherwise.
func (c Config) Wrapped(argv []string) []string {
	if len(argv) == 0 {
		return argv
	}
	w, ok := c.Wrappers[argv[0]]
	if !ok {
		return argv
	}
	return slices.Concat(w.prefix(argv[0]), argv[1:])
}

// prefix is what w, named name, puts in place of its name in a command.
func (w Wrapper) prefix(name string) []string {
	shell := w.Shell
	if shell == "" {
		shell = DefaultWrapperShell
	}
	return []string{shell, "-c", w.Script, name}
}

// WrapSpec is s with c's wrappers in place in its command and
// check_command, as Load puts them.
func (c Config) WrapSpec(s agents.Spec) agents.Spec {
	if len(s.Command) > 0 {
		_, s.Wrapped = c.Wrappers[s.Command[0]]
	}
	s.Command, s.CheckCommand = c.Wrapped(s.Command), c.Wrapped(s.CheckCommand)
	return s
}

// useWrappers checks c's wrappers and puts each in place in the agents'
// command and check_command that start with its name, so that everything
// that runs an agent's command, the sandbox check among them, runs it.
func useWrappers(c *Config) error {
	for _, name := range slices.Sorted(maps.Keys(c.Wrappers)) {
		w := c.Wrappers[name]
		switch {
		case !wrapperName.MatchString(name):
			return fmt.Errorf("[wrappers.%s]: a wrapper's name is one word of letters, digits, _, . and -", name)
		case strings.TrimSpace(w.Script) == "":
			return fmt.Errorf("[wrappers.%s]: script is empty", name)
		case w.Shell != "" && !filepath.IsAbs(w.Shell) && !wrapperName.MatchString(w.Shell):
			return fmt.Errorf("[wrappers.%s]: shell is a program on PATH, such as \"sh\", or an absolute path", name)
		}
	}
	for k, s := range c.Agents {
		c.Agents[k] = c.WrapSpec(s)
	}
	return nil
}

// DefaultWorktreesDir is where new worktrees go when the config file does
// not say: one folder wherever nerdr is started, not one beside each
// folder it happens to be started in.
const DefaultWorktreesDir = "~/worktrees-nerdr"

// DefaultFPS is how many times a second the screen may be drawn when the
// config file does not say.
const DefaultFPS = 60

// DefaultPortBase and DefaultPortsPerTab are port_base and ports_per_tab
// when the config file does not say: from 20000, below the ports Linux and
// macOS hand out to programs that ask for any (32768 and 49152 up), ten
// to a tab.
const (
	DefaultPortBase    = 20000
	DefaultPortsPerTab = 10
)

// DefaultPaneMinWidth is pane_min_width when the file does not say.
const DefaultPaneMinWidth = 60

// DefaultGitHubRefresh is GitHubRefresh when the config file does not
// set it, and what the intervals it scales are written for.
const DefaultGitHubRefresh = 90

// Review is the [review] table.
type Review struct {
	// Auto starts a review by itself, at the end of a turn that changed
	// the worktree since it was last reviewed; When says which turns. Off,
	// the prefix + R starts one.
	Auto bool `toml:"auto"`
	// When is which turns end in a review with Auto: "ready", the ones in
	// which the agent said the work is ready for one (`nerdr
	// request-review`), or "turn", every one that changed the worktree.
	When string `toml:"when"`
	// Agent is the agent that reviews: a kind ("codex"), or "other" for
	// another kind than the tab's own where one is installed.
	Agent string `toml:"agent"`
	// Rounds is the most times the reviewer looks before the user decides.
	Rounds int `toml:"rounds"`
	// SendFindings is what is done with the findings: "ask" keeps them in
	// the tab's messages (F4) for you to send, "auto" sends them to the
	// tab's agent at once.
	SendFindings string `toml:"send_findings"`
	// Args are added to the reviewer's command line, by kind: a mode that
	// keeps it from changing files, say.
	Args map[agents.Kind][]string `toml:"args"`
	// Models are the models reviewers are started with, by kind, in place
	// of the one they start with otherwise; picked in the review pane or
	// given to nerdr request-review, another goes for that review.
	Models map[agents.Kind]string `toml:"models"`
	// ModelNames is who turns the model the user named for a review, as
	// they said it ("astra 6.2"), into the agent's own name for it:
	// "nerdr", from the models the agent lists, or "agent", the agent
	// asking for the review, which looks the name up itself.
	ModelNames string `toml:"model_names"`
}

// Review settings.
const (
	ReviewOther  = "other"
	FindingsAsk  = "ask"
	FindingsAuto = "auto"
	WhenReady    = "ready"
	WhenTurn     = "turn"
	NamesByNerdr = "nerdr"
	NamesByAgent = "agent"
)

// Auto is the [auto] table: for each thing that can happen to a pull
// request of yours that a tab works on, what nerdr does by itself. "off"
// does nothing (the news of it is told as before), "ask" writes the
// message F2 would and keeps it in the tab's F3 for you to read and send,
// telling you so, and "auto" has it sent to the tab's agent when it is
// idle. A repository's .nerdr.toml may set them for itself; see
// workspace.RepoAuto.
type Auto struct {
	// CIFailed is when checks have come to fail on the PR's head, once
	// none is still running: fix the build, with the failing jobs' logs.
	CIFailed string `toml:"ci_failed"`
	// ReviewComments is when a review that is not an approval comes in:
	// draft replies to the reviews and comments.
	ReviewComments string `toml:"review_comments"`
	// Conflicts is when the PR has come to conflict with its base: rebase
	// onto it.
	Conflicts string `toml:"conflicts"`
	// MaxTries is how many times in a row one of them is sent by itself
	// for one pull request, without it being cleared up between (the
	// checks passing, an approval, the conflict gone): the next is kept
	// for you, as with "ask".
	MaxTries int `toml:"max_tries"`
	// Wake lets an auto message wake a tab whose agent is asleep, or that
	// has gone cold; off, it waits there for you as with "ask".
	Wake bool `toml:"wake"`
}

// The [auto] settings of each event.
const (
	AutoOff  = "off"
	AutoAsk  = "ask"
	AutoSend = "auto"
)

// The [auto] events, by their keys.
const (
	EventCIFailed       = "ci_failed"
	EventReviewComments = "review_comments"
	EventConflicts      = "conflicts"
)

// AutoEvents is every [auto] event, in the order they are told of.
var AutoEvents = []string{EventCIFailed, EventReviewComments, EventConflicts}

// DefaultAutoTries is max_tries when the file does not set it.
const DefaultAutoTries = 3

// Mode is what is set for event, AutoOff for an unknown one.
func (a Auto) Mode(event string) string {
	switch event {
	case EventCIFailed:
		return a.CIFailed
	case EventReviewComments:
		return a.ReviewComments
	case EventConflicts:
		return a.Conflicts
	}
	return AutoOff
}

// AutoMode is s as an [auto] setting: AutoOff for anything else.
func AutoMode(s string) string {
	switch s {
	case AutoAsk, AutoSend:
		return s
	}
	return AutoOff
}

// KeepAwake settings, besides "off".
const (
	KeepBusy   = "busy"
	KeepAlways = "always"
)

// Install settings.
const (
	InstallHome     = "home"
	InstallWorktree = "worktree"
	InstallOff      = "off"
)

// renamed is the settings before any table that had another name once,
// by the name they have now: the old name is read where the new one is
// not set, and taken out when the new one is written.
var renamed = map[string]string{"install": "skills"}

// NewRepoHost is RepoHost, github.com when the file does not say.
func (c Config) NewRepoHost() string {
	if h := strings.ToLower(strings.TrimSpace(c.RepoHost)); h != "" {
		return h
	}
	return "github.com"
}

// DataDirSet reports whether the config file sets data_dir.
func (c Config) DataDirSet() bool { return c.dataDirSet }

// WorktreesDirSet reports whether the config file sets worktrees_dir.
func (c Config) WorktreesDirSet() bool { return c.worktreesDirSet }

// NotifyOn reports whether a tab entering the given state ("blocked" or
// "done") should be notified.
func (c Config) NotifyOn(state string) bool {
	switch c.Notify {
	case "all":
		return true
	case "off":
		return false
	}
	return c.Notify == state
}

// Path is where the config file lives.
func Path() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "nerdr", "config.toml")
}

// Load reads the config file, filling in defaults. A missing file is not
// an error.
func Load() (Config, error) {
	c := Config{}
	data, err := os.ReadFile(Path())
	if err == nil {
		md, err := toml.Decode(string(data), &c)
		if err != nil {
			return defaults(Config{}), fmt.Errorf("%s: %w", Path(), err)
		}
		if err := useWrappers(&c); err != nil {
			return defaults(Config{}), fmt.Errorf("%s: %w", Path(), err)
		}
		c.dataDirSet = md.IsDefined("data_dir")
		c.worktreesDirSet = md.IsDefined("worktrees_dir")
		if !md.IsDefined("install") {
			var old struct {
				Skills string `toml:"skills"`
			}
			_, _ = toml.Decode(string(data), &old)
			c.Install = old.Skills
		}
	} else if !os.IsNotExist(err) {
		return defaults(Config{}), err
	}
	return defaults(c), nil
}

func defaults(c Config) Config {
	if c.Prefix == "" {
		// ctrl+q: free in claude and opencode (ctrl+g, the first choice,
		// opens claude's external editor), and within reach on a Nordic
		// keyboard, unlike ctrl+] or ctrl+\.
		c.Prefix = "ctrl+q"
	}
	for i, d := range c.RepoDirs {
		c.RepoDirs[i] = ExpandHome(d)
	}
	if c.WorktreesDir == "" {
		c.WorktreesDir = DefaultWorktreesDir
	}
	c.WorktreesDir = ExpandHome(c.WorktreesDir)
	switch c.Notify {
	case "":
		c.Notify = "all"
	case "all", "blocked", "done", "off":
	default:
		c.Notify = "all"
	}
	if c.TabDetails == nil {
		t := true
		c.TabDetails = &t
	}
	if c.BottomBar == nil {
		t := true
		c.BottomBar = &t
	}
	switch c.TabBar {
	case "top", "left", "left-small":
	default:
		c.TabBar = "left"
	}
	if c.Mouse == nil {
		t := true
		c.Mouse = &t
	}
	switch c.DiffColors {
	case "nerdr", "github":
	default:
		c.DiffColors = "github"
	}
	switch c.StatusLights {
	case "dots", "symbols":
	default:
		c.StatusLights = "dots"
	}
	if c.PortBase <= 0 {
		c.PortBase = DefaultPortBase
	}
	c.PortBase = min(max(c.PortBase, 1024), 65535)
	if c.PortsPerTab == nil || *c.PortsPerTab < 0 {
		n := DefaultPortsPerTab
		c.PortsPerTab = &n
	} else if *c.PortsPerTab > 1000 {
		n := 1000
		c.PortsPerTab = &n
	}
	c.SleepAfter = min(max(c.SleepAfter, 0), 24*60)
	c.HeadlessCols = min(max(c.HeadlessCols, 0), 500)
	c.HeadlessRows = min(max(c.HeadlessRows, 0), 300)
	if c.PaneMinWidth <= 0 {
		c.PaneMinWidth = DefaultPaneMinWidth
	}
	for _, b := range []**bool{&c.CloseMerged, &c.PRNotify, &c.IssueNotify, &c.FunctionKeys, &c.PrefixHint, &c.ClaudeStatusLine, &c.OpenGlobalPlugins} {
		if *b == nil {
			t := true
			*b = &t
		}
	}
	if c.MaxTabs == nil || *c.MaxTabs < 0 {
		n := 20
		c.MaxTabs = &n
	}
	if c.ColdDays == nil || *c.ColdDays < 0 {
		n := 5
		c.ColdDays = &n
	}
	if c.FPS <= 0 {
		c.FPS = DefaultFPS
	}
	c.FPS = min(c.FPS, 120)
	if c.GitHubRefresh <= 0 {
		c.GitHubRefresh = DefaultGitHubRefresh
	}
	c.GitHubRefresh = min(max(c.GitHubRefresh, 15), 3600)
	switch c.KeepAwake {
	case KeepBusy, KeepAlways, "off":
	default:
		c.KeepAwake = KeepBusy
	}
	if c.KeepScreenMax <= 0 {
		c.KeepScreenMax = 60
	}
	if c.KeepScreenOn == nil {
		f := false
		c.KeepScreenOn = &f
	}
	if c.Agents == nil {
		c.Agents = map[agents.Kind]agents.Spec{}
	}
	if c.Review.Agent == "" {
		c.Review.Agent = ReviewOther
	}
	if c.Review.Rounds <= 0 {
		c.Review.Rounds = 2
	}
	if c.Review.SendFindings != FindingsAuto {
		c.Review.SendFindings = FindingsAsk
	}
	if c.Review.When != WhenTurn {
		c.Review.When = WhenReady
	}
	if c.Review.ModelNames != NamesByAgent {
		c.Review.ModelNames = NamesByNerdr
	}
	c.Auto.CIFailed = AutoMode(c.Auto.CIFailed)
	c.Auto.ReviewComments = AutoMode(c.Auto.ReviewComments)
	c.Auto.Conflicts = AutoMode(c.Auto.Conflicts)
	if c.Auto.MaxTries <= 0 {
		c.Auto.MaxTries = DefaultAutoTries
	}
	c.Auto.MaxTries = min(c.Auto.MaxTries, 20)
	switch c.Install {
	case InstallHome, InstallWorktree, InstallOff:
	default:
		c.Install = "" // not a setting: asked for again at start
	}
	return c
}

// ExpandHome turns a leading "~" of p into the home folder.
func ExpandHome(p string) string {
	home, _ := os.UserHomeDir()
	if home != "" && (p == "~" || strings.HasPrefix(p, "~/")) {
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
}

// SetKey sets key in table ("" for the keys before any table, "agents.claude"
// for [agents.claude]) to value, a TOML value as written in the file, and
// with value "" removes it, so the default holds again. The rest of the file
// is left as it was: the key replaced where it is, added at the top of the
// table where not, and the table added at the end where there is none. An
// edit that would not read as a config is refused, the file untouched.
func SetKey(table, key, value string) error {
	return SetKeys(Edit{table, key, value})
}

// Edit is a key to set by SetKeys, as SetKey takes it.
type Edit struct {
	Table, Key, Value string
}

// SetKeys is SetKey of each of edits, in one write of the file: all of
// them, or none when the file would not read.
func SetKeys(edits ...Edit) error {
	return editConfig(func(text string) string { return setKeys(text, edits) })
}

func setKeys(text string, edits []Edit) string {
	for _, e := range edits {
		if old, ok := renamed[e.Key]; ok && e.Table == "" {
			text = setKey(text, "", old, "")
		}
		text = setKey(text, e.Table, e.Key, e.Value)
	}
	return text
}

// keyName is how key is written on the left of =: bare where it can be.
func keyName(key string) string {
	if regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(key) {
		return key
	}
	return strconv.Quote(key)
}

func setKey(text, table, key, value string) string {
	name := keyName(key)
	line := ""
	if value != "" {
		line = name + " = " + value + "\n"
	}
	var headEnd int
	if table != "" {
		head := regexp.MustCompile(`(?m)^[ \t]*\[[ \t]*` + regexp.QuoteMeta(table) + `[ \t]*\][^\n]*(\n|$)`).FindStringIndex(text)
		if head == nil {
			if line == "" {
				return text
			}
			if text != "" && !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
			return text + "\n[" + table + "]\n" + line
		}
		headEnd = head[1]
	}
	body := text[headEnd:]
	end := nextTable(body)
	part := body[:end]
	switch spans := keySpans(part, name); {
	case len(spans) > 0:
		// The first in its place, any more taken out.
		for i := len(spans) - 1; i >= 0; i-- {
			with := ""
			if i == 0 {
				with = line
			}
			part = part[:spans[i][0]] + with + part[spans[i][1]:]
		}
	case line != "":
		part = line + part
	}
	if line != "" && headEnd > 0 && !strings.HasSuffix(text[:headEnd], "\n") {
		part = "\n" + part
	}
	return text[:headEnd] + part + body[end:]
}

// Quote is s as a TOML string.
func Quote(s string) string { return strconv.Quote(s) }

// List is ss as a TOML array of strings.
func List(ss []string) string { return skillList(ss) }

// Table is m as a TOML inline table of strings, by name.
func Table(m map[string]string) string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, keyName(k)+" = "+strconv.Quote(m[k]))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// editConfig rewrites the config file with edit, as long as what it gives
// is still a config that reads: read, edited and written with the file to
// itself (see locked), so that two edits at once both hold.
func editConfig(edit func(string) string) error {
	return locked(func() error { return editUnlocked(edit) })
}

func editUnlocked(edit func(string) string) error {
	old, err := os.ReadFile(Path())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	data := []byte(edit(string(old)))
	var check Config
	if _, err := toml.Decode(string(data), &check); err != nil {
		return err
	}
	if err := denied.Err(os.MkdirAll(filepath.Dir(Path()), 0755)); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(Path()), ".nerdr-config-*")
	if denied.Err(err) != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return denied.Err(os.Rename(f.Name(), Path()))
}

// nextTable is where the first [table] header in text starts, len(text)
// when there is none. A line starting with [ inside a value of several
// lines — an array of arrays, say — is no header.
func nextTable(text string) int {
	for i := 0; i < len(text); {
		lineEnd := strings.IndexByte(text[i:], '\n')
		if lineEnd < 0 {
			lineEnd = len(text)
		} else {
			lineEnd += i + 1
		}
		trimmed := strings.TrimLeft(text[i:lineEnd], " \t")
		switch {
		case strings.HasPrefix(trimmed, "["):
			return i
		case strings.HasPrefix(trimmed, "#"), strings.TrimSpace(trimmed) == "":
			i = lineEnd
		default:
			// A key: past its value, which may go on for several lines.
			if eq := strings.IndexByte(trimmed, '='); eq >= 0 {
				i = valueEnd(text, i+(len(text[i:lineEnd])-len(trimmed))+eq+1)
			} else {
				i = lineEnd
			}
		}
	}
	return len(text)
}

// keySpans are where the lines setting key are in text, a table's body
// with no header in it: from the start of each line to past the end of
// its value, which may go on for several lines, and the line's end.
func keySpans(text, key string) [][2]int {
	var spans [][2]int
	at := regexp.MustCompile(`^[ \t]*` + regexp.QuoteMeta(key) + `[ \t]*=`)
	for i := 0; i < len(text); {
		lineEnd := strings.IndexByte(text[i:], '\n')
		if lineEnd < 0 {
			lineEnd = len(text)
		} else {
			lineEnd += i + 1
		}
		line := text[i:lineEnd]
		if m := at.FindStringIndex(line); m != nil {
			end := valueEnd(text, i+m[1])
			spans = append(spans, [2]int{i, end})
			i = end
			continue
		}
		if trimmed := strings.TrimLeft(line, " \t"); trimmed != "" && trimmed[0] != '#' {
			if eq := strings.IndexByte(trimmed, '='); eq >= 0 {
				i = valueEnd(text, i+(len(line)-len(trimmed))+eq+1)
				continue
			}
		}
		i = lineEnd
	}
	return spans
}

// valueEnd is where the TOML value starting at i in text ends, past the
// end of its last line: an array or inline table over several lines, or a
// multi-line string, is followed to where it closes, its strings and
// comments read past.
func valueEnd(text string, i int) int {
	depth := 0
	for i < len(text) {
		c := text[i]
		switch {
		case strings.HasPrefix(text[i:], `"""`), strings.HasPrefix(text[i:], "'''"):
			i = multilineEnd(text, i)
			continue
		case c == '"' || c == '\'':
			// To past its closing quote, or to the end of the line, which
			// the loop then takes as the value's end.
			for i++; i < len(text) && text[i] != c && text[i] != '\n'; i++ {
				if c == '"' && text[i] == '\\' {
					i++ // an escaped character, perhaps a quote
				}
			}
			if i < len(text) && text[i] == c {
				i++
			}
			continue
		case c == '#':
			for i < len(text) && text[i] != '\n' {
				i++
			}
			continue
		case c == '[' || c == '{':
			depth++
		case c == ']' || c == '}':
			depth--
		case c == '\n' && depth <= 0:
			return i + 1
		}
		i++
	}
	return len(text)
}

// multilineEnd is where the multi-line string starting at i in text, with
// three double or three single quotes, ends: past the run of quotes that closes it, which may hold
// one or two of the string's own before the three that end it. A basic
// string's escaped characters do not end it.
func multilineEnd(text string, i int) int {
	q := text[i]
	for i += 3; i < len(text); i++ {
		switch {
		case q == '"' && text[i] == '\\':
			i++
		case strings.HasPrefix(text[i:], strings.Repeat(string(q), 3)):
			for i < len(text) && text[i] == q {
				i++
			}
			return i
		}
	}
	return len(text)
}

// sameAsKey are the ctrl keys a terminal sends as the same byte as a key
// of their own: taking one as the prefix would take that key from the
// agent too, and bubbletea names them after the other key.
var sameAsKey = map[byte]string{
	'h': "backspace",
	'i': "tab",
	'j': "enter (a newline)",
	'm': "enter",
}

// PrefixByte is the control byte the prefix key sends, e.g. 0x11 for ctrl+q.
func (c Config) PrefixByte() (byte, error) {
	k := strings.ToLower(strings.TrimSpace(c.Prefix))
	letter, ok := strings.CutPrefix(k, "ctrl+")
	if ok && letter == "space" {
		return 0, nil
	}
	if !ok || len(letter) != 1 {
		return 0, fmt.Errorf("prefix %q: only ctrl+<key> is supported", c.Prefix)
	}
	ch := letter[0]
	if other, ok := sameAsKey[ch]; ok {
		return 0, fmt.Errorf("prefix %q: the terminal sends it as %s, choose another", c.Prefix, other)
	}
	switch {
	case ch >= 'a' && ch <= 'z':
		return ch - 'a' + 1, nil
	case ch == '@':
		return 0, nil
	case ch == '\\':
		return 0x1c, nil
	case ch == ']':
		return 0x1d, nil
	}
	return 0, fmt.Errorf("prefix %q: only ctrl+<letter> is supported", c.Prefix)
}

// PrefixKey is the prefix as bubbletea names the key, for comparing with
// what it reports: ctrl+space arrives as ctrl+@.
func (c Config) PrefixKey() string {
	k := strings.ToLower(strings.TrimSpace(c.Prefix))
	if k == "ctrl+space" {
		return "ctrl+@"
	}
	return k
}

// SkillEnabled is whether the skill called name is turned on: put in
// place by nerdr. Features that rely on skill discovery check Model.skillOn;
// explicit UI requests can instead carry their own instructions. With install
// "off" nerdr puts none in place.
// Without a selection, every skill is on.
func (c Config) SkillEnabled(name string) bool {
	if c.EnabledSkills == nil {
		return true
	}
	return slices.Contains(c.EnabledSkills, name)
}

func skillList(names []string) string {
	var quoted []string
	for _, n := range names {
		quoted = append(quoted, strconv.Quote(n))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// Raw is what the config file sets, as TOML has it, without defaults: for
// the settings dialog to tell a setting from its default.
func Raw() (map[string]any, error) {
	data, err := os.ReadFile(Path())
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return map[string]any{}, err
	}
	return RawText(string(data))
}

// Text is the config file as written, "" when there is none.
func Text() (string, error) {
	data, err := os.ReadFile(Path())
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(data), err
}

// RawText is Raw of a config file's text, as Text read it, maybe on
// another machine.
func RawText(text string) (map[string]any, error) {
	raw := map[string]any{}
	_, err := toml.Decode(text, &raw)
	for k, old := range renamed {
		if v, ok := raw[old]; ok {
			if _, set := raw[k]; !set {
				raw[k] = v
			}
			delete(raw, old)
		}
	}
	return raw, err
}
