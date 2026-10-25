package review

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"nerdr/internal/git"
	"nerdr/internal/github"
)

// uiMode is the top-level menu selection.
type uiMode int

const (
	// modeReview is the default: the repo/worktree/files/diff review layout.
	modeReview uiMode = iota
	// modePRs is the pull request of the worktree being looked at: its
	// state, checks and conversation.
	modePRs
	// modeMessages is the tab's messages for its agent, only inside nerdr,
	// which draws them in the panel's place.
	modeMessages
	// modeSkills is the skills nerdr gives the agents, only inside nerdr,
	// which draws them as it does the messages.
	modeSkills
	// modePlugins is the user.s plugins, only inside nerdr,
	// which draws them, and a plugin's own screen.
	modePlugins
)

// screens are the menu's screens in order, each with the function key it
// is on. The keys are the same in nerdr and on the tool's own, which lacks
// the screens only nerdr has: there F3 to F5 do nothing. short is the
// name nerdr's column of F-keys gives it.
var screens = [...]struct {
	key   string
	mode  uiMode
	label string
	short string
	nerdr bool // only inside nerdr
}{
	{"f1", modeReview, "code changes", "changes", false},
	{"f2", modePRs, "github", "github", false},
	{"f3", modeMessages, "tasks", "tasks", true},
	{"f4", modeSkills, "skills", "skills", true},
	{"f5", modePlugins, "plugins", "plugins", true},
}

// menuModes are the screens the menu has, in its order.
func (m Model) menuModes() []uiMode {
	var out []uiMode
	for _, s := range screens {
		if m.embedded || !s.nerdr {
			out = append(out, s.mode)
		}
	}
	return out
}

// keyMode is the screen function key k goes to, if the menu has it.
func (m Model) keyMode(k string) (uiMode, bool) {
	for _, s := range screens {
		if s.key == k && (m.embedded || !s.nerdr) {
			return s.mode, true
		}
	}
	return 0, false
}

// screenKey is what the menu shows before the i-th screen's name: its
// F-key, or the keys nerdr gave in its place (SetScreenKeys).
func (m Model) screenKey(i int) string {
	if i < len(m.screenKeys) {
		return m.screenKeys[i]
	}
	return strings.ToUpper(screens[i].key)
}

// menu is the top-level menu's labels, in menuModes' order.
func (m Model) menu() []string {
	var labels []string
	for i, s := range screens {
		if !m.embedded && s.nerdr {
			continue
		}
		l := m.screenKey(i) + " " + s.label
		if s.mode == modePRs && m.embedded && (m.prNews || m.issueNews) {
			l += " ◆"
		}
		labels = append(labels, l)
	}
	return labels
}

// Messages reports whether the panel is showing the tab's messages (F3),
// which nerdr draws in the panel's place: View draws nothing then.
func (m Model) Messages() bool { return m.mode == modeMessages }

// Skills reports whether the panel is showing nerdr's skills (F4), which
// nerdr draws as it does the messages.
func (m Model) Skills() bool { return m.mode == modeSkills }

// Plugins reports whether the panel is showing the plugins (F5), which
// nerdr draws as it does the messages.
func (m Model) Plugins() bool { return m.mode == modePlugins }

// PRView is which of F2's views the panel shows inside nerdr; on its own
// the screen has only the first.
type PRView int

const (
	// ViewBranch is the worktree's pull request, which the panel draws.
	ViewBranch PRView = iota
	// ViewMyPRs is all your pull requests, which nerdr draws.
	ViewMyPRs
	// ViewIssues is the issues assigned to you, or all open ones, which
	// nerdr draws.
	ViewIssues
	prViewCount
)

// AllPRs reports whether the panel is showing all your pull requests,
// the second of F2's views, which nerdr draws below the menu as it does
// the messages.
func (m Model) AllPRs() bool { return m.embedded && m.mode == modePRs && m.prShown == ViewMyPRs }

// Issues reports whether the panel is showing the issues, the third of
// F2's views, which nerdr draws below the menu as it does the messages.
func (m Model) Issues() bool { return m.embedded && m.mode == modePRs && m.prShown == ViewIssues }

// BranchPR reports whether the panel is showing the worktree's pull
// request inside nerdr, the first of F2's views.
func (m Model) BranchPR() bool { return m.embedded && m.mode == modePRs && m.prShown == ViewBranch }

// SetPRView switches F2 to view v.
func (m *Model) SetPRView(v PRView) tea.Cmd {
	if !m.embedded || m.prShown == v {
		return nil
	}
	m.prShown = v
	if v == ViewBranch && m.mode == modePRs && time.Since(m.lastPRLoad) >= m.prRefresh() {
		return m.reloadPR()
	}
	return nil
}

