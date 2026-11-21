package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/state"
	limits "github.com/nerdr-dev/nerdr/internal/usage"
)

// The commands an agent sees and steers the rest of nerdr with: the tab's
// agents and their state, what another pane shows, terminals the user
// sees, every tab, new tabs, past conversations and the accounts' usage.
// Each usage text is written for the agent, as sendUsage is.

const agentsUsage = `usage: nerdr agents

Lists the agents of this nerdr tab, as JSON: each one's number, kind,
model, state (starting, working, background, blocked, idle, done, exited), how full
its context window is, and which is you.
`

const waitUsage = `usage: nerdr wait [--timeout 10m] N [N…]

Waits until each of agents N of this tab has finished what it was doing,
or asks something (blocked), then prints their states as JSON. A task
waiting for an agent counts as work to do. Exits 0 when they are done, 3
when the timeout comes first.

An agent you started reports back with nerdr send anyway: wait when you
have nothing else to do until it has. Mind your own tool's time limit:
keep --timeout under it, or run nerdr wait in the background.
`

const readUsage = `usage: nerdr read [--lines 80] N | terminal [K]
       nerdr read terminal [K] --wait-for TEXT [--timeout 2m]

Prints what agent N of this tab shows, or the terminal pane named
(terminal, terminal 2: the newest without a number), as plain text: the
last lines of its screen and what scrolled off it.

--wait-for waits until TEXT, as it is (not a pattern), is among those
lines, then prints them: a dev server you started with nerdr terminal,
until it says it is ready. Exits 0 once TEXT is there, 3 when the timeout
comes first, printing what the pane shows then. Text already shown
counts, from before a --restart too: wait for what only the new run
prints. Mind your own tool's time limit: keep --timeout under it.
`

const terminalUsage = `usage: nerdr terminal COMMAND…
       nerdr terminal --restart [terminal [K]] [COMMAND…]

Runs COMMAND in a new terminal pane of this tab, in the worktree, where the
user sees it — a dev server, a watcher, a long test run — instead of out
of sight in your own shell. The pane is a shell, which stays when the
command ends. nerdr answers with its name: read what it shows with
nerdr read terminal K.

COMMAND in one argument is a command line for the shell, as it is:
nerdr terminal "npm install && npm run dev". In several, it is the words
of one command, each passed on as you gave it: nerdr terminal npm run dev.

--restart stops what runs in terminal K (the newest without one) — ctrl+c,
then harder if it does not stop — and runs the command you ran in it
again, or COMMAND: a server to restart after a change, in the same pane.

Close it with nerdr close terminal K, only when the user asks you to.
`

const closeUsage = `usage: nerdr close N | terminal [K]

Closes the pane of agent N of this tab, or terminal K (the newest without
one), and stops what runs in it. An agent closed keeps its conversation.
Not your own pane, nor the tab's first agent's, which stays as long as the
tab.

Only when the user asks you to close it. When you think a pane is no
longer needed and the user has not said, ask the user first.
`

const tabsUsage = `usage: nerdr tabs
       nerdr tabs [FOLDER]    (outside nerdr)

Lists every nerdr tab, as JSON: its name and folder, yours for your own,
its branches (commits ahead and behind main, lines and files changed,
pushed, pr, pr_state, ci), its status line, its agents with their state
(starting, working, background, blocked, idle, done, exited) and its terminals.

ci is pass, fail, pending, or empty when there is none. When a branch has
commits after its merged or closed pull request, commits_after_pr says how
many; they are not in any pull request yet.

Run outside nerdr — in your own terminal, or over ssh — it lists the tabs
of the nerdr running for FOLDER (this folder when not given), as its API's
state has them: each tab, agent and terminal with its id too, terminals as
objects (name, command, running), and no yours or you.
`

const tabUsage = `usage: nerdr tab new --branch NAME [--repo REPO] [--kind KIND] [--model MODEL] TASK
       (or - to read TASK from stdin)

Makes a new nerdr tab: branch NAME from main, in a worktree of its own, in
this tab's repositories or REPO, with an agent (KIND, MODEL as for nerdr
agent) given TASK as its first message. nerdr answers at once and tells
you when the tab is there; follow it with nerdr tabs.

Only when the user asks for new tabs or branches made without asking them
about each, as nerdr spinoff does.
`

