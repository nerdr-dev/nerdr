package app

import (
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// menu is a list of commands that pops up where it was asked for: the
// ≡ button's menu of everything nerdr does, or what can be done to the
// tab or group that was right-clicked. It is the mouse's way to every
// command, and it shows the key each one has after the prefix, so the
// keys are learnt by using it.
type menu struct {
	title string
	items []menuItem
	// x, y is where the pointer was: the top-left corner of the menu, as
	// far as the screen allows.
	x, y int
	hi   int
	top  int
}

// menuItem is one line of a menu. An item with no run is a heading.
type menuItem struct {
	label string
	key   string // the prefix key for the same command, shown on the right
	run   func(m *Model) tea.Cmd
	// off says why the item cannot do anything now: it is drawn grey,
	// with off below the menu while it is highlighted.
	off string
}

func (it menuItem) heading() bool { return it.run == nil }

// anchored is a dialog drawn at a place on the screen rather than
// centred.
type anchored interface {
	anchor() (x, y int)
}

func (mu *menu) anchor() (int, int) { return mu.x, mu.y }

func (mu *menu) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	switch k {
	case "esc", "q", "ctrl+c":
		m.dialog = nil
		return nil
	case "up", "k", "shift+tab":
		mu.move(-1)
		return nil
	case "down", "j", "tab":
		mu.move(1)
		return nil
	case "pgdown":
		for range 8 {
			mu.move(1)
		}
		return nil
	case "pgup":
		for range 8 {
			mu.move(-1)
		}
		return nil
	case "enter", " ":
		return mu.activate(m, mu.hi)
	}
	for i, it := range mu.items {
		if !it.heading() && (it.key == k || (strings.HasPrefix(it.key, "F") && strings.ToLower(it.key) == k)) {
			return mu.activate(m, i)
		}
	}
	return nil
}

// move puts the highlight on the next item in direction d, over headings.
func (mu *menu) move(d int) {
	for i := mu.hi + d; i >= 0 && i < len(mu.items); i += d {
		if !mu.items[i].heading() {
			mu.hi = i
			return
		}
	}
}

func (mu *menu) activate(m *Model, i int) tea.Cmd {
	if i < 0 || i >= len(mu.items) || mu.items[i].heading() {
		return nil
	}
	m.dialog = nil
	if why := mu.items[i].off; why != "" {
		m.toast(why)
		return nil
	}
	return mu.items[i].run(m)
}

// menuKey is an item's keys after the prefix as they are pressed; the
// function keys need no prefix and stay as they are.
func (m *Model) menuKey(k string) string {
	if panelKey(strings.ToLower(k)) {
		return k
	}
	return m.shownKeys(k)
}

