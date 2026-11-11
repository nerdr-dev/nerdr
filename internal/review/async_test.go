package review

import (
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nerdr-dev/nerdr/internal/analysis"
	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/github"
	"github.com/nerdr-dev/nerdr/internal/order"
	"github.com/nerdr-dev/nerdr/internal/walkthrough"
	"github.com/nerdr-dev/nerdr/internal/why"
)

// countingSource is Local, counting the calls made through it.
type countingSource struct {
	Local
	n *atomic.Int32
}

func (s countingSource) ListWorktrees(repo string) ([]git.Worktree, error) {
	s.n.Add(1)
	return s.Local.ListWorktrees(repo)
}

func (s countingSource) DetectBase(wt string) string {
	s.n.Add(1)
	return s.Local.DetectBase(wt)
}

func (s countingSource) Files(c git.Comparison) ([]git.FileStatus, map[string]git.LineCount, error) {
	s.n.Add(1)
	return s.Local.Files(c)
}

func (s countingSource) Diff(c git.Comparison, file string, untracked bool, original string) (string, error) {
	s.n.Add(1)
	return s.Local.Diff(c, file, untracked, original)
}

func (s countingSource) OldFile(c git.Comparison, file string) (string, error) {
	s.n.Add(1)
	return s.Local.OldFile(c, file)
}

func (s countingSource) OldSource(c git.Comparison, file string) (string, error) {
	s.n.Add(1)
	return s.Local.OldSource(c, file)
}

func (s countingSource) FullNewFile(wt, file, diff string) (string, error) {
	s.n.Add(1)
	return s.Local.FullNewFile(wt, file, diff)
}

func (s countingSource) ReadFile(wt, file string) (string, error) {
	s.n.Add(1)
	return s.Local.ReadFile(wt, file)
}

func (s countingSource) Summary(wt string) (int, int, int) {
	s.n.Add(1)
	return s.Local.Summary(wt)
}

func (s countingSource) Stamps(wt string, paths []string) map[string]string {
	s.n.Add(1)
	return s.Local.Stamps(wt, paths)
}

func (s countingSource) GhAvailable() bool {
	s.n.Add(1)
	return s.Local.GhAvailable()
}

func (s countingSource) CurrentPR(wt string) (github.PR, bool, string) {
	s.n.Add(1)
	return github.PR{}, false, "no pull request in tests"
}

func (s countingSource) FailedRunLogs(wt string, checks []github.Check, limit int) (string, error) {
	s.n.Add(1)
	return "", nil
}

func (s countingSource) Notes(wt string) why.Index {
	s.n.Add(1)
	return s.Local.Notes(wt)
}

func (s countingSource) Order(wt, branch string) order.Index {
	s.n.Add(1)
	return s.Local.Order(wt, branch)
}

func (s countingSource) Walkthrough(wt, branch string, files []string) (walkthrough.Walkthrough, bool, bool) {
	s.n.Add(1)
	return s.Local.Walkthrough(wt, branch, files)
}

func (s countingSource) WalkthroughPage(wt, branch string) ([]byte, bool, error) {
	s.n.Add(1)
	return s.Local.WalkthroughPage(wt, branch)
}

func (s countingSource) LoadProject(wt string) int {
	s.n.Add(1)
	return s.Local.LoadProject(wt)
}

func (s countingSource) Imports(wt string, paths []string) map[string][]string {
	s.n.Add(1)
	return s.Local.Imports(wt, paths)
}

func (s countingSource) Relations(wt, file string) ([]analysis.Symbol, [][]analysis.Ref, string) {
	s.n.Add(1)
	return s.Local.Relations(wt, file)
}

