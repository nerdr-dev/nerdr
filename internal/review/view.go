package review

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// menuRows is the row the top-level menu occupies, above everything else;
// none inside nerdr while it shows the screens in a column of its own.
func (m Model) menuRows() int {
	if m.embedded && !m.menuOn {
		return 0
	}
	return 1
}

func (m Model) View() string {
	if m.nerdrDraws() {
		if m.menuRows() == 1 {
			return m.menuBar(max(m.width, 1)) // nerdr draws the screen below
		}
		return "" // nerdr draws the screen
	}
	if m.overview {
		return m.overviewView()
	}

	W, H := m.width, m.height
	if W == 0 {
		W = 120
	}
	if H == 0 {
		H = 32
	}

	switch m.mode {
	case modePRs:
		return m.withStatus(W, append(m.menuPart(W), m.prView(W, H))...)
	}

	// One source for the geometry: what is drawn here is what the mouse
	// hit-tests against (see layout).
	lay := m.layout(W, H)

	reposPane := renderPane(m.repoItems(), m.repoIdx, lay.repos.w, lay.repos.h,
		m.hasFocus(focusRepos), "repo", hintRepos)
	wtPane := renderPane(m.wtItems(), m.wtIdx, lay.worktrees.w, lay.worktrees.h,
		m.hasFocus(focusWorktrees), "worktree", hintWorktrees)
	leftCol := lipgloss.JoinVertical(lipgloss.Left, reposPane, wtPane)

	filesTitle := fmt.Sprintf("files (%d)", len(m.files))
	if m.showBase {
		filesTitle = fmt.Sprintf("files (%d) vs %s", len(m.files), m.baseRef)
	}
	// The pane is showing the change in the order it was made, so its title
	// says what the change is rather than only how many files it touches.
	if (m.showOrder || m.folderTree) && m.orderIndex.Summary() != "" {
		filesTitle += " · " + m.orderIndex.Summary()
	}
	if m.refreshing {
		filesTitle = m.spinner.View() + " " + filesTitle
	}
	fileItems, fileIdx := m.treeItems(lay.files.w-2), m.cursor
	if len(fileItems) == 0 {
		fileItems, _ = m.emptyFiles()
		fileIdx = -1
	}
	filesPane := renderPane(fileItems, fileIdx, lay.files.w, lay.files.h,
		m.hasFocus(focusFiles), filesTitle, m.filesHint())
	switch {
	case m.hasRepoBar():
		if m.onRepoBar && m.hasFocus(focusFiles) {
			fileIdx = -1
		}
		filesPane = renderBarPane(fileItems, fileIdx, lay.files.w, lay.files.h,
			m.hasFocus(focusFiles), filesTitle, m.repoBarView(lay.files.w-2), m.filesHint())
	case m.embedded:
		filesPane = renderTitledPane(fileItems, fileIdx, lay.files.w, lay.files.h,
			m.hasFocus(focusFiles), filesTitle, m.filesHint())
	}

	diffTitle := "diff"
	if m.peek != nil {
		diffTitle = printableLine(m.peek.file) + "  [external file · u to go back]"
	} else if p, ok := m.currentFile(); ok {
		diffTitle = printableLine(p)
		if status, ok := m.currentFileStatus(); ok && status.Code == "??" {
			diffTitle += "  [new file]"
		} else {
			switch m.diffMode {
			case diffModeOld:
				diffTitle += "  [old code]"
			case diffModeNewFull:
				diffTitle += "  [whole file, changes marked]"
			}
		}
		if tabs := m.sharedWith(p); len(tabs) > 0 {
			diffTitle += "  [⚠ also changed in " + strings.Join(tabs, ", ") + "]"
		}
	}
	if m.zoomed {
		return lipgloss.JoinVertical(lipgloss.Left, append(m.menuPart(W), m.zoomedView(W, H, filesTitle, diffTitle))...)
	}

	diffPane := renderDiff(m.diffLines, m.diffCursor, m.diffViewTop, lay.diff.w, lay.diff.h,
		m.hasFocus(focusDiff), diffTitle, m.diffHint(), m.diffSel)
	if m.embedded {
		diffPane = renderTitledDiff(m.diffLines, m.diffCursor, m.diffViewTop, lay.diff.w, lay.diff.h,
			m.hasFocus(focusDiff), diffTitle, m.diffHint(), m.diffSel)
	}

	top := lipgloss.JoinHorizontal(lipgloss.Top, leftCol, filesPane, diffPane)
	switch {
	case m.embedded && m.sideBySide(W):
		top = lipgloss.JoinHorizontal(lipgloss.Top, filesPane, diffPane)
	case m.embedded:
		top = lipgloss.JoinVertical(lipgloss.Left, filesPane, diffPane)
	}

	parts := append(m.menuPart(W), top)
	if !m.embedded {
		parts = append(parts, m.renderRelations(lay.relations.w, lay.relations.h))
	}
	return m.withStatus(W, parts...)
}

