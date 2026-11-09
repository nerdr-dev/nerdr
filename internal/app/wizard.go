package app

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// wizard is the new-tab dialog: pick repositories (or an existing
// worktree), name the branch, pick the agent.
type wizard struct {
	step int

	repos    []workspace.Repo
	picked   map[int]bool
	existing []workspace.Group
	cursor   int

	branch textinput.Model
	// groupName names the group of a tab spanning several repositories,
	// optional: multi when left empty. Only asked for then.
	groupName textinput.Model
	groups    []string // the names groups have already
	// task is the first thing the agent is asked to do, optional; it is
	// queued for the new tab.
	task textarea.Model
	// field is the one being typed into: fieldBranch, fieldGroup or
	// fieldTask.
	field int
	// id names the dialog in what it asks core, and core's answers by it.
	id    string
	plan  workspace.Plan
	group *workspace.Group // set when opening an existing worktree
	// fromBranch is set when group was picked at stepBranch, among the
	// picked repository's worktrees, so esc goes back there.
	fromBranch bool
	// exCursor is the row of repoExisting under the cursor, when the
	// typing is at fieldExisting.
	exCursor int
	// pulls is the picked repository's open pull requests, offered to
	// carry on with; see wizardprs.go.
	pulls wizardPRs

	// useExisting is the choice made when the branch already exists, on
	// origin or here: continue it rather than make a new one from the
	// base. The pull-request list sets it to start with, being where you
	// go to continue a branch; typed in, the new branch is offered first,
	// unless a local branch of the name has work that is not merged, which
	// a new one cannot take the place of.
	useExisting bool

	agents   []agents.Kind
	agentIdx int

	// repoDir is where a repository made or cloned here goes, "" when
	// there is no such folder; newRepo is the form for it.
	repoDir string
	newRepo newRepoForm

	// repoGiven is set when the dialog opened with the repository picked,
	// at the branch name, or with the worktree to open picked, at the
	// agent (a PR's): esc there closes it rather than going back to a
	// list that was never shown.
	repoGiven bool

	// on is the local branch a new branch starts on top of, another
	// tab's, rather than main: see openWizardOn.
	on string

	// unfetched is set when the fetch of the base branch failed: l then
	// starts from it as it was last fetched.
	unfetched bool

	// newIfNone is set when the dialog opened for a new repository: it
	// goes on to the form for one once core has found none to pick.
	newIfNone bool

	err  string
	busy string
}

const (
	stepRepos = iota
	stepBranch
	stepExisting // the branch exists: continue it, or start anew?
	stepAgent
	stepCreating
	stepNewRepo // make a repository, or clone one, to work on
)

// The fields of stepBranch.
const (
	fieldBranch   = iota
	fieldExisting // the picked repository's worktrees no tab has open
	fieldGroup
	fieldTask
	fieldPRs // the picked repository's open pull requests
)

func (m *Model) openWizard() tea.Cmd {
	if m.TabLimitReached() {
		m.openTabLimit()
		return nil
	}
	if m.offerRoom((*Model).openWizard) {
		return nil
	}
	w := &wizard{id: core.NewJobID(), picked: map[int]bool{}}
	w.scan(m)
	w.agents = m.Available()
	w.branch = textinput.New()
	w.branch.Prompt = "branch: "
	w.branch.Placeholder = "feature/my-change, or #123 for a GitHub issue"
	w.branch.CharLimit = 100
	w.groupName = textinput.New()
	w.groupName.Prompt = "group:  "
	w.groupName.Placeholder = core.MultiGroup
	w.groupName.CharLimit = 40
	// The groups there are, so a tab joins one by the same name rather
	// than starting another for a typo: → takes the one suggested (tab
	// moves between the fields).
	w.groups = m.KnownGroupNames()
	w.groupName.ShowSuggestions = true
	w.groupName.SetSuggestions(w.groups)
	w.groupName.KeyMap.AcceptSuggestion = key.NewBinding(key.WithKeys("right"))
	w.task = newTextArea("optional: the first task for the agent", min(max(m.width-16, 30), 80))
	w.task.SetHeight(4)
	w.task.Blur()
	m.dialog = w
	return m.do(core.ScanRepos{})
}

// openNewRepoDialog opens the new-tab dialog for the repository list's
// "+ new repo": at the repositories there are to pick from, which offers
// making or cloning one too, or, with none, at the form for a new one.
// Where there is no folder to make one in, the dialog says why. Whether
// there are any is as core last found, and found again now.
func (m *Model) openNewRepoDialog() tea.Cmd {
	cmd := m.openWizard()
	if w, ok := m.dialog.(*wizard); ok {
		w.newIfNone = true
		return tea.Batch(cmd, w.toNewRepo(m))
	}
	return cmd
}

// toNewRepo goes on to the form for a new repository when the dialog was
// opened for one, and core has looked and found none to pick.
func (w *wizard) toNewRepo(m *Model) tea.Cmd {
	if !w.newIfNone || w.step != stepRepos || m.Scan.At.IsZero() || w.repoDir == "" || len(w.repos) > 0 {
		return nil
	}
	w.newIfNone = false
	return w.openNewRepo(false, !m.NoGh)
}

