// Package skills holds the agent skills nerdr ships — how an agent explains
// a finished change for the review panel when asked (nerdr-explain), how to
// split its work off into new tabs
// (nerdr-spinoff), and how to hand its work to the agent its tab is
// switched to (nerdr-handoff), and how to have a second agent review its
// work (nerdr-request-review), how to send a message to another agent of
// the same tab and start more (nerdr-send), how to see and use the rest of
// nerdr (nerdr-tools), and how to find and run the user's plugins
// (nerdr-plugins) — and puts them where each agent looks for skills.
//
// The skills are the SKILL.md files in this folder, built into nerdr. They
// can go in two places:
//
//   - the agents' own skills folders in the home folder (Home), for agents
//     that can see it;
//   - the folder an agent starts in (Project), for agents in a sandbox that
//     sees little more than the worktree: .claude/skills, or .agents/skills
//     for the agents that read only that.
package skills

import (
	"bytes"
	"embed"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/agents"
)

//go:embed */SKILL.md
var files embed.FS

// Marker is in every skill nerdr installs. A file with it is nerdr's, and is
// brought up to date when nerdr's copy changes; one without it — the user
// took the line out to keep their own edits — is left alone.
const Marker = "<!-- nerdr: installed and kept up to date by nerdr."

// Skill is one skill: its name (also its folder) and its SKILL.md.
type Skill struct {
	Name string
	Text []byte
}

// All is every skill nerdr ships, by name.
func All() []Skill {
	var out []Skill
	entries, _ := fs.ReadDir(files, ".")
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		text, err := files.ReadFile(path.Join(e.Name(), "SKILL.md"))
		if err != nil {
			continue
		}
		out = append(out, Skill{Name: e.Name(), Text: text})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Install puts every skill in dir (a skills folder: dir/<name>/SKILL.md),
// and returns the names it wrote. A skill already there is replaced only
// when it is nerdr's (it has Marker) and differs.
func Install(dir string) ([]string, error) {
	return Sync(dir, func(string) bool { return true })
}

// Sync removes only nerdr-owned entrypoints for disabled skills; personal copies survive.
func Sync(dir string, enabled func(string) bool) ([]string, error) {
	var wrote []string
	for _, s := range All() {
		file := filepath.Join(dir, s.Name, "SKILL.md")
		old, err := os.ReadFile(file)
		if !enabled(s.Name) {
			if err == nil && bytes.Contains(old, []byte(Marker)) {
				if err := os.Remove(file); err != nil {
					return wrote, err
				}
			} else if err != nil && !os.IsNotExist(err) {
				return wrote, err
			}
			continue
		}
		switch {
		case err == nil && (bytes.Equal(old, s.Text) || !bytes.Contains(old, []byte(Marker))):
			continue
		case err != nil && !os.IsNotExist(err):
			return wrote, err
		}
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return wrote, err
		}
		if err := os.WriteFile(file, s.Text, 0o644); err != nil {
			return wrote, err
		}
		wrote = append(wrote, s.Name)
	}
	return wrote, nil
}

// RemoveDisabled also clears old managed copies in other discovery directories.
// It never installs skills there or removes a personal entrypoint.
func RemoveDisabled(dir string, enabled func(string) bool) error {
	for _, s := range All() {
		if enabled(s.Name) {
			continue
		}
		file := filepath.Join(dir, s.Name, "SKILL.md")
		old, err := os.ReadFile(file)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if bytes.Contains(old, []byte(Marker)) {
			if err := os.Remove(file); err != nil {
				return err
			}
		}
	}
	return nil
}

// Target is a skills folder, and the agents that read it.
type Target struct {
	Dir    string
	Agents []agents.Kind
}

// Home is where the skills go in the home folder for the given agents, one
// folder each and no folder twice:
//
//   - claude reads ~/.claude/skills;
//   - opencode reads ~/.claude/skills too, and ~/.config/opencode/skills —
//     the latter only when claude is not there, since opencode would
//     otherwise find each skill twice;
//   - copilot reads ~/.copilot/skills;
//   - codex reads ~/.agents/skills, and gemini that or ~/.gemini/skills;
//   - agy (Antigravity) reads ~/.gemini/config/skills;
//   - cursor reads ~/.claude/skills and ~/.agents/skills as well as its
//     own ~/.cursor/skills, which is used only when neither of the others
//     is. With both of them it finds each skill twice, which is left to it.
func Home(home string, kinds []agents.Kind) []Target {
	has := map[agents.Kind]bool{}
	for _, k := range kinds {
		has[k] = true
	}
	var out []Target
	claude := filepath.Join(home, ".claude", "skills")
	switch {
	case has[agents.Claude] && has[agents.OpenCode]:
		out = append(out, Target{claude, []agents.Kind{agents.Claude, agents.OpenCode}})
	case has[agents.Claude]:
		out = append(out, Target{claude, []agents.Kind{agents.Claude}})
	case has[agents.OpenCode]:
		out = append(out, Target{filepath.Join(home, ".config", "opencode", "skills"), []agents.Kind{agents.OpenCode}})
	}
	if has[agents.Copilot] {
		out = append(out, Target{filepath.Join(home, ".copilot", "skills"), []agents.Kind{agents.Copilot}})
	}
	var shared []agents.Kind
	for _, k := range []agents.Kind{agents.Codex, agents.Gemini} {
		if has[k] {
			shared = append(shared, k)
		}
	}
	if len(shared) > 0 {
		out = append(out, Target{filepath.Join(home, ".agents", "skills"), shared})
	}
	if has[agents.Antigravity] {
		out = append(out, Target{filepath.Join(home, ".gemini", "config", "skills"), []agents.Kind{agents.Antigravity}})
	}
	if has[agents.Cursor] {
		joined := false
		for i, tg := range out {
			if tg.Dir == claude || tg.Dir == filepath.Join(home, ".agents", "skills") {
				out[i].Agents = append(out[i].Agents, agents.Cursor)
				joined = true
				break
			}
		}
		if !joined {
			out = append(out, Target{filepath.Join(home, ".cursor", "skills"), []agents.Kind{agents.Cursor}})
		}
	}
	return out
}

