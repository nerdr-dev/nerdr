package core

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
)

// ReadLines is how much of a pane `nerdr read` gives without --lines.
const ReadLines = 80

// StateName is a status as the agent CLI says it.
func StateName(s Status) string {
	switch s {
	case StStarting:
		return "starting"
	case StIdle:
		return "idle"
	case StWorking:
		return "working"
	case StBackground:
		return "background"
	case StBlocked:
		return "blocked"
	case StDone:
		return "done"
	case StExited:
		return "exited"
	case StAsleep:
		return "asleep"
	}
	return "unknown"
}

// AgentJSON is one agent as `nerdr agents` and `nerdr tabs` give it.
type AgentJSON struct {
	Num     int      `json:"num"`
	Kind    string   `json:"kind"`
	Model   string   `json:"model,omitempty"`
	State   string   `json:"state"`
	You     bool     `json:"you,omitempty"`
	Review  bool     `json:"reviewer,omitempty"`
	Context *float64 `json:"context_percent,omitempty"`
	Waiting int      `json:"messages_waiting,omitempty"`
}

func (m *Core) AgentJSON(a, from *Tab) AgentJSON {
	j := AgentJSON{Num: a.Num, Kind: string(a.Agent), Model: a.Model, State: StateName(a.Light),
		You: a == from, Review: a.ReviewOf != nil, Waiting: len(a.Messages)}
	if j.Num == 0 {
		j.Num = 1
	}
	if a.Usage != nil && a.Usage.Context != nil {
		c := math.Round(*a.Usage.Context)
		j.Context = &c
	}
	return j
}

// Answerer is how to answer report r, if it waits for an answer.
func Answerer(r hooks.Report) func(hooks.Answer) {
	return func(a hooks.Answer) {
		if r.Reply != nil {
			select {
			case r.Reply <- a:
			default:
			}
		}
	}
}

func JsonAnswer(v any) hooks.Answer {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return hooks.Answer{Err: err.Error()}
	}
	return hooks.Answer{Text: string(b)}
}

// ReadPane is what a pane of t shows, for `nerdr read`: agent r.To's, or
// the terminal r.Name names ("terminal", "terminal 2"). A confined agent
// reads only agents in a sandbox.
func (m *Core) ReadPane(t, from *Tab, r hooks.Report, confined bool) hooks.Answer {
	lines := r.Lines
	if lines <= 0 {
		lines = ReadLines
	}
	if r.To > 0 {
		a := t.AgentNumbered(r.To)
		if a == nil {
			return hooks.Answer{Err: fmt.Sprintf("there is no agent %d in this tab; its agents are %s", r.To, m.AgentList(t, from))}
		}
		if a == from {
			return hooks.Answer{Err: fmt.Sprintf("agent %d is you", r.To)}
		}
		if confined && !m.InSandbox(a) {
			return hooks.Answer{Err: fmt.Sprintf("you run in a sandbox, and agent %d does not: you cannot read it", r.To)}
		}
		if a.Pane == nil {
			return hooks.Answer{Err: fmt.Sprintf("agent %d is not running: %s", r.To, StateName(a.Light))}
		}
		return hooks.Answer{Text: a.Pane.Text(lines)}
	}
	if confined {
		return hooks.Answer{Err: "you run in a sandbox, and the terminals do not: you cannot read them"}
	}
	p, err := FindTerminal(t, r.Name)
	if err != "" {
		return hooks.Answer{Err: err}
	}
	return hooks.Answer{Text: p.Shell.Text(lines)}
}

// FindTerminal is the terminal pane of t that name names ("terminal",
// the newest; "terminal 2"; "2"), or why there is none.
func FindTerminal(t *Tab, name string) (*PaneTab, string) {
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
	var shells []*PaneTab
	for _, p := range t.Panes() {
		if p.Kind == PaneShell && p.Shell != nil {
			shells = append(shells, p)
		}
	}
	names := func() string {
		if len(shells) == 0 {
			return "the tab has no terminal open"
		}
		var out []string
		for _, p := range shells {
			out = append(out, TerminalName(p))
		}
		return "its terminals are " + strings.Join(out, ", ")
	}
	if n == 0 {
		return nil, fmt.Sprintf("%q is neither an agent's number nor a terminal; %s", name, names())
	}
	for i := len(shells) - 1; i >= 0; i-- {
		if p := shells[i]; n == -1 || p.N == n {
			return p, ""
		}
	}
	return nil, fmt.Sprintf("there is no %s in this tab; %s", name, names())
}