const searchUsage = `usage: nerdr search [--limit 20] WORDS…
       nerdr search --show ID

Finds the claude and codex conversations had in this nerdr's repositories
and worktrees with every one of WORDS said in them, by the user or the
agent, the latest first. --show ID prints what was said in one.
`

const statusUsage = `usage: nerdr status [TEXT]    (or - to read TEXT from stdin)
       nerdr status --clear

Sets the line saying where this tab's work stands, in a sentence: what you
just did and what comes next. The user sees it above the agent, and other
agents in nerdr tabs. Without TEXT, prints the line as it is, to keep what
still holds when you change it. --clear takes it away.

Set it when the work changes phase: a cause found, a fix made, tests
running, waiting on the user or something outside.
`

const usageUsage = `usage: nerdr usage

Prints how much of the Claude and Codex plans' rate limits is used, in
percent, and when each window resets, as JSON.
`

// runAgents is `nerdr agents`.
func runAgents(args []string) int {
	if helpAsked(args) {
		fmt.Fprint(os.Stderr, agentsUsage)
		return 2
	}
	return query("nerdr agents", map[string]any{"what": hooks.QueryAgents})
}

// runTabs is `nerdr tabs`. In a tab it asks the nerdr running the tab,
// which shows an agent in a sandbox its own tab alone; it never turns to
// the API there, which would show it all. Outside nerdr it asks the API
// of the nerdr running for FOLDER.
func runTabs(args []string) int {
	if helpAsked(args) {
		fmt.Fprint(os.Stderr, tabsUsage)
		return 2
	}
	if inNerdr() {
		if len(args) > 0 {
			fmt.Fprintf(os.Stderr, "nerdr tabs: in a nerdr tab it takes no FOLDER: it lists the tabs of the nerdr running this one\n\n%s", tabsUsage)
			return 2
		}
		return query("nerdr tabs", map[string]any{"what": hooks.QueryTabs})
	}
	if len(args) > 1 || len(args) == 1 && strings.HasPrefix(args[0], "-") {
		fmt.Fprint(os.Stderr, tabsUsage)
		return 2
	}
	folder := "."
	if len(args) == 1 {
		folder = args[0]
	}
	tabs, err := apiTabs(folder)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr tabs:", err)
		return 1
	}
	b, _ := json.MarshalIndent(tabs, "", "  ")
	fmt.Println(string(b))
	return 0
}

// inNerdr is whether this runs in a nerdr tab, or in anything nerdr
// started: what nerdr gives its programs is there, all or some of it.
func inNerdr() bool {
	for _, v := range []string{"NERDR", "NERDR_TAB", "NERDR_SOCKET"} {
		if os.Getenv(v) != "" {
			return true
		}
	}
	return false
}

// apiTabs are the tabs of the nerdr running for folder, from its API.
func apiTabs(folder string) ([]core.APITab, error) {
	root, err := bridgeRoot(folder)
	if err != nil {
		return nil, err
	}
	c, err := dialAPI(root)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	_, st, err := askState(c)
	if err != nil {
		return nil, err
	}
	if st.Tabs == nil {
		st.Tabs = []core.APITab{}
	}
	return st.Tabs, nil
}

