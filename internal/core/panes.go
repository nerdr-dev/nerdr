package core

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/term"
)

// FrontAgent is the agent the keyboard is with, or the one it is nearest
// to: the front tab's focused pane's agent, its own otherwise. Nil with no
// tab in front.
func (m *Core) FrontAgent() *Tab {
	t := m.Front()
	if t == nil {
		return nil
	}
	if a := t.Focused().Agent(); a != nil {
		return a
	}
	return t
}

// AllAgents are every agent nerdr runs: the tabs' own, the others beside
// them and the reviewers.
func (m *Core) AllAgents() []*Tab {
	var out []*Tab
	for _, t := range m.Tabs {
		out = append(out, t.AgentsIn()...)
	}
	return out
}

// AgentByHook is the agent that reports as id, of any tab, or nil.
func (m *Core) AgentByHook(id string) *Tab {
	for _, a := range m.AllAgents() {
		if a.HookID != "" && a.HookID == id {
			return a
		}
	}
	return nil
}

// PaneMinW is the narrowest a pane is shown, its frame included.
func (m *Core) PaneMinW() int { return max(m.Cfg.PaneMinWidth, 20) }

// PaneMinShare is the narrowest a divider can make a pane, its frame
// included.
const PaneMinShare = 12

// ShellMargin is the columns a terminal pane leaves empty at its right.
const ShellMargin = 1

// ShellCommand is the shell to run: the config's, else $SHELL, else sh.
func (m *Core) ShellCommand() []string {
	for _, s := range []string{m.Cfg.Shell, os.Getenv("SHELL")} {
		if f := strings.Fields(s); len(f) > 0 {
			return f
		}
	}
	return []string{"/bin/sh"}
}

// RestoreAgents puts back the agents t had beside its own when nerdr was
// last running, to be resumed.
func (m *Core) RestoreAgents(t *Tab) {
	for _, st := range t.Agents {
		a := &Tab{AgentState: Agent{Tab: st, Light: StStarting}, PartOf: t}
		if !t.Holds(a.Dir) {
			// Kept from before an agent could run in a folder of the tab
			// other than its own: one carrying on a conversation had in
			// one of its repositories, from the search.
			a.Dir = t.Dir
		}
		a.Name, a.Worktrees = t.Name, t.Worktrees
		a.TakeQueue()
		t.PaneList = append(t.Panes(), &PaneTab{Kind: PaneAgent, A: a, Used: time.Now()})
		if a.Num == 0 || t.AgentNumbered(a.Num) != a {
			a.Num = 0
			a.Num = t.NextNum()
		}
	}
	t.Agents = nil
}

// ShellTitle is what is worth showing of the title a terminal's program
// set, in a worktree at dir: of a shell's "user@host: ~/dir/sub", the
// directory, "sub" as from the worktree, or none in the worktree itself,
// which the tab is named for; another program's title (vim's, say) as it
// is.
func ShellTitle(title, dir string) string {
	title = strings.TrimSpace(title)
	at, path, ok := strings.Cut(title, ":")
	if !ok || !strings.Contains(at, "@") || strings.ContainsAny(at, " \t") {
		return title
	}
	path = strings.TrimSpace(path)
	if rest, ok := strings.CutPrefix(path, "~"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			path = home + rest
		}
	}
	if rel, err := filepath.Rel(dir, filepath.Clean(path)); err == nil && !strings.HasPrefix(rel, "..") {
		if rel == "." {
			return ""
		}
		return rel
	}
	return filepath.Base(path)
}

// AgentLabel names agent a among t's: its number and kind, "2:codex";
// the kind alone while it is the tab's only agent.
func (m *Core) AgentLabel(t *Tab, a *Tab) string {
	if a == nil {
		return "agent"
	}
	if len(t.ExtraAgents()) == 0 && a.ReviewOf == nil {
		return string(a.Agent)
	}
	return strconv.Itoa(a.Num) + ":" + string(a.Agent)
}

// MaxAgents is the most agents a tab has beside its own that another
// agent can start: each is a program running, and spends from its
// account.
const MaxAgents = 8

// AgentList is t's agents as words, "1:claude (you), 2:codex", you
// being from.
func (m *Core) AgentList(t, from *Tab) string {
	var out []string
	for _, a := range append([]*Tab{t}, t.ExtraAgents()...) {
		s := strconv.Itoa(a.Num) + ":" + string(a.Agent)
		if a == from {
			s += " (you)"
		}
		out = append(out, s)
	}
	return strings.Join(out, ", ")
}

