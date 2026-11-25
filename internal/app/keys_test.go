package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/input"
	"github.com/nerdr-dev/nerdr/internal/state"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// runIDs are the commands run's switch has a case for.
func runIDs(t *testing.T) map[string]bool {
	f, err := parser.ParseFile(token.NewFileSet(), "keys.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	ast.Inspect(f, func(node ast.Node) bool {
		fn, ok := node.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "run" {
			return true
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			sw, ok := node.(*ast.SwitchStmt)
			if !ok || sw.Tag == nil {
				return true
			}
			for _, c := range sw.Body.List {
				for _, e := range c.(*ast.CaseClause).List {
					lit, ok := e.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					k, _ := strconv.Unquote(lit.Value)
					ids[k] = true
				}
			}
			return false
		})
		return false
	})
	if len(ids) == 0 {
		t.Fatal("found no keys in run's switch")
	}
	return ids
}

// prefixCommand must know every key run's switch runs something for, or
// that key, pressed after walking the tabs, goes to the agent instead; a
// command two keys long must be in its group, or no key reaches it.
func TestPrefixCommandKnowsEveryCommand(t *testing.T) {
	for k := range runIDs(t) {
		if g, k2, ok := strings.Cut(k, " "); ok && g != "" {
			if run, found := core.GroupCommand(g, k2); !found || run != k {
				t.Errorf("%q is not in group %q", k, g)
			}
			if !prefixCommand(g) {
				t.Errorf("group %q is a command prefixCommand does not know", g)
			}
			continue
		}
		if !navKey(k) && !prefixCommand(k) && k != "esc" {
			t.Errorf("%q is a command prefixCommand does not know", k)
		}
	}
}

// Every command the hint, the menu and the palette offer runs something.
func TestEveryListedCommandRuns(t *testing.T) {
	ids := runIDs(t)
	for _, g := range core.AllCommands() {
		for _, e := range g.Entries {
			if !ids[e.Run] && !panelKey(e.Run) {
				t.Errorf("%s: %q (%s) runs nothing", g.Title, e.Key, e.Run)
			}
		}
	}
	for _, g := range core.CommandGroups() {
		for _, e := range g.Entries {
			if !ids[e.Run] && !core.IsGroup(e.Run) && !panelKey(e.Run) {
				t.Errorf("%s: %q runs nothing", g.Title, e.Key)
			}
		}
	}
}

// No key after the prefix is a group's and a command's both, and no key
// means one thing in small letters and another in capitals unless the
// two belong together.
func TestKeysDoNotCollide(t *testing.T) {
	ids := runIDs(t)
	for g := range core.KeyGroups() {
		if ids[g] || navKey(g) {
			t.Errorf("group key %q is also a command", g)
		}
	}
	paired := map[string]bool{"m": true, "h": true, "c": true}
	for k := range ids {
		if len(k) != 1 || strings.ToLower(k) == k {
			continue
		}
		low := strings.ToLower(k)
		if (ids[low] || core.IsGroup(low)) && !paired[low] {
			t.Errorf("%q and %q are different commands", low, k)
		}
	}
}

// TestRestartAsksWhileWorking: a r on an agent mid-turn asks first, and no
// leaves it running.
func TestRestartAsksWhileWorking(t *testing.T) {
	m := testModel(t)
	tb := m.Cur()
	tb.Light = core.StWorking
	m.runCommand("a r")
	if _, ok := m.dialog.(*confirm); !ok {
		t.Fatalf("restarting a working agent should ask first, got %T", m.dialog)
	}
	m.handleKey(keyMsg("n"))
	if m.dialog != nil || tb.Light != core.StWorking {
		t.Fatalf("no should leave the agent alone: dialog %T, status %v", m.dialog, tb.Light)
	}
}

