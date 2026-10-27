package agents

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// promptScanLines is how far into a conversation's log the first prompt is
// looked for: it comes near the top, after a few lines of bookkeeping.
const promptScanLines = 400

// FirstPrompt is the first thing the user asked in the conversation, as
// the agent logged it, or "" when that cannot be found. Only Claude's
// logs are read; for the other kinds nerdr has what was typed instead.
func FirstPrompt(k Kind, sessionID string) string {
	return FirstPromptIn(k, sessionID, "")
}

// FirstPromptIn is FirstPrompt for a conversation in dir.
func FirstPromptIn(k Kind, sessionID, dir string) string {
	for _, f := range claudeLogsIn(k, sessionID, dir) {
		if p := firstClaudePrompt(f); p != "" {
			return p
		}
	}
	return ""
}

// lastPromptTail is how much of the end of a conversation's log the last
// prompt is looked for in: a log runs to megabytes, and what matters is
// near its end.
const lastPromptTail = 1 << 20

// Latest is the end of a conversation, as the agent logged it: the last
// thing the user asked and Claude's last recap, each with when; Claude's
// logs only, as FirstPrompt. A part that cannot be found is left empty.
type Latest struct {
	Prompt   string
	PromptAt time.Time
	// Recap is the summary Claude writes into the conversation when it
	// has stood idle a few minutes: where the work is, and what is next.
	Recap   string
	RecapAt time.Time
}

// LatestOf reads the end of conversation sessionID; see Latest.
func LatestOf(k Kind, sessionID string) Latest {
	return LatestOfIn(k, sessionID, "")
}

// LatestOfIn is LatestOf for a conversation in dir.
func LatestOfIn(k Kind, sessionID, dir string) Latest {
	for _, f := range claudeLogsIn(k, sessionID, dir) {
		if l := latestClaude(f); l.Prompt != "" || l.Recap != "" {
			return l
		}
	}
	return Latest{}
}

// Prompt is one thing the user asked in a conversation, and when.
type Prompt struct {
	Text string
	At   time.Time
}

// PromptsOf is what the user asked in conversation sessionID, the oldest
// first, at most the last n; Claude's logs only, as FirstPrompt. It reads
// the whole log, so it is for when the list is asked for.
func PromptsOf(k Kind, sessionID string, n int) []Prompt {
	for _, f := range claudeLogs(k, sessionID) {
		if ps := claudePrompts(f, n); len(ps) > 0 {
			return ps
		}
	}
	return nil
}

// claudePrompts reads the last n prompts the user typed from a Claude
// conversation log.
func claudePrompts(path string, n int) []Prompt {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Prompt
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if p := claudePrompt(line); p != "" {
			out = append(out, Prompt{Text: p, At: logTime(line)})
			if len(out) > 2*n {
				out = append(out[:0], out[len(out)-n:]...)
			}
		}
		if err != nil {
			return out[max(len(out)-n, 0):]
		}
	}
}

// claudeLogs are the files Claude's conversation sessionID may be logged
// in; none for the other kinds.
func claudeLogs(k Kind, sessionID string) []string {
	return claudeLogsIn(k, sessionID, "")
}

func claudeLogsIn(k Kind, sessionID, workdir string) []string {
	if k != Claude || !ValidSessionID(sessionID) {
		return nil
	}
	dir := claudeHome()
	if dir == "" {
		return nil
	}
	// The log is under a folder named after the working directory, spelt
	// the way Claude spells it; the session id alone is enough to find it.
	files, _ := filepath.Glob(filepath.Join(dir, "projects", "*", sessionID+".jsonl"))
	if workdir == "" {
		return files
	}
	var out []string
	for _, f := range files {
		if samePath(claudeCwd(f), workdir) {
			out = append(out, f)
		}
	}
	return out
}

// latestClaude reads the last prompt the user typed, and the last recap,
// from the end of a Claude conversation log.
func latestClaude(path string) Latest {
	var out Latest
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() > lastPromptTail {
		if _, err := f.Seek(st.Size()-lastPromptTail, io.SeekStart); err != nil {
			return out
		}
	}
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		// A line cut by the seek is no JSON, and skipped.
		if p := claudePrompt(line); p != "" {
			out.Prompt, out.PromptAt = p, logTime(line)
		} else if c := claudeRecap(line); c != "" {
			out.Recap, out.RecapAt = c, logTime(line)
		}
		if err != nil {
			return out
		}
	}
}

// recapFooter is what Claude ends each recap with, for the user of its
// own screen; it means nothing in nerdr.
const recapFooter = "(disable recaps in /config)"

// claudeRecap is the text of a recap line in a Claude conversation log,
// or "".
func claudeRecap(line []byte) string {
	var e struct {
		Type        string `json:"type"`
		Subtype     string `json:"subtype"`
		IsSidechain bool   `json:"isSidechain"`
		Content     string `json:"content"`
	}
	if json.Unmarshal(line, &e) != nil || e.Type != "system" || e.Subtype != "away_summary" || e.IsSidechain {
		return ""
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(e.Content), recapFooter))
}

// logTime is when a line of a Claude conversation log was written, or the
// zero time.
func logTime(line []byte) time.Time {
	var e struct {
		Timestamp time.Time `json:"timestamp"`
	}
	_ = json.Unmarshal(line, &e)
	return e.Timestamp
}

// firstClaudePrompt reads the first prompt the user typed from a Claude
// conversation log: the first user message that is text of the user's
// own, not a tool's result, a slash command or text Claude added.
func firstClaudePrompt(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for i := 0; i < promptScanLines; i++ {
		line, err := r.ReadBytes('\n')
		if p := claudePrompt(line); p != "" {
			return p
		}
		if err != nil {
			return ""
		}
	}
	return ""
}

func claudePrompt(line []byte) string {
	var e struct {
		Type        string `json:"type"`
		IsSidechain bool   `json:"isSidechain"`
		IsMeta      bool   `json:"isMeta"`
		Message     struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &e) != nil || e.Type != "user" || e.IsSidechain || e.IsMeta || e.Message.Role != "user" {
		return ""
	}
	var text string
	if json.Unmarshal(e.Message.Content, &text) != nil {
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(e.Message.Content, &parts) != nil {
			return ""
		}
		for _, p := range parts {
			if p.Type == "text" {
				text = p.Text
				break
			}
		}
	}
	text = strings.TrimSpace(text)
	// Slash commands, their output and reminders Claude adds come as
	// tagged text; what the user typed does not start with a tag. An
	// interruption is logged as the user's text too.
	if text == "" || strings.HasPrefix(text, "<") || strings.HasPrefix(text, "/") ||
		strings.HasPrefix(text, "[Request interrupted") {
		return ""
	}
	return text
}