// ClaimMsg is the conversation found to be agent t's, while pane was its
// process; sure, that the one nerdr gave it is.
type ClaimMsg struct {
	T    *Tab
	Id   string
	Pid  *term.Pane
	Sure bool
}

// ClaimSession looks, once agent t has finished a turn, for the
// conversation it is in, when nerdr could not give it an id and it has
// not reported one (codex, agy), or gave one it may not have taken
// (opencode): the one in the worktree since it started that is no other
// agent's and has in it what nerdr typed in to it. Known by its id, it is
// the one carried on, never another agent's beside it. While that is not
// certain, it is looked for again after the next turn.
func (m *Core) ClaimSession(t *Tab) Cmd {
	if t.ReviewOf != nil || t.Pane == nil || t.SessionID != "" && !t.SessionUnsure {
		return nil
	}
	specs, kind, dir, pane := m.Cfg.Agents, t.Agent, t.Dir, t.Pane
	skip, since, said, given := m.OthersSessions(t), t.StartedAt.Add(-time.Second), slices.Clone(t.Said), t.SessionID
	return func() Event {
		if given != "" && agents.HasSession(specs, kind, given, dir) {
			return ClaimMsg{T: t, Id: given, Pid: pane, Sure: true}
		}
		return ClaimMsg{T: t, Id: agents.Claim(specs, kind, dir, skip, since, said), Pid: pane}
	}
}

// addPane puts p last in t's panes, and gives it the keyboard unless
// quiet: a review that starts by itself does not take it, and is the
// first to be folded away.
func (m *Core) AddPane(t *Tab, p *PaneTab, quiet bool) Cmd {
	t.PaneList = append(t.Panes(), p)
	if quiet {
		m.Tell(PanesMsg{})
		return nil
	}
	return m.Tell(PaneOpenedMsg{T: t, P: p})
}

// newShellIn opens a terminal in t's worktree, in a pane of its own,
// last; quiet leaves the keyboard where it is. It returns the pane, nil
// when the terminal did not start.
func (m *Core) NewShellIn(t *Tab, quiet bool) (*PaneTab, Cmd) {
	if m.ReposHold(t) {
		return nil, nil
	}
	n := 1
	for _, p := range t.Panes() {
		if p.Kind == PaneShell {
			n = max(n, p.N+1)
		}
	}
	p := &PaneTab{Kind: PaneShell, N: n}
	cmd := m.AddPane(t, p, quiet)
	w, h := m.Client.ShellSize(t, p)
	env := append(ChildEnv(t), "NERDR_SHELL=1")
	sh, err := term.Start(m.ShellCommand(), t.Dir, env, w, h, m.Notify)
	if err != nil {
		m.DropPane(t, p)
		m.Toast("terminal: " + err.Error())
		return nil, m.Tell(LayoutMsg{})
	}
	p.Shell = sh
	m.Tell(TermsMsg{T: t})
	return p, cmd
}

// checkShells closes the pane of a terminal that has exited, as a
// terminal window closes when its shell does.
func (m *Core) CheckShells() {
	for _, t := range m.Tabs {
		// A copy: dropPane takes the pane out of the list ranged over.
		for _, p := range slices.Clone(t.Panes()) {
			if p.Kind != PaneShell || p.Shell == nil {
				continue
			}
			if exited, _, _ := p.Shell.Exited(); exited {
				go p.Shell.Close()
				m.DropPane(t, p)
			}
		}
	}
}

// newAgent starts kind k in a new conversation in t's worktree, in a pane
// of its own, last.
func (m *Core) NewAgent(t *Tab, k agents.Kind) Cmd {
	_, cmd := m.NewAgentWith(t, k, "", "", false, false)
	return cmd
}

