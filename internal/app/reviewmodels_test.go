package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/hooks"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// argvAgent has codex stand in by a shell that adds the command line it
// was started with to a file, each ended by --, then waits as an agent
// does; the file's path is returned. nerdr runs it for its help too.
func argvAgent(t *testing.T, m *Model) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "argv")
	m.Core.Cfg.Agents = map[agents.Kind]agents.Spec{agents.Codex: {
		Command: []string{"sh", "-c", `printf '%s\n' "$@" -- >> "` + out + `"; exec cat`, "sh"},
	}}
	askedHelp(m.Core.Cfg.Agents)
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
func reviewOn(t *testing.T, m *Model) *core.Tab {
	t.Helper()
	tb := m.Cur()
	tb.Dir = testRepo(t)
	tb.Worktrees = nil
	writeRepoFile(t, tb.Dir, "a.txt", "two\n")
	tb.Light = core.StIdle
	return tb
}

// TestReadyNamesTheReviewer covers nerdr request-review --agent --model:
// the review is by that agent, started with that model.
func TestReadyNamesTheReviewer(t *testing.T) {
	m := testModel(t)
	out := argvAgent(t, m)
	tb := reviewOn(t, m)
	cmd := m.Core.OnReady(hooks.Report{Method: hooks.RequestReview, Pane: tb.HookKey(), Kind: "codex", Model: "gpt-6-astra"})
	if cmd == nil {
		t.Fatal("ready between turns looks at the worktree")
	}
	m.Core.OnTree(cmd().(core.TreeMsg))
	t.Cleanup(func() { m.Core.EndReview(tb, "done", false) })
	if tb.Review == nil || tb.Review.Seats[0].R.Agent != agents.Codex || tb.Review.Seats[0].R.Model != "gpt-6-astra" {
		t.Fatalf("review %+v", tb.Review)
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
	m.Core.Cfg.Review.Models = map[agents.Kind]string{agents.Codex: "gpt-6-sol"}
	tb := reviewOn(t, m)
	m.Core.OnTree(m.Core.OnReady(hooks.Report{Method: hooks.RequestReview, Pane: tb.HookKey(), Kind: "codex"})().(core.TreeMsg))
	t.Cleanup(func() { m.Core.EndReview(tb, "done", false) })
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
	p := m.focused(tb)
	for i, k := range m.reviewKinds(tb) {
		if k == agents.Codex {
			m.reviewerCursor = i
		}
	}
	m.handleKey(keyMsg("m"))
	if m.reviewPick == nil || m.reviewPick.kind != agents.Codex {
		t.Fatal("m picks codex's model")
	}
	m.Core.OnModels(core.ModelsMsg{Kind: agents.Codex, Models: []agents.AgentModel{{ID: "gpt-6-luna", Name: "GPT-6-Luna"}, {ID: "gpt-6-astra", Name: "GPT-6-Astra"}}})
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
	m.Core.OnTree(m.Core.LookAtTree(tb, core.TreeHand, core.Reviewer{Kind: agents.Codex, Model: "gpt-6-astra"})().(core.TreeMsg))
	t.Cleanup(func() { m.Core.EndReview(tb, "done", false) })
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
		if !core.ModelMatches(am, typed) {
			t.Errorf("%q should find %s", typed, am.ID)
		}
	}
	for _, typed := range []string{"opus", "sonnet 4"} {
		if core.ModelMatches(am, typed) {
			t.Errorf("%q should not find %s", typed, am.ID)
		}
	}
}

// TestReviewPaneModelButton covers the model button on an agent's line in
// the review pane: it shows the model the agent reviews with, a click on
// it picks another, and the one picked is kept in the config.
func TestReviewPaneModelButton(t *testing.T) {
	m := testModel(t)
	argvAgent(t, m)
	tb := reviewOn(t, m)
	m.runCommand("t r")
	m.Core.OnModels(core.ModelsMsg{Kind: agents.Codex, Own: "gpt-6-luna", Models: []agents.AgentModel{{ID: "gpt-6-luna"}, {ID: "gpt-6-astra"}}})
	clickOn(t, m, "gpt-6-luna ▾", tea.MouseButtonLeft)
	if m.reviewPick == nil || m.reviewPick.kind != agents.Codex {
		t.Fatal("the button picks codex's model")
	}
	settle(m, clickOn(t, m, "gpt-6-astra", tea.MouseButtonLeft))
	if m.reviewPick != nil || m.Core.Cfg.Review.Models[agents.Codex] != "gpt-6-astra" {
		t.Fatalf("picked %q", m.Core.Cfg.Review.Models[agents.Codex])
	}
	t.Cleanup(func() {
		if tb.Review != nil {
			m.Core.EndReview(tb, "done", false)
		}
	})
	cfg, err := config.Load()
	if err != nil || cfg.Review.Models[agents.Codex] != "gpt-6-astra" {
		t.Fatalf("saved %v, %v", cfg.Review.Models, err)
	}
}

// TestPickOffersModelsUsedBefore: a model the agent does not list, but
// one of its kind was started with, is offered, and the one picked before
// is where the pick starts.
func TestPickOffersModelsUsedBefore(t *testing.T) {
	m := testModel(t)
	m.Core.Cfg.Review.Models = map[agents.Kind]string{agents.Gemini: "gemini-picked"}
	m.Cur().Agent, m.Cur().Model = agents.Gemini, "gemini-ran"
	m.Core.OnModels(core.ModelsMsg{Kind: agents.Gemini, Again: true})
	m.openModelPick(agents.Gemini)
	cs := m.choices(m.reviewPick)
	var got []string
	for _, c := range cs {
		got = append(got, c.Model)
	}
	if strings.Join(got, ",") != ",gemini-picked,gemini-ran" {
		t.Fatalf("choices %q", got)
	}
	if m.reviewPick.cursor != 1 {
		t.Fatalf("starts on %d, not the one picked before", m.reviewPick.cursor)
	}
}

// TestModelsAskedAgainWhenNone: an agent that lists no models is asked
// once more a little later, not left with none until the pick is opened
// again.
func TestModelsAskedAgainWhenNone(t *testing.T) {
	m := testModel(t)
	if m.Core.OnModels(core.ModelsMsg{Kind: agents.OpenCode}) == nil || !m.Core.AgentModels[agents.OpenCode].Asking {
		t.Fatal("none listed: asked again")
	}
	if m.Core.OnModels(core.ModelsMsg{Kind: agents.OpenCode, Again: true}) != nil {
		t.Fatal("none listed the second time: left until next time")
	}
	if m.Core.OnModels(core.ModelsMsg{Kind: agents.OpenCode, Models: []agents.AgentModel{{ID: "a/b"}}}) != nil {
		t.Fatal("listed: kept")
	}
}
