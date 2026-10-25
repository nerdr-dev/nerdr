package review

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/proc"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDiffLeavesOutGitsHeader(t *testing.T) {
	lines := buildDiffLines(`diff --git a/f.go b/f.go
new file mode 100644
index 0000000..1111111
--- /dev/null
+++ b/f.go
@@ -0,0 +1,2 @@
+package f
+-- not a header
\ No newline at end of file`)
	var got []string
	for _, l := range lines {
		got = append(got, l.text)
	}
	want := []string{"new file mode 100644", "@@ -0,0 +1,2 @@", "+package f", "+-- not a header", `\ No newline at end of file`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
}

// TestPanelBesideAnAgent: no relations pane and no status bar, the diff
// down to the bottom, and the toast handed to nerdr.
func TestPanelBesideAnAgent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := filepath.Join(t.TempDir(), "repo")
	runGit(t, "", "init", "-q", repo)
	if err := writeFileDirs(filepath.Join(repo, "a.go"), "package a\n\nfunc A() {}\n"); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "init")
	if err := writeFileDirs(filepath.Join(repo, "a.go"), "package a\n\nfunc A() { B() }\n\nfunc B() {}\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeFileDirs(filepath.Join(repo, "b.go"), "package a\n"); err != nil {
		t.Fatal(err)
	}

	m := NewEmbedded()
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = nm.(Model)
	m = pinNow(t, m, []Pin{{Repo: repo, Path: repo}})
	m = m.SetShared(map[string][]string{SharedKey(repo, "a.go"): {"login"}})

	view := stripANSI(m.View())
	rows := strings.Split(view, "\n")
	if len(rows) != 30 {
		t.Fatalf("%d rows, want 30:\n%s", len(rows), view)
	}
	if !strings.HasPrefix(rows[29], "╰") {
		t.Fatalf("the diff goes down to the bottom, no status bar under it: %q", rows[29])
	}
	for _, gone := range []string{"1 github", "diff --git", "index "} {
		if strings.Contains(view, gone) {
			t.Fatalf("%q is gone:\n%s", gone, view)
		}
	}
	if !strings.Contains(view, "a.go +3 −1 ⚠") || !strings.Contains(view, "b.go +1") {
		t.Fatalf("the files' counts and marks:\n%s", view)
	}
	if !strings.Contains(view, "[⚠ also changed in login]") {
		t.Fatalf("the diff says which tab changes it too:\n%s", view)
	}

	m.setToast("opened in the browser")
	if s := m.PopToast(); s != "opened in the browser" || m.toast != "" {
		t.Fatalf("toast %q, left %q", s, m.toast)
	}
	m.focus = focusDiff
	m.focusRight()
	if m.focus != focusDiff {
		t.Fatal("no pane to the right of the diff")
	}
}

func TestWhyNoteOpensInTheDiff(t *testing.T) {
	m := NewEmbedded()
	m.width, m.height = 80, 30
	m.diffLines = []diffLine{
		{kind: '@', text: "@@ -1 +1 @@"},
		{kind: 'w', text: "  ✎ short", whyDetail: "claude-code · today\n\nthe long reason"},
		{kind: '+', text: "+x := 1", newLine: 1},
	}
	m.diffCursor = 1
	if !m.openWhyDetail() {
		t.Fatal("enter on the ✎ line opens it")
	}
	var texts []string
	for _, l := range m.diffLines {
		texts = append(texts, strings.TrimSpace(l.text))
	}
	if strings.Join(texts, "|") != "@@ -1 +1 @@|✎ short|claude-code · today||the long reason|+x := 1" {
		t.Fatalf("opened under the line: %q", texts)
	}
	if m.focus == focusRelations {
		t.Fatal("the keyboard stays in the diff")
	}
	m.openWhyDetail()
	if len(m.diffLines) != 3 {
		t.Fatalf("enter again closes it: %d lines", len(m.diffLines))
	}
}

