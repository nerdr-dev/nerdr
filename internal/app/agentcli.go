package app

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
)

// What an agent can ask nerdr of the rest of nerdr, beyond its own work:
// the tab's agents and their state (`nerdr agents`, and `nerdr wait`,
// which asks again until they are done), what another pane of the tab
// shows (`nerdr read`), every tab (`nerdr tabs`), where conversations are
// searched (`nerdr search`); and what it can have nerdr do: run a command
// in a terminal pane the user sees (`nerdr terminal`), or again in one
// (`nerdr terminal --restart`), close a pane when the user asked for it
// (`nerdr close`), and make a tab on a new branch with an agent given a
// task (`nerdr tab new`).

// readLines is how much of a pane `nerdr read` gives without --lines.
const readLines = 80

// stateName is a status as the agent CLI says it.
func stateName(s status) string {
	switch s {
	case stStarting:
		return "starting"
	case stIdle:
		return "idle"
	case stWorking:
		return "working"
	case stBlocked:
		return "blocked"
	case stDone:
		return "done"
	case stExited:
		return "exited"
	case stAsleep:
		return "asleep"
	}
	return "unknown"
}

// agentJSON is one agent as `nerdr agents` and `nerdr tabs` give it.
type agentJSON struct {
	Num     int      `json:"num"`
	Kind    string   `json:"kind"`
	Model   string   `json:"model,omitempty"`
	State   string   `json:"state"`
	You     bool     `json:"you,omitempty"`
	Review  bool     `json:"reviewer,omitempty"`
	Context *float64 `json:"context_percent,omitempty"`
	Waiting int      `json:"messages_waiting,omitempty"`
}

func (m *Model) agentJSON(a, from *tab) agentJSON {
	j := agentJSON{Num: a.Num, Kind: string(a.Agent), Model: a.Model, State: stateName(a.status),
		You: a == from, Review: a.reviewOf != nil, Waiting: len(a.Messages)}
	if j.Num == 0 {
		j.Num = 1
	}
	if a.usage != nil && a.usage.Context != nil {
		c := math.Round(*a.usage.Context)
		j.Context = &c
	}
	return j
}

// onQuery answers what an agent asks of nerdr.
func (m *Model) onQuery(r hooks.Report) tea.Cmd {
	reply := answerer(r)
	from := m.agentByHook(r.Pane)
	if from == nil {
		reply(hooks.Answer{Err: "this agent is not in a nerdr tab nerdr knows"})
		return nil
	}
	o := from.owner()
	confined := m.confined(r, from)
	switch r.What {
	case hooks.QueryAgents:
		var out []agentJSON
		for _, a := range append([]*tab{o}, o.extraAgents()...) {
			out = append(out, m.agentJSON(a, from))
		}
		reply(jsonAnswer(out))
	case hooks.QueryTabs:
		tabs := m.tabsJSON(o)
		if confined {
			// The others are outside its sandbox.
			tabs = slices.DeleteFunc(tabs, func(j tabJSON) bool { return !j.Yours })
		}
		reply(jsonAnswer(tabs))
	case hooks.QueryRoots:
		roots := m.searchRoots()
		if confined {
			roots = []string{o.Dir}
			for _, w := range o.Worktrees {
				roots = append(roots, w.Path)
			}
		}
		reply(hooks.Answer{Text: strings.Join(roots, "\n")})
	case hooks.QueryRead:
		reply(m.readPane(o, from, r, confined))
	case hooks.QueryReply:
		reply(m.takeReply(o, from, r.To))
	default:
		reply(hooks.Answer{Err: "nerdr cannot tell " + r.What})
	}
	return nil
}

// answerer is how to answer report r, if it waits for an answer.
func answerer(r hooks.Report) func(hooks.Answer) {
	return func(a hooks.Answer) {
		if r.Reply != nil {
			select {
			case r.Reply <- a:
			default:
			}
		}
	}
}

func jsonAnswer(v any) hooks.Answer {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return hooks.Answer{Err: err.Error()}
	}
	return hooks.Answer{Text: string(b)}
}

