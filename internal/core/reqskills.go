package core

import (
	"os"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/usage"
)

// The requests about the skills nerdr puts in place for the agents, and
// what core reads from its files only when first asked.

func init() { registerRequests(ApplySkills{}, ReadSkillUse{}, ReadClaudeLimits{}, LookAtSkills{}) }

// ApplySkills puts the skills turned on in place, and takes the others
// away, as the config says now; what it did is told after Say, or
// Otherwise when it did nothing.
type ApplySkills struct{ Say, Otherwise string }

func (q ApplySkills) Do(m *Core) Cmd {
	m.SkillLook.At = time.Time{}
	codex := false
	note := m.applySkills(func(bool) (bool, error) { codex = true; return false, nil })
	if !codex {
		q.tell(m, note)
		return nil
	}
	// codex is asked away from Mu: it runs it.
	ask := m.AskCodexTrust(true)
	return func() Event {
		trusted, wrote, err := ask()
		return SkillsAppliedMsg{Q: q, Note: note, Trusted: trusted, Wrote: wrote, Err: err}
	}
}

// tell says what applying the skills did.
func (q ApplySkills) tell(m *Core, note string) {
	msg := strings.TrimSpace(q.Say + " " + note)
	if msg == "" {
		msg = q.Otherwise
	}
	if msg != "" {
		m.Toast(msg)
	}
}

// SkillsAppliedMsg is codex's answer, asked by ApplySkills: whether it
// trusts nerdr's hooks, and whether it wrote its config to. Note is what
// ApplySkills did before.
type SkillsAppliedMsg struct {
	Q              ApplySkills
	Note           string
	Trusted, Wrote bool
	Err            error
}

// OnSkillsApplied takes codex's answer, and says what was done.
func (m *Core) OnSkillsApplied(ev SkillsAppliedMsg) {
	m.SetCodexTrust(ev.Trusted)
	if ev.Err != nil {
		Debugf("codex hooks: %v", ev.Err)
	}
	note := ev.Note
	if ev.Wrote {
		home, _ := os.UserHomeDir()
		codex := TildePath(CodexConfig(home), home) + " (trusts nerdr's codex hooks)"
		if note == "" {
			note = "skills installed in " + codex
		} else {
			note += ", " + codex
		}
	}
	ev.Q.tell(m, note)
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