func TestFileMarks(t *testing.T) {
	m := NewEmbedded()
	m.fileStats = map[string]git.LineCount{"a": {Add: 2}, "b": {Del: 4}, "c": {Binary: true}}
	for path, want := range map[string]string{"a": " +2 ✎", "b": " −4", "c": " bin", "d": ""} {
		if got := stripANSI(m.fileMarks(path, path == "a")); got != want {
			t.Errorf("%s: %q, want %q", path, got, want)
		}
	}
}

// E in the file list asks the worktree's agent to explain its change, in
// the words its nerdr-explain skill is used on.
func TestExplainAsksTheAgent(t *testing.T) {
	m := NewEmbedded()
	m.worktrees = []git.Worktree{{Path: "/a"}}
	m.wtIdx, m.focus = 0, focusFiles
	E := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("E")}
	if _, cmd := m.Update(E); cmd != nil {
		t.Fatal("with the skill off, E asks for nothing")
	}
	m = m.SetExplain(true)
	_, cmd := m.Update(E)
	if cmd == nil {
		t.Fatal("E sends nothing")
	}
	if msg, ok := cmd().(AgentMsg); !ok || msg.Dir != "/a" || !strings.Contains(strings.ToLower(msg.Text), "explain the code for nerdr") {
		t.Fatalf("E: %#v", cmd())
	}
}

// ? in nerdr's panel is help, as everywhere in nerdr; v steps the diff
// through the whole file and the old code, back to the diff.
func TestQuestionMarkIsHelpAndVStepsTheDiff(t *testing.T) {
	m := NewEmbedded()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if cmd == nil {
		t.Fatal("? asks for nothing")
	}
	if _, ok := cmd().(HelpMsg); !ok {
		t.Fatalf("?: %#v", cmd())
	}
	v := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")}
	for _, want := range []diffMode{diffModeNewFull, diffModeOld, diffModeHunk} {
		nm, _ := m.Update(v)
		m = nm.(Model)
		if m.diffMode != want {
			t.Fatalf("v: diff mode %v, want %v", m.diffMode, want)
		}
	}
}

// pinNow pins pins and waits for what loadPinned reads in the background,
// as the panel shows it once it has arrived; the diff is read with it.
func pinNow(t *testing.T, m Model, pins []Pin) Model {
	t.Helper()
	m, cmd := m.Pin(pins)
	return settlePin(m, cmd)
}

// settlePin hands m what cmd reads of a pinned worktree.
func settlePin(m Model, cmd tea.Cmd) Model {
	for _, msg := range runCmds(cmd) {
		switch msg.(type) {
		case pinnedMsg, diffMsg:
			nm, more := m.Update(msg)
			m = nm.(Model)
			for _, msg := range runCmds(more) {
				if _, ok := msg.(diffMsg); ok {
					nm, _ := m.Update(msg)
					m = nm.(Model)
				}
			}
		}
	}
	return m
}

// runCmds runs cmd and the commands a batch of it holds, and returns the
// messages they make.
func runCmds(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, runCmds(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// TestPinRunsNoGitOnTheSpot: pinning a worktree, as a tab switch does,
// reads it in the background, and not at all while the panel is hidden.
func TestPinRunsNoGitOnTheSpot(t *testing.T) {
	parent := setupJumpRepo(t, map[string]string{"a.go": "package a\n"}, nil)
	repo := filepath.Join(parent, "repo")
	if err := writeFileDirs(filepath.Join(repo, "b.go"), "package a\n"); err != nil {
		t.Fatal(err)
	}
	gitCalls := func() int {
		calls, _ := proc.Counts()
		n := 0
		for c, k := range calls {
			if c.Program == "git" {
				n += k
			}
		}
		return n
	}
	m := NewEmbedded()
	m, _ = m.SetShown(false)
	before := gitCalls()
	m, cmd := m.Pin([]Pin{{Repo: repo, Path: repo}})
	if gitCalls() != before || cmd != nil {
		t.Fatal("a hidden panel should read nothing")
	}
	m, cmd = m.SetShown(true)
	if gitCalls() != before || cmd == nil {
		t.Fatal("shown, the panel should read in the background")
	}
	m = settlePin(m, cmd)
	if len(m.files) == 0 {
		t.Fatal("the files should arrive")
	}
}
