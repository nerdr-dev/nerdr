package agents

import (
	"path/filepath"
	"strings"
	"testing"
)

// Each agent's way of using a skill is counted, and nerdr's own skills
// read in a repository, to work on them, are not.
func TestSkillOfTool(t *testing.T) {
	for line, want := range map[string]string{
		`Skill: {"skill":"nerdr-request-review"}`:                                                      "nerdr-request-review",
		`Skill: {"skill":"anthropic-skills:docx","args":"x"}`:                                          "anthropic-skills:docx",
		`activate_skill: {"name":"nerdr-send"}`:                                                        "nerdr-send",
		`skill: {"name":"nerdr-explain"}`:                                                              "nerdr-explain",
		`shell: {"command":["bash","-lc","cat /home/u/.agents/skills/nerdr-request-review/SKILL.md"]}`: "nerdr-request-review",
		`view: {"path":"/Users/u/.copilot/skills/nerdr-spinoff/SKILL.md"}`:                             "nerdr-spinoff",
		`read: {"path":"/h/.config/opencode/skills/nerdr-send/SKILL.md"}`:                              "nerdr-send",
		`Read: {"file_path":"/w/nerdr/skills/nerdr-request-review/SKILL.md"}`:                          "",
		`Bash: {"command":"go test ./..."}`:                                                            "",
	} {
		if got := skillOfTool(line); got != want {
			t.Errorf("%s: %q, want %q", line, got, want)
		}
	}
}

// A claude log: the Skill tool, and a skill run as a slash command, but
// not claude's own commands.
func TestSkillUsesInClaudesLog(t *testing.T) {
	cfg, dir := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	writeFile(t, filepath.Join(cfg, "skills", "nerdr-explain", "SKILL.md"), "---\nname: nerdr-explain\n---\n")
	log := `{"type":"user","message":{"role":"user","content":"<command-name>/model</command-name>"}}
{"type":"user","message":{"role":"user","content":"<command-name>/nerdr-explain</command-name>"}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Skill","input":{"skill":"nerdr-request-review"}}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Skill","input":{"skill":"nerdr-request-review"}}]}}
`
	writeFile(t, filepath.Join(claudeProject(dir), "s1.jsonl"), log)
	got, err := SkillUses(nil, Claude, "s1", dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["nerdr-request-review"] != 2 || got["nerdr-explain"] != 1 {
		t.Fatalf("counted %v", got)
	}
	if text, _ := Transcript(nil, Claude, "s1", dir); text == "" || strings.Contains(text, "## command") {
		t.Fatalf("a slash command is counted, not told: %q", text)
	}
}
