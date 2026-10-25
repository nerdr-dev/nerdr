package review

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"nerdr/internal/git"
	"nerdr/internal/github"
)

func samplePR() github.PR {
	return github.PR{
		Number: 7, Title: "Tighten access control", URL: "https://gh/pr/7",
		State: "OPEN", Author: "bob", Branch: "fix/access",
		UpdatedAt: time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC),
		Mergeable: "MERGEABLE", ReviewDecision: "CHANGES_REQUESTED",
		Reviews: []github.Review{{Author: "alice", State: "CHANGES_REQUESTED", URL: "https://gh/r/1"}},
		Checks: []github.Check{
			{Name: "CI / test", Status: "SUCCESS"},
			{Name: "CI / lint", Status: "FAILURE", DetailsURL: "https://gh/run/2"},
			{Name: "CI / e2e", Status: "IN_PROGRESS"},
		},
		Comments: []github.Comment{{
			Author: "bob", Body: "This needs a test for the denied path.",
			CreatedAt: time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC), URL: "https://gh/c/9",
		}},
	}
}

func rowTexts(rows []relLine) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(r.text + "\n")
	}
	return b.String()
}

// TestConversationShowsFailingChecksReviewsAndComments covers the lower
// pane carrying what you would actually read: which check failed (the
// list's ✗ only says that one did), reviews that came with words, and the
// comments.
func TestConversationShowsFailingChecksReviewsAndComments(t *testing.T) {
	pr := samplePR()
	pr.Reviews[0].Body = "The denied path is untested."
	got := stripANSI(rowTexts(prConversationLines(pr, 80, true)))

	for _, want := range []string{
		"✗ CI / lint",
		"◍ CI / e2e",
		"── checks not passed",
		"── conversation",
		"alice  changes requested",
		"The denied path is untested.",
		"bob",
		"This needs a test for the denied path.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// A passing check has nothing to read; the list's glyph covers it.
	if strings.Contains(got, "CI / test") {
		t.Errorf("passing checks should not be listed:\n%s", got)
	}
}

// TestPassingChecksHaveNoSection covers a PR whose checks all passed:
// nothing under "checks not passed", so no such heading either.
func TestPassingChecksHaveNoSection(t *testing.T) {
	pr := samplePR()
	pr.Checks = []github.Check{{Name: "CI / test", Status: "SUCCESS"}}
	if got := stripANSI(rowTexts(prConversationLines(pr, 80, true))); strings.Contains(got, "checks not passed") {
		t.Fatalf("a heading over nothing:\n%s", got)
	}
}

// TestRunningChecksAreNotShownAsFailed covers a check queued or in
// progress: listed with the pending glyph, not the ✗ of one that failed.
func TestRunningChecksAreNotShownAsFailed(t *testing.T) {
	pr := samplePR()
	pr.Checks = []github.Check{
		{Name: "CI / build", Status: "QUEUED"},
		{Name: "CI / test", Status: "IN_PROGRESS"},
		{Name: "CI / lint", Status: "FAILURE"},
	}
	got := stripANSI(rowTexts(prConversationLines(pr, 80, true)))
	for _, want := range []string{"◍ CI / build  queued", "◍ CI / test  in_progress", "✗ CI / lint  failure"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// TestBareApprovalIsNotAConversationEntry covers a review with no body: the
// list already shows the verdict, so an empty block under it is noise.
func TestBareApprovalIsNotAConversationEntry(t *testing.T) {
	pr := github.PR{Reviews: []github.Review{{Author: "alice", State: "APPROVED"}}}
	got := stripANSI(rowTexts(prConversationLines(pr, 80, true)))
	if strings.Contains(got, "alice") {
		t.Errorf("a wordless approval should not take a block in the conversation:\n%s", got)
	}
	if !strings.Contains(got, "(no comments)") {
		t.Errorf("expected the pane to say there is nothing to read:\n%s", got)
	}
}

// TestConversationDoesNotClaimSilenceBeforeFetching covers the list omitting
// comment bodies: saying "no comments" there would state as fact that a PR
// has no discussion when nobody has looked yet.
func TestConversationDoesNotClaimSilenceBeforeFetching(t *testing.T) {
	pr := github.PR{} // exactly what the list returns: no comment bodies

	got := rowTexts(prConversationLines(pr, 80, false))
	if strings.Contains(got, "no comments") {
		t.Fatalf("unfetched comments rendered as an absence of them:\n%s", got)
	}
	if !strings.Contains(got, "press enter") {
		t.Fatalf("expected the pane to say how to get them:\n%s", got)
	}

	// Once fetched, an actual absence is stated plainly.
	if got := rowTexts(prConversationLines(pr, 80, true)); !strings.Contains(got, "(no comments)") {
		t.Fatalf("a fetched PR with no comments should say so:\n%s", got)
	}
}

func TestPRRowGlyphsRankByWhatNeedsAttention(t *testing.T) {
	plain := func(s string) string { return stripANSI(s) }

	cases := []struct {
		name       string
		pr         github.PR
		wantReview string
		wantChecks string
	}{
		{"approved and green", github.PR{State: "OPEN", ReviewDecision: "APPROVED",
			Checks: []github.Check{{Status: "SUCCESS"}}}, "✓", "✓"},
		{"changes requested", github.PR{State: "OPEN", ReviewDecision: "CHANGES_REQUESTED"}, "✗", "·"},
		{"draft", github.PR{State: "OPEN", IsDraft: true}, "◌", "·"},
		{"draft beats a verdict", github.PR{State: "OPEN", IsDraft: true,
			ReviewDecision: "APPROVED"}, "◌", "·"},
		{"one failure among passes", github.PR{State: "OPEN",
			Checks: []github.Check{{Status: "SUCCESS"}, {Status: "FAILURE"}, {Status: "QUEUED"}}}, "·", "✗"},
		{"still running", github.PR{State: "OPEN",
			Checks: []github.Check{{Status: "SUCCESS"}, {Status: "IN_PROGRESS"}}}, "·", "◍"},
	}
	for _, c := range cases {
		if got := plain(reviewGlyph(c.pr)); got != c.wantReview {
			t.Errorf("%s: review glyph = %q, want %q", c.name, got, c.wantReview)
		}
		if got := plain(checksGlyph(c.pr)); got != c.wantChecks {
			t.Errorf("%s: checks glyph = %q, want %q", c.name, got, c.wantChecks)
		}
	}
}

// TestMenuSwitchesModes covers F1/F2 and, more importantly, that the review
// layout's keys stop at the PR screen: they act on panes that aren't shown.
func TestMenuSwitchesModes(t *testing.T) {
	m := Model{height: 40, width: 120, baseRef: "main"}

	send := func(key string) {
		var msg tea.Msg
		if len(key) > 1 {
			msg = tea.KeyMsg{Type: tea.KeyF2}
			if key == "f1" {
				msg = tea.KeyMsg{Type: tea.KeyF1}
			}
		} else {
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
		}
		nm, _ := m.Update(msg)
		m = nm.(Model)
	}

	send("f2")
	if m.mode != modePRs {
		t.Fatalf("F2 should open the PR overview, got mode %d", m.mode)
	}

	send("b") // base diff: a review-layout key
	if m.showBase {
		t.Error("the base diff must not be toggled from the PR screen")
	}
	send("/") // file filter
	if m.filtering {
		t.Error("the file filter must not open from the PR screen")
	}

	send("f1")
	if m.mode != modeReview {
		t.Fatalf("F1 should return to code changes, got mode %d", m.mode)
	}
	// And back in the review layout the same key works again.
	send("b")
	if !m.showBase {
		t.Error("expected b to toggle the base diff in review mode")
	}
}

// TestStalePRReplyIsIgnored covers a slow gh answer landing after the user
// has asked for a reload: the older list must not overwrite the newer one.
// TestArrowsOnlyMoveBetweenPullRequests covers the one navigation rule of
// this screen: the cursor lives in the list, and the conversation below is
// read rather than stepped through.
// TestScrollToMovesTheViewAsLittleAsPossible covers the shared viewport rule
// the PR panes use: the cursor moves freely inside the view, and only an
// edge forces a scroll.
func TestScrollToMovesTheViewAsLittleAsPossible(t *testing.T) {
	const innerH = 10
	cases := []struct{ cursor, viewTop, want int }{
		{5, 0, 0},   // inside the view: nothing moves
		{9, 0, 0},   // last visible row
		{10, 0, 1},  // one past the bottom: scroll by one
		{30, 0, 21}, // a jump: land with the cursor at the bottom
		{3, 10, 3},  // above the view: cursor becomes the top
	}
	for _, c := range cases {
		if got := scrollTo(c.cursor, c.viewTop, innerH); got != c.want {
			t.Errorf("scrollTo(cursor=%d, top=%d) = %d, want %d", c.cursor, c.viewTop, got, c.want)
		}
	}
}

// TestPREntriesGroupByRepoThenRecency covers the list's order: a
// repository's PRs stay together, and within one the newest is on top.
// TestListColumnsLineUpWithTheirHeader covers the one thing a bare pair of
// ticks cannot say: which glyph is the review verdict and which is CI. The
// header names them, so each glyph has to sit under its own name.
// TestBackgroundRefreshKeepsYouOnTheSamePR covers a two-minute refresh
// landing while you read: it must not move the cursor to a different pull
// request because one above it disappeared.
// TestRepoRescanKeepsTheSelectedRepo covers the same for the repo list: a
// repository cloned in earlier in the alphabet must not shift the selection
// onto a different one.
func TestRepoRescanKeepsTheSelectedRepo(t *testing.T) {
	parent := t.TempDir()
	mk := func(name string) {
		repo := filepath.Join(parent, name)
		runGit(t, "", "init", "-q", repo)
		writeFile(t, filepath.Join(repo, "a.txt"), "x\n")
		runGit(t, repo, "add", ".")
		runGit(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "init")
	}
	mk("beta")
	mk("gamma")

	m := New(parent)
	if len(m.repos) != 2 {
		t.Fatalf("expected 2 repos, got %#v", m.repos)
	}
	m.repoIdx = 1
	selected := m.repos[1].Path

	mk("alpha") // sorts first, shifting every index
	m.rescanRepos()

	if len(m.repos) != 3 {
		t.Fatalf("the new repo was not picked up: %#v", m.repos)
	}
	if m.repos[m.repoIdx].Path != selected {
		t.Fatalf("selection moved to %q, want %q", m.repos[m.repoIdx].Path, selected)
	}
}

// TestCommentMarkerSaysWhetherAnyoneHasWritten covers the row answering the
// question that used to need opening the PR.
// TestConversationEntriesAnchorEachBlock covers what the pane navigates
// between: each failing check, review and comment must be findable at its
// own first row.
func TestConversationEntriesAnchorEachBlock(t *testing.T) {
	pr := samplePR()
	pr.Reviews[0].Body = "The denied path is untested."
	pr.Comments = append(pr.Comments, github.Comment{Author: "carol", Body: "Agreed."})

	rows, entries := prConversation(pr, 80, true)

	// Two failing/pending checks, one review with words, two comments.
	if len(entries) != 5 {
		t.Fatalf("expected 5 navigable blocks, got %d: %#v", len(entries), entries)
	}
	for i, e := range entries {
		if e.row < 0 || e.row >= len(rows) {
			t.Fatalf("block %d points at row %d, outside the %d rendered", i, e.row, len(rows))
		}
		if i > 0 && e.row <= entries[i-1].row {
			t.Fatalf("block %d is not after block %d (rows %d, %d)", i, i-1, e.row, entries[i-1].row)
		}
		head := stripANSI(rows[e.row].text)
		if e.kind != "check" && !strings.Contains(head, e.author) {
			t.Errorf("block %d (%s) does not start on its own header: %q", i, e.kind, head)
		}
	}
	if entries[0].kind != "check" || entries[0].answerable() {
		t.Errorf("a failing check is not something to reply to: %#v", entries[0])
	}
	if !entries[2].answerable() {
		t.Errorf("a review with words should be answerable: %#v", entries[2])
	}
}

// TestConvNavigationStepsBlockByBlock covers the pane moving a comment at a
// time — stepping through a long comment line by line is not navigation —
// and bringing the selected one to the top, where you start reading it.
// TestAnswerKeyFollowsFocus covers `a` meaning "this one" in the pane and
// "all of them" in the list.
// prModel is the PR screen on a worktree whose PR is pr.
func prModel(pr github.PR) Model {
	return Model{height: 40, width: 120, mode: modePRs, curWT: "/wt",
		worktrees: []git.Worktree{{Path: "/wt"}}, pr: pr, prOK: true}
}

// TestStalePRReloadIsIgnored covers a slow gh answer landing after a newer
// reload, or after the panel has moved to another worktree.
func TestStalePRReloadIsIgnored(t *testing.T) {
	m := prModel(github.PR{Number: 1})
	m.prToken = 2
	nm, _ := m.Update(prReloadMsg{wt: "/wt", token: 1, pr: github.PR{Number: 9}, ok: true})
	if m = nm.(Model); m.pr.Number != 1 {
		t.Fatalf("a superseded reply was applied: #%d", m.pr.Number)
	}
	nm, _ = m.Update(prReloadMsg{wt: "/other", token: 2, pr: github.PR{Number: 9}, ok: true})
	if m = nm.(Model); m.pr.Number != 1 {
		t.Fatalf("a reply about another worktree was applied: #%d", m.pr.Number)
	}
	nm, _ = m.Update(prReloadMsg{wt: "/wt", token: 2, pr: github.PR{Number: 2}, ok: true})
	if m = nm.(Model); m.pr.Number != 2 {
		t.Fatalf("the current reply was not applied: #%d", m.pr.Number)
	}
}

// TestPRScreenShowsOnlyTheWorktreesPR covers F2 being about the worktree
// you stand on: its PR's header, then its conversation.
func TestPRScreenShowsOnlyTheWorktreesPR(t *testing.T) {
	m := prModel(samplePR())
	got := stripANSI(m.View())
	for _, want := range []string{"#7 Tighten access control", "CI / lint", "This needs a test"} {
		if !strings.Contains(got, want) {
			t.Errorf("the PR screen lacks %q:\n%s", want, got)
		}
	}
	m.prOK, m.prMsg = false, "no pull requests found for branch \"x\""
	if got := stripANSI(m.View()); !strings.Contains(got, "no pull requests found") {
		t.Errorf("without a PR the screen should say why:\n%s", got)
	}
}

// TestConvNavigationStepsBlockByBlock covers the cursor moving a comment
// at a time, and the header staying above the first.
func TestConvNavigationStepsBlockByBlock(t *testing.T) {
	pr := samplePR()
	pr.Checks, pr.Reviews, pr.Comments = nil, nil, nil
	for _, a := range []string{"alice", "bob", "carol"} {
		pr.Comments = append(pr.Comments, github.Comment{
			Author: a, Body: strings.Repeat("a long comment body. ", 250),
		})
	}
	m := prModel(pr)
	_, entries := m.prContent()
	if len(entries) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(entries))
	}
	send := func(k tea.KeyType) {
		nm, _ := m.Update(tea.KeyMsg{Type: k})
		m = nm.(Model)
	}
	send(tea.KeyDown)
	if m.prConvCursor != 1 {
		t.Fatalf("down should select the next comment, got %d", m.prConvCursor)
	}
	if m.prConvViewTop != entries[1].row {
		t.Fatalf("the selected comment should be at the top: top %d, row %d", m.prConvViewTop, entries[1].row)
	}
	send(tea.KeyUp)
	if m.prConvCursor != 0 || m.prConvViewTop != 0 {
		t.Fatalf("back on the first, the header should show: cursor %d top %d", m.prConvCursor, m.prConvViewTop)
	}
}

// agentText runs cmd and returns the text it asks nerdr to give the agent.
func agentText(t *testing.T, cmd tea.Cmd) AgentMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a message for the agent, got no command")
	}
	msg, ok := cmd().(AgentMsg)
	if !ok {
		t.Fatalf("expected an AgentMsg, got %#v", msg)
	}
	return msg
}