// newAgentWith is newAgent, the agent started with model in place of the
// config's, if any, and given task as its first message, if any; quiet
// leaves the keyboard where it is, and sandboxed marks task as from an
// agent in a sandbox. It returns the agent, nil when t is gone.
func (m *Core) NewAgentWith(t *Tab, k agents.Kind, model, task string, quiet, sandboxed bool) (*Tab, Cmd) {
	if m.TabIndex(t) < 0 || m.ReposHold(t) {
		return nil, nil
	}
	a := &Tab{AgentState: Agent{Tab: state.Tab{ID: NewTabID(), Name: t.Name, Dir: t.Dir, Agent: k, Model: model, Worktrees: t.Worktrees, Num: t.NextNum()}, Light: StStarting}, PartOf: t}
	p := &PaneTab{Kind: PaneAgent, A: a}
	cmd := m.AddPane(t, p, quiet)
	if task != "" {
		m.AddMessageFrom(a, task, true, sandboxed) // before the start, which may take it
	}
	start := m.Start(a, false)
	if a.Pane == nil && a.Err != "" {
		m.Toast(t.Name + ": " + a.Err)
	}
	m.SaveSession()
	m.Tell(TermsMsg{T: t})
	return a, Batch(cmd, start)
}

// onMessage takes a message one agent sends another of its tab with
// `nerdr send`: it goes to the other as a task, typed in when it is idle,
// saying whom it is from and how to answer. The sender is told whether it
// went; with no number, which agents there are.
func (m *Core) OnMessage(r hooks.Report) Cmd {
	reply := func(a hooks.Answer) {
		if r.Reply != nil {
			select {
			case r.Reply <- a:
			default:
			}
		}
	}
	from := m.AgentByHook(r.Pane)
	if from == nil {
		reply(hooks.Answer{Err: "this agent is not in a nerdr tab nerdr knows"})
		return nil
	}
	o := from.Owner()
	if from.ReviewOf != nil {
		reply(hooks.Answer{Err: "a reviewer sends its review with nerdr post-review, to the agent it reviews"})
		return nil
	}
	to := o.AgentNumbered(r.To)
	switch {
	case r.To == 0:
		reply(hooks.Answer{Text: "The agents of this tab: " + m.AgentList(o, from) + ". nerdr send N MESSAGE sends one a message."})
		return nil
	case to == nil:
		reply(hooks.Answer{Err: fmt.Sprintf("there is no agent %d in this tab; its agents are %s", r.To, m.AgentList(o, from))})
		return nil
	case to == from:
		reply(hooks.Answer{Err: fmt.Sprintf("agent %d is you; the others are %s", r.To, m.AgentList(o, from))})
		return nil
	case m.Confined(r, from) && !m.InSandbox(to):
		reply(hooks.Answer{Err: fmt.Sprintf("you run in a sandbox, and agent %d (%s) does not: "+
			"nerdr does not pass it what you write. Ask the user instead", to.Num, to.Agent)})
		return nil
	}
	text := fmt.Sprintf("Message from agent %d (%s) in this nerdr tab:\n\n%s\n\n"+
		"It has none of your conversation. To answer it, or to send it your work, use the nerdr-send skill: nerdr send %d \"…\"",
		from.Num, from.Agent, r.Text, from.Num)
	m.AddMessageFrom(to, text, true, m.Confined(r, from))
	to.Messages[len(to.Messages)-1].From = from.Num // for nerdr send --wait
	m.SaveSession()
	m.Tell(PanesMsg{})
	m.Toast(o.Name + ": " + m.AgentLabel(o, from) + " → " + m.AgentLabel(o, to) + ": a message, sent when it is idle")
	when := "now"
	if to.Light != StIdle && to.Light != StDone {
		when = "when it is idle"
	}
	reply(hooks.Answer{Text: fmt.Sprintf("Sent to agent %d (%s): it gets it %s, and answers with nerdr send %d if it has anything to say.",
		to.Num, to.Agent, when, from.Num)})
	return nil
}

