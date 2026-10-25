package app

import (
	"strings"
	"testing"

	"nerdr/internal/agents"
	"nerdr/internal/config"
	"nerdr/internal/hooks"
)

// TestReviewWaitsForReady: with when = "ready" a turn that changed the
// worktree starts no review until the agent says its work is ready; then
// one starts at the end of its turn, told what the agent wants looked at.
// An agent without the skill is reviewed after every such turn instead.
func TestReviewWaitsForReady(t *testing.T) {
	m := testModel(t)
	m.cfg.Install = config.InstallHome
	m.cfg.Review = config.Review{Auto: true, When: config.WhenReady, Agent: "codex", Rounds: 2}
	// cat stands in for codex, which CI does not have.
	m.cfg.Agents = map[agents.Kind]agents.Spec{agents.Codex: {Command: []string{"cat"}}}
	tb := m.cur()
	tb.Dir = t.TempDir()
	tb.status = stIdle

	if m.afterTurn(tb) != nil {
		t.Fatal("a turn the agent did not call ready in started a review")
	}
	tb.status = stWorking
	if m.onReady(hooks.Report{Method: hooks.Ready, Pane: tb.hookKey(), Focus: "the locking"}) != nil {
		t.Fatal("ready mid-turn should wait for the turn to end")
	}
	if tb.reviewAsk == nil || tb.reviewAsk.focus != "the locking" {
		t.Fatalf("ready not kept: %v", tb.reviewAsk)
	}
	tb.status = stIdle
	if m.afterTurn(tb) == nil {
		t.Fatal("the end of the turn ready was said in should look at the worktree")
	}
	m.onTree(treeMsg{t: tb, why: treeAuto, changed: true, tree: "t1"})
	t.Cleanup(func() { m.endReview(tb, "done", false) })
	if tb.review == nil || tb.review.focus != "the locking" || tb.reviewAsk != nil {
		t.Fatalf("review %+v, ask %v", tb.review, tb.reviewAsk)
	}
	if p := tb.review.seats[0].r.paneOf(); p == nil || p.kind != paneReview || tb.focused() == p {
		t.Fatalf("the reviewer should have a pane of its own, leaving the keyboard where it was: %+v", p)
	}
	m.endReview(tb, "done", false)
	if len(tb.panes()) != 1 {
		t.Fatalf("the reviewer's pane should go with the review: %d panes", len(tb.panes()))
	}
	if v := strings.Join(m.reviewPaneLines(tb, &paneTab{kind: paneReview, of: tb}, 120, 40), "\n"); !strings.Contains(v, "says its work is ready") {
		t.Errorf("a review pane should say when a review starts by itself:\n%s", v)
	}

	// Without the skill there is nothing to wait for.
	m.cfg.EnabledSkills = []string{"nerdr-explain"}
	if m.afterTurn(tb) == nil {
		t.Fatal("an agent without nerdr-request-review should be reviewed after a turn")
	}
}

// TestReadyWithAutoOff: with auto off, a turn starts no review, but an
// agent saying its work is ready does, the user having asked for one.
func TestReadyWithAutoOff(t *testing.T) {
	m := testModel(t)
	tb := m.cur()
	tb.Dir = t.TempDir()
	tb.status = stIdle
	if m.afterTurn(tb) != nil {
		t.Fatal("a turn with auto off started a review")
	}
	if m.onReady(hooks.Report{Method: hooks.Ready, Pane: tb.hookKey(), Focus: "the locking"}) == nil || tb.reviewAsk == nil {
		t.Fatal("ready with auto off should look at the worktree for a review")
	}
}

// TestClaudeIsToldToSayReady: claude's system prompt asks it to use
// nerdr-request-review only while a review waits for it, and never a reviewer's.
func TestClaudeIsToldToSayReady(t *testing.T) {
	m := testModel(t)
	m.cfg.Install = config.InstallHome
	tb := m.cur()
	has := func(tb *tab) bool {
		args := m.claudeNote(tb)
		return len(args) == 2 && strings.Contains(args[1], "nerdr-request-review")
	}
	if has(tb) {
		t.Fatal("told about nerdr-request-review with auto off")
	}
	m.cfg.Review = config.Review{Auto: true, When: config.WhenReady}
	if !has(tb) || !strings.Contains(m.claudeNote(tb)[1], "nerdr-spinoff") {
		t.Fatalf("both notes expected: %q", m.claudeNote(tb))
	}
	if has(&tab{Tab: tb.Tab, reviewOf: tb}) {
		t.Fatal("a reviewer was told to ask for a review")
	}
	m.cfg.Review.When = config.WhenTurn
	if has(tb) {
		t.Fatal("told about nerdr-request-review with when = turn")
	}
}
