package core

import (
	"os"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/skills"
)

// Where each agent has the skills, read away from the loop and kept in
// SkillLooks, by the agent's tab id: what SkillOn, and AgentNote and
// WaitsForReady by it, go by, in core and in a client's copy alike, as
// the folders may be on another machine than the client, and core reads
// no disk under Mu. Core reads them for every agent every SkillsEvery
// while nerdr puts no skills in place (install "off"), as SkillOn then
// asks where the agent looks; for an agent about to start, before it
// starts (Spawn), so its first note is right; and for the tab F4 shows,
// when the client asks (LookAtSkills).

// SkillsCheckEvery is how long a look at the skills folders is trusted: the
// F4 screen is drawn on every frame, and the files hardly ever change. An
// agent starting is looked at again when its look is older.
const SkillsCheckEvery = 2 * time.Second

// SkillsEvery is how often every agent's skills are looked at again while
// nerdr puts none in place: one the user put in place meanwhile is then
// offered.
const SkillsEvery = 30 * time.Second

// SkillLook is a look at where a tab's agent has the skills: for the tab
// and agent of Key, at At, where each of skills.All is (Found) and in what
// state, and the agent's other skills.
type SkillLook struct {
	Key    string
	At     time.Time
	Found  []string
	States []skills.State
	Others []skills.Local
}

// SkillLookOf is the last look at where t's agent has the skills, the
// zero look when there is none for it as it is now. It reads no folder.
func (m *Core) SkillLookOf(t *Tab) SkillLook {
	if t == nil {
		return SkillLook{}
	}
	v, ok := m.SkillLooks[t.ID]
	if !ok || v.Key != SkillKey(t) || len(v.States) != len(skills.All()) {
		return SkillLook{}
	}
	return v
}

// SkillStates is where the agent of t has each skill, and in what state,
// as core looked last; every skill missing when it has not looked at the
// agent as it is now. It reads no folder.
func (m *Core) SkillStates(t *Tab) ([]string, []skills.State) {
	if v := m.SkillLookOf(t); v.Key != "" {
		return v.Found, v.States
	}
	all := skills.All()
	return make([]string, len(all)), make([]skills.State, len(all))
}

// SkillKey is what a look at t's skills is for: the tab, its agent and
// its folder.
func SkillKey(t *Tab) string {
	if t == nil {
		return ""
	}
	return t.ID + "/" + string(t.Agent) + "/" + t.Dir
}

// skillsAsk is what reading where an agent has the skills needs, taken
// under Mu to read with it let go.
type skillsAsk struct {
	id, key, dir string
	agent        agents.Kind
}

func skillsAskOf(t *Tab) skillsAsk {
	return skillsAsk{id: t.ID, key: SkillKey(t), dir: t.Dir, agent: t.Agent}
}

// read looks at where the agent has the skills. Without Mu.
func (q skillsAsk) read() SkillLook {
	all := skills.All()
	v := SkillLook{Key: q.key, At: time.Now(), Found: make([]string, len(all)), States: make([]skills.State, len(all))}
	home, _ := os.UserHomeDir()
	dirs := skills.Dirs(home, q.dir, q.agent)
	v.Others = skills.Others(dirs)
	for i, s := range all {
		file, st := s.Find(dirs)
		v.Found[i], v.States[i] = TildePath(file, home), st
	}
	return v
}

// LookAtSkills has core look again at where tab T's agent has the skills,
// away from the loop: what it finds comes in SkillLooks.
type LookAtSkills struct{ T TabRef }

// skillsLooked is looks at where agents have the skills, by tab id; all
// is set when they are every agent's, and the looks of the others go.
type skillsLooked struct {
	looks map[string]SkillLook
	all   bool
}

func (q LookAtSkills) Do(m *Core) Cmd {
	t := m.ByRef(q.T)
	if t == nil {
		return nil
	}
	ask := skillsAskOf(t)
	return func() Event { return skillsLooked{looks: map[string]SkillLook{ask.id: ask.read()}} }
}

// LookAtSkillsSoon has every agent's skills looked at again at the next
// InfoTick: where they are put changed.
func (m *Core) LookAtSkillsSoon() {
	m.skillsSoon = true
	for id, v := range m.SkillLooks {
		v.At = time.Time{}
		m.SkillLooks[id] = v
	}
}

// lookAtSkills looks at where every agent has the skills away from the
// loop, when it is time to and nerdr puts none in place; see SkillsEvery.
func (m *Core) lookAtSkills(now time.Time) Cmd {
	if m.Cfg.Install != config.InstallOff {
		return nil
	}
	if !m.skillsSoon && !m.skillsAt.IsZero() && now.Sub(m.skillsAt) < SkillsEvery {
		return nil
	}
	m.skillsSoon, m.skillsAt = false, now
	var asks []skillsAsk
	for _, a := range m.AllAgents() {
		asks = append(asks, skillsAskOf(a))
	}
	return func() Event {
		looks := map[string]SkillLook{}
		for _, q := range asks {
			looks[q.id] = q.read()
		}
		return skillsLooked{looks: looks, all: true}
	}
}

func (m *Core) onSkillsLooked(ev skillsLooked) {
	if ev.all {
		for id := range m.SkillLooks {
			if _, ok := ev.looks[id]; !ok {
				delete(m.SkillLooks, id)
			}
		}
	}
	for id, look := range ev.looks {
		m.keepSkillLook(id, look)
	}
}

// keepSkillLook keeps look for the agent of tab id, unless the one kept
// for the same is newer.
func (m *Core) keepSkillLook(id string, look SkillLook) {
	if old, ok := m.SkillLooks[id]; ok && old.Key == look.Key && !look.At.After(old.At) {
		return
	}
	if m.SkillLooks == nil {
		m.SkillLooks = map[string]SkillLook{}
	}
	m.SkillLooks[id] = look
}

// skillsBeforeStart is how to look at where t's agent has the skills
// before it starts, with Mu let go, so that its note says what it can
// use: nil when SkillOn does not ask, as nerdr puts the skills in place,
// or the last look is fresh.
func (m *Core) skillsBeforeStart(t *Tab) func() SkillLook {
	if m.Cfg.Install != config.InstallOff {
		return nil
	}
	if v := m.SkillLookOf(t); v.Key != "" && time.Since(v.At) < SkillsCheckEvery {
		return nil
	}
	return skillsAskOf(t).read
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