// readPane is what a pane of t shows, for `nerdr read`: agent r.To's, or
// the terminal r.Name names ("terminal", "terminal 2"). A confined agent
// reads only agents in a sandbox.
func (m *Model) readPane(t, from *tab, r hooks.Report, confined bool) hooks.Answer {
	lines := r.Lines
	if lines <= 0 {
		lines = readLines
	}
	if r.To > 0 {
		a := t.agentNumbered(r.To)
		if a == nil {
			return hooks.Answer{Err: fmt.Sprintf("there is no agent %d in this tab; its agents are %s", r.To, m.agentList(t, from))}
		}
		if a == from {
			return hooks.Answer{Err: fmt.Sprintf("agent %d is you", r.To)}
		}
		if confined && !m.inSandbox(a) {
			return hooks.Answer{Err: fmt.Sprintf("you run in a sandbox, and agent %d does not: you cannot read it", r.To)}
		}
		if a.pane == nil {
			return hooks.Answer{Err: fmt.Sprintf("agent %d is not running: %s", r.To, stateName(a.status))}
		}
		return hooks.Answer{Text: a.pane.Text(lines)}
	}
	if confined {
		return hooks.Answer{Err: "you run in a sandbox, and the terminals do not: you cannot read them"}
	}
	p, err := findTerminal(t, r.Name)
	if err != "" {
		return hooks.Answer{Err: err}
	}
	return hooks.Answer{Text: p.shell.Text(lines)}
}

// findTerminal is the terminal pane of t that name names ("terminal",
// the newest; "terminal 2"; "2"), or why there is none.
func findTerminal(t *tab, name string) (*paneTab, string) {
	name = strings.TrimSpace(name)
	n := 0
	if v, err := strconv.Atoi(name); err == nil && v > 0 {
		name = "terminal " + name
	}
	if rest, ok := strings.CutPrefix(name, "terminal"); ok {
		rest = strings.TrimSpace(rest)
		if rest == "" {
			n = -1 // the newest
		} else if v, err := strconv.Atoi(rest); err == nil && v > 0 {
			n = v
		}
	}
	var shells []*paneTab
	for _, p := range t.panes() {
		if p.kind == paneShell && p.shell != nil {
			shells = append(shells, p)
		}
	}
	names := func() string {
		if len(shells) == 0 {
			return "the tab has no terminal open"
		}
		var out []string
		for _, p := range shells {
			out = append(out, terminalName(p))
		}
		return "its terminals are " + strings.Join(out, ", ")
	}
	if n == 0 {
		return nil, fmt.Sprintf("%q is neither an agent's number nor a terminal; %s", name, names())
	}
	for i := len(shells) - 1; i >= 0; i-- {
		if p := shells[i]; n == -1 || p.n == n {
			return p, ""
		}
	}
	return nil, fmt.Sprintf("there is no %s in this tab; %s", name, names())
}

// takeReply takes the message agent n sent from out of from's list, for
// `nerdr send --wait`, which asks until there is one: its text, or none
// while there is none yet. Left there, it is typed in when from is next
// idle, as any other.
func (m *Model) takeReply(t, from *tab, n int) hooks.Answer {
	for i, msg := range from.Messages {
		// One an agent in a sandbox wrote is not handed to one outside.
		if msg.Auto && msg.From == n && (!msg.Sandboxed || from.sandboxed) {
			from.dropMessage(i)
			m.saveSession()
			m.resizePanes()
			return hooks.Answer{Text: msg.Text}
		}
	}
	if a := t.agentNumbered(n); a == nil || a == from {
		return hooks.Answer{Err: fmt.Sprintf("there is no other agent %d in this tab to wait for; its agents are %s", n, m.agentList(t, from))}
	} else if a.pane == nil {
		return hooks.Answer{Err: fmt.Sprintf("agent %d is not running: %s", n, stateName(a.status))}
	}
	return hooks.Answer{}
}

