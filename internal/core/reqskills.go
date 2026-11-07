package core

import (
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/usage"
)

// The requests about the skills nerdr puts in place for the agents, and
// what core reads from its files only when first asked.

func init() { registerRequests(ApplySkills{}, ReadSkillUse{}, ReadClaudeLimits{}) }

// ApplySkills puts the skills turned on in place, and takes the others
// away, as the config says now; what it did is told after Say, or
// Otherwise when it did nothing.
type ApplySkills struct{ Say, Otherwise string }

func (q ApplySkills) Do(m *Core) Cmd {
	m.SkillLook.At = time.Time{}
	msg := strings.TrimSpace(q.Say + " " + m.ApplySkills())
	if msg == "" {
		msg = q.Otherwise
	}
	if msg != "" {
		m.Toast(msg)
	}
	return nil
}

// ReadSkillUse reads how much the agents have used each skill, the first
// time it is asked for.
type ReadSkillUse struct{}

func (ReadSkillUse) Do(m *Core) Cmd {
	if m.SkillUse.Convs == nil {
		m.SkillUse = LoadSkillUse()
	}
	return nil
}

// ReadClaudeLimits reads what a claude agent last said of its account's
// limits, kept from before nerdr started, the first time it is asked for.
type ReadClaudeLimits struct{}

func (ReadClaudeLimits) Do(m *Core) Cmd {
	if m.ClaudeLimits == nil {
		if s, ok := usage.LoadClaude(state.Dir()); ok {
			r := s.Report
			m.ClaudeLimits, m.ClaudeAt = &r, s.At
		}
	}
	return nil
}
