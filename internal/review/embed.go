package review

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/order"
	"github.com/nerdr-dev/nerdr/internal/walkthrough"
	"github.com/nerdr-dev/nerdr/internal/why"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// This file runs the review screen as nerdr's panel: beside an agent,
// pinned to that tab's worktrees, rather than as a screen of its own that
// browses every repository under a folder.

// Pin is one worktree the panel shows.
type Pin struct {
	Repo string // the primary repository
	Path string // the worktree
}

// LayoutMsg tells nerdr that F1's files were put beside the diff, or back
// above it: beside, they need the room, and nerdr can make the panel as
// wide as the window for them.
type LayoutMsg struct{ Beside bool }

// LeaveMsg asks nerdr to give focus back to the agent. The panel sends it
// where a program of its own would quit.
type LeaveMsg struct{}

// CopyMsg asks nerdr to put Text on the clipboard: the lines of the diff
// chosen with the mouse.
type CopyMsg struct{ Text string }

// HelpMsg asks nerdr for its list of commands: ? is help there, as
// everywhere else in nerdr.
type HelpMsg struct{}

// EditMsg asks nerdr to open an editor; the panel cannot hand the
// terminal over itself, since nerdr reads the terminal on its own.
type EditMsg struct {
	Dir  string // the worktree
	File string // the file, as a full path
	Line int    // the line to open it on; 0 for none
}

// NewEmbedded creates the panel. It shows nothing until Pin is called.
func NewEmbedded() Model {
	m := Model{
		embedded:  true,
		focus:     focusFiles,
		expanded:  map[string]bool{},
		showOrder: true,
		recent:    map[string]int{},
		spinner:   spinner.New(spinner.WithSpinner(spinner.MiniDot)),
	}
	m.filterInput = textinput.New()
	m.filterInput.Prompt = "/"
	m.filterInput.Placeholder = "filter files…"
	m.buildRelRows()
	return m
}

// Pin points the panel at a tab's worktrees. Several worktrees (one tab
// spanning several repositories) are switched between with [ and ].
func (m Model) Pin(pins []Pin) (Model, tea.Cmd) {
	if samePins(m.pins, pins) {
		return m, nil
	}
	m.pins = pins
	m.repos = nil
	for _, p := range pins {
		m.repos = append(m.repos, git.Repo{Name: filepath.Base(p.Repo), Path: p.Repo})
	}
	m.repoIdx = 0
	m.curRepo = ""
	m.wtIdx = 0
	m.overview = false
	m.zoomed = false
	if len(pins) > 0 {
		m.cwd = filepath.Dir(pins[0].Path)
	}
	m.focus = focusFiles
	m.cursor = 0
	m.pinPending = true
	return m, m.loadPinned()
}

// loadPinned shows the pinned worktree of the repository at repoIdx. It is
// what a tab switch waits for, so it runs no git on the screen's time:
// the worktree is shown at once, empty, and its branch, base, notes and
// files are read in the background (pinnedMsg). While the panel is not
// on screen, nothing is read at all; SetShown reads it once it is.
func (m *Model) loadPinned() tea.Cmd {
	if m.hidden {
		return nil
	}
	m.pinPending = false
	if m.repoIdx < 0 || m.repoIdx >= len(m.pins) || m.repoIdx >= len(m.repos) {
		m.worktrees, m.curRepo, m.wtIdx = nil, "", 0
		m.switchWorktree()
		return nil
	}
	repo, path := m.repos[m.repoIdx].Path, m.pins[m.repoIdx].Path
	m.curRepo = repo
	m.worktrees, m.wtIdx = []git.Worktree{{Path: path}}, 0
	m.switchWorktree()
	m.refreshing = true
	m.filesToken++ // a refresh of the worktree left behind is overtaken
	m.pinToken++
	token, showBase := m.pinToken, m.showBase
	var cmds []tea.Cmd
	if m.ctxLoad != "" {
		// The slow part of a worktree's context (imports, PR); Update
		// starts it when a move asks for it, but Pin is called from
		// outside Update.
		cmds = append(cmds, loadContextCmd(m.ctxLoad))
		m.ctxLoad = ""
	}
	cmds = append(cmds, func() tea.Msg {
		msg := pinnedMsg{wt: path, token: token, found: true}
		if wts, err := git.ListWorktrees(repo); err == nil {
			msg.found = false
			for _, w := range wts {
				if samePath(w.Path, path) {
					msg.found, msg.listed = true, w
				}
			}
		}
		msg.base = git.DetectBase(path)
		msg.notes = loadNotes(path)
		msg.order = order.Load(path, msg.listed.Branch)
		msg.walk, msg.hasWalk = walkthrough.Load(path, msg.listed.Branch)
		c := git.Comparison{Worktree: path}
		if showBase && msg.base != "" {
			c.Base = msg.base
		}
		if msg.files, msg.err = c.Files(); msg.err == nil {
			msg.stats = c.NumStat()
		}
		return msg
	})
	return tea.Batch(cmds...)
}

