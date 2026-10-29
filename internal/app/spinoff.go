package app

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

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// An agent asked to split its work up sends the parts with `nerdr spinoff`
// (see internal/spinoff). They are kept on its tab, whose light is red
// until the user has been through them, and asked about one at a time in
// the spinoff dialog, shown when the user is in that tab. Each part made
// becomes a tab beside it, on a new branch in the same repositories, its
// agent given the part's prompt; when none are left and the agents of the
// tabs made have taken their prompts up, the agent that sent them is told
// what became of each.

// onSpinoff takes the parts an agent sent.
func (m *Model) onSpinoff(r hooks.Report) tea.Cmd {
	t := m.agentByHook(r.Pane)
	if t == nil || t.reviewOf != nil {
		return nil
	}
	// An agent beside the tab's own splits off from the same worktree:
	// the parts are the tab's to go through.
	sender, from, confined := t, t.ID, m.confined(r, t)
	t = t.owner()
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
		m.toast(fmt.Sprintf("%s: %d part(s) to spin off left out: %d already wait", t.Name, dropped, spinoff.MaxParts))
		m.addMessage(sender, fmt.Sprintf("nerdr: %d of the parts you sent were left out: %d parts already wait for the user to go through. "+
			"Send them again once the user has been through those.", dropped, spinoff.MaxParts), true)
	}
	m.saveSession()
	cmd := m.detect() // the light turns red at once
	if t == m.cur() && m.dialog == nil {
		return tea.Batch(cmd, m.openSpinoff(t))
	}
	return cmd
}

// spinoffDialog asks about the next part of t's plan: the branch to make
// for it, whether it starts with the commits that already have its work,
// and the agent to run in it.
type spinoffDialog struct {
	t      *tab
	branch textinput.Model
	// agents are the agents to pick from; agentIdx the one picked, t's own
	// to start with, and kept from one part to the next.
	agents   []agents.Kind
	agentIdx int
	// rewrite is set when the part's commits are to be written anew
	// rather than started from.
	rewrite bool
	// showPrompt is set while the prompt is shown in place of the summary,
	// promptTop its first line.
	showPrompt bool
	promptTop  int
	// repos are the repositories the new tab can be made in: the tab's,
	// those of others the agent named, and, while others is set, every
	// one nerdr has. repoOn says which the new tab gets a worktree in;
	// repoAt is the one space turns on or off while onRepos, when ↑↓ go
	// through them rather than the agents below them, and repoNote says
	// what of the agent's choice could not be followed.
	repos    []spinoffRepo
	repoOn   []bool
	repoAt   int
	onRepos  bool
	others   bool
	repoNote string
	err      string
}

// spinoffRepo is a repository a part can be made in.
type spinoffRepo struct {
	workspace.Repo
	// label is what the dialog calls it: its folder in the tab for one of
	// the tab's, else its name.
	label string
	// own is set for one of the tab's: the part's commits can be brought
	// into it, and the new worktree shares the repository with the tab's.
	own bool
}

// openSpinoff opens the dialog for t's next part, if it has one.
func (m *Model) openSpinoff(t *tab) tea.Cmd {
	if t.Spinoff == nil || len(t.Spinoff.Next) == 0 {
		return nil
	}
	d := &spinoffDialog{t: t}
	d.branch = textinput.New()
	d.branch.Prompt = "branch: "
	d.branch.CharLimit = 100
	if !d.reset(m) {
		return m.spinoffNoAgent(t)
	}
	m.dialog = d
	return d.branch.Focus()
}

// spinoffNoAgent gives up t's first part, sent by an agent in a sandbox,
// with no agent set sandboxed to run it. The next part comes back to the
// dialog, which may give up that one too: one a part.
func (m *Model) spinoffNoAgent(t *tab) tea.Cmd {
	if d, ok := m.dialog.(*spinoffDialog); ok && d.t == t {
		m.dialog = nil
	}
	m.toast(t.Name + ": an agent in a sandbox sent a part to spin off, and no agent is set sandboxed to run it")
	return m.spinoffNext(t, spinoff.Outcome{Part: t.Spinoff.Next[0],
		Err: "no agent is set sandboxed = true to run it in, and you run in a sandbox"}, false)
}

