package app

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

// worktreeNote is told to the agent in every tab, beside its own
// instructions, where it holds whatever the conversation is about. A skill
// is only read when its description matches what is asked, and "do it in
// two branches" did not: the agent made them itself, switching the tab's
// own worktree to another branch, which the tab then follows.
const worktreeNote = "You run in a nerdr tab, and this worktree is the tab's: never create or switch branches in it. " +
	"When the user asks for work on new or separate branches, worktrees or tabs, use the nerdr-spinoff skill, which has nerdr make a tab for each once the user confirms its branch and agent; " +
	"nerdr tab new makes tabs without asking, and is only for when the user says not to be asked."

// readyNote is told to the agent beside worktreeNote when its turns are
// reviewed once it says the work is ready: for the same reason, as "I am
// done" matches no skill's description well enough to be counted on.
const readyNote = "Code review is on in this tab: when you have finished what you were asked to do and it changed code, " +
	"use the nerdr-request-review skill before you end your turn, so a second agent reviews the work."

// agentNote is worktreeNote and readyNote as t's agent is to be told
// them: none of either when it cannot use the skill it points to, or nerdr
// is not set up yet, and no readyNote for a reviewer. "" for none.
func (m *Model) agentNote(t *tab) string {
	if m.cfg.Install == "" {
		return ""
	}
	var notes []string
	if m.skillOn(t, "nerdr-spinoff") {
		notes = append(notes, worktreeNote)
	}
	if m.cfg.Review.Auto && m.waitsForReady(t) && t.reviewOf == nil {
		notes = append(notes, readyNote)
	}
	return strings.Join(notes, "\n\n")
}

// claudeNote is the arguments that give t's claude agentNote, in its
// system prompt.
func (m *Model) claudeNote(t *tab) []string {
	note := m.agentNote(t)
	if note == "" {
		return nil
	}
	return []string{"--append-system-prompt", note}
}

// codexNote is the arguments that give t's codex agentNote, as its
// developer instructions, when it is started as argv with env. Set on the
// command line they replace those it would have had, so those come first,
// as its app server reads them in the tab's folder; none are added when
// they cannot be read, or argv picks a profile, whose own the app server
// does not read, rather than lose them.
func (m *Model) codexNote(t *tab, argv, env []string) []string {
	note := m.agentNote(t)
	if note == "" {
		return nil
	}
	command, _ := agents.Command(m.specsFor(t), t.Agent)
	config, profile := codexOptions(argv[min(len(command), len(argv)):])
	if profile {
		return nil
	}
	own, err := codexInstructions(command, env, config, t.Dir)
	if err != nil {
		return nil
	}
	if own != "" {
		note = own + "\n\n" + note
	}
	return []string{"-c", "developer_instructions=" + tomlString(note)}
}

// codexInstructions is hooks.CodexDeveloperInstructions; the tests keep
// the user's codex out of it.
var codexInstructions = hooks.CodexDeveloperInstructions

// codexOptions is the -c options in args, codex's options on its command
// line, each key=value, and whether they pick a profile; a prompt after
// "--" is not read.
func codexOptions(args []string) (config []string, profile bool) {
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

// tomlString is s as a TOML basic string: quoted, with its backslashes,
// quotes and control characters escaped.
func tomlString(s string) string {
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

// copilotInstructionsVar names the folders copilot looks in for
// instructions besides the repository's.
const copilotInstructionsVar = "COPILOT_CUSTOM_INSTRUCTIONS_DIRS"

// copilotNote is env, the variables t's copilot is given, with a folder
// holding agentNote added to copilotInstructionsVar: the user's own
// folders, set there or inherited, stay in it. The folder is nerdr's, in
// its state folder, one for each note, so nothing goes in the worktree.
func (m *Model) copilotNote(t *tab, env []string) []string {
	note := m.agentNote(t)
	if note == "" {
		return env
	}
	dir, err := copilotNoteDir(note)
	if err != nil {
		return env
	}
	dirs := os.Getenv(copilotInstructionsVar)
	var out []string
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, copilotInstructionsVar+"="); ok {
			dirs = v
			continue
		}
		out = append(out, kv)
	}
	if dirs != "" {
		dirs += ","
	}
	return append(out, copilotInstructionsVar+"="+dirs+dir)
}

// copilotNoteDir writes note where copilot reads a repository's
// instructions from in a folder of copilotInstructionsVar, applying to
// every file, and returns the folder.
func copilotNoteDir(note string) (string, error) {
	sum := sha256.Sum256([]byte(note))
	base, name := filepath.Join(filepath.Dir(state.Dir()), "copilot-notes"), hex.EncodeToString(sum[:6])
	rel := filepath.Join(name, ".github", "instructions", "nerdr.instructions.md")
	return filepath.Join(base, name), writeNote(base, rel, []byte("---\napplyTo: \"**\"\n---\n\n"+note+"\n"))
}

// agyNote is the arguments that give t's Antigravity agentNote: a folder
// of nerdr's, added to its workspace, whose AGENTS.md it reads beside the
// user's own. It may write in the folders of its workspace, so each
// worktree has its own, written afresh at each start: an agent can change
// no other tab's note.
func (m *Model) agyNote(t *tab) []string {
	note := m.agentNote(t)
	if note == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(t.Dir + "\x00" + note))
	base, name := filepath.Join(filepath.Dir(state.Dir()), "agy-notes"), hex.EncodeToString(sum[:6])
	if err := writeNote(base, filepath.Join(name, "AGENTS.md"), []byte(note+"\n")); err != nil {
		return nil
	}
	return []string{"--add-dir", filepath.Join(base, name)}
}

// writeNote writes text to rel in base, one of nerdr's folders, making its
// folders, unless it holds that already. An agent may write where rel is,
// so nothing there is followed: a link in its place is taken away, and the
// text goes to a new file of its own, renamed onto rel.
func writeNote(base, rel string, text []byte) error {
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