// scan has the repositories to pick from, and the worktrees of theirs no
// tab has open, as core last found them; see core.RepoScan. Those picked
// stay picked.
func (w *wizard) scan(m *Model) {
	picked := map[string]bool{}
	for _, r := range w.pickedRepos() {
		picked[filepath.Clean(r.Path)] = true
	}
	w.repos, w.err = m.Scan.Repos, m.Scan.Err
	for p := range picked {
		w.repoListed(p)
	}
	w.picked = map[int]bool{}
	for i, r := range w.repos {
		if picked[filepath.Clean(r.Path)] {
			w.picked[i] = true
		}
	}
	// Starting inside a repository: that is the one to work on.
	if len(w.repos) == 1 {
		w.picked[0] = true
	}
	w.existing = m.ClosedGroups()
	w.repoDir = m.Scan.NewRepoDir
	w.cursor = min(w.cursor, max(w.rows()-1, 0))
}

// repoListed is the row of the repository at path, added when core's last
// look did not find it: one a menu names is there to pick, looked for
// since or not.
func (w *wizard) repoListed(path string) int {
	for i, r := range w.repos {
		if filepath.Clean(r.Path) == filepath.Clean(path) {
			return i
		}
	}
	w.repos = append(slices.Clone(w.repos), workspace.Repo{Name: filepath.Base(path), Path: filepath.Clean(path)})
	return len(w.repos) - 1
}

// rescanned has the dialog offer the repositories core found again,
// while they are being picked.
func (w *wizard) rescanned(m *Model) tea.Cmd {
	if w.step != stepRepos {
		return nil
	}
	w.scan(m)
	return w.toNewRepo(m)
}

// noAgentHint says what to do when no agent is found, the first thing the
// dialog says then: nothing can be started without one.
func noAgentHint(m *Model) []string {
	return []string{
		warn("Found no agent on PATH: claude, opencode, copilot, codex, cursor-agent, gemini or agy."),
		"Install one, or name the command of yours in " + dim(m.ConfigPath()) + ":",
		"  " + dim(`[agents.my-agent]`) + "  " + dim(`command = ["my-agent"]`),
	}
}

// openWizardFor opens the new-tab dialog with the repository at repo
// picked, at the branch name: the menus' "new tab in <repo>".
func (m *Model) openWizardFor(repo string) tea.Cmd {
	if !m.TabLimitReached() && m.offerRoom(func(m *Model) tea.Cmd { return m.openWizardFor(repo) }) {
		return nil
	}
	cmd := m.openWizard()
	w, ok := m.dialog.(*wizard)
	if !ok {
		return cmd
	}
	i := w.repoListed(repo)
	w.picked = map[int]bool{i: true}
	w.cursor = i
	w.step = stepBranch
	w.repoGiven = true
	return tea.Batch(cmd, w.focusBranch(), w.loadPRs(m))
}

// openWizardOn opens the dialog for a new tab whose branch starts on top
// of branch, of repository repo: work that builds on another tab's before
// it is merged, with a pull request against that one's.
func (m *Model) openWizardOn(repo, branch string) tea.Cmd {
	cmd := m.openWizardFor(repo)
	if w, ok := m.dialog.(*wizard); ok && w.step == stepBranch {
		w.on = branch
	}
	return cmd
}

// rows is how many entries the first step lists: repositories, then
// existing worktrees.
func (w *wizard) rows() int { return len(w.repos) + len(w.existing) }