func (mu *menu) view(m *Model, w, h int) string {
	labelW, keyW := lipgloss.Width(mu.title), 0
	for _, it := range mu.items {
		labelW = max(labelW, lipgloss.Width(it.label))
		keyW = max(keyW, lipgloss.Width(m.menuKey(it.key)))
		if it.off != "" {
			labelW = max(labelW, lipgloss.Width(it.off)-keyW)
		}
	}
	labelW = min(labelW, max(8, w-keyW-8))
	rowW := labelW + 2 + keyW + 2
	room := max(1, h-5)
	if mu.hi < len(mu.items) && mu.items[mu.hi].heading() {
		mu.move(1)
	}
	if mu.hi < mu.top {
		mu.top = mu.hi
	}
	if mu.hi >= mu.top+room {
		mu.top = mu.hi - room + 1
	}
	mu.top = min(mu.top, max(0, len(mu.items)-room))
	var rows []string
	if mu.title != "" {
		rows = append(rows, lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("110")).
			Render(padTo(" "+mu.title, rowW)))
	}
	for i := mu.top; i < min(len(mu.items), mu.top+room); i++ {
		it := mu.items[i]
		if it.heading() {
			rows = append(rows, dim(padTo(" "+ansi.Truncate(it.label, rowW-1, "…"), rowW)))
			continue
		}
		label := padTo(" "+ansi.Truncate(it.label, labelW, "…"), labelW+3)
		key := padTo(m.menuKey(it.key), keyW) + " "
		var row string
		switch {
		case i == mu.hi:
			st := lipgloss.NewStyle().Background(lipgloss.Color("63")).Foreground(lipgloss.Color("231"))
			if it.off != "" {
				st = st.Foreground(lipgloss.Color("248"))
			}
			row = st.Bold(true).Render(label) + st.Render(key)
		case it.off != "":
			row = dim(label + key)
		default:
			row = label + keyHint(key)
		}
		rows = append(rows, m.zone(row, func(m *Model, ev tea.MouseMsg) tea.Cmd {
			switch {
			case ev.Action == tea.MouseActionMotion:
				mu.hi = i
			case ev.Action == tea.MouseActionPress && ev.Button == tea.MouseButtonLeft:
				return mu.activate(m, i)
			}
			return nil
		}))
	}
	if len(mu.items) > room {
		rows = append(rows, dim(padTo(" ↑↓ / wheel / PgUp PgDn", rowW)))
	}
	if mu.hi < len(mu.items) && mu.items[mu.hi].off != "" {
		rows = append(rows, warn(padTo(" "+ansi.Truncate(mu.items[mu.hi].off, rowW-1, "…"), rowW)))
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("63")).
		Render(strings.Join(rows, "\n"))
}

// openMenu shows the menu of everything nerdr does.
func (m *Model) openMenu(x, y int) {
	var items []menuItem
	for _, group := range allCommands() {
		items = append(items, menuItem{label: group.title})
		for _, entry := range group.entries {
			e := entry
			items = append(items, menuItem{label: e.label, key: e.key, off: m.commandOff(e.run), run: func(m *Model) tea.Cmd {
				return m.runCommand(e.run)
			}})
		}
	}
	m.dialog = &menu{items: items, x: x, y: y, hi: 1}
}

// tabItems are the commands on the active tab, as they are in the menus.
func (m *Model) tabItems() []menuItem {
	cmd := func(k string) func(m *Model) tea.Cmd {
		return func(m *Model) tea.Cmd { return m.runCommand(k) }
	}
	t := m.cur()
	items := []menuItem{}
	if repo := tabRepo(t); repo != "" {
		items = append(items, menuItem{label: "New tab in " + filepath.Base(repo) + "…",
			run: func(m *Model) tea.Cmd { return m.openWizardFor(repo) }},
			menuItem{label: "New tab on " + t.Worktrees[0].Branch + "…", key: "C", run: cmd("C")})
	}
	items = append(items,
		menuItem{label: "Git and GitHub"},
		menuItem{label: "Branches, PR and push", key: "i i", run: cmd("i i")},
		menuItem{label: "Since you were here", key: "h", run: cmd("h")},
		menuItem{label: "Agent and tasks"},
		menuItem{label: "Task for the agent…", key: "m", run: cmd("m")},
		menuItem{label: "Tasks for the agent", key: m.screenMenuKey(3), run: cmd("e 3")},
		menuItem{label: "Switch agent…", key: "a s", run: cmd("a s")},
		menuItem{label: "Restart " + string(t.Agent), key: "a r", run: cmd("a r")},
		menuItem{label: "Panes beside the agent"},
		menuItem{label: "Terminal", key: "t t", run: cmd("t t")},
		menuItem{label: "Another agent…", key: "t a", run: cmd("t a")},
		menuItem{label: "Code review", key: "t r", run: cmd("t r"), off: m.commandOff("t r")},
		menuItem{label: "Work tools"},
		menuItem{label: "Note for yourself…", key: "a n", run: cmd("a n")},
		menuItem{label: "Open in editor", key: "o", run: cmd("o")},
		menuItem{label: "nerdr's skills for the agent", key: m.screenMenuKey(4), run: cmd("e 4")},
		menuItem{label: "Scroll back", key: "b", run: cmd("b")},
		menuItem{label: "Tasks and tabs"},
		menuItem{label: "Rename…", key: "r", run: cmd("r")},
		menuItem{label: "Move left", key: "⇧←", run: cmd("shift+left")},
		menuItem{label: "Move right", key: "⇧→", run: cmd("shift+right")},
		menuItem{label: "Close tab…", key: "x", run: cmd("x")},
	)
	return items
}

