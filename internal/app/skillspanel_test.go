package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/skills"
)

// Disabled and missing skills must still describe the alternatives before
// the long instructions, with every line reachable in a narrow panel.
func TestSkillsAlternativesWithoutInstallation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := testModel(t)
	m.cfg.Install = config.InstallOff
	m.cfg.EnabledSkills = []string{}
	for i, sk := range skills.All() {
		t.Run(sk.Name, func(t *testing.T) {
			m.skills.cursor, m.skills.scroll = i, 0
			body := ansi.Strip(strings.Join(m.skillsBody(m.cur(), 80, 250), "\n"))
			for _, want := range []string{"Without this skill", "CLI:", "Tokens and usage", "not token costs", "Skill instructions"} {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			if strings.Index(body, "Without this skill") > strings.Index(body, "Skill instructions") {
				t.Fatal("alternatives come after the instructions")
			}
			m.skills.scroll = 0
			var pages strings.Builder
			for range 100 {
				lines := m.skillsBody(m.cur(), 42, 24)
				if len(lines) != 24 {
					t.Fatalf("panel height: %d", len(lines))
				}
				pages.WriteString(ansi.Strip(strings.Join(lines, "\n")))
				before := m.skills.scroll
				m.skillsKey(keyMsg("pgdown"))
				m.skillsBody(m.cur(), 42, 24)
				if m.skills.scroll == before {
					break
				}
			}
			for _, want := range []string{"Without this skill", "Tokens and usage", "Skill instructions"} {
				if !strings.Contains(pages.String(), want) {
					t.Errorf("cannot scroll to %q", want)
				}
			}
		})
	}
}
