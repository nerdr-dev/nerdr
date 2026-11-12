// Command nerdr runs coding agents in tabs, each in its own git
// worktree, with a live review of what they change beside them.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sync"
	"syscall"

	"github.com/nerdr-dev/nerdr/internal/app"
	"github.com/nerdr-dev/nerdr/internal/browser"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/editor"
	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/holder"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/input"
	"github.com/nerdr-dev/nerdr/internal/sandboxcheck"
	"github.com/nerdr-dev/nerdr/internal/spawn"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/store"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

const usage = `nerdr — agents in git worktrees, one per tab, with a live review beside them

usage:
  nerdr              start in the current folder (restores its tabs, if any)
  nerdr connect HOST:FOLDER
                     the tabs of FOLDER on HOST, over ssh (nerdr installed
                     there too); FOLDER is ~ when not given
  nerdr sandbox      the repositories nerdr holds back in, an agent having
                     run in a sandbox there; clear forgets it (takes -h)
  nerdr help         this text
  nerdr version      which nerdr this is

For the agents in its tabs (each takes -h):
  nerdr why …        record why a change was made
  nerdr order …      record how to read a change
  nerdr walkthrough  record a guided walkthrough of a change
  nerdr spinoff FILE hand parts of an agent's work to new tabs
  nerdr handoff FILE hand an agent's work to the one its tab switches to
  nerdr post-review send a reviewer's code review of the worktree
  nerdr request-review say the work is ready for a code review
  nerdr send N MSG   send message MSG to agent N of the same tab
  nerdr agent TASK   start another agent in the same tab with TASK
  nerdr agents       list this tab's agents and their state
  nerdr wait N       wait until agent N of the tab is done
  nerdr read N       what agent N, or a terminal, of the tab shows
  nerdr terminal CMD run command CMD in a terminal pane the user sees
  nerdr close N      close agent N's pane, or a terminal, when the user asks
  nerdr tabs         list every tab: branches, PRs, CI and agents
  nerdr status TEXT  say where the tab's work stands, above the agent
  nerdr tab new …    make a tab on a new branch with a task
  nerdr search WORDS find past conversations with WORDS
  nerdr usage        the Claude and Codex plans' rate limits used
  nerdr plugin …     list the plugins there are, and run one

Start it in the folder that holds your repositories. New worktrees go in
%s, or where worktrees_dir in the config says.
Press ctrl+q ? inside for the keys.

config: %s
`