// openTabMenu shows what can be done to tab t, which it makes the active
// tab: the commands act on that one.
func (m *Model) openTabMenu(t *tab, x, y int) tea.Cmd {
	i := m.tabIndex(t)
	if i < 0 {
		return nil
	}
	prev := m.cur()
	cmd := m.selectTab(i)
	items := m.tabItems()
	if prev != nil && prev != t && m.splitTab() != prev && prev.group() == t.group() {
		// Beside the tab that was on screen, which is where it goes back to.
		items = append([]menuItem{{label: "Show beside " + prev.Name,
			run: func(m *Model) tea.Cmd { return m.splitWith(prev, t) }}}, items...)
	}
	m.dialog = &menu{title: t.Name, items: items, x: x, y: y}
	return cmd
}

// openGroupMenu shows what can be done in group g.
func (m *Model) openGroupMenu(g string, x, y int) tea.Cmd {
	idx := m.groupTabs(g)
	if len(idx) == 0 {
		return nil
	}
	cmd := m.selectTab(m.groupTab(g))
	var items []menuItem
	if repo := tabRepo(m.tabs[idx[0]]); repo != "" && g != multiGroup {
		items = append(items, menuItem{label: "New tab in " + g + "…",
			run: func(m *Model) tea.Cmd { return m.openWizardFor(repo) }})
	} else {
		items = append(items, menuItem{label: "New tab…", key: "c",
			run: func(m *Model) tea.Cmd { return m.command(keyMsg("c")) }})
	}
	items = append(items, menuItem{label: "Task to its tabs…",
		run: func(m *Model) tea.Cmd { return m.command(keyMsg("M")) }})
	if len(m.tabs[idx[0]].Worktrees) > 1 {
		items = append(items, menuItem{label: "Rename group…",
			run: func(m *Model) tea.Cmd { return m.openGroupRename(g) }})
	}
	m.dialog = &menu{title: g, items: items, x: x, y: y}
	return cmd
}

// tabRepo is the main checkout of the repository t's worktree was made
// from, or "" for a tab spanning several.
// stackOnTab opens the dialog for a new tab whose branch starts on top of
// the active tab's.
func (m *Model) stackOnTab() tea.Cmd {
	t := m.cur()
	repo := tabRepo(t)
	if repo == "" {
		m.toast(m.commandOff("C"))
		return nil
	}
	return m.openWizardOn(repo, t.Worktrees[0].Branch)
}

func tabRepo(t *tab) string {
	if t == nil || len(t.Worktrees) != 1 {
		return ""
	}
	return t.Worktrees[0].Repo
}

func onOff(on bool, yes, no string) string {
	if on {
		return yes
	}
	return no
}

// keyMsg is the key named k, as bubbletea would report it.
func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "space", " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "shift+left":
		return tea.KeyMsg{Type: tea.KeyShiftLeft}
	case "shift+right":
		return tea.KeyMsg{Type: tea.KeyShiftRight}
	case "delete":
		return tea.KeyMsg{Type: tea.KeyDelete}
	case "ctrl+o":
		return tea.KeyMsg{Type: tea.KeyCtrlO}
	case "ctrl+x":
		return tea.KeyMsg{Type: tea.KeyCtrlX}
	case "alt+x":
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}, Alt: true}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}
