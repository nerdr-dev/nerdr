package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nerdr/internal/agents"
	"nerdr/internal/github"
	"nerdr/internal/state"
	"nerdr/internal/workspace"

	"github.com/charmbracelet/x/ansi"
)

// TestWizardAsksAboutOriginsBranch covers the step shown when origin
// already has the branch typed: it says whether it is merged, offers a new
// branch from main first, and carries out the choice.
func TestWizardAsksAboutOriginsBranch(t *testing.T) {
	m := tabsModel(t)
	m.openWizard()
	w := m.dialog.(*wizard)
	w.agents = []agents.Kind{agents.Claude, agents.Codex} // two to pick from
	w.step = stepBranch
	plan := workspace.Plan{Branch: "diverse", Dir: "/w/diverse-app", Items: []workspace.PlanItem{{
		Repo: workspace.Repo{Name: "app", Path: "/r/app"}, Path: "/w/diverse-app",
		Mode: "remote", Base: "origin/main", Ahead: 2, PR: 9, PRState: "MERGED",
	}}}
	w.handlePlan(m, planMsg{plan: plan})
	if w.step != stepExisting {
		t.Fatalf("step = %d, want the question about origin's branch", w.step)
	}
	view := w.view(m, 120, 40)
	for _, want := range []string{"origin/diverse already exists", "PR #9 merged", "New branch diverse from origin/main", "Continue origin/diverse", "A push is refused"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view lacks %q:\n%s", want, view)
		}
	}

	w.key(m, keyMsg("enter"))
	if w.step != stepAgent || w.chosenPlan().Items[0].Mode != "new" {
		t.Fatalf("enter should take the new branch: step %d mode %s", w.step, w.chosenPlan().Items[0].Mode)
	}
	if w.plan.Items[0].Mode != "remote" {
		t.Fatal("choosing must not change the plan asked about")
	}

	w.key(m, keyMsg("esc"))
	if w.step != stepExisting {
		t.Fatalf("esc from the agent should go back to the question, step %d", w.step)
	}
	w.key(m, keyMsg("u"))
	if w.step != stepAgent || w.chosenPlan().Items[0].Mode != "remote" {
		t.Fatalf("u should continue origin's branch: step %d mode %s", w.step, w.chosenPlan().Items[0].Mode)
	}

	// A branch with work of its own is not called merged.
	w.useExisting = false
	w.plan.Items[0].PR, w.plan.Items[0].PRState = 12, "OPEN"
	w.step = stepExisting
	view = w.view(m, 120, 40)
	if !strings.Contains(view, "2 commits not in origin/main") || !strings.Contains(view, "not merged") {
		t.Fatalf("view:\n%s", view)
	}
}

// TestWizardAsksAboutLocalBranch covers the same step for a branch that
// exists here: one merged long ago gets a new branch from main in its
// place to start with, one with work that is not merged can only be
// continued.
func TestWizardAsksAboutLocalBranch(t *testing.T) {
	m := tabsModel(t)
	m.openWizard()
	w := m.dialog.(*wizard)
	w.agents = []agents.Kind{agents.Claude, agents.Codex} // two to pick from
	w.step = stepBranch
	plan := workspace.Plan{Branch: "fix", Dir: "/w/fix-app", Items: []workspace.PlanItem{{
		Repo: workspace.Repo{Name: "app", Path: "/r/app"}, Path: "/w/fix-app",
		Mode: "local", Base: "origin/main", Ahead: 3, Behind: 40, PR: 6, PRState: "MERGED", Tip: "abc1234def", PRHasTip: true,
	}}}
	w.handlePlan(m, planMsg{plan: plan})
	if w.step != stepExisting {
		t.Fatalf("step = %d, want the question about the local branch", w.step)
	}
	view := w.view(m, 120, 40)
	for _, want := range []string{"Branch fix already exists here", "local branch · PR #6 merged · 40 commits behind origin/main",
		"New branch fix from origin/main", "Continue existing fix", "takes the place of the local one"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view lacks %q:\n%s", want, view)
		}
	}
	w.key(m, keyMsg("enter"))
	if it := w.chosenPlan().Items[0]; w.step != stepAgent || it.Mode != "new" || it.Replaces != "abc1234def" {
		t.Fatalf("enter should take a new branch in place of the merged one: step %d %+v", w.step, it)
	}
	if !strings.Contains(w.view(m, 120, 40), "in place of the merged local one (abc1234)") {
		t.Fatalf("the agent step should say the local branch is replaced:\n%s", w.view(m, 120, 40))
	}
	w.key(m, keyMsg("esc"))
	if w.step != stepExisting {
		t.Fatalf("esc from the agent should go back to the question, step %d", w.step)
	}

	// Work that is not merged: only continuing it is on offer.
	w.step = stepBranch
	plan.Items[0].PR, plan.Items[0].PRState = 0, ""
	w.handlePlan(m, planMsg{plan: plan})
	view = w.view(m, 120, 40)
	for _, want := range []string{"3 commits not in origin/main", "would throw away", "pick another name"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view lacks %q:\n%s", want, view)
		}
	}
	for _, k := range []string{"down", "n"} {
		w.key(m, keyMsg(k))
		if w.step != stepExisting || !w.useExisting {
			t.Fatalf("%s offered a new branch over unmerged work: step %d", k, w.step)
		}
	}
	w.key(m, keyMsg("enter"))
	if w.step != stepAgent || w.chosenPlan().Items[0].Mode != "local" {
		t.Fatalf("enter should continue the local branch: step %d mode %s", w.step, w.chosenPlan().Items[0].Mode)
	}
}

