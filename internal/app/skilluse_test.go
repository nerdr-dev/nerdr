package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// A conversation's count is set, not added to; one that drops is a new
// conversation under the same key, and the old one's count is kept.
func TestSkillUseCounts(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	u := loadSkillUse()
	at := time.Now()
	u.set("claude:a", "claude", "", map[string]int{"nerdr-request-review": 2}, at)
	u.set("claude:a", "claude", "", map[string]int{"nerdr-request-review": 2}, at.Add(time.Hour))
	u.set("codex:dir:/w", "codex", "", map[string]int{"nerdr-request-review": 3, "nerdr-send": 1}, at)
	u.set("codex:dir:/w", "codex", "", map[string]int{"nerdr-request-review": 1}, at.Add(time.Minute)) // a new conversation
	if err := u.save(); err != nil {
		t.Fatal(err)
	}
	tot := loadSkillUse().totals()
	try := tot["nerdr-request-review"]
	if try == nil || try.n != 6 || try.byAgent["claude"] != 2 || try.byAgent["codex"] != 4 {
		t.Fatalf("nerdr-request-review: %+v", try)
	}
	if !try.last.Equal(at.Add(time.Minute)) {
		t.Errorf("last used %v", try.last)
	}
	if tot["nerdr-send"] == nil || tot["nerdr-send"].n != 1 {
		t.Errorf("nerdr-send from the conversation before: %+v", tot["nerdr-send"])
	}
	if got := skillUseLine(try, at.Add(2*time.Minute)); got == "" {
		t.Error("no line")
	}
}

// A log that has not changed since it was counted is not read again; one
// that has is.
func TestUnchangedLogIsNotCountedAgain(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	m := testModel(t)
	tb := &tab{Tab: state.Tab{ID: "x", Agent: agents.Claude, SessionID: "s1", Dir: t.TempDir()}}
	logDir := filepath.Join(cfg, "projects", "p")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(logDir, "s1.jsonl")
	use := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Skill","input":{"skill":"nerdr-request-review"}}]}}` + "\n"
	if err := os.WriteFile(log, []byte(use), 0o644); err != nil {
		t.Fatal(err)
	}
	msg, ok := m.countSkills(tb)().(skillUseMsg)
	if !ok || msg.counts["nerdr-request-review"] != 1 {
		t.Fatalf("the first count: %#v", msg)
	}
	m.onSkillUse(msg)
	if again := m.countSkills(tb)(); again != nil {
		t.Fatalf("an unchanged log was read again: %#v", again)
	}
	later := time.Now().Add(time.Second)
	if err := os.WriteFile(log, []byte(use+use), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(log, later, later)
	msg, ok = m.countSkills(tb)().(skillUseMsg)
	if !ok || msg.counts["nerdr-request-review"] != 2 {
		t.Fatalf("a changed log should be read again: %#v", msg)
	}
	m.onSkillUse(msg)
	if got := m.skillTotals()["nerdr-request-review"]; got == nil || got.n != 2 {
		t.Fatalf("total %+v", got)
	}
}