func main() {
	profile()
	// A nerdr that runs its programs itself, as it runs; see localExec.
	local := len(os.Args) == 2 && os.Args[1] == spawn.LocalArg
	if local {
		os.Args = os.Args[:1]
	}
	if len(os.Args) > 1 && os.Args[1] == core.PluginExecArg {
		os.Exit(core.ExecPlugin(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == sandboxcheck.Arg {
		// The core's look from inside an agent's sandbox; see
		// core.CheckSandbox.
		os.Exit(sandboxcheck.Main(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "holder" {
		os.Exit(holder.Main(os.Args[2:]))
	}
	// Where notes go: what nerdr told the agent, when this is nerdr run by
	// an agent (perhaps in a sandbox that cannot read the config), else
	// the config.
	if v, ok := os.LookupEnv(store.EnvDataDir); ok {
		store.SetDataDir(v)
	} else if cfg, err := config.Load(); err == nil {
		store.SetDataDir(cfg.DataDir)
	}
	if len(os.Args) > 1 {
		// These commands run in the caller's process/sandbox, not in the
		// host core. They exit below and must discover their own worktree.
		switch os.Args[1] {
		case "why", "order", "walkthrough", "spinoff":
			gitsafe.UseCallerRepository()
		}
		switch os.Args[1] {
		case "why":
			os.Exit(runWhy(os.Args[2:]))
		case "order":
			os.Exit(runOrder(os.Args[2:]))
		case "walkthrough":
			os.Exit(runWalkthrough(os.Args[2:]))
		case "spinoff":
			os.Exit(runSpinoff(os.Args[2:]))
		case "handoff":
			os.Exit(runHandoff(os.Args[2:]))
		case "post-review":
			os.Exit(runReview(os.Args[2:]))
		case "request-review":
			os.Exit(runRequestReview(os.Args[2:]))
		case "send":
			os.Exit(runSend(os.Args[2:]))
		case "agent":
			os.Exit(runAgent(os.Args[2:]))
		case "agents":
			os.Exit(runAgents(os.Args[2:]))
		case "wait":
			os.Exit(runWait(os.Args[2:]))
		case "read":
			os.Exit(runRead(os.Args[2:]))
		case "terminal":
			os.Exit(runTerminal(os.Args[2:]))
		case "close":
			os.Exit(runClose(os.Args[2:]))
		case "tabs":
			os.Exit(runTabs(os.Args[2:]))
		case "status":
			os.Exit(runStatus(os.Args[2:]))
		case "tab":
			os.Exit(runTab(os.Args[2:]))
		case "search":
			os.Exit(runSearch(os.Args[2:]))
		case "usage":
			os.Exit(runUsage(os.Args[2:]))
		case "plugin", "plugins":
			os.Exit(runPlugin(os.Args[2:]))
		case "sandbox":
			os.Exit(runSandbox(os.Args[2:]))
		case "hook":
			// nerdr's own hooks in claude; see hooks.ClaudeSettings.
			if len(os.Args) > 2 && os.Args[2] == "claude" {
				os.Exit(hooks.ClaudeCommand(os.Stdin, os.Stderr))
			}
			// nerdr's own plugin in opencode; see hooks.OpenCodePlugin.
			if len(os.Args) > 2 && os.Args[2] == "opencode" {
				os.Exit(hooks.OpenCodeCommand(os.Stdin, os.Stderr))
			}
			// nerdr's own hooks in codex; see hooks.CodexArgs.
			if len(os.Args) > 2 && os.Args[2] == "codex" {
				os.Exit(hooks.CodexCommand(os.Stdin, os.Stderr))
			}
			// nerdr's own hooks in copilot; see hooks.InstallCopilotPlugin.
			if len(os.Args) > 2 && os.Args[2] == "copilot" {
				os.Exit(hooks.CopilotCommand(os.Args[3:], os.Stdin, os.Stderr))
			}
			// nerdr's own hooks in agy; see hooks.InstallAgyHooks.
			if len(os.Args) > 2 && os.Args[2] == "agy" {
				os.Exit(hooks.AgyCommand(os.Args[3:], os.Stdin, os.Stderr))
			}
			// nerdr's own hooks in grok; see hooks.InstallGrokHooks.
			if len(os.Args) > 2 && os.Args[2] == "grok" {
				os.Exit(hooks.GrokCommand(os.Args[3:], os.Stdin, os.Stderr))
			}
			if len(os.Args) > 2 && os.Args[2] == "statusline" {
				os.Exit(hooks.StatusLineCommand(os.Stdin, os.Stdout, os.Stderr))
			}
			os.Exit(2)
		case "pane":
			// herdr's integrations in a tab report through this; see
			// internal/hooks.
			os.Exit(hooks.Command(os.Args[2:], os.Stderr))
		case "help":
			fmt.Printf(usage, config.DefaultWorktreesDir, config.Path())
			return
		case "version":
			fmt.Println("nerdr", version())
			return
		case "core":
			// nerdr's core with no terminal, as its holder starts it;
			// see runClient.
			os.Exit(coreMain())
		case "bridge":
			// A client on another machine reaching the core here over
			// ssh; see runBridge.
			os.Exit(runBridge(os.Args[2:]))
		case "connect":
			remote := ""
			if len(os.Args) > 2 {
				remote = os.Args[2]
			}
			if err := runClient(false, remote); err != nil {
				fmt.Fprintln(os.Stderr, "nerdr:", err)
				os.Exit(1)
			}
			return
		}
	}
	flag.Usage = func() { fmt.Fprintf(os.Stderr, usage, config.DefaultWorktreesDir, config.Path()) }
	showVersion := flag.Bool("version", false, "")
	flag.Parse()
	if *showVersion {
		fmt.Println("nerdr", version())
		return
	}
	if flag.NArg() > 0 {
		// A command nerdr does not have, a typo most likely: not a reason
		// to start in this folder.
		fmt.Fprintf(os.Stderr, "nerdr: no command %q\n\n", flag.Arg(0))
		flag.Usage()
		os.Exit(2)
	}

	if core.UseHolder() && os.Getenv(holder.EnvFD) == "" {
		// The agents in a core of their own, which the terminal is a
		// client of: what nerdr does unless NERDR_HOLDER=0, or a program
		// in a tab started it.
		if err := runClient(true, ""); err != nil {
			fmt.Fprintln(os.Stderr, "nerdr:", err)
			os.Exit(1)
		}
		return
	}
	if os.Getenv(holder.EnvFD) != "" {
		// Under a holder nerdr runs only as its core.
		fmt.Fprintln(os.Stderr, "nerdr: under its holder nerdr runs as `nerdr core`")
		os.Exit(holder.ExitDone)
	}
	if !local {
		localExec()
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "nerdr:", err)
		os.Exit(1)
	}
}

// localExec has a nerdr about to run its programs itself exec itself as
// `nerdr --nerdr-runs-its-programs`, the name it was started by kept, so
// that a core finds it among the processes a client runs under and
// refuses the client: one of its programs, out to drive the nerdr of
// another folder (see core.ClientUnderNerdr). Where it cannot, it runs on
// as it is.
func localExec() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	err = syscall.Exec(exe, []string{os.Args[0], spawn.LocalArg}, os.Environ())
	fmt.Fprintln(os.Stderr, "nerdr: could not mark itself as running its programs:", err)
}

// releaseVersion is the release tag, such as "v0.1.0", set by the release
// build with -ldflags "-X main.releaseVersion=...".
var releaseVersion string

// version is the release tag for a release build; otherwise the commit
// nerdr was built from, as Go records it, with "+changes" when the tree
// had changes not committed; "unknown" when it was not built from a git
// checkout.
func version() string {
	if releaseVersion != "" {
		return releaseVersion
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	rev, modified, at := "", false, ""
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		case "vcs.time":
			at = s.Value
		}
	}
	if rev == "" {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
		return "unknown"
	}
	out := rev[:min(len(rev), 12)]
	if modified {
		out += "+changes"
	}
	if at != "" {
		out += " (" + at + ")"
	}
	return out
}

// run is nerdr in the terminal, running the programs itself, without a
// holder: with NERDR_HOLDER=0, or when a program in a tab started it.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	browser.Setting = cfg.Browser
	editor.File, editor.Worktree = cfg.Editor, cfg.WorktreeEditor
	prefix, err := cfg.PrefixByte()
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	root, _ := filepath.EvalSymlinks(cwd)
	if root == "" {
		root = cwd
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("needs a terminal")
	}

	release, err := state.Lock(root)
	if err != nil {
		return err
	}
	defer release()

	sess, loadErr := state.Load(root)

	// herdr's agent integrations report to this socket; the program is
	// told once it runs.
	var send func(any)
	var sendMu sync.Mutex
	pass := func(r hooks.Report) {
		sendMu.Lock()
		s := send
		sendMu.Unlock()
		if s != nil {
			s(r)
		}
	}
	// One nerdr has the socket a sandbox is told of; another, started in
	// another folder, gets one of its own beside it.
	hookSrv, err := hooks.Listen(hooks.SocketPath(), pass)
	var taken error
	if errors.Is(err, hooks.ErrTaken) {
		taken = err
		hookSrv, err = hooks.Listen(hooks.ForRoot(hooks.SocketPath(), root), pass)
	}
	sockErr := err
	if err == nil {
		defer hookSrv.Close()
	}
	// Wake gpg-agent before any agent commits: a signed commit from a tab
	// otherwise waits on a pinentry nobody can see.
	if _, err := exec.LookPath("gpg-connect-agent"); err == nil {
		_ = exec.Command("gpg-connect-agent", "/bye").Run()
	}

	// nerdr reads the terminal itself (see input.Router), so it puts it
	// in raw mode itself too: bubbletea only does that for a terminal it
	// reads directly.
	old, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	defer func() { _ = term.Restore(int(os.Stdin.Fd()), old) }()

	router := input.New(os.Stdin, prefix)
	model := app.New(cfg, root, router, sess)
	model.SetVersion(version())
	// Core runs in this process, the fleet's one machine.
	local := model.Fleet().Home()
	if hookSrv != nil {
		local.Core.SetHookSocket(hookSrv.Path())
	} else {
		local.Core.SetHookSocketError(sockErr)
	}
	if taken != nil && hookSrv != nil {
		local.Core.SetSocketShared(taken)
	}
	if loadErr != nil {
		model.Notice("could not read the saved session, started fresh: " + loadErr.Error() +
			" — earlier copies are in " + state.SnapshotDir())
	}
	opts := []tea.ProgramOption{
		tea.WithAltScreen(),
		tea.WithInput(router.Reader()),
		tea.WithReportFocus(),
		tea.WithFPS(cfg.FPS), // only here: changing it takes a restart
	}
	if *cfg.Mouse {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	p := tea.NewProgram(model, opts...)
	router.SetSend(func(m any) { p.Send(m) })
	tell := local.Tell(func(m any) { p.Send(m) })
	model.Loop = core.NewLoop(&local.Core, func(ev core.Event) { tell(ev) })
	local.Core.FindAgents()
	sendMu.Lock()
	send = func(m any) { model.Loop.Post(m) }
	sendMu.Unlock()
	go router.Run()

	// The agents run in sessions of their own, so a closing terminal's
	// hangup does not reach them: stop them here instead.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGHUP, syscall.SIGTERM)
	go func() {
		<-sig
		p.Kill()
	}()

	// Autowrap off while nerdr draws: a line the terminal measures wider
	// than nerdr did — an emoji it draws wider, say — is cut at the edge,
	// rather than wrapped onto the next row, scrolling the whole screen
	// a row up and leaving rows bubbletea thinks it drew on the wrong ones.
	_, _ = os.Stdout.WriteString(ansi.ResetModeAutoWrap)
	_, err = p.Run()
	_, _ = os.Stdout.WriteString(ansi.SetModeAutoWrap)
	model.Shutdown()
	if err != nil && (!errors.Is(err, tea.ErrProgramKilled) || errors.Is(err, tea.ErrProgramPanic)) {
		return err
	}
	return nil
}