// NextPRView switches F2 to the view after the one shown, by step (1 or
// -1), round to the first after the last.
func (m *Model) NextPRView(step int) tea.Cmd {
	return m.SetPRView((m.prShown + PRView(step) + prViewCount) % prViewCount)
}

// SetBranch gives the rows nerdr shows above the worktree's pull request:
// the tab's branches, and the keys for what to do about them.
func (m Model) SetBranch(rows []string) Model {
	m.branch = rows
	return m
}

// ReloadPR fetches the worktree's PR again at once: after nerdr pushed
// or opened it, rather than at the slow refresh.
func (m *Model) ReloadPR() tea.Cmd { return m.reloadPR() }

// HasPR reports whether F2 has the worktree's pull request to show.
func (m Model) HasPR() bool { return m.prOK }

// CanFix is whether f has a failing check to fix on the PR.
func (m Model) CanFix() bool {
	_, fail, _ := countChecks(m.pr.Checks)
	return m.prOK && fail > 0
}

// CanReply is whether a has a comment to answer under the cursor.
func (m Model) CanReply() bool {
	e, ok := m.selectedEntry()
	return m.prOK && ok && e.answerable()
}

// CanReplyAll is whether A has comments to answer on the PR.
func (m Model) CanReplyAll() bool {
	return m.prOK && (len(m.pr.Comments) > 0 || hasReviewBody(m.pr))
}

// Worktree is the worktree the panel shows, whose PR F2 draws.
func (m Model) Worktree() string { return m.curWT }

// ShowPRView goes to F2's view v.
func (m *Model) ShowPRView(v PRView) tea.Cmd {
	if !m.embedded {
		return nil
	}
	if m.mode == modePRs {
		return m.SetPRView(v)
	}
	m.prShown = v
	return m.setMode(modePRs)
}

// Screen is which of the menu's screens the panel shows, and on F2 which
// of its views. nerdr keeps one for each tab, so coming back to a tab
// finds the panel where it was left there; the zero Screen is F1.
type Screen struct {
	mode uiMode
	view PRView
}

// ScreenNames are the menu's screens' names, F1 to F5.
func ScreenNames() []string {
	var out []string
	for _, s := range screens {
		out = append(out, s.label)
	}
	return out
}

// ScreenShort are the menu's screens' short names, F1 to F5, for nerdr's
// column of F-keys.
func ScreenShort() []string {
	var out []string
	for _, s := range screens {
		out = append(out, s.short)
	}
	return out
}

// ScreenIndex is which of the screens the panel shows, 0 for F1.
func (m Model) ScreenIndex() int {
	for i, s := range screens {
		if s.mode == m.mode {
			return i
		}
	}
	return 0
}

// News reports whether F2 has news not seen yet, which its menu item
// marks ◆.
func (m Model) News() bool { return m.prNews || m.issueNews }

// Screen is the screen the panel shows now.
func (m Model) Screen() Screen { return Screen{mode: m.mode, view: m.prShown} }

// SetScreen goes to s, without the switching F2 does when pressed again.
func (m *Model) SetScreen(s Screen) tea.Cmd {
	if m.mode == modeReview && s.mode != modeReview {
		m.leaveJumps()
	}
	m.mode, m.prShown = s.mode, s.view
	if !m.embedded {
		m.prShown = ViewBranch
	}
	switch {
	case m.mode == modePRs && m.prShown == ViewBranch && time.Since(m.lastPRLoad) >= m.prRefresh():
		return m.reloadPR()
	}
	return m.refreshIfStale()
}

// ShowBranchPR goes to F2's view of the worktree's own pull request.
func (m *Model) ShowBranchPR() tea.Cmd { return m.SetScreen(Screen{mode: modePRs}) }

// SetPRNews marks F2 while there is news of your pull requests not seen
// yet; going to F2 then shows all of them, where the news is.
func (m *Model) SetPRNews(news bool) { m.prNews = news }

// SetIssueNews marks F2 while there are issues newly assigned to you not
// seen yet; going to F2 then shows the issues, without news of PRs.
func (m *Model) SetIssueNews(news bool) { m.issueNews = news }

// The PR screen's views: the one shown wears the menu's blue, the others
// sit on its darker bar.
var (
	prViewActiveStyle = menuActiveStyle
	prViewItemStyle   = menuItemStyle
)

