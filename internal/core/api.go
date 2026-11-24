package core

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/term"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// The API: what a program of the user's own, such as the connector of a
// phone app or a script, reads of core and asks of it, by the API socket
// (see internal/api). It is the user's, as the client socket is, and is
// guarded as that is; its calls run on the loop (see Call). Everything it
// names, it names by ID: a tab's, an agent's, a terminal pane's, a
// question's.

// APIVersion is the version of the API: raised when a change would break
// a program written for the one before. What is only added does not.
const APIVersion = 1

// APIState is what the API's state gives: every tab, the agents that want
// the user, neediest first (as the waiting list has them), what nerdr
// asks the user, and the agents there are to start.
type APIState struct {
	Root      string        `json:"root"`
	Tabs      []APITab      `json:"tabs"`
	Waiting   []string      `json:"waiting"`
	Questions []APIQuestion `json:"questions"`
	Agents    []string      `json:"agents"`
}

// APITab is a tab. Status is what its agent last said of where the work
// stands (nerdr status); Task what it was asked to do.
type APITab struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Group     string        `json:"group,omitempty"`
	Dir       string        `json:"dir"`
	Task      string        `json:"task,omitempty"`
	Status    string        `json:"status,omitempty"`
	Agents    []APIAgent    `json:"agents"`
	Terminals []APITerminal `json:"terminals,omitempty"`
	Branches  []BranchJSON  `json:"branches,omitempty"`
}

// APIAgent is an agent of a tab: Num is its number in the tab, as the
// user sees it ("1:claude"); State as `nerdr agents` says it; Messages how
// many messages wait to be typed in to it.
type APIAgent struct {
	ID       string   `json:"id"`
	Num      int      `json:"num"`
	Kind     string   `json:"kind"`
	Model    string   `json:"model,omitempty"`
	State    string   `json:"state"`
	Reviewer bool     `json:"reviewer,omitempty"`
	Messages int      `json:"messages_waiting,omitempty"`
	Context  *float64 `json:"context_percent,omitempty"`
}

// APITerminal is a terminal pane of a tab: Name as `nerdr read` calls it,
// Command the last an agent ran in it.
type APITerminal struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Command string `json:"command,omitempty"`
	Running bool   `json:"running"`
}

// APIQuestion is something nerdr asks the user, to answer yes or no by its
// ID. Kind is "repo_setup" (may the setup of Repos run in new worktrees)
// or "plugin_run" (may Agent run Plugin with Values, in Dir; unanswered by
// Until, it is no).
type APIQuestion struct {
	ID     int            `json:"id"`
	Kind   string         `json:"kind"`
	Text   string         `json:"text"`
	Repos  []string       `json:"repos,omitempty"`
	Agent  string         `json:"agent,omitempty"`
	Plugin string         `json:"plugin,omitempty"`
	Values map[string]any `json:"values,omitempty"`
	Dir    string         `json:"dir,omitempty"`
	Until  *time.Time     `json:"until,omitempty"`
}

// APIRepo is a repository a tab can be made in.
type APIRepo struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// ErrAPIQuitting is the answer to a call that would change something
// while nerdr quits.
var ErrAPIQuitting = errors.New("nerdr is quitting")

// APIState is the state now. Mu is held.
func (m *Core) APIState() APIState {
	s := APIState{Root: m.Root, Tabs: []APITab{}, Waiting: []string{}, Questions: []APIQuestion{}, Agents: []string{}}
	for _, t := range m.Tabs {
		j := APITab{ID: m.Ref(t).Tab, Name: t.Name, Group: t.Group, Dir: t.Dir, Task: t.Goal, Status: t.Status,
			Agents: []APIAgent{}, Branches: BranchesJSON(t)}
		for _, a := range t.AgentsIn() {
			j.Agents = append(j.Agents, m.apiAgent(a))
		}
		for _, p := range t.Panes() {
			if p.Kind == PaneShell {
				j.Terminals = append(j.Terminals, APITerminal{ID: m.PaneRef(p).Pane, Name: TerminalName(p),
					Command: p.Command, Running: running(p)})
			}
		}
		s.Tabs = append(s.Tabs, j)
	}
	for _, a := range m.Waiting() {
		s.Waiting = append(s.Waiting, m.Ref(a).Tab)
	}
	for _, q := range m.Asking {
		s.Questions = append(s.Questions, m.apiQuestion(q))
	}
	for _, k := range m.Available() {
		s.Agents = append(s.Agents, string(k))
	}
	return s
}

