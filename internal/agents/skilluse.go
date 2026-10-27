package agents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Which skills an agent used is read from its conversation log, the same
// for every agent nerdr knows, as the tools it ran:
//
//   - claude has a tool for it (Skill, given the skill), and a skill can
//     be run as a slash command, which its log keeps tagged;
//   - gemini (activate_skill) and opencode (skill) have one too;
//   - codex, copilot and cursor read the skill's SKILL.md themselves, from
//     a folder they look for skills in.

// skillTools are the tools that load a skill, lower case.
var skillTools = map[string]bool{"skill": true, "activate_skill": true, "use_skill": true, "load_skill": true}

// skillFile is a SKILL.md in a folder the agents load skills from — not
// one in a repository, as nerdr's own are, which an agent reads when it
// works on them, not with them.
var skillFile = regexp.MustCompile(`(?:\.claude|\.agents|\.copilot|\.cursor|\.gemini|opencode)/(?:[^\s"'\\]*/)?skills/([A-Za-z0-9._:-]+)/SKILL\.md`)

// commandTag is a slash command as claude logs it.
var commandTag = regexp.MustCompile(`^<command-name>/?([A-Za-z0-9._:-]+)</command-name>`)

// slashCommand is the command a claude log line tagged as one ran, or "".
func slashCommand(text string) string {
	if m := commandTag.FindStringSubmatch(strings.TrimSpace(text)); m != nil {
		return m[1]
	}
	return ""
}

// SkillUses counts the skills used in conversation sessionID of kind k in
// dir (its newest there with no sessionID), by name.
func SkillUses(specs map[Kind]Spec, k Kind, sessionID, dir string) (map[string]int, error) {
	turns, err := turnsOf(specs, k, sessionID, dir)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, t := range turns {
		var name string
		switch t.who {
		case "tool":
			name = skillOfTool(t.text)
		case "command":
			if isSkill(t.text, dir) {
				name = t.text
			}
		}
		if name != "" {
			out[name]++
		}
	}
	return out, nil
}

// skillOfTool is the skill a tool run loaded, as toolLine wrote it, or "".
func skillOfTool(line string) string {
	name, in, _ := strings.Cut(line, ": ")
	if skillTools[strings.ToLower(name)] {
		var v struct {
			Skill string `json:"skill"`
			Name  string `json:"name"`
		}
		if json.Unmarshal([]byte(in), &v) == nil {
			if v.Skill != "" {
				return v.Skill
			}
			return v.Name
		}
		return strings.TrimSpace(in)
	}
	if m := skillFile.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return ""
}

// isSkill reports whether a slash command claude ran in dir is a skill:
// one in a folder claude reads skills from, its own, the shared one or the
// project's, or in one of its plugins. The others (/model, /clear) are
// claude's.
func isSkill(name, dir string) bool {
	if strings.ContainsAny(name, `/\*?[`) {
		return false
	}
	short := name[strings.LastIndex(name, ":")+1:]
	home, _ := os.UserHomeDir()
	roots := []string{
		filepath.Join(claudeHome(), "skills"), filepath.Join(home, ".agents", "skills"),
		filepath.Join(dir, ".claude", "skills"), filepath.Join(dir, ".agents", "skills"),
	}
	for _, root := range roots {
		if _, err := os.Stat(filepath.Join(root, short, "SKILL.md")); err == nil {
			return true
		}
	}
	for _, depth := range []string{"*", "*/*", "*/*/*", "*/*/*/*"} {
		if found, _ := filepath.Glob(filepath.Join(claudeHome(), "plugins", depth, "skills", short, "SKILL.md")); len(found) > 0 {
			return true
		}
	}
	return false
}

// SkillRules is the version of how skills are told in a log: a log read
// with older rules is read again, though it has not changed.
const SkillRules = 1

// LogStamp is what conversation sessionID of kind k in dir (its newest
// there with no sessionID) looks like from outside — its log's file, size
// and when it was last written, and the rules it is read with — to tell
// whether it changed since it was counted. "" when its log is no file
// nerdr can look at: opencode keeps a database of its own.
func LogStamp(k Kind, sessionID, dir string) string {
	path := conversationLog(k, sessionID, dir)
	if path == "" {
		return ""
	}
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("v%d %s %d %d", SkillRules, path, st.Size(), st.ModTime().UnixNano())
}

// conversationLog is the file conversation sessionID of kind k in dir is
// logged in, as turnsOf finds it, or "".
func conversationLog(k Kind, sessionID, dir string) string {
	switch k {
	case Claude:
		if files := claudeLogs(Claude, sessionID); len(files) > 0 {
			return files[0]
		}
	case Codex:
		path, _ := codexLog(sessionID, dir)
		return path
	case Copilot:
		if sessionID == "" {
			sessionID = latestCopilotSession(dir, nil)
		}
		home, err := os.UserHomeDir()
		if !ValidSessionID(sessionID) || err != nil || (dir != "" && !copilotInDir(home, sessionID, dir)) {
			return ""
		}
		return filepath.Join(home, ".copilot", "session-state", sessionID, "events.jsonl")
	case Cursor:
		if sessionID == "" {
			sessionID = latestCursorChat(dir, nil)
		}
		if !ValidSessionID(sessionID) {
			return ""
		}
		return cursorTranscript(sessionID, dir)
	case Gemini:
		path, _ := geminiLog(sessionID, dir)
		return path
	}
	return ""
}
