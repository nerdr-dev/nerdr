package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/spinoff"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// spinoffRemoteSetup is moveSetup with a part to split off waiting on
// this machine's tab, starting with the tab's commit, and the dialog open
// for it. It gives the commit and box's clone.
func spinoffRemoteSetup(t *testing.T, m *Model, told chan any, here, box *core.Core, more ...spinoff.Part) (commit, boxRepo string) {
	t.Helper()
	wt, boxRepo := moveSetup(t, m, told, here, box, false)
	commit = strings.TrimSpace(moveGit(t, wt, "rev-parse", "HEAD"))
	here.Mu.Lock()
	here.Tabs[0].Spinoff = &spinoff.Pending{Next: append([]spinoff.Part{
		{ID: "pa", Branch: "part-a", Summary: "Part A.", Prompt: "Do part A.", Commits: []string{commit[:10]}},
	}, more...)}
	here.Mu.Unlock()
	locked(m, func() { m.do(core.RenameTab{T: core.TabRef{Tab: "1"}, Name: "login"}) })
	pumpUntil(t, m, told, "the plan", func() bool {
		var ok bool
		locked(m, func() { ok = m.fleet.Tabs()[0].Spinoff != nil })
		return ok
	})
	goTo(m, 0)
	locked(m, func() {
		m.dialog = nil
		m.openSpinoff(m.Cur())
	})
	return commit, boxRepo
}

// pressOn has m take key k, and what the key's command gives.
func pressOn(m *Model, k string) {
	_, cmd := m.Update(keyMsg(k))
	if cmd != nil {
		if msg := cmd(); msg != nil {
			m.Update(msg)
		}
	}
}

// boxTabNamed is box's tab of the name, nil when there is none, and its
// first message.
func boxTabNamed(box *core.Core, name string) (dir, first string, wts []workspace.Worktree) {
	box.Mu.Lock()
	defer box.Mu.Unlock()
	for _, tb := range box.Tabs {
		if tb.Name == name {
			if len(tb.Messages) > 0 {
				first = tb.Messages[0].Text
			}
			return tb.Dir, first, tb.Worktrees
		}
	}
	return "", "", nil
}

// TestFleetSpinoffOnAnotherMachine: ctrl+t has the part made on box,
// found there by its origin; its commit is carried over and starts the
// branch there, box's agent is told where the part came from, and this
// machine's agent what became of it, and where.
func TestFleetSpinoffOnAnotherMachine(t *testing.T) {
	m, here, box, _, told := twoMachines(t)
	_, boxRepo := spinoffRemoteSetup(t, m, told, here, box)
	pump(t, m, told, "the dialog", func(v string) bool {
		return strings.Contains(v, "Machine: this machine")
	})
	pressOn(m, "ctrl+t")
	pump(t, m, told, "box's repository", func(v string) bool {
		return strings.Contains(v, "Machine: box") && strings.Contains(v, filepath.Join("box", "proj")) && strings.Contains(v, "carried over to box")
	})
	pressOn(m, "enter")
	pumpUntil(t, m, told, "the tab on box", func() bool {
		dir, _, _ := boxTabNamed(box, "part-a")
		return dir != ""
	})
	dir, first, wts := boxTabNamed(box, "part-a")
	if len(wts) != 1 || wts[0].Repo != boxRepo {
		t.Fatalf("worktrees on box: %+v", wts)
	}
	if log := moveGit(t, dir, "log", "--format=%s", "origin/main..HEAD"); strings.TrimSpace(log) != "work" {
		t.Errorf("the branch on box has %q, not the part's commit", log)
	}
	if !strings.Contains(first, "Do part A.") || !strings.Contains(first, "on the machine ") || !strings.Contains(first, "were brought over") {
		t.Errorf("box's agent was told:\n%s", first)
	}
	pumpUntil(t, m, told, "the report here", func() bool {
		here.Mu.Lock()
		defer here.Mu.Unlock()
		tb := here.Tabs[0]
		for _, msg := range tb.Messages {
			if strings.Contains(msg.Text, "part-a: made as a new tab on branch part-a") && strings.Contains(msg.Text, "on the machine box") {
				return tb.Spinoff == nil
			}
		}
		return false
	})
	locked(m, func() {
		if m.spinoffRun != nil {
			t.Error("the run is not over")
		}
	})
	// Nothing went to origin, and nothing is left here of the pack.
	if out := moveGit(t, boxRepo, "ls-remote", "origin", "part-a"); out != "" {
		t.Errorf("origin has part-a: %s", out)
	}
	here.Mu.Lock()
	n := len(here.Tabs)
	here.Mu.Unlock()
	if n != 1 {
		t.Errorf("%d tabs here", n)
	}
}