// runRead is `nerdr read`.
func runRead(args []string) int {
	fs := flag.NewFlagSet("read", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	lines := fs.Int("lines", 0, "")
	waitText := fs.String("wait-for", "", "")
	timeout := fs.Duration("timeout", 2*time.Minute, "")
	if helpAsked(args) || parseAnywhere(fs, args) != nil || fs.NArg() == 0 {
		fmt.Fprint(os.Stderr, readUsage)
		return 2
	}
	p := map[string]any{"what": hooks.QueryRead, "lines": *lines}
	if n, err := strconv.Atoi(strings.TrimPrefix(fs.Arg(0), "#")); err == nil && fs.NArg() == 1 {
		if n < 1 {
			fmt.Fprint(os.Stderr, readUsage)
			return 2
		}
		p["to"] = n
	} else {
		p["name"] = strings.Join(fs.Args(), " ")
	}
	if *waitText == "" {
		return query("nerdr read", p)
	}
	tab, sock, ok := tabSocket("nerdr read", "not running in a nerdr tab")
	if !ok {
		return 1
	}
	p["pane_id"] = tab
	read := func(deadline time.Time) (string, error) {
		answer, err := hooks.RequestBefore(sock, hooks.Query, p, deadline)
		return answer, explain(sock, err)
	}
	shown, found, err := readUntil(read, *waitText, *timeout, waitEvery)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr read:", err)
		return 1
	}
	if shown != "" {
		fmt.Println(shown)
	}
	if !found {
		fmt.Fprintf(os.Stderr, "nerdr read: timed out; %q is not shown yet\n", *waitText)
		return 3
	}
	return 0
}

// readUntil reads with read, every so often, until what it gives holds
// text or timeout has passed: what it gave last, and whether text was in
// it. Each read is given what is left of the timeout; one that runs out
// before it, at the socket's own shorter wait, is tried again. err is a
// read that failed otherwise.
func readUntil(read func(deadline time.Time) (string, error), text string, timeout, every time.Duration) (shown string, found bool, err error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		got, err := read(deadline)
		var ne net.Error
		switch {
		case errors.Is(err, os.ErrDeadlineExceeded) || errors.As(err, &ne) && ne.Timeout():
			continue
		case err != nil:
			return "", false, err
		}
		shown = got
		if strings.Contains(shown, text) {
			return shown, true, nil
		}
		time.Sleep(min(every, time.Until(deadline)))
	}
	return shown, false, nil
}

// query asks the nerdr running this tab what p asks, and prints its
// answer.
func query(name string, p map[string]any) int {
	answer, ok := ask(name, hooks.Query, p)
	if !ok {
		return 1
	}
	fmt.Println(answer)
	return 0
}

// ask makes request method with p of the nerdr running this tab, saying
// what went wrong, prefixed with name, when it did not.
func ask(name, method string, p map[string]any) (string, bool) {
	tab, sock, ok := tabSocket(name, "not running in a nerdr tab")
	if !ok {
		return "", false
	}
	p["pane_id"] = tab
	answer, err := request(sock, method, p)
	if err != nil {
		fmt.Fprintln(os.Stderr, name+":", err)
		return "", false
	}
	return answer, true
}

// parseAnywhere is fs.Parse with flags after the arguments as well as
// before them: agents write "nerdr read terminal --lines 80" as often as
// "nerdr read --lines 80 terminal", and the flag package stops at the
// first argument. Everything after "--" is an argument.
func parseAnywhere(fs *flag.FlagSet, args []string) error {
	var rest []string
	for {
		if err := fs.Parse(args); err != nil {
			return err
		}
		left := fs.Args()
		if len(left) == 0 {
			break
		}
		if used := len(args) - len(left); used > 0 && args[used-1] == "--" {
			rest = append(rest, left...)
			break
		}
		rest = append(rest, left[0])
		args = left[1:]
	}
	return fs.Parse(append([]string{"--"}, rest...))
}

// takeFlags takes fs's own flags out of args wherever they are, and
// gives back the rest, in order: the words of a message or a task, which
// agents write with the flags after them as often as before ("nerdr send
// 2 hello --wait"). A word that only looks like a flag, one fs does not
// have ("run it with --verbose"), stays a word of the text. Everything
// after "--" is text.
func takeFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return append(rest, args[i+1:]...), nil
		}
		name, ok := strings.CutPrefix(a, "-")
		if !ok || name == "" {
			rest = append(rest, a)
			continue
		}
		name = strings.TrimPrefix(name, "-")
		name, value, hasValue := strings.Cut(name, "=")
		f := fs.Lookup(name)
		if f == nil {
			rest = append(rest, a)
			continue
		}
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() && !hasValue {
			value, hasValue = "true", true
		}
		if !hasValue {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("flag needs an argument: -%s", name)
			}
			i++
			value = args[i]
		}
		if err := fs.Set(name, value); err != nil {
			return nil, fmt.Errorf("invalid value %q for flag -%s: %v", value, name, err)
		}
	}
	return rest, nil
}

