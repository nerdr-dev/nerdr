package core

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/term"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// Changing a tab's repositories: adding some, each in a worktree of its
// own, and deleting the worktrees of others. The client asks git first
// what the change would do (RepoChange), and asks the user; then core
// makes it (ChangeRepos).

func init() { registerRequests(ChangeRepos{}, CheckRepoChange{}) }

// CheckRepoChange asks git what adding repositories Add to tab T, and
// deleting its worktrees Remove, would do: told as a RepoCheckedMsg of
// Job.
type CheckRepoChange struct {
	Job    string
	T      TabRef
	Add    []workspace.Repo
	Remove []workspace.Worktree
}

// RepoCheckedMsg is what CheckRepoChange of Job found: the change, or why
// it cannot be made.
type RepoCheckedMsg struct {
	Job    string
	Change *RepoChange
	Err    string
}

func (q CheckRepoChange) Do(m *Core) Cmd {
	t := m.ByRef(q.T)
	if t == nil || len(t.Worktrees) == 0 {
		return nil
	}
	nest := len(q.Add) > 0 && len(t.Worktrees) == 1 && SamePath(t.Worktrees[0].Path, t.Dir)
	dir, branch := t.Dir, t.Worktrees[0].Branch
	return Later(func() Event {
		c := &RepoChange{Remove: q.Remove, Nest: nest}
		if len(q.Add) > 0 {
			plan, err := workspace.PlanAdd(dir, branch, q.Add)
			if err != nil {
				return RepoCheckedMsg{Job: q.Job, Err: err.Error()}
			}
			c.Add = plan
		}
		c.Dirty, c.Unpushed = workspace.Dirty(q.Remove), workspace.Unpushed(q.Remove)
		return RepoCheckedMsg{Job: q.Job, Change: c}
	})
}

// RepoChange is the repositories to add to a tab, and the worktrees to
// delete; what the worktrees deleted hold that is not committed, or not
// pushed. Nest is set when the tab's one worktree is its folder, to be
// moved into it before another goes beside it.
type RepoChange struct {
	Add             workspace.Plan
	Remove          []workspace.Worktree
	Dirty, Unpushed []string
	Nest            bool
}

