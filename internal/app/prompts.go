package app

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/prompts"
	"github.com/nerdr-dev/nerdr/internal/term"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Saved prompts are what the user types into agents again and again
// ("commit, push and open a pull request"), kept once and picked from a
// list: the prefix + l, or F3's second view, → from the tasks. Picked,
// one is typed into the agent with the keyboard as a paste, and enter
// pressed after it when the prompt says so; else it waits in the agent's
// prompt to be changed or sent. They are the same in every repository;
// see internal/prompts.

// savedView is the state of F3's view of the saved prompts.
type savedView struct {
	// on is set while F3 shows the saved prompts, not the tab's tasks.
	on     bool
	cursor int
	list   []prompts.Prompt
}

// promptEnterMsg presses enter after a saved prompt typed into t's pane;
// not into another program started in t since.
type promptEnterMsg struct {
	t    *tab
	pane *term.Pane
}

// loadPrompts reads the saved prompts anew, as they may have been changed
// by hand. When the file does not read it says so and keeps the list as
// it was, and false is returned: nothing may then be saved over it.
func (m *Model) loadPrompts() bool {
	ps, err := prompts.Load()
	if err != nil {
		m.toast(err.Error())
		return false
	}
	m.saved.list = ps
	return true
}

// findPrompt reads the file anew and finds in it prompt p, shown at i:
// at i still, or else the one equal to it when there is only one. -1 when
// the file does not read, or p is not in it or not told apart from
// another, which it says.
func (m *Model) findPrompt(i int, p prompts.Prompt) int {
	if !m.loadPrompts() {
		return -1
	}
	if i >= 0 && i < len(m.saved.list) && m.saved.list[i] == p {
		return i
	}
	at := -1
	for j, x := range m.saved.list {
		if x == p {
			if at >= 0 {
				at = -1
				break
			}
			at = j
		}
	}
	if at < 0 {
		m.toast(tildePath(prompts.Path(), homeDir()) + " has changed: see the list again, and try once more")
	}
	return at
}

// savePrompts writes the saved prompts, saying so when it cannot.
func (m *Model) savePrompts() {
	if err := prompts.Save(m.saved.list); err != nil {
		m.toast("the saved prompts were not saved: " + err.Error())
	}
}

// usePrompt types saved prompt p into agent a, and gives it the keyboard.
func (m *Model) usePrompt(a *tab, p prompts.Prompt) tea.Cmd {
	if a == nil || !m.typeIn(a, p.Text) {
		return nil
	}
	cmd := tea.Batch(m.goToAgent(a), m.resize())
	if !p.Enter {
		m.toast("put in: enter sends it")
		return cmd
	}
	// As in the queue, the agent takes the paste in as text before enter.
	pane := a.pane
	return tea.Batch(cmd, tea.Tick(enterAfter, func(time.Time) tea.Msg { return promptEnterMsg{t: a, pane: pane} }))
}

// promptEnter presses enter after a saved prompt, if the agent it was
// typed into still runs in t: restarted or switched, the program there
// now never had it.
func (m *Model) promptEnter(t *tab, pane *term.Pane) {
	if !m.live(t) || t.pane == nil || t.pane != pane {
		return
	}
	t.pane.Input([]byte("\r"))
	if t.typed != nil {
		t.typed.feed([]byte("\r"))
	}
}

// promptPicker is the list of saved prompts the prefix + l opens: type to
// find one, enter or its number to use it.
type promptPicker struct {
	input  textinput.Model
	cursor int
	// agent is who the prompt picked is typed into: the agent with the
	// keyboard when the list was opened.
	agent *tab
}

func (m *Model) openPromptPicker() tea.Cmd {
	m.loadPrompts()
	in := textinput.New()
	in.Prompt = "prompt: "
	in.Placeholder = "type to filter…"
	in.Focus()
	m.dialog = &promptPicker{input: in, agent: m.curAgent()}
	return nil
}

// matches are the indexes of the saved prompts matching what is typed,
// best first; with nothing typed, all of them in order.
func (d *promptPicker) matches(m *Model) []int {
	q := strings.ToLower(strings.TrimSpace(d.input.Value()))
	type hit struct{ i, score int }
	var hits []hit
	for i, p := range m.saved.list {
		if score, ok := fuzzy(q, strings.ToLower(p.Text)); ok {
			hits = append(hits, hit{i, score})
		}
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].score > hits[b].score })
	out := make([]int, len(hits))
	for n, h := range hits {
		out[n] = h.i
	}
	return out
}

