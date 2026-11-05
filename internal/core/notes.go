package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/safefile"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// WorktreeNote is told to the agent in every tab, beside its own
// instructions, where it holds whatever the conversation is about. A skill
// is only read when its description matches what is asked, and "do it in
// two branches" did not: the agent made them itself, switching the tab's
// own worktree to another branch, which the tab then follows.
const WorktreeNote = "You run in a nerdr tab, and this worktree is the tab's: never create or switch branches in it. " +
	"When the user asks for work on new or separate branches, worktrees or tabs, use the nerdr-spinoff skill, which has nerdr make a tab for each once the user confirms its branch and agent; " +
	"nerdr tab new makes tabs without asking, and is only for when the user says not to be asked."

// ReadyNote is told to the agent beside worktreeNote when its turns are
// reviewed once it says the work is ready: for the same reason, as "I am
// done" matches no skill's description well enough to be counted on.
const ReadyNote = "Code review is on in this tab: when you have finished what you were asked to do and it changed code, " +
	"use the nerdr-request-review skill before you end your turn, so a second agent reviews the work."

// CodexInstructions is hooks.CodexDeveloperInstructions; the tests keep
// the user's codex out of it.
var CodexInstructions = hooks.CodexDeveloperInstructions

// CodexOptions is the -c options in args, codex's options on its command
// line, each key=value, and whether they pick a profile; a prompt after
// "--" is not read.
func CodexOptions(args []string) (config []string, profile bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		for _, f := range []string{"-c", "--config", "-p", "--profile"} {
			var v string
			switch {
			case a == f && i+1 < len(args):
				i++
				v = args[i]
			case strings.HasPrefix(a, f+"="):
				v = a[len(f)+1:]
			case len(f) == 2 && strings.HasPrefix(a, f) && len(a) > 2 && a[2] != '-':
				v = a[2:]
			default:
				continue
			}
			if f == "-c" || f == "--config" {
				config = append(config, v)
			} else {
				profile = true
			}
			break
		}
	}
	return config, profile
}

// TomlString is s as a TOML basic string: quoted, with its backslashes,
// quotes and control characters escaped.
func TomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// CopilotInstructionsVar names the folders copilot looks in for
// instructions besides the repository's.
const CopilotInstructionsVar = "COPILOT_CUSTOM_INSTRUCTIONS_DIRS"

// CopilotNoteDir writes note where copilot reads a repository's
// instructions from in a folder of copilotInstructionsVar, applying to
// every file, and returns the folder.
func CopilotNoteDir(note string) (string, error) {
	sum := sha256.Sum256([]byte(note))
	base, name := filepath.Join(filepath.Dir(state.Dir()), "copilot-notes"), hex.EncodeToString(sum[:6])
	rel := filepath.Join(name, ".github", "instructions", "nerdr.instructions.md")
	return filepath.Join(base, name), WriteNote(base, rel, []byte("---\napplyTo: \"**\"\n---\n\n"+note+"\n"))
}

// WriteNote writes text to rel in base, one of nerdr's folders, making its
// folders, unless it holds that already. An agent may write where rel is,
// so nothing there is followed: a link in its place is taken away, and the
// text goes to a new file of its own, renamed onto rel.
func WriteNote(base, rel string, text []byte) error {
	if err := os.MkdirAll(base, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return err
	}
	defer root.Close()
	if old, err := safefile.ReadRegular(root, rel); err == nil && bytes.Equal(old, text) {
		return nil
	}
	if info, err := root.Lstat(rel); err == nil && !info.Mode().IsRegular() {
		if err := root.Remove(rel); err != nil {
			return err
		}
	}
	return safefile.WriteRegular(root, rel, text, 0o644)
}

// agentNote is worktreeNote and readyNote as t's agent is to be told
// them: none of either when it cannot use the skill it points to, or nerdr
// is not set up yet, and no readyNote for a reviewer. "" for none.
func (m *Core) AgentNote(t *Tab) string {
	if m.Cfg.Install == "" {
		return ""
	}
	var notes []string
	if m.SkillOn(t, "nerdr-spinoff") {
		notes = append(notes, WorktreeNote)
	}
	if m.Cfg.Review.Auto && m.WaitsForReady(t) && t.ReviewOf == nil {
		notes = append(notes, ReadyNote)
	}
	return strings.Join(notes, "\n\n")
}

// claudeNote is the arguments that give t's claude agentNote, in its
// system prompt.
func (m *Core) ClaudeNote(t *Tab) []string {
	note := m.AgentNote(t)
	if note == "" {
		return nil
	}
	return []string{"--append-system-prompt", note}
}

// grokNote is the arguments that give t's grok agentNote, in its system
// prompt. grok keeps the rules a conversation began with: on a resume these
// change nothing.
func (m *Core) GrokNote(t *Tab) []string {
	note := m.AgentNote(t)
	if note == "" {
		return nil
	}
	return []string{"--rules", note}
}

// codexNote is the arguments that give t's codex agentNote, as its
// developer instructions, when it is started as argv with env. Set on the
// command line they replace those it would have had, so those come first,
// as its app server reads them in the tab's folder; none are added when
// they cannot be read, or argv picks a profile, whose own the app server
// does not read, rather than lose them.
func (m *Core) CodexNote(t *Tab, argv, env []string) []string {
	note := m.AgentNote(t)
	if note == "" {
		return nil
	}
	command, _ := agents.Command(m.SpecsFor(t), t.Agent)
	config, profile := CodexOptions(argv[min(len(command), len(argv)):])
	if profile {
		return nil
	}
	own, err := CodexInstructions(command, env, config, t.Dir)
	if err != nil {
		return nil
	}
	if own != "" {
		note = own + "\n\n" + note
	}
	return []string{"-c", "developer_instructions=" + TomlString(note)}
}

// copilotNote is env, the variables t's copilot is given, with a folder
// holding agentNote added to copilotInstructionsVar: the user's own
// folders, set there or inherited, stay in it. The folder is nerdr's, in
// its state folder, one for each note, so nothing goes in the worktree.
func (m *Core) CopilotNote(t *Tab, env []string) []string {
	note := m.AgentNote(t)
	if note == "" {
		return env
	}
	dir, err := CopilotNoteDir(note)
	if err != nil {
		return env
	}
	dirs := os.Getenv(CopilotInstructionsVar)
	var out []string
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, CopilotInstructionsVar+"="); ok {
			dirs = v
			continue
		}
		out = append(out, kv)
	}
	if dirs != "" {
		dirs += ","
	}
	return append(out, CopilotInstructionsVar+"="+dirs+dir)
}

// agyNote is the arguments that give t's Antigravity agentNote: a folder
// of nerdr's, added to its workspace, whose AGENTS.md it reads beside the
// user's own. It may write in the folders of its workspace, so each
// worktree has its own, written afresh at each start: an agent can change
// no other tab's note.
func (m *Core) AgyNote(t *Tab) []string {
	note := m.AgentNote(t)
	if note == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(t.Dir + "\x00" + note))
	base, name := filepath.Join(filepath.Dir(state.Dir()), "agy-notes"), hex.EncodeToString(sum[:6])
	if err := WriteNote(base, filepath.Join(name, "AGENTS.md"), []byte(note+"\n")); err != nil {
		return nil
	}
	return []string{"--add-dir", filepath.Join(base, name)}
}
