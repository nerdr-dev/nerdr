package core

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/skills"
)

// skillsIn puts nerdr's skills where claude looks in dir.
func skillsIn(t *testing.T, dir string) {
	t.Helper()
	if _, err := skills.Install(skills.ProjectDir(dir, agents.Claude)); err != nil {
		t.Fatal(err)
	}
}

// TestSkillStatesReadNoFolder: neither core, which reads no disk under
// Mu, nor a client's copy, whose folders may be on another machine,
// reads the skills folders for SkillStates: they have what core looked
// at last away from Mu (LookAtSkills), however old, and nothing for an
// agent core has not looked at.
func TestSkillStatesReadNoFolder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	skillsIn(t, dir)
	tb := &Tab{Tab: state.Tab{ID: "1", Agent: agents.Claude, Dir: dir}}

	c := &Core{Tabs: []*Tab{tb}}
	cp := &Core{Copy: true}
	for _, m := range []*Core{c, cp} {
		if found, _ := m.SkillStates(tb); found[0] != "" {
			t.Fatalf("copy %v found %q itself", m.Copy, found[0])
		}
	}

	c.handleAll(t, LookAtSkills{T: c.Ref(tb)}.Do(c)())
	want, _ := c.SkillStates(tb)
	if want[0] == "" {
		t.Fatal("core did not find the skill")
	}
	cp.SkillLooks = c.SkillLooks // as the copy is sent it
	v := cp.SkillLooks[tb.ID]
	v.At = time.Now().Add(-time.Hour)
	cp.SkillLooks[tb.ID] = v
	if found, _ := cp.SkillStates(tb); found[0] != want[0] {
		t.Fatalf("the copy has %q, core found %q", found[0], want[0])
	}

	// Another agent in the tab is another look.
	tb.Agent = agents.Codex
	if found, _ := c.SkillStates(tb); found[0] != "" {
		t.Fatalf("codex has claude's look: %q", found[0])
	}
}

// TestAgentStartsWithItsSkillsLooked: with skills "off", an agent is told
// of the skills it finds where it looks; core looks there before it
// starts, away from Mu, so its first note is right.
func TestAgentStartsWithItsSkillsLooked(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	skillsIn(t, dir)
	rec := &recorder{}
	m := &Core{Client: NoClient{}, Spawner: rec}
	m.Cfg.Install = config.InstallOff
	m.Cfg.Agents = map[agents.Kind]agents.Spec{agents.Claude: {Command: []string{"cat"}}}
	tab := &Tab{Tab: state.Tab{ID: "1", Agent: agents.Claude, Dir: dir}}
	m.Tabs = []*Tab{tab}

	cmd := m.Spawn(tab, agents.Launch{Argv: []string{"cat"}}, false)
	if len(rec.specs) != 0 || cmd == nil {
		t.Fatalf("started before its skills were looked at: %d", len(rec.specs))
	}
	ev, ok := cmd().(SpawnLooked)
	if !ok {
		t.Fatalf("the Cmd gave %T", ev)
	}
	m.OnSpawnLooked(ev)
	if tab.Pane == nil || len(rec.specs) != 1 {
		t.Fatalf("claude did not start: %q", tab.Err)
	}
	defer tab.Pane.Close()
	if argv := rec.specs[0].Argv; !slices.Contains(argv, "--append-system-prompt") || !strings.Contains(strings.Join(argv, " "), "nerdr-spinoff") {
		t.Errorf("claude was not told of the skill it has: %q", argv)
	}

	// Looked at a moment ago: the next starts at once.
	next := &Tab{Tab: state.Tab{ID: "2", Agent: agents.Claude, Dir: dir}}
	m.Tabs = append(m.Tabs, next)
	m.SkillLooks[next.ID] = SkillLook{Key: SkillKey(next), At: time.Now(), Found: m.SkillLooks[tab.ID].Found, States: m.SkillLooks[tab.ID].States}
	if m.Spawn(next, agents.Launch{Argv: []string{"cat"}}, false); next.Pane == nil {
		t.Fatalf("looked at, claude did not start at once: %q", next.Err)
	}
	next.Pane.Close()

	// Skills nerdr puts in place are not looked for.
	m.Cfg.Install = config.InstallHome
	third := &Tab{Tab: state.Tab{ID: "3", Agent: agents.Claude, Dir: dir}}
	m.Tabs = append(m.Tabs, third)
	if m.Spawn(third, agents.Launch{Argv: []string{"cat"}}, false); third.Pane == nil {
		t.Fatalf("with skills put in place, claude did not start at once: %q", third.Err)
	}
	third.Pane.Close()

	// Closed while its skills were looked at: not started.
	m.Cfg.Install = config.InstallOff
	gone := &Tab{Tab: state.Tab{ID: "4", Agent: agents.Claude, Dir: dir}}
	m.Tabs = append(m.Tabs, gone)
	cmd = m.Spawn(gone, agents.Launch{Argv: []string{"cat"}}, false)
	m.Tabs = m.Tabs[:len(m.Tabs)-1]
	if m.OnSpawnLooked(cmd().(SpawnLooked)); gone.Pane != nil {
		gone.Pane.Close()
		t.Fatal("a closed tab's agent started")
	}
}

// TestCoreLooksAtEveryAgentsSkills: with skills "off", core looks at
// where every agent has the skills every SkillsEvery, away from Mu, and
// forgets the agents gone; a skill the user put in place meanwhile is
// then offered.
func TestCoreLooksAtEveryAgentsSkills(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	tab := &Tab{Tab: state.Tab{ID: "1", Agent: agents.Claude, Dir: dir}}
	m := &Core{Client: NoClient{}, Tabs: []*Tab{tab}}
	m.Cfg.Install = config.InstallHome
	now := time.Now()
	if m.lookAtSkills(now) != nil {
		t.Fatal("looked, with the skills put in place by nerdr")
	}
	m.Cfg.Install = config.InstallOff
	m.SkillLooks = map[string]SkillLook{"gone": {Key: "gone"}}
	m.handleAll(t, m.lookAtSkills(now)())
	if m.SkillOn(tab, "nerdr-spinoff") {
		t.Fatal("a skill not in place is on")
	}
	if _, ok := m.SkillLooks["gone"]; ok {
		t.Error("the look of an agent gone is kept")
	}
	skillsIn(t, dir)
	if m.lookAtSkills(now.Add(time.Second)) != nil {
		t.Fatal("looked again before SkillsEvery")
	}
	m.handleAll(t, m.lookAtSkills(now.Add(SkillsEvery))())
	if !m.SkillOn(tab, "nerdr-spinoff") {
		t.Fatal("a skill put in place is not on")
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