func (w *wizard) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	if w.step == stepCreating {
		// Fetching and the repository's setup can take minutes; the other
		// tabs need not wait for them.
		if k == "esc" || k == "ctrl+c" {
			m.dialog = nil
			m.toast("making " + w.plan.Branch + " in the background: its tab opens beside yours when it is ready")
		}
		return nil
	}
	if w.busy != "" {
		if w.step == stepNewRepo && (k == "esc" || k == "ctrl+c") {
			// A clone can take minutes too.
			m.dialog = nil
			m.toast(strings.TrimSuffix(w.busy, "…") + " in the background: it is listed here when it is done")
		}
		return nil // wait for it
	}
	if k == "ctrl+c" {
		m.dialog = nil
		return nil
	}
	switch w.step {
	case stepRepos:
		switch k {
		case "up", "k":
			w.cursor = max(w.cursor-1, 0)
		case "down", "j":
			w.cursor = min(w.cursor+1, max(w.rows()-1, 0))
		case " ", "x":
			if w.cursor < len(w.repos) {
				w.picked[w.cursor] = !w.picked[w.cursor]
			}
		case "a":
			all := len(w.pickedRepos()) < len(w.repos)
			for i := range w.repos {
				w.picked[i] = all
			}
		case "n", "c":
			if w.repoDir != "" {
				return w.openNewRepo(k == "c", !m.NoGh)
			}
		case "enter":
			w.err = ""
			if len(w.agents) == 0 && w.rows() > 0 {
				w.err = "there is no agent to start: see above"
				return nil
			}
			if w.cursor >= len(w.repos) && w.cursor < w.rows() {
				g := w.existing[w.cursor-len(w.repos)]
				w.group, w.fromBranch = &g, false
				return w.toAgent(m)
			}
			if len(w.pickedRepos()) == 0 && w.cursor < len(w.repos) {
				w.picked[w.cursor] = true
			}
			if len(w.repos) == 0 {
				w.err = "there are no repositories to pick: see above"
				return nil
			}
			if len(w.pickedRepos()) == 0 {
				w.err = "pick at least one repository (space)"
				return nil
			}
			w.group, w.exCursor = nil, 0
			w.step = stepBranch
			return tea.Batch(w.focusBranch(), w.loadPRs(m))
		case "esc", "q":
			m.dialog = nil
		}
		return nil

	case stepBranch:
		if w.field == fieldPRs {
			if cmd, done := w.prsKey(m, msg); done {
				return cmd
			}
		}
		if w.field == fieldExisting {
			switch k {
			case "up", "k":
				if w.exCursor == 0 {
					if n := len(w.shownPRs()); n > 0 {
						w.pulls.cursor = n - 1
						return w.focusField(fieldPRs)
					}
					return w.focusField(fieldBranch)
				}
				w.exCursor--
				return nil
			case "down", "j":
				w.exCursor = min(w.exCursor+1, len(w.repoExisting())-1)
				return nil
			case "enter":
				return w.openExisting(m, w.exCursor)
			}
		}
		if k == "down" && w.field == fieldBranch && len(w.shownPRs()) > 0 {
			w.pulls.cursor = 0
			return w.focusField(fieldPRs)
		}
		if k == "down" && w.field == fieldBranch && len(w.repoExisting()) > 0 {
			return w.focusField(fieldExisting)
		}
		switch k {
		case "esc":
			if w.repoGiven {
				m.dialog = nil
				return nil
			}
			w.step = stepRepos
			w.err = ""
			w.focusBranch()
			w.branch.Blur()
			return nil
		case "tab", "shift+tab":
			fields := w.fields()
			for i, f := range fields {
				if f == w.field {
					d := 1
					if k == "shift+tab" {
						d = len(fields) - 1
					}
					return w.focusField(fields[(i+d)%len(fields)])
				}
			}
			return w.focusField(fieldBranch)
		case "enter":
			name := strings.TrimSpace(w.branch.Value())
			if n, ok := issueNumber(name); ok {
				return w.lookUpIssue(m, w.pickedRepos()[0].Path, n)
			}
			w.err = ""
			w.busy = "checking…"
			q := core.MakePlan{Job: w.id, Name: name, Repos: w.pickedRepos(), On: w.on}
			// The list knows the picked repository's forks' pull requests;
			// with several picked, or no list, GitHub is asked.
			if fork, isFork, listed := w.forkPRNamed(name); listed {
				q.Fork = &core.ForkPR{Is: isFork, Number: fork.PR.Number, Owner: fork.PR.Fork}
			}
			return m.do(q)
		}
		var cmd tea.Cmd
		switch w.field {
		case fieldExisting:
			// nothing to type into
		case fieldTask:
			w.task, cmd = w.task.Update(msg)
		case fieldGroup:
			w.groupName, cmd = w.groupName.Update(msg)
		default:
			before := w.branch.Value()
			w.branch, cmd = w.branch.Update(msg)
			if w.branch.Value() != before {
				// A name of your own: the choice to continue a branch
				// made by picking a pull request is not yours for it.
				w.useExisting = false
			}
		}
		return cmd

	case stepNewRepo:
		return w.newRepoKey(m, msg)

	case stepExisting:
		switch k {
		case "up", "k", "down", "j", "tab":
			w.useExisting = !w.useExisting || !w.plan.CanStartAnew()
		case "n":
			if !w.plan.CanStartAnew() {
				return nil
			}
			w.useExisting = false
			return w.key(m, keyMsg("enter"))
		case "u":
			w.useExisting = true
			return w.key(m, keyMsg("enter"))
		case "enter":
			return w.toAgent(m)
		case "esc":
			w.step = stepBranch
			return w.focusBranch()
		}
		return nil

	case stepAgent:
		switch k {
		case "up", "k":
			w.agentIdx = max(w.agentIdx-1, 0)
		case "down", "j":
			w.agentIdx = min(w.agentIdx+1, max(len(w.agents)-1, 0))
		case "1", "2", "3":
			if i := int(k[0] - '1'); i < len(w.agents) {
				w.agentIdx = i
			}
		case "esc":
			w.err, w.unfetched = "", false
			switch {
			case w.group != nil && w.fromBranch:
				w.step = stepBranch
			case w.group != nil && w.repoGiven:
				m.dialog = nil // opened for it, from a PR
			case w.group != nil:
				w.step = stepRepos
			case len(w.plan.ExistingItems()) > 0:
				w.step = stepExisting
			default:
				w.step = stepBranch
				return w.focusBranch()
			}
		case "l":
			if !w.unfetched || len(w.agents) == 0 {
				return nil
			}
			w.err, w.unfetched = "", false
			plan := w.chosenPlan()
			plan.Unfetched = true
			return w.create(m, plan)
		case "enter":
			if len(w.agents) == 0 {
				return nil
			}
			if w.group != nil {
				m.dialog = nil
				_, cmd := m.addTab(w.group.Name, w.group.Dir, "", w.group.Worktrees, w.agents[w.agentIdx], true, "")
				return cmd
			}
			plan := w.chosenPlan()
			var repos []string
			for _, it := range plan.Items {
				repos = append(repos, it.Repo.Path)
			}
			return m.askSetup(repos, func(m *Model) tea.Cmd { return w.create(m, plan) })
		}
		return nil
	}
	return nil
}

