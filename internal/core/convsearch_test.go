package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
)

// TestCoreSearchesTheConversations: core reads the agents' logs and
// searches them for a client, which gets of each conversation found only
// what was said where, and whether its folder is gone.
func TestCoreSearchesTheConversations(t *testing.T) {
	codex := t.TempDir()
	t.Setenv("CODEX_HOME", codex)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	wt, gone := filepath.Join(root, "wt"), filepath.Join(root, "gone")
	if err := os.Mkdir(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	log := func(id, cwd string, said ...string) {
		t.Helper()
		body := `{"type":"session_meta","payload":{"cwd":"` + cwd + `"}}` + "\n"
		for _, s := range said {
			body += `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + s + `"}]}}` + "\n"
		}
		path := filepath.Join(codex, "sessions", "2026", "11", "09", "rollout-2026-11-09T10-00-00-"+id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	log("11111111-1111-1111-1111-111111111111", wt, "first thing", "alpha beta", "last thing")
	log("22222222-2222-2222-2222-222222222222", gone, "alpha gamma")

	c := &Core{Root: root}
	answer := func(cmd Cmd) Event {
		t.Helper()
		evs := runNow(cmd, 5*time.Second)
		if len(evs) != 1 {
			t.Fatalf("answers %v", evs)
		}
		return evs[0].(Reply).Ev
	}
	if got := answer(ReadConversations{Job: "r"}.Do(c)); got != (ConversationsReadMsg{Job: "r", N: 2}) {
		t.Fatalf("read: %#v", got)
	}
	got := answer(SearchConversations{Job: "s", Words: agents.Words("alpha")}.Do(c)).(SearchHitsMsg)
	if got.Job != "s" || len(got.Hits) != 2 || len(got.Gone) != 2 {
		t.Fatalf("search: %+v", got)
	}
	for i, h := range got.Hits {
		if len(h.Conv.Said) != 1 || h.At != 0 || h.Snippet([]string{"alpha"}, 40) == "" {
			t.Errorf("hit %d carries %d things said, at %d", i, len(h.Conv.Said), h.At)
		}
		if got.Gone[i] != (h.Conv.Dir == gone) {
			t.Errorf("hit in %s: gone %v", h.Conv.Dir, got.Gone[i])
		}
	}
}
