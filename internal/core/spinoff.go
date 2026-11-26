package core

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/spinoff"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// SpinoffRepo is a repository a part can be made in.
type SpinoffRepo struct {
	workspace.Repo
	// Label is what the dialog calls it: its folder in the tab for one of
	// the tab's, else its name.
	Label string
	// Own is set for one of the tab's: the part's commits can be brought
	// into it, and the new worktree shares the repository with the tab's.
	Own bool
	// For a part made on another machine than the tab's, one of the tab's
	// is the repository with its origin there: From is its path on the
	// tab's machine, Origin the origin. Path is "" when the machine has
	// none, and CloneInto is where it would be cloned, or Err why it
	// cannot be.
	From, Origin, CloneInto, Err string
}

// OtherRepos are the repositories nerdr has that are none of have.
func (m *Core) OtherRepos(have []SpinoffRepo) []workspace.Repo {
	var all []workspace.Repo
	if m.Copy {
		// Those of another machine's core are on its disk, not this one's:
		// its last look at them.
		all = slices.Clone(m.Scan.Repos)
	} else {
		all, _ = workspace.ScanAll(m.Root, m.Cfg.WorktreesDir, m.Cfg.RepoDirs)
	}
	var out []workspace.Repo
	for _, r := range all {
		r.Path = filepath.Clean(r.Path)
		if !slices.ContainsFunc(have, func(h SpinoffRepo) bool { return h.Path == r.Path }) {
			out = append(out, r)
		}
	}
	return out
}

// RepoName is what worktree w is called among a tab's: its folder in the
// tab's, which is its repository's name.
func RepoName(w workspace.Worktree) string { return filepath.Base(w.Path) }

// RepoNamed reports whether w is the repository called name: by its
// folder in the tab, or the repository's own.
func RepoNamed(w workspace.Worktree, name string) bool {
	return name == RepoName(w) || name == filepath.Base(w.Repo)
}

// SpinoffRepos is which of wts part pt is for at first: those the agent
// named, else those its commits are in (InRepos, as core found them), else
// all of them. It reads no disk: a client of a core elsewhere calls it,
// where the worktrees are not.
func SpinoffRepos(wts []workspace.Worktree, pt spinoff.Part) []bool {
	on := make([]bool, len(wts))
	some := false
	for _, names := range [][]string{pt.Repos, pt.InRepos} {
		for i, w := range wts {
			on[i] = slices.ContainsFunc(names, func(r string) bool { return RepoNamed(w, strings.TrimSpace(r)) })
			some = some || on[i]
		}
		if some {
			break
		}
	}
	if !some {
		for i := range on {
			on[i] = true
		}
	}
	return on
}

// commitRepos are, for a part that names no repositories, the folders of
// those of wts its commits are in: what SpinoffRepos picks at first. It is
// worked out here, where the worktrees are, as the part comes; the full
// ids nerdr spinoff sent are quick to look up.
func commitRepos(wts []workspace.Worktree, pt spinoff.Part) []string {
	if len(pt.Repos) > 0 || len(pt.Commits) == 0 || len(wts) < 2 {
		return nil
	}
	var out []string
	for _, w := range wts {
		if slices.ContainsFunc(pt.Commits, func(c string) bool {
			_, err := workspace.ResolveCommit(w.Path, c)
			return err == nil
		}) {
			out = append(out, RepoName(w))
		}
	}
	return out
}

// UnknownRepos are those of names that are none of wts.
func UnknownRepos(wts []workspace.Worktree, names []string) []string {
	var out []string
	for _, n := range names {
		n = strings.TrimSpace(n)
		if !slices.ContainsFunc(wts, func(w workspace.Worktree) bool { return RepoNamed(w, n) }) {
			out = append(out, n)
		}
	}
	return out
}