// esc while the worktrees are being made hides the wizard; the tab then
// opens beside the active one without taking the keyboard, and the result
// does not land in a wizard opened since.
func TestWizardHiddenWhileCreating(t *testing.T) {
	m := testModel(t)
	m.openWizard()
	w := m.dialog.(*wizard)
	w.step, w.plan.Branch = stepCreating, "new"
	w.key(m, keyMsg("esc"))
	if m.dialog != nil {
		t.Fatal("esc hides the wizard while it creates")
	}

	m.openWizard()
	other := m.dialog
	plan := workspace.Plan{Branch: "new", Dir: "/w/new"}
	m.Update(wizardMsg{w: w, plan: plan, agent: "claude", task: "do it"})
	if m.dialog != other {
		t.Fatalf("the result went to the wizard opened since: %T", m.dialog)
	}
	if m.cur().Name != "login" {
		t.Fatalf("on %s: the new tab must not take the keyboard", m.cur().Name)
	}
	nt := m.tabs[len(m.tabs)-1]
	if nt.Name != "new" || len(nt.Messages) != 1 || nt.Goal != "do it" {
		t.Fatalf("the new tab and its task: %+v", nt.Tab)
	}

	m.dialog = nil
	n := len(m.tabs)
	m.Update(wizardMsg{w: w, plan: plan, err: errors.New("no fetch")})
	if len(m.tabs) != n || !strings.Contains(m.toastText, "no fetch") {
		t.Fatalf("a failure is told, and opens nothing: %d tabs, toast %q", len(m.tabs), m.toastText)
	}
}

// TestWizardOffersTheLastFetchedCopy covers a fetch of the base branch
// that failed: the wizard says so and offers to start from what was last
// fetched, which a failure of another kind does not.
func TestWizardOffersTheLastFetchedCopy(t *testing.T) {
	m := testModel(t)
	m.openWizard()
	w := m.dialog.(*wizard)
	w.step, w.plan.Branch = stepCreating, "new"
	plan := workspace.Plan{Branch: "new", Dir: "/w/new"}

	m.Update(wizardMsg{w: w, plan: plan, err: &workspace.FetchError{Repo: "a", Branch: "main", Err: errors.New("no key")}})
	if w.step != stepAgent || !w.unfetched {
		t.Fatalf("a failed fetch offers the last fetched copy: step %d, unfetched %v", w.step, w.unfetched)
	}
	if !strings.Contains(ansi.Strip(w.view(m, 120, 40)), "last fetched") {
		t.Fatal("the offer is shown")
	}
	w.key(m, keyMsg("esc"))
	if w.unfetched {
		t.Fatal("going back drops the offer")
	}

	w.step = stepCreating
	m.Update(wizardMsg{w: w, plan: plan, err: errors.New("disk full")})
	if w.unfetched {
		t.Fatal("only a failed fetch offers the last fetched copy")
	}
}

