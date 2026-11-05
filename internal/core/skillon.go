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
// looked up again when the last look is old or was for another tab.
func (m *Core) SkillStates(t *Tab) ([]string, []skills.State) {
	all := skills.All()
	v := &m.SkillLook
	key := ""
	if t != nil {
		key = t.ID + "/" + string(t.Agent) + "/" + t.Dir
	}
	if key == v.Key && time.Since(v.At) < SkillsCheckEvery && len(v.States) == len(all) {
		return v.Found, v.States
	}
	v.Key, v.At = key, time.Now()
	v.Found, v.States = make([]string, len(all)), make([]skills.State, len(all))
	v.Others = nil
	if t == nil {
		return v.Found, v.States
	}
	home, _ := os.UserHomeDir()
	dirs := skills.Dirs(home, t.Dir, t.Agent)
	v.Others = skills.Others(dirs)
	for i, s := range all {
		file, st := s.Find(dirs)
		v.Found[i], v.States[i] = TildePath(file, home), st
	}
	return v.Found, v.States
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
