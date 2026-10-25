package review

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/order"
	"github.com/nerdr-dev/nerdr/internal/store"
)

func orderModel(t *testing.T) Model {
	t.Helper()
	files := []git.FileStatus{
		{Path: "internal/api/handler.go", Code: " M"},
		{Path: "internal/cache/cache.go", Code: " M"},
		{Path: "internal/cache/cache_test.go", Code: "??"},
		{Path: "internal/ui/view.go", Code: " M"},
	}
	m := Model{height: 40, width: 120, showOrder: true, expanded: map[string]bool{}, recent: map[string]int{}}
	m.files = files
	return m
}

// TestReadingOrderPutsTheChangeFirstAndFoldsTheRepetition covers what the
// order is for: the file carrying the change comes first, the same edit
// repeated is last and collapsed, and files nobody described are still
// listed rather than quietly dropped.
func TestReadingOrderPutsTheChangeFirstAndFoldsTheRepetition(t *testing.T) {
	m := orderModel(t)
	ix := indexFrom(t,
		order.Entry{File: "internal/cache/cache.go", Role: order.RoleCore, Short: "the new cache"},
		order.Entry{File: "internal/cache/cache_test.go", Role: order.RoleTest, Short: "pins the eviction rule"},
		order.Entry{File: "internal/api/handler.go", Role: order.RoleMechanical, Short: "call sites"},
	)
	m.orderIndex = ix
	m.rebuildTree("")

	var sections, files []string
	for _, r := range m.flatRows() {
		if r.node.isDir {
			sections = append(sections, r.node.name)
		} else {
			files = append(files, r.node.path)
		}
	}
	wantSections := "the change itself | tests | mechanical | not described"
	if got := strings.Join(sections, " | "); got != wantSections {
		t.Fatalf("sections %q, want %q", got, wantSections)
	}
	wantFiles := "internal/cache/cache.go internal/cache/cache_test.go internal/ui/view.go"
	if got := strings.Join(files, " "); got != wantFiles {
		t.Fatalf("visible files %q, want %q (mechanical stays collapsed)", got, wantFiles)
	}

	// The one-line description of each file is on its row.
	if rows := strings.Join(m.treeItems(100), "\n"); !strings.Contains(rows, "the new cache") {
		t.Errorf("expected each file's description on its row:\n%s", rows)
	}
}

// TestTCyclesTheFileList covers t: from the reading order to the list
// as it is without one, to the folders, and back to the reading order.
func TestTCyclesTheFileList(t *testing.T) {
	m := orderModel(t)
	m.orderIndex = indexFrom(t, order.Entry{
		File: "internal/cache/cache.go", Role: order.RoleCore, Short: "the new cache"})
	m.rebuildTree("")
	m.focus = focusFiles

	press := func() {
		nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
		m = nm.(Model)
	}
	press()
	if m.showOrder || m.folderTree {
		t.Fatal("t should go from the reading order to the ordinary list")
	}
	for _, r := range m.flatRows() {
		if r.node.isDir && strings.Contains(r.node.name, "the change itself") {
			t.Fatal("the ordinary list should not carry the order's sections")
		}
	}
	press()
	if !m.folderTree {
		t.Fatal("t should go on to the folders")
	}
	press()
	if !m.showOrder || m.folderTree {
		t.Fatal("t should come round to the reading order")
	}

	// Without a reading order, t goes between the other two.
	m.orderIndex = order.Index{}
	press()
	press()
	if m.folderTree {
		t.Fatal("without a reading order, the folders lead back to the ordinary list")
	}
}

func TestToggleSaysSoWhenNothingHasBeenRecorded(t *testing.T) {
	m := orderModel(t)
	m.toggleOrder()
	if m.showOrder != true || !strings.Contains(m.toast, "no reading order") {
		t.Fatalf("expected an explanation, got toast %q", m.toast)
	}
}

