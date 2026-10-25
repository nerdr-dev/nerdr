package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/term"
	"github.com/nerdr-dev/nerdr/skills"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestMessagesWaitUnlessTicked(t *testing.T) {
	m := testModel(t)
	login := m.cur()

	m.openMessage(false)
	d := m.dialog.(*messageDialog)
	d.input.SetValue("also rename the flag")
	d.key(m, keyMsg("enter"))
	if len(login.Messages) != 1 || login.Messages[0].Auto {
		t.Fatalf("a message waits in F3 unless ticked: %+v", login.Messages)
	}
	if !m.panelMessages() || m.focus != focusReview {
		t.Fatal("F3 is shown with the keyboard once a message is added, to see what became of it")
	}
	if login.nextAuto() >= 0 {
		t.Fatal("nothing to send by itself")
	}

	m.openMessage(false)
	d = m.dialog.(*messageDialog)
	d.input.SetValue("then run the tests")
	d.key(m, tea.KeyMsg{Type: tea.KeyTab})
	d.key(m, keyMsg("enter"))
	if len(login.Messages) != 2 || !login.Messages[1].Auto || login.nextAuto() != 1 {
		t.Fatalf("tab ticks it: %+v", login.Messages)
	}
	m.openMessage(false)
	if !m.dialog.(*messageDialog).auto {
		t.Fatal("the next one starts as the last was")
	}
	m.dialog = nil

	// The panel says there are some.
	var msgs string
	for _, r := range m.taskRows(login, 120, 30) {
		if r.label == "tasks:" {
			msgs = r.text
		}
	}
	if !strings.Contains(msgs, "1 task waiting") || !strings.Contains(msgs, "F3") {
		t.Fatalf("the panel's line: %q", msgs)
	}
}

