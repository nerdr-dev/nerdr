package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/sandboxcheck"
	"github.com/nerdr-dev/nerdr/internal/spinoff"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

func TestPendingLaunchRejectsChangedSandboxConfiguration(t *testing.T) {
	c, tab, _ := checkedCore(t, "echo unknown\n")
	cmd := c.Start(tab, false)
	if cmd == nil {
		t.Fatal("expected asynchronous preparation")
	}
	ev, ok := cmd().(SpawnReady)
	if !ok {
		t.Fatal("expected SpawnReady")
	}
	spec := c.Cfg.Agents[agents.Codex]
	spec.Sandboxed = false
	c.Cfg.Agents[agents.Codex] = spec
	c.OnSpawnReady(ev)
	if tab.Pane != nil || !strings.Contains(tab.Err, "configuration changed") {
		t.Fatalf("stale launch accepted: pane=%v err=%q", tab.Pane, tab.Err)
	}
}

func TestImportedSandboxHistoryPersistsWithoutRunningAgent(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(root, "removed-worktree")
	c := &Core{Root: root}
	c.ImportSandboxed([]workspace.Worktree{{Repo: repo, Path: missing}})
	sess, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	restored := &Core{Root: root}
	restored.restoreSandboxed(sess)
	if restored.SandboxedDirs[missing] || !restored.SandboxedRepos[repo] || !gitsafe.RepoSandboxed(repo) {
		t.Fatal("lost imported provenance without a live worktree or agent")
	}
}

func TestLegacySandboxHistorySurvivesDeletedWorktree(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	c := &Core{Root: root}
	c.restoreSandboxed(state.Session{SandboxedDirs: map[string]bool{filepath.Join(root, "removed"): true}})
	if !c.SandboxedRepos[repo] || !gitsafe.RepoSandboxed(repo) {
		t.Fatal("legacy history lost on migration")
	}
	tab := &Tab{AgentState: Agent{Tab: state.Tab{Worktrees: []workspace.Worktree{{Repo: repo, Path: filepath.Join(root, "new")}}}}}
	c.MarkSandboxed(tab)
	if tab.HasSandboxed() {
		t.Fatal("untouched tab inherited repository history")
	}
}

func TestMoveKeepsImportedHistoryWhenSandboxProbeRefusesStart(t *testing.T) {
	c, old, _ := checkedCore(t, runsCodex)
	c.Tabs = nil
	c.onMoveLanded(moveLanded{
		q:   MoveLand{Job: "audit", ID: "moved", Kind: agents.Codex, Sandboxed: true},
		man: moveManifest{Name: "moved", Sandboxed: true}, dir: old.Dir, prompt: "handoff",
	})
	if len(c.Tabs) != 1 {
		t.Fatalf("tabs: %d", len(c.Tabs))
	}
	moved := c.Tabs[0]
	if moved.Pane != nil {
		t.Fatal("expected pending preparation")
	}
	c.OnSpawnReady(SpawnReady{T: moved, Kind: agents.Codex,
		SpecKey: launchKey(c.SpecsFor(moved), agents.Codex),
		L:       agents.NewSession(c.Cfg.Agents, agents.Codex),
		Prep:    SpawnPrep{Check: &SandboxCheckedMsg{OK: true, Result: sandboxcheck.Result{Client: []string{"socket"}}}},
	})
	if moved.Pane != nil || !strings.Contains(moved.Err, "not started") {
		t.Fatalf("unexpected start: %s", moved.Err)
	}
	c.MarkSandboxed(moved)
	if !moved.HasSandboxed() {
		t.Fatal("refused destination start lost imported sandbox history")
	}
}

func TestSpinoffReportKeepsSandboxedProvenanceOfEveryPart(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	tab := &Tab{AgentState: Agent{Tab: state.Tab{ID: "same-slot", Agent: agents.Codex}}}
	c := &Core{Root: t.TempDir(), Client: NoClient{}, Tabs: []*Tab{tab}}
	c.ReportSpinoff(tab, []spinoff.Outcome{
		{Part: spinoff.Part{From: tab.ID, Summary: "old unsandboxed part"}},
		{Part: spinoff.Part{From: tab.ID, Branch: "sandboxed-payload", Sandboxed: true}},
	}, "main")
	if len(tab.Messages) != 1 || !tab.Messages[0].Sandboxed || !strings.Contains(tab.Messages[0].Text, "sandboxed-payload") {
		t.Fatalf("lost mixed report provenance: %+v", tab.Messages)
	}
}