func helpAsked(args []string) bool {
	return len(args) > 0 && (args[0] == "-h" || args[0] == "--help")
}

// waitEvery is how often nerdr wait asks again.
const waitEvery = 2 * time.Second

// runWait is `nerdr wait`.
func runWait(args []string) int {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	timeout := fs.Duration("timeout", 10*time.Minute, "")
	if helpAsked(args) || parseAnywhere(fs, args) != nil || fs.NArg() == 0 {
		fmt.Fprint(os.Stderr, waitUsage)
		return 2
	}
	var nums []int
	for _, a := range fs.Args() {
		n, err := strconv.Atoi(strings.TrimPrefix(a, "#"))
		if err != nil || n < 1 {
			fmt.Fprintf(os.Stderr, "nerdr wait: %q is not an agent's number\n\n%s", a, waitUsage)
			return 2
		}
		nums = append(nums, n)
	}
	deadline := time.Now().Add(*timeout)
	for {
		answer, ok := ask("nerdr wait", hooks.Query, map[string]any{"what": hooks.QueryAgents})
		if !ok {
			return 1
		}
		var all []waitAgent
		if err := json.Unmarshal([]byte(answer), &all); err != nil {
			fmt.Fprintln(os.Stderr, "nerdr wait: could not read nerdr's answer:", err)
			return 1
		}
		got, done, missing := waitFor(all, nums)
		if missing != 0 {
			fmt.Fprintf(os.Stderr, "nerdr wait: there is no agent %d in this tab\n", missing)
			return 1
		}
		if done || time.Now().After(deadline) {
			b, _ := json.MarshalIndent(got, "", "  ")
			fmt.Println(string(b))
			if !done {
				fmt.Fprintln(os.Stderr, "nerdr wait: timed out; they are still at work")
				return 3
			}
			return 0
		}
		time.Sleep(min(waitEvery, time.Until(deadline)+time.Millisecond))
	}
}

// waitAgent is an agent as nerdr agents gives it, as far as wait reads.
type waitAgent struct {
	Num     int    `json:"num"`
	Kind    string `json:"kind"`
	State   string `json:"state"`
	Waiting int    `json:"messages_waiting,omitempty"`
}

// waitFor is agents nums of all, whether every one has finished, and the
// first of nums not there, if any.
func waitFor(all []waitAgent, nums []int) (got []waitAgent, done bool, missing int) {
	done = true
	for _, n := range nums {
		found := false
		for _, a := range all {
			if a.Num != n {
				continue
			}
			found = true
			got = append(got, a)
			switch a.State {
			case "idle", "done", "blocked", "exited":
				if a.Waiting > 0 && a.State != "blocked" && a.State != "exited" {
					done = false
				}
			default:
				done = false
			}
		}
		if !found {
			return nil, false, n
		}
	}
	return got, done, 0
}

// runTerminal is `nerdr terminal`.
func runTerminal(args []string) int {
	if helpAsked(args) || len(args) == 0 {
		fmt.Fprint(os.Stderr, terminalUsage)
		return 2
	}
	p := map[string]any{}
	if args[0] == "--restart" || args[0] == "-restart" {
		name, rest := terminalArg(args[1:])
		p["restart"], p["name"] = true, name
		args = rest
	} else if len(args) > 0 && strings.HasPrefix(args[0], "-") {
		fmt.Fprint(os.Stderr, terminalUsage)
		return 2
	}
	p["text"] = shellCommand(args)
	answer, ok := ask("nerdr terminal", hooks.Terminal, p)
	if !ok {
		return 1
	}
	fmt.Println(answer)
	return 0
}