// PRViews is the line of F2's views, in PRView's order, the one shown
// highlighted and those with news marked ◆, as nerdr draws it above all
// your pull requests and the issues too.
func PRViews(shown PRView, prNews, issueNews bool) []string {
	labels := []string{" this branch ", " my PRs ", " issues "}
	if prNews {
		labels[ViewMyPRs] = " my PRs ◆ "
	}
	if issueNews {
		labels[ViewIssues] = " issues ◆ "
	}
	for i, l := range labels {
		if PRView(i) == shown {
			labels[i] = prViewActiveStyle.Render(l)
		} else {
			labels[i] = prViewItemStyle.Render(l)
		}
	}
	return labels
}

// prViewAt is which of the views is drawn x columns into the line PRViews
// makes, and whether x is on one.
func (m Model) prViewAt(x int) (PRView, bool) {
	at := 0
	for i, l := range PRViews(m.prShown, m.prNews, m.issueNews) {
		w := lipgloss.Width(l)
		if x >= at && x < at+w {
			return PRView(i), true
		}
		at += w
	}
	return 0, false
}

// nerdrDraws is whether nerdr draws what is below the menu.
func (m Model) nerdrDraws() bool {
	return m.mode == modeMessages || m.mode == modeSkills || m.mode == modePlugins || m.AllPRs() || m.Issues()
}

// prReloadMsg carries the worktree's PR fetched again: r, or the slow
// refresh while the PR screen is open.
type prReloadMsg struct {
	wt    string
	token int
	pr    github.PR
	ok    bool
	msg   string
}

// prFixMsg carries the failing jobs' log, read for the message asking the
// agent to fix the build.
type prFixMsg struct {
	wt   string
	pr   github.PR
	logs string
	err  error
}

// setMode switches the top-level menu. Entering the PR screen fetches the
// worktree's PR again if what is shown has gone stale.
//
// Inside nerdr F2 again goes on to its next view; going to it with news
// not seen yet shows where the news is: all your PRs, or the issues.
func (m *Model) setMode(mode uiMode) tea.Cmd {
	if mode == modePRs && m.embedded {
		if m.mode == modePRs {
			return m.NextPRView(1)
		}
		switch {
		case m.prNews:
			m.prShown = ViewMyPRs
		case m.issueNews:
			m.prShown = ViewIssues
		}
	}
	if mode == m.mode {
		return nil
	}
	if m.mode == modeReview {
		m.leaveJumps()
	}
	m.mode = mode
	if mode == modePRs && m.prShown == ViewBranch && time.Since(m.lastPRLoad) >= m.prRefresh() {
		return m.reloadPR()
	}
	return m.refreshIfStale()
}

// reloadPR fetches the worktree's PR again, leaving what is on screen
// until the answer arrives.
func (m *Model) reloadPR() tea.Cmd {
	wt := m.curWT
	if wt == "" {
		return nil
	}
	m.prToken++
	m.lastPRLoad = time.Now()
	token := m.prToken
	return func() tea.Msg {
		pr, ok, msg := github.CurrentPR(wt)
		return prReloadMsg{wt: wt, token: token, pr: pr, ok: ok, msg: msg}
	}
}

// prContent is what the PR screen shows: the worktree's PR — its state,
// review and checks — and below that its conversation, whose blocks the
// cursor steps between.
//
// Inside nerdr the line that switches to all your pull requests is above
// it, in the pane's heading (see prView), and a blank line sets it apart.
func (m Model) prContent() ([]relLine, []convEntry) {
	rows, entries := m.prOwnContent()
	if !m.embedded {
		return rows, entries
	}
	top := []relLine{{text: ""}}
	if len(m.branch) > 0 {
		width := m.width
		if width == 0 {
			width = 120
		}
		top = append(top, section("branch", width-6))
	}
	for _, r := range m.branch {
		top = append(top, relLine{text: r})
	}
	if len(m.branch) > 0 {
		top = append(top, relLine{text: ""})
	}
	for i := range entries {
		entries[i].row += len(top)
	}
	return append(top, rows...), entries
}

// prOwnContent is the worktree's PR, as prContent shows it.
func (m Model) prOwnContent() ([]relLine, []convEntry) {
	switch {
	case m.curWT == "":
		return []relLine{{text: "(no worktree)"}}, nil
	case !github.Available():
		return []relLine{{text: "(the gh CLI is not installed)"}}, nil
	case !m.prOK:
		msg := m.prMsg
		if msg == "" {
			msg = "no pull request for this branch"
		}
		if m.embedded {
			// nerdr's keys above, with the branches, push and open one.
			return []relLine{{text: "(" + printableLine(msg) + ")"}}, nil
		}
		return []relLine{{text: "(" + printableLine(msg) + ")"}, {text: ""},
			{text: dimStyle.Render("push and open one from nerdr's tab panel (prefix, then i)")}}, nil
	}
	width := m.width
	if width == 0 {
		width = 120
	}
	rows := append([]relLine{section("pull request", width-6)}, prHeader(m.pr)...)
	if m.pr.URL != "" {
		rows = append(rows, relLine{text: ""},
			relLine{text: prButtonStyle.Render(" o  open in browser "), openURL: m.pr.URL, button: true})
	}
	rows = append(rows, relLine{text: ""})
	conv, entries := prConversation(m.pr, width-6, true)
	for i := range entries {
		entries[i].row += len(rows)
	}
	return append(rows, conv...), entries
}

