package review

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07]*(\x07|\x1b\\)`)

// TestLayoutDoesNotOverflow ensures that with long file paths and long diff
// lines the rendered view never exceeds the terminal height or width (no
// wrapping/vertical scroll-off).
func TestLayoutDoesNotOverflow(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	parent := t.TempDir()
	repo := filepath.Join(parent, "myrepo")
	runGit(t, "", "init", "-q", repo)
	// deeply nested path so the file path is very long
	longRel := filepath.Join("src", "app", "api", "a", "b", "c", "d", "e", "f", "g", "very-long-route-file-name.ts")
	abs := filepath.Join(repo, longRel)
	if err := writeFileDirs(abs, "export function handler() {\n  return \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"\n}\n"); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "init")
	// modify with a very long line
	if err := writeFileDirs(abs, "export function handler() {\n  return \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"\n}\n"); err != nil {
		t.Fatal(err)
	}

	m := New(parent)
	send := func(msg tea.Msg) {
		nm, _ := m.Update(msg)
		m = nm.(Model)
	}
	send(tea.KeyMsg{Type: tea.KeyRight}) // -> worktrees
	send(tea.KeyMsg{Type: tea.KeyRight}) // -> files

	// Expand the whole tree and select the file so the diff is rendered.
	for _, r := range m.flatRows() {
		if r.node.isDir {
			m.expanded[r.node.path] = true
		}
	}
	for i, r := range m.flatRows() {
		if !r.node.isDir {
			m.cursor = i
			break
		}
	}
	m.focus = focusDiff
	m.loadDiff()

	for _, dim := range [][2]int{{80, 24}, {120, 32}} {
		m.width, m.height = dim[0], dim[1]
		v := ansiRe.ReplaceAllString(m.View(), "")
		lines := strings.Split(v, "\n")
		if len(lines) > dim[1] {
			t.Errorf("term %dx%d: rendered %d lines (overflow by %d)", dim[0], dim[1], len(lines), len(lines)-dim[1])
		}
		for i, l := range lines {
			if w := lipgloss.Width(l); w > dim[0] {
				t.Errorf("term %dx%d: line %d width %d exceeds %d: %q", dim[0], dim[1], i, w, dim[0], l)
			}
		}
	}
}

func writeFileDirs(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// TestStatusBarKeepsToastVisible guards feedback the user just triggered
// from being squeezed out by the (long) hint line.
func TestStatusBarKeepsToastVisible(t *testing.T) {
	m := Model{cwd: "/home/user/develop", toast: "select a line of code to ask about"}
	got := m.statusBar(120)
	if !strings.Contains(got, "select a line of code") {
		t.Fatalf("toast was dropped from the status bar:\n%q", got)
	}
	if w := lipgloss.Width(got); w > 120 {
		t.Fatalf("status bar overflows the terminal: %d > 120", w)
	}

	// With no toast the path is unchanged: cwd on the left, hints right.
	m.toast = ""
	got = m.statusBar(120)
	if !strings.Contains(ansiRe.ReplaceAllString(got, ""), "q quit") {
		t.Fatalf("the hint line must fit a 120-column terminal without losing its tail:\n%q", got)
	}
	if w := lipgloss.Width(got); w > 120 {
		t.Fatalf("status bar overflows the terminal: %d > 120", w)
	}
}

// TestHeadingRowSpansThePaneAndMarksFocus guards the bar being a full-width
// heading rather than a first row of content, and the focused pane's bar
// differing so it doubles as a focus cue.
func TestHeadingRowSpansThePaneAndMarksFocus(t *testing.T) {
	inactive := headingRow("repo", 20, false)
	active := headingRow("repo", 20, true)

	if lipgloss.Width(inactive) != 20 || lipgloss.Width(active) != 20 {
		t.Fatalf("heading must span the pane: got %d and %d, want 20",
			lipgloss.Width(inactive), lipgloss.Width(active))
	}
	if inactive == active {
		t.Fatal("the focused pane's heading must differ, since it also signals focus")
	}
	for _, got := range []string{inactive, active} {
		if !strings.Contains(got, "repo") {
			t.Fatalf("heading lost its title: %q", got)
		}
		if !strings.Contains(got, "48;5;") {
			t.Fatalf("expected a background colour so it reads as a bar: %q", got)
		}
	}
}

// TestHeadingNeverWearsTheSelectionColour pins the split: light blue means
// "selected" (row cursor, active tab), so a heading in the same colour
// would be indistinguishable from a selected row.
func TestHeadingNeverWearsTheSelectionColour(t *testing.T) {
	selBg := selectedStyle.GetBackground()
	for _, active := range []bool{false, true} {
		st := headingStyle
		if active {
			st = headingActiveStyle
		}
		if st.GetBackground() == selBg {
			t.Fatalf("heading (active=%v) uses the selection background %v", active, selBg)
		}
	}
}

// TestHintRowIsPaneSizedAndFocusOnly covers hints belonging to the pane
// they act on: shown only where you are, and never wider than the pane.
func TestHintRowIsPaneSizedAndFocusOnly(t *testing.T) {
	if got := hintRow(hintFiles, 34, false); strings.TrimSpace(got) != "" {
		t.Fatalf("an unfocused pane must not advertise its keys, got %q", got)
	}
	active := hintRow(hintFiles, 34, true)
	if !strings.Contains(active, "filter") {
		t.Fatalf("expected the pane's own keys, got %q", active)
	}
	for _, c := range []struct {
		hint  string
		width int
	}{
		{hintRepos, 20}, {hintWorktrees, 20}, {hintFiles, 34}, {hintDiff, 58}, {hintDiffJumped, 58},
	} {
		if lipgloss.Width(c.hint) > c.width {
			t.Fatalf("hint %q is %d wide and will be cut in a %d-wide pane",
				c.hint, lipgloss.Width(c.hint), c.width)
		}
		if w := lipgloss.Width(hintRow(c.hint, c.width, true)); w != c.width {
			t.Fatalf("hint row is %d wide, want exactly %d", w, c.width)
		}
	}
}

// TestSelectionStaysVisibleInAnUnfocusedPane covers the file pane still
// saying which file the diff belongs to once focus has moved into the diff
// — and, since n/p move that selection from inside the diff pane, the only
// place the change is visible at all.
func TestSelectionStaysVisibleInAnUnfocusedPane(t *testing.T) {
	items := []string{"first", "second", "third"}

	unfocused := renderPane(items, 1, 30, 12, false, "files", hintFiles)
	focused := renderPane(items, 1, 30, 12, true, "files", hintFiles)

	markedLine := func(pane string) string {
		for _, line := range strings.Split(pane, "\n") {
			if strings.Contains(line, "second") {
				return line
			}
		}
		t.Fatalf("selected row missing from pane %q", pane)
		return ""
	}

	selBg := func(st lipgloss.Style, sample string) string {
		// The background escape the style actually emits, so the test
		// checks rendering rather than restating the colour numbers.
		full := st.Render(sample)
		return full[:strings.Index(full, sample)]
	}
	activeSeq := selBg(selectedStyle, "x")
	inactiveSeq := selBg(selectedInactiveStyle, "x")

	if row := markedLine(unfocused); !strings.Contains(row, inactiveSeq) {
		t.Fatalf("unfocused pane left its selected row unmarked: %q", row)
	}
	// Distinct from the focused selection, so the two panes don't both look
	// like they hold the cursor.
	if row := markedLine(unfocused); strings.Contains(row, activeSeq) {
		t.Fatalf("unfocused selection wears the focused pane's colour: %q", row)
	}
	if row := markedLine(focused); !strings.Contains(row, activeSeq) {
		t.Fatalf("focused pane lost its selection colour: %q", row)
	}
	// And only the selected row is marked.
	for _, line := range strings.Split(unfocused, "\n") {
		if strings.Contains(line, "first") || strings.Contains(line, "third") {
			if strings.Contains(line, inactiveSeq) || strings.Contains(line, activeSeq) {
				t.Fatalf("an unselected row is marked as selected: %q", line)
			}
		}
	}
}
