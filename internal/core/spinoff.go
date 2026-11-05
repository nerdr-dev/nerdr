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
}

// OtherRepos are the repositories nerdr has that are none of have.
func (m *Core) OtherRepos(have []SpinoffRepo) []workspace.Repo {
	all, _ := workspace.ScanAll(m.Root, m.Cfg.WorktreesDir, m.Cfg.RepoDirs)
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
// named, else those its commits are in, else all of them.
func SpinoffRepos(wts []workspace.Worktree, pt spinoff.Part) []bool {
	on := make([]bool, len(wts))
	some := false
	for i, w := range wts {
		on[i] = slices.ContainsFunc(pt.Repos, func(r string) bool { return RepoNamed(w, strings.TrimSpace(r)) })
		some = some || on[i]
	}
	if !some && len(pt.Commits) > 0 && len(wts) > 1 {
		for i, w := range wts {
			on[i] = slices.ContainsFunc(pt.Commits, func(c string) bool {
				_, err := workspace.ResolveCommit(w.Path, c)
				return err == nil
			})
			some = some || on[i]
		}
	}
	if !some {
		for i := range on {
			on[i] = true
		}
	}
	return on
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
		if o.Tab == "" || o.Start != "" {
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
		m.AddMessageFrom(to, spinoff.Report(by[from], branch), true, by[from][0].Part.Sandboxed)
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
	group = m.RememberGroup(dir, group, wts)
	t := &Tab{AgentState: Agent{Tab: state.Tab{
		ID:        NewTabID(),
		Name:      name,
		Dir:       dir,
		Agent:     kind,
		Model:     model,
		Worktrees: wts,
		Group:     group,
	}}}
	TrustWorktrees(t)
	m.Tabs = append(m.Tabs, t)
	m.AddMessageFrom(t, task, true, sandboxed) // before the start, which may take it
	cmd := m.Start(t, false)
	m.SaveSession()
	return t, Batch(cmd, m.Client.Resize())
}

// onSpinoff takes the parts an agent sent.
func (m *Core) OnSpinoff(r hooks.Report) Cmd {
	t := m.AgentByHook(r.Pane)
	if t == nil || t.ReviewOf != nil {
		return nil
	}
	// An agent beside the tab's own splits off from the same worktree:
	// the parts are the tab's to go through.
	sender, from, confined := t, t.ID, m.Confined(r, t)
	t = t.Owner()
	if t.Spinoff == nil {
		t.Spinoff = &spinoff.Pending{}
	}
	// One plan is MaxParts at most; plans sent one after another wait
	// together, kept with the session, and are no more than one either:
	// the user goes through each.
	dropped := 0
	for _, pt := range r.Spinoff {
		if len(t.Spinoff.Next) >= spinoff.MaxParts {
			dropped++
			continue
		}
		pt.From, pt.Sandboxed = from, confined
		t.Spinoff.Next = append(t.Spinoff.Next, pt)
	}
	if dropped > 0 {
		m.Toast(fmt.Sprintf("%s: %d part(s) to spin off left out: %d already wait", t.Name, dropped, spinoff.MaxParts))
		m.AddMessage(sender, fmt.Sprintf("nerdr: %d of the parts you sent were left out: %d parts already wait for the user to go through. "+
			"Send them again once the user has been through those.", dropped, spinoff.MaxParts), true)
	}
	m.SaveSession()
	cmd := m.Client.Detect() // the light turns red at once
	if t == m.Front() && m.Client.DialogClosed() {
		return Batch(cmd, m.Client.OpenSpinoff(t))
	}
	return cmd
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
		if err := workspace.ValidBranch(name); err != nil {
			res.Err = err
			return res
		}
		plan, err := workspace.MakePlan(wtRoot, name, repos)
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
			return res
		}
		res.Warnings, err = plan.Sync()
		if err == nil {
			res.Wts, err = plan.Execute()
		}
		if err != nil {
			res.Err = err
			return res
		}
		res.Plan, res.Outcome.Dir = plan, plan.Dir
		// Made ready before the agent's commits are brought in: setup runs
		// the code it finds as the user, and theirs is the agent's to run.
		res.Warnings = append(res.Warnings, workspace.Prepare(res.Wts, plan.SetupIn(), allowed, func(string) {})...)
		if reuse {
			for _, w := range res.Wts {
				if !own[filepath.Clean(w.Repo)] {
					continue
				}
				if _, err := workspace.Pick(w, pt.Commits); err != nil {
					res.Outcome.PickFailed = err.Error()
				}
			}
		}
		return res
	}
}