// Doing says what c is about to do.
func (c RepoChange) Doing() string {
	var parts []string
	if c.Nest {
		parts = append(parts, "moving the worktree into the tab's folder")
	}
	if n := len(c.Add.Items); n > 0 {
		parts = append(parts, fmt.Sprintf("adding %d repositor%s", n, YIes(n)))
	}
	if n := len(c.Remove); n > 0 {
		parts = append(parts, fmt.Sprintf("deleting %d worktree%s", n, pluralS(n)))
	}
	return strings.Join(parts, ", ") + "…"
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ChangeRepos makes change C to tab T's repositories, once the user has
// allowed the new repositories' setup.
type ChangeRepos struct {
	T TabRef
	C RepoChange
}

func (q ChangeRepos) Do(m *Core) Cmd {
	t := m.ByRef(q.T)
	if m.TabIndex(t) < 0 {
		return nil
	}
	if len(q.C.Add.Items) == 0 {
		return m.ChangeRepos(t, q.C)
	}
	var repos []string
	for _, it := range q.C.Add.Items {
		repos = append(repos, it.Repo.Path)
	}
	return m.AskSetup(repos, func() Cmd {
		if m.TabIndex(t) < 0 {
			return nil
		}
		return m.ChangeRepos(t, q.C)
	})
}

// ReposChangedMsg is tab T's worktrees once a change was made, as far as
// it went: Err says where it stopped.
type ReposChangedMsg struct {
	T        *Tab
	Wts      []workspace.Worktree
	Added    []workspace.Worktree
	Removed  []workspace.Worktree
	Nested   *workspace.Worktree
	Restart  []*Tab
	Warnings []string
	Err      error
}

// ReposDoneMsg tells the client tab T's repositories changed: Text says
// how it went.
type ReposDoneMsg struct {
	T    TabRef
	Text string
}

// ChangeRepos makes change c to t's worktrees in the background. A tab
// whose worktree is moved has every program in it stopped first: its
// agents, started again once it is done, and its terminals, closed.
func (m *Core) ChangeRepos(t *Tab, c RepoChange) Cmd {
	if m.TabIndex(t) < 0 {
		return nil
	}
	if why := m.ReposOff(t); why != "" {
		// Changed or closed since the dialog asked git: what it found
		// may no longer be so.
		m.Toast(t.Name + ": " + why)
		return nil
	}
	t.ReposBusy = true
	var progs []*term.Pane
	var restart []*Tab
	if c.Nest {
		for _, a := range append([]*Tab{t}, t.ExtraAgents()...) {
			if a.Pane != nil {
				progs = append(progs, a.Pane)
				a.Pane = nil
			}
			a.Light = StStarting
			restart = append(restart, a)
		}
		for _, p := range append([]*PaneTab{}, t.Panes()...) {
			if p.Kind == PaneShell {
				if p.Shell != nil {
					progs = append(progs, p.Shell)
				}
				m.DropPane(t, p)
			}
		}
		m.Tell(PanesMsg{})
	}
	m.Toast(t.Name + ": " + c.Doing())
	wts := append([]workspace.Worktree(nil), t.Worktrees...)
	allowed := SetupAllowedFile()
	force := len(c.Dirty) > 0
	remove := append([]workspace.Worktree(nil), c.Remove...)
	return func() Event {
		for _, p := range progs {
			p.Close()
		}
		out := ReposChangedMsg{T: t, Wts: wts, Restart: restart}
		if c.Nest {
			w, err := workspace.Nest(wts[0])
			if err != nil {
				out.Err = err
				return out
			}
			// One let go of in the same change is deleted where it is now.
			for i := range remove {
				if SamePath(remove[i].Path, wts[0].Path) {
					remove[i] = w
				}
			}
			out.Wts[0], out.Nested = w, &w
		}
		if len(c.Add.Items) > 0 {
			plan := c.Add
			warnings, err := plan.Sync()
			out.Warnings = append(out.Warnings, warnings...)
			var made []workspace.Worktree
			if err == nil {
				made, err = plan.Execute()
			}
			if err != nil {
				out.Err = err
				return out
			}
			out.Warnings = append(out.Warnings, workspace.Prepare(made, plan.SetupIn(), allowed, func(string) {})...)
			out.Wts = append(out.Wts, made...)
			out.Added = made
		}
		for _, w := range remove {
			if err := workspace.Remove("", []workspace.Worktree{w}, force); err != nil {
				out.Err = err
				continue
			}
			out.Wts = slices.DeleteFunc(out.Wts, func(x workspace.Worktree) bool { return SamePath(x.Path, w.Path) })
			out.Removed = append(out.Removed, w)
		}
		return out
	}
}

// OnReposChanged puts t's worktrees as they are now in place, and brings
// what goes by them up to date: the group, the skills, and its agents,
// which are told. The client is told how it went.
func (m *Core) OnReposChanged(msg ReposChangedMsg) Cmd {
	t := msg.T
	t.ReposBusy = false
	var cmds []Cmd
	live := m.TabIndex(t) >= 0
	for _, a := range msg.Restart {
		if live && m.Live(a) {
			cmds = append(cmds, m.Start(a, true))
		}
	}
	if !live {
		return Batch(cmds...)
	}
	before := len(t.Worktrees)
	t.Worktrees = msg.Wts
	TrustWorktrees(t)
	for _, a := range t.ExtraAgents() {
		a.Worktrees = t.Worktrees
	}
	switch {
	case len(t.Worktrees) > 1 && before < 2:
		t.Group = m.RememberGroup(t.Dir, "", t.Worktrees)
	case len(t.Worktrees) < 2:
		t.Group = ""
	}
	// F1 is pinned to the worktrees again, F2 shows their branches, and
	// F4 looks for the skills where the agent starts.
	t.Info.Stale, t.Info.StalePR = true, true
	m.LookAtSkillsSoon()
	m.InstallProjectSkills(t)
	if note := msg.Note(t); note != "" {
		for _, a := range append([]*Tab{t}, t.ExtraAgents()...) {
			m.AddMessage(a, note, true)
		}
	}
	m.SaveSession()
	m.FindOverlaps()
	text := msg.Done()
	switch {
	case msg.Err != nil:
		text = msg.Err.Error()
	case len(msg.Warnings) > 0:
		text = strings.Join(msg.Warnings, "; ")
	}
	cmds = append(cmds, m.Tell(ReposDoneMsg{T: m.Ref(t), Text: t.Name + ": " + text}), m.RefreshInfo())
	return Batch(cmds...)
}

// Done says what the change did.
func (msg ReposChangedMsg) Done() string {
	var parts []string
	for _, w := range msg.Added {
		parts = append(parts, "added "+filepath.Base(w.Repo))
	}
	for _, w := range msg.Removed {
		parts = append(parts, "removed "+filepath.Base(w.Repo)+", its branch kept")
	}
	if len(msg.Restart) > 0 {
		parts = append(parts, "the agent"+pluralS(len(msg.Restart))+" started again")
	}
	return strings.Join(parts, "; ")
}

// Note is what t's agents are told of the change: it cannot see it
// otherwise, and its NERDR_WORKTREES is as it was when it started.
func (msg ReposChangedMsg) Note(t *Tab) string {
	if len(msg.Added) == 0 && len(msg.Removed) == 0 && msg.Nested == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("nerdr: the repositories of this tab have changed.")
	if w := msg.Nested; w != nil {
		fmt.Fprintf(&b, " The worktree of %s moved to %s; you now start in %s, the folder above it.", filepath.Base(w.Repo), w.Path, t.Dir)
	}
	for _, w := range msg.Added {
		fmt.Fprintf(&b, " Added %s: its worktree is %s, on branch %s.", filepath.Base(w.Repo), w.Path, w.Branch)
	}
	for _, w := range msg.Removed {
		fmt.Fprintf(&b, " Removed %s: its worktree %s is deleted, its branch %s kept.", filepath.Base(w.Repo), w.Path, w.Branch)
	}
	b.WriteString(" The tab's worktrees are now:")
	for _, w := range t.Worktrees {
		b.WriteString(" " + w.Path)
	}
	b.WriteString(". Nothing to do about it unless your task needs it.")
	return b.String()
}
