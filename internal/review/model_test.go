package review

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/charmbracelet/x/ansi"
	"nerdr/internal/git"
)

func TestFolderArrowNavigation(t *testing.T) {
	for _, embedded := range []bool{false, true} {
		m := NewEmbedded()
		m.embedded = embedded
		m.tree = buildFolderTree([]git.FileStatus{{Path: "src/main.go", Code: " M"}})
		folder := m.tree[0]
		for _, step := range []struct {
			key      tea.KeyMsg
			expanded bool
		}{
			{tea.KeyMsg{Type: tea.KeyLeft}, false},
			{tea.KeyMsg{Type: tea.KeyLeft}, false},
			{tea.KeyMsg{Type: tea.KeyRight}, true},
			{tea.KeyMsg{Type: tea.KeyRight}, true},
			{tea.KeyMsg{Type: tea.KeyEnter}, false},
			{tea.KeyMsg{Type: tea.KeyEnter}, true},
			{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}}, false},
			{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}}, true},
		} {
			next, _ := m.Update(step.key)
			m = next.(Model)
			if m.isExpanded(folder) != step.expanded || m.focus != focusFiles || m.cursor != 0 {
				t.Fatalf("embedded=%v key=%s: expanded=%v focus=%v cursor=%d", embedded, step.key, m.isExpanded(folder), m.focus, m.cursor)
			}
		}
	}
}

func TestSinceVisitMarkerSurvivesLiveMarkerDecayAndIsWorktreeScoped(t *testing.T) {
	m := NewEmbedded()
	m.worktrees = []git.Worktree{{Path: "/a"}, {Path: "/b"}}
	m.files = []git.FileStatus{{Path: "a.txt", Code: " M"}}
	m.rebuildTree("")
	m = m.SetUnseen(map[string]map[string]bool{"/a": {"a.txt": true}})
	m.recent["a.txt"] = 1
	m.decay()
	view := ansi.Strip(strings.Join(m.treeItems(80), "\n"))
	if !strings.Contains(view, "◆") {
		t.Fatal(view)
	}
	m.wtIdx = 1
	view = ansi.Strip(strings.Join(m.treeItems(80), "\n"))
	if strings.Contains(view, "◆") {
		t.Fatal("marker leaked to another worktree")
	}
}

// TestModelNavigation drives the model through repo -> worktree -> file ->
// diff using a real temporary repository with a linked worktree.
func TestModelNavigation(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	parent := t.TempDir()
	repo := filepath.Join(parent, "myrepo")
	runGit(t, "", "init", "-q", repo)
	writeFile(t, filepath.Join(repo, "a.txt"), "hello\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "init")

	wt := filepath.Join(parent, "myrepo-wt")
	runGit(t, repo, "worktree", "add", "-q", "-b", "feature/x", wt)
	writeFile(t, filepath.Join(wt, "a.txt"), "hello\nworld\n")

	m := New(parent)
	if len(m.repos) != 1 || m.repos[0].Name != "myrepo" {
		t.Fatalf("expected one repo, got %#v", m.repos)
	}

	send := func(msg tea.Msg) {
		nm, _ := m.Update(msg)
		m = nm.(Model)
	}

	// repos -> worktrees
	send(tea.KeyMsg{Type: tea.KeyRight})
	if len(m.worktrees) != 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(m.worktrees))
	}

	// select the feature worktree (second in list order, sorted by path)
	m.wtIdx = 1
	send(tea.KeyMsg{Type: tea.KeyRight})
	if len(m.files) != 1 || m.files[0].Path != "a.txt" || m.files[0].Code != " M" {
		t.Fatalf("expected one modified file a.txt, got %#v", m.files)
	}

	// files -> diff: move the cursor onto the file row, then descend
	send(tea.KeyMsg{Type: tea.KeyDown})
	send(tea.KeyMsg{Type: tea.KeyEnter})
	if m.diff == "" {
		t.Fatalf("expected non-empty diff")
	}
}