func TestMessagesScreen(t *testing.T) {
	m := testModel(t)
	login := m.cur()
	login.Messages = []state.Message{{Text: "first"}, {Text: "second"}, {Text: "third", Auto: true}}

	m.Update(tea.KeyMsg{Type: tea.KeyF3})
	if !m.panelMessages() || m.focus != focusReview {
		t.Fatalf("F3 opens F3 with the keyboard: shown %v, focus %v", m.panelMessages(), m.focus)
	}
	if !strings.Contains(ansi.Strip(m.View()), "second") {
		t.Fatal("the messages are drawn")
	}
	if m.msgCursor != noMessage {
		t.Fatalf("F3 starts with no message picked: cursor %d", m.msgCursor)
	}
	m.Update(keyMsg("enter"))
	if len(login.Messages) != 3 || m.focus != focusReview {
		t.Fatalf("enter with none picked puts nothing in: %+v, focus %v", login.Messages, m.focus)
	}
	m.Update(keyMsg("j"))
	m.Update(keyMsg("j"))
	m.Update(keyMsg(" "))
	if !login.Messages[1].Auto {
		t.Fatal("space ticks it")
	}
	m.Update(keyMsg("J"))
	if login.Messages[2].Text != "second" || m.msgCursor != 2 {
		t.Fatalf("J moves it down: %+v", login.Messages)
	}
	m.Update(keyMsg("d"))
	if len(login.Messages) != 2 || login.Messages[1].Text != "third" {
		t.Fatalf("d deletes it: %+v", login.Messages)
	}

	// Put in: typed into the agent without enter, and the agent gets the
	// keyboard.
	pane, err := term.Start([]string{"cat"}, t.TempDir(), nil, 60, 10, func() {})
	if err != nil {
		t.Skip("no cat:", err)
	}
	defer pane.Close()
	login.pane, login.status = pane, stIdle
	m.msgCursor = 0
	m.Update(keyMsg("enter"))
	if len(login.Messages) != 1 || m.focus != focusAgent {
		t.Fatalf("put in: %+v, focus %v", login.Messages, m.focus)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(pane.Snapshot().Screen, "first") {
		if time.Now().After(deadline) {
			t.Fatalf("not typed in: %q", pane.Snapshot().Screen)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Not while the agent asks something: it would be the answer.
	login.status = stBlocked
	m.setFocus(focusReview)
	m.Update(keyMsg("enter"))
	if len(login.Messages) != 1 {
		t.Fatal("kept while the agent asks something")
	}
}

func TestOldQueueIsTakenIn(t *testing.T) {
	st := state.Tab{Queue: []string{"a", "b"}, Messages: []state.Message{{Text: "kept"}}}
	st.TakeQueue()
	if len(st.Queue) != 0 || len(st.Messages) != 3 || !st.Messages[1].Auto || st.Messages[2].Text != "b" {
		t.Fatalf("%+v", st)
	}
}

// F3 from the agent, with the messages already on screen, gives them the
// keyboard: the arrows and space are for the list, and ↑ does not reach
// the agent to bring back what it was last sent.
func TestF4AgainTakesTheKeyboard(t *testing.T) {
	m := testModel(t)
	login := m.cur()
	login.Messages = []state.Message{{Text: "first"}, {Text: "second"}}
	f3 := tea.KeyMsg{Type: tea.KeyF3}

	m.Update(f3)
	if m.focus != focusReview || !m.panelMessages() {
		t.Fatalf("F3 opens the messages with the keyboard: focus %v", m.focus)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.focus != focusAgent || !m.panelMessages() {
		t.Fatalf("esc gives the agent the keyboard, the messages stay: focus %v", m.focus)
	}
	m.Update(f3)
	if m.focus != focusReview || m.msgCursor != noMessage {
		t.Fatalf("F3 again: focus %v, cursor %d", m.focus, m.msgCursor)
	}
	m.Update(keyMsg("j"))
	m.Update(keyMsg(" "))
	if !login.Messages[0].Auto || login.Messages[1].Auto {
		t.Fatalf("space ticks the first message: %+v", login.Messages)
	}
}

// The F-keys get out of the scrollback, which would otherwise take them.
func TestF4LeavesTheScrollback(t *testing.T) {
	m := testModel(t)
	m.scrolling = true
	m.Update(tea.KeyMsg{Type: tea.KeyF3})
	if m.scrolling || !m.panelMessages() || m.focus != focusReview {
		t.Fatalf("scrolling %v, messages %v, focus %v", m.scrolling, m.panelMessages(), m.focus)
	}
}

// A long message is shown whole under the cursor, copied whole with y, and
// kept whole when it is changed with e — past the text field's 99 lines.
func TestLongMessage(t *testing.T) {
	m := testModel(t)
	login := m.cur()
	var long []string
	for i := range 150 {
		long = append(long, fmt.Sprintf("line %d of a long message", i))
	}
	text := strings.Join(long, "\n")
	login.Messages = []state.Message{{Text: "short"}, {Text: text}}
	var copied string
	was := toClipboard
	toClipboard = func(s string) bool { copied = s; return true }
	defer func() { toClipboard = was }()

	m.Update(tea.KeyMsg{Type: tea.KeyF3})
	m.Update(keyMsg("j"))
	m.Update(keyMsg("j"))
	body := ansi.Strip(strings.Join(m.messagesBody(login, 60, 20), "\n"))
	if !strings.Contains(body, "line 0 of") || !strings.Contains(body, "line 5 of") {
		t.Fatalf("the message under the cursor is shown on its own lines, as far as there is room:\n%s", body)
	}

	m.Update(keyMsg("y"))
	if copied != text {
		t.Fatalf("y copies the whole message: %d of %d bytes", len(copied), len(text))
	}

	m.Update(keyMsg("e"))
	d, ok := m.dialog.(*messageDialog)
	if !ok {
		t.Fatal("e opens the message")
	}
	if d.input.Height() <= 5 {
		t.Fatalf("the text grows with a long message: %d rows", d.input.Height())
	}
	d.key(m, keyMsg("enter"))
	if login.Messages[1].Text != text {
		t.Fatalf("saved whole: %d lines", strings.Count(login.Messages[1].Text, "\n")+1)
	}
}

// F4 lists nerdr's skills with whether the tab's agent has them, and shows
// the one picked.
func TestSkillsScreen(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := testModel(t)
	m.Update(tea.KeyMsg{Type: tea.KeyF4})
	if !m.panelSkills() || m.focus != focusReview {
		t.Fatalf("F4 opens the skills with the keyboard: shown %v, focus %v", m.panelSkills(), m.focus)
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"F4 skills", "nerdr-explain", "nerdr-handoff", "nerdr-spinoff", "nerdr-tools", "✗"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the screen lacks %q:\n%s", want, view)
		}
	}
	for range len(skills.All()) - 2 { // nerdr-spinoff, before nerdr-tools
		m.Update(keyMsg("j"))
	}
	body := ansi.Strip(strings.Join(m.skillsBody(m.cur(), 70, 40), "\n"))
	if !strings.Contains(body, "── nerdr-spinoff") || !strings.Contains(body, "not where claude looks") {
		t.Fatalf("j picks the next skill, shown with its state:\n%s", body)
	}

	// Installed where claude looks: available.
	home, _ := os.UserHomeDir()
	if _, err := skills.Install(filepath.Join(home, ".claude", "skills")); err != nil {
		t.Fatal(err)
	}
	m.skills.at = time.Time{}
	body = ansi.Strip(strings.Join(m.skillsBody(m.cur(), 70, 40), "\n"))
	if !strings.Contains(body, "available to claude") || !strings.Contains(body, "~/.claude/skills/nerdr-spinoff") {
		t.Fatalf("once installed:\n%s", body)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.focus != focusAgent || !m.panelSkills() {
		t.Fatalf("esc gives the agent the keyboard: focus %v", m.focus)
	}
}

// What is typed into an agent stays one paste: an end-of-paste sequence or
// a control character in the text does not reach the agent as keys.
func TestPasteKeepsTheTextInThePaste(t *testing.T) {
	got := string(paste("fix this\r\nplease\x1b[201~\r/clear\x03\tnow\u009b"))
	want := "\x1b[200~fix this\nplease[201~\n/clear\tnow\x1b[201~"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if strings.Count(got, "\x1b") != 2 {
		t.Fatalf("only the paste's own brackets may carry escape: %q", got)
	}
}

// TestMInF4StepsScreens covers m in F3: the next of the panel's screens,
// as m is in the others, not a new message; n is that.
func TestMInF4StepsScreens(t *testing.T) {
	m := testModel(t)
	m.openMessages()
	if !m.panelMessages() || m.focus != focusReview {
		t.Fatal("F3 is not open with the keyboard")
	}
	m.Update(keyMsg("m"))
	if m.dialog != nil {
		t.Fatalf("m in F3 opened %T", m.dialog)
	}
	if m.panelMessages() {
		t.Fatal("m in F3 stays on F3")
	}
	m.openMessages()
	m.Update(keyMsg("n"))
	if _, ok := m.dialog.(*messageDialog); !ok {
		t.Fatalf("n in F3 opens %T", m.dialog)
	}
}

// TestSkillsWheel: the wheel anywhere over F4 scrolls the skill's text,
// and not above its top.
func TestSkillsWheel(t *testing.T) {
	m := testModel(t)
	m.handleKey(keyMsg("f4"))
	m.View()
	x := m.sideW() + m.mainW() - 5
	m.Update(tea.MouseMsg{X: x, Y: m.barRows() + 3, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if m.skills.scroll != 3 {
		t.Fatalf("scroll %d after a notch down, want 3", m.skills.scroll)
	}
	for range 3 {
		m.Update(tea.MouseMsg{X: x, Y: m.barRows() + 3, Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	}
	if m.skills.scroll != 0 {
		t.Fatalf("scroll %d after notches up, want 0", m.skills.scroll)
	}
}
