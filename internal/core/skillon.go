package core

import (
	"os"
	"time"

	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/skills"
)

// SkillsCheckEvery is how long a look at the skills folders is trusted: the
// F4 screen is drawn on every frame, and the files hardly ever change.
const SkillsCheckEvery = 2 * time.Second

// SkillLook is the last look at where a tab's agent has the skills: for the
// tab and agent of Key, at At, where each of skills.All is (Found) and in
// what state, and the agent's other skills.
type SkillLook struct {
	Key    string
	At     time.Time
	Found  []string
	States []skills.State
	Others []skills.Local
}

// SkillStates is where the agent of t has each skill, and in what state,
// looked up again when the last look is old or was for another tab. A
// client's copy reads no folder: it has what core looked at last (see
// LookAtSkills), and none for another tab.
func (m *Core) SkillStates(t *Tab) ([]string, []skills.State) {
	all := skills.All()
	v := &m.SkillLook
	key := SkillKey(t)
	if key == v.Key && len(v.States) == len(all) && (m.Copy || time.Since(v.At) < SkillsCheckEvery) {
		return v.Found, v.States
	}
	if m.Copy {
		return make([]string, len(all)), make([]skills.State, len(all))
	}
	*v = readSkills(key, t)
	return v.Found, v.States
}

// SkillKey is what a look at t's skills is for: the tab, its agent and
// its folder.
func SkillKey(t *Tab) string {
	if t == nil {
		return ""
	}
	return t.ID + "/" + string(t.Agent) + "/" + t.Dir
}

// readSkills looks at where t's agent has the skills, for key.
func readSkills(key string, t *Tab) SkillLook {
	all := skills.All()
	v := SkillLook{Key: key, At: time.Now(), Found: make([]string, len(all)), States: make([]skills.State, len(all))}
	if t == nil {
		return v
	}
	home, _ := os.UserHomeDir()
	dirs := skills.Dirs(home, t.Dir, t.Agent)
	v.Others = skills.Others(dirs)
	for i, s := range all {
		file, st := s.Find(dirs)
		v.Found[i], v.States[i] = TildePath(file, home), st
	}
	return v
}

// LookAtSkills has core look again at where tab T's agent has the skills,
// away from the loop, for a client whose copy reads no folder: what it
// finds comes in SkillLook.
type LookAtSkills struct{ T TabRef }

// skillsLooked is LookAtSkills' look.
type skillsLooked struct{ look SkillLook }

func (q LookAtSkills) Do(m *Core) Cmd {
	t := m.ByRef(q.T)
	if t == nil {
		return nil
	}
	key, tab := SkillKey(t), &Tab{Tab: t.Tab}
	return func() Event { return skillsLooked{readSkills(key, tab)} }
}

func (m *Core) onSkillsLooked(ev skillsLooked) {
	if ev.look.At.After(m.SkillLook.At) || ev.look.Key != m.SkillLook.Key {
		m.SkillLook = ev.look
	}
}

// SkillOn is whether tab t's agent can use the skill called name: it is
// turned on and, with skills "off", where the agent looks for skills, as
// nerdr then puts none in place. What uses a skill is offered only then.
func (m *Core) SkillOn(t *Tab, name string) bool {
	if !m.Cfg.SkillEnabled(name) {
		return false
	}
	if m.Cfg.Install != config.InstallOff {
		return true
	}
	if t == nil {
		return false
	}
	_, states := m.SkillStates(t)
	for i, s := range skills.All() {
		if s.Name == name {
			return states[i] != skills.Missing
		}
	}
	return false
}
