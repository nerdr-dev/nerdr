package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/herdr"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
)

func TestFuzzy(t *testing.T) {
	for _, tc := range []struct {
		q, s string
		ok   bool
	}{
		{"", "anything", true},
		{"lgn", "feat-login repoa claude", true},
		{"nig", "feat-login", false},
	} {
		if _, ok := fuzzy(tc.q, tc.s); ok != tc.ok {
			t.Errorf("fuzzy(%q, %q) = %v", tc.q, tc.s, ok)
		}
	}
	// Letters that follow each other, at the start of a word, win.
	a, _ := fuzzy("log", "feat-login")
	b, _ := fuzzy("log", "fix-lint-go")
	if a <= b {
		t.Errorf("contiguous %d <= scattered %d", a, b)
	}
}

func TestFindOverlaps(t *testing.T) {
	mk := func(id string, changed map[string][]string) *core.Tab {
		t := &core.Tab{AgentState: core.Agent{Tab: state.Tab{ID: id, Name: id}}}
		for repo, files := range changed {
			t.Worktrees = append(t.Worktrees, workspace.Worktree{Repo: repo})
			t.Info.Wts = append(t.Info.Wts, core.WtInfo{Repo: repo, Stats: workspace.Stats{Changed: files}})
		}
		return t
	}
	a := mk("a", map[string][]string{"/r/app": {"x.go", "y.go"}})
	b := mk("b", map[string][]string{"/r/app": {"y.go", "z.go"}})
	c := mk("c", map[string][]string{"/r/other": {"y.go"}}) // same name, other repo
	m := &Model{Core: core.Core{Tabs: []*core.Tab{a, b, c}}}
	m.Core.Client = m
	m.Core.FindOverlaps()

	if got := m.Core.Overlaps["a"]; len(got) != 1 || got[0].With != b || !reflect.DeepEqual(got[0].Files, []string{"app/y.go"}) {
		t.Fatalf("a: %+v", got)
	}
	if got := m.Core.Overlaps["b"]; len(got) != 1 || got[0].With != a {
		t.Fatalf("b: %+v", got)
	}
	if got := m.Core.Overlaps["c"]; len(got) != 0 {
		t.Fatalf("c: %+v", got)
	}
}

func TestMergedNeedsEveryWorktree(t *testing.T) {
	in := core.TabInfo{Wts: []core.WtInfo{{HasPR: true}, {HasPR: true}}}
	in.Wts[0].Pr.State = "MERGED"
	if in.Merged() {
		t.Fatal("merged with one PR still open")
	}
	in.Wts[1].Pr.State = "MERGED"
	if !in.Merged() {
		t.Fatal("not merged")
	}
	// Commits after the merged PR are in no PR: the tab is not done.
	in.Wts[1].Pr.After = 1
	if in.Merged() {
		t.Fatal("merged with commits after the PR")
	}
	if got := core.PrLabel(in); got != "#0 merged" {
		t.Fatalf("label %q", got)
	}
	in.Wts[0].Pr.After = 2
	if got := core.PrLabel(in); got != "#0 merged · 2 commits after" {
		t.Fatalf("label %q", got)
	}
	if (core.TabInfo{}).Merged() {
		t.Fatal("no worktrees counts as merged")
	}
}

func TestIssueNumber(t *testing.T) {
	for s, want := range map[string]int{"#12": 12, "12": 0, "#": 0, "#x": 0, "#0": 0} {
		n, ok := issueNumber(s)
		if (want > 0) != ok || (ok && n != want) {
			t.Errorf("%q: %d %v", s, n, ok)
		}
	}
}

func TestNotesInsideHerdr(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "herdr.log")
	bin := filepath.Join(dir, "herdr")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	top := &core.Tab{AgentState: core.Agent{Tab: state.Tab{ID: "a", Name: "top"}, Light: core.StBlocked}}
	other := &core.Tab{AgentState: core.Agent{Tab: state.Tab{ID: "b", Name: "other"}, Light: core.StDone}}
	m := &Model{Core: core.Core{Tabs: []*core.Tab{top, other}}, Herdr: herdr.NewClient(bin, "w1:p1"), blurred: true}
	m.Core.Client = m
	m.Core.Cfg.Notify = "all"
	now := time.Now()
	m.notifyChange(top, false, now)
	m.notifyChange(other, false, now)
	m.notifyChange(other, false, now) // again at once: dropped
	if len(m.notes) != 2 {
		t.Fatalf("notes = %d", len(m.notes))
	}
	before := m.Herdr.State()
	m.reportHerdr() // the pane turns blocked: herdr notifies of top itself
	m.flushNotes(before)

	// Wait for the report as well as the notification: both run in the
	// background, and one still writing the log fails TempDir's cleanup.
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, _ := os.ReadFile(log)
		got := string(data)
		if strings.Contains(got, "notification show") && strings.Contains(got, "pane report-metadata") {
			if strings.Contains(got, "notification show nerdr · top") {
				t.Fatalf("notified of the tab herdr notifies of:\n%s", got)
			}
			if !strings.Contains(got, "notification show nerdr · other --body  is done --sound done") {
				t.Fatalf("log:\n%s", got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no notification:\n%s", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestKeyHintsPressTheirKeys(t *testing.T) {
	for _, k := range []string{"ctrl+o", "ctrl+x", "alt+x", "tab", "enter"} {
		if got := keyMsg(k).String(); got != k {
			t.Errorf("keyMsg(%q) = %q", k, got)
		}
	}
}

// A new size clears the screen and asks for the size again once the
// resize has settled; that size, unchanged, does nothing more, so the
// asking stops.
func TestResizeAsksForTheSizeAgain(t *testing.T) {
	m := testModel(t)
	if cmd := m.update(tea.WindowSizeMsg{Width: 100, Height: 30}); cmd == nil || m.width != 100 || m.height != 30 {
		t.Fatalf("a new size should be taken and followed up: %dx%d", m.width, m.height)
	}
	if cmd := m.update(tea.WindowSizeMsg{Width: 100, Height: 30}); cmd != nil {
		t.Fatal("the same size again should end it")
	}
}