func (m *Core) apiAgent(a *Tab) APIAgent {
	j := m.AgentJSON(a, nil)
	return APIAgent{ID: m.Ref(a).Tab, Num: j.Num, Kind: j.Kind, Model: j.Model, State: j.State,
		Reviewer: j.Review, Messages: j.Waiting, Context: j.Context}
}

func (m *Core) apiQuestion(q *Question) APIQuestion {
	j := APIQuestion{ID: q.ID}
	switch q.Kind {
	case AskRepoSetup:
		j.Kind, j.Repos = "repo_setup", q.Repos
		var names []string
		for _, r := range q.Repos {
			names = append(names, workspaceName(r))
		}
		j.Text = "Run the setup of " + strings.Join(names, ", ") + " (its .nerdr.toml) in new worktrees?"
	case AskPluginRun:
		j.Kind, j.Plugin, j.Values, j.Dir = "plugin_run", q.P.Name, q.Vals, q.Dir
		who := "an agent"
		if a := m.ByRef(q.A); a != nil {
			j.Agent = q.A.Tab
			who = m.AgentName(a)
		}
		j.Text = who + " would run the plugin " + q.P.Name
		if q.Confined {
			j.Text += ", outside its sandbox"
		}
		if !q.Until.IsZero() {
			until := q.Until
			j.Until = &until
		}
	default:
		j.Kind = "other"
	}
	return j
}

// workspaceName is the last element of a repository's path.
func workspaceName(path string) string {
	if i := strings.LastIndexByte(strings.TrimRight(path, "/"), '/'); i >= 0 {
		return strings.TrimRight(path, "/")[i+1:]
	}
	return path
}

// running reports whether terminal pane p's program runs.
func running(p *PaneTab) bool {
	if p.Shell == nil {
		return false
	}
	exited, _, _ := p.Shell.Exited()
	return !exited
}

// apiAgentByID is the agent id names, or why there is none.
func (m *Core) apiAgentByID(id string) (*Tab, error) {
	a := TabList(m.Tabs).Find(id)
	if a == nil {
		return nil, fmt.Errorf("there is no agent %q", id)
	}
	return a, nil
}

// APIRead is the last lines of what the agent or terminal pane id shows,
// as text.
func (m *Core) APIRead(id string, lines int) (string, error) {
	l, err := m.APIReadLook(id, lines)
	return l.Text, err
}

// APIReadLook is APIRead, with the size of the screen the text is read
// from and whether the program scrolls itself (see APIScroll), in the
// same look.
func (m *Core) APIReadLook(id string, lines int) (term.Look, error) {
	if lines <= 0 {
		lines = ReadLines
	}
	if a := TabList(m.Tabs).Find(id); a != nil {
		if a.Pane == nil {
			return term.Look{}, fmt.Errorf("the agent is not running: %s", StateName(a.Light))
		}
		return a.Pane.TextLook(lines), nil
	}
	if _, p := TabList(m.Tabs).FindPane(id); p != nil && p.Kind == PaneShell {
		if p.Shell == nil {
			return term.Look{}, errors.New("the terminal is not running")
		}
		return p.Shell.TextLook(lines), nil
	}
	return term.Look{}, fmt.Errorf("there is no agent or terminal %q", id)
}

// APISend adds text to agent id's messages, to be typed in when it is at
// its prompt, as a message from another agent is.
func (m *Core) APISend(id, text string) error {
	if m.Quitting {
		return ErrAPIQuitting
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("no text to send")
	}
	a, err := m.apiAgentByID(id)
	if err != nil {
		return err
	}
	m.AddMessage(a, text, true)
	return nil
}