// pinnedMsg is what loadPinned read of worktree wt in the background.
type pinnedMsg struct {
	wt, base string
	token    int
	found    bool         // wt is a worktree of the repository still
	listed   git.Worktree // as git lists it: its branch and head
	notes    why.Index
	order    order.Index
	walk     walkthrough.Walkthrough
	hasWalk  bool
	files    []git.FileStatus
	stats    map[string]git.LineCount
	err      error
}

// onPinned puts what loadPinned read in place, if the panel still shows
// that worktree.
func (m *Model) onPinned(msg pinnedMsg) tea.Cmd {
	if msg.token != m.pinToken || msg.wt != m.curWT || len(m.worktrees) != 1 {
		return nil
	}
	m.refreshing = false
	if !msg.found {
		m.worktrees, m.wtIdx = nil, 0 // as filterPinned leaves it
		m.switchWorktree()
		return nil
	}
	m.worktrees[0].Branch, m.worktrees[0].Head = msg.listed.Branch, msg.listed.Head
	m.baseRef, m.whyIndex, m.orderIndex = msg.base, msg.notes, msg.order
	if msg.err != nil {
		m.setWalk(msg.walk, msg.hasWalk)
		m.err = msg.err
		return nil
	}
	m.err = nil
	m.fileStats = msg.stats
	m.setFiles(msg.files, true)
	m.setWalk(msg.walk, msg.hasWalk) // after the files, which it is older than or not
	return tea.Batch(m.loadDiffLater(), m.walkArrived())
}

// Pins is what the panel is showing.
func (m Model) Pins() []Pin { return m.pins }

func samePins(a, b []Pin) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// pinnedPath is the worktree pinned for the repository at repoIdx.
func (m Model) pinnedPath() string {
	if m.repoIdx < 0 || m.repoIdx >= len(m.pins) {
		return ""
	}
	return m.pins[m.repoIdx].Path
}

// filterPinned keeps only the pinned worktree of the current repository.
func (m Model) filterPinned(wts []git.Worktree) []git.Worktree {
	want := m.pinnedPath()
	for _, w := range wts {
		if samePath(w.Path, want) {
			return []git.Worktree{w}
		}
	}
	return nil
}

func samePath(a, b string) bool {
	if a == b {
		return true
	}
	ra, e1 := filepath.EvalSymlinks(a)
	rb, e2 := filepath.EvalSymlinks(b)
	return e1 == nil && e2 == nil && ra == rb
}

// cyclePin moves to the next or previous pinned repository.
func (m *Model) cyclePin(delta int) tea.Cmd {
	if len(m.pins) < 2 {
		m.setToast("this tab has one repository")
		return nil
	}
	m.peek = nil
	m.jumpHistory = nil
	m.repoIdx = (m.repoIdx + delta + len(m.pins)) % len(m.pins)
	m.cursor = 0
	m.setToast(m.repos[m.repoIdx].Name)
	return m.loadPinned()
}

// SetShown tells the panel whether nerdr has it on screen. Hidden, its
// file list is not refreshed; shown again, a stale one is.
func (m Model) SetShown(shown bool) (Model, tea.Cmd) {
	if m.hidden == !shown {
		return m, nil
	}
	m.hidden = !shown
	if shown && m.pinPending {
		return m, m.loadPinned()
	}
	return m, m.refreshIfStale()
}

// PinIdx is which of the pinned worktrees the panel shows.
func (m Model) PinIdx() int { return m.repoIdx }

// ShowPin shows the i-th pinned worktree, as [ and ] step to it: in F2,
// its pull request.
func (m *Model) ShowPin(i int) tea.Cmd {
	if i < 0 || i >= len(m.pins) || i == m.repoIdx {
		return nil
	}
	cmd := m.cyclePin(i - m.repoIdx)
	if m.ctxLoad != "" {
		wt := m.ctxLoad
		m.ctxLoad = ""
		cmd = tea.Batch(cmd, loadContextCmd(wt))
	}
	return cmd
}

// quit is what the quit keys do: in nerdr, hand focus back.
func (m Model) quit() tea.Cmd {
	if m.embedded {
		return func() tea.Msg { return LeaveMsg{} }
	}
	return tea.Quit
}

// SetScreenKeys gives the panel the keys to name its six screens by in
// its menu, in place of F1 to F5, and hint, which says how to press them;
// nil for the F-keys.
func (m Model) SetScreenKeys(keys []string, hint string) Model {
	m.screenKeys, m.screensHint = keys, hint
	return m
}