// TestWizardGroupName covers naming the group of a tab spanning several
// repositories: asked for only then, multi when left empty.
func TestWizardGroupName(t *testing.T) {
	m := tabsModel(t)
	m.openWizard()
	w := m.dialog.(*wizard)
	w.repos = []workspace.Repo{{Name: "a", Path: "/r/a"}, {Name: "b", Path: "/r/b"}}
	w.picked = map[int]bool{0: true}
	w.step = stepBranch
	w.focusBranch()
	if strings.Contains(w.view(m, 120, 40), "group:") {
		t.Fatal("one repository: no group to name")
	}
	w.key(m, keyMsg("tab"))
	if w.field != fieldTask {
		t.Fatalf("tab with one repository should go to the task, field %d", w.field)
	}

	w.picked[1] = true
	w.focusBranch()
	if !strings.Contains(w.view(m, 120, 40), "group:") {
		t.Fatal("several repositories: the group field is missing")
	}
	if w.chosenGroup() != "" {
		t.Fatalf("empty group = %q, want the default", w.chosenGroup())
	}
	w.key(m, keyMsg("tab"))
	if w.field != fieldGroup {
		t.Fatalf("tab should go to the group, field %d", w.field)
	}
	w.groupName.SetValue("  platform ")
	if w.chosenGroup() != "platform" {
		t.Fatalf("group = %q", w.chosenGroup())
	}
	w.key(m, keyMsg("shift+tab"))
	if w.field != fieldBranch {
		t.Fatalf("shift+tab should go back to the branch, field %d", w.field)
	}

	tb := &tab{}
	tb.Worktrees = []workspace.Worktree{{Repo: "/r/a"}, {Repo: "/r/b"}}
	if tb.group() != multiGroup {
		t.Fatalf("unnamed group = %q", tb.group())
	}
	tb.Group = "platform"
	if tb.group() != "platform" {
		t.Fatalf("named group = %q", tb.group())
	}
}

// TestGroupNameKeptAndRenamed covers a group's name outliving its tab: a
// tab opened again on the same worktrees goes in the same group, the
// new-tab dialog offers the names there are, and a group can be renamed,
// back to multi too.
func TestGroupNameKeptAndRenamed(t *testing.T) {
	m := tabsModel(t)
	wts := []workspace.Worktree{{Repo: "/r/a"}, {Repo: "/r/b"}}
	open := func(group string) *tab {
		t := &tab{Tab: state.Tab{ID: newTabID(), Name: "feat", Dir: "/w/feat-a-b", Worktrees: wts}}
		t.Group = m.rememberGroup(t.Dir, group, wts) // as addTab does, without starting an agent
		m.tabs = append(m.tabs, t)
		return t
	}
	m.closeTab(open("platform"), false, false, nil)

	again := open("")
	if again.group() != "platform" {
		t.Fatalf("opened again: group %q", again.group())
	}

	m.openWizard()
	w := m.dialog.(*wizard)
	w.repos = []workspace.Repo{{Name: "a", Path: "/r/a"}, {Name: "b", Path: "/r/b"}}
	w.picked = map[int]bool{0: true, 1: true}
	w.step = stepBranch
	if view := ansi.Strip(w.view(m, 120, 40)); !strings.Contains(view, "existing (→ completes): platform") {
		t.Fatalf("the names there are should be offered:\n%s", view)
	}
	m.dialog = nil

	m.renameGroup("platform", "infra")
	if again.group() != "infra" || m.groupNames["/w/feat-a-b"] != "infra" {
		t.Fatalf("renamed: group %q, kept %q", again.group(), m.groupNames["/w/feat-a-b"])
	}
	m.renameGroup("infra", "")
	if again.group() != multiGroup {
		t.Fatalf("renamed to nothing: group %q", again.group())
	}

	// Deleting the worktrees forgets the name.
	m.renameGroup(multiGroup, "infra")
	m.closeTab(again, true, true, nil)
	if _, ok := m.groupNames["/w/feat-a-b"]; ok {
		t.Fatal("the name of deleted worktrees should be forgotten")
	}
}