// SpinoffMsg is the result of making the tab for a part.
type SpinoffMsg struct {
	T        *Tab        // the tab that sent the plan
	Kind     agents.Kind // the agent to run in the new tab
	Group    string      // the group of the new tab, if it has several repositories
	Outcome  spinoff.Outcome
	Plan     workspace.Plan
	Wts      []workspace.Worktree
	Warnings []string
	// Err is set when nothing was made, for the user to put right in the
	// dialog: a branch name taken, say.
	Err error
}

// SpinoffStartWait is the longest the report on the parts waits for the
// agents of the tabs made to take their prompts up: one slow to start, or
// asleep, does not keep the agent that sent them waiting for ever.
const SpinoffStartWait = 2 * time.Minute

// SpinoffStart is what has become of the agent of the tab with the given
// id, made for a part, and its prompt, or "" while it is yet to take the
// prompt up.
func (m *Core) SpinoffStart(id string) string {
	i := slices.IndexFunc(m.Tabs, func(x *Tab) bool { return x.ID == id })
	if i < 0 {
		return spinoff.StartClosed
	}
	switch nt := m.Tabs[i]; {
	case nt.Begun:
		return spinoff.StartBegun
	case nt.QueuePaused && nt.Unsent:
		return spinoff.StartUnsent
	case nt.QueuePaused:
		return spinoff.StartStalled
	case nt.Light == StExited:
		return spinoff.StartExited
	}
	return ""
}

// reportWhenBegun tells the agents that sent t's parts what became of
// them, once each made tab's agent has taken its prompt up, or stopped,
// or been waited for long enough. Told sooner, an agent that looked for
// itself found the tabs made last still starting, and took it that their
// prompts were lost.
func (m *Core) ReportWhenBegun(t *Tab, now time.Time) {
	if len(t.SpinoffReport) == 0 {
		return
	}
	if t.SpinoffSince.IsZero() {
		t.SpinoffSince = now // since nerdr started, after a restart
	}
	late := now.Sub(t.SpinoffSince) >= SpinoffStartWait
	done := t.SpinoffReport
	for i := range done {
		o := &done[i]
		// One made on another machine is that core's: this one cannot
		// see it start.
		if o.Tab == "" || o.Start != "" || o.Machine != "" {
			continue
		}
		if o.Start = m.SpinoffStart(o.Tab); o.Start == "" {
			if !late {
				return
			}
			o.Start = spinoff.StartSlow
		}
	}
	branch := ""
	if len(t.Worktrees) > 0 {
		branch = t.Worktrees[0].Branch
	}
	t.SpinoffReport = nil
	m.ReportSpinoff(t, done, branch)
}

// reportSpinoff tells each agent that sent parts of t's what became of
// its own, and of no other's: an agent outside a sandbox is not given
// what one in a sandbox sent. Parts from before nerdr kept the sender go
// to t's own agent.
func (m *Core) ReportSpinoff(t *Tab, done []spinoff.Outcome, branch string) {
	var order []string
	by := map[string][]spinoff.Outcome{}
	for _, o := range done {
		if _, ok := by[o.Part.From]; !ok {
			order = append(order, o.Part.From)
		}
		by[o.Part.From] = append(by[o.Part.From], o)
	}
	for _, from := range order {
		to := t
		if from != "" {
			to = nil
			for _, a := range append([]*Tab{t}, t.ExtraAgents()...) {
				if a.ID == from {
					to = a
				}
			}
		}
		if to == nil {
			continue // the agent is gone
		}
		sandboxed := slices.ContainsFunc(by[from], func(o spinoff.Outcome) bool { return o.Part.Sandboxed })
		m.AddMessageFrom(to, spinoff.Report(by[from], branch), true, sandboxed)
	}
}

// addTabBeside opens a tab for a part split off from another, in the
// group named group if it spans several repositories, and stays where the
// user is: there may be more parts to ask about. task, if any, is its
// agent's first message.
func (m *Core) AddTabBeside(name, dir, group string, wts []workspace.Worktree, kind agents.Kind, task string) (*Tab, Cmd) {
	return m.AddTabWith(name, dir, group, wts, kind, "", task, false)
}

