package review

import (
	"strings"
	"testing"

	"nerdr/internal/git"

	tea "github.com/charmbracelet/bubbletea"
)

// A tab on several repositories has a bar of them at the top of F1's file
// list, each with the files its branch changes; the one shown counts its
// own list.
func TestRepoBar(t *testing.T) {
	m := NewEmbedded()
	m.pins = []Pin{{Repo: "/r/front", Path: "/w/front"}, {Repo: "/r/back", Path: "/w/back"}, {Repo: "/r/lib", Path: "/w/lib"}}
	m.repos = []git.Repo{{Name: "front"}, {Name: "back"}, {Name: "lib"}}
	m = m.SetPinCounts([]int{9, 2, -1})
	m.files = make([]git.FileStatus, 3)
	bar := stripANSI(m.repoBarView(80))
	for _, want := range []string{" front 3 ", " back 2 ", " lib ", "[ ] switch"} {
		if !strings.Contains(bar, want) {
			t.Errorf("missing %q in %q", want, bar)
		}
	}
	if pin, ok := m.repoBarAt(80, strings.Index(bar, "back")); !ok || pin != 1 {
		t.Errorf("a click on back is on %d, %v", pin, ok)
	}
	m.repoIdx = 2
	if narrow := stripANSI(m.repoBarView(12)); !strings.Contains(narrow, "lib") {
		t.Errorf("narrow, the one shown should stay in view: %q", narrow)
	}
	m.pins, m.repos = m.pins[:1], m.repos[:1]
	if m.hasRepoBar() || m.filesHeader() != titledHeader {
		t.Error("one repository needs no bar")
	}
}

// ↑ from the first file goes on to the bar, where ← and → switch the
// repository and ↓ goes back to the files.
func TestRepoBarArrows(t *testing.T) {
	m := NewEmbedded()
	m.pins = []Pin{{Repo: "/r/front", Path: "/w/front"}, {Repo: "/r/back", Path: "/w/back"}}
	m.repos = []git.Repo{{Name: "front"}, {Name: "back"}}
	key := func(k string) {
		var msg tea.KeyMsg
		switch k {
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "right":
			msg = tea.KeyMsg{Type: tea.KeyRight}
		case "left":
			msg = tea.KeyMsg{Type: tea.KeyLeft}
		}
		rm, _ := m.Update(msg)
		m = rm.(Model)
	}
	key("up")
	if !m.onRepoBar {
		t.Fatal("↑ on the first file should go on to the bar")
	}
	key("right")
	if m.repoIdx != 1 || !m.onRepoBar {
		t.Fatalf("→ should show the next repository, on the bar: %d %v", m.repoIdx, m.onRepoBar)
	}
	key("left")
	if m.repoIdx != 0 {
		t.Fatal("← should show the one before")
	}
	key("down")
	if m.onRepoBar {
		t.Fatal("↓ should go back to the files")
	}
}

// F1's files are above the diff, however wide the panel, while an agent
// is beside it; beside the diff in a wide panel alone on the screen. L
// puts them the other way.
func TestFilesBesideTheDiff(t *testing.T) {
	m := NewEmbedded()
	if l := m.embeddedLayout(160, 40); l.files.y == l.diff.y {
		t.Error("beside an agent, the files should be above the diff")
	}
	m = m.SetAlone(true)
	if l := m.embeddedLayout(80, 40); l.files.y == l.diff.y {
		t.Error("narrow, the files should be above the diff")
	}
	l := m.embeddedLayout(160, 40)
	if l.files.y != l.diff.y || l.files.x+l.files.w != l.diff.x || l.diff.x+l.diff.w != 160 {
		t.Errorf("wide, the files should be beside the diff: %+v %+v", l.files, l.diff)
	}
	m.width = 80
	cmd := m.toggleSide()
	if msg, ok := cmd().(LayoutMsg); !ok || !msg.Beside {
		t.Fatalf("L should tell nerdr the files went beside: %v", msg)
	}
	if l := m.embeddedLayout(80, 40); l.files.y != l.diff.y {
		t.Error("after L, the files should be beside the diff")
	}
	if msg := m.toggleSide()().(LayoutMsg); msg.Beside || m.embeddedLayout(160, 40).files.y == m.embeddedLayout(160, 40).diff.y {
		t.Error("L again should put them above, even in a wide panel")
	}
}

// c in F1 steps through the diff's colour schemes, and back to nerdr's.
func TestDiffColors(t *testing.T) {
	m := NewEmbedded()
	m, _ = m.SetDiffColors("unknown")
	if m.diffColors != "nerdr" {
		t.Fatalf("an unknown scheme is nerdr's, got %q", m.diffColors)
	}
	nerdrAdd := addStyle.Render("x")
	m.cycleDiffColors()
	if m.diffColors != "github" || addStyle.Render("x") == nerdrAdd || syntaxStyle.Name != "github-dark" {
		t.Fatalf("c should show GitHub's colours: %q, syntax %q", m.diffColors, syntaxStyle.Name)
	}
	m.cycleDiffColors()
	if m.diffColors != "nerdr" || addStyle.Render("x") != nerdrAdd || syntaxStyle.Name != "monokai" {
		t.Fatalf("c again should go back to nerdr's: %q", m.diffColors)
	}
}
