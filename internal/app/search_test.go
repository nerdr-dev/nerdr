package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/agents"
)

// TestOpenConversation: a conversation an agent carries on goes to that
// agent; one had in a tab's worktree that no agent carries on is carried
// on in a new pane of that tab.
func TestOpenConversation(t *testing.T) {
	m := paneModel(t)
	fakeAgent(m, agents.Codex)
	tb := m.cur()
	tb.SessionID = "11111111-1111-1111-1111-111111111111"
	m.openConversation(&agents.Conversation{Kind: agents.Claude, ID: tb.SessionID, Dir: tb.Dir})
	if len(tb.extraAgents()) != 0 {
		t.Fatal("the agent carrying it on is gone to, not started again")
	}
	id := "33333333-3333-3333-3333-333333333333"
	m.openConversation(&agents.Conversation{Kind: agents.Codex, ID: id, Dir: tb.Dir})
	if len(tb.extraAgents()) != 1 || tb.extraAgents()[0].Agent != agents.Codex || tb.extraAgents()[0].SessionID != id {
		t.Fatalf("a new pane should carry it on: %+v", tb.extraAgents())
	}
	if tb.focused().a != tb.extraAgents()[0] {
		t.Fatal("and have the keyboard")
	}

	// One had in a repository in the tab's folder is carried on there,
	// where the agent finds it, and stays there when nerdr starts again.
	sub := filepath.Join(tb.Dir, "backend")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	id = "44444444-4444-4444-4444-444444444444"
	m.openConversation(&agents.Conversation{Kind: agents.Codex, ID: id, Dir: sub})
	a := tb.extraAgents()[1]
	if a.SessionID != id || a.Dir != sub {
		t.Fatalf("carried on in %s, want %s", a.Dir, sub)
	}
	again := &tab{Tab: tb.Tab}
	again.Agents = tb.savedAgents()
	m.restoreAgents(again)
	if got := again.extraAgents()[1].Dir; got != sub {
		t.Fatalf("restored in %s, want %s", got, sub)
	}

	// A folder that is gone carries nothing on.
	m.openConversation(&agents.Conversation{Kind: agents.Codex, ID: "55555555-5555-5555-5555-555555555555", Dir: filepath.Join(tb.Dir, "gone")})
	if len(tb.extraAgents()) != 2 {
		t.Fatal("a conversation carried on in a folder that is gone")
	}
}
