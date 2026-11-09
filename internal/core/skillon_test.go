package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/skills"
)

// TestACopyReadsNoSkillsFolder: a client's copy of core reads no folder
// for the skills, which may be on another machine: it has what core
// looked at last (LookAtSkills), however old, and nothing for a tab core
// has not looked at.
func TestACopyReadsNoSkillsFolder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	first := skills.All()[0]
	file := filepath.Join(skills.ProjectDir(dir, agents.Claude), first.Name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("---\nname: "+first.Name+"\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tb := &Tab{Tab: state.Tab{ID: "1", Agent: agents.Claude, Dir: dir}}

	cp := &Core{Copy: true}
	if found, _ := cp.SkillStates(tb); found[0] != "" {
		t.Fatalf("the copy found %q itself", found[0])
	}

	c := &Core{Tabs: []*Tab{tb}}
	c.handleAll(t, LookAtSkills{T: c.Ref(tb)}.Do(c)())
	want, _ := c.SkillStates(tb)
	if want[0] == "" {
		t.Fatal("core did not find the skill")
	}
	cp.SkillLook = c.SkillLook // as the copy is sent it
	cp.SkillLook.At = time.Now().Add(-time.Hour)
	if found, _ := cp.SkillStates(tb); found[0] != want[0] {
		t.Fatalf("the copy has %q, core found %q", found[0], want[0])
	}
}

// handleAll has c handle evs, with Mu held.
func (c *Core) handleAll(t *testing.T, evs ...Event) {
	t.Helper()
	c.Mu.Lock()
	defer c.Mu.Unlock()
	for _, ev := range evs {
		if _, ok := c.Handle(ev); !ok {
			t.Fatalf("core did not handle %T", ev)
		}
	}
}