// TerminalName is what `nerdr read` calls terminal pane p.
func TerminalName(p *PaneTab) string {
	if p.N > 1 {
		return "terminal " + strconv.Itoa(p.N)
	}
	return "terminal"
}

// TabJSON is one tab as `nerdr tabs` gives it.
type TabJSON struct {
	Name      string       `json:"name"`
	Dir       string       `json:"dir"`
	Yours     bool         `json:"yours,omitempty"`
	Group     string       `json:"group,omitempty"`
	Branches  []BranchJSON `json:"branches,omitempty"`
	Agents    []AgentJSON  `json:"agents"`
	Goal      string       `json:"task,omitempty"`
	Status    string       `json:"status,omitempty"`
	Terminals []string     `json:"terminals,omitempty"`
}

type BranchJSON struct {
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
	// After is how many commits the branch has on top of its merged or
	// closed PR: work that went on after it, in no PR.
	After int `json:"commits_after_pr,omitempty"`
}

// TabsJSON is every tab, yours being the one asking.
func (m *Core) TabsJSON(yours *Tab) []TabJSON {
	var out []TabJSON
	for _, t := range m.Tabs {
		j := TabJSON{Name: t.Name, Dir: t.Dir, Yours: t == yours, Group: t.Group, Goal: t.Goal, Status: t.Status}
		for _, a := range t.AgentsIn() {
			j.Agents = append(j.Agents, m.AgentJSON(a, nil))
		}
		for _, w := range t.Info.Wts {
			b := BranchJSON{Repo: filepath.Base(w.Repo), Branch: w.Branch, Ahead: w.Stats.Ahead, Behind: w.Stats.Behind,
				Added: w.Add, Deleted: w.Del, Changed: len(w.Stats.Changed), Pushed: w.Unpushed == 0}
			if w.HasPR {
				b.PR, b.PRState, b.CI, b.URL, b.After = w.Pr.Number, w.Pr.State, w.Pr.CI, w.Pr.URL, w.Pr.After
			}
			j.Branches = append(j.Branches, b)
		}
		if len(j.Branches) == 0 {
			for _, w := range t.Worktrees {
				j.Branches = append(j.Branches, BranchJSON{Repo: filepath.Base(w.Repo), Branch: w.Branch})
			}
		}
		for _, p := range t.Panes() {
			if p.Kind == PaneShell {
				j.Terminals = append(j.Terminals, TerminalName(p))
			}
		}
		out = append(out, j)
	}
	return out
}

// TabMadeMsg is the result of making the tab an agent asked for.
type TabMadeMsg struct {
	From     *Tab
	Branch   string
	Kind     agents.Kind
	Model    string
	Task     string
	Confined bool
	Plan     workspace.Plan
	Wts      []workspace.Worktree
	Warnings []string
	Err      error
}

// TabRepos are the repositories a tab an agent asks for is made in: the
// asking tab's, or the one named, among those of t and, outside a
// sandbox, those nerdr finds.
func (m *Core) TabRepos(t *Tab, name string, confined bool) ([]workspace.Repo, error) {
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
	all, _ := workspace.ScanAll(m.Root, m.Cfg.WorktreesDir, m.Cfg.RepoDirs)
	var names []string
	for _, r := range all {
		if r.Name == name {
			return []workspace.Repo{r}, nil
		}
		names = append(names, r.Name)
	}
	return nil, fmt.Errorf("there is no repository %q; those there are: %s", name, strings.Join(names, ", "))
}

// KindList is the agents on PATH, as words.
func (m *Core) KindList() string {
	var names []string
	for _, k := range m.Available() {
		names = append(names, string(k))
	}
	return strings.Join(names, ", ")
}

// onQuery answers what an agent asks of nerdr.
func (m *Core) OnQuery(r hooks.Report) Cmd {
	reply := Answerer(r)
	from := m.AgentByHook(r.Pane)
	if from == nil {
		reply(hooks.Answer{Err: "this agent is not in a nerdr tab nerdr knows"})
		return nil
	}
	o := from.Owner()
	confined := m.Confined(r, from)
	switch r.What {
	case hooks.QueryAgents:
		var out []AgentJSON
		for _, a := range append([]*Tab{o}, o.ExtraAgents()...) {
			out = append(out, m.AgentJSON(a, from))
		}
		reply(JsonAnswer(out))
	case hooks.QueryTabs:
		tabs := m.TabsJSON(o)
		if confined {
			// The others are outside its sandbox.
			tabs = slices.DeleteFunc(tabs, func(j TabJSON) bool { return !j.Yours })
		}
		reply(JsonAnswer(tabs))
	case hooks.QueryRoots:
		roots := m.SearchRoots()
		if confined {
			roots = []string{o.Dir}
			for _, w := range o.Worktrees {
				roots = append(roots, w.Path)
			}
		}
		reply(hooks.Answer{Text: strings.Join(roots, "\n")})
	case hooks.QueryRead:
		reply(m.ReadPane(o, from, r, confined))
	case hooks.QueryReply:
		reply(m.TakeReply(o, from, r.To))
	default:
		reply(hooks.Answer{Err: "nerdr cannot tell " + r.What})
	}
	return nil
}

