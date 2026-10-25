package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"

	"nerdr/internal/hooks"
	"nerdr/internal/plugin"
)

// In a nerdr tab, an agent's run of a plugin is nerdr's: the agent asks
// the nerdr running its tab to run it (hooks.PluginRun), which does, as
// the user, outside any sandbox the agent is in, once the user has
// allowed the plugin as it is — and perhaps said yes to this run. Then
// this asks for what it prints until it ends (hooks.PluginRead), and
// prints that, ending with its exit status. Stopped, it stops the run.
// A plugin that watches goes on: this returns once it has started, and
// nerdr sends the agent what it prints.

// The answers nerdr gives a run it did not start: the user has not
// allowed the plugin as it is, said no to the run, was busy with another
// question, or did not answer in time.
const (
	notAllowed = "not allowed"
	declined   = "declined"
	busy       = "busy"
	unanswered = "unanswered"
)

// whyNot says to the agent what each of those means for it.
var whyNot = map[string]string{
	notAllowed: "the user has not allowed it as it is now. They allow it in nerdr's F5, with a, after reading it: " +
		"tell them, and do not copy or change it to get round this.",
	declined:   "the user said no to this run. Do not run it again unless they ask you to.",
	busy:       "the user was answering another question in nerdr, so they were not asked. Try again in a little while.",
	unanswered: "the user was asked to say yes to the run, and did not answer in time. Tell them it waits for them, and run it again when they ask.",
}

// agentTab is the tab and socket of the nerdr whose agent this runs
// under, when it does: not in a tab's shell, which is the user's.
func agentTab() (tab, sock string, ok bool) {
	tab, sock = os.Getenv("NERDR_TAB"), os.Getenv("NERDR_SOCKET")
	return tab, sock, os.Getenv("NERDR") == "1" && os.Getenv("NERDR_SHELL") != "1" && tab != "" && sock != ""
}

// remoteRun has the nerdr running the tab run p with vals in dir, and
// prints what it prints; its exit status is the run's.
func remoteRun(p plugin.Plugin, vals plugin.Values, dir string) int {
	const name = "nerdr plugin run"
	tab, sock, _ := agentTab()
	answer, err := hooks.RequestWithin(sock, hooks.Plugin,
		map[string]any{"pane_id": tab, "what": hooks.PluginRun, "name": p.Name, "values": vals, "dir": dir}, hooks.PluginAskWait)
	if err = explain(sock, err); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
		return 1
	}
	run, started := strings.CutPrefix(answer, "started ")
	if !started {
		why := whyNot[answer]
		if why == "" {
			why = answer
		}
		fmt.Fprintf(os.Stderr, "%s: %s did not run: %s\n", name, p.Name, why)
		return 1
	}
	if p.Watch {
		fmt.Printf("Started %s, run %s: it goes on watching. What it prints comes to you as messages from nerdr, "+
			"when you are idle: there is nothing to wait for or poll. Stop it with: nerdr plugin stop %s\n", p.Name, run, p.Name)
		return 0
	}
	// Stopped, as an agent stops a command it has waited on too long, the
	// run stops too.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-sig
		_ = hooks.Send(sock, hooks.Plugin, map[string]any{"pane_id": tab, "what": hooks.PluginStop, "run": run})
		os.Exit(130)
	}()
	offset := 0
	for {
		text, err := hooks.Request(sock, hooks.Plugin, map[string]any{"pane_id": tab, "what": hooks.PluginRead, "run": run, "offset": offset})
		if err = explain(sock, err); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s: %v\n", name, p.Name, err)
			return 1
		}
		var out hooks.RunOutput
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s: could not read nerdr's answer\n", name, p.Name)
			return 1
		}
		if out.Lost {
			fmt.Println("[… some of what it printed is no longer kept]")
		}
		os.Stdout.WriteString(out.Text)
		offset = out.Offset
		if out.Ended && out.Text == "" {
			return out.Exit
		}
	}
}

var runID = regexp.MustCompile(`^[0-9a-f]{16}$`)

// stopTarget says whether arg names a run or a plugin. A plugin named as
// 16 hex digits is still a plugin's name.
func stopTarget(arg string) (field, value string) {
	if runID.MatchString(arg) {
		for _, p := range findPlugins() {
			if p.Name == arg {
				return "name", arg
			}
		}
		return "run", arg
	}
	return "name", arg
}

// pluginStop is `nerdr plugin stop NAME|RUN`.
func pluginStop(args []string) int {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		fmt.Fprint(os.Stderr, pluginUsage)
		return 2
	}
	p := map[string]any{"what": hooks.PluginStop}
	field, value := stopTarget(args[0])
	p[field] = value
	answer, ok := ask("nerdr plugin stop", hooks.Plugin, p)
	if !ok {
		return 1
	}
	fmt.Println(answer)
	return 0
}