// SetMenu tells the panel whether it draws its menu row above the screen
// inside nerdr, which then has no column of F-keys beside it.
func (m Model) SetMenu(on bool) Model {
	m.menuOn = on
	return m
}

// SetWideKey tells the panel the keys that make it as wide as the window,
// for its hints.
func (m Model) SetWideKey(k string) Model {
	m.wideKey = k
	return m
}

// SetFocused tells the panel whether it has the keyboard, which only
// changes how its borders are drawn.
func (m Model) SetFocused(on bool) Model {
	m.unfocused = !on
	return m
}

// SetExplain tells the panel whether the agents have the nerdr-explain
// skill: without it, E and its button are not there.
func (m Model) SetExplain(on bool) Model {
	m.explainOn = on
	return m
}

// TopFocused reports whether the pane right below the menu is the one with
// the keyboard, so the menu's frame, which runs into that pane's, wears
// the same colour: in F1 that is the file list, not the diff below it.
func (m Model) TopFocused() bool {
	if m.mode == modeReview && !m.zoomed {
		return m.hasFocus(focusFiles)
	}
	return !m.unfocused
}

// filesHeader is the header contentRow counts in the file list: beside an
// agent its title is in the border (see renderTitledPane), and its heading
// row is the repositories' bar or none.
func (m Model) filesHeader() int {
	if m.embedded && !m.hasRepoBar() {
		return titledHeader
	}
	return 0
}

// diffHeader is filesHeader for the diff, whose title is in the border
// beside an agent too (renderTitledDiff).
func (m Model) diffHeader() int {
	if m.embedded {
		return titledHeader
	}
	return 0
}

// The file list beside an agent is above the diff, as the panel is a
// narrow column; in a panel with the screen to itself and wide enough for
// both, or when L puts it there, it is beside the diff, which keeps the
// height.

// BesideWidth is how wide the panel, alone on the screen, is before F1's
// file list goes beside the diff by itself.
const BesideWidth = 130

// paneSide is where F1's file list is beside an agent.
type paneSide int

const (
	sideAuto    paneSide = iota // beside when the panel is alone and BesideWidth wide
	sideBeside                  // beside the diff
	sideStacked                 // above the diff
)

// sideBySide reports whether the file list is beside the diff in a panel
// W wide.
func (m Model) sideBySide(W int) bool {
	switch m.side {
	case sideBeside:
		return true
	case sideStacked:
		return false
	}
	return m.alone && W >= BesideWidth
}

// SetAlone tells the panel whether it has the screen to itself, with no
// agent beside it: only then does F1's file list go beside the diff by
// itself.
func (m Model) SetAlone(alone bool) Model {
	if m.alone == alone {
		return m
	}
	m.alone = alone
	m.setDiffCursor(m.diffCursor) // the diff's height may have changed
	return m
}

// toggleSide is L: the file list beside the diff, or above it.
func (m *Model) toggleSide() tea.Cmd {
	beside := !m.sideBySide(m.width)
	m.side = sideStacked
	if beside {
		m.side = sideBeside
	}
	m.setDiffCursor(m.diffCursor) // the diff's height changed
	return func() tea.Msg { return LayoutMsg{Beside: beside} }
}

// embeddedLayout lays the panes out beside an agent: the file list above
// the diff, or beside it (see sideBySide).
func (m Model) embeddedLayout(W, H int) paneLayout {
	var l paneLayout
	l.menu = m.menuRect(W)
	// No relations pane and no status bar beside an agent: the PR is on
	// F2, a why note opens in the diff, and nerdr shows the toasts.
	rest := max(H-m.statusRows()-m.menuRows(), 6)
	if m.sideBySide(W) {
		filesW := min(max(W*3/10, 32), 56)
		l.files = rect{0, m.menuRows(), filesW, rest}
		l.diff = rect{filesW, m.menuRows(), max(W-filesW, 20), rest}
		return l
	}
	rows := len(m.flatRows())
	if rows == 0 {
		empty, _ := m.emptyFiles() // what the pane says instead, button and all
		rows = len(empty)
	}
	filesH := min(max(rest*2/5, 5), max(rows+3+hintRows+m.filesHeader(), 5))
	diffH := max(rest-filesH, 3)
	l.files = rect{0, m.menuRows(), W, filesH}
	l.diff = rect{0, m.menuRows() + filesH, W, diffH}
	return l
}

// SetShared tells the panel which of the files it shows other tabs change
// too: the names of those tabs, by SharedKey. The file list marks them ⚠.
func (m Model) SetShared(shared map[string][]string) Model {
	m.shared = shared
	return m
}

// SharedKey is the key of a file of repository repo (its main checkout)
// in what SetShared is given.
func SharedKey(repo, file string) string { return repo + "\n" + file }