// onClose closes the pane of another agent of the tab, or a terminal, for
// the agent asking (`nerdr close`): the user asked it to. Not the tab's
// first agent, which stays as long as the tab, nor the asking agent's
// own.
func (m *Model) onClose(r hooks.Report) tea.Cmd {
	reply := answerer(r)
	from := m.agentByHook(r.Pane)
	if from == nil {
		reply(hooks.Answer{Err: "this agent is not in a nerdr tab nerdr knows"})
		return nil
	}
	if from.reviewOf != nil {
		reply(hooks.Answer{Err: "a reviewer does not close panes"})
		return nil
	}
	confined := m.confined(r, from)
	if confined && r.To == 0 {
		reply(hooks.Answer{Err: "you run in a sandbox, and the terminals do not: you cannot close them"})
		return nil
	}
	o := from.owner()
	var p *paneTab
	if r.To > 0 {
		a := o.agentNumbered(r.To)
		switch {
		case a == nil:
			reply(hooks.Answer{Err: fmt.Sprintf("there is no agent %d in this tab; its agents are %s", r.To, m.agentList(o, from))})
			return nil
		case confined && !m.inSandbox(a):
			reply(hooks.Answer{Err: fmt.Sprintf("you run in a sandbox, and agent %d does not: you cannot close it", r.To)})
			return nil
		case a == from:
			reply(hooks.Answer{Err: fmt.Sprintf("agent %d is you: the user closes your pane", r.To)})
			return nil
		case a == o:
			reply(hooks.Answer{Err: fmt.Sprintf("agent %d is the tab's first agent: it stays as long as the tab", r.To)})
			return nil
		}
		for _, q := range o.panes() {
			if q.a == a {
				p = q
			}
		}
		if p == nil {
			reply(hooks.Answer{Err: fmt.Sprintf("agent %d has no pane to close", r.To)})
			return nil
		}
	} else {
		var err string
		if p, err = findTerminal(o, r.Name); err != "" {
			reply(hooks.Answer{Err: err})
			return nil
		}
	}
	name := m.paneName(o, p)
	if p.kind == paneShell {
		name = terminalName(p)
	}
	m.toast(o.Name + ": " + m.agentLabel(o, from) + " closed " + name)
	reply(hooks.Answer{Text: "Closed " + name + "."})
	return m.closePane(o, p)
}

// terminalName is what `nerdr read` calls terminal pane p.
func terminalName(p *paneTab) string {
	if p.n > 1 {
		return "terminal " + strconv.Itoa(p.n)
	}
	return "terminal"
}

// tabJSON is one tab as `nerdr tabs` gives it.
type tabJSON struct {
	Name      string       `json:"name"`
	Dir       string       `json:"dir"`
	Yours     bool         `json:"yours,omitempty"`
	Group     string       `json:"group,omitempty"`
	Branches  []branchJSON `json:"branches,omitempty"`
	Agents    []agentJSON  `json:"agents"`
	Goal      string       `json:"task,omitempty"`
	Status    string       `json:"status,omitempty"`
	Terminals []string     `json:"terminals,omitempty"`
}

type branchJSON struct {
	Repo    string `json:"repo"`
	Branch  string `json:"branch"`
	Ahead   int    `json:"ahead"`
	Behind  int    `json:"behind"`
	Added   int    `json:"lines_added"`
	Deleted int    `json:"lines_deleted"`
	Changed int    `json:"files_changed"`
	Pushed  bool   `json:"pushed"`
	PR      int    `json:"pr,omitempty"`
	PRState string `json:"pr_state,omitempty"`
	CI      string `json:"ci,omitempty"`
	URL     string `json:"pr_url,omitempty"`
}