// ProjectDir is the skills folder in dir, the folder an agent of kind k
// starts in: claude, opencode, copilot and cursor all read .claude/skills
// there; codex, gemini and agy .agents/skills.
func ProjectDir(dir string, k agents.Kind) string {
	switch k {
	case agents.Codex, agents.Gemini, agents.Antigravity:
		return filepath.Join(dir, ".agents", "skills")
	}
	return filepath.Join(dir, ".claude", "skills")
}

// ExcludePatterns are the paths, from the worktree root, that skills put
// in a worktree take up, for keeping them out of git.
func ExcludePatterns() []string {
	var out []string
	for _, s := range All() {
		out = append(out, "/.claude/skills/"+s.Name+"/", "/.agents/skills/"+s.Name+"/")
	}
	return out
}

// Names lists the skills' names, for saying what was installed.
func Names() string {
	var names []string
	for _, s := range All() {
		names = append(names, s.Name)
	}
	return strings.Join(names, ", ")
}

// Description is the skill's description, from its front matter: what it
// is for, and when the agent is to use it.
func (s Skill) Description() string {
	front, _ := s.split()
	for _, l := range strings.Split(front, "\n") {
		if d, ok := strings.CutPrefix(l, "description:"); ok {
			return strings.TrimSpace(d)
		}
	}
	return ""
}

// Body is the skill's instructions, after its front matter, without the
// line that marks it as nerdr's.
func (s Skill) Body() string {
	_, body := s.split()
	var out []string
	for _, l := range strings.Split(body, "\n") {
		if !strings.HasPrefix(l, Marker) {
			out = append(out, l)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// split cuts SKILL.md into its front matter and the rest.
func (s Skill) split() (front, body string) {
	text := strings.ReplaceAll(string(s.Text), "\r\n", "\n")
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return "", text
	}
	front, body, ok = strings.Cut(rest, "\n---")
	if !ok {
		return "", text
	}
	return front, strings.TrimPrefix(body, "\n")
}

// Dirs are the skills folders an agent of kind k started in dir reads, in
// the home folder home: see Home and ProjectDir.
func Dirs(home, dir string, k agents.Kind) []string {
	out := []string{ProjectDir(dir, k)}
	switch k {
	case agents.Claude:
		out = append(out, filepath.Join(home, ".claude", "skills"))
	case agents.OpenCode:
		out = append(out, filepath.Join(home, ".claude", "skills"), filepath.Join(home, ".config", "opencode", "skills"))
	case agents.Copilot:
		out = append(out, filepath.Join(home, ".copilot", "skills"))
	case agents.Codex:
		out = append(out, filepath.Join(home, ".agents", "skills"))
	case agents.Gemini:
		out = append(out, filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".gemini", "skills"))
	case agents.Antigravity:
		out = append(out, filepath.Join(home, ".gemini", "config", "skills"))
	case agents.Cursor:
		out = append(out, filepath.Join(home, ".claude", "skills"), filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".cursor", "skills"))
	}
	return out
}

// State is whether an agent can use a skill.
type State int

const (
	// Missing: in none of the folders the agent reads.
	Missing State = iota
	// Current: nerdr's copy, as this nerdr ships it.
	Current
	// Outdated: nerdr's copy, from another version of nerdr; the next
	// start brings it up to date.
	Outdated
	// Own: a copy the user keeps their own edits in (the marker taken out),
	// which nerdr leaves alone.
	Own
)

// Find is where the skill is in dirs, the first that has it, and in what
// state; "" and Missing when none has it.
func (s Skill) Find(dirs []string) (string, State) {
	for _, d := range dirs {
		file := filepath.Join(d, s.Name, "SKILL.md")
		text, err := os.ReadFile(file)
		switch {
		case err != nil:
			continue
		case bytes.Equal(text, s.Text):
			return file, Current
		case bytes.Contains(text, []byte(Marker)):
			return file, Outdated
		}
		return file, Own
	}
	return "", Missing
}