// indexFrom builds an Index the way Load does, without a git repository.
func indexFrom(t *testing.T, entries ...order.Entry) order.Index {
	t.Helper()
	path := filepath.Join(t.TempDir(), "order.jsonl")
	for _, e := range entries {
		if err := store.Append(path, e); err != nil {
			t.Fatal(err)
		}
	}
	return order.LoadFile(path, "")
}

// TestFolderTreeListsFilesWhereTheyLive covers t: folders before files,
// each by name, a folder holding only a folder joined with it, and no
// folder repeated behind a file's name.
func TestFolderTreeListsFilesWhereTheyLive(t *testing.T) {
	m := orderModel(t)
	m.files = append(m.files, git.FileStatus{Path: "README.md", Code: " M"})
	m.rebuildTree("")
	m.toggleFolderTree()

	var rows []string
	for _, r := range m.flatRows() {
		rows = append(rows, strings.Repeat(">", r.depth)+r.node.name)
	}
	want := "internal >api >>handler.go >cache >>cache.go >>cache_test.go >ui >>view.go README.md"
	if got := strings.Join(rows, " "); got != want {
		t.Fatalf("rows\n %q, want\n %q", got, want)
	}

	// A folder with one folder in it shares its row.
	m.files = []git.FileStatus{{Path: "internal/review/view.go", Code: " M"}}
	m.rebuildTree("")
	if rows := m.flatRows(); len(rows) != 2 || rows[0].node.name != "internal/review" {
		t.Fatalf("expected internal/review joined, got %d rows, first %q", len(rows), rows[0].node.name)
	}
	if items := strings.Join(m.treeItems(100), "\n"); strings.Count(items, "internal/review") != 1 {
		t.Fatalf("the folder is above the file, not behind it too:\n%s", items)
	}

	m.toggleFolderTree()
	if m.folderTree {
		t.Fatal("t goes back")
	}
}

// TestDSwitchesBetweenExplanationAndFolder covers d: the explanation
// behind the name, or the folder, in any view; e asks for it whole.
func TestDSwitchesBetweenExplanationAndFolder(t *testing.T) {
	m := orderModel(t)
	m.orderIndex = indexFrom(t, order.Entry{File: "internal/cache/cache.go", Role: order.RoleCore, Short: "the new cache"})
	m.rebuildTree("internal/cache/cache.go")
	row := func() string {
		for _, it := range m.treeItems(100) {
			if strings.Contains(it, "cache.go") && !strings.Contains(it, "cache_test") {
				return it
			}
		}
		t.Fatal("no row for cache.go")
		return ""
	}
	if r := row(); !strings.Contains(r, "the new cache") || strings.Contains(r, "internal/cache") {
		t.Fatalf("expected the explanation behind the name: %q", r)
	}
	m.toggleFolderNote()
	if r := row(); strings.Contains(r, "the new cache") || !strings.Contains(r, "internal/cache") {
		t.Fatalf("d puts the folder there: %q", r)
	}
	m.toggleFolderNote()
	m.toggleOrder() // the import view keeps the explanation too
	if r := row(); !strings.Contains(r, "the new cache") {
		t.Fatalf("expected the explanation in the import view: %q", r)
	}

	m.wtIdx = -1
	for i, r := range m.flatRows() {
		if r.node.path == "internal/cache/cache.go" {
			m.cursor = i
		}
	}
	cmd := m.explainFile()
	if cmd == nil {
		t.Fatal("e on a described file shows it")
	}
	if msg, ok := cmd().(ExplainMsg); !ok || msg.Short != "the new cache" || msg.Role != "the change itself" {
		t.Fatalf("e: %#v", cmd())
	}
}

