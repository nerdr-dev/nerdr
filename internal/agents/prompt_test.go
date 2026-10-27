package agents

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFirstPrompt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	proj := filepath.Join(dir, "projects", "-home-x-repo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	log := `{"type":"mode","mode":"normal"}
{"type":"user","isMeta":true,"message":{"role":"user","content":"Caveat: added by Claude"}}
{"type":"user","message":{"role":"user","content":"<command-name>/model</command-name>"}}
{"type":"user","isSidechain":true,"message":{"role":"user","content":"a subagent's task"}}
{"type":"user","message":{"role":"user","content":[{"type":"text","text":"  fix the login bug\nand add a test  "}]}}
{"type":"user","timestamp":"2026-10-06T19:40:00Z","message":{"role":"user","content":"a later message"}}
{"type":"user","timestamp":"2026-10-06T19:41:00Z","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}
{"type":"system","subtype":"away_summary","timestamp":"2026-10-06T19:43:40Z","content":"You fixed the login bug; nothing is committed. (disable recaps in /config)"}
{"type":"system","subtype":"away_summary","isSidechain":true,"content":"a subagent's recap"}
`
	if err := os.WriteFile(filepath.Join(proj, "abc.jsonl"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := FirstPrompt(Claude, "abc"); got != "fix the login bug\nand add a test" {
		t.Fatalf("FirstPrompt = %q", got)
	}
	l := LatestOf(Claude, "abc")
	if l.Prompt != "a later message" || l.PromptAt.Minute() != 40 {
		t.Fatalf("last prompt %q at %v", l.Prompt, l.PromptAt)
	}
	if l.Recap != "You fixed the login bug; nothing is committed." || l.RecapAt.Minute() != 43 {
		t.Fatalf("recap %q at %v", l.Recap, l.RecapAt)
	}
	ps := PromptsOf(Claude, "abc", 10)
	if len(ps) != 2 || ps[0].Text != "fix the login bug\nand add a test" || ps[1].Text != "a later message" || ps[1].At.Minute() != 40 {
		t.Fatalf("prompts %+v", ps)
	}
	if ps := PromptsOf(Claude, "abc", 1); len(ps) != 1 || ps[0].Text != "a later message" {
		t.Fatalf("the last prompt alone %+v", ps)
	}
	if got := FirstPrompt(Claude, "missing"); got != "" {
		t.Fatalf("a missing log gives %q", got)
	}
	if got := FirstPrompt(OpenCode, "abc"); got != "" {
		t.Fatalf("opencode gives %q", got)
	}
}