// TestSwitchingRepoResetsWorktreeIndex covers the exact bug reported: with
// a non-zero worktree selected in one repo, moving to a different repo that
// also has multiple worktrees must default to that repo's primary worktree
// (index 0), not reuse the previous repo's numeric index — which happened
// to point at an unrelated worktree there. A single-worktree repo always
// clamped back to 0 regardless, which is why this only showed up with more
// than one worktree per repo.
func TestSwitchingRepoResetsWorktreeIndex(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	parent := t.TempDir()
	makeRepoWithTwoWorktrees := func(name string) {
		repo := filepath.Join(parent, name)
		runGit(t, "", "init", "-q", repo)
		writeFile(t, filepath.Join(repo, "a.txt"), "hello\n")
		runGit(t, repo, "add", ".")
		runGit(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "init")
		wt := filepath.Join(parent, name+"-wt")
		runGit(t, repo, "worktree", "add", "-q", "-b", "feature/"+name, wt)
		writeFile(t, filepath.Join(wt, "a.txt"), "hello\n"+name+"\n")
	}
	// Names sorted so "repo-a" < "repo-b" both as repo list order and as
	// worktree list order (main checkout before the linked "-wt" one).
	makeRepoWithTwoWorktrees("repo-a")
	makeRepoWithTwoWorktrees("repo-b")

	m := New(parent)
	if len(m.repos) != 2 {
		t.Fatalf("expected 2 repos, got %#v", m.repos)
	}

	send := func(msg tea.Msg) {
		nm, _ := m.Update(msg)
		m = nm.(Model)
	}

	// Enter repo-a's worktrees and select its second (non-primary) one.
	send(tea.KeyMsg{Type: tea.KeyRight})
	if len(m.worktrees) != 2 {
		t.Fatalf("expected 2 worktrees in repo-a, got %d", len(m.worktrees))
	}
	m.wtIdx = 1
	m.focus = focusWorktrees
	send(tea.KeyMsg{Type: tea.KeyRight}) // load repo-a's files, so there is something to clear
	selectedBranch := m.worktrees[1].Branch
	if len(m.files) == 0 {
		t.Fatal("expected repo-a's worktree to have changed files for this test to mean anything")
	}

	// Back out to the repos pane and move to repo-b.
	m.focus = focusRepos
	send(tea.KeyMsg{Type: tea.KeyDown})

	if m.wtIdx != 0 {
		t.Fatalf("expected wtIdx reset to 0 for the new repo, got %d (worktree %v)", m.wtIdx, m.worktrees)
	}
	if len(m.worktrees) != 2 {
		t.Fatalf("expected 2 worktrees in repo-b, got %d", len(m.worktrees))
	}
	if m.worktrees[m.wtIdx].Branch == selectedBranch {
		t.Fatalf("landed on repo-b's worktree that coincidentally shares repo-a's selected branch name %q — test setup ambiguous", selectedBranch)
	}
	// Changing repo must not leave the previous repo's files on screen; it
	// also must not load any, since which files changed depends on which
	// worktree is picked next.
	if len(m.files) != 0 || len(m.tree) != 0 || m.diff != "" {
		t.Fatalf("expected the files pane cleared on repo change, got %d files / %d tree roots / diff %q",
			len(m.files), len(m.tree), m.diff)
	}

	// Entering the worktree pane is what loads them, for the worktree the
	// cursor is on.
	send(tea.KeyMsg{Type: tea.KeyRight})
	if m.curWT != m.worktrees[0].Path {
		t.Fatalf("expected repo-b's primary worktree to be loaded, got %q", m.curWT)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCurrentNewLineIsTheLineUnderTheDiffCursor covers what "o" hands vim:
// the line being read, not line 1. Rows with no line of their own — the
// hunk header, an ✎ rationale — belong to the code below them.
func TestCurrentNewLineIsTheLineUnderTheDiffCursor(t *testing.T) {
	m := Model{
		diffLines: []diffLine{
			{kind: '@', text: "@@ -1,3 +1,4 @@"},
			{kind: ' ', text: " one", newLine: 1},
			{kind: 'w', text: "  ✎ why the next line changed"},
			{kind: '+', text: "+two", newLine: 2},
			{kind: ' ', text: " three", newLine: 3},
		},
	}

	for _, c := range []struct{ cursor, want int }{
		{0, 1}, // hunk header: the first line it introduces
		{1, 1},
		{2, 2}, // rationale: the line it explains, which follows it
		{3, 2},
		{4, 3},
	} {
		m.diffCursor = c.cursor
		if got := m.currentNewLine(); got != c.want {
			t.Errorf("cursor %d: got line %d, want %d", c.cursor, got, c.want)
		}
	}

	// Nothing selected in an empty diff: no +N argument at all, rather than
	// vim being sent to a line that doesn't exist.
	empty := Model{}
	if got := empty.currentNewLine(); got != 0 {
		t.Errorf("empty diff: got line %d, want 0", got)
	}
}

// TestEditorOpensTheFileOnTheLineBeingRead covers the o key handing the
// editor the real worktree path and the line the cursor is on.
func TestEditorOpensTheFileOnTheLineBeingRead(t *testing.T) {
	m := Model{
		worktrees: []git.Worktree{{Path: "/wt"}},
		tree:      []*treeNode{{name: "src/a.go", path: "src/a.go"}},
		diffLines: []diffLine{{kind: '+', text: "+x", newLine: 42}},
	}
	m.diffCursor = 0

	path, line, ok := m.editorTarget()
	if !ok {
		t.Fatal("expected the selected file to be openable")
	}
	if want := filepath.FromSlash("/wt/src/a.go"); path != want || line != 42 {
		t.Fatalf("got %q line %d, want %q line 42", path, line, want)
	}

	// Nothing selected: no editor at all, rather than one on a directory.
	empty := Model{}
	if _, _, ok := empty.editorTarget(); ok {
		t.Fatal("expected no editor command when no file is selected")
	}
}

// TestHidingIgnoredFilesLeavesTheFileListAlone covers the filter working on
// a copy: filtering m.files in place corrupted it for every later refresh.
func TestHidingIgnoredFilesLeavesTheFileListAlone(t *testing.T) {
	files := []git.FileStatus{{Path: "a.go"}, {Path: "dist/x.js"}, {Path: "b.go"}}
	if got := hideIgnoredFiles(files); len(got) != 2 {
		t.Fatalf("kept %v", got)
	}
	if files[1].Path != "dist/x.js" || files[2].Path != "b.go" {
		t.Fatalf("the list filtered was changed: %v", files)
	}
}

// TestEditingAChangedFileReloadsItsDiff covers an agent carrying on with a
// file already in the change: the list stays as it was, the diff must not.
func TestEditingAChangedFileReloadsItsDiff(t *testing.T) {
	parent := setupJumpRepo(t, map[string]string{"a.go": "package a\n"}, nil)
	repo := filepath.Join(parent, "repo")
	writeFile(t, filepath.Join(repo, "a.go"), "package a\n\nvar first = 1\n")

	m := New(parent)
	m = m.send(tea.KeyMsg{Type: tea.KeyRight})
	m = m.send(tea.KeyMsg{Type: tea.KeyRight})
	if path, ok := m.currentFile(); !ok || path != "a.go" {
		t.Fatalf("setup: on %q", path)
	}
	if !strings.Contains(m.diff, "first") {
		t.Fatalf("setup: diff %q", m.diff)
	}

	writeFile(t, filepath.Join(repo, "a.go"), "package a\n\nvar first = 1\nvar second = 2\n")
	m = m.settle(m.refreshFiles())
	if !strings.Contains(m.diff, "second") {
		t.Fatalf("the diff was not reloaded after the edit:\n%s", m.diff)
	}
	if m.recent["a.go"] == 0 {
		t.Fatal("the edit should mark the file as just touched")
	}
}

// TestOverviewCountsInTheBackground covers the overview opening at once
// and its counts arriving afterwards, for the overview they were made for.
func TestOverviewCountsInTheBackground(t *testing.T) {
	parent := setupJumpRepo(t, map[string]string{"a.go": "package a\n"}, map[string]string{"b.go": "package b\n"})
	m := New(parent)
	cmd := m.toggleOverview()
	if !m.overview || len(m.ovItems) != 1 || m.ovItems[0].counted {
		t.Fatalf("expected one uncounted item at once, got %+v", m.ovItems)
	}
	msg := cmd()
	stale := msg.(ovStatsMsg)
	stale.token--
	if m = m.send(stale); m.ovItems[0].counted {
		t.Fatal("counts for an earlier overview must be dropped")
	}
	m = m.send(msg)
	if it := m.ovItems[0]; !it.counted || it.files != 1 || it.add != 1 {
		t.Fatalf("counts %+v, want 1 file +1", it)
	}
}

func (m Model) send(msg tea.Msg) Model {
	nm, _ := m.Update(msg)
	return nm.(Model)
}

// settle runs cmd, and what the panel does with its answers, as the
// program would: the file list and diff refreshed in the background come
// back. A command that waits longer (a tick, the next change on a watch)
// is left.
func (m Model) settle(cmd tea.Cmd) Model {
	var queue []tea.Cmd
	if cmd != nil {
		queue = append(queue, cmd)
	}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		got := make(chan tea.Msg, 1)
		go func() { got <- c() }()
		var msg tea.Msg
		select {
		case msg = <-got:
		case <-time.After(300 * time.Millisecond):
			continue
		}
		switch msg := msg.(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				if c != nil {
					queue = append(queue, c)
				}
			}
		case filesMsg, diffMsg, projectMsg:
			nm, next := m.Update(msg)
			m = nm.(Model)
			if next != nil {
				queue = append(queue, next)
			}
		}
	}
	return m
}

