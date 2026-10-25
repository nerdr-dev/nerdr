package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSearchConversations: the conversations had under the roots are
// found by words said in them, by the user or the agent, not in tools'
// output; those had elsewhere are not.
func TestSearchConversations(t *testing.T) {
	home, codex := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	t.Setenv("CODEX_HOME", codex)
	root := t.TempDir()
	wt := filepath.Join(root, "worktrees", "parser")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(claudeProject(wt), "11111111-1111-1111-1111-111111111111.jsonl"),
		`{"type":"user","cwd":"`+wt+`","message":{"content":"Fix the parser for nil input"}}`+"\n"+
			`{"type":"assistant","cwd":"`+wt+`","message":{"content":[{"type":"text","text":"The Parser now returns an error."},{"type":"tool_use","name":"Bash","input":{"command":"grep secretword"}}]}}`+"\n")
	elsewhere := t.TempDir()
	write(filepath.Join(claudeProject(elsewhere), "22222222-2222-2222-2222-222222222222.jsonl"),
		`{"type":"user","cwd":"`+elsewhere+`","message":{"content":"the parser elsewhere"}}`+"\n")
	write(filepath.Join(codex, "sessions", "2026", "10", "17", "rollout-2026-10-17T10-00-00-33333333-3333-3333-3333-333333333333.jsonl"),
		`{"type":"session_meta","payload":{"cwd":"`+wt+`"}}`+"\n"+
			`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"rate limits for codex"}]}}`+"\n")

	var x Index
	x.Update([]string{root})
	if x.Len() != 2 {
		t.Fatalf("conversations: %d, want the two under the root", x.Len())
	}
	hits := x.Search(Words("PARSER error"), 10)
	if len(hits) != 1 || hits[0].Conv.Kind != Claude || hits[0].Conv.Dir != wt || hits[0].Conv.ID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("hits %+v", hits)
	}
	if s := hits[0].Snippet(Words("parser"), 40); !strings.Contains(s, "Parser") {
		t.Fatalf("snippet %q", s)
	}
	if hits := x.Search(Words("secretword"), 10); len(hits) != 0 {
		t.Fatal("what tools were given is not searched")
	}
	if hits := x.Search(Words("rate limits"), 10); len(hits) != 1 || hits[0].Conv.Kind != Codex {
		t.Fatalf("codex: %+v", hits)
	}

	// A codex log had elsewhere is not opened again while it is unchanged:
	// one rewritten to look as if had under the root, its size and time
	// kept, stays left out.
	log := filepath.Join(codex, "sessions", "2026", "10", "18", "rollout-2026-10-18T10-00-00-44444444-4444-4444-4444-444444444444.jsonl")
	body := func(dir string) string {
		return `{"type":"session_meta","payload":{"cwd":"` + dir + `"}}` + "\n" +
			`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"from afar"}]}}` + "\n"
	}
	write(log, body(root+"Z/w/p")) // not under root
	x.Update([]string{root})
	st, err := os.Stat(log)
	if err != nil {
		t.Fatal(err)
	}
	write(log, body(root+"/w/pp")) // as long, under root
	if err := os.Chtimes(log, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	x.Update([]string{root})
	if x.Len() != 2 {
		t.Fatal("an unchanged log had elsewhere was read again")
	}
	if err := os.Chtimes(log, st.ModTime().Add(time.Second), st.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	x.Update([]string{root})
	if x.Len() != 3 {
		t.Fatal("a changed log was not read again")
	}
}