// reset fills the dialog in for the part now first in line: its branch,
// and the agents it may run, the one picked kept where it can be. It
// reports false when an agent in a sandbox sent the part and none set
// sandboxed is there to run it. With no agent at all, the dialog says so
// when the part is to be made.
func (d *spinoffDialog) reset(m *Model) bool {
	pt := d.t.Spinoff.Next[0]
	picked := d.t.Agent
	if d.agentIdx < len(d.agents) {
		picked = d.agents[d.agentIdx]
	}
	d.agents, d.agentIdx = m.spinoffKinds(pt), 0
	if len(d.agents) == 0 && pt.Sandboxed && len(agents.Available(m.cfg.Agents)) > 0 {
		return false
	}
	if i := slices.Index(d.agents, picked); i >= 0 {
		d.agentIdx = i
	}
	d.branch.SetValue(pt.Branch)
	d.branch.CursorEnd()
	d.rewrite, d.showPrompt, d.promptTop, d.err = false, false, 0, ""
	d.repos, d.others, d.repoNote = nil, false, ""
	for _, w := range d.t.Worktrees {
		d.repos = append(d.repos, spinoffRepo{Repo: workspace.Repo{Name: filepath.Base(w.Repo), Path: filepath.Clean(w.Repo)},
			label: repoName(w), own: true})
	}
	d.repoOn = spinoffRepos(d.t.Worktrees, pt)
	// Those the agent named that the tab does not have are looked for
	// among the rest; an agent in a sandbox gets the tab's alone, as with
	// nerdr tab new, and the user can still tick another.
	var unknown, confined []string
	if names := unknownRepos(d.t.Worktrees, pt.Repos); len(names) > 0 {
		// Named none of the tab's, and found another: not the tab's as
		// well.
		noneOwn := len(names) == len(pt.Repos)
		all := m.otherRepos(d.repos)
		for _, n := range names {
			i := slices.IndexFunc(all, func(r workspace.Repo) bool { return r.Name == n })
			switch {
			case i < 0:
				unknown = append(unknown, n)
			case pt.Sandboxed:
				confined = append(confined, n)
			default:
				if noneOwn {
					clear(d.repoOn)
					noneOwn = false
				}
				d.repos = append(d.repos, spinoffRepo{Repo: all[i], label: all[i].Name})
				d.repoOn = append(d.repoOn, true)
			}
		}
	}
	switch {
	case len(unknown) > 0:
		d.repoNote = "the agent named " + strings.Join(unknown, ", ") + ", which nerdr does not have"
	case len(confined) > 0:
		d.repoNote = "the agent, in a sandbox, named " + strings.Join(confined, ", ") + ": ctrl+r to tick it yourself"
	}
	d.repoAt, d.onRepos = max(slices.Index(d.repoOn, true), 0), d.pickRepos()
	return true
}

// otherRepos are the repositories nerdr has that are none of have.
func (m *Model) otherRepos(have []spinoffRepo) []workspace.Repo {
	all, _ := workspace.ScanAll(m.root, m.cfg.WorktreesDir, m.cfg.RepoDirs)
	var out []workspace.Repo
	for _, r := range all {
		r.Path = filepath.Clean(r.Path)
		if !slices.ContainsFunc(have, func(h spinoffRepo) bool { return h.Path == r.Path }) {
			out = append(out, r)
		}
	}
	return out
}

// showOthers puts every repository nerdr has in the list, unticked, or
// takes those not ticked out of it again.
func (d *spinoffDialog) showOthers(m *Model) {
	d.err = ""
	if d.others {
		d.others = false
		for i := len(d.repos) - 1; i >= 0; i-- {
			if !d.repos[i].own && !d.repoOn[i] {
				d.repos, d.repoOn = slices.Delete(d.repos, i, i+1), slices.Delete(d.repoOn, i, i+1)
			}
		}
		d.repoAt = min(d.repoAt, len(d.repos)-1)
		d.onRepos = d.onRepos && d.pickRepos()
		return
	}
	more := m.otherRepos(d.repos)
	if len(more) == 0 {
		d.err = "nerdr has no other repository: repo_dirs says where it looks"
		return
	}
	d.others, d.onRepos, d.repoAt = true, true, len(d.repos)
	for _, r := range more {
		d.repos = append(d.repos, spinoffRepo{Repo: r, label: r.Name})
		d.repoOn = append(d.repoOn, false)
	}
}

