package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// A conversation's count is set, not added to; one that drops is a new
// conversation under the same key, and the old one's count is kept.
func TestSkillUseCounts(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	u := core.LoadSkillUse()
	at := time.Now()
	u.Set("claude:a", "claude", "t1", "", map[string]int{"nerdr-request-review": 2}, at)
	u.Set("claude:a", "claude", "t1", "", map[string]int{"nerdr-request-review": 2}, at.Add(time.Hour))
	u.Set("codex:dir:/w", "codex", "t2", "", map[string]int{"nerdr-request-review": 3, "nerdr-send": 1}, at)
	u.Set("codex:dir:/w", "codex", "t2", "", map[string]int{"nerdr-request-review": 1}, at.Add(time.Minute)) // a new conversation
	if err := u.Save(); err != nil {
		t.Fatal(err)
	}
	tot := core.LoadSkillUse().Totals("")
	try := tot["nerdr-request-review"]
	if try == nil || try.N != 6 || try.ByAgent["claude"] != 2 || try.ByAgent["codex"] != 4 {
		t.Fatalf("nerdr-request-review: %+v", try)
	}
	if !try.Last.Equal(at.Add(time.Minute)) {
		t.Errorf("last used %v", try.Last)
	}
	if tot["nerdr-send"] == nil || tot["nerdr-send"].N != 1 {
		t.Errorf("nerdr-send from the conversation before: %+v", tot["nerdr-send"])
	}
	inTab := core.LoadSkillUse().Totals("t1")
	if got := inTab["nerdr-request-review"]; got == nil || got.N != 2 || inTab["nerdr-send"] != nil {
		t.Errorf("in tab t1: %+v", inTab)
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
	tb := &core.Tab{AgentState: core.Agent{Tab: state.Tab{ID: "x", Agent: agents.Claude, SessionID: "s1", Dir: t.TempDir()}}}
	logDir := filepath.Join(cfg, "projects", "p")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(logDir, "s1.jsonl")
	use := `{"type":"assistant","cwd":` + `"` + tb.Dir + `"` + `,"message":{"content":[{"type":"tool_use","name":"Skill","input":{"skill":"nerdr-request-review"}}]}}` + "\n"
	if err := os.WriteFile(log, []byte(use), 0o644); err != nil {
		t.Fatal(err)
	}
	msg, ok := m.Core.CountSkills(tb)().(core.SkillUseMsg)
	if !ok || msg.Counts["nerdr-request-review"] != 1 {
		t.Fatalf("the first count: %#v", msg)
	}
	m.Core.OnSkillUse(msg)
	if again := m.Core.CountSkills(tb)(); again != nil {
		t.Fatalf("an unchanged log was read again: %#v", again)
	}
	later := time.Now().Add(time.Second)
	if err := os.WriteFile(log, []byte(use+use), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(log, later, later)
	msg, ok = m.Core.CountSkills(tb)().(core.SkillUseMsg)
	if !ok || msg.Counts["nerdr-request-review"] != 2 {
		t.Fatalf("a changed log should be read again: %#v", msg)
	}
	m.Core.OnSkillUse(msg)
	if got := m.skillTotals(tb)["nerdr-request-review"]; got == nil || got.N != 2 {
		t.Fatalf("total %+v", got)
	}
}