// onStatus sets, clears or tells the line an agent keeps on where its
// tab's work stands (`nerdr status`), shown above the agent.
func (m *Model) onStatus(r hooks.Report) {
	reply := answerer(r)
	from := m.agentByHook(r.Pane)
	if from == nil {
		reply(hooks.Answer{Err: "this agent is not in a nerdr tab nerdr knows"})
		return
	}
	o := from.owner()
	switch {
	case r.Clear:
		o.Status, o.StatusBy, o.StatusAt = "", 0, time.Time{}
		m.saveSession()
		reply(hooks.Answer{Text: "The status is cleared."})
		return
	case r.Text != "":
		o.Status, o.StatusBy, o.StatusAt = r.Text, 0, time.Now()
		if from != o {
			o.StatusBy = from.Num
		}
		m.saveSession()
		reply(hooks.Answer{Text: "The status is set; the user sees it above the agent."})
		return
	}
	if o.Status == "" {
		reply(hooks.Answer{Text: "No status is set."})
		return
	}
	by := ""
	if o.StatusBy > 0 {
		by = fmt.Sprintf(", by agent %d", o.StatusBy)
	}
	reply(hooks.Answer{Text: fmt.Sprintf("%s\n(set %s%s)", o.Status, ago(time.Since(o.StatusAt)), by)})
}

// tabsJSON is every tab, yours being the one asking.
func (m *Model) tabsJSON(yours *tab) []tabJSON {
	var out []tabJSON
	for _, t := range m.tabs {
		j := tabJSON{Name: t.Name, Dir: t.Dir, Yours: t == yours, Group: t.Group, Goal: t.Goal, Status: t.Status}
		for _, a := range t.agentsIn() {
			j.Agents = append(j.Agents, m.agentJSON(a, nil))
		}
		for _, w := range t.info.wts {
			b := branchJSON{Repo: filepath.Base(w.repo), Branch: w.branch, Ahead: w.stats.Ahead, Behind: w.stats.Behind,
				Added: w.add, Deleted: w.del, Changed: len(w.stats.Changed), Pushed: w.unpushed == 0}
			if w.hasPR {
				b.PR, b.PRState, b.CI, b.URL = w.pr.Number, w.pr.State, w.pr.CI, w.pr.URL
			}
			j.Branches = append(j.Branches, b)
		}
		if len(j.Branches) == 0 {
			for _, w := range t.Worktrees {
				j.Branches = append(j.Branches, branchJSON{Repo: filepath.Base(w.Repo), Branch: w.Branch})
			}
		}
		for _, p := range t.panes() {
			if p.kind == paneShell {
				j.Terminals = append(j.Terminals, terminalName(p))
			}
		}
		out = append(out, j)
	}
	return out
}

// onTerminal runs the command an agent asked for (`nerdr terminal`) in a
// new terminal pane of its tab: a shell, which stays when the command
// ends, for the user to see and use. It does not take the keyboard.
func (m *Model) onTerminal(r hooks.Report) tea.Cmd {
	reply := answerer(r)
	from := m.agentByHook(r.Pane)
	if from == nil {
		reply(hooks.Answer{Err: "this agent is not in a nerdr tab nerdr knows"})
		return nil
	}
	if from.reviewOf != nil {
		reply(hooks.Answer{Err: "a reviewer does not run terminals"})
		return nil
	}
	if m.confined(r, from) {
		reply(hooks.Answer{Err: noTerminal})
		return nil
	}
	o := from.owner()
	if r.Restart {
		return m.restartTerminal(o, from, r)
	}
	n := 0
	for _, p := range o.panes() {
		if p.kind == paneShell {
			n++
		}
	}
	if n >= maxAgents {
		reply(hooks.Answer{Err: fmt.Sprintf("the tab has %d terminals open; ask the user to close one", n)})
		return nil
	}
	p, cmd := m.newShellIn(o, true)
	if p == nil {
		reply(hooks.Answer{Err: "the terminal did not start"})
		return cmd
	}
	p.shell.Input([]byte(r.Text + "\r"))
	p.command = r.Text
	name := terminalName(p)
	m.toast(o.Name + ": " + m.agentLabel(o, from) + " runs " + truncName(r.Text, 40) + " in " + name)
	reply(hooks.Answer{Text: fmt.Sprintf("Running in %s of this tab, which the user sees. "+
		"Read what it shows with nerdr read %s; it stays open when the command ends.", name, strconv.Quote(name))})
	return cmd
}