// TestWizardForARepoClosesOnEsc covers the dialog opened with its
// repository picked, as "+ new worktree" does: esc at the branch name
// closes it, not going back to the repository list it never showed.
func TestWizardForARepoClosesOnEsc(t *testing.T) {
	m := tabsModel(t)
	repo := filepath.Join(m.root, "app")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	m.openWizardFor(repo)
	w, ok := m.dialog.(*wizard)
	if !ok || w.step != stepBranch {
		t.Fatalf("the dialog should open at the branch name: %T", m.dialog)
	}
	w.key(m, keyMsg("esc"))
	if m.dialog != nil {
		t.Fatalf("esc should close the dialog, step %d", w.step)
	}

	m.openWizard()
	w = m.dialog.(*wizard)
	w.step = stepBranch
	w.key(m, keyMsg("esc"))
	if m.dialog == nil || w.step != stepRepos {
		t.Fatal("esc from the branch name should go back to the list it came from")
	}
}

// TestNewRepoPicksFromTheRepositoriesFirst covers "+ new repo": with
// repositories to pick from, it opens at their list, which offers making
// or cloning one too; with none, at the form for a new one.
func TestNewRepoPicksFromTheRepositoriesFirst(t *testing.T) {
	m := tabsModel(t)
	m.cfg.RepoDirs = []string{m.root}
	m.openNewRepoDialog()
	if w, ok := m.dialog.(*wizard); !ok || w.repoDir == "" || w.step != stepNewRepo {
		t.Fatalf("with no repositories it should open at the new one's form: %T", m.dialog)
	}

	m.dialog = nil
	if err := os.MkdirAll(filepath.Join(m.root, "app", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	m.openNewRepoDialog()
	if w, ok := m.dialog.(*wizard); !ok || w.step != stepRepos || len(w.repos) != 1 {
		t.Fatalf("with a repository it should open at the list: %T", m.dialog)
	}
}

// With one agent there is nothing to pick: the tab is started without
// asking.
func TestWizardSkipsPickingTheOnlyAgent(t *testing.T) {
	m := tabsModel(t)
	m.openWizard()
	w := m.dialog.(*wizard)
	w.agents = []agents.Kind{agents.Claude}
	w.step = stepBranch
	plan := workspace.Plan{Branch: "x", Dir: "/w/x-app", Items: []workspace.PlanItem{{
		Repo: workspace.Repo{Name: "app", Path: "/r/app"}, Path: "/w/x-app", Mode: "new", Base: "origin/main",
	}}}
	w.handlePlan(m, planMsg{plan: plan})
	if w.step != stepCreating {
		t.Fatalf("step = %d, want the worktree being made", w.step)
	}
}

// TestWizardOffersTheRepositorysWorktrees covers the existing worktrees
// listed under the branch name: only those of the one repository picked,
// none when several are, and opening one by ↓ and enter.
func TestWizardOffersTheRepositorysWorktrees(t *testing.T) {
	m := tabsModel(t)
	m.openWizard()
	w := m.dialog.(*wizard)
	w.agents = []agents.Kind{agents.Claude, agents.Codex} // two to pick from
	w.repos = []workspace.Repo{{Name: "app", Path: "/r/app"}, {Name: "lib", Path: "/r/lib"}}
	w.existing = []workspace.Group{
		{Name: "fix/login", Dir: "/w/fix-login-app", Worktrees: []workspace.Worktree{{Repo: "/r/app", Path: "/w/fix-login-app", Branch: "fix/login"}}},
		{Name: "both", Dir: "/w/both", Worktrees: []workspace.Worktree{{Repo: "/r/app", Path: "/w/both/app"}, {Repo: "/r/lib", Path: "/w/both/lib"}}},
		{Name: "lib-only", Dir: "/w/lib-only-lib", Worktrees: []workspace.Worktree{{Repo: "/r/lib", Path: "/w/lib-only-lib"}}},
	}
	w.picked = map[int]bool{0: true}
	w.key(m, keyMsg("enter"))
	if w.step != stepBranch {
		t.Fatalf("step = %d, want the branch name", w.step)
	}
	view := ansi.Strip(w.view(m, 120, 40))
	if !strings.Contains(view, "existing worktree of app") || !strings.Contains(view, "fix/login") ||
		strings.Contains(view, "lib-only") || strings.Contains(view, "both") {
		t.Fatalf("only app's own worktrees should be offered:\n%s", view)
	}

	w.key(m, keyMsg("down"))
	if w.field != fieldExisting {
		t.Fatalf("↓ from the branch name should go to the worktrees, field %d", w.field)
	}
	w.key(m, keyMsg("enter"))
	if w.step != stepAgent || w.group == nil || w.group.Dir != "/w/fix-login-app" {
		t.Fatalf("enter should open the worktree: step %d group %#v", w.step, w.group)
	}
	w.key(m, keyMsg("esc"))
	if w.step != stepBranch {
		t.Fatalf("esc should go back to the branch name, step %d", w.step)
	}
	w.key(m, keyMsg("up"))
	if w.field != fieldBranch {
		t.Fatalf("↑ from the first worktree should go to the branch name, field %d", w.field)
	}

	// Several repositories: none offered.
	w.key(m, keyMsg("esc"))
	w.picked = map[int]bool{0: true, 1: true}
	w.key(m, keyMsg("enter"))
	if view := ansi.Strip(w.view(m, 120, 40)); strings.Contains(view, "existing worktree of") {
		t.Fatalf("no worktrees should be offered for several repositories:\n%s", view)
	}
}

// TestWizardScrollsLongLists: more worktrees than the screen has room for
// are shown around the cursor, with how many more above and below, and
// the keys under the list stay on screen.
func TestWizardScrollsLongLists(t *testing.T) {
	m := tabsModel(t)
	// Folders too long for a line, as a Mac's temporary ones are: their
	// headings take two rows each.
	m.root = "/" + strings.Repeat("folder/", 15) + "code"
	m.wtRoot = m.root + "/worktrees"
	m.openWizard()
	w := m.dialog.(*wizard)
	w.repos = []workspace.Repo{{Name: "app", Path: "/r/app"}}
	w.existing = nil
	for i := range 40 {
		w.existing = append(w.existing, workspace.Group{Name: fmt.Sprintf("wt-%02d", i), Dir: "/w/x",
			Worktrees: []workspace.Worktree{{Repo: "/r/app", Path: "/w/x"}}})
	}
	view := ansi.Strip(w.view(m, 100, 30))
	if n := strings.Count(view, "\n") + 1; n > 30 {
		t.Fatalf("%d rows in 30:\n%s", n, view)
	}
	if !strings.Contains(view, "esc cancel") || !strings.Contains(view, "more") || strings.Contains(view, "wt-39") {
		t.Fatalf("the list should scroll, the keys stay:\n%s", view)
	}
	w.cursor = len(w.repos) + 39
	if view := ansi.Strip(w.view(m, 100, 30)); !strings.Contains(view, "❯ wt-39") || !strings.Contains(view, "↑ ") {
		t.Fatalf("the cursor's row should be shown:\n%s", view)
	}
}

// TestWizardOffersOpenPullRequests: with one repository picked, the branch
// step lists its open pull requests, those your review is asked for first
// and marked, a long list scrolling around the cursor; typing narrows it,
// and enter on one carries on with its branch, a fork's fetched first.
// Those a tab is open on are left out, and a worktree on a listed pull
// request's branch is offered there only. A
// fork's branch name typed is checked against the list.
func TestWizardOffersOpenPullRequests(t *testing.T) {
	m := tabsModel(t, state.Tab{ID: "a", Name: "change-2", Dir: "/w/change-2",
		Worktrees: []workspace.Worktree{{Repo: "/r/app", Path: "/w/change-2", Branch: "change-2"}}})
	m.openWizard()
	w := m.dialog.(*wizard)
	app := workspace.Repo{Name: "app", Path: "/r/app"}
	w.repos = []workspace.Repo{app}
	w.existing = []workspace.Group{
		{Name: "change-4", Dir: "/w/change-4", Worktrees: []workspace.Worktree{{Repo: "/r/app", Path: "/w/change-4", Branch: "change-4"}}},
		{Name: "spike", Dir: "/w/spike", Worktrees: []workspace.Worktree{{Repo: "/r/app", Path: "/w/spike", Branch: "spike"}}},
	}
	w.picked = map[int]bool{0: true}
	w.agents = []agents.Kind{agents.Claude} // whatever is installed where the test runs
	w.key(m, keyMsg("enter"))
	if w.step != stepBranch {
		t.Fatalf("step = %d, want the branch name", w.step)
	}
	now := time.Now()
	got := repoPRs{at: now, names: []string{"acme/app"}}
	for i := 1; i <= 20; i++ {
		got.entries = append(got.entries, prEntry{repo: app, pr: github.PR{Number: i, Title: fmt.Sprintf("Change %d", i),
			Author: "ann", Branch: fmt.Sprintf("change-%d", i), UpdatedAt: now.Add(-time.Duration(i) * time.Hour)}})
	}
	got.entries = append(got.entries, prEntry{repo: app, pr: github.PR{Number: 21, Title: "From a fork", Author: "bob",
		Branch: "main", Fork: "bob", UpdatedAt: now.Add(-48 * time.Hour)}})
	w.pulls = wizardPRs{repo: app.Path}
	w.setPRs(m, got, map[string]bool{github.RequestKey("acme/app", 15): true})
	for _, e := range w.pulls.entries {
		if e.pr.Number == 2 {
			t.Fatal("#2 has a tab open on its branch: there is no new tab to make for it")
		}
	}
	if gs := w.repoExisting(); len(gs) != 1 || gs[0].Name != "spike" {
		t.Fatalf("change-4's worktree is offered among the pull requests, not again below: %+v", gs)
	}

	view := ansi.Strip(w.view(m, 140, 50))
	if !strings.Contains(view, "open pull request") || !strings.Contains(view, "● review asked") ||
		!strings.Contains(view, "↓ ") || strings.Contains(view, "Change 20") {
		t.Fatalf("the list should show its first rows, the one asking your review marked:\n%s", view)
	}
	if w.shownPRs()[0].pr.Number != 15 {
		t.Fatalf("the one asking your review should be first: #%d", w.shownPRs()[0].pr.Number)
	}

	w.key(m, keyMsg("down"))
	if w.field != fieldPRs {
		t.Fatalf("↓ from the branch name should go to the pull requests, field %d", w.field)
	}
	for range 12 {
		w.key(m, keyMsg("down"))
	}
	cur := w.shownPRs()[w.pulls.cursor]
	if view := ansi.Strip(w.view(m, 140, 50)); !strings.Contains(view, fmt.Sprintf("❯ #%d", cur.pr.Number)) || !strings.Contains(view, "↑ ") {
		t.Fatalf("the list should scroll to the cursor (#%d):\n%s", cur.pr.Number, view)
	}

	// Typing narrows the list, from the branch name.
	for _, r := range "fork" {
		w.key(m, keyMsg(string(r)))
	}
	if w.field != fieldBranch || w.branch.Value() != "fork" || len(w.shownPRs()) != 1 || w.shownPRs()[0].pr.Number != 21 {
		t.Fatalf("typing should narrow the list: field %d %q %d", w.field, w.branch.Value(), len(w.shownPRs()))
	}
	if view := ansi.Strip(w.view(m, 140, 50)); strings.Contains(view, "review asked") {
		t.Fatalf("#15, asking your review, is filtered away: the heading should not say so:\n%s", view)
	}
	if e, isFork, listed := w.forkPRNamed("main"); !listed || !isFork || e.pr.Number != 21 {
		t.Fatal("the list should know the fork's branch")
	}

	// Enter on the fork's fetches it first.
	w.key(m, keyMsg("down"))
	if cmd := w.key(m, keyMsg("enter")); cmd == nil || !strings.Contains(w.busy, "fetching #21") {
		t.Fatalf("enter on a fork's PR should fetch it: busy %q", w.busy)
	}
	w.busy = ""

	// Enter on one of origin's carries on with its branch.
	w.branch.SetValue("change-3")
	w.focusField(fieldBranch)
	w.key(m, keyMsg("down"))
	w.key(m, keyMsg("enter"))
	if w.branch.Value() != "change-3" || !w.useExisting || w.busy != "checking…" {
		t.Fatalf("enter should go on with the branch, continued: %q %v %q", w.branch.Value(), w.useExisting, w.busy)
	}
}