// create has core make plan's worktrees, and then opens the tab.
func (w *wizard) create(m *Model, plan workspace.Plan) tea.Cmd {
	w.step = stepCreating
	w.busy = "creating worktree" + pluralS(len(w.plan.Items)) + "…"
	return m.do(core.MakeWorktrees{Job: w.id, Plan: plan, Kind: w.agents[w.agentIdx], Task: w.task.Value(), Group: w.chosenGroup()})
}

// issueNumber reads "#123" (or "123" alone is a branch name, not an issue).
func issueNumber(s string) (int, bool) {
	rest, ok := strings.CutPrefix(s, "#")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil && n > 0
}

// lookUpIssue has core read issue n of the repository at dir, to fill
// in the branch and the task from.
func (w *wizard) lookUpIssue(m *Model, dir string, n int) tea.Cmd {
	w.err = ""
	w.busy = "looking up issue #" + strconv.Itoa(n) + "…"
	return m.do(core.LookUpIssue{Job: w.id, Dir: dir, N: n})
}

// handleIssue fills in the branch and the task from an issue.
func (w *wizard) handleIssue(msg core.IssueMsg) tea.Cmd {
	w.busy = ""
	if w.step != stepBranch {
		return nil
	}
	if msg.Err != "" {
		w.err = "issue: " + msg.Err
		return nil
	}
	w.branch.SetValue(msg.Issue.BranchName())
	w.branch.CursorEnd()
	w.task.SetValue(msg.Issue.Task())
	w.err = ""
	return nil
}

// repoExisting is the worktrees no tab has open of the one repository
// picked, offered beside a new branch for it. With several picked there
// are none: a tab on one of them would not span the others. One on a
// branch the open pull requests list is left out: it is offered there,
// with the pull request's title and whether your review is asked.
func (w *wizard) repoExisting() []workspace.Group {
	picked := w.pickedRepos()
	if len(picked) != 1 {
		return nil
	}
	repo := filepath.Clean(picked[0].Path)
	listed := map[string]bool{}
	for _, e := range w.pulls.entries {
		listed[e.Branch()] = true
	}
	var out []workspace.Group
	for _, g := range w.existing {
		all := true
		for _, wt := range g.Worktrees {
			all = all && filepath.Clean(wt.Repo) == repo && !listed[wt.Branch]
		}
		if all {
			out = append(out, g)
		}
	}
	return out
}

// openExisting opens a tab on row i of repoExisting, by way of picking
// the agent.
func (w *wizard) openExisting(m *Model, i int) tea.Cmd {
	gs := w.repoExisting()
	if i < 0 || i >= len(gs) {
		return nil
	}
	if len(w.agents) == 0 {
		w.err = "there is no agent to start: see " + m.ConfigPath()
		return nil
	}
	g := gs[i]
	w.group, w.fromBranch, w.exCursor, w.err = &g, true, i, ""
	return w.toAgent(m)
}

// toAgent goes on to picking the agent, or, with only one to pick, starts
// it.
func (w *wizard) toAgent(m *Model) tea.Cmd {
	w.step = stepAgent
	if len(w.agents) == 1 {
		return w.key(m, keyMsg("enter"))
	}
	return nil
}

// handlePlan takes the result of checking the branch.
func (w *wizard) handlePlan(m *Model, msg core.PlanMsg) tea.Cmd {
	w.busy = ""
	if w.step != stepBranch {
		return nil
	}
	if msg.Err != "" {
		w.err = msg.Err
		return nil
	}
	w.plan, w.err = msg.Plan, ""
	if !w.plan.CanStartAnew() {
		w.useExisting = true
	}
	w.branch.Blur()
	if len(w.plan.ExistingItems()) > 0 {
		w.step = stepExisting
		return nil
	}
	return w.toAgent(m)
}

// chosenPlan is the plan as it will be carried out: with a new branch from
// the base where the branch already exists, unless continuing it was
// chosen.
func (w *wizard) chosenPlan() workspace.Plan {
	if w.useExisting {
		return w.plan
	}
	return w.plan.FromBase()
}