// withStatus puts the status bar, or the filter being typed, under parts,
// when there is one; see statusRows.
func (m Model) withStatus(W int, parts ...string) string {
	switch {
	case m.filtering:
		parts = append(parts, m.filterInput.View())
	case m.statusRows() > 0:
		parts = append(parts, m.statusBar(W))
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// zoomedView renders just the focused pane, filling everything above the
// status bar. Zoom follows focus rather than being pinned to one pane, so
// moving between panes while zoomed keeps whichever one you are reading
// full size.
func (m Model) zoomedView(W, H int, filesTitle, diffTitle string) string {
	body := H - m.statusRows() - m.menuRows()

	var pane string
	switch m.focus {
	case focusRepos:
		pane = renderPane(m.repoItems(), m.repoIdx, W, body, true, "repo", hintRepos)
	case focusWorktrees:
		pane = renderPane(m.wtItems(), m.wtIdx, W, body, true, "worktree", hintWorktrees)
	case focusFiles:
		pane = renderPane(m.treeItems(W-2), m.cursor, W, body, true, filesTitle, m.filesHint())
	case focusRelations:
		pane = m.renderRelations(W, body)
	default:
		pane = renderDiff(m.diffLines, m.diffCursor, m.diffViewTop, W, body, true,
			diffTitle+"  [z: back]", m.diffHint(), m.diffSel)
	}

	return m.withStatus(W, pane)
}

// diffHint picks the diff pane's key row; see hintDiff.
func (m Model) diffHint() string {
	if len(m.jumpHistory) > 0 {
		return hintDiffJumped
	}
	return hintDiff
}

// menuPart is the menu's row, as the first of the parts a screen is made
// of; none inside nerdr while it has the column.
func (m Model) menuPart(W int) []string {
	if m.menuRows() == 0 {
		return nil
	}
	return []string{m.menuBar(W)}
}

// menuBar renders the top-level menu: the two things the tool does, with
// the active one highlighted. The keys are in the labels because a menu you
// cannot see how to operate is not a menu.
func (m Model) menuBar(width int) string {
	if m.embedded && width > 2 {
		// nerdr frames the menu: its sides go in the end columns.
		return " " + m.menuItems(width-2) + " "
	}
	return m.menuItems(width)
}

// menuItems is the menu's row of items, width wide.
func (m Model) menuItems(width int) string {
	var b strings.Builder
	modes := m.menuModes()
	for i, label := range m.menuShown(width) {
		style := menuItemStyle
		if modes[i] == m.mode {
			style = menuActiveStyle
		}
		b.WriteString(style.Render(label))
	}
	bar := b.String()
	if w := lipgloss.Width(bar); w < width {
		bar += menuItemStyle.Render(strings.Repeat(" ", width-w))
	}
	return fit(bar, width)
}

// menuShown is the menu's items as menuBar draws them in width columns,
// padded: whole when they fit, else only the keys but for the active one,
// so every screen can still be seen and clicked in a narrow panel.
func (m Model) menuShown(width int) []string {
	labels := m.menu()
	out := make([]string, len(labels))
	w := 0
	for i, l := range labels {
		out[i] = "  " + l + "  "
		w += lipgloss.Width(out[i])
	}
	if w <= width {
		return out
	}
	modes := m.menuModes()
	for i, l := range labels {
		if modes[i] == m.mode {
			out[i] = " " + l + " "
		} else {
			key, _, _ := strings.Cut(l, " ")
			if strings.HasSuffix(l, " ◆") {
				key += "◆" // news: the mark is the point of the label
			}
			out[i] = " " + key + " "
		}
	}
	return out
}

// globalHints are the keys that work anywhere in the current mode. The
// review layout's own keys live in its panes; the PR screen has no panes to
// move between, so almost nothing is left to say here.
func (m Model) globalHints() string {
	if m.embedded {
		menu := "m or F1-F5 menu"
		if m.screensHint != "" {
			menu = "m or " + m.screensHint + " menu"
		}
		if m.mode != modeReview {
			return menu + " · esc agent"
		}
		zoom := "z pane alone"
		if m.zoomed {
			zoom = "z all panes"
		}
		if m.wideKey != "" {
			zoom += " · " + m.wideKey + " full width"
		}
		return "↑↓ move · ←→ pane · enter in · " + zoom + " · m menu · esc agent"
	}
	switch m.mode {
	case modePRs:
		return "m or F1, F2 menu · M mouse · q quit"
	}
	return "↑↓ move · ←→ pane · enter in · m menu · M mouse · q quit"
}

func (m Model) statusBar(width int) string {
	hints := dimStyle.Render(m.globalHints())
	rw := lipgloss.Width(hints)

	// A toast is feedback on something just attempted and disappears in
	// seconds, so it gets its space first and the hints give way. Without
	// that the hint line — which has grown long — squeezes the toast out
	// entirely on a normal-width terminal, and the feedback is never seen.
	left := toastStyle.Render(m.cwd)
	if m.toast != "" {
		left = toastStyle.Render(m.toast)
	}
	if rw > width {
		return fit(fit(left, max(0, width-rw))+hints, width)
	}
	// The hints sit at the right edge, so a toast beside them reads as its
	// own thing rather than running into them.
	return fit(left, width-rw) + hints
}

// prView renders the PR screen: the worktree's pull request, and its
// conversation, one block at a time under the cursor.
func (m Model) prView(W, H int) string {
	rows, entries := m.prContent()
	items := make([]string, 0, len(rows))
	for _, r := range rows {
		items = append(items, " "+r.text) // off the border
	}
	title := "pull request"
	if m.curWT != "" {
		title = "pull request · " + printableLine(filepath.Base(m.curWT))
	}
	cursor := -1
	if m.prConvCursor < len(entries) && !m.OnRepoList() {
		cursor = entries[m.prConvCursor].row
	}
	hint := "r reload"
	if m.prOK {
		hint = "o open in browser · enter open comment · a draft a reply · A reply to all · f fix ci · r reload"
	}
	h := H - m.statusRows() - m.menuRows()
	if m.embedded {
		// nerdr has f, a and A on its row of keys above, as buttons.
		if m.prOK {
			hint = "o open in browser · enter open comment · r reload"
		}
		switch {
		case m.OnRepoList():
			hint = "↑↓ repository · enter comments · r reload"
		case m.manyRepos():
			hint = "← repositories · " + hint
		}
		// As nerdr draws all your pull requests: the title in the border,
		// and the two views on the line below it, where they stay.
		views := strings.Join(PRViews(ViewBranch, m.prNews, m.issueNews), "") + dimStyle.Render("  tab: next")
		return renderTitledList(items, cursor, m.prConvViewTop, W, h, !m.unfocused, title, views, hint)
	}
	return renderScrollList(items, cursor, m.prConvViewTop, W, h, true, title, "", hint)
}

// renderTitledList is renderScrollList with title in the top border, the
// way nerdr frames the screens it draws below the menu, and heading on the
// row renderScrollList gives its title, so the rows are where they were.
func renderTitledList(items []string, cursor, viewTop, width, height int, active bool, title, heading, hint string) string {
	lines := strings.Split(renderScrollList(items, cursor, viewTop, width, height, active, "", "", hint), "\n")
	if len(lines) < 2 || width < 9 {
		return strings.Join(lines, "\n")
	}
	lines[0] = titledTop(title, width, active)
	bs := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	if active {
		bs = lipgloss.NewStyle().Foreground(lipgloss.Color("110"))
	}
	lines[1] = bs.Render("│") + fit(heading, width-2) + "\x1b[0m" + bs.Render("│")
	return strings.Join(lines, "\n")
}

// titledTop is a pane's top border with title in it, the way nerdr frames
// the screens it draws.
func titledTop(title string, width int, active bool) string {
	bs, ts := lipgloss.NewStyle().Foreground(lipgloss.Color("240")), lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	if active {
		bs, ts = lipgloss.NewStyle().Foreground(lipgloss.Color("110")), lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Bold(true)
	}
	title = ansi.Truncate(title, width-6, "…")
	return bs.Render("╭─") + ts.Render(" "+title+" ") + bs.Render(strings.Repeat("─", max(width-lipgloss.Width(title)-5, 0))+"╮")
}

// renderTitledPane is renderPane with title in the top border and no
// heading row, so the list gets that row: the file list beside an agent,
// framed like nerdr's other panels. The mouse counts its rows with
// titledHeader.
func renderTitledPane(items []string, idx, width, height int, active bool, title, hint string) string {
	lines := strings.Split(renderPane(items, idx, width, height+1, active, title, hint), "\n")
	if len(lines) < 2 || width < 9 {
		return strings.Join(lines, "\n")
	}
	lines[0] = titledTop(title, width, active)
	return strings.Join(append(lines[:1], lines[2:]...), "\n")
}

// renderBarPane is renderTitledPane with bar, a row width-2 wide, where
// renderPane has its heading: the file list of a tab on several
// repositories, with them at its top.
func renderBarPane(items []string, idx, width, height int, active bool, title, bar, hint string) string {
	lines := strings.Split(renderPane(items, idx, width, height, active, title, hint), "\n")
	if len(lines) < 2 || width < 9 {
		return strings.Join(lines, "\n")
	}
	lines[0] = titledTop(title, width, active)
	lines[1] = ansi.Cut(lines[1], 0, 1) + fit(bar, width-2) + ansi.Cut(lines[1], width-1, width)
	return strings.Join(lines, "\n")
}

// renderTitledDiff is renderDiff with title in the top border and no
// heading row, the way renderTitledPane draws the file list above it.
func renderTitledDiff(lines []diffLine, cursor, viewTop, width, height int, active bool, title, hint string, sel lineSpan) string {
	rows := strings.Split(renderDiff(lines, cursor, viewTop, width, height+1, active, title, hint, sel), "\n")
	if len(rows) < 2 || width < 9 {
		return strings.Join(rows, "\n")
	}
	rows[0] = titledTop(title, width, active)
	return strings.Join(append(rows[:1], rows[2:]...), "\n")
}

// titledHeader is what renderTitledPane does to the rows above the list,
// as contentRow counts them: one fewer than a pane with a heading row.
const titledHeader = -1

// renderScrollList is renderPane with a caller-owned viewport, so the cursor
// can move within the visible rows before the view scrolls (see scrollTo).
func renderScrollList(items []string, cursor, viewTop, width, height int, active bool, title, header, hint string) string {
	if height < 3 {
		height = 3
	}
	innerW := width - 2
	innerH := height - 3 - hintRows - headerRows(header)
	if innerH < 1 {
		innerH = 1
	}
	if len(items) == 0 {
		items = []string{"(none)"}
		cursor = -1
	}
	viewTop = clamp(viewTop, 0, max(0, len(items)-1))
	end := min(viewTop+innerH, len(items))

	rows := make([]string, 0, innerH)
	for i := viewTop; i < end; i++ {
		line := fit(items[i], innerW)
		switch {
		case i != cursor:
		case active:
			line = selectedStyle.Render(fit(stripANSI(items[i]), innerW))
		default:
			line = selectedInactiveStyle.Render(fit(stripANSI(items[i]), innerW))
		}
		rows = append(rows, line)
	}
	for len(rows) < innerH {
		rows = append(rows, strings.Repeat(" ", innerW))
	}
	style := borderInactive
	if active {
		style = borderActive
	}
	rows = append(rows, hintRow(hint, innerW, active))
	// The column header names what the rows' columns are — in particular
	// which of the two glyphs is the review verdict and which is CI, which
	// nothing else on the row can say.
	if header != "" {
		rows = append([]string{dimStyle.Render(fit(header, innerW))}, rows...)
	}
	return scrollbar(style.Render(headingRow(title, innerW, active)+"\n"+strings.Join(rows, "\n")),
		len(items), viewTop, innerH, 2+headerRows(header), active)
}

// headerRows is the row a column header takes, if there is one.
func headerRows(header string) int {
	if header == "" {
		return 0
	}
	return 1
}

func (m Model) overviewView() string {
	items := make([]string, len(m.ovItems))
	for i, it := range m.ovItems {
		if !it.counted {
			items[i] = fmt.Sprintf("%-28s %4s", it.label, "…")
			continue
		}
		items[i] = fmt.Sprintf("%-28s %4d files  +%-5d -%-5d", it.label, it.files, it.add, it.del)
	}
	W, H := m.width, m.height
	if W == 0 {
		W = 120
	}
	if H == 0 {
		H = 32
	}
	return renderPane(items, m.ovCursor, W, H, true, "worktrees", "enter select · esc back")
}

func (m Model) repoItems() []string {
	items := make([]string, len(m.repos))
	for i, r := range m.repos {
		items[i] = r.Name
	}
	return items
}

func (m Model) wtItems() []string {
	items := make([]string, len(m.worktrees))
	for i, w := range m.worktrees {
		branch := w.Branch
		if branch == "" {
			branch = "(detached)"
		}
		items[i] = branch
	}
	return items
}

// emptyFiles is what the file pane says when the change it shows has no
// files, and which of its rows is the button to the other comparison (-1
// for none). By default the pane shows what is not committed yet, so a
// branch whose agent has committed everything looks empty; this says so,
// and offers the branch against its base.
func (m Model) emptyFiles() (lines []string, button int) {
	if m.baseRef == "" || m.curWT == "" || m.filter != "" {
		return nil, -1
	}
	btn := lipgloss.NewStyle().Background(lipgloss.Color("63")).Foreground(lipgloss.Color("231")).Bold(true)
	if m.showBase {
		return []string{
			"No changes against " + m.baseRef + ".",
			"",
			btn.Render(" b: show uncommitted changes "),
		}, 2
	}
	return []string{
		"No uncommitted changes.",
		"",
		btn.Render(" b: show the branch vs " + m.baseRef + " "),
	}, 2
}

func (m Model) treeItems(width int) []string {
	rows := m.flatRows()
	// note is what stands behind a file's name in place of its folder:
	// what the reading order says the file is for, unless d asked for the
	// folder.
	note := func(path string) string {
		if m.showFolder {
			return ""
		}
		e, _ := m.orderIndex.For(path)
		return e.Short
	}
	// Where a sentence replaces the folder, two files called the same
	// thing in different packages would read as one row twice. Those, and
	// only those, keep a folder in front of them. In the folder tree the
	// folder is above them already.
	sameName := map[string]int{}
	for _, r := range rows {
		if !r.node.isDir && !m.folderTree {
			sameName[baseName(r.node.path)]++
		}
	}
	withWhy := map[string]bool{}
	for _, note := range m.whyIndex.All() {
		withWhy[note.File] = true
	}
	items := make([]string, len(rows))
	for i, r := range rows {
		n := r.node
		indent := strings.Repeat("  ", r.depth)
		if n.isDir {
			marker := "▸ "
			if m.isExpanded(n) {
				marker = "▾ "
			}
			items[i] = indent + marker + fmt.Sprintf("%s (%d)", printableLine(n.name), n.fileCount)
			continue
		}
		check := ""
		if m.wtIdx >= 0 && m.wtIdx < len(m.worktrees) && m.unseen[m.worktrees[m.wtIdx].Path][n.path] {
			check = " " + recentStyle.Render("◆")
		} else if m.recent[n.path] > 0 {
			check = " " + recentStyle.Render("●")
		}
		// Filename first, folder after it and dimmed: the pane truncates
		// from the right, and a path long enough to be cut is exactly the
		// one whose filename must not be what disappears.
		about := note(n.path)
		name := baseName(n.path)
		if about != "" && sameName[name] > 1 {
			name = baseName(dirOf(n.path)) + "/" + name
		}
		name = printableLine(name)
		if ignoredPath(n.path) {
			name = dimStyle.Render(name)
		}
		row := indent + "  " + statusColor(n.status).Render(statusMark(n.status)) + " " + name + check +
			m.fileMarks(n.path, withWhy[n.path])
		// What the reading order says this file is for takes the place of
		// the folder; e shows it whole when the pane cuts it.
		if about != "" {
			if avail := width - lipgloss.Width(row) - 2; avail > 6 {
				row += dimStyle.Render("  " + truncate(printableLine(about), avail))
			}
			items[i] = row
			continue
		}
		if dir := dirOf(n.path); dir != "" && !m.folderTree {
			if avail := width - lipgloss.Width(row) - 2; avail > 3 {
				row += dimStyle.Render("  " + elideLeft(printableLine(dir), avail))
			}
		}
		items[i] = row
	}
	return items
}

// fileMarks is what the file list says of path after its name: the lines
// it adds and removes, ✎ when an agent left a why note in it, and ⚠ when
// another tab changes it too.
func (m Model) fileMarks(path string, why bool) string {
	var s string
	if c, ok := m.fileStats[path]; ok {
		switch {
		case c.Binary:
			s += " " + dimStyle.Render("bin")
		case c.Add > 0 || c.Del > 0:
			s += " "
			if c.Add > 0 {
				s += statAddStyle.Render(fmt.Sprintf("+%d", c.Add))
			}
			if c.Del > 0 {
				if c.Add > 0 {
					s += " "
				}
				s += statDelStyle.Render(fmt.Sprintf("−%d", c.Del))
			}
		}
	}
	if why {
		s += " " + whyStyle.UnsetItalic().Render("✎")
	}
	if len(m.sharedWith(path)) > 0 {
		s += " " + warnStyle.Render("⚠")
	}
	return s
}

// elideLeft shortens a path from the front, keeping the end — for two
// files of the same name it is the nearest parent that tells them apart,
// so that is the part worth keeping. It cuts at a separator where one is
// close enough to the budget to be worth using.
func elideLeft(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	tail := string(r[len(r)-(max-1):])
	if i := strings.IndexByte(tail, '/'); i >= 0 && len(tail)-i > max/2 {
		tail = tail[i+1:]
	}
	return "…" + tail
}

// Each pane advertises the keys that act on it; the status bar carries
// only what works anywhere.
const (
	hintRepos     = "w: all worktrees"
	hintWorktrees = "[ ] switch · b base"
	hintFiles     = "/ filter · t order/tree"
	// n/p change the selected file without leaving the diff, which is
	// where that is worth knowing — in the file pane the arrows already
	// do it. The row only fits so many keys, so "u back" takes the
	// place of "n/p file" once there is actually somewhere to go back to:
	// mid-jump, returning is the thing you want and switching file isn't.
	hintDiff       = "n/p file · v whole/old · a ask agent · s ask…"
	hintDiffJumped = "v whole/old · a ask agent · s ask… · u back"
)

// filesHints are the file list's keys, each a button on its hint row. g
// comes early, so a narrow pane does not cut it off: it opens the
// walkthrough, or asks for one when there is none — "make guide", and
// "guide…" while the agent writes it; "guide (old)" once the change has
// moved on since it was written. G, beside one, asks for a new one. e is there only while
// the file under the cursor has an explanation to show; E, which asks the
// agent beside the list to explain the change, only beside an agent with
// the nerdr-explain skill.
func (m *Model) filesHints() []string {
	hints := strings.Split(hintFiles, hintSep)
	switch {
	case m.embedded && m.explainOn && m.walkWanted != "" && m.walkWanted == m.curWT:
		hints = append(hints, "g guide…")
	case m.walk && m.walkOld:
		hints = append(hints, "g guide (old)")
	case m.walk:
		hints = append(hints, "g guide")
	case m.embedded && m.explainOn:
		hints = append(hints, "g make guide")
	}
	if m.walk && m.embedded && m.explainOn {
		hints = append(hints, "G new guide")
	}
	if m.embedded {
		where := "beside"
		if m.sideBySide(m.width) {
			where = "above"
		}
		hints = append(hints, "L files "+where)
	}
	if _, ok := m.explanation(); ok {
		hints = append(hints, "e explanation")
	}
	if m.embedded && m.explainOn {
		hints = append(hints, "E agent, explain")
	}
	return hints
}

const hintSep = " · "

func (m *Model) filesHint() string { return strings.Join(m.filesHints(), hintSep) }

// hintRows is the row each pane reserves at its foot for the keys that
// belong to it. Reserved whether or not the pane has focus, so the content
// height — which the cursor and viewport logic is derived from — doesn't
// change as focus moves.
const hintRows = 1

// hintRow renders a pane's own key hints, or a blank line when the pane
// isn't focused: the keys you can press are the ones for where you are.
func hintRow(hint string, width int, active bool) string {
	if !active || hint == "" {
		return strings.Repeat(" ", max(0, width))
	}
	return fit(dimStyle.Render(hint), width)
}

// headingRow draws a pane title as a bar spanning the pane's width. The
// title must be unstyled: the bar's background is applied around the whole
// padded string, and an inner reset would cut it short.
func headingRow(title string, width int, active bool) string {
	st := headingStyle
	if active {
		st = headingActiveStyle
	}
	return st.Render(fit(title, width))
}

// renderPane renders a bordered, selectable list pane with a title.
func renderPane(items []string, idx, width, height int, active bool, title, hint string) string {
	if height < 3 {
		height = 3
	}
	innerW := width - 2
	innerH := height - 3 - hintRows // title row + top/bottom border + hints
	if innerH < 1 {
		innerH = 1
	}

	if len(items) == 0 {
		items = []string{"(none)"}
		idx = -1
	}

	start := listStart(idx, innerH, len(items))
	end := min(start+innerH, len(items))

	rows := make([]string, 0, innerH)
	for i := start; i < end; i++ {
		line := fit(items[i], innerW)
		// The selection is drawn whether or not the pane has focus: once
		// you step into the diff, the file pane is the only thing saying
		// which file you are reading, and n/p move that selection from
		// inside the diff pane.
		//
		// A row carries its own colours (the m/n/d mark, the dimmed
		// folder), and each of those ends in a reset that would cut the
		// selection background short — a bar five cells wide instead of a
		// selected row. So the selected row is flattened first and wears
		// the selection colour alone.
		switch {
		case i != idx:
		case active:
			line = selectedStyle.Render(fit(stripANSI(items[i]), innerW))
		default:
			line = selectedInactiveStyle.Render(fit(stripANSI(items[i]), innerW))
		}
		rows = append(rows, line)
	}
	for len(rows) < innerH {
		rows = append(rows, strings.Repeat(" ", innerW))
	}

	style := borderInactive
	if active {
		style = borderActive
	}
	rows = append(rows, hintRow(hint, innerW, active))
	return scrollbar(style.Render(headingRow(title, innerW, active)+"\n"+strings.Join(rows, "\n")),
		len(items), start, innerH, 2, active)
}

// renderDiff renders the diff pane with line-based coloring and scrolling.
// lines is the already-built (annotated, syntax-highlighted) diff, computed
// once by the model when the file/mode is loaded rather than on every
// render frame — chroma tokenisation is too expensive to redo per keystroke.
// cursor is the selected row; viewTop is the persisted first visible row
// (see Model.setDiffCursor) — the cursor moves freely within that view and
// only forces it to scroll once the cursor reaches an edge, rather than
// the view jumping to re-centre on the cursor on every move.
func renderDiff(lines []diffLine, cursor, viewTop, width, height int, active bool, title, hint string, sel lineSpan) string {
	if height < 3 {
		height = 3
	}
	innerW := width - 2
	innerH := height - 3 - hintRows
	if innerH < 1 {
		innerH = 1
	}

	style := borderInactive
	if active {
		style = borderActive
	}
	if len(lines) == 0 {
		content := plainText("", innerW, innerH)
		return style.Render(headingRow(title, innerW, active) + "\n" + content +
			"\n" + hintRow(hint, innerW, active))
	}

	cursor = clamp(cursor, 0, max(0, len(lines)-1))

	// start normally just is viewTop (kept in sync by setDiffCursor on
	// every cursor move); this is a defensive fallback only, e.g. right
	// after switching files before a sync has run.
	v := newDiffView(lines, width, innerH)
	v.sel = sel
	start := clamp(viewTop, 0, v.maxTop())
	if cursor < v.lineAt(0, start) || cursor > v.last(start) {
		start = v.fit(start, cursor)
	}

	// The gutter: a "you are here" marker on the cursor row plus the
	// line's position in the full new file (not just its offset within
	// the diff/hunk, so numbers stay meaningful even when only the changed
	// lines are shown); see diffView.render.
	rows := v.render(start, cursor, active)
	for len(rows) < innerH {
		rows = append(rows, strings.Repeat(" ", innerW))
	}
	rows = append(rows, hintRow(hint, innerW, active))
	return scrollbar(style.Render(headingRow(title, innerW, active)+"\n"+strings.Join(rows, "\n")),
		v.rowOf(len(lines)), start, innerH, 2, active)
}

func plainText(s string, w, h int) string {
	rows := make([]string, 0, h)
	for i := 0; i < h; i++ {
		row := ""
		if i == 0 && s != "" {
			row = s
		}
		rows = append(rows, fit(row, w))
	}
	return strings.Join(rows, "\n")
}

// fit truncates s to at most w visual cells and pads it to exactly w cells,
// preserving ANSI styling without ever wrapping. A width <= 0 yields "".
// ansiSeq matches the escape sequences lipgloss emits, so a styled string
// can be flattened back to plain text before being restyled.
var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiSeq.ReplaceAllString(s, "") }

func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	trunc := lipgloss.NewStyle().MaxWidth(w).Render(s)
	return trunc + strings.Repeat(" ", max(0, w-lipgloss.Width(trunc)))
}