// TestFilesHintButtons covers the file list's hint row: e is offered only
// on a file with an explanation, a only beside an agent with the skill,
// and each key on the row is a button.
func TestFilesHintButtons(t *testing.T) {
	m := orderModel(t)
	m.orderIndex = indexFrom(t, order.Entry{File: "internal/cache/cache.go", Role: order.RoleCore, Short: "the new cache"})
	m.rebuildTree("internal/cache/cache.go")
	m.wtIdx = -1
	put := func(file string) {
		for i, r := range m.flatRows() {
			if r.node.path == file {
				m.cursor = i
				return
			}
		}
		t.Fatalf("no row for %s", file)
	}

	put("internal/cache/cache_test.go")
	if h := m.filesHint(); strings.Contains(h, "e explanation") || strings.Contains(h, "a agent") {
		t.Fatalf("nothing to explain, no agent: %q", h)
	}
	put("internal/cache/cache.go")
	h := m.filesHint()
	if !strings.Contains(h, "e explanation") {
		t.Fatalf("expected e on a described file: %q", h)
	}
	at := lipgloss.Width(h[:strings.Index(h, "e explanation")])
	cmd := m.clickFilesHint(at+2, 100)
	if cmd == nil {
		t.Fatal("clicking e shows nothing")
	}
	if _, ok := cmd().(ExplainMsg); !ok {
		t.Fatalf("clicking e: %#v", cmd())
	}
	if m.clickFilesHint(at+2, at+5) != nil {
		t.Fatal("a key cut off by the pane's edge isn't a button")
	}

	m.embedded, m.explainOn = true, true
	if h := m.filesHint(); !strings.Contains(h, "E agent, explain") {
		t.Fatalf("expected E beside an agent with the skill: %q", h)
	}
	wasOrder := m.showOrder && m.orderIndex.Len() > 0
	m.clickFilesHint(lipgloss.Width("/ filter · ")+1, 100)
	if wasOrder && m.showOrder || !wasOrder && !m.folderTree {
		t.Fatal("clicking t steps the file list on")
	}
}

// TestWalkthroughButton covers g on the file list's hint row: offered once
// there is a walkthrough, or an agent with the skill to ask for one, and
// clicking it without one asks the agent, once, and opens the walkthrough
// when it is written.
func TestWalkthroughButton(t *testing.T) {
	m := orderModel(t)
	m.rebuildTree("")
	m.worktrees = []git.Worktree{{Path: t.TempDir(), Branch: "work"}}
	m.curWT = m.worktrees[0].Path
	if h := m.filesHint(); strings.Contains(h, "g ") {
		t.Fatalf("no walkthrough and no agent to ask: %q", h)
	}
	m.walk = true
	if h := m.filesHint(); !strings.Contains(h, "g guide") {
		t.Fatalf("expected g with a walkthrough: %q", h)
	}

	m.walk, m.embedded, m.explainOn = false, true, true
	h := m.filesHint()
	i := strings.Index(h, "g make guide")
	if i < 0 {
		t.Fatalf("expected g to make one beside an agent with the skill: %q", h)
	}
	cmd := m.clickFilesHint(lipgloss.Width(h[:i])+2, 200)
	if cmd == nil {
		t.Fatal("clicking g does nothing")
	}
	if msg, ok := cmd().(AgentMsg); !ok || msg.Text != walkthroughRequest {
		t.Fatalf("clicking g without a walkthrough: %#v", cmd())
	}
	if h := m.filesHint(); !strings.Contains(h, "g guide…") {
		t.Fatalf("expected g to show the agent is writing it: %q", h)
	}
	if m.openWalkthrough() != nil {
		t.Fatal("g asks the agent again while it writes the walkthrough")
	}
	if cmd := m.openWalkthrough(); cmd == nil {
		t.Fatal("g twice more does not ask anew, for an agent that never writes it")
	} else if msg, ok := cmd().(AgentMsg); !ok || msg.Text != walkthroughRequest {
		t.Fatalf("asking anew: %#v", msg)
	}

	if m.walkArrived() != nil {
		t.Fatal("opens a walkthrough that is not there")
	}
	m.walk = true
	if m.walkArrived() == nil {
		t.Fatal("the walkthrough asked for does not open when it is written")
	}
	if m.walkWanted != "" || m.walkArrived() != nil {
		t.Fatal("the walkthrough opens more than once")
	}
}