var prButtonStyle = lipgloss.NewStyle().Background(lipgloss.Color("63")).Foreground(lipgloss.Color("231")).Bold(true)

// section is the line a part of the PR screen starts with: its name on a
// rule, as wide as the text below it, within reason.
func section(name string, width int) relLine {
	n := max(min(width, 100)-len(name)-4, 3)
	return relLine{text: dimStyle.Render("── ") + headingText.Render(name) + dimStyle.Render(" "+strings.Repeat("─", n))}
}

var headingText = lipgloss.NewStyle().Bold(true)

// prStateStyle colours a PR's state as GitHub does, near enough.
func prStateStyle(state string) lipgloss.Style {
	switch state {
	case "OPEN":
		return okStyle
	case "MERGED":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("141"))
	case "CLOSED":
		return failStyle
	}
	return dimStyle
}

// prHeader is the PR in a few lines: title, state, review verdict, checks,
// the last two labelled in a column of their own.
func prHeader(pr github.PR) []relLine {
	state := printableLine(pr.State)
	if pr.IsDraft {
		state = "DRAFT"
	}
	out := []relLine{
		{text: headingText.Render(fmt.Sprintf("#%d %s", pr.Number, printableLine(pr.Title))) + "  " + prStateStyle(state).Render(state), openURL: pr.URL},
		{text: ""},
	}
	label := func(s string) string { return dimStyle.Render(fmt.Sprintf("%-8s", s)) }
	decision := strings.ReplaceAll(strings.ToLower(pr.ReviewDecision), "_", " ")
	if decision == "" {
		decision = "no review yet"
	}
	out = append(out, relLine{text: label("review") + reviewGlyph(pr) + " " + printableLine(decision)})
	for _, r := range pr.Reviews {
		out = append(out, relLine{
			text:    label("") + "  " + printableLine(r.Author) + dimStyle.Render(": "+printableLine(strings.ReplaceAll(strings.ToLower(r.State), "_", " "))),
			openURL: r.URL,
		})
	}
	if len(pr.Checks) > 0 {
		pass, fail, pending := countChecks(pr.Checks)
		out = append(out, relLine{
			text: label("checks") + fmt.Sprintf("%s %d passed · %d failed · %d pending", checksGlyph(pr), pass, fail, pending),
		})
	}
	if pr.Mergeable == "CONFLICTING" {
		out = append(out, relLine{text: failStyle.Render("conflicts with the base branch")})
	}
	return out
}

// handlePRKey handles keys while the PR screen is on, and reports whether
// the key was consumed. The cursor steps between the conversation's
// blocks: a comment is read as a whole.
func (m *Model) handlePRKey(key string) (tea.Cmd, bool) {
	if cmd, ok := m.repoListKey(key); ok {
		return cmd, true
	}
	switch key {
	case "up", "k":
		m.movePRConv(-1)
		return nil, true
	case "down", "j":
		m.movePRConv(1)
		return nil, true
	case "pgup":
		m.scrollPRPane(-m.prConvInnerHeight())
		return nil, true
	case "pgdown":
		m.scrollPRPane(m.prConvInnerHeight())
		return nil, true
	case "enter":
		// On a row that points somewhere — a comment, a review, a failing
		// check's run — enter opens it on GitHub.
		if e, ok := m.selectedEntry(); ok && e.url != "" {
			return openInBrowser(e.url), true
		}
		return nil, true
	case "o":
		if m.prOK && m.pr.URL != "" {
			return openInBrowser(m.pr.URL), true
		}
		return nil, true
	case "r":
		return m.reloadPR(), true
	case "tab":
		return m.NextPRView(1), true
	case "shift+tab":
		return m.NextPRView(-1), true
	case "a":
		return m.draftReply(), true
	case "A":
		return m.draftReplies(), true
	case "f":
		return m.startPRFix(), true
	case "[", "]":
		// Inside nerdr, a tab on several repositories has a PR for each.
		if !m.embedded {
			return nil, false
		}
		if key == "[" {
			return m.cyclePin(-1), true
		}
		return m.cyclePin(1), true
	case "left", "right":
		return nil, true
	}
	return nil, false
}

