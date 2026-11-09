package app

import (
	"sort"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/skills"
)

// How often the agents use each skill, for F4: read from each tab's
// conversation log when its agent finishes a turn (see agents.SkillUses),
// and kept across starts, for every nerdr, in skill-use.json beside the
// sessions.
//
// A log is counted whole every time, so what is kept for a conversation is
// its count, set and not added to. A tab with no conversation id is
// counted from the newest conversation in its worktree, which changes when
// the agent starts a new one: a count that drops is a new conversation,
// and the old one's is kept apart.

// skillTotals is the skills' use in tab t, or in every tab when F4 asks for
// all of them or no tab is open. The file is read the first time.
func (m *Model) skillTotals(t *core.Tab) map[string]*core.SkillTotal {
	if m.machineOf(t).Core.SkillUse.Convs == nil {
		m.do(core.ReadSkillUse{})
	}
	if m.skills.allTabs || t == nil {
		return m.machineOf(t).Core.SkillUse.Totals("")
	}
	return m.machineOf(t).Core.SkillUse.Totals(t.Owner().ID)
}

// otherSkills are the skills on F4's other list, by name: those t's agent
// can find (File is set), and those used in the tab or tabs shown, as the
// agents' own built-in and plugin skills are in no folder nerdr looks in.
func (m *Model) otherSkills(t *core.Tab, tot map[string]*core.SkillTotal) []skills.Local {
	m.lookAtSkills(t)
	seen := map[string]bool{}
	for _, s := range skills.All() {
		seen[s.Name] = true
	}
	var out []skills.Local
	for _, l := range m.machineOf(t).Core.SkillLookOf(t).Others {
		seen[l.Name] = true
		out = append(out, l)
	}
	for name := range tot {
		if !seen[name] {
			out = append(out, skills.Local{Skill: skills.Skill{Name: skills.Printable(name)}})
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// lookAtSkills has core look again at where t's agent has the skills,
// when what it has is old or for another agent: it comes in SkillLooks.
// One ask at a time a tab, every SkillsCheckEvery at most.
func (m *Model) lookAtSkills(t *core.Tab) {
	if t == nil {
		return
	}
	key := core.SkillKey(t)
	v := m.machineOf(t).Core.SkillLookOf(t)
	fresh := v.Key == key && time.Since(v.At) < core.SkillsCheckEvery
	if fresh || key == m.skillsAsked && time.Since(m.skillsAskedAt) < core.SkillsCheckEvery {
		return
	}
	m.skillsAsked, m.skillsAskedAt = key, time.Now()
	m.tellCore(core.LookAtSkills{T: m.fleet.Ref(t)})
}