func (d *promptPicker) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	hits := d.matches(m)
	k := msg.String()
	switch k {
	case "esc", "ctrl+c":
		m.dialog = nil
		return nil
	case "up", "ctrl+p", "ctrl+k":
		d.cursor = max(d.cursor-1, 0)
		return nil
	case "down", "ctrl+j", "tab":
		d.cursor = min(d.cursor+1, max(len(hits)-1, 0))
		return nil
	case "ctrl+n":
		return m.openPromptEdit(-1, d.reopen)
	case "ctrl+e":
		if d.cursor < len(hits) {
			return m.openPromptEdit(hits[d.cursor], d.reopen)
		}
		return nil
	case "enter":
		if d.cursor < len(hits) {
			return d.use(m, hits[d.cursor])
		}
		if len(m.saved.list) == 0 {
			return m.openPromptEdit(-1, d.reopen)
		}
		return nil
	}
	// With nothing typed, a number picks the prompt it is shown with.
	if n, err := strconv.Atoi(k); err == nil && n >= 1 && d.input.Value() == "" {
		if n <= len(hits) {
			return d.use(m, hits[n-1])
		}
		return nil
	}
	var cmd tea.Cmd
	d.input, cmd = d.input.Update(msg)
	d.cursor = 0
	return cmd
}

// use types saved prompt i into the picker's agent.
func (d *promptPicker) use(m *Model, i int) tea.Cmd {
	m.dialog = nil
	if d.agent == nil || !m.live(d.agent) {
		m.toast("no agent to type it into")
		return nil
	}
	return m.usePrompt(d.agent, m.saved.list[i])
}

// reopen is where the edit dialog goes back to: the list, as it was.
func (d *promptPicker) reopen(m *Model) tea.Cmd {
	m.loadPrompts()
	m.dialog = d
	return d.input.Focus()
}

func (d *promptPicker) view(m *Model, w, h int) string {
	sel := lipgloss.NewStyle().Foreground(lipgloss.Color("110")).Bold(true)
	lines := []string{d.input.View(), ""}
	if t := d.agent; t != nil {
		lines = []string{"For " + t.owner().Name + " (" + m.agentLabel(t.owner(), t) + ").", "", d.input.View(), ""}
	}
	hits := d.matches(m)
	switch {
	case len(m.saved.list) == 0:
		lines = append(lines, dim("  No saved prompts yet: ctrl+n saves one, such as"),
			dim("  \"commit, push and open a pull request\"."))
	case len(hits) == 0:
		lines = append(lines, dim("  no prompt matches"))
	}
	room := max(h-12, 3)
	top := max(0, d.cursor-room+1)
	lineW := max(w-12, 30)
	for n, i := range hits {
		if n < top || n >= top+room {
			continue
		}
		p := m.saved.list[i]
		num := "  "
		if n < 9 && d.input.Value() == "" {
			num = keyHint(strconv.Itoa(n+1)) + " "
		}
		line := num + promptMark(p) + " " + strings.Join(strings.Fields(p.Text), " ")
		line = ansi.Truncate(line, lineW, "…")
		lines = append(lines, m.onClick(cursorLine(n == d.cursor, line, sel), func(m *Model) tea.Cmd {
			d.cursor = n
			return d.key(m, keyMsg("enter"))
		}))
	}
	lines = append(lines, "", dim("  ⏎ sent with enter   ✎ put in for you to send"), "",
		m.keys("enter", "use", "", "1-9 pick", "ctrl+n", "new", "ctrl+e", "edit", "esc", "cancel"))
	return box("Saved prompts", lines, w)
}

// promptMark says whether a saved prompt is sent with enter, or put in.
func promptMark(p prompts.Prompt) string {
	if p.Enter {
		return "⏎"
	}
	return "✎"
}