// apiKeys are the keys the API's keys takes by name.
var apiKeys = map[string]string{
	"enter": "\r", "esc": "\x1b", "tab": "\t", "shift-tab": "\x1b[Z", "backspace": "\x7f", "space": " ",
	"up": "\x1b[A", "down": "\x1b[B", "right": "\x1b[C", "left": "\x1b[D",
	"home": "\x1b[H", "end": "\x1b[F", "pageup": "\x1b[5~", "pagedown": "\x1b[6~",
	"ctrl-c": "\x03", "ctrl-d": "\x04", "ctrl-z": "\x1a", "ctrl-l": "\x0c", "ctrl-r": "\x12",
	"ctrl-u": "\x15", "ctrl-a": "\x01", "ctrl-e": "\x05", "ctrl-o": "\x0f", "ctrl-t": "\x14",
}

// APIKeyBytes is keys as the program is to be given them: each a name of
// apiKeys or a single character, typed as it is.
func APIKeyBytes(keys []string) ([]byte, error) {
	var b []byte
	for _, k := range keys {
		if s, ok := apiKeys[strings.ToLower(k)]; ok {
			b = append(b, s...)
			continue
		}
		if r := []rune(k); len(r) == 1 && r[0] >= ' ' && r[0] != 0x7f {
			b = append(b, k...)
			continue
		}
		names := make([]string, 0, len(apiKeys))
		for n := range apiKeys {
			names = append(names, n)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("no key %q: a key is one character, or one of %s", k, strings.Join(names, ", "))
	}
	return b, nil
}

// APIType types text, as a paste, and then keys, into the agent or the
// terminal pane id, at once: to answer what an agent asks on its screen,
// or to run a command in a terminal. A message is APISend's.
func (m *Core) APIType(id, text string, keys []string) error {
	return m.APITypeBy("", id, text, keys)
}

// APITypeBy is APIType by the API's viewer who: if it shows the program,
// that counts as it being active there, its size the program's, once
// what is typed is known to go in, just before it does.
func (m *Core) APITypeBy(who, id, text string, keys []string) error {
	if m.Quitting {
		return ErrAPIQuitting
	}
	b, err := APIKeyBytes(keys)
	if err != nil {
		return err
	}
	ref, _, err := m.apiRunning(id)
	if err != nil {
		return err
	}
	typeIn := func() {
		if text != "" {
			Type{Ref: ref, B: Paste(text)}.Do(m)
		}
		if len(b) > 0 {
			Type{Ref: ref, B: b}.Do(m)
		}
	}
	if who == "" {
		typeIn()
		return nil
	}
	m.Views.Act(ref.Pane, who, time.Now(), m.Program(ref), typeIn)
	return nil
}

// apiRunning is the program of the agent or terminal pane id, which must
// be running, and what refers to it.
func (m *Core) apiRunning(id string) (ProgramRef, Program, error) {
	var ref ProgramRef
	if a := TabList(m.Tabs).Find(id); a != nil {
		if a.Pane == nil {
			return ref, nil, fmt.Errorf("the agent is not running: %s", StateName(a.Light))
		}
		ref = ProgramRef{Pane: m.PaneRef(a.PaneOf())}
	} else if _, p := TabList(m.Tabs).FindPane(id); p != nil && p.Kind == PaneShell {
		if !running(p) {
			return ref, nil, errors.New("the terminal is not running")
		}
		ref = ProgramRef{Pane: m.PaneRef(p)}
	} else {
		return ref, nil, fmt.Errorf("there is no agent or terminal %q", id)
	}
	prog := m.Program(ref)
	if prog == nil {
		return ref, nil, errors.New("nothing runs there")
	}
	return ref, prog, nil
}

// MaxScroll is the most wheel steps one APIScroll turns; more are as many.
const MaxScroll = 50

// APIScroll turns the wheel n steps over the agent or terminal pane id,
// for viewer who ("" for none): up, to older text, when n is positive,
// down when it is negative, as nerdr's window does over a full-screen
// program, which keeps its history itself. Only a program on the
// alternate screen that is told of the wheel scrolls; for any other it is
// an error, and nothing is sent: no arrow keys in place of the wheel, as
// an agent takes up for its earlier prompts. n is brought within
// ±MaxScroll; 0 does nothing. Scrolling counts as who being active there,
// as typing does.
func (m *Core) APIScroll(who, id string, n int) error {
	if m.Quitting {
		return ErrAPIQuitting
	}
	ref, prog, err := m.apiRunning(id)
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	up := n > 0
	n = min(max(n, -n), MaxScroll)
	w, h := prog.Size()
	switch {
	case !prog.AltScreen():
		return errors.New("the program does not scroll: it is not on the alternate screen, so read has all it shows")
	case !prog.ReportsMouse(term.WheelEvent(up, w, h)):
		return errors.New("the program does not scroll: it does not take the mouse wheel")
	}
	turn := func() {
		w, h := prog.Size() // who's size, if it took it just now
		for range n {
			prog.SendMouse(term.WheelEvent(up, w, h))
		}
	}
	if who == "" {
		turn()
		return nil
	}
	m.Views.Act(ref.Pane, who, time.Now(), prog, turn)
	return nil
}

// APIAnswer answers question id, yes or no.
func (m *Core) APIAnswer(id int, yes bool) (Cmd, error) {
	if m.Quitting {
		return nil, ErrAPIQuitting
	}
	if !slices.ContainsFunc(m.Asking, func(q *Question) bool { return q.ID == id }) {
		return nil, fmt.Errorf("there is no question %d, or it has been answered", id)
	}
	answer := RunDeclined
	if yes {
		answer = AnswerAllowed
	}
	return m.Answer(id, answer), nil
}

// RepoPlaces are where nerdr looks for repositories to make a tab in, as
// core's config says now: read with Mu held, for Scan to look in away
// from it, as the disk can be slow.
type RepoPlaces struct {
	root, worktrees string
	dirs            []string
}

// RepoPlaces are where the repositories are looked for now. Mu is held.
func (m *Core) RepoPlaces() RepoPlaces {
	return RepoPlaces{root: m.Root, worktrees: m.Cfg.WorktreesDir, dirs: slices.Clone(m.Cfg.RepoDirs)}
}

// Scan finds the repositories in p, on the disk: not with Mu held.
func (p RepoPlaces) Scan() []workspace.Repo {
	all, _ := workspace.ScanAll(p.root, p.worktrees, p.dirs)
	return all
}

// APIRepos are the repositories nerdr found, all, to make a tab in.
func APIRepos(all []workspace.Repo) []APIRepo {
	out := []APIRepo{}
	for _, r := range all {
		out = append(out, APIRepo{Name: r.Name, Path: r.Path})
	}
	return out
}

// APINewTab is a tab the API asks for: on a new branch Branch in the
// repositories named Repos, with agent Agent (the first there is when "")
// and Model, given Task.
type APINewTab struct {
	Repos  []string `json:"repos"`
	Branch string   `json:"branch"`
	Agent  string   `json:"agent,omitempty"`
	Model  string   `json:"model,omitempty"`
	Task   string   `json:"task,omitempty"`
}

// APIMakeTab starts making the tab q asks for, in the repositories all,
// which RepoPlaces.Scan found; the tab comes, or a notice of why it did
// not, once its worktrees are made.
func (m *Core) APIMakeTab(q APINewTab, all []workspace.Repo) (Cmd, error) {
	if m.Quitting {
		return nil, ErrAPIQuitting
	}
	if m.TabLimitReached() {
		return nil, errors.New("the tab limit (max_tabs) is reached")
	}
	if err := workspace.ValidBranch(q.Branch); err != nil {
		return nil, err
	}
	if len(q.Repos) == 0 {
		return nil, errors.New("name at least one repository")
	}
	avail := m.Available()
	k := agents.Kind(strings.ToLower(q.Agent))
	if k == "" && len(avail) > 0 {
		k = avail[0]
	}
	if !slices.Contains(avail, k) {
		return nil, fmt.Errorf("there is no agent %q here; those there are: %s", k, m.KindList())
	}
	if q.Model != "" && !agents.TakesModel(k) {
		return nil, fmt.Errorf("nerdr does not know how to give %s a model", k)
	}
	var repos []workspace.Repo
	for _, name := range q.Repos {
		i := slices.IndexFunc(all, func(r workspace.Repo) bool { return r.Name == name })
		if i < 0 {
			var names []string
			for _, r := range all {
				names = append(names, r.Name)
			}
			return nil, fmt.Errorf("there is no repository %q; those there are: %s", name, strings.Join(names, ", "))
		}
		if !slices.ContainsFunc(repos, func(r workspace.Repo) bool { return r.Path == all[i].Path }) {
			repos = append(repos, all[i])
		}
	}
	return m.MakeTab(nil, q.Branch, k, q.Model, strings.TrimSpace(q.Task), false, repos), nil
}

// APISeen is the user looking at agent id: what it did is seen, and its
// messages, held back since the user typed to it, go on.
func (m *Core) APISeen(id string) error {
	a, err := m.apiAgentByID(id)
	if err != nil {
		return err
	}
	SeeAgent{A: m.Ref(a)}.Do(m)
	return nil
}

// APIWake wakes the agents of tab id, put to sleep while it was not looked
// at.
func (m *Core) APIWake(id string) (Cmd, error) {
	if m.Quitting {
		return nil, ErrAPIQuitting
	}
	t := TabList(m.Tabs).Find(id)
	if t == nil {
		return nil, fmt.Errorf("there is no tab %q", id)
	}
	return WakeTab{T: m.Ref(t.Owner())}.Do(m), nil
}

// apiPane is the pane of the agent or terminal id, or why there is none.
func (m *Core) apiPane(id string) (*PaneTab, error) {
	if a := TabList(m.Tabs).Find(id); a != nil {
		if p := a.PaneOf(); p != nil {
			return p, nil
		}
		return nil, fmt.Errorf("the agent %q has no pane", id)
	}
	if _, p := TabList(m.Tabs).FindPane(id); p != nil && p.Kind == PaneShell {
		return p, nil
	}
	return nil, fmt.Errorf("there is no agent or terminal %q", id)
}

// APISize is the size of the program of the agent or terminal id, columns
// and rows; 0, 0 when none runs there.
func (m *Core) APISize(id string) (int, int) {
	p, err := m.apiPane(id)
	if err != nil {
		return 0, 0
	}
	if prog := m.Program(ProgramRef{Pane: m.PaneRef(p)}); prog != nil {
		return prog.Size()
	}
	return 0, 0
}

// APIView has the API's viewer who show the agent or terminal id at w×h,
// brought within the sizes a view may claim, for ViewTTL unless it says so
// again (see views.go), and is the size its program has now: who's when
// it leads, else another viewer's.
func (m *Core) APIView(who, id string, w, h int) (int, int, error) {
	if m.Quitting {
		return 0, 0, ErrAPIQuitting
	}
	if w <= 0 || h <= 0 {
		return 0, 0, errors.New("give the cols and rows the view shows")
	}
	p, err := m.apiPane(id)
	if err != nil {
		return 0, 0, err
	}
	w, h = ClampView(w, h)
	ref := m.PaneRef(p)
	var prog sized
	if pr := m.Program(ProgramRef{Pane: ref}); pr != nil {
		prog = pr
	}
	m.Views.ShowApply(ref, who, w, h, ViewTTL, time.Now(), prog, false)
	if pw, ph := m.APISize(id); pw > 0 {
		return pw, ph, nil
	}
	w, h, _ = m.Views.Size(ref, time.Now())
	return w, h, nil
}

// APIUnview takes who's claim on the size of the agent or terminal id
// away, the size of the claims left holding.
func (m *Core) APIUnview(who, id string) error {
	p, err := m.apiPane(id)
	if err != nil {
		return err
	}
	ref := m.PaneRef(p)
	if m.Views.Drop(ref, who) {
		m.applyView(ref)
	}
	return nil
}

// APIActive counts who as active in the agent or terminal id, typing to
// it, if it shows it: its size is the program's from now.
func (m *Core) APIActive(who, id string) {
	p, err := m.apiPane(id)
	if err != nil {
		return
	}
	ref := m.PaneRef(p)
	m.Views.Act(ref, who, time.Now(), m.paneProgram(ref), nil)
}

// APIViewerGone takes away the claims of who, whose connection closed:
// what it asked was done before this comes, so nothing more of its can.
func (m *Core) APIViewerGone(who string) {
	m.viewerGone(who)
	m.Views.Forget(who)
}
