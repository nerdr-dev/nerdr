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
// The skills are the folders in this folder, built into nerdr: each a
// SKILL.md, and any other files it points the agent at for what it needs
// only sometimes. They can go in two places:
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
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/safefile"
)

//go:embed */*.md
var files embed.FS

// Marker is in every skill nerdr installs. A file with it is nerdr's, and is
// brought up to date when nerdr's copy changes; one without it — the user
// took the line out to keep their own edits — is left alone.
const Marker = "<!-- nerdr: installed and kept up to date by nerdr."

// Skill is one skill: its name (also its folder), its SKILL.md and the
// other files beside it.
type Skill struct {
	Name  string
	Text  []byte
	Files []File
}

// File is one of a skill's files other than SKILL.md, which SKILL.md points
// the agent at by its name. It has Marker too, and is kept up to date, or
// left alone, on its own.
type File struct {
	Name string
	Text []byte
}

// all is every file of the skill, SKILL.md first.
func (s Skill) all() []File {
	return append([]File{{"SKILL.md", s.Text}}, s.Files...)
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
		s := Skill{Name: e.Name(), Text: text}
		more, _ := fs.ReadDir(files, e.Name())
		for _, f := range more {
			if f.Name() == "SKILL.md" {
				continue
			}
			if b, err := files.ReadFile(path.Join(e.Name(), f.Name())); err == nil {
				s.Files = append(s.Files, File{f.Name(), b})
			}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Install puts every skill in dir (a skills folder: dir/<name>/SKILL.md and
// the skill's other files), and returns the names of the skills it wrote to.
// A file already there is replaced only when it is nerdr's (it has Marker)
// and differs.
func Install(dir string) ([]string, error) {
	return Sync(dir, func(string) bool { return true })
}

// Sync removes only nerdr-owned entrypoints for disabled skills; personal copies survive.
func Sync(dir string, enabled func(string) bool) ([]string, error) {
	return syncSkills(dir, !isHomeSkillsDir(dir), enabled)
}

// SyncProject syncs skills into a worktree's skills folder, refusing
// symbolic links anywhere inside the worktree.
func SyncProject(dir string, enabled func(string) bool) ([]string, error) {
	return syncSkills(dir, true, enabled)
}

func isHomeSkillsDir(dir string) bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	dir = filepath.Clean(dir)
	for _, k := range agents.All {
		for _, d := range Dirs(home, "", k)[1:] {
			if d != "" && filepath.Clean(d) == dir {
				return true
			}
		}
	}
	return false
}

func syncSkills(dir string, worktree bool, enabled func(string) bool) ([]string, error) {
	root, base, err := safefile.OpenSkillsRoot(dir, worktree, true)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var wrote []string
	for _, s := range All() {
		if !enabled(s.Name) {
			if err := removeOwned(root, base, s); err != nil {
				return wrote, err
			}
			continue
		}
		changed := false
		for _, f := range s.all() {
			file := filepath.Join(base, s.Name, f.Name)
			old, err := safefile.ReadRegular(root, file)
			switch {
			case err == nil && (bytes.Equal(old, f.Text) || !bytes.Contains(old, []byte(Marker))):
				continue
			case err != nil && !os.IsNotExist(err):
				return wrote, err
			}
			if err := safefile.WriteRegular(root, file, f.Text, 0o644); err != nil {
				return wrote, err
			}
			changed = true
		}
		if changed {
			wrote = append(wrote, s.Name)
		}
	}
	return wrote, nil
}

// removeOwned removes the files of skill s in the skills folder base of
// root that are nerdr's, and leaves those without Marker.
func removeOwned(root *os.Root, base string, s Skill) error {
	for _, f := range s.all() {
		file := filepath.Join(base, s.Name, f.Name)
		old, err := safefile.ReadRegular(root, file)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if bytes.Contains(old, []byte(Marker)) {
			if err := root.Remove(file); err != nil {
				return err
			}
		}
	}
	return nil
}

// RemoveDisabled also clears old managed copies in other discovery directories.
// It never installs skills there or removes a personal entrypoint.
func RemoveDisabled(dir string, enabled func(string) bool) error {
	return removeDisabled(dir, !isHomeSkillsDir(dir), enabled)
}

// RemoveDisabledProject clears disabled nerdr skills in a worktree folder,
// refusing symbolic links anywhere inside the worktree.
func RemoveDisabledProject(dir string, enabled func(string) bool) error {
	return removeDisabled(dir, true, enabled)
}

func removeDisabled(dir string, worktree bool, enabled func(string) bool) error {
	root, base, err := safefile.OpenSkillsRoot(dir, worktree, false)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	for _, s := range All() {
		if enabled(s.Name) {
			continue
		}
		if err := removeOwned(root, base, s); err != nil {
			return err
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
//   - grok reads ~/.agents/skills as well as ~/.grok/skills, ~/.claude/skills
//     and ~/.cursor/skills, and finds each skill once, by its name;
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
	for _, k := range []agents.Kind{agents.Codex, agents.Gemini, agents.Grok} {
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
// there; codex, gemini, agy and grok .agents/skills. grok reads a
// project's only once its folder is trusted.
func ProjectDir(dir string, k agents.Kind) string {
	switch k {
	case agents.Codex, agents.Gemini, agents.Antigravity, agents.Grok:
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
	lines := strings.Split(front, "\n")
	for i, l := range lines {
		d, ok := strings.CutPrefix(l, "description:")
		if !ok {
			continue
		}
		d = strings.TrimSpace(d)
		if d != "" && !strings.HasPrefix(d, "|") && !strings.HasPrefix(d, ">") {
			return d
		}
		// A YAML block: the indented lines that follow.
		var block []string
		for _, l := range lines[i+1:] {
			if l != "" && l[0] != ' ' && l[0] != '\t' {
				break
			}
			block = append(block, strings.TrimSpace(l))
		}
		return strings.TrimSpace(strings.Join(block, " "))
	}
	return ""
}

// Body is the skill's instructions, after its front matter, followed by its
// other files, without the lines that mark them as nerdr's.
func (s Skill) Body() string {
	_, body := s.split()
	for _, f := range s.Files {
		body += "\n\n" + string(f.Text)
	}
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
	case agents.Grok:
		out = append(out, filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".grok", "skills"), filepath.Join(home, ".claude", "skills"), filepath.Join(home, ".cursor", "skills"))
	case agents.Cursor:
		out = append(out, filepath.Join(home, ".claude", "skills"), filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".cursor", "skills"))
	}
	return out
}

// maxSkillFile is how much of a file that is not nerdr's is read, for showing.
const maxSkillFile = 256 << 10

// Local is a skill in a skills folder that nerdr does not ship.
type Local struct {
	Skill
	File string // its SKILL.md
}

// Others are the skills in dirs that are not nerdr's, by name; a name in
// two folders is the first folder's, as with Find. Their files are the
// user's or a repository's, so what they say is cleaned for the screen.
func Others(dirs []string) []Local {
	seen := map[string]bool{}
	for _, s := range All() {
		seen[s.Name] = true
	}
	var out []Local
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if seen[e.Name()] {
				continue
			}
			file := filepath.Join(d, e.Name(), "SKILL.md")
			text, ok := ReadShown(file)
			if !ok {
				continue
			}
			seen[e.Name()] = true
			out = append(out, Local{Skill{Name: Printable(e.Name()), Text: text}, file})
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// ReadShown is the start of file, cleaned for the screen, if it is a regular
// file: a FIFO would hang the caller.
func ReadShown(file string) ([]byte, bool) {
	st, err := os.Stat(file)
	if err != nil || !st.Mode().IsRegular() {
		return nil, false
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	text, err := io.ReadAll(io.LimitReader(f, maxSkillFile))
	if err != nil {
		return nil, false
	}
	return []byte(Printable(string(text))), true
}

// Printable is s without escape sequences and control characters other
// than newlines and tabs, safe to draw on the terminal.
func Printable(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || (!unicode.IsControl(r) && r != utf8.RuneError) {
			return r
		}
		return -1
	}, ansi.Strip(s))
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

// Find is where the skill's SKILL.md is in dirs, the first that has it, and
// in what state; "" and Missing when none has it. nerdr's SKILL.md beside
// another file of the skill that is not as this nerdr ships it is Outdated.
func (s Skill) Find(dirs []string) (string, State) {
	for _, d := range dirs {
		file := filepath.Join(d, s.Name, "SKILL.md")
		text, err := os.ReadFile(file)
		switch {
		case err != nil:
			continue
		case !bytes.Contains(text, []byte(Marker)):
			return file, Own
		case !bytes.Equal(text, s.Text):
			return file, Outdated
		}
		for _, f := range s.Files {
			text, err := os.ReadFile(filepath.Join(d, s.Name, f.Name))
			if err != nil || !bytes.Equal(text, f.Text) && bytes.Contains(text, []byte(Marker)) {
				return file, Outdated
			}
		}
		return file, Current
	}
	return "", Missing
}