// TestCommandReplacesDialog: a command after the prefix over an open
// dialog closes it and runs, so the last dialog asked for is the one open;
// esc only closes.
func TestCommandReplacesDialog(t *testing.T) {
	m := testModel(t)
	prefix := tea.KeyMsg{Type: tea.KeyCtrlQ}
	m.handleKey(prefix)
	m.handleKey(keyMsg("w"))
	if _, ok := m.dialog.(*inbox); !ok {
		t.Fatalf("w opens the inbox, got %T", m.dialog)
	}
	m.handleKey(prefix)
	m.handleKey(keyMsg("f"))
	if _, ok := m.dialog.(*switcher); !ok || m.prefix {
		t.Fatalf("f over the inbox opens the switcher, got %T, prefix %v", m.dialog, m.prefix)
	}
	// A group's command too, once its second key is pressed.
	m.handleKey(prefix)
	m.handleKey(keyMsg("g"))
	if _, ok := m.dialog.(*switcher); !ok {
		t.Fatalf("the group's first key leaves the dialog open, got %T", m.dialog)
	}
	m.handleKey(keyMsg("g"))
	if m.dialog != nil || !m.panelBranchPR() {
		t.Fatalf("g g over the switcher goes to F2's branch: dialog %T, F2 %v", m.dialog, m.panelBranchPR())
	}
	m.handleKey(prefix)
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog != nil || m.prefix {
		t.Fatalf("esc closes the dialog: %T, prefix %v", m.dialog, m.prefix)
	}
}

// The palette is the only help, so every command in it says what it does.
func TestEveryCommandHasADescription(t *testing.T) {
	for _, g := range core.AllCommands() {
		for _, e := range g.Entries {
			if strings.TrimSpace(e.Desc) == "" {
				t.Errorf("%s: %q (%s) has no description", g.Title, e.Label, e.Key)
			}
		}
	}
}

// A prefix whose letter is a command's key swaps that command with quit:
// with ctrl+r, r twice quits and q renames, and every list of keys says so.
func TestPrefixLetterSwapsWithQ(t *testing.T) {
	m := testModel(t)
	m.home().Core.Cfg.Prefix = "ctrl+r"
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlR})
	m.handleKey(keyMsg("q"))
	if _, ok := m.dialog.(*renamer); !ok {
		t.Fatalf("ctrl+r q opened %T, want the rename dialog", m.dialog)
	}
	m.dialog = nil
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlR})
	m.handleKey(keyMsg("r"))
	if c, ok := m.dialog.(*confirm); !ok || !strings.Contains(c.title, "Quit") {
		t.Fatalf("ctrl+r r opened %T, want quit", m.dialog)
	}
	m.dialog = nil
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlR})
	m.handleKey(keyMsg("?"))
	if view := ansi.Strip(m.View()); !strings.Contains(view, "ctrl+r q") {
		t.Errorf("the palette does not show rename on q:\n%s", view)
	}
	for _, r := range "switch agent" {
		m.Update(keyMsg(string(r)))
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "ctrl+r a s") {
		t.Errorf("the palette does not show a s as it is:\n%s", view)
	}
	for range "switch agent" {
		m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	for _, r := range "quit" {
		m.Update(keyMsg(string(r)))
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "ctrl+r r") {
		t.Errorf("the palette does not show quit on r:\n%s", view)
	}
	if m.pk("r") != "ctrl+r q" || m.pk("q") != "ctrl+r r" || m.pk("a s") != "ctrl+r a s" {
		t.Errorf("keys shown: %q %q %q", m.pk("r"), m.pk("q"), m.pk("a s"))
	}

	// A letter no command has changes nothing.
	m.home().Core.Cfg.Prefix = "ctrl+y"
	if m.pk("q") != "ctrl+y q" || m.pk("r") != "ctrl+y r" {
		t.Errorf("ctrl+y: %q %q", m.pk("q"), m.pk("r"))
	}
}