// promptDialog writes a saved prompt, or changes one.
type promptDialog struct {
	// messageDialog's text box, so the two are written in alike.
	text messageDialog
	// editing is the index of the prompt changed, -1 for a new one, and
	// orig the prompt as it was, to find it again in the file.
	editing int
	orig    prompts.Prompt
	enter   bool
	err     string
	// back is where the dialog goes once done: the list it was opened
	// from.
	back func(*Model) tea.Cmd
}

// openPromptEdit opens saved prompt i to be changed, or with -1 a new
// one; back is called once it is saved or left.
func (m *Model) openPromptEdit(i int, back func(*Model) tea.Cmd) tea.Cmd {
	d := &promptDialog{editing: i, enter: true, back: back}
	d.text.input = newTextArea("what to type into the agent…", min(max(m.width-16, 30), 90))
	if i >= 0 && i < len(m.saved.list) {
		d.orig = m.saved.list[i]
		d.text.input.SetValue(d.orig.Text)
		d.enter = d.orig.Enter
	} else {
		d.editing = -1
	}
	d.text.fit(m)
	m.dialog = d
	return nil
}

func (d *promptDialog) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.dialog = nil
		return d.back(m)
	case "tab", "shift+tab":
		d.enter = !d.enter
		return nil
	case "enter":
		text := strings.TrimSpace(d.text.input.Value())
		if text == "" {
			d.err = "the prompt is empty"
			return nil
		}
		// The file may have been changed by hand since it was read: it is
		// read again, and the prompt changed found in it, before it is
		// written. What is written here stays in the dialog meanwhile.
		p := prompts.Prompt{Text: text, Enter: d.enter}
		var at int
		if d.editing >= 0 {
			at = m.findPrompt(d.editing, d.orig)
		} else if m.loadPrompts() {
			at = len(m.saved.list)
		} else {
			at = -1
		}
		if at < 0 {
			d.err = "not saved: " + tildePath(prompts.Path(), homeDir()) + " does not read, or has changed"
			return nil
		}
		if at < len(m.saved.list) {
			m.saved.list[at] = p
		} else {
			m.saved.list = append(m.saved.list, p)
		}
		m.savePrompts()
		m.saved.cursor = at
		m.dialog = nil
		return d.back(m)
	}
	var cmd tea.Cmd
	d.text.input, cmd = d.text.input.Update(msg)
	d.text.fit(m)
	return cmd
}

func (d *promptDialog) view(m *Model, w, h int) string {
	lines := []string{d.text.input.View(), "",
		m.onClick(checkbox(d.enter)+" press enter after it, to send it", func(*Model) tea.Cmd {
			d.enter = !d.enter
			return nil
		})}
	if !d.enter {
		lines = append(lines, dim("    else it is put in for you to change or send"))
	}
	lines = append(lines, "", dim("Kept in "+tildePath(prompts.Path(), homeDir())+", for every repository."), "",
		m.keys("enter", "save", "", newLineHint, "tab", "enter on/off", "esc", "cancel"))
	if d.err != "" {
		lines = append(lines, "", warn(d.err))
	}
	title := "New saved prompt"
	if d.editing >= 0 {
		title = "Saved prompt"
	}
	return box(title, lines, w)
}

// savedKey handles a key while F3 shows the saved prompts.
func (m *Model) savedKey(msg tea.KeyMsg) tea.Cmd {
	s := &m.saved
	k := msg.String()
	switch k {
	case "n", "a":
		return m.openPromptEdit(-1, backToSaved)
	}
	s.cursor = min(max(s.cursor, 0), max(len(s.list)-1, 0))
	i := s.cursor
	has := i < len(s.list)
	switch k {
	case "up", "k":
		s.cursor = max(i-1, 0)
	case "down", "j":
		s.cursor = min(i+1, max(len(s.list)-1, 0))
	case "K", "shift+up":
		if has && i > 0 {
			if i = m.findPrompt(i, s.list[i]); i > 0 {
				s.list[i-1], s.list[i] = s.list[i], s.list[i-1]
				s.cursor = i - 1
				m.savePrompts()
			}
		}
	case "J", "shift+down":
		if has && i+1 < len(s.list) {
			if i = m.findPrompt(i, s.list[i]); i >= 0 && i+1 < len(s.list) {
				s.list[i+1], s.list[i] = s.list[i], s.list[i+1]
				s.cursor = i + 1
				m.savePrompts()
			}
		}
	case " ", "x":
		if has {
			if i = m.findPrompt(i, s.list[i]); i >= 0 {
				s.list[i].Enter = !s.list[i].Enter
				s.cursor = i
				m.savePrompts()
			}
		}
	case "enter":
		if has {
			return m.usePrompt(m.curAgent(), s.list[i])
		}
	case "e":
		if has {
			return m.openPromptEdit(i, backToSaved)
		}
	case "y", "c":
		if has {
			m.copyText(s.list[i].Text)
		}
	case "d", "delete", "backspace":
		if has {
			if i = m.findPrompt(i, s.list[i]); i >= 0 {
				s.list = append(s.list[:i:i], s.list[i+1:]...)
				s.cursor = min(i, max(len(s.list)-1, 0))
				m.savePrompts()
			}
		}
	}
	return nil
}

