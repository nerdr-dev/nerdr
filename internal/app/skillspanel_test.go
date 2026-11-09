package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/skills"
)

// Disabled and missing skills must still describe the alternatives before
// the long instructions, with every line reachable in a narrow panel.
func TestSkillsAlternativesWithoutInstallation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := testModel(t)
	m.home().Core.Cfg.Install = config.InstallOff
	m.home().Core.Cfg.EnabledSkills = []string{}
	for i, sk := range skills.All() {
		t.Run(sk.Name, func(t *testing.T) {
			m.skills.cursor, m.skills.scroll = i, 0
			body := ansi.Strip(strings.Join(m.skillsBody(m.Cur(), 80, 250), "\n"))
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
				lines := m.skillsBody(m.Cur(), 42, 24)
				if len(lines) != 24 {
					t.Fatalf("panel height: %d", len(lines))
				}
				pages.WriteString(ansi.Strip(strings.Join(lines, "\n")))
				before := m.skills.scroll
				m.skillsKey(keyMsg("pgdown"))
				m.skillsBody(m.Cur(), 42, 24)
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

// The other list has the agent's skills and those used in the tab, by name;
// a skill's use is the tab's until a asks for all tabs. tab goes between
// the lists, and space leaves the other skills as they are.
func TestSkillsOtherListAndScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := filepath.Join(home, ".claude", "skills", "Zebra")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "---\nname: Zebra\ndescription: >-\n  Stripes\n  \x1b[31mand more\n---\nThe body.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	m := testModel(t)
	cur := m.Cur()
	u := core.LoadSkillUse()
	now := time.Now()
	u.Set("claude:here", "claude", cur.ID, "", map[string]int{"alpha": 1}, now)
	u.Set("claude:there", "claude", "elsewhere", "", map[string]int{"alpha": 2, "beta": 4, "nerdr-send": 3}, now)
	if err := u.Save(); err != nil {
		t.Fatal(err)
	}
	draw := func() string { return ansi.Strip(strings.Join(m.skillsBody(cur, 90, 40), "\n")) }

	if body := draw(); !strings.Contains(body, "use in this tab") || strings.Contains(body, "nerdr-send  3×") {
		t.Fatalf("the tab's use by default:\n%s", body)
	}
	m.skillsKey(keyMsg("a"))
	if body := draw(); !strings.Contains(body, "use in all tabs") || !strings.Contains(body, "nerdr-send  3×") {
		t.Fatalf("a counts all tabs:\n%s", body)
	}
	m.skillsKey(keyMsg("a"))

	m.skillsKey(keyMsg("tab"))
	body := draw()
	if !strings.Contains(body, "other 2") || strings.Contains(body, "beta") {
		t.Fatalf("Zebra and alpha in this tab:\n%s", body)
	}
	if a, z := strings.Index(body, "alpha  1×"), strings.Index(body, "Zebra"); a < 0 || z < a {
		t.Fatalf("not by name:\n%s", body)
	}
	m.skillsKey(keyMsg("j"))
	body = draw()
	if !strings.Contains(body, "~/.claude/skills/Zebra/SKILL.md") || !strings.Contains(body, "Stripes and more") || !strings.Contains(body, "The body.") {
		t.Fatalf("the skill picked:\n%s", body)
	}
	before := m.home().Core.Cfg.EnabledSkills
	m.skillsKey(keyMsg(" "))
	if len(m.home().Core.Cfg.EnabledSkills) != len(before) {
		t.Fatal("space changed nerdr's skills from the other list")
	}
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		t.Fatalf("space touched the skill: %v", err)
	}

	m.skillsKey(keyMsg("shift+tab"))
	if !strings.Contains(draw(), "nerdr-explain") {
		t.Fatal("shift+tab did not go back")
	}
}

// The context list has what the agent reads, shows a file's text and only an
// MCP server's name, and space does nothing there.
func TestSkillsContextList(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := testModel(t)
	cur := m.Cur()
	cur.Dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(cur.Dir, "CLAUDE.md"), []byte("Use tabs.\x1b[31m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cur.Dir, ".mcp.json"), []byte(`{"mcpServers":{"docs":{"env":{"TOKEN":"hunter2"}}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m.skills.list = listContext
	draw := func() string { return ansi.Strip(strings.Join(m.skillsBody(cur, 90, 50), "\n")) }
	body := draw()
	for _, want := range []string{"Instructions", "CLAUDE.md", "Use tabs.", "MCP servers", "docs", "context "} {
		if !strings.Contains(body, want) {
			t.Fatalf("lacks %q:\n%s", want, body)
		}
	}
	m.skillsKey(keyMsg("j"))
	body = draw()
	if strings.Contains(body, "hunter2") {
		t.Fatalf("an MCP server's settings shown:\n%s", body)
	}
	before := len(m.home().Core.Cfg.EnabledSkills)
	m.skillsKey(keyMsg(" "))
	if len(m.home().Core.Cfg.EnabledSkills) != before {
		t.Fatal("space changed skills from the context list")
	}
}

// A long list stays on the screen around the row picked, with its text below.
func TestSkillsLongListKeepsThePickVisible(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for i := range 40 {
		dir := filepath.Join(home, ".claude", "skills", fmt.Sprintf("skill-%02d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		text := fmt.Sprintf("---\ndescription: About number %02d\n---\nBody\n", i)
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := testModel(t)
	m.setFocus(core.FocusReview)
	m.skills.list = listOther
	for range 30 {
		m.skillsKey(keyMsg("j"))
	}
	body := ansi.Strip(strings.Join(m.skillsBody(m.Cur(), 80, 30), "\n"))
	for _, want := range []string{"❯ skill-30", "About number 30"} {
		if !strings.Contains(body, want) {
			t.Fatalf("lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "skill-00") {
		t.Fatalf("the list was not cut:\n%s", body)
	}
}
