package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/denied"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/skills"

	tea "github.com/charmbracelet/bubbletea"
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

// skillUse is the file: by conversation.
type skillUse struct {
	Convs map[string]*convSkills `json:"conversations"`
}

// convSkills is one conversation's use of the skills.
type convSkills struct {
	Agent string `json:"agent"`
	// Tab is the tab it was last counted in. Older entries have none, and
	// count only towards all tabs until their conversation is counted again.
	Tab string `json:"tab,omitempty"`
	// Now is what its log says now; Before what earlier conversations
	// counted under the same key said.
	Now    map[string]int       `json:"now,omitempty"`
	Before map[string]int       `json:"before,omitempty"`
	Last   map[string]time.Time `json:"last,omitempty"`
	// Log is what its log looked like when it was counted (see
	// agents.LogStamp): a log that still looks so is not read again.
	Log string `json:"log,omitempty"`
}

// skillTotal is a skill's use over every conversation.
type skillTotal struct {
	n       int
	byAgent map[string]int
	last    time.Time
}

func skillUsePath() string { return filepath.Join(filepath.Dir(state.Dir()), "skill-use.json") }

func loadSkillUse() skillUse {
	u := skillUse{Convs: map[string]*convSkills{}}
	if data, err := os.ReadFile(skillUsePath()); err == nil {
		_ = json.Unmarshal(data, &u)
	}
	if u.Convs == nil {
		u.Convs = map[string]*convSkills{}
	}
	return u
}

func (u skillUse) save() error {
	data, err := json.MarshalIndent(u, "", "  ")
	if err != nil {
		return err
	}
	p := skillUsePath()
	if err := denied.Err(os.MkdirAll(filepath.Dir(p), 0o755)); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := denied.Err(os.WriteFile(tmp, data, 0o644)); err != nil {
		return err
	}
	return denied.Err(os.Rename(tmp, p))
}

// set takes what conversation key's log in tab, looking as stamp says,
// counts now, at now, and reports whether anything changed.
func (u skillUse) set(key, agent, tab, stamp string, counts map[string]int, now time.Time) bool {
	c := u.Convs[key]
	if c == nil {
		if len(counts) == 0 && stamp == "" {
			return false
		}
		c = &convSkills{Agent: agent}
		u.Convs[key] = c
	}
	changed := stamp != c.Log || tab != c.Tab
	c.Log, c.Tab = stamp, tab
	for name, n := range c.Now {
		if counts[name] < n {
			// Fewer than before: another conversation under the same key.
			if c.Before == nil {
				c.Before = map[string]int{}
			}
			for name, n := range c.Now {
				c.Before[name] += n
			}
			c.Now = nil
			break
		}
	}
	for name, n := range counts {
		if n > c.Now[name] {
			if c.Last == nil {
				c.Last = map[string]time.Time{}
			}
			c.Last[name] = now
			changed = true
		}
	}
	if len(counts) != len(c.Now) {
		changed = true
	}
	c.Now = counts
	return changed
}

// totals sums the skills' use over the conversations of tab, or over every
// conversation with tab "".
func (u skillUse) totals(tab string) map[string]*skillTotal {
	out := map[string]*skillTotal{}
	for _, c := range u.Convs {
		if tab != "" && c.Tab != tab {
			continue
		}
		add := func(m map[string]int) {
			for name, n := range m {
				s := out[name]
				if s == nil {
					s = &skillTotal{byAgent: map[string]int{}}
					out[name] = s
				}
				s.n += n
				s.byAgent[c.Agent] += n
				if c.Last[name].After(s.last) {
					s.last = c.Last[name]
				}
			}
		}
		add(c.Before)
		add(c.Now)
	}
	return out
}

// skillUseMsg is what t's conversation log says of the skills used.
type skillUseMsg struct {
	key, agent, tab, stamp string
	counts                 map[string]int
}

// countSkills reads t's conversation log for the skills its agent used,
// in the background.
func (m *Model) countSkills(t *tab) tea.Cmd {
	if t == nil || !agents.Known(t.Agent) {
		return nil
	}
	specs, kind, id, dir, tabID := m.cfg.Agents, t.Agent, t.SessionID, t.Dir, t.owner().ID
	key := string(kind) + ":" + id
	if id == "" {
		key = string(kind) + ":dir:" + dir
	}
	return func() tea.Msg {
		// A log that has not changed since it was counted, in this tab, is
		// not read.
		stamp := agents.LogStamp(kind, id, dir)
		if c := loadSkillUse().Convs[key]; stamp != "" && c != nil && c.Log == stamp && c.Tab == tabID {
			return nil
		}
		counts, err := agents.SkillUses(specs, kind, id, dir)
		if err != nil {
			return nil
		}
		return skillUseMsg{key: key, agent: string(kind), tab: tabID, stamp: stamp, counts: counts}
	}
}

// onSkillUse keeps what a log said, for F4.
func (m *Model) onSkillUse(msg skillUseMsg) {
	u := loadSkillUse() // another nerdr may have written since
	if u.set(msg.key, msg.agent, msg.tab, msg.stamp, msg.counts, time.Now()) {
		if err := u.save(); err != nil {
			debugf("skill use: %v", err)
		}
	}
	m.skillUse = u
}

// skillTotals is the skills' use in tab t, or in every tab when F4 asks for
// all of them or no tab is open. The file is read the first time.
func (m *Model) skillTotals(t *tab) map[string]*skillTotal {
	if m.skillUse.Convs == nil {
		m.skillUse = loadSkillUse()
	}
	if m.skills.allTabs || t == nil {
		return m.skillUse.totals("")
	}
	return m.skillUse.totals(t.owner().ID)
}

// otherSkills are the skills on F4's other list, by name: those t's agent
// can find (File is set), and those used in the tab or tabs shown, as the
// agents' own built-in and plugin skills are in no folder nerdr looks in.
func (m *Model) otherSkills(t *tab, tot map[string]*skillTotal) []skills.Local {
	m.skillStates(t)
	seen := map[string]bool{}
	for _, s := range skills.All() {
		seen[s.Name] = true
	}
	var out []skills.Local
	for _, l := range m.skills.others {
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