// shellCommand is the command line args make for the terminal's shell. One
// argument is a command line as it is: nerdr terminal "make && make test".
// Several are the words of one command, each quoted as needed so the
// shell gets them as the shell they were typed in passed them: nerdr
// terminal bash -c "make && x" runs bash -c 'make && x'.
func shellCommand(args []string) string {
	if len(args) == 1 {
		return args[0]
	}
	words := make([]string, len(args))
	for i, a := range args {
		words[i] = shellQuote(a)
	}
	return strings.Join(words, " ")
}

// shellQuote is s as one word for a POSIX shell: as it is when that is
// safe, else in single quotes.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./:=@%+,") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// terminalArg takes the name of a terminal off the front of args:
// "terminal", "terminal 2" or "2"; "terminal", the newest, when args do
// not start with one. The rest is a command.
func terminalArg(args []string) (string, []string) {
	if len(args) > 0 && args[0] == "terminal" {
		if len(args) > 1 {
			if n, err := strconv.Atoi(args[1]); err == nil && n > 0 {
				return "terminal " + args[1], args[2:]
			}
		}
		return "terminal", args[1:]
	}
	if len(args) > 0 {
		if n, err := strconv.Atoi(args[0]); err == nil && n > 0 {
			return "terminal " + args[0], args[1:]
		}
	}
	return "terminal", args
}

// runClose is `nerdr close`.
func runClose(args []string) int {
	if helpAsked(args) || len(args) == 0 {
		fmt.Fprint(os.Stderr, closeUsage)
		return 2
	}
	p := map[string]any{}
	if n, err := strconv.Atoi(strings.TrimPrefix(args[0], "#")); err == nil && len(args) == 1 {
		if n < 1 {
			fmt.Fprint(os.Stderr, closeUsage)
			return 2
		}
		p["to"] = n
	} else {
		name, rest := terminalArg(args)
		if args[0] != "terminal" || len(rest) > 0 {
			fmt.Fprintf(os.Stderr, "nerdr close: %q is neither an agent's number nor a terminal\n\n%s", strings.Join(args, " "), closeUsage)
			return 2
		}
		p["name"] = name
	}
	answer, ok := ask("nerdr close", hooks.Close, p)
	if !ok {
		return 1
	}
	fmt.Println(answer)
	return 0
}

// runStatus is `nerdr status`.
func runStatus(args []string) int {
	if helpAsked(args) {
		fmt.Fprint(os.Stderr, statusUsage)
		return 2
	}
	p := map[string]any{}
	switch {
	case len(args) == 1 && args[0] == "--clear":
		p["clear"] = true
	case len(args) == 1 && args[0] == "-":
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "nerdr status:", err)
			return 1
		}
		p["text"] = strings.TrimSpace(string(data))
	default:
		p["text"] = strings.TrimSpace(strings.Join(args, " "))
	}
	if t, _ := p["text"].(string); len(t) > hooks.MaxStatus {
		fmt.Fprintf(os.Stderr, "nerdr status: the status is %d bytes; keep it under %d, a line saying where the work stands\n", len(t), hooks.MaxStatus)
		return 2
	}
	answer, ok := ask("nerdr status", hooks.Status, p)
	if !ok {
		return 1
	}
	fmt.Println(answer)
	return 0
}

// runTab is `nerdr tab new`.
func runTab(args []string) int {
	if len(args) == 0 || args[0] != "new" {
		fmt.Fprint(os.Stderr, tabUsage)
		return 2
	}
	fs := flag.NewFlagSet("tab new", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	branch := fs.String("branch", "", "")
	repo := fs.String("repo", "", "")
	kind := fs.String("kind", "", "")
	model := fs.String("model", "", "")
	words, err := takeFlags(fs, args[1:])
	if helpAsked(args[1:]) || err != nil || *branch == "" {
		fmt.Fprint(os.Stderr, tabUsage)
		return 2
	}
	text := strings.Join(words, " ")
	if len(words) == 1 && words[0] == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "nerdr tab new:", err)
			return 1
		}
		text = string(data)
	}
	if strings.TrimSpace(text) == "" {
		fmt.Fprintf(os.Stderr, "nerdr tab new: no task for the new tab's agent\n\n%s", tabUsage)
		return 2
	}
	if len(text) > hooks.MaxText {
		fmt.Fprintf(os.Stderr, "nerdr tab new: the task is %d bytes; keep it under %d\n", len(text), hooks.MaxText)
		return 2
	}
	answer, ok := ask("nerdr tab new", hooks.NewTab, map[string]any{
		"branch": *branch, "repo": *repo, "kind": *kind, "model": *model, "text": text,
	})
	if !ok {
		return 1
	}
	fmt.Println(answer)
	return 0
}