// addTabWith is addTabBeside, its agent started with model in place of
// the config's, if any, and task marked as from a sandboxed agent when
// sandboxed is set.
func (m *Core) AddTabWith(name, dir, group string, wts []workspace.Worktree, kind agents.Kind, model, task string, sandboxed bool) (*Tab, Cmd) {
	return m.addTab(AddTab{ID: NewTabID(), Name: name, Dir: dir, Group: group, Wts: wts, Kind: kind, Model: model, Task: task, Sandboxed: sandboxed})
}

// newSessionTab is the session's part of a new tab.
func newSessionTab(id, name, dir string, kind agents.Kind, model, group string, wts []workspace.Worktree) state.Tab {
	return state.Tab{ID: id, Name: name, Dir: dir, Agent: kind, Model: model, Worktrees: wts, Group: group}
}

// onSpinoff takes the parts an agent sent.
func (m *Core) OnSpinoff(r hooks.Report) Cmd {
	reply := Answerer(r)
	t := m.AgentByHook(r.Pane)
	if t == nil || t.ReviewOf != nil {
		reply(hooks.Answer{Err: "this agent is not one nerdr takes parts to split off from"})
		return nil
	}
	// An agent beside the tab's own splits off from the same worktree:
	// the parts are the tab's to go through.
	if tooLate(r) {
		// Told nerdr did not answer, the agent has not the parts' ids to
		// send their prompts by: it sends the plan again.
		return nil
	}
	sender, from, confined := t, t.ID, m.Confined(r, t)
	t.Preparing = ""
	t = t.Owner()
	if t.Spinoff == nil {
		t.Spinoff = &spinoff.Pending{}
	}
	// One plan is MaxParts at most; plans sent one after another wait
	// together, kept with the session, and are no more than one either:
	// the user goes through each.
	dropped := 0
	var ids []string
	for _, pt := range r.Spinoff {
		if len(t.Spinoff.Next) >= spinoff.MaxParts {
			dropped++
			continue
		}
		pt.From, pt.Sandboxed = from, confined
		pt.ID = NewTabID() // what its prompt comes by, if after (OnSpinoffPrompt)
		pt.InRepos = commitRepos(t.Worktrees, pt)
		t.Spinoff.Next = append(t.Spinoff.Next, pt)
		ids = append(ids, pt.ID+"\t"+pt.Branch)
	}
	reply(hooks.Answer{Text: strings.Join(ids, "\n")})
	if dropped > 0 {
		m.Toast(fmt.Sprintf("%s: %d part(s) to spin off left out: %d already wait", t.Name, dropped, spinoff.MaxParts))
		m.AddMessage(sender, fmt.Sprintf("nerdr: %d of the parts you sent were left out: %d parts already wait for the user to go through. "+
			"Send them again once the user has been through those.", dropped, spinoff.MaxParts), true)
	}
	m.SaveSession()
	cmd := m.DetectAll(time.Now()) // the light turns red at once
	if t == m.Front() && m.Client.DialogClosed() {
		return Batch(cmd, m.Tell(SpinoffAskMsg{T: m.Ref(t)}))
	}
	return cmd
}

// OnSpinoffPrompt takes the prompt of a part the agent sent without one,
// by the id nerdr gave the part (r.Part), while the user is yet to go
// through it. The dialog that waits for it goes on.
func (m *Core) OnSpinoffPrompt(r hooks.Report) Cmd {
	reply := Answerer(r)
	sender := m.AgentByHook(r.Pane)
	if sender == nil {
		reply(hooks.Answer{Err: "this agent is not in a nerdr tab nerdr knows"})
		return nil
	}
	t := sender.Owner()
	if t.Spinoff != nil {
		for i := range t.Spinoff.Next {
			pt := &t.Spinoff.Next[i]
			if pt.ID != r.Part || pt.From != sender.ID {
				continue
			}
			switch {
			case m.Confined(r, sender) && !pt.Sandboxed:
				// The agent was switched, or put in a sandbox, since it
				// sent the part: what it writes now is not to go to an
				// agent outside one.
				reply(hooks.Answer{Err: "you run in a sandbox, and the part " + pt.Branch + " was sent from outside one: " +
					"its prompt is not taken from you. Send the plan again"})
				return nil
			case pt.Prompt != "":
				// Its tab may be being made with it already.
				reply(hooks.Answer{Err: "nerdr already has the prompt for " + pt.Branch + ", and keeps it: " +
					"a prompt is sent once, and not replaced"})
				return nil
			}
			pt.Prompt = r.Text
			m.SaveSession()
			reply(hooks.Answer{Text: "nerdr has the prompt for " + pt.Branch + "."})
			return m.Tell(SpinoffPromptMsg{T: m.Ref(t)})
		}
	}
	reply(hooks.Answer{Err: "no part of yours with the id " + r.Part + " waits for the user: they have been through it already " +
		"(nerdr tells you what became of it), or it is not an id nerdr gave you for a part"})
	return nil
}