// takeReply takes the message agent n sent from out of from's list, for
// `nerdr send --wait`, which asks until there is one: its text, or none
// while there is none yet. Left there, it is typed in when from is next
// idle, as any other.
func (m *Core) TakeReply(t, from *Tab, n int) hooks.Answer {
	for i, msg := range from.Messages {
		// One an agent in a sandbox wrote is not handed to one outside.
		if msg.Auto && msg.From == n && (!msg.Sandboxed || from.InSandbox) {
			from.DropMessage(i)
			m.SaveSession()
			m.Tell(PanesMsg{})
			return hooks.Answer{Text: msg.Text}
		}
	}
	if a := t.AgentNumbered(n); a == nil || a == from {
		return hooks.Answer{Err: fmt.Sprintf("there is no other agent %d in this tab to wait for; its agents are %s", n, m.AgentList(t, from))}
	} else if a.Pane == nil {
		return hooks.Answer{Err: fmt.Sprintf("agent %d is not running: %s", n, StateName(a.Light))}
	}
	return hooks.Answer{}
}

// onStatus sets, clears or tells the line an agent keeps on where its
// tab's work stands (`nerdr status`), shown above the agent.
func (m *Core) OnStatus(r hooks.Report) {
	reply := Answerer(r)
	from := m.AgentByHook(r.Pane)
	if from == nil {
		reply(hooks.Answer{Err: "this agent is not in a nerdr tab nerdr knows"})
		return
	}
	o := from.Owner()
	switch {
	case r.Clear:
		o.Status, o.StatusBy, o.StatusAt = "", 0, time.Time{}
		m.SaveSession()
		reply(hooks.Answer{Text: "The status is cleared."})
		return
	case r.Text != "":
		o.Status, o.StatusBy, o.StatusAt = r.Text, 0, time.Now()
		if from != o {
			o.StatusBy = from.Num
		}
		m.SaveSession()
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
	reply(hooks.Answer{Text: fmt.Sprintf("%s\n(set %s%s)", o.Status, Ago(time.Since(o.StatusAt)), by)})
}

// onTerminal runs the command an agent asked for (`nerdr terminal`) in a
// new terminal pane of its tab: a shell, which stays when the command
// ends, for the user to see and use. It does not take the keyboard.
func (m *Core) OnTerminal(r hooks.Report) Cmd {
	reply := Answerer(r)
	from := m.AgentByHook(r.Pane)
	if from == nil {
		reply(hooks.Answer{Err: "this agent is not in a nerdr tab nerdr knows"})
		return nil
	}
	if from.ReviewOf != nil {
		reply(hooks.Answer{Err: "a reviewer does not run terminals"})
		return nil
	}
	if m.Confined(r, from) {
		reply(hooks.Answer{Err: NoTerminal})
		return nil
	}
	o := from.Owner()
	if r.Restart {
		return m.RestartTerminal(o, from, r)
	}
	n := 0
	for _, p := range o.Panes() {
		if p.Kind == PaneShell {
			n++
		}
	}
	if n >= MaxAgents {
		reply(hooks.Answer{Err: fmt.Sprintf("the tab has %d terminals open; ask the user to close one", n)})
		return nil
	}
	if o.ReposBusy {
		reply(hooks.Answer{Err: "the tab's repositories are being changed; try again in a moment"})
		return nil
	}
	p, cmd := m.NewShellIn(o, true, EnvByAgent+"=1")
	if p == nil {
		reply(hooks.Answer{Err: "the terminal did not start"})
		return cmd
	}
	p.Shell.Input([]byte(r.Text + "\r"))
	p.Command = r.Text
	name := TerminalName(p)
	m.Toast(o.Name + ": " + m.AgentLabel(o, from) + " runs " + TruncName(r.Text, 40) + " in " + name)
	reply(hooks.Answer{Text: fmt.Sprintf("Running in %s of this tab, which the user sees. "+
		"Read what it shows with nerdr read %s; it stays open when the command ends.", name, strconv.Quote(name))})
	return cmd
}

// restartTerminal stops what runs in the terminal r names, and runs its
// command again in it, or the one r gives (`nerdr terminal --restart`).
// nerdr answers at once; the shell is waited for out of the screen's way.
func (m *Core) RestartTerminal(o, from *Tab, r hooks.Report) Cmd {
	reply := Answerer(r)
	name := r.Name
	if name == "" {
		name = "terminal"
	}
	p, err := FindTerminal(o, name)
	if err != "" {
		reply(hooks.Answer{Err: err})
		return nil
	}
	command := r.Text
	if command == "" {
		command = p.Command
	}
	if command == "" {
		reply(hooks.Answer{Err: fmt.Sprintf("nerdr ran no command in %s to run again; give the command after its name", TerminalName(p))})
		return nil
	}
	p.Command = command
	name = TerminalName(p)
	m.Toast(o.Name + ": " + m.AgentLabel(o, from) + " restarts " + TruncName(command, 40) + " in " + name)
	reply(hooks.Answer{Text: fmt.Sprintf("Stopping what runs in %s of this tab and running %s in it again. "+
		"Read what it shows with nerdr read %s.", name, strconv.Quote(command), strconv.Quote(name))})
	sh := p.Shell
	return func() Event {
		if !sh.Rerun(command) {
			return ToastMsg(name + ": what ran did not stop; " + command + " was not run again")
		}
		return nil
	}
}

// tabMade opens the tab made for an agent, and tells the agent.
func (m *Core) TabMade(msg TabMadeMsg) Cmd {
	tell := func(text string) {
		if m.Live(msg.From) || m.Live(msg.From.Owner()) {
			m.AddMessageFrom(msg.From, text, true, msg.Confined)
		}
	}
	if msg.Err != nil {
		m.Toast("tab " + msg.Branch + ": " + msg.Err.Error())
		tell("nerdr could not make the tab you asked for: " + msg.Err.Error())
		return nil
	}
	if msg.Confined && !m.SandboxedKind(msg.Kind) {
		why := string(msg.Kind) + " is no longer set sandboxed; the worktree is made at " + msg.Plan.Dir + ", with no agent in it"
		m.Toast("tab " + msg.Branch + ": " + why)
		tell("nerdr could not start the tab you asked for: " + why)
		return nil
	}
	group := ""
	if m.Live(msg.From.Owner()) {
		group = msg.From.Owner().Group
	}
	t, cmd := m.AddTabWith(msg.Plan.Branch, msg.Plan.Dir, group, msg.Wts, msg.Kind, msg.Model, msg.Task, msg.Confined)
	m.Tell(PanesMsg{})
	note := "made " + t.Name + " for " + m.AgentName(msg.From)
	if len(msg.Warnings) > 0 {
		note += "; " + strings.Join(msg.Warnings, "; ")
	}
	m.Toast(note)
	tell(fmt.Sprintf("nerdr made the tab %s (%s), in %s, its agent %s given your task. "+
		"See how it gets on with nerdr tabs; the user reviews and merges its branch.", t.Name, msg.Plan.Branch, t.Dir, t.Agent))
	return cmd
}

// onClose closes the pane of another agent of the tab, or a terminal, for
// the agent asking (`nerdr close`): the user asked it to. Not the tab's
// first agent, which stays as long as the tab, nor the asking agent's
// own.
func (m *Core) OnClose(r hooks.Report) Cmd {
	reply := Answerer(r)
	from := m.AgentByHook(r.Pane)
	if from == nil {
		reply(hooks.Answer{Err: "this agent is not in a nerdr tab nerdr knows"})
		return nil
	}
	if from.ReviewOf != nil {
		reply(hooks.Answer{Err: "a reviewer does not close panes"})
		return nil
	}
	confined := m.Confined(r, from)
	if confined && r.To == 0 {
		reply(hooks.Answer{Err: "you run in a sandbox, and the terminals do not: you cannot close them"})
		return nil
	}
	o := from.Owner()
	var p *PaneTab
	if r.To > 0 {
		a := o.AgentNumbered(r.To)
		switch {
		case a == nil:
			reply(hooks.Answer{Err: fmt.Sprintf("there is no agent %d in this tab; its agents are %s", r.To, m.AgentList(o, from))})
			return nil
		case confined && !m.InSandbox(a):
			reply(hooks.Answer{Err: fmt.Sprintf("you run in a sandbox, and agent %d does not: you cannot close it", r.To)})
			return nil
		case a == from:
			reply(hooks.Answer{Err: fmt.Sprintf("agent %d is you: the user closes your pane", r.To)})
			return nil
		case a == o:
			reply(hooks.Answer{Err: fmt.Sprintf("agent %d is the tab's first agent: it stays as long as the tab", r.To)})
			return nil
		}
		for _, q := range o.Panes() {
			if q.A == a {
				p = q
			}
		}
		if p == nil {
			reply(hooks.Answer{Err: fmt.Sprintf("agent %d has no pane to close", r.To)})
			return nil
		}
	} else {
		var err string
		if p, err = FindTerminal(o, r.Name); err != "" {
			reply(hooks.Answer{Err: err})
			return nil
		}
	}
	name := m.PaneName(o, p)
	if p.Kind == PaneShell {
		name = TerminalName(p)
	}
	m.Toast(o.Name + ": " + m.AgentLabel(o, from) + " closed " + name)
	reply(hooks.Answer{Text: "Closed " + name + "."})
	return m.ClosePane(o, p)
}

// onNewTab makes the tab an agent asked for (`nerdr tab new`): a branch
// from main in the asking tab's repositories, or the one named, and its
// agent given the task. nerdr answers at once; the agent is told when the
// tab is there, or why it is not.
func (m *Core) OnNewTab(r hooks.Report) Cmd {
	reply := Answerer(r)
	from := m.AgentByHook(r.Pane)
	if from == nil {
		reply(hooks.Answer{Err: "this agent is not in a nerdr tab nerdr knows"})
		return nil
	}
	if from.ReviewOf != nil {
		reply(hooks.Answer{Err: "a reviewer does not make tabs"})
		return nil
	}
	if m.TabLimitReached() {
		reply(hooks.Answer{Err: "the tab limit (max_tabs) is reached: ask the user to close a tab"})
		return nil
	}
	if err := workspace.ValidBranch(r.Branch); err != nil {
		reply(hooks.Answer{Err: err.Error()})
		return nil
	}
	k := from.Owner().Agent
	if r.Kind != "" {
		k = agents.Kind(strings.ToLower(r.Kind))
	}
	if !slices.Contains(m.Available(), k) {
		reply(hooks.Answer{Err: fmt.Sprintf("there is no agent %q here; those there are: %s", k, m.KindList())})
		return nil
	}
	confined := m.Confined(r, from)
	if why := m.StartRefusal(k); why != "" && confined {
		reply(hooks.Answer{Err: why})
		return nil
	}
	if r.Model != "" && !agents.TakesModel(k) {
		reply(hooks.Answer{Err: fmt.Sprintf("nerdr does not know how to give %s a model", k)})
		return nil
	}
	repos, err := m.TabRepos(from.Owner(), r.Repo, confined)
	if err != nil {
		reply(hooks.Answer{Err: err.Error()})
		return nil
	}
	var paths []string
	for _, rp := range repos {
		paths = append(paths, rp.Path)
	}
	task := fmt.Sprintf("Task from agent %d (%s) in the nerdr tab %s, which made this tab for you, on a branch of its own:\n\n%s",
		max(from.Num, 1), from.Agent, from.Owner().Name, r.Text)
	reply(hooks.Answer{Text: fmt.Sprintf("nerdr is making the tab %s, with %s; you get a message when it is there, or why it is not. "+
		"Follow it with nerdr tabs.", r.Branch, k)})
	branch, model, wtRoot, allowed := r.Branch, r.Model, m.WtRoot, SetupAllowedFile()
	return m.AskSetup(paths, func() Cmd {
		return func() Event {
			res := TabMadeMsg{From: from, Branch: branch, Kind: k, Model: model, Task: task, Confined: confined}
			plan, err := workspace.MakePlan(wtRoot, branch, repos)
			// An unpinned base is one the asking agent could have moved.
			plan.Sandboxed = confined
			if err == nil {
				for _, it := range plan.Items {
					if it.Mode == "local" || it.Mode == "remote" {
						err = fmt.Errorf("%s: a branch %s is there already; pick another name", it.Repo.Name, branch)
					}
				}
			}
			if err == nil {
				res.Warnings, err = plan.Sync()
			}
			if err == nil {
				res.Wts, err = plan.Execute()
			}
			if err != nil {
				res.Err = err
				return res
			}
			res.Plan = plan
			res.Warnings = append(res.Warnings, workspace.Prepare(res.Wts, plan.SetupIn(), allowed, func(string) {})...)
			return res
		}
	})
}