// existingLines describes the existing branch in each repository that has
// it, on origin or here, for the choice between continuing it and starting
// anew.
func (w *wizard) existingLines(m *Model, sel lipgloss.Style) []string {
	items := w.plan.ExistingItems()
	b := w.plan.Branch
	local := false
	for _, it := range items {
		local = local || it.Mode == "local"
	}
	var lines []string
	if local {
		lines = append(lines, warn("Branch "+b+" already exists here."), "")
	} else {
		lines = append(lines, warn("origin/"+b+" already exists."), "")
	}
	allMerged, diverges := true, false
	for _, it := range items {
		var what []string
		if local {
			where := "local branch"
			if it.Mode == "remote" {
				where = "on origin"
			}
			what = append(what, where)
		}
		if it.PR > 0 {
			what = append(what, fmt.Sprintf("PR #%d %s", it.PR, strings.ToLower(it.PRState)))
		}
		switch {
		case it.Base == "":
		case it.PRState == "MERGED" && it.Mode == "local" && it.AfterPR > 0:
			what = append(what, fmt.Sprintf("%d commit%s after the merged PR", it.AfterPR, pluralS(it.AfterPR)))
		case it.PRState == "MERGED" && it.Mode == "local" && !it.PRHasTip:
			what = append(what, "commits the merged PR may not have")
		case it.PRState == "MERGED":
		case it.Ahead == 0:
			what = append(what, "nothing in it that "+it.Base+" lacks")
		default:
			what = append(what, fmt.Sprintf("%d commit%s not in %s", it.Ahead, pluralS(it.Ahead), it.Base))
		}
		if it.Mode == "local" && it.Behind > 0 {
			what = append(what, fmt.Sprintf("%d commit%s behind %s", it.Behind, pluralS(it.Behind), it.Base))
		}
		if !it.Merged() {
			allMerged = false
		}
		if it.Mode == "remote" && it.Ahead > 0 {
			diverges = true
		}
		lines = append(lines, "  "+it.Repo.Name+": "+strings.Join(what, " · "))
	}
	base := items[0].Base
	cont := "Continue origin/" + b
	if local {
		cont = "Continue existing " + b
	}
	lines = append(lines, "")
	if w.plan.CanStartAnew() {
		lines = append(lines, m.onClick(cursorLine(!w.useExisting, "New branch "+b+" from "+base, sel), func(m *Model) tea.Cmd { return w.key(m, keyMsg("n")) }))
	} else {
		lines = append(lines, dim("  New branch "+b+" from "+base+" (not while "+b+" has work that is not merged)"))
	}
	lines = append(lines, m.onClick(cursorLine(w.useExisting, cont, sel), func(m *Model) tea.Cmd { return w.key(m, keyMsg("u")) }), "")
	switch {
	case !w.plan.CanStartAnew():
		lines = append(lines, dim("The local "+b+" has work that is not merged, which a new branch"),
			dim("of the name would throw away: continue it, or go back (esc) and"),
			dim("pick another name."))
	case allMerged:
		lines = append(lines, dim("It is merged, so a new branch is most likely what you want."))
		if local {
			lines = append(lines, dim("The new branch takes the place of the local one."))
		}
	default:
		lines = append(lines, dim("It has work that is not merged: continue it, unless it is"),
			dim("someone else's, or you mean to start over."))
	}
	if !w.useExisting && diverges {
		lines = append(lines, dim("A push is refused while origin/"+b+" has commits the new branch"),
			dim("lacks: delete it on GitHub first, or pick another name."))
	}
	use := "use origin's"
	if local {
		use = "use existing"
	}
	return append(lines, "", m.keys("", "↑↓ choose", "n", "new", "u", use, "enter", "continue", "esc", "back"))
}

// handle takes the result of creating the worktrees.
func (w *wizard) handle(m *Model, msg core.WorktreesMadeMsg) tea.Cmd {
	if msg.Err != "" {
		w.err = msg.Err
		w.step = stepAgent
		w.busy = ""
		w.unfetched = msg.Unfetched
		return nil
	}
	m.dialog = nil
	return m.createdTab(msg)
}

// createdTab opens the tab for worktrees that have been made, and goes to
// it.
func (m *Model) createdTab(msg core.WorktreesMadeMsg) tea.Cmd {
	t, cmd := m.addTab(msg.Plan.Branch, msg.Plan.Dir, msg.Group, msg.Wts, msg.Kind, false, msg.Task)
	m.setUpCreated(t, msg)
	return cmd
}

// createdBehind takes the result of a wizard hidden while it was making
// the worktrees. The tab opens beside the active one without going to it:
// you are busy with something else by now, and what you type must not land
// in the new agent.
func (m *Model) createdBehind(msg core.WorktreesMadeMsg) tea.Cmd {
	if msg.Err != "" {
		m.toast("could not make " + msg.Plan.Branch + ": " + msg.Err)
		return nil
	}
	if len(m.Tabs) == 0 {
		return m.createdTab(msg) // nothing else to be busy with
	}
	id := core.NewTabID()
	cmd := m.do(core.AddTab{ID: id, Name: msg.Plan.Branch, Dir: msg.Plan.Dir, Group: msg.Group, Wts: msg.Wts,
		Kind: msg.Kind, Task: msg.Task, Goal: msg.Task})
	if t := m.ByRef(core.TabRef{Core: m.ID, Tab: id}); t != nil {
		m.toast(t.Name + " is ready in a tab of its own")
		m.setUpCreated(t, msg)
	}
	return cmd
}

