package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/denied"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// SkillUse is the file: by conversation.
type SkillUse struct {
	Convs map[string]*ConvSkills `json:"conversations"`
}

// ConvSkills is one conversation's use of the skills.
type ConvSkills struct {
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

// SkillTotal is a skill's use over every conversation.
type SkillTotal struct {
	N       int
	ByAgent map[string]int
	Last    time.Time
}

func SkillUsePath() string { return filepath.Join(filepath.Dir(state.Dir()), "skill-use.json") }

func LoadSkillUse() SkillUse {
	u := SkillUse{Convs: map[string]*ConvSkills{}}
	if data, err := os.ReadFile(SkillUsePath()); err == nil {
		_ = json.Unmarshal(data, &u)
	}
	if u.Convs == nil {
		u.Convs = map[string]*ConvSkills{}
	}
	return u
}

func (u SkillUse) Save() error {
	data, err := json.MarshalIndent(u, "", "  ")
	if err != nil {
		return err
	}
	p := SkillUsePath()
	if err := denied.Err(os.MkdirAll(filepath.Dir(p), 0o755)); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := denied.Err(os.WriteFile(tmp, data, 0o644)); err != nil {
		return err
	}
	return denied.Err(os.Rename(tmp, p))
}

// Set takes what conversation key's log in tab, looking as stamp says,
// counts now, at now, and reports whether anything changed.
func (u SkillUse) Set(key, agent, tab, stamp string, counts map[string]int, now time.Time) bool {
	c := u.Convs[key]
	if c == nil {
		if len(counts) == 0 && stamp == "" {
			return false
		}
		c = &ConvSkills{Agent: agent}
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

// Totals sums the skills' use over the conversations of tab, or over every
// conversation with tab "".
func (u SkillUse) Totals(tab string) map[string]*SkillTotal {
	out := map[string]*SkillTotal{}
	for _, c := range u.Convs {
		if tab != "" && c.Tab != tab {
			continue
		}
		add := func(m map[string]int) {
			for name, n := range m {
				s := out[name]
				if s == nil {
					s = &SkillTotal{ByAgent: map[string]int{}}
					out[name] = s
				}
				s.N += n
				s.ByAgent[c.Agent] += n
				if c.Last[name].After(s.Last) {
					s.Last = c.Last[name]
				}
			}
		}
		add(c.Before)
		add(c.Now)
	}
	return out
}

// SkillUseMsg is what t's conversation log says of the skills used.
type SkillUseMsg struct {
	Key, Agent, Tab, Stamp string
	Counts                 map[string]int
}

// CountSkills reads t's conversation log for the skills its agent used,
// in the background.
func (m *Core) CountSkills(t *Tab) Cmd {
	if t == nil || !agents.Known(t.Agent) {
		return nil
	}
	specs, kind, id, dir, tabID := m.Cfg.Agents, t.Agent, t.SessionID, t.Dir, t.Owner().ID
	key := string(kind) + ":" + id
	if id == "" {
		key = string(kind) + ":dir:" + dir
	}
	return func() Event {
		// A log that has not changed since it was counted, in this tab, is
		// not read.
		stamp := agents.LogStamp(kind, id, dir)
		if c := LoadSkillUse().Convs[key]; stamp != "" && c != nil && c.Log == stamp && c.Tab == tabID {
			return nil
		}
		counts, err := agents.SkillUses(specs, kind, id, dir)
		if err != nil {
			return nil
		}
		return SkillUseMsg{Key: key, Agent: string(kind), Tab: tabID, Stamp: stamp, Counts: counts}
	}
}

// onSkillUse keeps what a log said, for F4.
func (m *Core) OnSkillUse(msg SkillUseMsg) {
	u := LoadSkillUse() // another nerdr may have written since
	if u.Set(msg.Key, msg.Agent, msg.Tab, msg.Stamp, msg.Counts, time.Now()) {
		if err := u.Save(); err != nil {
			Debugf("skill use: %v", err)
		}
	}
	m.SkillUse = u
}