// TestFleetSpinoffOnAnotherMachineIsHeld: while a part is made on
// another machine, the tab's core holds it: the API can neither skip it
// nor make it again, an outcome for another part moves nothing, and once
// it is made, the next part is the one asked about.
func TestFleetSpinoffOnAnotherMachineIsHeld(t *testing.T) {
	m, here, box, _, told := twoMachines(t)
	spinoffRemoteSetup(t, m, told, here, box, spinoff.Part{ID: "pb", Branch: "part-b", Summary: "Part B.", Prompt: "Do part B."})
	pressOn(m, "ctrl+t")
	pump(t, m, told, "box's repository", func(v string) bool {
		return strings.Contains(v, "Machine: box") && strings.Contains(v, "carried over to box")
	})
	pressOn(m, "enter")
	pumpUntil(t, m, told, "the hold", func() bool {
		here.Mu.Lock()
		defer here.Mu.Unlock()
		return here.Tabs[0].SpinoffHold != ""
	})
	here.Mu.Lock()
	id := here.Tabs[0].ID
	if _, err := here.APISpinoffSkip(id, "pa"); err == nil || !strings.Contains(err.Error(), "being made") {
		t.Errorf("skipped while made on box: %v", err)
	}
	if _, err := here.APISpinoffCreate(core.APISpinoffMake{Tab: id, Part: "pa"}, nil); err == nil || !strings.Contains(err.Error(), "being made") {
		t.Errorf("made again while made on box: %v", err)
	}
	if s := here.APIState().Tabs[0].Spinoff; s == nil || !strings.Contains(s.Busy, "on box") {
		t.Errorf("the state does not say it is made on box: %+v", s)
	}
	here.SpinoffNext(here.Tabs[0], spinoff.Outcome{Part: spinoff.Part{ID: "pb"}, Skipped: true})
	if n := len(here.Tabs[0].Spinoff.Next); n != 2 {
		t.Errorf("an outcome for part B moved the parts: %d left", n)
	}
	here.Mu.Unlock()
	pumpUntil(t, m, told, "part B asked about", func() bool {
		here.Mu.Lock()
		defer here.Mu.Unlock()
		tb := here.Tabs[0]
		return tb.Spinoff != nil && len(tb.Spinoff.Next) == 1 && tb.Spinoff.Next[0].ID == "pb" && tb.SpinoffBusy == "" && tb.SpinoffHold == ""
	})
	if dir, _, _ := boxTabNamed(box, "part-a"); dir == "" {
		t.Error("part A was not made on box")
	}
}

// TestFleetSpinoffHoldUnanswered: a tab's core that never says it holds
// the part, of an older nerdr, gives the run up when the wait is over,
// rather than keeping the dialog busy for ever.
func TestFleetSpinoffHoldUnanswered(t *testing.T) {
	m, here, box, _, told := twoMachines(t)
	spinoffRemoteSetup(t, m, told, here, box)
	pressOn(m, "ctrl+t")
	pump(t, m, told, "box's repository", func(v string) bool { return strings.Contains(v, "Machine: box") })
	var job string
	locked(m, func() {
		d := m.dialog.(*spinoffDialog)
		// As spinoffRemote has it before its hold is answered.
		m.spinoffRun = &spinoffRun{job: "late", ref: m.fleet.Ref(d.t), from: m.machineOf(d.t), to: d.target(m),
			land: core.SpinoffLand{Name: "part-a"}, busy: "making part-a on box…"}
		job = m.spinoffRun.job
		m.spinoffHoldLate(job)
		if m.spinoffRun != nil || !strings.Contains(d.err, "did not say it holds the part") {
			t.Errorf("the run waits on: %q", d.err)
		}
	})
}

// TestFleetSpinoffOnAnotherMachineClonesAndFails: box without the
// repository says it would clone it; a branch of the name taken there is
// said in the dialog, and the part waits, for the user to put it right.
func TestFleetSpinoffOnAnotherMachineClonesAndFails(t *testing.T) {
	m, here, box, _, told := twoMachines(t)
	_, boxRepo := spinoffRemoteSetup(t, m, told, here, box)
	box.Mu.Lock()
	box.Scan.Repos = nil
	box.Root = ""
	box.Mu.Unlock()
	os.Rename(boxRepo, boxRepo+"-old")
	pressOn(m, "ctrl+t")
	pump(t, m, told, "the clone", func(v string) bool {
		// The path cut in the middle, should it be long.
		return strings.Contains(v, "Machine: box") && strings.Contains(v, "cloned from origin into ") && strings.Contains(v, filepath.Join("box", "proj"))
	})
	// The folder it would be cloned into is taken meanwhile.
	os.MkdirAll(filepath.Join(boxRepo, "x"), 0o755)
	pressOn(m, "tab") // written anew: no pack
	pressOn(m, "enter")
	// Said at the dialog's foot, below what the test's screen shows.
	pumpUntil(t, m, told, "the error", func() bool {
		var err string
		locked(m, func() {
			if d, ok := m.dialog.(*spinoffDialog); ok {
				err = d.err
			}
		})
		return strings.Contains(err, "proj: ") && strings.Contains(err, "already exists")
	})
	locked(m, func() {
		if m.spinoffRun != nil || m.fleet.Tabs()[0].Spinoff == nil {
			t.Error("the part does not wait for the user")
		}
	})
	// And the tab's core lets go of it, for it to be answered again.
	pumpUntil(t, m, told, "the hold let go of", func() bool {
		here.Mu.Lock()
		defer here.Mu.Unlock()
		return here.Tabs[0].SpinoffHold == "" && here.Tabs[0].SpinoffBusy == ""
	})
}