// TestScreensBehindThePrefix: g c, g g, e, k and h go to the panel's
// screens, as F1 to F5 do, and t c puts the F1 dialog in a pane.
func TestScreensBehindThePrefix(t *testing.T) {
	m := testModel(t)
	prefix := tea.KeyMsg{Type: tea.KeyCtrlQ}
	m.handleKey(prefix)
	m.handleKey(keyMsg("e"))
	if !m.panelMessages() || m.focus != core.FocusReview {
		t.Fatalf("e should show F3 with the keyboard: tasks %v, focus %v", m.panelMessages(), m.focus)
	}
	m.handleKey(prefix)
	m.handleKey(keyMsg("g"))
	m.handleKey(keyMsg("c"))
	if m.panelMessages() || !m.panelDlg || m.focus != core.FocusReview {
		t.Fatalf("g c should show F1 with the keyboard: tasks %v, dialog %v, focus %v", m.panelMessages(), m.panelDlg, m.focus)
	}
	m.handleKey(prefix)
	m.handleKey(keyMsg("t"))
	m.handleKey(keyMsg("c"))
	if f := m.focused(m.Cur()); f.Kind != core.PaneView || f.Screen != core.ViewChanges || m.panelDlg {
		t.Fatal("t c should put the F1 dialog in a pane of the tab's")
	}
}

// With function_keys off, the menu and the hints name the prefix keys.
func TestFunctionKeysOff(t *testing.T) {
	m := testModel(t)
	if m.screenKey(3) != "F3" {
		t.Fatalf("F-keys by default, got %q", m.screenKey(3))
	}
	off := false
	m.home().Core.Cfg.FunctionKeys = &off
	m.review = m.review.SetScreenKeys(m.screenMenuKeys())
	if got := m.screenKey(3); got != "ctrl+q e" {
		t.Fatalf("screenKey(3) = %q", got)
	}
}

// ] and [ after the prefix go to the next and previous repository, as in
// the review panel, and the prefix stays on for more.
func TestBracketsStepRepositories(t *testing.T) {
	m := testModel(t)
	if len(m.home().Core.Groups()) < 2 {
		t.Skip("needs two repositories")
	}
	g := m.curGroup()
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.handleKey(keyMsg("]"))
	if m.curGroup() == g || !m.prefix {
		t.Fatalf("] should go to the next repository with the prefix kept: %q, prefix %v", m.curGroup(), m.prefix)
	}
	m.handleKey(keyMsg("["))
	if m.curGroup() != g {
		t.Fatalf("[ should come back to %q, got %q", g, m.curGroup())
	}
}

// n in the close dialog is no, as in every other question; k closes the
// tab and keeps its worktree.
func TestCloseDialogNoIsNo(t *testing.T) {
	m := testModel(t)
	tb := m.Cur()
	m.openCloseFor(tb)
	m.Update(core.Reply{Ev: core.CloseInfoMsg{T: m.home().Core.Ref(tb)}})
	m.Update(keyMsg("n"))
	if m.dialog != nil || m.home().Core.TabIndex(tb) < 0 {
		t.Fatalf("n should cancel: dialog %T, tab at %d", m.dialog, m.home().Core.TabIndex(tb))
	}
	m.openCloseFor(tb)
	m.Update(core.Reply{Ev: core.CloseInfoMsg{T: m.home().Core.Ref(tb)}})
	m.Update(keyMsg("k"))
	if m.home().Core.TabIndex(tb) >= 0 {
		t.Fatal("k should close the tab, keeping the worktree")
	}
}

// p and P in F2 ask first, and a key that is not yes — the next letter of
// a word meant for the agent — answers no.
func TestPushAsksFirst(t *testing.T) {
	m := testModel(t)
	m.runCommand("g g")
	if m.curBranchPanel() == nil {
		t.Skip("no branch panel here")
	}
	for _, k := range []string{"p", "P"} {
		m.handleKey(keyMsg(k))
		if c, ok := m.dialog.(*confirm); !ok || !c.strict {
			t.Fatalf("%s should ask first, got %T", k, m.dialog)
		}
		m.handleKey(keyMsg("l"))
		if m.dialog != nil || m.curBranchPanel().busy != "" {
			t.Fatalf("%s then l should push nothing: dialog %T, busy %q", k, m.dialog, m.curBranchPanel().busy)
		}
	}
}