// sharedWith is the other tabs changing path too, in the repository shown.
func (m Model) sharedWith(path string) []string {
	if len(m.shared) == 0 || m.repoIdx >= len(m.repos) {
		return nil
	}
	return m.shared[SharedKey(m.repos[m.repoIdx].Path, path)]
}

// statusRows is the rows the status bar takes: none beside an agent,
// but for the filter while one is typed.
func (m Model) statusRows() int {
	if m.embedded && !m.filtering {
		return 0
	}
	return 1
}

// PopToast takes the panel's toast, for nerdr to show: the panel has no
// status bar of its own beside an agent.
func (m *Model) PopToast() string {
	s := m.toast
	if m.embedded {
		m.toast, m.toastTicks = "", 0
	}
	return s
}

// F1 in a tab on several repositories has a bar of them at the top of the
// file list, in the row its heading would have: the one shown highlighted,
// each with how many files its branch changes, and a button for its own
// changes. ↑ from the first file goes on to the bar, where ← and → step
// along it, as [ and ] do from anywhere.

// SetPinCounts tells the panel how many files each pinned worktree's
// branch changes, in the pins' order; -1 when not known yet. The bar
// shows them.
func (m Model) SetPinCounts(counts []int) Model {
	m.pinCounts = counts
	return m
}

// hasRepoBar reports whether the file list starts with the bar.
func (m Model) hasRepoBar() bool {
	return m.embedded && len(m.pins) > 1 && len(m.repos) == len(m.pins)
}

// repoBarItem is one repository on the bar: the pin it shows, and where
// it is drawn.
type repoBarItem struct {
	pin, x int
	label  string
}

// repoBar lays out the bar's items in a row w wide, moved along so the
// one shown is in view when they do not all fit.
func (m Model) repoBar(w int) []repoBarItem {
	if !m.hasRepoBar() {
		return nil
	}
	var items []repoBarItem
	x, selEnd := 0, 0
	for i, r := range m.repos {
		n := -1
		if i < len(m.pinCounts) {
			n = m.pinCounts[i]
		}
		if i == m.repoIdx {
			n = len(m.files)
		}
		label := " " + printableLine(r.Name)
		if n >= 0 {
			label += " " + strconv.Itoa(n)
		}
		label += " "
		items = append(items, repoBarItem{pin: i, x: x, label: label})
		x += lipgloss.Width(label) + 1
		if i == m.repoIdx {
			selEnd = x - 1
		}
	}
	if shift := selEnd - w; shift > 0 {
		for i := range items {
			items[i].x -= shift
		}
	}
	return items
}

// repoBarView draws the bar, w wide.
func (m Model) repoBarView(w int) string {
	var b strings.Builder
	at := 0
	for _, it := range m.repoBar(w) {
		label := it.label
		if it.x < 0 {
			if it.x+lipgloss.Width(label) <= 0 {
				continue
			}
			label = ansi.Cut(label, -it.x, lipgloss.Width(label))
		}
		style := menuItemStyle
		switch {
		case it.pin == m.repoIdx && m.onRepoBar && m.hasFocus(focusFiles):
			style = selectedStyle
		case it.pin == m.repoIdx:
			style = menuActiveStyle
		}
		b.WriteString(strings.Repeat(" ", max(it.x, at)-at) + style.Render(label))
		at = max(it.x, at) + lipgloss.Width(label)
	}
	hint := "  [ ] switch"
	if m.onRepoBar && m.hasFocus(focusFiles) {
		hint = "  ← → switch · ↓ files"
	}
	if at+lipgloss.Width(hint) <= w {
		b.WriteString(styleHints(hint))
	}
	return fit(b.String(), w)
}

// repoBarAt is the pin whose item on the bar, w wide, is at x.
func (m Model) repoBarAt(w, x int) (int, bool) {
	for _, it := range m.repoBar(w) {
		if x >= it.x && x < it.x+lipgloss.Width(it.label) {
			return it.pin, true
		}
	}
	return 0, false
}

// repoBarKey handles a key while the bar has the cursor, reporting
// whether it was the bar's.
func (m *Model) repoBarKey(k string) (tea.Cmd, bool) {
	switch k {
	case "left", "h":
		return m.ShowPin(max(m.repoIdx-1, 0)), true
	case "right", "l":
		return m.ShowPin(min(m.repoIdx+1, len(m.pins)-1)), true
	case "up", "k":
		return nil, true
	case "down", "j", "enter":
		m.onRepoBar = false
		return nil, true
	}
	m.onRepoBar = false
	return nil, false
}

// hasFocus reports whether pane f is the one keys go to — never while the
// agent beside the panel has the keyboard.
func (m Model) hasFocus(f focus) bool {
	return !m.unfocused && m.focus == f
}

// SetUnseen marks content changed while the active tab was away, for this visit.
func (m Model) SetUnseen(files map[string]map[string]bool) Model { m.unseen = files; return m }