// spinoffCreate makes part pt's worktrees and tab, in the background.
// elsewhere is set when none of repos is the tab's own, and own says, by
// repository path, which are: the part's commits are brought into those
// alone, though a fork elsewhere may have them too.
func (m *Core) SpinoffCreate(t *Tab, pt spinoff.Part, kind agents.Kind, name string, reuse, elsewhere bool, group string, repos []workspace.Repo, own map[string]bool) Cmd {
	t.SpinoffBusy = "making " + name + "…"
	wtRoot := m.WtRoot
	allowed := SetupAllowedFile()
	return func() Event {
		res := SpinoffMsg{T: t, Kind: kind, Group: group,
			Outcome: spinoff.Outcome{Part: pt, Branch: name, Agent: string(kind), Reused: reuse, Elsewhere: elsewhere}}
		var pick func(workspace.Worktree) error
		if reuse {
			pick = func(w workspace.Worktree) error {
				if !own[filepath.Clean(w.Repo)] {
					return nil
				}
				_, err := workspace.Pick(w, pt.Commits)
				return err
			}
		}
		makePart(&res, wtRoot, allowed, repos, pick)
		return res
	}
}

// makePart makes the branch res.Outcome.Branch and its worktrees in repos
// under wtRoot, for part res.Outcome.Part, and gets them ready with the
// setup allowed in the file allowed; then, with pick set, has it bring the
// part's commits into each worktree. What it made, and why it did not, go
// in res: Err when nothing was made, Outcome.PickFailed when a pick did
// not apply.
func makePart(res *SpinoffMsg, wtRoot, allowed string, repos []workspace.Repo, pick func(workspace.Worktree) error) {
	name, pt := res.Outcome.Branch, res.Outcome.Part
	if err := workspace.ValidBranch(name); err != nil {
		res.Err = err
		return
	}
	plan, err := workspace.MakePlan(wtRoot, name, repos)
	// An unpinned base is one the agent that sent the part could have moved.
	plan.Sandboxed = pt.Sandboxed
	if err == nil {
		for _, it := range plan.Items {
			switch it.Mode {
			case "local":
				err = fmt.Errorf("%s: branch %s already exists; pick another name", it.Repo.Name, name)
			case "remote":
				err = fmt.Errorf("%s: origin already has a branch %s; pick another name", it.Repo.Name, name)
			}
		}
	}
	if err != nil {
		res.Err = err
		return
	}
	res.Warnings, err = plan.Sync()
	if err == nil {
		res.Wts, err = plan.Execute()
	}
	if err != nil {
		res.Err = err
		return
	}
	res.Plan, res.Outcome.Dir = plan, plan.Dir
	// Made ready before the agent's commits are brought in: setup runs
	// the code it finds as the user, and theirs is the agent's to run.
	res.Warnings = append(res.Warnings, workspace.Prepare(res.Wts, plan.SetupIn(), allowed, func(string) {})...)
	if pick == nil {
		return
	}
	for _, w := range res.Wts {
		if err := pick(w); err != nil {
			res.Outcome.PickFailed = err.Error()
		}
	}
}