// TestTypingAfterSteppingPanes: after t ← steps to a pane, keeping the
// group on for more steps, a letter that is none of t's commands is typed
// to the pane, not taken for a command that is not there.
func TestTypingAfterSteppingPanes(t *testing.T) {
	m := paneModel(t)
	m.newShell()
	m.Update(input.PrefixMsg{})
	m.Update(keyMsg("t"))
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if !m.prefix || m.keyGroup != "t" {
		t.Fatal("the group should stay on after an arrow")
	}
	m.Update(keyMsg("b"))
	if m.prefix || m.keyGroup != "" || strings.Contains(m.toastText, "no such command") {
		t.Fatalf("b after t ← should go to the pane: prefix %v group %q toast %q", m.prefix, m.keyGroup, m.toastText)
	}

	// The prefix after a step starts a new command.
	m.Update(input.PrefixMsg{})
	m.Update(keyMsg("t"))
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlQ})
	if !m.prefix || m.keyGroup != "" {
		t.Fatalf("the prefix after a step should start over: prefix %v group %q", m.prefix, m.keyGroup)
	}
	m.prefix = false

	// Without an arrow first, a letter t has not is a mistyped command.
	m.Update(input.PrefixMsg{})
	m.Update(keyMsg("t"))
	m.Update(keyMsg("b"))
	if !strings.Contains(m.toastText, "no such command") {
		t.Fatalf("toast %q", m.toastText)
	}
}