// setUpCreated shows a tab made from the wizard its task, which addTab
// gave its agent.
func (m *Model) setUpCreated(t *core.Tab, msg core.WorktreesMadeMsg) {
	if strings.TrimSpace(msg.Task) != "" {
		m.resizePanes() // the panel takes rows above the agent, its goal the task
	}
	if len(msg.Warnings) > 0 {
		m.toast(t.Name + ": " + strings.Join(msg.Warnings, "; "))
	}
}

// addTab opens a tab on worktrees, in the group named group if they span
// several repositories, starts its agent and goes to it. On worktrees that
// were there before, carryOn has the agent carry on the conversation it
// had in them, if any; otherwise it starts a new one. task, if any, is its
// agent's first message.
// The tab left is not asked for its note: you are on your way to the new
// one.
//
// The group is kept for the folder: a tab opened on it again with no group
// given, from the list of existing worktrees, goes in the same one.
func (m *Model) addTab(name, dir, group string, wts []workspace.Worktree, kind agents.Kind, carryOn bool, task string) (*core.Tab, tea.Cmd) {
	id := core.NewTabID()
	cmd := m.do(core.AddTab{ID: id, Name: name, Dir: dir, Group: group, Wts: wts, Kind: kind, Task: task, Goal: task, CarryOn: carryOn})
	t := m.ByRef(core.TabRef{Core: m.ID, Tab: id})
	if t == nil {
		return nil, cmd
	}
	m.Active = m.TabIndex(t)
	m.setFocus(core.FocusAgent)
	m.saveLayout()
	return t, tea.Batch(cmd, m.selectTab(m.Active), m.resize())
}

func (w *wizard) pickedRepos() []workspace.Repo {
	var out []workspace.Repo
	for i, r := range w.repos {
		if w.picked[i] {
			out = append(out, r)
		}
	}
	return out
}