// runSearch is `nerdr search`. It reads the conversations itself; only
// where to look comes from the nerdr running the tab.
func runSearch(args []string) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", 20, "")
	show := fs.String("show", "", "")
	words, err := takeFlags(fs, args)
	if helpAsked(args) || err != nil || len(words) == 0 && *show == "" {
		fmt.Fprint(os.Stderr, searchUsage)
		return 2
	}
	answer, ok := ask("nerdr search", hooks.Query, map[string]any{"what": hooks.QueryRoots})
	if !ok {
		return 1
	}
	var x agents.Index
	x.Update(strings.Split(answer, "\n"))
	if *show != "" {
		c := x.Conversation(*show)
		if c == nil {
			fmt.Fprintf(os.Stderr, "nerdr search: no conversation %s here\n", *show)
			return 1
		}
		fmt.Printf("%s conversation %s in %s, last written %s\n\n", c.Kind, c.ID, c.Dir, c.Modified.Format("2006-01-02 15:04"))
		for _, s := range c.Said {
			fmt.Printf("## %s\n\n%s\n\n", s.Who, s.Text)
		}
		return 0
	}
	search := agents.Words(strings.Join(words, " "))
	hits := x.Search(search, max(*limit, 1))
	if len(hits) == 0 {
		fmt.Println("No conversation has all of these words.")
		return 0
	}
	for _, h := range hits {
		c := h.Conv
		fmt.Printf("%s  %s  %s  %s  (%d)\n    %s: %s\n", c.Modified.Format("2006-01-02 15:04"), c.Kind, c.Dir, c.ID,
			h.Count, c.Said[h.At].Who, h.Snippet(search, 160))
	}
	fmt.Println("\nnerdr search --show ID prints a conversation.")
	return 0
}

// runUsage is `nerdr usage`. It reads what the agents said itself.
func runUsage(args []string) int {
	if helpAsked(args) {
		fmt.Fprint(os.Stderr, usageUsage)
		return 2
	}
	type window struct {
		Used     float64 `json:"used_percent"`
		ResetsAt string  `json:"resets_at,omitempty"`
		ResetsIn string  `json:"resets_in,omitempty"`
	}
	type account struct {
		Session *window `json:"five_hours,omitempty"`
		Weekly  *window `json:"week,omitempty"`
		Plan    string  `json:"plan,omitempty"`
		Seen    string  `json:"seen,omitempty"`
		Note    string  `json:"note,omitempty"`
	}
	now := time.Now()
	win := func(w *limits.Window) *window {
		if w == nil {
			return nil
		}
		out := &window{Used: math.Round(w.Used*10) / 10}
		if r := w.Resets(); !r.IsZero() {
			if !r.After(now) {
				out.Used = 0
			}
			out.ResetsAt = r.Format(time.RFC3339)
			out.ResetsIn = max(time.Until(r), 0).Round(time.Minute).String()
		}
		return out
	}
	out := map[string]account{}
	if s, ok := limits.LoadClaude(state.Dir()); ok {
		out["claude"] = account{Session: win(s.Session), Weekly: win(s.Weekly), Seen: s.At.Format(time.RFC3339)}
	} else {
		out["claude"] = account{Note: "not known: a claude agent in nerdr tells it after its first answer"}
	}
	if r, at, ok := limits.Codex(agents.CodexHome()); ok {
		out["codex"] = account{Session: win(r.Session), Weekly: win(r.Weekly), Plan: r.Plan, Seen: at.Format(time.RFC3339)}
	} else {
		out["codex"] = account{Note: "not known: no codex log has it"}
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
	return 0
}