// restartTerminal stops what runs in the terminal r names, and runs its
// command again in it, or the one r gives (`nerdr terminal --restart`).
// nerdr answers at once; the shell is waited for out of the screen's way.
func (m *Model) restartTerminal(o, from *tab, r hooks.Report) tea.Cmd {
	reply := answerer(r)
	name := r.Name
	if name == "" {
		name = "terminal"
	}
	p, err := findTerminal(o, name)
	if err != "" {
		reply(hooks.Answer{Err: err})
		return nil
	}
	command := r.Text
	if command == "" {
		command = p.command
	}
	if command == "" {
		reply(hooks.Answer{Err: fmt.Sprintf("nerdr ran no command in %s to run again; give the command after its name", terminalName(p))})
		return nil
	}
	p.command = command
	name = terminalName(p)
	m.toast(o.Name + ": " + m.agentLabel(o, from) + " restarts " + truncName(command, 40) + " in " + name)
	reply(hooks.Answer{Text: fmt.Sprintf("Stopping what runs in %s of this tab and running %s in it again. "+
		"Read what it shows with nerdr read %s.", name, strconv.Quote(command), strconv.Quote(name))})
	sh := p.shell
	return func() tea.Msg {
		if !sh.Rerun(command) {
			return toastMsg(name + ": what ran did not stop; " + command + " was not run again")
		}
		return nil
	}
}

// tabMadeMsg is the result of making the tab an agent asked for.
type tabMadeMsg struct {
	from     *tab
	branch   string
	kind     agents.Kind
	model    string
	task     string
	confined bool
	plan     workspace.Plan
	wts      []workspace.Worktree
	warnings []string
	err      error
}

// onNewTab makes the tab an agent asked for (`nerdr tab new`): a branch
// from main in the asking tab's repositories, or the one named, and its
// agent given the task. nerdr answers at once; the agent is told when the
// tab is there, or why it is not.
func (m *Model) onNewTab(r hooks.Report) tea.Cmd {
	reply := answerer(r)
	from := m.agentByHook(r.Pane)
	if from == nil {
		reply(hooks.Answer{Err: "this agent is not in a nerdr tab nerdr knows"})
		return nil
	}
	if from.reviewOf != nil {
		reply(hooks.Answer{Err: "a reviewer does not make tabs"})
		return nil
	}
	if m.tabLimitReached() {
		reply(hooks.Answer{Err: "the tab limit (max_tabs) is reached: ask the user to close a tab"})
		return nil
	}
	if err := workspace.ValidBranch(r.Branch); err != nil {
		reply(hooks.Answer{Err: err.Error()})
		return nil
	}
	k := from.owner().Agent
	if r.Kind != "" {
		k = agents.Kind(strings.ToLower(r.Kind))
	}
	if !slices.Contains(agents.Available(m.cfg.Agents), k) {
		reply(hooks.Answer{Err: fmt.Sprintf("there is no agent %q here; those there are: %s", k, m.kindList())})
		return nil
	}
	confined := m.confined(r, from)
	if why := m.startRefusal(k); why != "" && confined {
		reply(hooks.Answer{Err: why})
		return nil
	}
	if r.Model != "" && !agents.TakesModel(k) {
		reply(hooks.Answer{Err: fmt.Sprintf("nerdr does not know how to give %s a model", k)})
		return nil
	}
	repos, err := m.tabRepos(from.owner(), r.Repo, confined)
	if err != nil {
		reply(hooks.Answer{Err: err.Error()})
		return nil
	}
	var paths []string
	for _, rp := range repos {
		paths = append(paths, rp.Path)
	}
	task := fmt.Sprintf("Task from agent %d (%s) in the nerdr tab %s, which made this tab for you, on a branch of its own:\n\n%s",
		max(from.Num, 1), from.Agent, from.owner().Name, r.Text)
	reply(hooks.Answer{Text: fmt.Sprintf("nerdr is making the tab %s, with %s; you get a message when it is there, or why it is not. "+
		"Follow it with nerdr tabs.", r.Branch, k)})
	branch, model, wtRoot, allowed := r.Branch, r.Model, m.wtRoot, setupAllowedFile()
	return m.askRepoSetup(paths, func(m *Model) tea.Cmd {
		return func() tea.Msg {
			res := tabMadeMsg{from: from, branch: branch, kind: k, model: model, task: task, confined: confined}
			plan, err := workspace.MakePlan(wtRoot, branch, repos)
			if err == nil {
				for _, it := range plan.Items {
					if it.Mode == "local" || it.Mode == "remote" {
						err = fmt.Errorf("%s: a branch %s is there already; pick another name", it.Repo.Name, branch)
					}
				}
			}
			if err == nil {
				res.warnings, err = plan.Sync()
			}
			if err == nil {
				res.wts, err = plan.Execute()
			}
			if err != nil {
				res.err = err
				return res
			}
			res.plan = plan
			res.warnings = append(res.warnings, workspace.Prepare(res.wts, plan.SetupIn(), allowed, func(string) {})...)
			return res
		}
	})
}