func (w *wizard) view(m *Model, width, height int) string {
	var lines []string
	// The lists that scroll when the box is taller than the screen, the
	// one to shorten first first.
	var lists []scrollList
	sel := lipgloss.NewStyle().Foreground(lipgloss.Color("110")).Bold(true)
	switch w.step {
	case stepRepos:
		if len(w.agents) == 0 {
			lines = append(lines, noAgentHint(m)...)
			lines = append(lines, "")
		}
		lines = append(lines, "Repositories in "+dim(strings.Join(append([]string{m.Root}, m.Cfg.RepoDirs...), ", "))+":", "")
		if len(w.repos) == 0 {
			lines = append(lines, warn("  None found, so there is nothing to pick."), "")
			if w.repoDir != "" {
				lines = append(lines,
					"  Make a new one ("+keyHint("n")+") or clone one ("+keyHint("c")+") into "+dim(w.repoDir)+",",
					"  or start nerdr in a folder with repositories right under it (or in")
			} else {
				lines = append(lines, "  Start nerdr in a folder with repositories right under it (or in")
			}
			lines = append(lines,
				"  one), or list the folders to look in, in "+dim(m.ConfigPath())+":", "",
				"    "+dim(`repo_dirs = ["~/code", "~/work"]`))
		}
		repoList := scrollList{start: len(lines), n: len(w.repos), cursor: w.cursor}
		for i, r := range w.repos {
			box := "[ ]"
			if w.picked[i] {
				box = "[✓]"
			}
			line := fmt.Sprintf("%s %s", box, r.Name)
			lines = append(lines, m.onClick(cursorLine(i == w.cursor, line, sel), func(m *Model) tea.Cmd {
				w.cursor = i
				return w.key(m, keyMsg(" "))
			}))
		}
		if len(w.existing) > 0 {
			lines = append(lines, "", "Or open an existing worktree in "+dim(m.WtRoot)+":", "")
			lists = append(lists, scrollList{start: len(lines), n: len(w.existing), cursor: w.cursor - len(w.repos)})
			nameW := 0
			for _, g := range w.existing {
				nameW = max(nameW, lipgloss.Width(g.Name))
			}
			nameW = min(nameW, 40)
			for j, g := range w.existing {
				var repos []string
				for _, wt := range g.Worktrees {
					repos = append(repos, filepath.Base(wt.Repo))
				}
				line := padTo(ansi.Truncate(g.Name, nameW, "…"), nameW+2) + dim(strings.Join(repos, ", "))
				row := len(w.repos) + j
				lines = append(lines, m.onClick(cursorLine(row == w.cursor, line, sel), func(m *Model) tea.Cmd {
					w.cursor = row
					return w.key(m, keyMsg("enter"))
				}))
			}
		}
		lists = append(lists, repoList)
		keys := []string{"", "↑↓ move", " ", "pick", "a", "all", "enter", "continue"}
		if w.repoDir != "" {
			keys = append(keys, "n", "new repository", "c", "clone")
		}
		lines = append(lines, "", m.keys(append(keys, "esc", "cancel")...))
	case stepNewRepo:
		lines = append(lines, w.newRepoLines(m, sel)...)
	case stepBranch:
		var names []string
		for _, r := range w.pickedRepos() {
			names = append(names, r.Name)
		}
		lines = append(lines, "New worktree for "+strings.Join(names, ", "), "",
			m.onClick(w.branch.View(), func(m *Model) tea.Cmd { return w.focusField(fieldBranch) }), "")
		if w.on != "" {
			lines = append(lines,
				"A new branch starts on top of "+sel.Render(w.on)+", with what it has not pushed;",
				dim("its changes and pull request are against "+w.on+" until that is merged."),
				dim("An existing branch is checked out as it is."))
		} else {
			lines = append(lines,
				dim("An existing local branch is checked out; otherwise a new one"),
				dim("is made from origin's default branch, asking first if origin has one."))
		}
		lines = append(lines, dim("#123 fills in the branch and the task from GitHub issue 123."))
		// The rest of the step takes some 25 rows: the list gets what is
		// left, and scrolls.
		lines = append(lines, w.prLines(m, boxInner(width), height-4-25, sel)...)
		if gs := w.repoExisting(); len(gs) > 0 {
			lines = append(lines, "", "Or open an existing worktree of "+names[0]+" "+dim("(↓)")+":")
			cur := -1
			if w.field == fieldExisting {
				cur = w.exCursor
			}
			lists = append(lists, scrollList{start: len(lines), n: len(gs), cursor: cur})
			for i, g := range gs {
				line := g.Name
				if g.Name != filepath.Base(g.Dir) {
					line += "  " + dim(filepath.Base(g.Dir))
				}
				lines = append(lines, m.onClick(cursorLine(w.field == fieldExisting && i == w.exCursor, line, sel), func(m *Model) tea.Cmd {
					return w.openExisting(m, i)
				}))
			}
		}
		if w.multi() {
			lines = append(lines, "",
				m.onClick(w.groupName.View(), func(m *Model) tea.Cmd { return w.focusField(fieldGroup) }),
				dim("The group the tab goes in, spanning several repositories; "+core.MultiGroup+" if empty."))
			if len(w.groups) > 0 {
				line := dim("existing (→ completes):")
				for _, g := range w.groups {
					line += " " + m.onClick(keyHint(g), func(m *Model) tea.Cmd {
						w.groupName.SetValue(g)
						w.groupName.CursorEnd()
						return w.focusField(fieldGroup)
					})
				}
				lines = append(lines, line)
			}
		}
		lines = append(lines, "", "Task "+dim("(sent to the agent once it has started)")+":",
			m.onClick(w.task.View(), func(m *Model) tea.Cmd { return w.focusField(fieldTask) }), "")
		if w.busy != "" {
			lines = append(lines, warn(w.busy))
		} else if w.field == fieldExisting {
			lines = append(lines, m.keys("", "↑↓ move", "enter", "open", "tab", "next field", "esc", "back"))
		} else if w.field == fieldPRs {
			lines = append(lines, m.keys("", "↑↓ move", "enter", "take it", "", "typing narrows", "tab", "next field", "esc", "back"))
		} else {
			lines = append(lines, m.keys("enter", "continue", "tab", "next field", "", core.NewLineHint, "esc", "back"))
		}
	case stepExisting:
		lines = append(lines, "Branch "+sel.Render(w.plan.Branch)+" in "+dim(w.plan.Dir), "")
		lines = append(lines, w.existingLines(m, sel)...)
	case stepAgent, stepCreating:
		if w.group != nil {
			lines = append(lines, "Open "+w.group.Name+" "+dim(w.group.Dir), "")
		} else {
			lines = append(lines, "Branch "+sel.Render(w.plan.Branch)+" in "+dim(w.plan.Dir))
			for _, d := range w.chosenPlan().Describe() {
				lines = append(lines, "  "+d)
			}
			lines = append(lines, "")
		}
		if len(w.agents) == 0 {
			lines = append(lines, noAgentHint(m)...)
		} else {
			lines = append(lines, "Agent:")
			for i, a := range w.agents {
				lines = append(lines, m.onClick(cursorLine(i == w.agentIdx, fmt.Sprintf("%d  %s", i+1, a), sel), func(m *Model) tea.Cmd {
					w.agentIdx = i
					return w.key(m, keyMsg("enter"))
				}))
			}
		}
		lines = append(lines, "")
		if w.busy != "" && w.step == stepCreating {
			lines = append(lines, warn(w.busy), dim("esc hides this; the tab opens beside yours when it is ready"))
		} else if w.unfetched {
			lines = append(lines, m.keys("", "↑↓ pick", "enter", "try again", "l", "start from the last fetched copy", "esc", "back"))
		} else {
			lines = append(lines, m.keys("", "↑↓ pick", "enter", "start", "esc", "back"))
		}
	}
	if w.err != "" {
		lines = append(lines, "", warn(w.err))
	}
	return box("New tab", fitLists(lines, lists, height-4, boxInner(width)), width)
}