// TestReplyKeysAskTheWorktreesAgent covers a and A: drafts are asked of
// the agent in the worktree, as a message, never run on the side.
func TestReplyKeysAskTheWorktreesAgent(t *testing.T) {
	pr := samplePR()
	pr.Reviews[0].Body = "The denied path is untested."
	m := prModel(pr)
	_, entries := m.prContent()
	for i, e := range entries {
		if e.kind == "comment" {
			m.prConvCursor = i
		}
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	one := agentText(t, cmd)
	if one.Dir != "/wt" || !strings.Contains(one.Text, "This needs a test") ||
		!strings.Contains(one.Text, "do not answer these") || !strings.Contains(one.Text, "do not post") {
		t.Errorf("a: %#v", one)
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("A")})
	all := agentText(t, cmd)
	if !strings.Contains(all.Text, "[1] review by alice") || !strings.Contains(all.Text, "[2] comment by bob") {
		t.Errorf("A: %#v", all)
	}

	// On a failing check there is nothing to reply to.
	m.prConvCursor = 0
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")}); cmd != nil {
		t.Error("a on a check should not ask the agent anything")
	}
}

// TestFixAsksTheAgentWithTheLog covers f: once the log is read, the
// message carries the failing checks and the log.
func TestFixAsksTheAgentWithTheLog(t *testing.T) {
	m := prModel(samplePR())
	nm, cmd := m.Update(prFixMsg{wt: "/wt", pr: samplePR(), logs: "Error: lint failed at x.go:3"})
	if nm.(Model).prBusy {
		t.Error("the log arriving should end the wait")
	}
	msg := agentText(t, cmd)
	if msg.Dir != "/wt" || !strings.Contains(msg.Text, "CI / lint (failure)") ||
		!strings.Contains(msg.Text, "lint failed at x.go:3") || !strings.Contains(msg.Text, "Do not commit, push") {
		t.Errorf("f: %#v", msg)
	}

	pass := samplePR()
	pass.Checks = []github.Check{{Name: "ok", Status: "SUCCESS"}}
	m = prModel(pass)
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")}); cmd != nil {
		t.Error("f with every check green should do nothing")
	}
}

