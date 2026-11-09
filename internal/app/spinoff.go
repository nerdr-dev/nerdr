package app

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/spinoff"
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

// spinoffDialog asks about the next part of t's plan: the branch to make
// for it, whether it starts with the commits that already have its work,
// and the agent to run in it.
type spinoffDialog struct {
	t      *core.Tab
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
	repos    []core.SpinoffRepo
	repoOn   []bool
	repoAt   int
	onRepos  bool
	others   bool
	repoNote string
	err      string
}

// openSpinoff opens the dialog for t's next part, if it has one.
func (m *Model) openSpinoff(t *core.Tab) tea.Cmd {
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
func (m *Model) spinoffNoAgent(t *core.Tab) tea.Cmd {
	if d, ok := m.dialog.(*spinoffDialog); ok && d.t == t {
		m.dialog = nil
	}
	m.toast(t.Name + ": an agent in a sandbox sent a part to spin off, and no agent is set sandboxed to run it")
	return m.spinoffNext(t, spinoff.Outcome{Part: t.Spinoff.Next[0],
		Err: "no agent is set sandboxed = true to run it in, and you run in a sandbox"})
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
	d.agents, d.agentIdx = m.SpinoffKinds(pt), 0
	if len(d.agents) == 0 && pt.Sandboxed && len(m.Available()) > 0 {
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
		d.repos = append(d.repos, core.SpinoffRepo{Repo: workspace.Repo{Name: filepath.Base(w.Repo), Path: filepath.Clean(w.Repo)},
			Label: core.RepoName(w), Own: true})
	}
	d.repoOn = core.SpinoffRepos(d.t.Worktrees, pt)
	// Those the agent named that the tab does not have are looked for
	// among the rest; an agent in a sandbox gets the tab's alone, as with
	// nerdr tab new, and the user can still tick another.
	var unknown, confined []string
	if names := core.UnknownRepos(d.t.Worktrees, pt.Repos); len(names) > 0 {
		// Named none of the tab's, and found another: not the tab's as
		// well.
		noneOwn := len(names) == len(pt.Repos)
		all := m.OtherRepos(d.repos)
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
				d.repos = append(d.repos, core.SpinoffRepo{Repo: all[i], Label: all[i].Name})
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

// showOthers puts every repository nerdr has in the list, unticked, or
// takes those not ticked out of it again.
func (d *spinoffDialog) showOthers(m *Model) {
	d.err = ""
	if d.others {
		d.others = false
		for i := len(d.repos) - 1; i >= 0; i-- {
			if !d.repos[i].Own && !d.repoOn[i] {
				d.repos, d.repoOn = slices.Delete(d.repos, i, i+1), slices.Delete(d.repoOn, i, i+1)
			}
		}
		d.repoAt = min(d.repoAt, len(d.repos)-1)
		d.onRepos = d.onRepos && d.pickRepos()
		return
	}
	more := m.OtherRepos(d.repos)
	if len(more) == 0 {
		d.err = "nerdr has no other repository: repo_dirs says where it looks"
		return
	}
	d.others, d.onRepos, d.repoAt = true, true, len(d.repos)
	for _, r := range more {
		d.repos = append(d.repos, core.SpinoffRepo{Repo: r, Label: r.Name})
		d.repoOn = append(d.repoOn, false)
	}
}

// ownTicked reports whether one of the tab's own repositories is ticked:
// the part's commits can be brought in only there.
func (d *spinoffDialog) ownTicked() bool {
	for i, r := range d.repos {
		if r.Own && d.repoOn[i] {
			return true
		}
	}
	return false
}

// othersTicked reports whether a repository not the tab's is ticked.
func (d *spinoffDialog) othersTicked() bool {
	for i, r := range d.repos {
		if !r.Own && d.repoOn[i] {
			return true
		}
	}
	return false
}

// pickRepos reports whether the dialog asks which repositories: when
// there are several to pick from.
func (d *spinoffDialog) pickRepos() bool { return len(d.repos) > 1 }

func (d *spinoffDialog) part() spinoff.Part { return d.t.Spinoff.Next[0] }

func (d *spinoffDialog) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	if m.TabIndex(d.t) < 0 || d.t.Spinoff == nil || len(d.t.Spinoff.Next) == 0 {
		m.dialog = nil
		return nil
	}
	if d.t.SpinoffBusy != "" {
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

// spinoffMake makes the branch and worktrees for the part asked about, in
// the background.
func (m *Model) spinoffMake(d *spinoffDialog) tea.Cmd {
	if m.TabLimitReached() {
		d.err = "the tab limit (max_tabs) is reached: close a tab first, or skip this part"
		return nil
	}
	if len(d.agents) == 0 {
		d.err = "found no agent on PATH"
		return nil
	}
	t, pt, kind := d.t, d.part(), d.agents[d.agentIdx]
	if pt.Sandboxed && !m.SandboxedKind(kind) {
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
			own[r.Path] = r.Own
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
	return m.do(core.SpinoffCreate{T: m.Ref(t), Part: pt, Kind: kind, Name: name, Reuse: reuse, Elsewhere: elsewhere,
		Group: group, Repos: repos, Own: own})
}

// spinoffFailed is the tab for t's first part not made: said in the
// dialog when it is open for t, else in a toast.
func (m *Model) spinoffFailed(t *core.Tab, branch, err string) {
	if d, ok := m.dialog.(*spinoffDialog); ok && d.t == t {
		d.err = err
		return
	}
	m.toast("spinoff " + branch + ": " + err)
}

// spinoffSkip leaves the part asked about without a tab, or with all set,
// every part left.
func (m *Model) spinoffSkip(t *core.Tab, all bool) tea.Cmd {
	var cmd tea.Cmd
	for t.Spinoff != nil && len(t.Spinoff.Next) > 0 {
		cmd = m.spinoffNext(t, spinoff.Outcome{Part: t.Spinoff.Next[0], Skipped: true})
		if !all {
			break
		}
	}
	return cmd
}

// spinoffNext has core record what became of t's first part, and move on.
func (m *Model) spinoffNext(t *core.Tab, o spinoff.Outcome) tea.Cmd {
	return m.do(core.SpinoffNext{T: m.Ref(t), Outcome: o})
}

// spinoffMoved is t's first part done with, left more to go through: the
// next is asked about, in the dialog when it is shown or when nothing else
// is; with none left, the dialog closes.
func (m *Model) spinoffMoved(t *core.Tab, left int) tea.Cmd {
	d, shown := m.dialog.(*spinoffDialog)
	shown = shown && d.t == t
	if left == 0 {
		if shown {
			m.dialog = nil
		}
		return nil
	}
	if shown {
		if !d.reset(m) {
			return m.spinoffNoAgent(t)
		}
		return d.branch.Focus()
	}
	if m.dialog == nil && t == m.Cur() {
		return m.openSpinoff(t)
	}
	return nil
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
				m.onClick(cursorLine(!d.rewrite, "Start the branch with its "+core.Plural(len(pt.Commits), "commit"), sel), func(m *Model) tea.Cmd {
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
			label := "[ ] " + r.Label
			if d.repoOn[i] {
				label = "[x] " + r.Label
			}
			if !r.Own {
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
	lines = append(lines, dim(fmt.Sprintf("A new branch from origin's main %s; the agent is given a prompt of %s.", where, core.Plural(len(pt.Prompt), "character"))))
	if t.SpinoffBusy != "" {
		lines = append(lines, "", warn(t.SpinoffBusy), dim("esc hides this; the tab opens when it is ready"))
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