// TestKeyPanel: with key_panel on, a column at the right shows the keys
// all the time; the prefix and a group's key open the group's keys below
// it, in place of the hint line at the bottom. Off, there is no column
// and the hint line shows after the prefix.
func TestKeyPanel(t *testing.T) {
	m := testModel(t)
	if m.railShown() || m.railCols() != 0 {
		t.Fatal("no column on the right without key_panel")
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.prefixAt = m.prefixAt.Add(-hintDelay)
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Agent ›") {
		t.Fatalf("the hint line should show after the prefix:\n%s", v)
	}
	m.handleKey(keyMsg("esc"))

	on := true
	m.home().Core.Cfg.KeyPanel = &on
	m.resize()
	if !m.railShown() {
		t.Fatal("key_panel should show the column")
	}
	rail := func() string { return ansi.Strip(strings.Join(m.railLines(60), "\n")) }
	if v := rail(); !strings.Contains(v, "a   Agent") || strings.Contains(v, "Note for yourself") {
		t.Fatalf("the panel should list the keys, its groups closed:\n%s", v)
	}
	// The review panel's screens are in the groups, with no list of their
	// own: the groups start at the top, navigation first.
	if v := rail(); strings.Contains(v, "F1  code changes") || !strings.HasPrefix(strings.TrimSpace(strings.Split(v, "\n")[1]), "│ Overview and navigation") {
		t.Fatalf("the panel should start with Overview and navigation, with no Panel:\n%s", v)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.handleKey(keyMsg("a"))
	if m.keyGroup != "a" {
		t.Fatal("a should open the agent's group")
	}
	v := rail()
	at, note := strings.Index(v, "a   Agent"), strings.Index(v, "n   Note for yourself")
	if note < 0 || note < at || !strings.Contains(v, "▾") {
		t.Fatalf("the group's keys should open below it:\n%s", v)
	}
	if view := ansi.Strip(m.View()); strings.Count(view, "Note for yourself") != 1 {
		t.Fatalf("the keys should be in the panel alone, not the hint line too:\n%s", view)
	}
}

// TestPrefixHint: the hint shows in the middle once the prefix has waited,
// every key in one level with the most used first and the groups marked;
// a group's key shows its keys, backspace goes back, and prefix_hint off
// keeps it away.
func TestPrefixHint(t *testing.T) {
	m := testModel(t)
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	if v := ansi.Strip(m.View()); strings.Contains(v, "Keys after") {
		t.Fatalf("the hint should wait a moment before it shows:\n%s", v)
	}
	m.prefixAt = m.prefixAt.Add(-hintDelay)
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "Keys after") || !strings.Contains(v, "Git/GitHub ›") {
		t.Fatalf("the hint should show every key after a moment:\n%s", v)
	}
	lines := strings.Split(v, "\n")
	at := -1
	for i, l := range lines {
		if strings.Contains(l, "Keys after") {
			at = i
		}
	}
	if at < 3 || strings.Index(lines[at], "Keys after") < 5 {
		t.Fatalf("the hint should be in the middle, not at the edge:\n%s", v)
	}
	if n, x := strings.Index(v, "New tab"), strings.Index(v, "Quit"); n < 0 || x < 0 || n > x {
		t.Fatalf("the most used keys should come first, the rare last:\n%s", v)
	}
	if !strings.Contains(v, "Agent ›") {
		t.Fatalf("a group should be marked:\n%s", v)
	}
	m.handleKey(keyMsg("a"))
	v = ansi.Strip(m.View())
	if !strings.Contains(v, "Note for yourself") || !strings.Contains(v, "esc back") || strings.Contains(v, "New tab") {
		t.Fatalf("the group's keys should show alone, with the way back:\n%s", v)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyBackspace})
	if !m.prefix || m.keyGroup != "" {
		t.Fatal("backspace should go back to the prefix's keys")
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Keys after") {
		t.Fatalf("back, the prefix's keys should show again:\n%s", v)
	}
	m.handleKey(keyMsg("esc"))

	off := false
	m.home().Core.Cfg.PrefixHint = &off
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m.prefixAt = m.prefixAt.Add(-hintDelay)
	if v := ansi.Strip(m.View()); strings.Contains(v, "Keys after") {
		t.Fatalf("prefix_hint off should keep the hint away:\n%s", v)
	}
}

// TestPrefixHintNarrow: on a narrow screen, a group of more pages than
// fit keeps the hint's box inside the screen, its keys below it wrapped.
func TestPrefixHintNarrow(t *testing.T) {
	m := testModel(t)
	m.startPrefix()
	m.keyGroup = "a"
	for _, size := range [][2]int{{40, 12}, {30, 10}, {24, 8}} {
		box := m.prefixHint(size[0], size[1])
		if w, h := lipgloss.Width(box), lipgloss.Height(box); w > size[0] || h > size[1] {
			t.Fatalf("%dx%d: the box is %dx%d:\n%s", size[0], size[1], w, h, ansi.Strip(box))
		}
		if v := ansi.Strip(box); !strings.Contains(v, "esc back") || !strings.Contains(v, "more (") {
			t.Fatalf("%dx%d: the way back and the pages should show:\n%s", size[0], size[1], v)
		}
	}
}

// TestMovedKeysInMenusAndNews: the tab's menu and the news of an issue
// name the keys as they are now.
func TestMovedKeysInMenusAndNews(t *testing.T) {
	m := testModel(t)
	found := false
	for _, it := range m.tabItems() {
		if it.label == "Since you were here" {
			found = true
			if it.key != "a w" {
				t.Fatalf("Since you were here is a w, got %q", it.key)
			}
		}
	}
	if !found {
		t.Fatal("the tab's menu has Since you were here")
	}
	m.tellIssueNews([]state.IssueNews{{Repo: "o/r", Number: 7, Title: "a bug"}})
	if !strings.Contains(m.toastText, m.pk("g o")) {
		t.Fatalf("the news should name g o: %q", m.toastText)
	}
}