// renderRelations renders the relations panel: tab line plus m.relRows
// (already built for the active tab — see buildRelRows), with a cursor
// marker on the selected row when the panel has focus, matching the diff
// pane's viewport behaviour (cursor moves freely within view, scrolls only
// at the edge).
func (m Model) renderRelations(width, height int) string {
	if height < 3 {
		height = 3
	}
	innerW := width - 2
	innerH := height - 3 - hintRows // tab line + top/bottom border + hints
	if innerH < 1 {
		innerH = 1
	}
	active := m.hasFocus(focusRelations)

	// Built segment by segment rather than through headingRow: the tab
	// labels are styled individually, so the bar's background has to be
	// carried by each of them instead of wrapped around the whole row.
	var b strings.Builder
	for i, t := range relationsTabLabels {
		if i == m.relationsTab {
			b.WriteString(selectedStyle.Render(" " + t + " "))
		} else {
			b.WriteString(headingStyle.Render(" " + t + " "))
		}
	}
	if pad := innerW - lipgloss.Width(b.String()); pad > 0 {
		b.WriteString(headingStyle.Render(strings.Repeat(" ", pad)))
	}
	tabLine := fit(b.String(), innerW)

	lines := m.relRows
	viewTop := clamp(m.relViewTop, 0, max(0, len(lines)-1))
	if m.relCursor < viewTop {
		viewTop = m.relCursor
	} else if m.relCursor >= viewTop+innerH {
		viewTop = max(0, m.relCursor-innerH+1)
	}
	end := min(viewTop+innerH, len(lines))

	rows := make([]string, 0, innerH)
	for i, rl := range lines[viewTop:end] {
		gutter := "  "
		text := rl.text
		if rl.actionable() {
			text = refStyle.Render(rl.text)
		}
		if active && viewTop+i == m.relCursor {
			gutter = cursorMarkStyle.Render("▶ ")
		}
		rows = append(rows, gutter+fit(text, innerW-2))
	}
	for len(rows) < innerH {
		rows = append(rows, strings.Repeat(" ", innerW))
	}

	rows = append(rows, hintRow("tab/1-3 switch tab · enter opens the link on GitHub", innerW, active))

	style := borderInactive
	if active {
		style = borderActive
	}
	return scrollbar(style.Render(tabLine+"\n"+strings.Join(rows, "\n")), len(lines), viewTop, innerH, 2, active)
}