// onNewAgent starts another agent in the tab of the one asking (`nerdr
// agent`), in a pane beside it, with the task it wrote. It does not take
// the keyboard: the user may be typing to another agent.
func (m *Core) OnNewAgent(r hooks.Report) Cmd {
	reply := func(a hooks.Answer) {
		if r.Reply != nil {
			select {
			case r.Reply <- a:
			default:
			}
		}
	}
	from := m.AgentByHook(r.Pane)
	if from == nil {
		reply(hooks.Answer{Err: "this agent is not in a nerdr tab nerdr knows"})
		return nil
	}
	if from.ReviewOf != nil {
		reply(hooks.Answer{Err: "a reviewer does not start agents; it sends its review with nerdr post-review"})
		return nil
	}
	o := from.Owner()
	if n := len(o.ExtraAgents()); n >= MaxAgents {
		reply(hooks.Answer{Err: fmt.Sprintf("the tab has %d agents beside its first, the most nerdr starts for an agent: "+
			"give one of them the task with nerdr send, or ask the user to close one; its agents are %s", n, m.AgentList(o, from))})
		return nil
	}
	k := from.Agent
	if r.Kind != "" {
		k = agents.Kind(strings.ToLower(r.Kind))
	}
	avail := agents.Available(m.Cfg.Agents)
	if !slices.Contains(avail, k) {
		names := make([]string, len(avail))
		for i, a := range avail {
			names[i] = string(a)
		}
		reply(hooks.Answer{Err: fmt.Sprintf("there is no agent %q here; those there are: %s", k, strings.Join(names, ", "))})
		return nil
	}
	if why := m.StartRefusal(k); why != "" && m.Confined(r, from) {
		reply(hooks.Answer{Err: why})
		return nil
	}
	if r.Model != "" && !agents.TakesModel(k) {
		reply(hooks.Answer{Err: fmt.Sprintf("nerdr does not know how to give %s a model: leave --model out, and it runs the one the config gives it", k)})
		return nil
	}
	if o.ReposBusy {
		reply(hooks.Answer{Err: "the tab's repositories are being changed; try again in a moment"})
		return nil
	}
	num := o.NextNum()
	task := fmt.Sprintf("Task from agent %d (%s) in this nerdr tab, which started you in a pane beside it, as agent %d, "+
		"in the same worktree:\n\n%s\n\n"+
		"You have none of its conversation. When you are done, or need something from it, tell it with the nerdr-send skill: "+
		"nerdr send %d \"…\". It waits for your answer.",
		from.Num, from.Agent, num, r.Text, from.Num)
	a, cmd := m.NewAgentWith(o, k, r.Model, task, true, m.Confined(r, from))
	if a == nil {
		reply(hooks.Answer{Err: "the tab is gone"})
		return nil
	}
	m.Toast(o.Name + ": " + m.AgentLabel(o, from) + " started " + m.AgentLabel(o, a))
	if a.Pane == nil && a.Err != "" {
		reply(hooks.Answer{Err: fmt.Sprintf("agent %d (%s) could not start: %s", a.Num, a.Agent, a.Err)})
		return cmd
	}
	model := ""
	if a.Model != "" {
		model = ", model " + a.Model
	}
	reply(hooks.Answer{Text: fmt.Sprintf("Started agent %d (%s%s) beside you, with your task as its first message. "+
		"It answers with nerdr send %d when it is done; reach it with nerdr send %d. The tab's agents: %s.",
		a.Num, a.Agent, model, from.Num, a.Num, m.AgentList(o, from))})
	return cmd
}

// onClaim keeps the conversation found for an agent, unless it has been
// restarted meanwhile, or another agent has it.
func (m *Core) OnClaim(msg ClaimMsg) {
	t := msg.T
	if !m.Live(t) || t.Pane != msg.Pid || t.SessionID != "" && !t.SessionUnsure {
		return
	}
	switch {
	case msg.Sure && msg.Id == t.SessionID:
		t.SessionUnsure = false
	case msg.Id != "" && !msg.Sure && !m.OthersSessions(t)[msg.Id]:
		t.SessionUnsure = false
		m.SetSession(t, msg.Id)
	}
}

// PaneKind is what runs in a pane.
type PaneKind int

const (
	// PaneAgent is an agent: the tab's own, or another beside it.
	PaneAgent PaneKind = iota
	// PaneShell is a terminal in the worktree.
	PaneShell
	// PaneReview is a code review of one of the tab's agents.
	PaneReview
)

// PaneTab is one of a tab's panes.
type PaneTab struct {
	Kind PaneKind
	// A is the agent in the pane: the tab itself in its first pane,
	// another agent, or the reviewer; nil in a review pane while the
	// reviewer is being picked.
	A *Tab
	// Of is, in a review pane, the agent whose work it reviews.
	Of *Tab
	// Shell is the terminal's program; n numbers the tab's terminals;
	// command is the last command an agent ran in it (nerdr terminal), to
	// run again.
	Shell   *term.Pane
	N       int
	Command string
	// Small is set while the pane is minimised by hand; used is when it
	// last had the keyboard, for which to fold first when room runs out.
	Small bool
	Used  time.Time
	// Weight is the pane's share of the room beside the others shown, as
	// its divider was dragged to; 0 is an even share.
	Weight float64
}