// TestUpdateReadsNothingOnTheSpot: the panel's Source may be on another
// machine, so Update never reads through it itself; what a key needs read
// is read by the command it returns, and put in place when it comes.
func TestUpdateReadsNothingOnTheSpot(t *testing.T) {
	parent := setupJumpRepo(t, map[string]string{
		"caller.ts": "import { helper } from './lib';\nexport function use() { return helper(); }\n",
	}, map[string]string{
		"lib.ts":  "export function helper() { return 1; }\n",
		"more.ts": "export const a = 1;\n",
	})
	repo := filepath.Join(parent, "repo")
	var n atomic.Int32
	m := pinNow(t, NewEmbedded(countingSource{n: &n}), []Pin{{Repo: repo, Path: repo}})
	m = m.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = openFileDiff(t, m, "lib.ts")

	key := func(k string) tea.KeyMsg {
		switch k {
		case "down":
			return tea.KeyMsg{Type: tea.KeyDown}
		case "enter":
			return tea.KeyMsg{Type: tea.KeyEnter}
		}
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	step := func(what string, msg tea.Msg) {
		t.Helper()
		n.Store(0)
		nm, cmd := m.Update(msg)
		if got := n.Load(); got != 0 {
			t.Fatalf("%s: Update read %d times through the Source itself", what, got)
		}
		m = nm.(Model).settle(cmd)
	}

	m.focus = focusFiles
	step("down", key("down"))
	step("v", key("v"))
	step("v again", key("v"))
	step("b", key("b"))
	step("b again", key("b"))
	step("tick", tickMsg{})

	m = openFileDiff(t, m, "lib.ts")
	m.diffCursor = annotationIndex(t, m)
	want := m.diffLines[m.diffCursor].refLine
	step("enter on a reference", key("enter"))
	if m.peek == nil || !strings.Contains(m.diff, "export function use()") {
		t.Fatalf("the jump did not land: peek %v, diff %q", m.peek, m.diff)
	}
	if got := m.diffLines[m.diffCursor].newLine; got != want {
		t.Fatalf("the jump put the cursor on line %d, want %d", got, want)
	}
	step("u", key("u"))
	if m.peek != nil {
		t.Fatal("u did not go back")
	}

	step("w", key("w"))
	if !m.overview || len(m.ovItems) != 1 {
		t.Fatalf("the overview did not list the worktree: %+v", m.ovItems)
	}
	step("enter in the overview", key("enter"))
	if m.overview || len(m.files) == 0 {
		t.Fatalf("enter in the overview did not show the worktree: %v", m.files)
	}
}

// TestADiffTakingAWhileSaysSo: until a diff comes the pane keeps what it
// had, and once it takes a while its title has a spinner.
func TestADiffTakingAWhileSaysSo(t *testing.T) {
	m := NewEmbedded(Local{})
	m.width, m.height = 120, 40
	m.worktrees, m.curWT = []git.Worktree{{Path: "/wt"}}, "/wt"
	m.files = []git.FileStatus{{Path: "a.go", Code: " M"}, {Path: "b.go", Code: " M"}}
	m.rebuildTree("")
	m.diff = "old"

	cmd := m.loadDiff()
	if cmd == nil || !m.diffPending || m.diff != "old" {
		t.Fatalf("the diff should load in the background, keeping the old: %v %q", m.diffPending, m.diff)
	}
	m = m.send(diffSlowMsg{token: m.diffToken - 1})
	if m.diffSlow {
		t.Fatal("an overtaken load is not the one taking a while")
	}
	m = m.send(diffSlowMsg{token: m.diffToken})
	if !m.diffSlow || !strings.Contains(stripANSI(m.View()), m.spinner.View()) {
		t.Fatal("a load taking a while shows a spinner")
	}
	path, _ := m.currentFile()
	m.onDiff(diffMsg{wt: "/wt", path: path, token: m.diffToken, diff: "@@ -1 +1 @@\n+new"})
	if m.diffSlow || m.diffPending || m.diff == "old" {
		t.Fatalf("the diff that came is shown, without a spinner: %v %q", m.diffSlow, m.diff)
	}
}