// tabMade opens the tab made for an agent, and tells the agent.
func (m *Model) tabMade(msg tabMadeMsg) tea.Cmd {
	tell := func(text string) {
		if m.live(msg.from) || m.live(msg.from.owner()) {
			m.addMessageFrom(msg.from, text, true, msg.confined)
		}
	}
	if msg.err != nil {
		m.toast("tab " + msg.branch + ": " + msg.err.Error())
		tell("nerdr could not make the tab you asked for: " + msg.err.Error())
		return nil
	}
	if msg.confined && !m.sandboxedKind(msg.kind) {
		why := string(msg.kind) + " is no longer set sandboxed; the worktree is made at " + msg.plan.Dir + ", with no agent in it"
		m.toast("tab " + msg.branch + ": " + why)
		tell("nerdr could not start the tab you asked for: " + why)
		return nil
	}
	group := ""
	if m.live(msg.from.owner()) {
		group = msg.from.owner().Group
	}
	t, cmd := m.addTabWith(msg.plan.Branch, msg.plan.Dir, group, msg.wts, msg.kind, msg.model, msg.task, msg.confined)
	m.resizePanes()
	note := "made " + t.Name + " for " + m.agentName(msg.from)
	if len(msg.warnings) > 0 {
		note += "; " + strings.Join(msg.warnings, "; ")
	}
	m.toast(note)
	tell(fmt.Sprintf("nerdr made the tab %s (%s), in %s, its agent %s given your task. "+
		"See how it gets on with nerdr tabs; the user reviews and merges its branch.", t.Name, msg.plan.Branch, t.Dir, t.Agent))
	return cmd
}

// tabRepos are the repositories a tab an agent asks for is made in: the
// asking tab's, or the one named, among those of t and, outside a
// sandbox, those nerdr finds.
func (m *Model) tabRepos(t *tab, name string, confined bool) ([]workspace.Repo, error) {
	var repos []workspace.Repo
	for _, w := range t.Worktrees {
		repos = append(repos, workspace.Repo{Name: filepath.Base(w.Repo), Path: w.Repo})
	}
	if name == "" {
		if len(repos) == 0 {
			return nil, fmt.Errorf("this tab has no repository to make a branch in; name one with --repo")
		}
		return repos, nil
	}
	for _, r := range repos {
		if r.Name == name {
			return []workspace.Repo{r}, nil
		}
	}
	if confined {
		var names []string
		for _, r := range repos {
			names = append(names, r.Name)
		}
		return nil, fmt.Errorf("you run in a sandbox, and %q is not in this tab's repositories (%s)", name, strings.Join(names, ", "))
	}
	all, _ := workspace.ScanAll(m.root, m.cfg.WorktreesDir, m.cfg.RepoDirs)
	var names []string
	for _, r := range all {
		if r.Name == name {
			return []workspace.Repo{r}, nil
		}
		names = append(names, r.Name)
	}
	return nil, fmt.Errorf("there is no repository %q; those there are: %s", name, strings.Join(names, ", "))
}

// kindList is the agents on PATH, as words.
func (m *Model) kindList() string {
	var names []string
	for _, k := range agents.Available(m.cfg.Agents) {
		names = append(names, string(k))
	}
	return strings.Join(names, ", ")
}
