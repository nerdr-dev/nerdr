package skills

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/agents"
)

// TestSkillsAreValidForEveryAgent covers what claude, opencode and copilot
// all require: a SKILL.md whose name matches its folder and is lowercase
// with single hyphens, and a description.
func TestSkillsAreValidForEveryAgent(t *testing.T) {
	all := All()
	if len(all) != 7 {
		t.Fatalf("expected the seven skills, got %d", len(all))
	}
	nameRe := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	for _, s := range all {
		text := string(s.Text)
		if !strings.HasPrefix(text, "---\nname: "+s.Name+"\n") {
			t.Errorf("%s: the frontmatter must open with its own name", s.Name)
		}
		if !nameRe.MatchString(s.Name) || len(s.Name) > 64 {
			t.Errorf("%s: not a valid skill name", s.Name)
		}
		if !strings.Contains(text, "\ndescription: ") {
			t.Errorf("%s: no description", s.Name)
		}
		if !strings.Contains(text, Marker) {
			t.Errorf("%s: no marker, so it would never be updated", s.Name)
		}
	}
}

// TestInstallUpdatesOnlyWhatIsNerdrs covers installing into a skills
// folder: missing skills are written, an outdated copy of nerdr's is
// brought up to date, and one the user took over is left alone.
func TestInstallUpdatesOnlyWhatIsNerdrs(t *testing.T) {
	dir := t.TempDir()
	wrote, err := Install(dir)
	if err != nil || len(wrote) != len(All()) {
		t.Fatalf("first install: %v %v", wrote, err)
	}
	if wrote, _ := Install(dir); len(wrote) != 0 {
		t.Fatalf("an up-to-date install should write nothing, wrote %v", wrote)
	}

	explain := filepath.Join(dir, "nerdr-explain", "SKILL.md")
	handoff := filepath.Join(dir, "nerdr-handoff", "SKILL.md")
	_ = os.WriteFile(explain, []byte("old\n"+Marker+" …-->\n"), 0o644) // nerdr's, outdated
	_ = os.WriteFile(handoff, []byte("mine, no marker\n"), 0o644)      // the user's

	wrote, _ = Install(dir)
	if len(wrote) != 1 || wrote[0] != "nerdr-explain" {
		t.Fatalf("expected only nerdr-explain updated, got %v", wrote)
	}
	if got, _ := os.ReadFile(handoff); !bytes.Equal(got, []byte("mine, no marker\n")) {
		t.Fatalf("the user's own skill was overwritten: %q", got)
	}
}

func TestDisabledSkillsAreRemovedButPersonalCopiesSurvive(t *testing.T) {
	dir := t.TempDir()
	if _, err := Install(dir); err != nil {
		t.Fatal(err)
	}
	personal := filepath.Join(dir, "nerdr-explain", "SKILL.md")
	if err := os.WriteFile(personal, []byte("my own skill"), 0644); err != nil {
		t.Fatal(err)
	}
	enabled := func(name string) bool { return name == "nerdr-request-review" }
	if _, err := Sync(dir, enabled); err != nil {
		t.Fatal(err)
	}
	for _, s := range All() {
		_, err := os.Stat(filepath.Join(dir, s.Name, "SKILL.md"))
		want := s.Name == "nerdr-request-review" || s.Name == "nerdr-explain"
		if (err == nil) != want {
			t.Fatalf("%s: %v", s.Name, err)
		}
	}
	if err := RemoveDisabled(dir, func(string) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "nerdr-request-review", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("disabled entrypoint remains")
	}
	b, _ := os.ReadFile(personal)
	if string(b) != "my own skill" {
		t.Fatal("personal copy changed")
	}
}

// TestHomeTargetsEachFolderOnce covers where the skills go for the agents
// on the PATH, without opencode finding them twice.
func TestHomeTargetsEachFolderOnce(t *testing.T) {
	dirs := func(kinds ...agents.Kind) []string {
		var out []string
		for _, tg := range Home("/h", kinds) {
			out = append(out, tg.Dir)
		}
		return out
	}
	for _, c := range []struct {
		kinds []agents.Kind
		want  string
	}{
		{[]agents.Kind{agents.Claude, agents.OpenCode, agents.Copilot}, "/h/.claude/skills /h/.copilot/skills"},
		{[]agents.Kind{agents.OpenCode}, "/h/.config/opencode/skills"},
		{[]agents.Kind{agents.Copilot}, "/h/.copilot/skills"},
		{[]agents.Kind{agents.Claude, agents.Codex}, "/h/.claude/skills /h/.agents/skills"},
		{[]agents.Kind{agents.Codex, agents.Gemini, agents.Cursor}, "/h/.agents/skills"},
		{[]agents.Kind{agents.Cursor}, "/h/.cursor/skills"},
		{[]agents.Kind{agents.Antigravity}, "/h/.gemini/config/skills"},
		{nil, ""},
	} {
		if got := strings.Join(dirs(c.kinds...), " "); got != c.want {
			t.Errorf("%v: %q, want %q", c.kinds, got, c.want)
		}
	}
}

