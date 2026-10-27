package app

import (
	"os"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/prompts"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// TestSavedPromptPicked: the prefix + l lists the saved prompts; one is
// picked by its number or enter, typed into the agent, and enter pressed
// after it only when the prompt says so.
func TestSavedPromptPicked(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := testModel(t)
	tb := runningTab(t, m)
	if err := prompts.Save([]prompts.Prompt{
		{Text: "commit, push and open a pull request", Enter: true},
		{Text: "rebase on main"},
	}); err != nil {
		t.Fatal(err)
	}

	m.runCommand("l")
	d, ok := m.dialog.(*promptPicker)
	if !ok {
		t.Fatal("the prefix + l opens the saved prompts")
	}
	view := ansi.Strip(d.view(m, 100, 30))
	if !strings.Contains(view, "commit, push and open a pull request") || !strings.Contains(view, "rebase on main") {
		t.Fatalf("both are listed:\n%s", view)
	}
	before := tb.pane.Inputs()
	d.key(m, keyMsg("2"))
	if m.dialog != nil || len(tb.said) != 1 || tb.said[0] != "rebase on main" || tb.pane.Inputs() != before+1 {
		t.Fatalf("typed in as a paste, without enter: %q", tb.said)
	}

	m.runCommand("l")
	d = m.dialog.(*promptPicker)
	for _, r := range "push" {
		d.key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if hits := d.matches(m); len(hits) != 1 || hits[0] != 0 {
		t.Fatalf("typing finds it: %v", hits)
	}
	cmd := d.key(m, keyMsg("enter"))
	if len(tb.said) != 2 || tb.said[1] != "commit, push and open a pull request" {
		t.Fatalf("enter uses the one found: %q", tb.said)
	}
	before = tb.pane.Inputs()
	m.Update(promptEnterMsg{t: tb, pane: tb.pane})
	if tb.pane.Inputs() != before+1 || cmd == nil {
		t.Fatal("enter is pressed after a prompt that says so")
	}
}

// TestSavedPromptWritten: a prompt is saved from the list and kept in the
// file, its enter as ticked; F3's second view lists, changes and deletes
// them.
func TestSavedPromptWritten(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := testModel(t)

	m.runCommand("l")
	m.dialog.key(m, keyMsg("ctrl+n"))
	e, ok := m.dialog.(*promptDialog)
	if !ok || !e.enter {
		t.Fatal("ctrl+n writes a new one, sent with enter to start with")
	}
	e.text.input.SetValue("run the tests")
	e.key(m, tea.KeyMsg{Type: tea.KeyTab})
	e.key(m, keyMsg("enter"))
	if _, ok := m.dialog.(*promptPicker); !ok {
		t.Fatal("saved, the list is back")
	}
	ps, _ := prompts.Load()
	if len(ps) != 1 || ps[0].Text != "run the tests" || ps[0].Enter {
		t.Fatalf("kept in the file, tab having turned enter off: %+v", ps)
	}
	m.dialog = nil

	m.handleKey(keyMsg("f3"))
	m.handleKey(keyMsg("right"))
	if !m.saved.on {
		t.Fatal("→ in F3 shows the saved prompts")
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "saved prompts") || !strings.Contains(view, "run the tests") {
		t.Fatalf("F3 lists them:\n%s", view)
	}
	m.handleKey(keyMsg(" "))
	if ps, _ := prompts.Load(); !ps[0].Enter {
		t.Fatal("space turns enter on")
	}
	m.handleKey(keyMsg("e"))
	e = m.dialog.(*promptDialog)
	e.text.input.SetValue("run all the tests")
	e.key(m, keyMsg("enter"))
	if ps, _ := prompts.Load(); len(ps) != 1 || ps[0].Text != "run all the tests" || !ps[0].Enter {
		t.Fatalf("changed in its place: %+v", ps)
	}
	m.handleKey(keyMsg("d"))
	if ps, _ := prompts.Load(); len(ps) != 0 {
		t.Fatalf("d deletes it: %+v", ps)
	}
	m.handleKey(keyMsg("left"))
	if m.saved.on {
		t.Fatal("← goes back to the tasks")
	}
}

// TestSavedPromptFileKept: a file that does not read is not written over,
// and what was added to it by hand while the list was open stays; of two
// prompts with the same text, the one changed is the one picked.
func TestSavedPromptFileKept(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := testModel(t)
	if err := prompts.Save([]prompts.Prompt{{Text: "push", Enter: true}, {Text: "push"}}); err != nil {
		t.Fatal(err)
	}
	m.handleKey(keyMsg("f3"))
	m.handleKey(keyMsg("right"))

	// The second "push" is changed, not the first.
	m.handleKey(keyMsg("down"))
	m.handleKey(keyMsg("e"))
	e := m.dialog.(*promptDialog)
	e.text.input.SetValue("push the branch")
	e.key(m, keyMsg("enter"))
	ps, _ := prompts.Load()
	if len(ps) != 2 || ps[0] != (prompts.Prompt{Text: "push", Enter: true}) || ps[1] != (prompts.Prompt{Text: "push the branch"}) {
		t.Fatalf("the second should be changed: %+v", ps)
	}

	// Added by hand meanwhile: kept when one is changed in F3.
	if err := prompts.Save(append(ps, prompts.Prompt{Text: "rebase"})); err != nil {
		t.Fatal(err)
	}
	m.handleKey(keyMsg(" "))
	ps, _ = prompts.Load()
	if len(ps) != 3 || ps[2].Text != "rebase" || !ps[1].Enter {
		t.Fatalf("the one added by hand should stay: %+v", ps)
	}

	// A file that does not read is left as it is.
	bad := []byte("[[prompt]\ntext = \"half written\"\n")
	if err := os.WriteFile(prompts.Path(), bad, 0644); err != nil {
		t.Fatal(err)
	}
	m.handleKey(keyMsg("d"))
	m.handleKey(keyMsg("n"))
	e = m.dialog.(*promptDialog)
	e.text.input.SetValue("new one")
	e.key(m, keyMsg("enter"))
	if m.dialog != e || e.err == "" {
		t.Fatal("not saved, the dialog stays with what was written")
	}
	if got, _ := os.ReadFile(prompts.Path()); string(got) != string(bad) {
		t.Fatalf("the file was written over:\n%s", got)
	}
}

// TestSavedPromptEnterToItsAgent: the enter after a saved prompt goes to
// the program it was typed into, wherever the keyboard is, and to none
// started in its place since.
func TestSavedPromptEnterToItsAgent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := testModel(t)
	tb := runningTab(t, m)
	cmd := m.usePrompt(tb, prompts.Prompt{Text: "push", Enter: true})
	if cmd == nil {
		t.Fatal("enter is to follow")
	}
	msg := promptEnterMsg{t: tb, pane: tb.pane}
	m.handleKey(keyMsg("f3")) // the keyboard to the panel
	before := tb.pane.Inputs()
	m.Update(msg)
	if tb.pane.Inputs() != before+1 {
		t.Fatal("enter goes to the agent it was typed into")
	}

	old := tb.pane
	runningTab(t, m) // restarted: another program in the tab
	if tb.pane == old {
		t.Fatal("the pane should be a new one")
	}
	before = tb.pane.Inputs()
	m.Update(msg)
	if tb.pane.Inputs() != before {
		t.Fatal("no enter for a program that never had the prompt")
	}
}