// TestAskTurnsWhereYouStandIntoAMessage covers ? and s: a message for the
// worktree's agent about the line, the file or the whole change, with the
// question left for the user to write after s.
func TestAskTurnsWhereYouStandIntoAMessage(t *testing.T) {
	m := Model{
		focus: focusDiff, height: 40, curWT: "/wt",
		worktrees: []git.Worktree{{Path: "/wt"}},
		diffLines: []diffLine{
			{kind: '@', text: "@@ -1,2 +1,2 @@"},
			{kind: '+', text: "+x := clamp(v, 0, 9)", newLine: 12},
		},
		diffCursor: 1,
	}
	msg := agentText(t, m.askSelected(false))
	if msg.Dir != "/wt" || !strings.Contains(msg.Text, "`x := clamp(v, 0, 9)`") ||
		!strings.Contains(msg.Text, ":12") || !strings.Contains(msg.Text, lineQuestion) {
		t.Errorf("? on a line: %#v", msg)
	}
	if msg := agentText(t, m.askSelected(true)); !strings.HasSuffix(msg.Text, ": ") {
		t.Errorf("s should leave the question to the user: %q", msg.Text)
	}

	m.diffCursor = 0
	if cmd := m.askSelected(false); cmd != nil {
		t.Error("a hunk header is not a line to ask about")
	}

	m.focus = focusWorktrees
	if msg := agentText(t, m.askSelected(false)); !strings.Contains(msg.Text, "whole change") ||
		!strings.Contains(msg.Text, changeQuestion) {
		t.Errorf("? on the worktree: %q", msg.Text)
	}
}