// manyRepos reports whether F2 has the tab's repositories listed above
// the PR, to pick one from.
func (m Model) manyRepos() bool { return m.embedded && len(m.pins) > 1 }

// OnRepoList reports whether the cursor is on the list of repositories
// above the PR in F2, which nerdr draws.
func (m Model) OnRepoList() bool { return m.prOnRepos && m.manyRepos() }

// repoListKey handles the keys that go between the list of repositories
// above the PR and its conversation, as ↑ from the first file goes to the
// bar in F1: on the list ↑ ↓ pick a repository, and ↓ from the last,
// enter or → go down to the conversation; ↑ from its first block, or ←,
// come back up.
func (m *Model) repoListKey(key string) (tea.Cmd, bool) {
	if !m.manyRepos() {
		return nil, false
	}
	if m.prOnRepos {
		switch key {
		case "up", "k":
			return m.ShowPin(max(m.repoIdx-1, 0)), true
		case "down", "j":
			if m.repoIdx < len(m.pins)-1 {
				return m.ShowPin(m.repoIdx + 1), true
			}
			m.prOnRepos = false
			return nil, true
		case "enter", "right", "l":
			m.prOnRepos = false
			return nil, true
		case "left", "h":
			return nil, true
		}
		return nil, false
	}
	switch key {
	case "up", "k":
		if m.prConvCursor > 0 {
			return nil, false
		}
	case "left", "h":
	default:
		return nil, false
	}
	m.prOnRepos = true
	m.prConvViewTop = 0
	return nil, true
}

// selectedEntry is the conversation block under the cursor.
func (m Model) selectedEntry() (convEntry, bool) {
	_, entries := m.prContent()
	if m.prConvCursor < 0 || m.prConvCursor >= len(entries) {
		return convEntry{}, false
	}
	return entries[m.prConvCursor], true
}

// movePRConv steps between conversation blocks rather than between lines,
// bringing the selected one to the top of the pane, which is where you
// start reading it.
func (m *Model) movePRConv(delta int) {
	rows, entries := m.prContent()
	last := max(0, len(rows)-m.prConvInnerHeight())
	if len(entries) == 0 {
		m.prConvViewTop = clamp(m.prConvViewTop+delta, 0, last)
		return
	}
	m.prConvCursor = clamp(m.prConvCursor+delta, 0, len(entries)-1)
	top := entries[m.prConvCursor].row
	if m.prConvCursor == 0 {
		top = 0 // the header is worth keeping in view from the first block
	}
	m.prConvViewTop = clamp(top, 0, last)
}

// scrollPRPane scrolls the PR screen, stopping at the last screenful.
func (m *Model) scrollPRPane(delta int) {
	rows, _ := m.prContent()
	last := max(0, len(rows)-m.prConvInnerHeight())
	m.prConvViewTop = clamp(m.prConvViewTop+delta, 0, last)
}

func (m Model) prConvInnerHeight() int {
	h := m.height
	if h == 0 {
		h = 32
	}
	return max(1, h-1-m.menuRows()-3-hintRows)
}

// draftReply is a: a message asking the agent to draft an answer to the
// comment under the cursor.
func (m *Model) draftReply() tea.Cmd {
	entry, ok := m.selectedEntry()
	if !m.prOK || !ok || !entry.answerable() {
		m.setToast("select a comment to answer")
		return nil
	}
	return m.askAgent(replyOnePrompt(m.pr, entry))
}

// draftReplies is A: a message asking for drafts for every comment.
func (m *Model) draftReplies() tea.Cmd {
	if !m.prOK || (len(m.pr.Comments) == 0 && !hasReviewBody(m.pr)) {
		m.setToast("no comments to answer")
		return nil
	}
	return m.askAgent(replyPrompt(m.pr))
}

// startPRFix is f: read the failing jobs' log, then a message asking the
// agent to fix the build.
func (m *Model) startPRFix() tea.Cmd {
	if !m.prOK {
		m.setToast("no pull request for this branch")
		return nil
	}
	if _, fail, _ := countChecks(m.pr.Checks); fail == 0 {
		m.setToast("no failing checks on this PR")
		return nil
	}
	if m.prBusy {
		return nil
	}
	m.prBusy = true
	m.setToast("reading the build log…")
	wt, pr := m.curWT, m.pr
	return func() tea.Msg {
		logs, err := github.FailedRunLogs(wt, pr.Checks, logLimit)
		return prFixMsg{wt: wt, pr: pr, logs: logs, err: err}
	}
}