// Panes are t's panes, its own agent first.
func (t *Tab) Panes() []*PaneTab {
	if len(t.PaneList) == 0 || t.PaneList[0].A != t {
		t.PaneList = append([]*PaneTab{{Kind: PaneAgent, A: t, Used: time.Now()}}, t.PaneList...)
	}
	if t.Num == 0 {
		t.Num = t.NextNum()
	}
	return t.PaneList
}

// OwnPane is the pane of t's own agent, which stays as long as the tab.
func (t *Tab) OwnPane() *PaneTab { return t.Panes()[0] }

// NextNum is the lowest number none of t's agents has. Numbers are kept
// for good: an agent that goes leaves a gap rather than renumber the
// others, which other agents may have been told.
func (t *Tab) NextNum() int {
	used := map[int]bool{t.Num: true}
	for _, p := range t.PaneList {
		if p.Kind == PaneAgent && p.A != nil {
			used[p.A.Num] = true
		}
	}
	n := 1
	for used[n] {
		n++
	}
	return n
}

// AgentNumbered is the agent of t's with number n, or nil.
func (t *Tab) AgentNumbered(n int) *Tab {
	for _, a := range append([]*Tab{t}, t.ExtraAgents()...) {
		if a.Num == n {
			return a
		}
	}
	return nil
}

// Focused is the pane of t's the keyboard goes to while it is with the
// tab: the one last gone to, or its own agent.
func (t *Tab) Focused() *PaneTab {
	ps := t.Panes()
	if t.On != nil && slices.Contains(ps, t.On) {
		return t.On
	}
	return ps[0]
}

// Term is the program the pane shows: its agent's or its terminal's; nil
// while there is none.
func (p *PaneTab) Term() *term.Pane {
	if p.Kind == PaneShell {
		return p.Shell
	}
	if p.A != nil {
		return p.A.Pane
	}
	return nil
}

// Agent is the agent the keys about an agent are for in pane p: its own,
// or in a review pane the one reviewed.
func (p *PaneTab) Agent() *Tab {
	switch p.Kind {
	case PaneAgent:
		return p.A
	case PaneReview:
		return p.Of
	}
	return nil
}

// AgentsIn are the agents working in t: its own, the others in its panes,
// and the reviewers of each.
func (t *Tab) AgentsIn() []*Tab {
	var out []*Tab
	for _, a := range append([]*Tab{t}, t.ExtraAgents()...) {
		out = append(out, a)
		if a.Review != nil {
			for _, s := range a.Review.Seats {
				out = append(out, s.R)
			}
		}
	}
	return out
}

// ExtraAgents are the agents t has beside its own, not counting reviewers.
func (t *Tab) ExtraAgents() []*Tab {
	var out []*Tab
	for _, p := range t.Panes()[1:] {
		if p.Kind == PaneAgent && p.A != nil {
			out = append(out, p.A)
		}
	}
	return out
}

// Owner is the tab agent t works in: itself, or the tab its pane is in.
func (t *Tab) Owner() *Tab {
	switch {
	case t.PartOf != nil:
		return t.PartOf
	case t.ReviewOf != nil:
		return t.ReviewOf.Owner()
	}
	return t
}

// PaneOf is the pane agent t is in, or nil.
func (t *Tab) PaneOf() *PaneTab {
	for _, p := range t.Owner().Panes() {
		if p.A == t {
			return p
		}
	}
	return nil
}

// Share is p's weight, 1 when it has none of its own.
func (p *PaneTab) Share() float64 {
	if p.Weight <= 0 {
		return 1
	}
	return p.Weight
}

