package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/core"
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
		{Branch: "part-a", Summary: "Part A.", Prompt: "Do part A.", Commits: []string{commit[:10]}},
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
		return strings.Contains(v, "Machine: box") && strings.Contains(v, boxRepo) && strings.Contains(v, "carried over to box")
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
		return strings.Contains(v, "Machine: box") && strings.Contains(v, "cloned from ") && strings.Contains(v, filepath.Join(filepath.Dir(boxRepo), "proj"))
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
