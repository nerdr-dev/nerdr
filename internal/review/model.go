package review

import (
	"os/exec"
	"path/filepath"
	"time"

	"github.com/nerdr-dev/nerdr/internal/analysis"
	"github.com/nerdr-dev/nerdr/internal/browser"
	"github.com/nerdr-dev/nerdr/internal/editor"
	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/github"
	"github.com/nerdr-dev/nerdr/internal/launch"
	"github.com/nerdr-dev/nerdr/internal/order"
	"github.com/nerdr-dev/nerdr/internal/watch"
	"github.com/nerdr-dev/nerdr/internal/why"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type focus int

const (
	focusRepos focus = iota
	focusWorktrees
	focusFiles
	focusDiff
	focusRelations
)

// diffMode selects how the diff pane renders the currently selected file.
type diffMode int

const (
	diffModeHunk    diffMode = iota // default: unified diff with limited context
	diffModeOld                     // full pre-change content, unmarked
	diffModeNewFull                 // full current content, changes still marked
)

// refreshInterval is how often the file list is polled. A var only so
// tests can shorten it.
var refreshInterval = 1500 * time.Millisecond

type tickMsg struct{}

type editorDoneMsg struct{}

// toastMsg carries a line of feedback from a background command.
type toastMsg struct{ text string }

type Model struct {
	cwd string

	repos        []git.Repo
	repoIdx      int
	lastRepoScan time.Time
	curRepo      string // path loadWorktrees last loaded, so a genuine repo change resets wtIdx to 0
	worktrees    []git.Worktree
	wtIdx        int
	pinCounts    []int    // files each pinned worktree changes; see SetPinCounts
	onRepoBar    bool     // the file list's cursor is on the repositories' bar
	side         paneSide // where F1's file list is beside an agent
	alone        bool     // no agent beside the panel (SetAlone)
	diffColors   string   // the scheme changes are drawn in; see colors.go
	// hidden is set by nerdr while the panel is not on screen; see
	// SetShown. Off F1, or hidden, the file list is not refreshed, only
	// marked stale, and refreshed on coming back.
	hidden     bool
	filesStale bool
	// A refresh of the file list, and a load of the diff, run in the
	// background (see files.go); the tokens tell an answer still wanted
	// from one overtaken.
	filesToken, diffToken int
	filesBusy, filesAgain bool
	files                 []git.FileStatus
	tree                  []*treeNode
	cursor                int
	expanded              map[string]bool
	unseen                map[string]map[string]bool
	recent                map[string]int
	// stamps are the changed files' write stamps at the last refresh; see
	// fileStamps.
	stamps      map[string]string
	diff        string
	diffLines   []diffLine
	diffCursor  int
	diffViewTop int // first visible row; persists so the cursor can move within the current view before it scrolls
	diffMode    diffMode
	curWT       string

	baseRef  string
	showBase bool

	// fileStats is the lines each changed file adds and removes, as the
	// file list shows them; shared is the other tabs changing the same
	// files, by repository and path (see SetShared).
	fileStats map[string]git.LineCount
	shared    map[string][]string

	toast      string
	toastTicks int

	overview bool
	ovItems  []ovItem
	ovCursor int
	ovToken  int // which buildOverview a count belongs to

	filter      string
	filtering   bool
	filterInput textinput.Model
	hideIgnored bool

	focus      focus
	width      int
	height     int
	err        error
	refreshing bool

	project *analysis.Project
	// ctxLoad is a worktree whose project and PR are still to be loaded;
	// see Update.
	ctxLoad string
	// projectLoad is set while the selected worktree's project is being
	// read again in the background; see refreshFiles.
	projectLoad bool
	// watcher reports changes to the selected worktree, so its files are
	// refreshed when something happens rather than polled for. nil until
	// it is set up, or when the kernel refused it; the tick polls then.
	watcher *watch.Watcher
	// pollTicks counts ticks since the files were last polled with a
	// watch in place; see tickMsg.
	pollTicks int
	// watchN counts the changes the watch has reported; see WatchChanges.
	watchN int
	// pinPending is set while the worktree pinned last is still to be
	// shown, as the panel was not on screen; pinToken tells the reads of
	// one pinned apart from those of the one before. See loadPinned.
	pinPending bool
	pinToken   int

	pr    github.PR
	prOK  bool
	prMsg string

	// mode is the top-level menu selection. The PR screen (F2) shows pr,
	// the worktree's own; these are its cursor and what it is waiting on.
	mode       uiMode
	prToken    int // identifies the in-flight reload, so stale replies are dropped
	lastPRLoad time.Time
	// prEvery is how often the shown pull request is read again from
	// GitHub; prRefreshInterval when not set.
	prEvery       time.Duration
	prConvViewTop int
	prConvCursor  int  // which conversation block is selected
	prBusy        bool // reading a failed build's log for f
	// prShown is which of F2's views is on: only inside nerdr, which draws
	// all your pull requests and the issues. prNews and issueNews are
	// whether nerdr has news of them not seen yet, marked on F2.
	prShown   PRView
	prNews    bool
	issueNews bool
	// branch is what nerdr says about the tab's branches, shown above the
	// worktree's PR: where they stand, and the keys that push them and
	// open the pull request, which nerdr handles (see SetBranch).
	branch []string

	// orderIndex is the reading order an agent recorded for this branch's
	// change, and showOrder whether the file list follows it. Off leaves
	// the list exactly as it is without one.
	orderIndex order.Index
	showOrder  bool
	// walk is whether an agent wrote a walkthrough of this branch's
	// change, which g opens. walkWanted is the worktree g asked its agent
	// for one in, for nerdr to open it when it is written: g once, not
	// once to ask and again to look. walkNudged is g pressed again while
	// the agent writes it: once more asks anew, for an agent that never
	// will. walkKey tells one walkthrough from the next, and walkBefore
	// is the one there was when G asked for a new one, which does not
	// open as the new one. walkOld is the change moved on since it was
	// written.
	walk       bool
	walkKey    string
	walkBefore string
	walkOld    bool
	walkWanted string
	walkNudged bool
	// folderTree lists the files in their folders, as GitHub does,
	// instead of by reading order or imports (t). showFolder puts the
	// folder behind a file's name where the reading order has a line on
	// it, instead of that line (d).
	folderTree bool
	showFolder bool

	// whyIndex holds the rationale notes agents wrote for this worktree;
	// whyDetail is the one currently opened in the relations panel.
	whyIndex  why.Index
	whyDetail string

	// zoomed expands the focused pane to fill the window, for reading a
	// long diff or a long answer without the other panes taking the space.
	zoomed bool

	// mouseOn reports whether mouse reporting is enabled. It is off by
	// default: the terminal hands the app every click and drag once it is
	// on, which takes select-and-copy away from a tool whose whole purpose
	// is reading code.
	mouseOn bool

	relationsTab int
	relCursor    int
	relViewTop   int

	// dragging is the pane whose scrollbar is being dragged, in dragRect,
	// its column header dragHeader rows; see scrollbar.go.
	dragging   scrollPane
	dragRect   rect
	dragHeader int

	// diffSel is the diff's lines chosen with the mouse, from the line
	// pressed on to the one dragged to, in selRect with selHeader rows
	// above its content while the button is held (selecting); see
	// select.go.
	diffSel   lineSpan
	selecting bool
	selRect   rect
	selHeader int
	relRows   []relLine // the active tab's rows, rebuilt by buildRelRows; the key handler acts on relRows[relCursor]

	// symbols and their references drive the diff pane's "↳ used in"
	// annotations, and the jump those rows offer.
	symbols    []analysis.Symbol
	symbolRefs [][]analysis.Ref

	// peek, when set, shows an arbitrary file's current content in the diff
	// pane instead of the tree-selected file — used when jumping to a
	// reference in a file that isn't part of the changed-file tree.
	peek        *peekTarget
	jumpHistory []jumpState

	spinner spinner.Model
	// spinning is whether the spinner's tick is running. It runs only while
	// refreshing, when the spinner is shown: each tick redraws the screen.
	spinning bool

	// embedded is set when the model runs as nerdr's review panel; see
	// embed.go. pins are the tab's worktrees, and unfocused is whether the
	// agent beside the panel has the keyboard.
	embedded bool
	// wideKey is the keys that make the panel as wide as the window, for
	// its hints: nerdr's, after its prefix.
	wideKey string
	// screenKeys name the screens in the menu in place of F1 to F5, and
	// screensHint says how they are pressed; see SetScreenKeys.
	screenKeys  []string
	screensHint string
	// menuOn is set inside nerdr while the panel is kept open, when it
	// has its menu row as on its own, in place of nerdr's column; see
	// SetMenu.
	menuOn    bool
	pins      []Pin
	unfocused bool
	// prOnRepos is set while the cursor is on the list of repositories
	// above the PR in F2, in a tab on several: ↑ ↓ pick one there.
	prOnRepos bool
	// explainOn is whether the agents have the nerdr-explain skill, which
	// a asks for: off in F4, there is no a.
	explainOn bool
}

// EnableMouse starts with mouse reporting on, for the --mouse flag. The
// program has to be started with tea.WithMouseCellMotion() to match.
func (m Model) EnableMouse() Model {
	m.mouseOn = true
	return m
}

// toggleMouse turns mouse reporting on or off while running. It is a toggle
// rather than a setting because the cost is not the app's to judge: with it
// on you get the wheel, with it off you get the terminal's own
// select-and-copy, and which you want changes minute to minute.
func (m *Model) toggleMouse() tea.Cmd {
	m.mouseOn = !m.mouseOn
	if m.mouseOn {
		m.setToast("mouse on — the terminal's own text selection needs shift")
		return tea.EnableMouseCellMotion
	}
	m.setToast("mouse off")
	return tea.DisableMouse
}

// New creates the root model, discovering repositories under cwd.
func New(cwd string) Model {
	repos, err := git.DiscoverRepos(cwd)
	m := Model{
		cwd:       cwd,
		repos:     repos,
		focus:     focusRepos,
		err:       err,
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

func (m Model) Init() tea.Cmd {
	// A worktree picked in New has its context loaded by the first Update:
	// the window size arrives at once.
	return tickCmd()
}

// tickCmd schedules the next refresh tick. Re-issued on every tick — see
// the tickMsg branch of Update.
func tickCmd() tea.Cmd {
	return tea.Every(refreshInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

// Update hands msg to update, then starts loading a worktree's context if
// handling it moved to a new one. The move can happen almost anywhere, deep
// in code that has no command to return, so it is picked up here instead.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	nm, cmd := m.update(msg)
	mm, ok := nm.(Model)
	if !ok {
		return nm, cmd
	}
	if mm.refreshing && !mm.spinning {
		mm.spinning = true
		cmd = tea.Batch(cmd, mm.spinner.Tick)
	}
	if mm.ctxLoad == "" {
		return mm, cmd
	}
	wt := mm.ctxLoad
	mm.ctxLoad = ""
	return mm, tea.Batch(cmd, loadContextCmd(wt))
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case ovStatsMsg:
		if msg.token != m.ovToken || len(msg.stats) != len(m.ovItems) {
			return m, nil // counts for an overview that has been rebuilt
		}
		for i, st := range msg.stats {
			it := &m.ovItems[i]
			it.files, it.add, it.del, it.counted = st.files, st.add, st.del, true
		}
		return m, nil

	case worktreeContextMsg:
		if msg.wt != m.curWT {
			if msg.watcher != nil {
				msg.watcher.Close()
			}
			return m, nil // the cursor has moved on to another worktree
		}
		var wait tea.Cmd
		if msg.watcher != nil {
			m.stopWatch()
			m.watcher = msg.watcher
			wait = waitForChange(m.watcher, msg.wt)
		}
		m.project = msg.project
		m.pr, m.prOK, m.prMsg = msg.pr, msg.prOK, msg.prMsg
		m.lastPRLoad = time.Now()
		// The tree groups files by the project's imports, and the diff is
		// annotated from it, so both are rebuilt now it has arrived.
		if m.peek == nil {
			path, _ := m.currentFile()
			m.rebuildTree(path)
		}
		m.loadDiff()
		return m, wait

	case projectMsg:
		if msg.wt != m.curWT {
			return m, nil // a worktree left behind; its load was forgotten
		}
		m.projectLoad = false
		if m.project == nil || m.project.Same(msg.project) {
			return m, nil
		}
		m.project = msg.project
		path, _ := m.currentFile()
		m.rebuildTree(path)
		return m, m.loadDiff()

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case spinner.TickMsg:
		if !m.refreshing {
			m.spinning = false
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tickMsg:
		m.decay()
		// tea.Every is a one-shot timer, so the next tick has to be asked
		// for here. Without it the whole poll stops after its first firing:
		// no live refresh, and the ● markers never decay.
		// With a watch the files are refreshed when it reports a change;
		// the tick still polls now and then, in case it missed one.
		var refresh tea.Cmd
		m.pollTicks++
		if m.watcher == nil || m.pollTicks >= watchedPollTicks {
			m.pollTicks = 0
			refresh = m.refreshIfShown()
		}
		return m, tea.Batch(refresh, m.autoRefresh(), tickCmd())

	case fsChangedMsg:
		if m.watcher == nil || msg.wt != m.curWT {
			return m, nil // a watch that has been replaced
		}
		m.watchN++
		return m, tea.Batch(m.refreshIfShown(), m.lookForWalk(), waitForChange(m.watcher, msg.wt))

	case walkMsg:
		return m, m.onWalk(msg)

	case filesMsg:
		return m, m.onFiles(msg)

	case pinnedMsg:
		return m, m.onPinned(msg)

	case diffMsg:
		m.onDiff(msg)
		return m, nil

	case editorDoneMsg:
		return m, m.refreshFiles()

	case toastMsg:
		m.setToast(msg.text)
		return m, nil

	case prReloadMsg:
		if msg.token != m.prToken || msg.wt != m.curWT {
			return m, nil // superseded, or about a worktree left behind
		}
		m.pr, m.prOK, m.prMsg = msg.pr, msg.ok, msg.msg
		m.buildRelRows()
		return m, nil

	case prFixMsg:
		m.prBusy = false
		if msg.err != nil {
			m.setToast(msg.err.Error())
			return m, nil
		}
		text := fixPrompt(msg.pr, msg.logs)
		return m, func() tea.Msg { return AgentMsg{Dir: msg.wt, Text: text} }

	case tea.MouseMsg:
		return m, m.handleMouse(msg)

	case tea.KeyMsg:
		m.diffSel = lineSpan{} // a key may move or change the lines it is of
		if m.filtering {
			switch msg.String() {
			case "f1", "f2", "f3", "f4", "f5":
				m.filtering = false // the filter stays, as with enter; the key goes on to the menu
			default:
				return m.handleFilterKey(msg)
			}
		}
		if mode, ok := m.keyMode(msg.String()); ok {
			m.overview = false
			return m, m.setMode(mode)
		}
		if m.overview {
			return m.handleOverviewKey(msg)
		}
		// The menu is above everything else, so its keys work in either
		// mode and are checked first.
		switch msg.String() {
		case "m":
			// The same menu without a function key: on a Mac laptop those
			// are the brightness and volume keys unless the system setting
			// is changed, so F1 alone would make the menu unreachable.
			modes := m.menuModes()
			next := modes[0]
			for i, mode := range modes {
				if mode == m.mode && i+1 < len(modes) {
					next = modes[i+1]
				}
			}
			return m, m.setMode(next)
		case "M":
			if m.embedded {
				return m, nil // nerdr reads the terminal itself; see input.Router
			}
			return m, m.toggleMouse()
		case "?":
			if m.embedded {
				// ? is help everywhere in nerdr: its commands, keys and all.
				return m, func() tea.Msg { return HelpMsg{} }
			}
		}
		if m.nerdrDraws() {
			// The shell has the keys while it runs; these arrive only
			// when it has exited, and nerdr handles enter. The messages'
			// keys are nerdr's.
			if k := msg.String(); k == "q" || k == "esc" || k == "ctrl+c" {
				return m, m.quit()
			}
			return m, nil
		}
		if m.mode == modePRs {
			if cmd, handled := m.handlePRKey(msg.String()); handled {
				return m, cmd
			}
			// Nothing else falls through: the review layout's keys act on
			// panes that aren't on screen, and silently toggling the base
			// diff or the file filter from here would only surprise.
			if k := msg.String(); k == "q" || k == "ctrl+c" {
				return m, m.quit()
			}
			return m, nil
		}
		if m.onRepoBar {
			if m.focus != focusFiles || !m.hasRepoBar() {
				m.onRepoBar = false
			} else if cmd, ok := m.repoBarKey(msg.String()); ok {
				return m, cmd
			}
		}
		switch msg.String() {
		case "ctrl+c", "q":
			return m, m.quit()

		case "up", "k":
			if m.focus == focusFiles && m.cursor == 0 && m.hasRepoBar() {
				m.onRepoBar = true
				return m, nil
			}
			m.move(-1)
			return m, m.onFocusMove()

		case "down", "j":
			m.move(1)
			return m, m.onFocusMove()

		case "pgup":
			m.page(-1)
			return m, nil

		case "pgdown":
			m.page(1)
			return m, nil

		case "right", "enter", "l":
			if m.focus == focusFiles {
				if m.openOrDescend(msg.String() == "enter") {
					return m, m.loadFocus()
				}
				return m, nil
			}
			if m.focus == focusDiff {
				if m.jumpToSelected() || m.openWhyDetail() {
					return m, nil
				}
			}
			if m.focus == focusRelations {
				if cmd, handled := m.activateRelationsRow(); handled {
					return m, cmd
				}
			}
			m.focusRight()
			return m, m.loadFocus()

		case "u":
			m.jumpBack()
			return m, nil

		case "left", "esc", "h":
			if msg.String() != "esc" && m.focus == focusFiles {
				rows := m.flatRows()
				if m.cursor >= 0 && m.cursor < len(rows) && rows[m.cursor].node.isDir {
					m.expanded[rows[m.cursor].node.path] = false
					return m, nil
				}
			}
			if m.embedded && m.focus <= focusFiles {
				if msg.String() == "esc" {
					return m, m.quit()
				}
				return m, nil
			}
			m.focusLeft()
			return m, nil

		case "n":
			m.moveToNextFile(1)
			return m, m.loadDiff()

		case "p":
			m.moveToNextFile(-1)
			return m, m.loadDiff()

		case "b":
			return m, m.toggleBase()

		case "v":
			return m, m.cycleDiffMode()

		case "[":
			if m.embedded {
				return m, m.cyclePin(-1)
			}
			m.cycleWorktree(-1)
			return m, nil

		case "]":
			if m.embedded {
				return m, m.cyclePin(1)
			}
			m.cycleWorktree(1)
			return m, nil

		case "w":
			return m, m.toggleOverview()

		case "c":
			return m, m.cycleDiffColors()

		case "L":
			if m.embedded {
				return m, m.toggleSide()
			}
			return m, nil

		case "/":
			m.startFilter()
			return m, nil

		case "o":
			return m, m.openEditor()

		case "t":
			return m, m.cycleFileList()

		case "d":
			return m, m.toggleFolderNote()

		case "e":
			return m, m.explainFile()

		case "E":
			return m, m.askExplain()

		case "g":
			return m, m.openWalkthrough()

		case "G":
			return m, m.askWalk()

		case "i":
			prev, _ := m.currentFile()
			m.hideIgnored = !m.hideIgnored
			m.rebuildTree(prev)
			return m, m.loadDiff()

		case "a":
			return m, m.askSelected(false)

		case "s":
			return m, m.askSelected(true)

		case "z":
			m.zoomed = !m.zoomed
			// The viewports are sized from the pane heights, which just
			// changed, so re-sync both cursors against the new geometry.
			m.setDiffCursor(m.diffCursor)
			m.setRelCursor(m.relCursor)
			return m, nil

		case "tab":
			m.setRelationsTab(m.relationsTab + 1)
			return m, nil

		case "shift+tab":
			m.setRelationsTab(m.relationsTab - 1)
			return m, nil

		case "1", "2":
			m.setRelationsTab(int(msg.String()[0] - '1'))
			return m, nil
		}
	}

	return m, nil
}

// setRelationsTab selects a relations tab, wrapping at both ends so tab
// and shift+tab cycle. The cursor is reset because the new tab's rows are
// unrelated to the old ones.
func (m *Model) setRelationsTab(i int) {
	m.relationsTab = ((i % relationsTabCount) + relationsTabCount) % relationsTabCount
	m.relCursor, m.relViewTop = 0, 0
	m.buildRelRows()
}

// move shifts the cursor within the currently focused pane.
func (m *Model) move(delta int) {
	switch m.focus {
	case focusRepos:
		m.repoIdx = clamp(m.repoIdx+delta, 0, len(m.repos)-1)
	case focusWorktrees:
		m.wtIdx = clamp(m.wtIdx+delta, 0, len(m.worktrees)-1)
	case focusFiles:
		m.moveCursor(delta)
	case focusDiff:
		m.setDiffCursor(m.diffCursor + delta)
	case focusRelations:
		m.setRelCursor(m.relCursor + delta)
	}
}

// onFocusMove reloads content when the cursor changes the selected context.
// Browsing repos only refreshes the worktree list and blanks what sits
// below it: which files changed is a property of a worktree, and a repo
// with several of them has no answer until one is picked. Browsing
// worktrees does load the files, so the list follows the selection.
func (m *Model) onFocusMove() tea.Cmd {
	switch m.focus {
	case focusRepos:
		return m.onRepoMove()
	case focusWorktrees:
		return m.onWorktreeMove()
	case focusFiles:
		return m.loadDiff()
	}
	return nil
}

// onRepoMove and onWorktreeMove are what a move in those panes entails,
// named separately because the mouse can move a pane it has not focused.
func (m *Model) onRepoMove() tea.Cmd {
	m.loadWorktrees()
	m.resetFilesPane()
	return nil
}

func (m *Model) onWorktreeMove() tea.Cmd {
	m.loadFiles()
	return m.loadDiff()
}

func (m *Model) focusRight() {
	last := focusRelations
	if m.embedded {
		last = focusDiff // no relations pane beside an agent
	}
	if m.focus < last {
		m.focus++
	}
}

func (m *Model) focusLeft() {
	if m.focus > focusRepos {
		m.focus--
	}
}

// loadFocus loads the data required by the currently focused pane.
func (m *Model) loadFocus() tea.Cmd {
	switch m.focus {
	case focusRepos:
		return nil
	case focusWorktrees:
		// Entering the worktree pane picks the worktree the cursor is
		// already on, so the files for it load here too.
		m.loadWorktrees()
		m.loadFiles()
		return m.loadDiff()
	case focusFiles:
		return m.loadFiles()
	case focusDiff:
		return m.loadDiff()
	}
	return nil
}

// page moves the focused pane's cursor by one screenful, in whichever
// direction dir points.
func (m *Model) page(dir int) {
	switch m.focus {
	case focusRelations:
		m.setRelCursor(m.relCursor + dir*m.relationsInnerHeight())
	default:
		m.scrollDiffTo(m.diffViewTop + dir*m.diffInnerHeight())
	}
}

// openEditor opens the selected file in the user's editor: a terminal
// one suspends the TUI until it exits, one with its own window is started
// beside it.
func (m *Model) openEditor() tea.Cmd {
	path, line, ok := m.editorTarget()
	if !ok {
		return nil
	}
	dir := m.worktrees[m.wtIdx].Path
	if m.embedded {
		return func() tea.Msg { return EditMsg{Dir: dir, File: path, Line: line} }
	}
	c := editor.FileCommand(dir, path, line)
	if c.Window {
		if err := launch.Detached(c.Argv); err != nil {
			m.setToast(c.Argv[0] + ": " + err.Error())
		}
		return nil
	}
	return tea.ExecProcess(exec.Command(c.Argv[0], c.Argv[1:]...), func(error) tea.Msg { return editorDoneMsg{} })
}

// editorTarget is the selected file, as a full path, and the line being
// read in it (0 for none): pressing o almost always means "fix this", and
// hunting the place back down in the editor is the whole cost the key
// exists to save.
func (m *Model) editorTarget() (string, int, bool) {
	path, ok := m.currentFile()
	if !ok || m.wtIdx < 0 || m.wtIdx >= len(m.worktrees) {
		return "", 0, false
	}
	full := filepath.Join(m.worktrees[m.wtIdx].Path, filepath.FromSlash(path))
	return full, max(m.currentNewLine(), 0), true
}

// moveCursor moves the cursor within the visible tree rows.
func (m *Model) moveCursor(delta int) {
	m.clearPeek()
	rows := m.flatRows()
	m.cursor = clamp(m.cursor+delta, 0, len(rows)-1)
}

// moveToNextFile moves the cursor to the next/previous file row (skipping
// directories), wrapping at the ends.
func (m *Model) moveToNextFile(delta int) {
	m.clearPeek()
	rows := m.flatRows()
	var files []int
	for i, r := range rows {
		if !r.node.isDir {
			files = append(files, i)
		}
	}
	if len(files) == 0 {
		return
	}
	pos := -1
	if delta > 0 {
		for i, fi := range files {
			if fi > m.cursor {
				pos = i
				break
			}
		}
		if pos == -1 {
			pos = 0
		}
	} else {
		for i := len(files) - 1; i >= 0; i-- {
			if files[i] < m.cursor {
				pos = i
				break
			}
		}
		if pos == -1 {
			pos = len(files) - 1
		}
	}
	m.cursor = files[pos]
}

// openOrDescend opens a directory (or toggles it for Enter), or descends into
// the diff pane on a file. It reports whether focus moved to the diff pane.
func (m *Model) openOrDescend(toggle bool) bool {
	rows := m.flatRows()
	if m.cursor < 0 || m.cursor >= len(rows) || !rows[m.cursor].node.isDir {
		m.focusRight()
		return true
	}
	n := rows[m.cursor].node
	m.expanded[n.path] = !toggle || !m.isExpanded(n)
	return false
}

// decay ages out transient state (toast and recent markers).
func (m *Model) decay() {
	if m.toastTicks > 0 {
		m.toastTicks--
		if m.toastTicks == 0 {
			m.toast = ""
		}
	}
	for k, v := range m.recent {
		if v <= 1 {
			delete(m.recent, k)
		} else {
			m.recent[k] = v - 1
		}
	}
}

func (m *Model) setToast(s string) {
	m.toast = s
	m.toastTicks = 4
}

// relationsPaneHeight returns the height of the relations panel row. h is
// m.height, or 0 to use the same fallback View() uses when the terminal
// size is unknown.
func (m Model) relationsPaneHeight(h int) int {
	if h == 0 {
		h = 32
	}
	if m.embedded {
		return 0 // see embeddedLayout
	}
	if m.zoomed {
		if m.focus == focusRelations {
			return h - m.statusRows() - m.menuRows() // everything but the menu and status bar
		}
		return 0
	}
	if h-1 < 24 {
		return 7
	}
	return 10
}

// topPaneHeight returns the height of the repo/worktree/files/diff row —
// shared by View() (to lay out those panes) and the diff-pane cursor logic
// (to know the viewport height without re-rendering). h is m.height, or 0
// to use the same fallback View() uses when the terminal size is unknown.
func (m Model) topPaneHeight(h int) int {
	if h == 0 {
		h = 32
	}
	if m.zoomed {
		if m.focus == focusRelations {
			return 3 // not shown; keep it valid for any incidental caller
		}
		return h - m.statusRows() - m.menuRows()
	}
	topH := h - m.statusRows() - m.menuRows() - m.relationsPaneHeight(h)
	if topH < 3 {
		topH = 3
	}
	return topH
}

// diffInnerWidth is the width of the diff pane's text, roughly: the pane
// less its border and gutter.
func (m Model) diffInnerWidth() int {
	return m.diffPaneWidth() - 2 - 8
}

// diffPaneWidth is the width the diff pane is drawn.
func (m Model) diffPaneWidth() int {
	W, H := m.width, m.height
	if W == 0 {
		W = 120
	}
	if H == 0 {
		H = 32
	}
	w := m.layout(W, H).diff.w
	if m.zoomed || w == 0 {
		w = W
	}
	return w
}

// diffView is the diff as the pane wraps it at its size now.
func (m Model) diffView() diffView {
	return newDiffView(m.diffLines, m.diffPaneWidth(), m.diffInnerHeight())
}

// diffInnerHeight returns the number of code rows actually visible in the
// diff pane (topPaneHeight minus its border/title rows), matching
// renderDiff's own innerH — needed to keep the cursor viewport in sync
// without re-rendering.
func (m Model) diffInnerHeight() int {
	paneH := m.topPaneHeight(m.height)
	if m.embedded && !m.zoomed {
		H := m.height
		if H == 0 {
			H = 32
		}
		// Its title is in the border (renderTitledDiff): one row more.
		paneH = m.embeddedLayout(m.width, H).diff.h - titledHeader
	}
	h := paneH - 3 - hintRows
	if h < 1 {
		return 1
	}
	return h
}

// setDiffCursor moves the diff-pane cursor to v (clamped to the valid
// range) and adjusts the persisted viewport (diffViewTop) by the minimum
// amount needed to keep it visible: the cursor moves freely within the
// current view and only forces a scroll once it reaches the top/bottom
// edge, instead of the view re-centring on the cursor every time.
func (m *Model) setDiffCursor(v int) {
	m.diffCursor = clamp(v, 0, max(0, len(m.diffLines)-1))
	m.diffViewTop = m.diffView().fit(m.diffViewTop, m.diffCursor)
}

// relationsInnerHeight returns the number of rows actually visible in the
// relations panel, matching renderRelations's own innerH.
func (m Model) relationsInnerHeight() int {
	h := m.relationsPaneHeight(m.height) - 3 - hintRows
	if h < 1 {
		return 1
	}
	return h
}

// setRelCursor is the relations-panel counterpart of setDiffCursor: moves
// the cursor within m.relRows (clamped) and keeps the persisted viewport in
// sync using the same minimal-scroll rule.
func (m *Model) setRelCursor(v int) {
	m.relCursor = clamp(v, 0, max(0, len(m.relRows)-1))
	innerH := m.relationsInnerHeight()
	if m.relCursor < m.relViewTop {
		m.relViewTop = m.relCursor
	} else if m.relCursor >= m.relViewTop+innerH {
		m.relViewTop = m.relCursor - innerH + 1
	}
	if m.relViewTop < 0 {
		m.relViewTop = 0
	}
}

// activateRelationsRow opens the GitHub URL carried by the row under the
// relations-panel cursor, and reports whether there was one. Jumping to
// code is the diff pane's own annotations; the panel only links out.
func (m *Model) activateRelationsRow() (tea.Cmd, bool) {
	if m.relCursor < 0 || m.relCursor >= len(m.relRows) {
		return nil, false
	}
	if url := m.relRows[m.relCursor].openURL; url != "" {
		return openInBrowser(url), true
	}
	return nil, false
}

// openInBrowser opens url in the user's browser; see internal/browser.
func openInBrowser(url string) tea.Cmd {
	return func() tea.Msg {
		if err := browser.Open(url); err != nil {
			return toastMsg{browser.Command(url)[0] + ": " + err.Error()}
		}
		return toastMsg{"opened in the browser"}
	}
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
