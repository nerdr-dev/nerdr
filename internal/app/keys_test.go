package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"nerdr/internal/input"

	tea "github.com/charmbracelet/bubbletea"
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
			if run, found := groupCommand(g, k2); !found || run != k {
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
	for _, g := range allCommands() {
		for _, e := range g.entries {
			if !ids[e.run] && !panelKey(e.run) {
				t.Errorf("%s: %q (%s) runs nothing", g.title, e.key, e.run)
			}
		}
	}
	for _, g := range commandGroups() {
		for _, e := range g.entries {
			if !ids[e.run] && !isGroup(e.run) && !panelKey(e.run) {
				t.Errorf("%s: %q runs nothing", g.title, e.key)
			}
		}
	}
}

// No key after the prefix is a group's and a command's both, and no key
// means one thing in small letters and another in capitals unless the
// two belong together.
func TestKeysDoNotCollide(t *testing.T) {
	ids := runIDs(t)
	for g := range keyGroups() {
		if ids[g] || navKey(g) {
			t.Errorf("group key %q is also a command", g)
		}
	}
	paired := map[string]bool{"m": true, "g": true, "h": true, "c": true}
	for k := range ids {
		if len(k) != 1 || strings.ToLower(k) == k {
			continue
		}
		low := strings.ToLower(k)
		if (ids[low] || isGroup(low)) && !paired[low] {
			t.Errorf("%q and %q are different commands", low, k)
		}
	}
}

// TestRestartAsksWhileWorking: a r on an agent mid-turn asks first, and no
// leaves it running.
func TestRestartAsksWhileWorking(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	tb.status = stWorking
	m.runCommand("a r")
	if _, ok := m.dialog.(*confirm); !ok {
		t.Fatalf("restarting a working agent should ask first, got %T", m.dialog)
	}
	m.handleKey(keyMsg("n"))
	if m.dialog != nil || tb.status != stWorking {
		t.Fatalf("no should leave the agent alone: dialog %T, status %v", m.dialog, tb.status)
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
	m.handleKey(keyMsg("i"))
	if _, ok := m.dialog.(*switcher); !ok {
		t.Fatalf("the group's first key leaves the dialog open, got %T", m.dialog)
	}
	m.handleKey(keyMsg("i"))
	if m.dialog != nil || !m.panelBranchPR() {
		t.Fatalf("i i over the switcher goes to F2's branch: dialog %T, F2 %v", m.dialog, m.panelBranchPR())
	}
	m.handleKey(prefix)
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.dialog != nil || m.prefix {
		t.Fatalf("esc closes the dialog: %T, prefix %v", m.dialog, m.prefix)
	}
}

// The palette is the only help, so every command in it says what it does.
func TestEveryCommandHasADescription(t *testing.T) {
	for _, g := range allCommands() {
		for _, e := range g.entries {
			if strings.TrimSpace(e.desc) == "" {
				t.Errorf("%s: %q (%s) has no description", g.title, e.label, e.key)
			}
		}
	}
}

// A prefix whose letter is a command's key swaps that command with quit:
// with ctrl+r, r twice quits and q renames, and every list of keys says so.
func TestPrefixLetterSwapsWithQ(t *testing.T) {
	m := testModel(t)
	m.cfg.Prefix = "ctrl+r"
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
	m.cfg.Prefix = "ctrl+y"
	if m.pk("q") != "ctrl+y q" || m.pk("r") != "ctrl+y r" {
		t.Errorf("ctrl+y: %q %q", m.pk("q"), m.pk("r"))
	}
}

// TestScreensBehindThePrefix: e 1 to e 4 go to the panel's screens, as
// F1 to F4 do, e twice to F1, and z e shows and hides the panel.
func TestScreensBehindThePrefix(t *testing.T) {
	m := testModel(t)
	prefix := tea.KeyMsg{Type: tea.KeyCtrlQ}
	m.handleKey(prefix)
	m.handleKey(keyMsg("e"))
	m.handleKey(keyMsg("3"))
	if !m.panelMessages() || m.focus != focusReview {
		t.Fatalf("e 3 should show F3 with the keyboard: tasks %v, focus %v", m.panelMessages(), m.focus)
	}
	m.handleKey(prefix)
	m.handleKey(keyMsg("e"))
	m.handleKey(keyMsg("e"))
	if m.panelMessages() || !m.reviewOpen || m.focus != focusReview {
		t.Fatalf("e e should show F1 with the keyboard: tasks %v, open %v, focus %v", m.panelMessages(), m.reviewOpen, m.focus)
	}
	m.handleKey(prefix)
	m.handleKey(keyMsg("z"))
	m.handleKey(keyMsg("e"))
	if m.reviewOpen {
		t.Fatal("z e should hide the panel")
	}
}

// With function_keys off, the menu and the hints name the prefix keys.
func TestFunctionKeysOff(t *testing.T) {
	m := testModel(t)
	if m.screenKey(3) != "F3" {
		t.Fatalf("F-keys by default, got %q", m.screenKey(3))
	}
	off := false
	m.cfg.FunctionKeys = &off
	m.review = m.review.SetScreenKeys(m.screenMenuKeys())
	m.reviewOpen = true
	m.resize()
	if got := m.screenKey(3); got != "ctrl+q e 3" {
		t.Fatalf("screenKey(3) = %q", got)
	}
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "e1 code changes") || strings.Contains(v, "F1 code changes") {
		t.Fatalf("the menu row should name e1, not F1:\n%s", v)
	}
	m.togglePanelSmall()
	v = ansi.Strip(m.View())
	if !strings.Contains(v, "e1 changes") || strings.Contains(v, "F1 changes") {
		t.Fatalf("the menu's column should name e1, not F1:\n%s", v)
	}
}

// ] and [ after the prefix go to the next and previous repository, as in
// the review panel, and the prefix stays on for more.
func TestBracketsStepRepositories(t *testing.T) {
	m := testModel(t)
	if len(m.groups()) < 2 {
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
	tb := m.cur()
	m.openCloseFor(tb)
	m.Update(closeInfoMsg{t: tb})
	m.Update(keyMsg("n"))
	if m.dialog != nil || m.tabIndex(tb) < 0 {
		t.Fatalf("n should cancel: dialog %T, tab at %d", m.dialog, m.tabIndex(tb))
	}
	m.openCloseFor(tb)
	m.Update(closeInfoMsg{t: tb})
	m.Update(keyMsg("k"))
	if m.tabIndex(tb) >= 0 {
		t.Fatal("k should close the tab, keeping the worktree")
	}
}

// p and P in F2 ask first, and a key that is not yes — the next letter of
// a word meant for the agent — answers no.
func TestPushAsksFirst(t *testing.T) {
	m := testModel(t)
	m.runCommand("i i")
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
