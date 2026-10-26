package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/hooks"

	"github.com/charmbracelet/x/ansi"
)

// argvAgent has codex stand in by a shell that adds the command line it
// was started with to a file, each ended by --, then waits as an agent
// does; the file's path is returned. nerdr runs it for its help too.
func argvAgent(t *testing.T, m *Model) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "argv")
	m.cfg.Agents = map[agents.Kind]agents.Spec{agents.Codex: {
		Command: []string{"sh", "-c", `printf '%s\n' "$@" -- >> "` + out + `"; exec cat`, "sh"},
	}}
	return out
}

// startedWith is the command lines the stand-in was started with, once
// it has been with a model. It waits up to 10s: CI's macOS runners can
// take over 2s to start it.
func startedWith(t *testing.T, out string) string {
	t.Helper()
	var b []byte
	for range 500 {
		b, _ = os.ReadFile(out)
		if strings.Contains(string(b), "--model\n") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return string(b)
}

// reviewOn makes tb's worktree a repository with a change to review.
func reviewOn(t *testing.T, m *Model) *tab {
	t.Helper()
	tb := m.cur()
	tb.Dir = testRepo(t)
	tb.Worktrees = nil
	writeRepoFile(t, tb.Dir, "a.txt", "two\n")
	tb.status = stIdle
	return tb
}

// TestReadyNamesTheReviewer covers nerdr request-review --agent --model:
// the review is by that agent, started with that model.
func TestReadyNamesTheReviewer(t *testing.T) {
	m := testModel(t)
	out := argvAgent(t, m)
	tb := reviewOn(t, m)
	cmd := m.onReady(hooks.Report{Method: hooks.RequestReview, Pane: tb.hookKey(), Kind: "codex", Model: "gpt-6-astra"})
	if cmd == nil {
		t.Fatal("ready between turns looks at the worktree")
	}
	m.onTree(cmd().(treeMsg))
	t.Cleanup(func() { m.endReview(tb, "done", false) })
	if tb.review == nil || tb.review.seats[0].r.Agent != agents.Codex || tb.review.seats[0].r.Model != "gpt-6-astra" {
		t.Fatalf("review %+v", tb.review)
	}
	if argv := startedWith(t, out); !strings.Contains(argv, "--model\ngpt-6-astra\n") {
		t.Fatalf("started with %q", argv)
	}
}

// TestReviewModelFromTheConfig covers [review.models]: the reviewer starts
// with the model given there, when none is named for the review.
func TestReviewModelFromTheConfig(t *testing.T) {
	m := testModel(t)
	out := argvAgent(t, m)
	m.cfg.Review.Models = map[agents.Kind]string{agents.Codex: "gpt-6-sol"}
	tb := reviewOn(t, m)
	m.onTree(m.onReady(hooks.Report{Method: hooks.RequestReview, Pane: tb.hookKey(), Kind: "codex"})().(treeMsg))
	t.Cleanup(func() { m.endReview(tb, "done", false) })
	if argv := startedWith(t, out); !strings.Contains(argv, "--model\ngpt-6-sol\n") {
		t.Fatalf("started with %q", argv)
	}
}

// TestReviewPanePicksAModel covers m in the review pane: the models the
// agent listed, narrowed by what is typed, and the review started with
// the one picked.
func TestReviewPanePicksAModel(t *testing.T) {
	m := testModel(t)
	out := argvAgent(t, m)
	tb := reviewOn(t, m)
	m.runCommand("t r")
	p := tb.focused()
	for i, k := range m.reviewKinds(tb) {
		if k == agents.Codex {
			m.reviewerCursor = i
		}
	}
	m.handleKey(keyMsg("m"))
	if m.reviewPick == nil || m.reviewPick.kind != agents.Codex {
		t.Fatal("m picks codex's model")
	}
	m.onModels(modelsMsg{kind: agents.Codex, models: []agents.AgentModel{{ID: "gpt-6-luna", Name: "GPT-6-Luna"}, {ID: "gpt-6-astra", Name: "GPT-6-Astra"}}})
	for _, r := range "astra" {
		m.handleKey(keyMsg(string(r)))
	}
	view := ansi.Strip(strings.Join(m.reviewPaneLines(tb, p, 100, 30), "\n"))
	if !strings.Contains(view, "❯ gpt-6-astra") || strings.Contains(view, "gpt-6-luna") {
		t.Fatalf("typing narrows to the one that matches, picked:\n%s", view)
	}
	look := m.handleKey(keyMsg("enter"))
	if look == nil || m.reviewPick != nil {
		t.Fatal("enter starts the review")
	}
	m.onTree(m.lookAtTree(tb, treeHand, reviewer{kind: agents.Codex, model: "gpt-6-astra"})().(treeMsg))
	t.Cleanup(func() { m.endReview(tb, "done", false) })
	if argv := startedWith(t, out); !strings.Contains(argv, "--model\ngpt-6-astra\n") {
		t.Fatalf("started with %q", argv)
	}
}

// TestModelMatchesWordByWord: the words typed narrow the list wherever
// they are in a model's id, so a name typed not quite as the agent has
// it still finds it.
func TestModelMatchesWordByWord(t *testing.T) {
	am := agents.AgentModel{ID: "claude-sonnet-5.5"}
	for _, typed := range []string{"", "sonnet", "sonnet 5.5", "Sonnet-5.5", "claude 5.5"} {
		if !modelMatches(am, typed) {
			t.Errorf("%q should find %s", typed, am.ID)
		}
	}
	for _, typed := range []string{"opus", "sonnet 4"} {
		if modelMatches(am, typed) {
			t.Errorf("%q should not find %s", typed, am.ID)
		}
	}
}
