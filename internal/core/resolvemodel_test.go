package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/hooks"
)

// TestResolveModel covers a model named for a review as the user said it:
// nerdr finds the agent's own name for it, asks back when it could be
// several, and with model_names = "agent" takes only the agent's names.
func TestResolveModel(t *testing.T) {
	m := &Core{AgentModels: map[agents.Kind]ModelList{agents.Codex: {Models: []agents.AgentModel{
		{ID: "gpt-6.2-astra"}, {ID: "gpt-6.2-astra-mini"}, {ID: "gpt-6.2-sol"}, {ID: "gpt-6.1-sol"}, {ID: "gpt-6.1-sol-pro"}}}}}
	m.Cfg.Review.Models = map[agents.Kind]string{agents.Codex: "gpt-5-old"}
	for _, c := range []struct{ in, want, err string }{
		{"gpt-6.2-astra", "gpt-6.2-astra", ""},
		{"GPT-6.2-Astra", "gpt-6.2-astra", ""},
		{"astra 6.2", "gpt-6.2-astra", ""}, // the fewest words besides
		{"astra mini", "gpt-6.2-astra-mini", ""},
		{"gpt-5-old", "gpt-5-old", ""}, // the config's, though not listed
		{"sol", "", "could be any of codex's models gpt-6.2-sol, gpt-6.1-sol"},
		{"gpt-7", "gpt-7", ""}, // a name the list may be short of
		{"luna 7", "", "none of codex's models is \"luna 7\""},
	} {
		got, err := m.ResolveModel(agents.Codex, c.in)
		switch {
		case c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)):
			t.Errorf("%q: got %q, %v; want the error %q", c.in, got, err, c.err)
		case c.err == "" && (err != nil || got != c.want):
			t.Errorf("%q: got %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	// With no list to look in, a loose name cannot be found.
	if _, err := m.ResolveModel(agents.Cursor, "sonnet 5"); err == nil || !strings.Contains(err.Error(), "could not get cursor's models") {
		t.Errorf("no list: %v", err)
	}

	m.Cfg.Review.ModelNames = config.NamesByAgent
	for _, c := range []struct{ in, want string }{{"gpt-6.2-astra", "gpt-6.2-astra"}, {"gpt-7", "gpt-7"}} {
		if got, err := m.ResolveModel(agents.Codex, c.in); err != nil || got != c.want {
			t.Errorf("agent's names, %q: got %q, %v", c.in, got, err)
		}
	}
	for _, in := range []string{"astra 6.2", "6.2-astra"} {
		if _, err := m.ResolveModel(agents.Codex, in); err == nil || !strings.Contains(err.Error(), "models_cache.json") {
			t.Errorf("agent's names, %q: %v", in, err)
		}
	}
}

// TestReadyListsTheModels: a request naming a model of an agent nerdr has
// no list of yet has it listed first, and is answered once it has.
func TestReadyListsTheModels(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	cache := `{"models": [{"slug": "gpt-6.2-astra", "priority": 1}, {"slug": "gpt-6.2-sol", "priority": 2}]}`
	if err := os.WriteFile(filepath.Join(home, "models_cache.json"), []byte(cache), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &Core{Agents: []agents.Kind{agents.Codex}}
	m.AgentsAt = m.AgentsAt.AddDate(2026, 0, 0)
	tb := &Tab{}
	tb.ID, tb.Agent, tb.Light = "a", agents.Claude, StWorking
	m.Tabs = []*Tab{tb}
	reply := make(chan hooks.Answer, 1)
	rep := hooks.Report{Method: hooks.RequestReview, Pane: tb.HookKey(), Reply: reply,
		Reviewers: []hooks.Reviewer{{Kind: "codex", Model: "astra"}}}
	cmd := m.OnReady(rep)
	if cmd == nil || tb.ReviewAsk != nil {
		t.Fatal("not listed first")
	}
	ev, ok := cmd().(readyListed)
	if !ok {
		t.Fatal("not a list")
	}
	m.onReadyListed(ev)
	if a := <-reply; a.Err != "" || a.Text != "codex reviews with gpt-6.2-astra" {
		t.Fatalf("answer %+v", a)
	}
	if a := tb.ReviewAsk; a == nil || len(a.By) != 1 || a.By[0].Model != "gpt-6.2-astra" {
		t.Fatalf("ask %+v", a)
	}
}

// TestReadyListsAgainWhenOld: a list of models older than ModelsFor is
// listed again before a model named for a review is looked for in it.
func TestReadyListsAgainWhenOld(t *testing.T) {
	m := &Core{Agents: []agents.Kind{agents.Codex}}
	m.AgentsAt = m.AgentsAt.AddDate(2026, 0, 0)
	m.AgentModels = map[agents.Kind]ModelList{agents.Codex: {Models: []agents.AgentModel{{ID: "gpt-5-old"}},
		At: time.Now().Add(-ModelsFor - time.Minute)}}
	tb := &Tab{}
	tb.ID, tb.Agent, tb.Light = "a", agents.Claude, StWorking
	m.Tabs = []*Tab{tb}
	cmd := m.OnReady(hooks.Report{Method: hooks.RequestReview, Pane: tb.HookKey(), Reviewers: []hooks.Reviewer{{Kind: "codex", Model: "old"}}})
	if cmd == nil || tb.ReviewAsk != nil {
		t.Fatal("an old list was looked in, not listed again")
	}
}

// TestReadyListingIsBounded: an agent slow to list its models holds the
// request up no longer than ReadyListWait, nor past the time the agent
// waits for the answer, counted from when it asked, however long the
// request waited for core; one taken up after that starts no review.
func TestReadyListingIsBounded(t *testing.T) {
	old := ReadyListWait
	ReadyListWait = 200 * time.Millisecond
	t.Cleanup(func() { ReadyListWait = old })
	m := &Core{Agents: []agents.Kind{agents.OpenCode}}
	m.AgentsAt = m.AgentsAt.AddDate(2026, 0, 0)
	m.Cfg.Agents = map[agents.Kind]agents.Spec{agents.OpenCode: {Command: []string{"sh", "-c", "sleep 5", "sh"}}}
	tb := &Tab{}
	tb.ID, tb.Agent, tb.Light = "a", agents.Claude, StWorking
	m.Tabs = []*Tab{tb}
	reply := make(chan hooks.Answer, 1)
	rep := hooks.Report{Method: hooks.RequestReview, Pane: tb.HookKey(), Reply: reply,
		Reviewers: []hooks.Reviewer{{Kind: "opencode", Model: "anthropic/sonnet"}}}
	start := time.Now()
	ev := m.OnReady(rep)().(readyListed)
	if took := time.Since(start); took > 2*time.Second || len(ev.lists) != 0 {
		t.Fatalf("waited %v for %d lists", took, len(ev.lists))
	}

	// Waited for core most of the time the agent waits: the look for the
	// models ends in time to answer it.
	ReadyListWait = time.Minute
	rep.Deadline = time.Now().Add(readyMargin + 300*time.Millisecond)
	start = time.Now()
	late := m.OnReady(rep)().(readyListed)
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("looked for %v, past the agent's wait", took)
	}
	for time.Now().Before(rep.Deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	m.onReadyListed(late)
	if tb.ReviewAsk != nil {
		t.Fatal("taken up after the agent gave up waiting")
	}
	m.onReadyListed(ev)
	if a := <-reply; a.Err != "" || tb.ReviewAsk == nil || tb.ReviewAsk.By[0].Model != "anthropic/sonnet" {
		t.Fatalf("in time, with no list, an exact name goes as it is: %+v, %+v", a, tb.ReviewAsk)
	}
}