// TestFleetSpinoffMachineGone: with box gone, the dialog makes the part
// on this machine again.
func TestFleetSpinoffMachineGone(t *testing.T) {
	m, here, box, _, bye, told := twoMachinesBye(t)
	spinoffRemoteSetup(t, m, told, here, box)
	pressOn(m, "ctrl+t")
	pump(t, m, told, "box", func(v string) bool { return strings.Contains(v, "Machine: box") })
	bye("test")
	if cmd := gone(t, m, told, "box"); cmd != nil {
		cmd()
	}
	pump(t, m, told, "back here", func(v string) bool { return !strings.Contains(v, "Machine: box") })
	locked(m, func() {
		d, ok := m.dialog.(*spinoffDialog)
		if !ok || d.remote(m) || !strings.Contains(d.err, "box went away") {
			t.Fatalf("dialog %T on another machine, or not saying why", m.dialog)
		}
	})
}

// TestFleetSpinoffTwoPartsCloneOnce: of two parts made on box, which
// lacks the repository, the first clones it and the second is made in
// that clone.
func TestFleetSpinoffTwoPartsCloneOnce(t *testing.T) {
	m, here, box, _, told := twoMachines(t)
	_, boxRepo := spinoffRemoteSetup(t, m, told, here, box,
		spinoff.Part{Branch: "part-b", Summary: "Part B.", Prompt: "Do part B."})
	// Box looks for its repositories, as it does, in the folder they are
	// cloned into.
	os.Rename(boxRepo, filepath.Join(filepath.Dir(boxRepo), "..", "proj-old"))
	box.Mu.Lock()
	box.Scan.Repos = nil
	box.Root = ""
	box.Cfg.RepoDirs = []string{filepath.Dir(boxRepo)}
	box.Mu.Unlock()
	pressOn(m, "ctrl+t")
	pump(t, m, told, "the clone", func(v string) bool { return strings.Contains(v, "Machine: box") && strings.Contains(v, "cloned from ") })
	pressOn(m, "tab")
	pressOn(m, "enter")
	pump(t, m, told, "part B", func(v string) bool {
		return strings.Contains(v, "branch: part-b") && strings.Contains(v, "Machine: box")
	})
	pressOn(m, "enter")
	pumpUntil(t, m, told, "both tabs on box", func() bool {
		a, _, _ := boxTabNamed(box, "part-a")
		b, _, _ := boxTabNamed(box, "part-b")
		return a != "" && b != ""
	})
	for _, name := range []string{"part-a", "part-b"} {
		if _, _, wts := boxTabNamed(box, name); len(wts) != 1 || wts[0].Repo != boxRepo {
			t.Errorf("%s on box: %+v", name, wts)
		}
	}
}

// TestSpinoffReposFoundByTheTabsCore: in a tab with several repositories
// of a core in another process, the repository a part's commits are in is
// found by that core, where the worktrees are, and ticked in the dialog
// from what it sends.
func TestSpinoffReposFoundByTheTabsCore(t *testing.T) {
	m, here, _, told := remoteTUI(t, "sh", "-c", `stty -echo; echo here-ready; while read l; do echo "here got $l"; done`)
	root := t.TempDir()
	var wts []workspace.Worktree
	for _, name := range []string{"api", "web"} {
		dir := filepath.Join(root, name)
		moveGit(t, root, "init", "-q", dir)
		moveGit(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", name)
		wts = append(wts, workspace.Worktree{Repo: dir, Path: dir})
	}
	webCommit := strings.TrimSpace(moveGit(t, wts[1].Path, "rev-parse", "HEAD"))
	here.Mu.Lock()
	here.Tabs[0].Worktrees = wts
	here.OnSpinoff(hooks.Report{Method: hooks.Spinoff, Pane: here.Tabs[0].HookKey(), Spinoff: []spinoff.Part{
		{Branch: "part-a", Summary: "A.", Prompt: "Do A.", Commits: []string{webCommit}, InRepos: []string{"api"}},
	}})
	pt := here.Tabs[0].Spinoff.Next[0]
	here.Mu.Unlock()
	if len(pt.InRepos) != 1 || pt.InRepos[0] != "web" {
		t.Fatalf("core found the commit in %v, not web alone", pt.InRepos)
	}
	locked(m, func() { m.do(core.RenameTab{T: core.TabRef{Tab: "1"}, Name: "login"}) })
	pumpUntil(t, m, told, "the plan", func() bool {
		var ok bool
		locked(m, func() {
			tb := m.fleet.Tabs()[0]
			ok = tb.Spinoff != nil && len(tb.Spinoff.Next) == 1 && len(tb.Worktrees) == 2
		})
		return ok
	})
	goTo(m, 0)
	var on []bool
	locked(m, func() {
		m.dialog = nil
		m.openSpinoff(m.Cur())
		if d, ok := m.dialog.(*spinoffDialog); ok {
			on = d.repoOn
		}
	})
	if len(on) != 2 || on[0] || !on[1] {
		t.Fatalf("ticked %v, want web alone", on)
	}
}