// Holds reports whether dir is t's folder, or in it or one of its
// worktrees.
func (t *Tab) Holds(dir string) bool {
	if dir == "" {
		return false
	}
	dir = filepath.Clean(dir)
	paths := []string{t.Dir}
	for _, w := range t.Worktrees {
		paths = append(paths, w.Path)
	}
	for _, p := range paths {
		p = filepath.Clean(p)
		if dir == p || strings.HasPrefix(dir, p+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// paneStatus is the light p shows: its agent's; a terminal has none.
func (p *PaneTab) Status() (Status, bool) {
	if p.A == nil || p.Kind == PaneShell {
		return 0, false
	}
	return p.A.Light, true
}

// SavedAgents are t's agents beside its own, as the session keeps them.
func (t *Tab) SavedAgents() []state.Tab {
	var out []state.Tab
	for _, a := range t.ExtraAgents() {
		st := a.Tab
		st.Agents = nil
		out = append(out, st)
	}
	return out
}

// NoteSaid keeps what nerdr typed in to agent t, the first few things
// since it started, to know its conversation by; see claimSession.
func (t *Tab) NoteSaid(s string) {
	if strings.TrimSpace(s) != "" && len(t.Said) < 8 {
		t.Said = append(t.Said, s)
	}
}

// closePane stops what runs in p and takes it out of the row: its
// terminal, its agent or its reviewer. The tab's first agent goes only
// with the tab: it is always there to carry on the tab's conversation.
func (m *Core) ClosePane(t *Tab, p *PaneTab) Cmd {
	switch {
	case p == t.OwnPane():
		m.Toast(m.AgentLabel(t, t) + " is the tab's first agent: it stays as long as the tab · " + KeyRef("x") + " closes the tab")
		return nil
	case p.Kind == PaneShell:
		if p.Shell != nil {
			go p.Shell.Close()
		}
	case p.Kind == PaneReview && p.A != nil:
		if p.Of.SeatOf(p.A) != nil {
			return Batch(m.DropReviewer(p.Of, p.A, string(p.A.Agent)+" stopped", false), m.Tell(LayoutMsg{}))
		}
	case p.Kind == PaneAgent && p.A != nil:
		a := p.A
		if a.Review != nil {
			m.EndReview(a, "stopped", false)
		}
		for _, q := range append([]*PaneTab{}, t.Panes()...) {
			if q.Kind == PaneReview && q.Of == a {
				m.DropPane(t, q)
			}
		}
		if a.Pane != nil {
			go a.Pane.Close()
		}
		name := m.PaneName(t, p)
		kept := t.KeepTasks(a)
		m.DropPane(t, p)
		m.SaveSession()
		note := t.Name + ": stopped " + name + "; its conversation is kept by " + string(a.Agent)
		if kept > 0 {
			note += " · " + Plural(kept, "task") + " for it wait in " + ScreenRef(3) + ", inactive"
		}
		m.Toast(note)
		return m.Tell(LayoutMsg{})
	}
	m.DropPane(t, p)
	return m.Tell(LayoutMsg{})
}

// PaneName is what p is called in its frame and in the row.
func (m *Core) PaneName(t *Tab, p *PaneTab) string {
	switch p.Kind {
	case PaneShell:
		name := "terminal"
		if p.N > 1 {
			name += " " + strconv.Itoa(p.N)
		}
		if p.Shell != nil {
			if title := ShellTitle(p.Shell.Title(), t.Dir); title != "" {
				name += ": " + ansi.Truncate(title, 24, "…")
			}
		}
		return name
	case PaneReview:
		if p.A == nil {
			return "review of " + m.AgentLabel(t, p.Of) + "…"
		}
		return string(p.A.Agent) + " reviews " + m.AgentLabel(t, p.Of)
	}
	return m.AgentLabel(t, p.A)
}

// AgentName is how a notification names agent a: its tab's name, and
// which of its agents it is when not the tab's own.
func (m *Core) AgentName(a *Tab) string {
	o := a.Owner()
	if o == a {
		return a.Name
	}
	if p := a.PaneOf(); p != nil {
		return o.Name + " · " + m.PaneName(o, p)
	}
	return o.Name + " · " + string(a.Agent)
}

// DropPane takes p out of t's panes, but for t's own, and tells the client.
func (m *Core) DropPane(t *Tab, p *PaneTab) {
	ps := t.Panes()
	i := slices.Index(ps, p)
	if i < 0 || p == t.OwnPane() {
		return
	}
	t.PaneList = slices.Delete(ps, i, i+1)
	m.Tell(PaneClosedMsg{T: t, P: p})
}

// DropReviewPane takes reviewer r's pane out of its tab.
func (m *Core) DropReviewPane(r *Tab) {
	if p := r.PaneOf(); p != nil {
		m.DropPane(r.Owner(), p)
	}
}