// ownTicked reports whether one of the tab's own repositories is ticked:
// the part's commits can be brought in only there.
func (d *spinoffDialog) ownTicked() bool {
	for i, r := range d.repos {
		if r.own && d.repoOn[i] {
			return true
		}
	}
	return false
}

// othersTicked reports whether a repository not the tab's is ticked.
func (d *spinoffDialog) othersTicked() bool {
	for i, r := range d.repos {
		if !r.own && d.repoOn[i] {
			return true
		}
	}
	return false
}

// repoName is what worktree w is called among a tab's: its folder in the
// tab's, which is its repository's name.
func repoName(w workspace.Worktree) string { return filepath.Base(w.Path) }

// repoNamed reports whether w is the repository called name: by its
// folder in the tab, or the repository's own.
func repoNamed(w workspace.Worktree, name string) bool {
	return name == repoName(w) || name == filepath.Base(w.Repo)
}

// spinoffRepos is which of wts part pt is for at first: those the agent
// named, else those its commits are in, else all of them.
func spinoffRepos(wts []workspace.Worktree, pt spinoff.Part) []bool {
	on := make([]bool, len(wts))
	some := false
	for i, w := range wts {
		on[i] = slices.ContainsFunc(pt.Repos, func(r string) bool { return repoNamed(w, strings.TrimSpace(r)) })
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

// unknownRepos are those of names that are none of wts.
func unknownRepos(wts []workspace.Worktree, names []string) []string {
	var out []string
	for _, n := range names {
		n = strings.TrimSpace(n)
		if !slices.ContainsFunc(wts, func(w workspace.Worktree) bool { return repoNamed(w, n) }) {
			out = append(out, n)
		}
	}
	return out
}

// pickRepos reports whether the dialog asks which repositories: when
// there are several to pick from.
func (d *spinoffDialog) pickRepos() bool { return len(d.repos) > 1 }

func (d *spinoffDialog) part() spinoff.Part { return d.t.Spinoff.Next[0] }

func (d *spinoffDialog) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	if m.tabIndex(d.t) < 0 || d.t.Spinoff == nil || len(d.t.Spinoff.Next) == 0 {
		m.dialog = nil
		return nil
	}
	if d.t.spinoffBusy != "" {
		if k := msg.String(); k == "esc" || k == "ctrl+c" {
			m.dialog = nil
		}
		return nil // wait for it
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		// Skips every part left, rather than putting them off: put off,
		// they kept the light red with no way back to them but leaving the
		// tab and coming back. Skipping loses nothing: the agent is told,
		// keeps the work, and can send it again.
		return m.spinoffSkip(d.t, true)
	case "tab", "shift+tab":
		if len(d.part().Commits) > 0 {
			d.rewrite = !d.rewrite
		}
		return nil
	case "up":
		// One list: the repositories, then the agents under them.
		switch {
		case d.onRepos:
			d.repoAt = max(d.repoAt-1, 0)
		case d.agentIdx == 0 && d.pickRepos():
			d.onRepos, d.repoAt = true, len(d.repoOn)-1
		default:
			d.agentIdx = max(d.agentIdx-1, 0)
		}
		return nil
	case "down":
		switch {
		case d.onRepos && d.repoAt < len(d.repoOn)-1:
			d.repoAt++
		case d.onRepos:
			d.onRepos = len(d.agents) == 0
		default:
			d.agentIdx = min(d.agentIdx+1, max(len(d.agents)-1, 0))
		}
		return nil
	case "ctrl+o":
		d.showPrompt, d.promptTop = !d.showPrompt, 0
		return nil
	case "ctrl+r":
		d.showOthers(m)
		return nil
	case "pgup":
		d.promptTop = max(d.promptTop-10, 0)
		return nil
	case "pgdown":
		d.promptTop += 10
		return nil
	case "ctrl+x":
		return m.spinoffSkip(d.t, false)
	case "enter":
		return m.spinoffMake(d)
	case " ":
		// No branch name has a space: it is for the repositories.
		if d.onRepos {
			d.repoOn[d.repoAt] = !d.repoOn[d.repoAt]
			return nil
		}
	}
	var cmd tea.Cmd
	d.branch, cmd = d.branch.Update(msg)
	return cmd
}

// spinoffMsg is the result of making the tab for a part.
type spinoffMsg struct {
	t        *tab        // the tab that sent the plan
	kind     agents.Kind // the agent to run in the new tab
	group    string      // the group of the new tab, if it has several repositories
	outcome  spinoff.Outcome
	plan     workspace.Plan
	wts      []workspace.Worktree
	warnings []string
	// err is set when nothing was made, for the user to put right in the
	// dialog: a branch name taken, say.
	err error
}

// spinoffMake makes the branch and worktrees for the part asked about, in
// the background.
func (m *Model) spinoffMake(d *spinoffDialog) tea.Cmd {
	if m.tabLimitReached() {
		d.err = "the tab limit (max_tabs) is reached: close a tab first, or skip this part"
		return nil
	}
	if len(d.agents) == 0 {
		d.err = "found no agent on PATH"
		return nil
	}
	t, pt, kind := d.t, d.part(), d.agents[d.agentIdx]
	if pt.Sandboxed && !m.sandboxedKind(kind) {
		d.err = "an agent in a sandbox sent this part: pick one set sandboxed"
		return nil
	}
	name := strings.TrimSpace(d.branch.Value())
	reuse := len(pt.Commits) > 0 && !d.rewrite && d.ownTicked()
	var repos []workspace.Repo
	own := map[string]bool{}
	for i, r := range d.repos {
		if d.repoOn[i] {
			repos = append(repos, r.Repo)
			own[r.Path] = r.own
		}
	}
	// In the tab's own repositories alone, it goes in the tab's group;
	// with others, in the one nerdr gives it.
	group, elsewhere := t.Group, !d.ownTicked()
	if d.othersTicked() {
		group = ""
	}
	switch {
	case len(t.Worktrees) == 0:
		d.err = "this tab has no worktrees to make the new one beside"
		return nil
	case len(repos) == 0:
		d.err = "pick at least one repository: ↑↓ to it, space ticks it"
		return nil
	}
	d.err = ""
	var paths []string
	for _, r := range repos {
		paths = append(paths, r.Path)
	}
	return m.askRepoSetup(paths, func(m *Model) tea.Cmd {
		return m.spinoffCreate(t, pt, kind, name, reuse, elsewhere, group, repos, own)
	})
}

// spinoffCreate makes part pt's worktrees and tab, in the background.
// elsewhere is set when none of repos is the tab's own, and own says, by
// repository path, which are: the part's commits are brought into those
// alone, though a fork elsewhere may have them too.
func (m *Model) spinoffCreate(t *tab, pt spinoff.Part, kind agents.Kind, name string, reuse, elsewhere bool, group string, repos []workspace.Repo, own map[string]bool) tea.Cmd {
	t.spinoffBusy = "making " + name + "…"
	wtRoot := m.wtRoot
	allowed := setupAllowedFile()
	return func() tea.Msg {
		res := spinoffMsg{t: t, kind: kind, group: group,
			outcome: spinoff.Outcome{Part: pt, Branch: name, Agent: string(kind), Reused: reuse, Elsewhere: elsewhere}}
		if err := workspace.ValidBranch(name); err != nil {
			res.err = err
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
			res.err = err
			return res
		}
		res.warnings, err = plan.Sync()
		if err == nil {
			res.wts, err = plan.Execute()
		}
		if err != nil {
			res.err = err
			return res
		}
		res.plan, res.outcome.Dir = plan, plan.Dir
		// Made ready before the agent's commits are brought in: setup runs
		// the code it finds as the user, and theirs is the agent's to run.
		res.warnings = append(res.warnings, workspace.Prepare(res.wts, plan.SetupIn(), allowed, func(string) {})...)
		if reuse {
			for _, w := range res.wts {
				if !own[filepath.Clean(w.Repo)] {
					continue
				}
				if _, err := workspace.Pick(w, pt.Commits); err != nil {
					res.outcome.PickFailed = err.Error()
				}
			}
		}
		return res
	}
}

// spinoffMade takes the result of making a part's tab: the tab is opened
// beside its parent, the part is done, and the next one is asked about.
func (m *Model) spinoffMade(msg spinoffMsg) tea.Cmd {
	t := msg.t
	t.spinoffBusy = ""
	d, shown := m.dialog.(*spinoffDialog)
	shown = shown && d.t == t
	if msg.err != nil {
		if shown {
			d.err = msg.err.Error()
		} else {
			m.toast("spinoff " + msg.outcome.Branch + ": " + msg.err.Error())
		}
		return nil
	}
	var cmds []tea.Cmd
	parent := spinoff.Parent{Name: t.Name, Dir: t.Dir}
	if len(t.Worktrees) > 0 {
		parent.Branch = t.Worktrees[0].Branch
	}
	if pt := msg.outcome.Part; pt.Sandboxed && !m.sandboxedKind(msg.kind) {
		// The config changed while the worktree was made.
		msg.outcome.Err = string(msg.kind) + " is no longer set sandboxed; the worktree is made, with no agent in it"
		m.toast("spinoff " + msg.outcome.Branch + ": " + msg.outcome.Err)
		return m.spinoffNext(t, msg.outcome, shown)
	}
	if m.tabIndex(t) >= 0 {
		nt, cmd := m.addTabWith(msg.plan.Branch, msg.plan.Dir, msg.group, msg.wts, msg.kind, "",
			spinoff.Prompt(msg.outcome.Part, parent, msg.outcome), msg.outcome.Part.Sandboxed)
		nt.Goal = msg.outcome.Part.Summary
		msg.outcome.Tab = nt.ID
		m.resizePanes()
		cmds = append(cmds, cmd)
	}
	note := "made " + msg.plan.Branch
	if msg.outcome.PickFailed != "" {
		note += ": its commits did not apply cleanly, so its agent brings them in"
	}
	if len(msg.warnings) > 0 {
		note += "; " + strings.Join(msg.warnings, "; ")
	}
	m.toast(note)
	cmds = append(cmds, m.spinoffNext(t, msg.outcome, shown))
	return tea.Batch(cmds...)
}

// spinoffSkip leaves the part asked about without a tab, or with all set,
// every part left.
func (m *Model) spinoffSkip(t *tab, all bool) tea.Cmd {
	var cmd tea.Cmd
	for t.Spinoff != nil && len(t.Spinoff.Next) > 0 {
		cmd = m.spinoffNext(t, spinoff.Outcome{Part: t.Spinoff.Next[0], Skipped: true}, true)
		if !all {
			break
		}
	}
	return cmd
}

// spinoffNext records what became of t's first part, and moves on: to the
// next part, in the dialog when it is shown, or, with none left, to
// telling the agent that sent them.
func (m *Model) spinoffNext(t *tab, o spinoff.Outcome, shown bool) tea.Cmd {
	p := t.Spinoff
	if p == nil || len(p.Next) == 0 {
		return nil
	}
	p.Done = append(p.Done, o)
	p.Next = p.Next[1:]
	if len(p.Next) > 0 {
		m.saveSession()
		if d, ok := m.dialog.(*spinoffDialog); ok && d.t == t {
			if !d.reset(m) {
				return m.spinoffNoAgent(t)
			}
			return d.branch.Focus()
		}
		if shown || (m.dialog == nil && t == m.cur()) {
			return m.openSpinoff(t)
		}
		return nil
	}
	if d, ok := m.dialog.(*spinoffDialog); ok && d.t == t {
		m.dialog = nil
	}
	t.Spinoff = nil
	t.SpinoffReport, t.spinoffSince = append(t.SpinoffReport, p.Done...), time.Time{}
	m.saveSession()
	return m.detect() // the light is the agent's again, and the report may go
}

// spinoffStartWait is the longest the report on the parts waits for the
// agents of the tabs made to take their prompts up: one slow to start, or
// asleep, does not keep the agent that sent them waiting for ever.
const spinoffStartWait = 2 * time.Minute

// reportWhenBegun tells the agents that sent t's parts what became of
// them, once each made tab's agent has taken its prompt up, or stopped,
// or been waited for long enough. Told sooner, an agent that looked for
// itself found the tabs made last still starting, and took it that their
// prompts were lost.
func (m *Model) reportWhenBegun(t *tab, now time.Time) {
	if len(t.SpinoffReport) == 0 {
		return
	}
	if t.spinoffSince.IsZero() {
		t.spinoffSince = now // since nerdr started, after a restart
	}
	late := now.Sub(t.spinoffSince) >= spinoffStartWait
	done := t.SpinoffReport
	for i := range done {
		o := &done[i]
		if o.Tab == "" || o.Start != "" {
			continue
		}
		if o.Start = m.spinoffStart(o.Tab); o.Start == "" {
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
	m.reportSpinoff(t, done, branch)
}

// spinoffStart is what has become of the agent of the tab with the given
// id, made for a part, and its prompt, or "" while it is yet to take the
// prompt up.
func (m *Model) spinoffStart(id string) string {
	i := slices.IndexFunc(m.tabs, func(x *tab) bool { return x.ID == id })
	if i < 0 {
		return spinoff.StartClosed
	}
	switch nt := m.tabs[i]; {
	case nt.begun:
		return spinoff.StartBegun
	case nt.queuePaused:
		return spinoff.StartStalled
	case nt.status == stExited:
		return spinoff.StartExited
	}
	return ""
}

// reportSpinoff tells each agent that sent parts of t's what became of
// its own, and of no other's: an agent outside a sandbox is not given
// what one in a sandbox sent. Parts from before nerdr kept the sender go
// to t's own agent.
func (m *Model) reportSpinoff(t *tab, done []spinoff.Outcome, branch string) {
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
			for _, a := range append([]*tab{t}, t.extraAgents()...) {
				if a.ID == from {
					to = a
				}
			}
		}
		if to == nil {
			continue // the agent is gone
		}
		m.addMessageFrom(to, spinoff.Report(by[from], branch), true, by[from][0].Part.Sandboxed)
	}
}

// addTabBeside opens a tab for a part split off from another, in the
// group named group if it spans several repositories, and stays where the
// user is: there may be more parts to ask about. task, if any, is its
// agent's first message.
func (m *Model) addTabBeside(name, dir, group string, wts []workspace.Worktree, kind agents.Kind, task string) (*tab, tea.Cmd) {
	return m.addTabWith(name, dir, group, wts, kind, "", task, false)
}

// addTabWith is addTabBeside, its agent started with model in place of
// the config's, if any, and task marked as from a sandboxed agent when
// sandboxed is set.
func (m *Model) addTabWith(name, dir, group string, wts []workspace.Worktree, kind agents.Kind, model, task string, sandboxed bool) (*tab, tea.Cmd) {
	group = m.rememberGroup(dir, group, wts)
	t := &tab{Tab: state.Tab{
		ID:        newTabID(),
		Name:      name,
		Dir:       dir,
		Agent:     kind,
		Model:     model,
		Worktrees: wts,
		Group:     group,
	}}
	trustWorktrees(t)
	m.tabs = append(m.tabs, t)
	m.addMessageFrom(t, task, true, sandboxed) // before the start, which may take it
	cmd := m.start(t, false)
	m.saveSession()
	return t, tea.Batch(cmd, m.resize())
}

func (d *spinoffDialog) view(m *Model, width, height int) string {
	t := d.t
	if t.Spinoff == nil || len(t.Spinoff.Next) == 0 {
		return box("Split off", []string{dim("Nothing left to ask about.")}, width)
	}
	pt := d.part()
	inner := min(max(width-10, 40), 90)
	sel := lipgloss.NewStyle().Foreground(lipgloss.Color("110")).Bold(true)
	i, n := t.Spinoff.Count()
	parentBranch := t.Name
	if len(t.Worktrees) > 0 && t.Worktrees[0].Branch != "" {
		parentBranch = t.Worktrees[0].Branch
	}
	lines := []string{dim(fmt.Sprintf("%s's agent split its work up; part %d of %d:", t.Name, i, n)), ""}
	if d.showPrompt {
		room := max(height-18, 5)
		rows := wrapLines(pt.Prompt, inner, 1<<20)
		d.promptTop = min(d.promptTop, max(len(rows)-room, 0))
		end := min(d.promptTop+room, len(rows))
		lines = append(lines, dim(fmt.Sprintf("The prompt for the new agent, lines %d–%d of %d:", d.promptTop+1, end, len(rows))))
		lines = append(lines, rows[d.promptTop:end]...)
	} else {
		lines = append(lines, wrapLines(pt.Summary, inner, 8)...)
	}
	lines = append(lines, "", d.branch.View(), "")
	if len(pt.Commits) > 0 {
		var short []string
		for _, c := range pt.Commits {
			short = append(short, c[:min(len(c), 7)])
		}
		code := "Code already written in " + parentBranch + " " + dim("("+strings.Join(short, " ")+")") + ":"
		if !d.ownTicked() {
			lines = append(lines, code, dim("  in this tab's repositories, none of them ticked: the new agent writes it anew"), "")
		} else {
			lines = append(lines, code,
				m.onClick(cursorLine(!d.rewrite, "Start the branch with its "+plural(len(pt.Commits), "commit"), sel), func(m *Model) tea.Cmd {
					d.rewrite = false
					return nil
				}),
				m.onClick(cursorLine(d.rewrite, "Write it anew from the prompt", sel), func(m *Model) tea.Cmd {
					d.rewrite = true
					return nil
				}), "")
		}
	}
	if d.pickRepos() {
		lines = append(lines, "Repositories:")
		var rows []string
		for i, r := range d.repos {
			label := "[ ] " + r.label
			if d.repoOn[i] {
				label = "[x] " + r.label
			}
			if !r.own {
				label += dim("  not in this tab")
			}
			rows = append(rows, m.onClick(cursorLine(d.onRepos && i == d.repoAt, label, sel), func(m *Model) tea.Cmd {
				d.onRepos, d.repoAt, d.repoOn[i] = true, i, !d.repoOn[i]
				return nil
			}))
		}
		if d.others {
			rows = scrollRows(rows, d.repoAt, min(len(rows)+2, max(height-26, 6)))
		}
		lines = append(lines, rows...)
		if d.repoNote != "" {
			lines = append(lines, warn("  "+d.repoNote))
		}
		lines = append(lines, "")
	} else if d.repoNote != "" {
		// With no list, what the agent asked for and did not get is to be
		// seen all the same.
		lines = append(lines, warn("Repository: "+d.repoNote), "")
	}
	if len(d.agents) == 0 {
		lines = append(lines, warn("Found no agent on PATH: claude, opencode, copilot, codex, cursor-agent, gemini or agy."), "")
	} else {
		lines = append(lines, "Agent:")
		for i, a := range d.agents {
			lines = append(lines, m.onClick(cursorLine(i == d.agentIdx, string(a), sel), func(m *Model) tea.Cmd {
				d.agentIdx, d.onRepos = i, false
				return nil
			}))
		}
		lines = append(lines, "")
	}
	where := "in the same repositories"
	switch {
	case d.othersTicked() && !d.ownTicked():
		where = "in the repositories ticked, not this tab's"
	case d.pickRepos():
		where = "in the repositories ticked"
	}
	lines = append(lines, dim(fmt.Sprintf("A new branch from origin's main %s; the agent is given a prompt of %s.", where, plural(len(pt.Prompt), "character"))))
	if t.spinoffBusy != "" {
		lines = append(lines, "", warn(t.spinoffBusy), dim("esc hides this; the tab opens when it is ready"))
	} else {
		pairs := []string{"enter", "make tab"}
		if len(pt.Commits) > 0 && d.ownTicked() {
			pairs = append(pairs, "tab", "code")
		}
		switch {
		case d.pickRepos():
			pairs = append(pairs, "↑↓", "repository or agent", "space", "tick")
		case len(d.agents) > 1:
			pairs = append(pairs, "", "↑↓ agent")
		}
		prompt := "show prompt"
		if d.showPrompt {
			prompt = "show summary"
		}
		others := "other repositories"
		if d.others {
			others = "fewer repositories"
		}
		pairs = append(pairs, "ctrl+o", prompt, "ctrl+r", others)
		if n-i > 0 {
			pairs = append(pairs, "ctrl+x", "skip", "esc", "skip all")
		} else {
			pairs = append(pairs, "esc", "skip")
		}
		lines = append(lines, "", m.keys(pairs...))
	}
	if d.err != "" {
		lines = append(lines, "", warn(d.err))
	}
	return box(fmt.Sprintf("New tab %d of %d", i, n), lines, width)
}