// FixMessage is the message f writes for pr: the failing jobs' log, read
// in dir, and the ask to fix the build. Without a failing check it is an
// error saying so.
func FixMessage(dir string, pr github.PR) (string, error) {
	if _, fail, _ := countChecks(pr.Checks); fail == 0 {
		return "", fmt.Errorf("no failing checks on #%d", pr.Number)
	}
	logs, err := github.FailedRunLogs(dir, pr.Checks, logLimit)
	if err != nil {
		return "", err
	}
	return fixPrompt(pr, logs), nil
}

// ReplyMessage is the message A writes for pr: drafts for every review and
// comment; "" when there is nothing to answer.
func ReplyMessage(pr github.PR) string {
	if len(pr.Comments) == 0 && !hasReviewBody(pr) {
		return ""
	}
	return replyPrompt(pr)
}

// logLimit is how much failing-build log goes into the message. The agent
// can read the repository itself, so this only has to carry the failure.
const logLimit = 6000

// said is the reviews that came with words: all of them when the PR was
// fetched by itself, else each reviewer's latest.
func said(pr github.PR) []github.Review {
	if len(pr.Said) > 0 {
		return pr.Said
	}
	return pr.Reviews
}

// where is the line a comment is on, "" for one in the conversation.
func where(c github.Comment) string {
	switch {
	case c.Path == "":
		return ""
	case c.Line > 0:
		return c.Path + ":" + strconv.Itoa(c.Line)
	}
	return c.Path
}

func hasReviewBody(pr github.PR) bool {
	for _, r := range said(pr) {
		if strings.TrimSpace(r.Body) != "" {
			return true
		}
	}
	return false
}

// otherEntries is the rest of the conversation, so a single reply knows
// what has already been said without being asked to answer it.
func otherEntries(pr github.PR, skip convEntry) string {
	var b strings.Builder
	for _, r := range said(pr) {
		if strings.TrimSpace(r.Body) == "" || (r.Author == skip.author && r.Body == skip.body) {
			continue
		}
		fmt.Fprintf(&b, "- review by %s: %s\n", r.Author, strings.TrimSpace(r.Body))
	}
	for _, c := range pr.Comments {
		if c.Author == skip.author && c.Body == skip.body {
			continue
		}
		on := ""
		if w := where(c); w != "" {
			on = " on " + w
		}
		fmt.Fprintf(&b, "- comment by %s%s: %s\n", c.Author, on, strings.TrimSpace(c.Body))
	}
	return b.String()
}

// The messages below quote what other people wrote. They are shown to the
// user before they reach the agent, and say plainly that nothing is to be
// posted: the quoted text is not instructions.

func replyOnePrompt(pr github.PR, entry convEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Help me answer one piece of review feedback on pull request #%d (%s).\n\n", pr.Number, pr.Title)
	fmt.Fprintf(&b, "The %s to answer, by %s:\n%s\n\n", entry.kind, entry.author, strings.TrimSpace(entry.body))
	if rest := otherEntries(pr, entry); rest != "" {
		b.WriteString("The rest of the conversation, for context — do not answer these:\n" + rest + "\n")
	}
	b.WriteString("Draft a reply to that one. Check what it claims against the code before " +
		"agreeing with it: say so plainly if it is mistaken, and say what you would change " +
		"if it is right. A few sentences, plain text. It is a draft for me to review and " +
		"send — do not post anything, and do not change any files yet.")
	return b.String()
}

func replyPrompt(pr github.PR) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Help me answer the review feedback on pull request #%d (%s).\n\n", pr.Number, pr.Title)
	n := 0
	for _, r := range said(pr) {
		if strings.TrimSpace(r.Body) == "" {
			continue
		}
		n++
		fmt.Fprintf(&b, "[%d] review by %s (%s):\n%s\n\n", n, r.Author,
			strings.ToLower(r.State), strings.TrimSpace(r.Body))
	}
	for _, c := range pr.Comments {
		n++
		on := ""
		if w := where(c); w != "" {
			on = " on " + w
		}
		fmt.Fprintf(&b, "[%d] comment by %s%s:\n%s\n\n", n, c.Author, on, strings.TrimSpace(c.Body))
	}
	b.WriteString("Draft a reply to each, numbered to match. Check what each one claims " +
		"against the code before agreeing with it: say so plainly when a comment is " +
		"mistaken, and say what you would change when it is right. A few sentences each, " +
		"plain text. These are drafts for me to review and send — do not post anything, " +
		"and do not change any files yet.")
	return b.String()
}