// TestPRScreenHasAnOpenInBrowserButton covers the button under the PR's
// header: a click on it opens the PR, a click elsewhere only selects.
func TestPRScreenHasAnOpenInBrowserButton(t *testing.T) {
	m := prModel(samplePR())
	rows, _ := m.prContent()
	button := -1
	for i, r := range rows {
		if r.button {
			button = i
		}
	}
	if button < 0 || rows[button].openURL != "https://gh/pr/7" ||
		!strings.Contains(stripANSI(rows[button].text), "open in browser") {
		t.Fatalf("no button to open the PR: %#v", rows)
	}
	l := m.layout(120, 40)
	click := func(row int) tea.Cmd {
		return m.handleMouse(tea.MouseMsg{X: l.prConv.x + 3, Y: l.prConv.y + 2 + row,
			Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	}
	if click(button) == nil {
		t.Error("a click on the button should open the PR")
	}
	if click(0) != nil {
		t.Error("a click on the title only selects")
	}
}

// TestLineCommentsInConversation: a comment on a line of the code says
// where it is, in the pane and in what the agent is asked; reviews come
// from all a reviewer said, not only its latest.
func TestLineCommentsInConversation(t *testing.T) {
	pr := github.PR{Number: 3, Title: "t",
		Reviews: []github.Review{{Author: "bob", State: "APPROVED"}},
		Said:    []github.Review{{Author: "bob", State: "COMMENTED", Body: "First round."}},
		Comments: []github.Comment{
			{Author: "ann", Body: "General."},
			{Author: "bob", Body: "Off by one.", Path: "a/b.go", Line: 12},
		},
	}
	rows, entries := prConversation(pr, 80, true)
	var text []string
	for _, r := range rows {
		text = append(text, ansi.Strip(r.text))
	}
	all := strings.Join(text, "\n")
	for _, want := range []string{"First round.", "General.", "bob  a/b.go:12", "Off by one."} {
		if !strings.Contains(all, want) {
			t.Fatalf("the conversation lacks %q:\n%s", want, all)
		}
	}
	if len(entries) != 3 {
		t.Fatalf("%d entries, want 3", len(entries))
	}
	if p := replyPrompt(pr); !strings.Contains(p, "comment by bob on a/b.go:12") || !strings.Contains(p, "First round.") {
		t.Fatalf("the reply prompt:\n%s", p)
	}
}

// TestReviewGlyphWithoutDecision: a repository that requires no review
// gives no decision, and the reviewers' verdicts make the glyph.
func TestReviewGlyphWithoutDecision(t *testing.T) {
	pr := github.PR{Author: "me", Reviews: []github.Review{{Author: "ann", State: "APPROVED"}, {Author: "me", State: "COMMENTED"}}}
	if g := ansi.Strip(reviewGlyph(pr)); g != "✓" {
		t.Fatalf("approved by ann: %q", g)
	}
	if who := Reviewers(pr, "APPROVED"); len(who) != 1 || who[0] != "ann" {
		t.Fatalf("approvers %v", who)
	}
	pr.Reviews = append(pr.Reviews, github.Review{Author: "bob", State: "CHANGES_REQUESTED"})
	if g := ansi.Strip(reviewGlyph(pr)); g != "✗" {
		t.Fatalf("changes requested by bob: %q", g)
	}
}