// backToSaved is where the edit dialog goes back to from F3: the saved
// prompts, with the keyboard.
func backToSaved(m *Model) tea.Cmd {
	m.loadPrompts()
	m.setFocus(focusReview)
	return nil
}

// showSaved switches F3 to the saved prompts, or back to the tasks.
func (m *Model) showSaved(on bool) {
	if on && !m.saved.on {
		m.loadPrompts()
	}
	m.saved.on = on
}

// f3Views is the row over F3's list naming its two views, the one shown
// lit, each a button for it.
func (m *Model) f3Views() string {
	sel := lipgloss.NewStyle().Foreground(accent).Bold(true)
	view := func(name string, on bool) string {
		label := " " + name + " "
		if on == m.saved.on {
			label = sel.Render("[" + name + "]")
		}
		return m.onClick(label, func(m *Model) tea.Cmd {
			m.showSaved(on)
			m.setFocus(focusReview)
			return nil
		})
	}
	return " " + view("tasks", false) + dim(" · ") + view("saved prompts", true) + dim("   ←/→")
}

// savedBody draws the saved prompts, w×h, for the panel's frame.
func (m *Model) savedBody(w, h int) []string {
	s := &m.saved
	textW := max(w-8, 10) // cursor, mark and margins
	sel := lipgloss.NewStyle().Foreground(accent).Bold(true)
	lines := []string{m.f3Views(), ""}
	if len(s.list) == 0 {
		lines = append(lines, " "+dim("No saved prompts yet."), "",
			" "+dim("Keep what you type into agents again and again, such as"),
			" "+dim("\"commit, push and open a pull request\", and pick it here"),
			" "+dim("or with "+m.pk("l")+" instead of writing it out."))
	}
	s.cursor = min(max(s.cursor, 0), max(len(s.list)-1, 0))
	foot := []string{"",
		" " + dim("⏎ sent with enter   ✎ put in for you to send · "+tildePath(prompts.Path(), homeDir())),
		" " + m.msgKeys("enter", "use", " ", "enter on/off", "n", "new", "e", "edit", "y", "copy", "d", "delete", "J/K", "move", "esc", "agent"),
	}
	room := max(h-len(foot), 1)
	cursorAt, cursorRows := 0, 0
	for i, p := range s.list {
		rows := wrapRows(p.Text, textW, messageRows)
		if i == s.cursor {
			cursorAt = len(lines)
			rows = wrapLines(p.Text, textW, room)
			cursorRows = len(rows)
		}
		on := i == s.cursor && m.focus == focusReview
		mark := "  "
		if on {
			mark = sel.Render("❯ ")
		}
		box := m.onClick(promptMark(p), func(m *Model) tea.Cmd {
			m.saved.cursor = i
			return m.savedKey(keyMsg(" "))
		})
		for j, r := range rows {
			if on {
				r = sel.Render(r)
			}
			text := m.onClick(r, func(m *Model) tea.Cmd {
				m.saved.cursor = i
				m.setFocus(focusReview)
				return nil
			})
			if j == 0 {
				lines = append(lines, mark+box+" "+text)
			} else {
				lines = append(lines, "    "+text)
			}
		}
	}
	if cursorAt+cursorRows > room {
		lines = lines[min(cursorAt+cursorRows-room, len(lines)):]
	}
	body := fitLines(lines, w, room)
	for i := range foot {
		foot[i] = ansi.Truncate(foot[i], w, "…")
	}
	return append(body, foot...)
}