// TestEmptyChangeOffersTheBranch covers a branch whose work is all
// committed: the file pane says why it is empty, and its button (or b)
// shows the branch against its base, deleted files included.
func TestEmptyChangeOffersTheBranch(t *testing.T) {
	parent := setupJumpRepo(t, map[string]string{"gone.sh": "echo hi\n"}, nil)
	repo := filepath.Join(parent, "repo")
	runGit(t, repo, "branch", "-M", "main")
	runGit(t, repo, "checkout", "-q", "-b", "feat")
	runGit(t, repo, "rm", "-q", "gone.sh")
	runGit(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "drop")

	m := NewEmbedded()
	m = pinNow(t, m, []Pin{{Repo: repo, Path: repo}})
	m = m.send(tea.WindowSizeMsg{Width: 100, Height: 40})
	m.refreshFiles()
	if got := stripANSI(m.View()); !strings.Contains(got, "No uncommitted changes") ||
		!strings.Contains(got, "b: show the branch vs main") {
		t.Fatalf("an empty change should explain itself:\n%s", got)
	}

	l := m.layout(100, 40)
	_, button := m.emptyFiles()
	m.handleMouse(tea.MouseMsg{X: l.files.x + 3, Y: l.files.y + 2 + m.filesHeader() + button,
		Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if !m.showBase || len(m.files) != 1 || m.files[0].Path != "gone.sh" || m.files[0].Code[0] != 'D' {
		t.Fatalf("the button should show the branch, deletion included: base %v files %v", m.showBase, m.files)
	}
}