func TestSkillParts(t *testing.T) {
	for _, s := range All() {
		if d := s.Description(); d == "" || strings.Contains(d, "\n") {
			t.Errorf("%s: description %q", s.Name, d)
		}
		b := s.Body()
		if !strings.HasPrefix(b, "# ") || strings.Contains(b, Marker) || strings.Contains(b, "description:") {
			t.Errorf("%s: body starts %q", s.Name, b[:min(len(b), 80)])
		}
	}
}

func TestFindSaysWhetherTheAgentHasIt(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	s := All()[0]
	dirs := Dirs(home, dir, agents.Copilot)
	if _, st := s.Find(dirs); st != Missing {
		t.Fatalf("nothing installed: %v", st)
	}
	if _, err := Install(filepath.Join(home, ".claude", "skills")); err != nil {
		t.Fatal(err)
	}
	if _, st := s.Find(dirs); st != Missing {
		t.Fatal("copilot does not read ~/.claude/skills")
	}
	if _, st := s.Find(Dirs(home, dir, agents.Claude)); st != Current {
		t.Fatalf("claude reads ~/.claude/skills: %v", st)
	}
	file := filepath.Join(ProjectDir(dir, agents.Copilot), s.Name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, append([]byte(Marker+" old -->\n"), "old"...), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, st := s.Find(dirs); st != Outdated || got != file {
		t.Fatalf("the worktree's own copy, from an older nerdr: %s %v", got, st)
	}
	if err := os.WriteFile(file, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, st := s.Find(dirs); st != Own {
		t.Fatalf("a copy without the marker is the user's: %v", st)
	}
}

// TestCodexReadsAgentsSkills covers codex, which reads .agents/skills and
// not claude's folders.
func TestCodexReadsAgentsSkills(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	s := All()[0]
	if _, err := Install(ProjectDir(dir, agents.Claude)); err != nil {
		t.Fatal(err)
	}
	if _, st := s.Find(Dirs(home, dir, agents.Codex)); st != Missing {
		t.Fatal("codex does not read .claude/skills")
	}
	if _, err := Install(ProjectDir(dir, agents.Codex)); err != nil {
		t.Fatal(err)
	}
	if got, st := s.Find(Dirs(home, dir, agents.Codex)); st != Current || got != filepath.Join(dir, ".agents", "skills", s.Name, "SKILL.md") {
		t.Fatalf("codex reads .agents/skills: %s %v", got, st)
	}

	victim := filepath.Join(t.TempDir(), "victim.md")
	_ = os.WriteFile(victim, []byte("keep\n"+Marker+"\n"), 0o644)
	link := filepath.Join(dir, ".agents", "skills", s.Name, "SKILL.md")
	_ = os.Remove(link)
	if err := os.Symlink(victim, link); err == nil {
		if _, err := Install(ProjectDir(dir, agents.Codex)); err == nil {
			t.Fatal("Install wrote through a symbolic link")
		}
		if got, _ := os.ReadFile(victim); !strings.HasPrefix(string(got), "keep\n") {
			t.Fatalf("victim overwritten: %q", got)
		}
	}

	// A parent symlink (.claude or .claude/skills) in a worktree is rejected,
	// while a dotfile symlink in the user's home folder (~/.claude -> dotfiles)
	// is followed.
	wtLink := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(wtLink, ".claude")); err == nil {
		if _, err := Install(ProjectDir(wtLink, agents.Claude)); err == nil {
			t.Fatal("Install followed a symlinked .claude in a worktree")
		}
	}
	dotHome := t.TempDir()
	dotfiles := t.TempDir()
	t.Setenv("HOME", dotHome)
	_ = os.MkdirAll(filepath.Join(dotfiles, "claude"), 0o755)
	if err := os.Symlink(filepath.Join(dotfiles, "claude"), filepath.Join(dotHome, ".claude")); err == nil {
		if _, err := Install(filepath.Join(dotHome, ".claude", "skills")); err != nil {
			t.Fatalf("home symlinked ~/.claude failed: %v", err)
		}
	}
	// A worktree inside ~/.local/share/worktrees must still reject symlinks.
	wtInHome := filepath.Join(dotHome, ".local", "share", "worktrees", "proj")
	_ = os.MkdirAll(wtInHome, 0o755)
	if err := os.Symlink(t.TempDir(), filepath.Join(wtInHome, ".claude")); err == nil {
		if _, err := SyncProject(ProjectDir(wtInHome, agents.Claude), func(string) bool { return true }); err == nil {
			t.Fatal("worktree under ~/.local followed .claude symlink")
		}
	}
}