func fixPrompt(pr github.PR, logs string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "A CI check on pull request #%d (%s) failed. Find out why, and fix it.\n\n", pr.Number, pr.Title)
	b.WriteString("Failing checks:\n")
	for _, c := range pr.Checks {
		switch strings.ToUpper(c.Status) {
		case "SUCCESS", "NEUTRAL", "SKIPPED":
			continue
		}
		fmt.Fprintf(&b, "  - %s (%s)\n", c.Name, strings.ToLower(c.Status))
	}
	b.WriteString("\nFrom the job log — the lines leading up to each error the runner " +
		"reported, with the rest cut:\n\n")
	b.WriteString(logs)
	b.WriteString("\n\nWork out the cause from the log and the code, make the smallest change " +
		"that fixes it, and run the project's own tests or linter for the part you touched. " +
		"Do not commit, push, or comment on the pull request. Then tell me what was wrong, " +
		"what you changed, and what you ran to check it.")
	return b.String()
}

// scrollTo returns the viewport top that keeps cursor visible, moving it as
// little as possible — the same rule the diff pane uses, so a cursor moving
// back up doesn't drag the view with it until it has to.
func scrollTo(cursor, viewTop, innerH int) int {
	if cursor < viewTop {
		return cursor
	}
	if cursor >= viewTop+innerH {
		return cursor - innerH + 1
	}
	return viewTop
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// reviewGlyph is the PR's review verdict in one character. Every PR in the
// list is open, so there is no merged or closed state to show.
func reviewGlyph(pr github.PR) string {
	if pr.IsDraft {
		return dimStyle.Render("◌")
	}
	switch strings.ToUpper(pr.ReviewDecision) {
	case "APPROVED":
		return okStyle.Render("✓")
	case "CHANGES_REQUESTED":
		return failStyle.Render("✗")
	case "REVIEW_REQUIRED":
		return pendingStyle.Render("◍")
	}
	// A repository that requires no review gives no decision: the
	// reviewers' own verdicts say it then.
	switch {
	case len(Reviewers(pr, "CHANGES_REQUESTED")) > 0:
		return failStyle.Render("✗")
	case len(Reviewers(pr, "APPROVED")) > 0:
		return okStyle.Render("✓")
	}
	return dimStyle.Render("·")
}

// Reviewers are those whose latest review of pr is in state, such as
// APPROVED.
func Reviewers(pr github.PR, state string) []string {
	var out []string
	for _, r := range pr.Reviews {
		if strings.EqualFold(r.State, state) && r.Author != pr.Author {
			out = append(out, r.Author)
		}
	}
	return out
}

// checksGlyph summarises the CI rollup: failing beats pending beats passing,
// since that is the order in which they need attention.
func checksGlyph(pr github.PR) string {
	pass, fail, pending := countChecks(pr.Checks)
	switch {
	case fail > 0:
		return failStyle.Render("✗")
	case pending > 0:
		return pendingStyle.Render("◍")
	case pass > 0:
		return okStyle.Render("✓")
	}
	return dimStyle.Render("·")
}

// PRGlyphs is the PR's review verdict and CI state, a character each, as
// the panel shows them — for nerdr's list of every PR.
func PRGlyphs(pr github.PR) (review, checks string) {
	return reviewGlyph(pr), checksGlyph(pr)
}

func countChecks(checks []github.Check) (pass, fail, pending int) {
	for _, c := range checks {
		switch st := strings.ToUpper(c.Status); {
		case github.Failed(st):
			fail++
		case st == "SUCCESS" || st == "NEUTRAL" || st == "SKIPPED":
			pass++
		default:
			pending++
		}
	}
	return pass, fail, pending
}

// convEntry is one navigable block in the conversation pane: a failing
// check, a review with words, or a comment. The pane moves between these
// rather than line by line, because a comment is read as a whole.
type convEntry struct {
	row    int    // index of the entry's first row in the pane
	kind   string // "check", "review" or "comment"
	author string
	body   string
	url    string
}

// answerable reports whether this is something an agent can reply to.
func (e convEntry) answerable() bool { return e.kind != "check" }

// prConversation renders what is worth reading about one PR — the checks
// that failed (the list's ✗ says that some did, not which), the reviews
// that came with words, and the comments — along with where each one
// starts, so the pane can be navigated a block at a time.
//
// fetched says whether the comments were actually loaded. The list carries
// comments but not review bodies, so a pane built from the list alone says
// what is still missing rather than implying it has everything.
func prConversation(pr github.PR, width int, fetched bool) ([]relLine, []convEntry) {
	if width < 20 {
		width = 20
	}
	var rows []relLine
	var entries []convEntry

	conversation := false
	add := func(e convEntry, head string) {
		if !conversation {
			conversation = true
			rows = append(rows, section("conversation", width))
		}
		e.row = len(rows)
		entries = append(entries, e)
		rows = append(rows, relLine{text: head, openURL: e.url})
		for _, l := range readable(e.body, width-4) {
			rows = append(rows, relLine{text: "    " + l})
		}
		rows = append(rows, relLine{text: ""})
	}

	for _, c := range pr.Checks {
		// A check still queued or running is listed too, with the list's
		// pending glyph: it has not failed.
		glyph := pendingStyle.Render("◍ ")
		switch st := strings.ToUpper(c.Status); {
		case st == "SUCCESS" || st == "NEUTRAL" || st == "SKIPPED":
			continue
		case github.Failed(st):
			glyph = failStyle.Render("✗ ")
		}
		if len(rows) == 0 {
			rows = append(rows, section("checks not passed", width))
		}
		entries = append(entries, convEntry{
			row: len(rows), kind: "check", author: c.Name, url: c.DetailsURL,
		})
		rows = append(rows, relLine{
			text:    glyph + printableLine(c.Name) + "  " + dimStyle.Render(printableLine(strings.ToLower(c.Status))),
			openURL: c.DetailsURL,
		})
	}

	if len(rows) > 0 {
		rows = append(rows, relLine{text: ""})
	}
	for _, r := range said(pr) {
		if strings.TrimSpace(r.Body) == "" {
			continue // a bare approval says all it has to say in the list
		}
		add(convEntry{kind: "review", author: r.Author, body: r.Body, url: r.URL},
			fmt.Sprintf("%s  %s  %s", headingText.Render(printableLine(r.Author)), printableLine(strings.ReplaceAll(strings.ToLower(r.State), "_", " ")),
				dimStyle.Render(r.SubmittedAt.Format("2006-01-02"))))
	}
	for _, c := range pr.Comments {
		head := headingText.Render(printableLine(c.Author))
		kind := "comment"
		if w := where(c); w != "" {
			head += "  " + printableLine(w)
			kind = "comment on " + w
		}
		add(convEntry{kind: kind, author: c.Author, body: c.Body, url: c.URL},
			head+"  "+dimStyle.Render(c.CreatedAt.Format("2006-01-02")))
	}

	if !conversation {
		rows = append(rows, section("conversation", width))
		if !fetched {
			rows = append(rows, relLine{text: dimStyle.Render("(press enter to read the conversation)")})
		} else {
			rows = append(rows, relLine{text: dimStyle.Render("(no comments)")})
		}
	}
	return rows, entries
}

// prConversationLines is prConversation for callers that only render it.
func prConversationLines(pr github.PR, width int, fetched bool) []relLine {
	rows, _ := prConversation(pr, width, fetched)
	return rows
}

// Both lists behind this screen go stale on their own: a repository can be
// cloned into the tree, and a PR's review or CI status changes on GitHub
// with nothing local to notice it. So both are re-read on a slow cadence,
// well apart from the 1.5 s file poll — these cost a directory scan and a
// round of `gh` calls respectively.
const (
	repoScanInterval  = 2 * time.Minute
	prRefreshInterval = 2 * time.Minute
)

// SetPRRefresh has the shown pull request read again every d.
func (m Model) SetPRRefresh(d time.Duration) Model {
	m.prEvery = d
	return m
}

// prRefresh is how often the shown pull request is read again.
func (m Model) prRefresh() time.Duration {
	if m.prEvery > 0 {
		return m.prEvery
	}
	return prRefreshInterval
}

// autoRefresh re-reads whatever has gone stale, and is called on every
// tick. It returns a command when something has to be fetched.
func (m *Model) autoRefresh() tea.Cmd {
	now := time.Now()
	if now.Sub(m.lastRepoScan) >= repoScanInterval {
		m.lastRepoScan = now
		m.rescanRepos()
	}
	// Only while the PR screen is the one being looked at: a round of gh
	// calls every two minutes behind a code review nobody is reading them
	// during is waste. Opening the screen refreshes a stale list anyway.
	if m.mode == modePRs && m.prShown == ViewBranch && now.Sub(m.lastPRLoad) >= m.prRefresh() {
		return m.reloadPR()
	}
	return nil
}

// rescanRepos re-reads the repositories under cwd, keeping the selection on
// the same repository rather than on the same index: a repo appearing or
// disappearing earlier in the list would otherwise silently move it.
func (m *Model) rescanRepos() {
	if m.embedded {
		return // pinned to the tab's worktrees
	}
	repos, err := git.DiscoverRepos(m.cwd)
	if err != nil || len(repos) == 0 {
		return
	}
	var selected string
	if m.repoIdx >= 0 && m.repoIdx < len(m.repos) {
		selected = m.repos[m.repoIdx].Path
	}
	m.repos = repos
	for i, r := range repos {
		if r.Path == selected {
			m.repoIdx = i
			return
		}
	}
	m.repoIdx = clamp(m.repoIdx, 0, len(repos)-1)
}