// scrollList is a list among a dialog's lines, n of them from start, with
// the cursor on its row cursor (-1 when it is not in it).
type scrollList struct{ start, n, cursor int }

// fitLists shortens lists, in order, until lines fit in h rows when
// wrapped at width w, as a long path in a heading is: each shows the rows
// around its cursor, with a line above and below saying how many more
// there are, as ↑ ↓ bring them into view.
func fitLists(lines []string, lists []scrollList, h, w int) []string {
	for i, l := range lists {
		over := wrappedRows(lines, w) - h
		if over <= 0 {
			break
		}
		show := max(l.n-over, 3)
		if show >= l.n {
			continue
		}
		rows := scrollRows(lines[l.start:l.start+l.n], l.cursor, show)
		lines = append(lines[:l.start:l.start], append(rows, lines[l.start+l.n:]...)...)
		for j := i + 1; j < len(lists); j++ {
			if lists[j].start > l.start {
				lists[j].start -= l.n - len(rows)
			}
		}
	}
	return lines
}

// wrappedRows is how many rows lines take in a box w wide, wrapped as
// box wraps them.
func wrappedRows(lines []string, w int) int {
	n := 0
	wrap := lipgloss.NewStyle().Width(w)
	for _, l := range lines {
		if lipgloss.Width(l) <= w {
			n++
		} else {
			n += strings.Count(wrap.Render(l), "\n") + 1
		}
	}
	return n
}

// scrollRows is n of rows, n at least 3: those around cursor, between a
// line saying how many more there are above and one saying how many
// below, blank when there are none, so the list keeps its height.
func scrollRows(rows []string, cursor, n int) []string {
	show := n - 2
	start := max(0, min(cursor-show/2, len(rows)-show))
	top, bottom := "", ""
	if start > 0 {
		top = dim(fmt.Sprintf("  ↑ %d more", start))
	}
	if rest := len(rows) - start - show; rest > 0 {
		bottom = dim(fmt.Sprintf("  ↓ %d more", rest))
	}
	out := append([]string{top}, rows[start:start+show]...)
	return append(out, bottom)
}

// multi is whether the tab will span several repositories, and so is
// asked for its group's name.
func (w *wizard) multi() bool { return len(w.pickedRepos()) > 1 }

// fields lists the fields of stepBranch, in the order tab goes through
// them.
func (w *wizard) fields() []int {
	if w.multi() {
		return []int{fieldBranch, fieldGroup, fieldTask}
	}
	out := []int{fieldBranch}
	if len(w.shownPRs()) > 0 {
		out = append(out, fieldPRs)
	}
	if len(w.repoExisting()) > 0 {
		out = append(out, fieldExisting)
	}
	return append(out, fieldTask)
}

// chosenGroup is the group name typed in, or empty for the default.
func (w *wizard) chosenGroup() string {
	if !w.multi() {
		return ""
	}
	return strings.TrimSpace(w.groupName.Value())
}

// focusBranch moves the typing to the branch name, as stepBranch is
// entered.
func (w *wizard) focusBranch() tea.Cmd {
	w.field = fieldBranch
	w.groupName.Blur()
	w.task.Blur()
	return w.branch.Focus()
}

// focusField moves the typing to field f.
func (w *wizard) focusField(f int) tea.Cmd {
	if w.step != stepBranch || w.busy != "" || w.field == f {
		return nil
	}
	w.field = f
	w.branch.Blur()
	w.groupName.Blur()
	w.task.Blur()
	switch f {
	case fieldExisting, fieldPRs:
		return nil
	case fieldTask:
		return w.task.Focus()
	case fieldGroup:
		return w.groupName.Focus()
	}
	return w.branch.Focus()
}

func cursorLine(on bool, s string, st lipgloss.Style) string {
	if on {
		return st.Render("❯ ") + st.Render(s)
	}
	return "  " + s
}

// wizardAnswer takes core's answer to what a new-tab dialog asked, in the
// dialog when it is still the one that asked.
func (m *Model) wizardAnswer(ev core.Event) tea.Cmd {
	w, _ := m.dialog.(*wizard)
	ours := func(job string) bool { return w != nil && w.id == job }
	switch ev := ev.(type) {
	case core.PlanMsg:
		if ours(ev.Job) {
			return w.handlePlan(m, ev)
		}
	case core.IssueMsg:
		if ours(ev.Job) {
			return w.handleIssue(ev)
		}
	case core.SetupProgressMsg:
		if ours(ev.Job) && w.step == stepCreating {
			w.busy = ev.Line
		}
	case core.WorktreesMadeMsg:
		if ours(ev.Job) {
			return w.handle(m, ev)
		}
		return m.createdBehind(ev)
	}
	return nil
}
