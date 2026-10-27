package agents

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/proc"
)

// A tab whose agent is switched hands the new agent what the old one did.
// When the old agent cannot write that itself — it has run out of tokens,
// say — the new one reads the old one's conversation instead, from the log
// its CLI keeps. Transcript turns that log into something an agent can read
// through: what the user and the agent said, and one line for each tool the
// agent ran. The tools' output, most of a log, is left out.

// Transcript is the conversation sessionID of kind k in dir, cut down to
// what was said, or an error saying why it could not be read. With no
// sessionID, the newest conversation of k in dir is taken.
func Transcript(specs map[Kind]Spec, k Kind, sessionID, dir string) (string, error) {
	turns, err := turnsOf(specs, k, sessionID, dir)
	if err != nil {
		return "", err
	}
	if len(turns) == 0 {
		return "", errors.New("its conversation log has nothing nerdr could read")
	}
	return render(k, turns), nil
}

// turnsOf reads the conversation sessionID of kind k in dir, the newest
// there with no sessionID, from the log its CLI keeps.
func turnsOf(specs map[Kind]Spec, k Kind, sessionID, dir string) ([]turn, error) {
	var turns []turn
	var err error
	switch k {
	case Claude:
		turns, err = claudeTurns(sessionID, dir)
	case Codex:
		turns, err = codexTurns(sessionID, dir)
	case OpenCode:
		turns, err = openCodeTurns(specFor(specs, k), sessionID, dir)
	case Copilot:
		turns, err = copilotTurns(sessionID, dir)
	case Cursor:
		turns, err = cursorTurns(sessionID, dir)
	case Gemini:
		turns, err = geminiTurns(sessionID, dir)
	default:
		err = fmt.Errorf("nerdr does not know where %s keeps its conversations", k)
	}
	return turns, err
}

// turn is one thing said in a conversation, or a tool run.
type turn struct {
	who  string // "user", the agent's kind, "tool", or "command"
	text string
}

const (
	// maxSaid is the most kept of one thing said: a message can be a
	// pasted log.
	maxSaid = 6000
	// maxTool is the most kept of what a tool was given.
	maxTool = 200
	// maxTranscript is the most of a transcript kept; past it the start is
	// dropped, all but the first thing the user asked, as the end is where
	// the work stands.
	maxTranscript = 300 << 10
)

func render(k Kind, turns []turn) string {
	var parts []string
	for _, t := range turns {
		switch t.who {
		case "tool":
			parts = append(parts, "  → "+clip(oneLine(t.text), maxTool))
		case "command":
			// A slash command the user ran: counted (see SkillUses), not
			// told.
		default:
			parts = append(parts, "## "+t.who+"\n\n"+clip(strings.TrimSpace(t.text), maxSaid))
		}
	}
	head := fmt.Sprintf("# %s's conversation\n\nCut down by nerdr for the agent taking over: what the user and %s said, and → for each tool %s ran (their output is left out).\n", k, k, k)
	size := len(head)
	for _, p := range parts {
		size += len(p) + 2
	}
	if size > maxTranscript && len(parts) > 1 {
		first := 0
		for i, t := range turns {
			if t.who == "user" {
				first = i
				break
			}
		}
		keep := []string{parts[first], "[… the conversation up to here is left out: it is long, and its end is where the work stands …]"}
		var tail []string
		room := maxTranscript - len(head) - len(keep[0]) - len(keep[1])
		for i := len(parts) - 1; i > first && room > 0; i-- {
			room -= len(parts[i]) + 2
			if room > 0 {
				tail = append(tail, parts[i])
			}
		}
		for i, j := 0, len(tail)-1; i < j; i, j = i+1, j-1 {
			tail[i], tail[j] = tail[j], tail[i]
		}
		parts = append(keep, tail...)
	}
	return head + "\n" + strings.Join(parts, "\n\n") + "\n"
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf(" [… %d more characters]", len(s)-cut)
}

func utf8Start(b byte) bool { return b&0xc0 != 0x80 }

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// toolLine is a tool's name and what it was given, on one line.
func toolLine(name string, input any) string {
	var in string
	switch v := input.(type) {
	case string:
		in = v
	case json.RawMessage:
		in = string(v)
		var s string
		if json.Unmarshal(v, &s) == nil {
			in = s
		}
	default:
		b, _ := json.Marshal(v)
		in = string(b)
	}
	if in == "" || in == "{}" || in == "null" {
		return name
	}
	return name + ": " + in
}

// said reports whether text is something the user typed, not text the CLI
// put in the user's place: reminders, the environment, slash commands and
// their output come tagged, and codex adds the AGENTS.md it read.
func said(text string) bool {
	text = strings.TrimSpace(text)
	return text != "" && !strings.HasPrefix(text, "<") && !strings.HasPrefix(text, "# AGENTS.md instructions")
}

// eachLine calls line for every line of the file at path.
func eachLine(path string, line func([]byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		b, err := r.ReadBytes('\n')
		if len(b) > 0 {
			line(b)
		}
		if err != nil {
			return nil
		}
	}
}

// claudeTurns reads Claude's log: user and assistant entries, leaving out
// tool results, thinking and side conversations (subagents).
func claudeTurns(sessionID, dir string) ([]turn, error) {
	if sessionID == "" {
		return nil, errors.New("the tab does not know claude's conversation id")
	}
	files := claudeLogsIn(Claude, sessionID, dir)
	if len(files) == 0 {
		return nil, fmt.Errorf("found no log of claude's conversation %s", sessionID)
	}
	return claudeFileTurns(files[0])
}

// claudeFileTurns reads the Claude log at path, as claudeTurns does.
func claudeFileTurns(path string) ([]turn, error) {
	var turns []turn
	err := eachLine(path, func(b []byte) {
		var e struct {
			Type        string `json:"type"`
			IsSidechain bool   `json:"isSidechain"`
			IsMeta      bool   `json:"isMeta"`
			Message     struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(b, &e) != nil || e.IsSidechain || e.IsMeta || (e.Type != "user" && e.Type != "assistant") {
			return
		}
		var s string
		if json.Unmarshal(e.Message.Content, &s) == nil {
			if e.Type == "user" && said(s) {
				turns = append(turns, turn{"user", s})
			} else if c := slashCommand(s); e.Type == "user" && c != "" {
				turns = append(turns, turn{"command", c})
			}
			return
		}
		var parts []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}
		if json.Unmarshal(e.Message.Content, &parts) != nil {
			return
		}
		for _, p := range parts {
			switch {
			case p.Type == "text" && e.Type == "user" && said(p.Text):
				turns = append(turns, turn{"user", p.Text})
			case p.Type == "text" && e.Type == "user" && slashCommand(p.Text) != "":
				turns = append(turns, turn{"command", slashCommand(p.Text)})
			case p.Type == "text" && e.Type == "assistant" && strings.TrimSpace(p.Text) != "":
				turns = append(turns, turn{string(Claude), p.Text})
			case p.Type == "tool_use":
				turns = append(turns, turn{"tool", toolLine(p.Name, p.Input)})
			}
		}
	})
	return turns, err
}

// codexHome is where codex keeps its sessions.
func codexHome() string {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex")
}

// codexLog finds the rollout file of codex's conversation sessionID, or
// with none, of its newest conversation started in dir.
func codexLog(sessionID, dir string) (string, error) {
	files, _ := filepath.Glob(filepath.Join(codexHome(), "sessions", "*", "*", "*", "rollout-*.jsonl"))
	if sessionID != "" {
		if !ValidSessionID(sessionID) {
			return "", fmt.Errorf("found no log of codex's conversation %s", sessionID)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "-"+sessionID+".jsonl") {
				if dir != "" && !samePath(codexCwd(f), dir) {
					return "", fmt.Errorf("found no log of codex's conversation %s", sessionID)
				}
				return f, nil
			}
		}
		return "", fmt.Errorf("found no log of codex's conversation %s", sessionID)
	}
	// Their names start with when they began, so the newest sort last.
	sort.Strings(files)
	for i := len(files) - 1; i >= 0 && i >= len(files)-300; i-- {
		if samePath(codexCwd(files[i]), dir) {
			return files[i], nil
		}
	}
	return "", errors.New("found no codex conversation started in this worktree")
}

func codexCwd(path string) string {
	var cwd string
	_ = eachFirstLine(path, func(b []byte) {
		var e struct {
			Payload struct {
				Cwd string `json:"cwd"`
			} `json:"payload"`
		}
		if json.Unmarshal(b, &e) == nil {
			cwd = e.Payload.Cwd
		}
	})
	return cwd
}

func eachFirstLine(path string, line func([]byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	b, _ := r.ReadBytes('\n')
	line(b)
	return nil
}

// codexTurns reads codex's rollout: the messages in it and the tools
// called, leaving out the developer's (its instructions) and what codex
// puts in the user's place.
func codexTurns(sessionID, dir string) ([]turn, error) {
	path, err := codexLog(sessionID, dir)
	if err != nil {
		return nil, err
	}
	return codexFileTurns(path)
}

// codexFileTurns reads the codex rollout at path, as codexTurns does.
func codexFileTurns(path string) ([]turn, error) {
	var turns []turn
	err := eachLine(path, func(b []byte) {
		var e struct {
			Type    string `json:"type"`
			Payload struct {
				Type    string `json:"type"`
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
				Name      string          `json:"name"`
				Input     json.RawMessage `json:"input"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"payload"`
		}
		if json.Unmarshal(b, &e) != nil || e.Type != "response_item" {
			return
		}
		p := e.Payload
		switch p.Type {
		case "message":
			for _, c := range p.Content {
				switch {
				case p.Role == "user" && said(c.Text):
					turns = append(turns, turn{"user", c.Text})
				case p.Role == "assistant" && strings.TrimSpace(c.Text) != "":
					turns = append(turns, turn{string(Codex), c.Text})
				}
			}
		case "custom_tool_call":
			turns = append(turns, turn{"tool", toolLine(p.Name, p.Input)})
		case "function_call":
			turns = append(turns, turn{"tool", toolLine(p.Name, p.Arguments)})
		}
	})
	return turns, err
}

// openCodeTurns asks opencode for the conversation, as it keeps them in a
// database of its own.
func openCodeTurns(s Spec, sessionID, dir string) ([]turn, error) {
	if sessionID == "" {
		sessionID = latestOpenCodeSession(s, dir, nil)
	}
	if !ValidSessionID(sessionID) {
		return nil, errors.New("found no opencode conversation started in this worktree")
	}
	if len(s.Command) == 0 {
		return nil, errors.New("no command for opencode")
	}
	cmd := proc.Detach(exec.Command(s.Command[0], append(s.Command[1:], "session", "export", sessionID)...))
	cmd.Dir = dir
	out, err := runFor(cmd, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("opencode session export %s: %v", sessionID, err)
	}
	turns, exportDir, err := parseOpenCodeExportWithDir(out)
	if err != nil {
		return nil, err
	}
	if dir != "" {
		if exportDir != "" {
			if !samePath(exportDir, dir) {
				return nil, fmt.Errorf("found no log of opencode's conversation %s", sessionID)
			}
		} else if !slices.ContainsFunc(openCodeSessions(s, dir), func(ses openCodeSession) bool { return ses.ID == sessionID }) {
			return nil, fmt.Errorf("found no log of opencode's conversation %s", sessionID)
		}
	}
	return turns, nil
}

// runFor runs cmd for at most d, and returns what it wrote.
func runFor(cmd *exec.Cmd, d time.Duration) ([]byte, error) {
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = proc.Output(cmd)
		close(done)
	}()
	select {
	case <-done:
		return out, err
	case <-time.After(d):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return nil, errors.New("took too long")
	}
}

// parseOpenCodeExport reads `opencode session export`, in the shape it
// has had over versions: messages with a type (or info.role) and their
// content (or parts) of text, reasoning and tool calls.
func parseOpenCodeExport(data []byte) ([]turn, error) {
	turns, _, err := parseOpenCodeExportWithDir(data)
	return turns, err
}

func parseOpenCodeExportWithDir(data []byte) ([]turn, string, error) {
	var ex struct {
		Info struct {
			Directory string `json:"directory"`
			Path      struct {
				Cwd string `json:"cwd"`
			} `json:"path"`
		} `json:"info"`
		Messages []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Info struct {
				Role string `json:"role"`
			} `json:"info"`
			Content []openCodePart `json:"content"`
			Parts   []openCodePart `json:"parts"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &ex); err != nil {
		return nil, "", fmt.Errorf("opencode's export is not what nerdr knows: %v", err)
	}
	dir := ex.Info.Directory
	if dir == "" {
		dir = ex.Info.Path.Cwd
	}
	var turns []turn
	for _, m := range ex.Messages {
		role := m.Type
		if role == "" {
			role = m.Info.Role
		}
		who := "user"
		if role == "assistant" {
			who = string(OpenCode)
		} else if role != "user" {
			continue
		}
		if who == "user" && said(m.Text) {
			turns = append(turns, turn{"user", m.Text})
		}
		for _, p := range append(m.Content, m.Parts...) {
			switch p.Type {
			case "text":
				if (who == "user" && said(p.Text)) || (who != "user" && strings.TrimSpace(p.Text) != "") {
					turns = append(turns, turn{who, p.Text})
				}
			case "tool":
				name := p.Name
				if name == "" {
					name = p.Tool
				}
				turns = append(turns, turn{"tool", toolLine(name, p.State.Input)})
			}
		}
	}
	return turns, dir, nil
}

type openCodePart struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Name  string `json:"name"`
	Tool  string `json:"tool"`
	State struct {
		Input json.RawMessage `json:"input"`
	} `json:"state"`
}

// copilotTurns reads the events the Copilot CLI logs for a session: the
// user's and the assistant's messages, and the tools it asked to run.
func copilotTurns(sessionID, dir string) ([]turn, error) {
	if sessionID == "" {
		sessionID = latestCopilotSession(dir, nil)
	}
	if !ValidSessionID(sessionID) {
		return nil, errors.New("found no copilot conversation started in this worktree")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	if dir != "" && !copilotInDir(home, sessionID, dir) {
		return nil, fmt.Errorf("found no log of copilot's conversation %s", sessionID)
	}
	path := filepath.Join(home, ".copilot", "session-state", sessionID, "events.jsonl")
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("found no log of copilot's conversation %s", sessionID)
	}
	var turns []turn
	err = eachLine(path, func(b []byte) {
		var e struct {
			Type string `json:"type"`
			Data struct {
				Content      string `json:"content"`
				ToolRequests []struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"toolRequests"`
			} `json:"data"`
		}
		if json.Unmarshal(b, &e) != nil {
			return
		}
		switch e.Type {
		case "user.message":
			if said(e.Data.Content) {
				turns = append(turns, turn{"user", e.Data.Content})
			}
		case "assistant.message":
			if strings.TrimSpace(e.Data.Content) != "" {
				turns = append(turns, turn{string(Copilot), e.Data.Content})
			}
			for _, r := range e.Data.ToolRequests {
				turns = append(turns, turn{"tool", toolLine(r.Name, r.Arguments)})
			}
		}
	})
	return turns, err
}

func copilotInDir(home, sessionID, dir string) bool {
	ws := filepath.Join(home, ".copilot", "session-state", sessionID, "workspace.yaml")
	data, err := os.ReadFile(ws)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "cwd:"); ok {
			return samePath(strings.Trim(strings.TrimSpace(v), `"'`), dir)
		}
	}
	return false
}

// cursorTranscript is the transcript cursor-agent writes of chat id, under
// a folder named after the directory it ran in: what the user and the
// agent said, and the tools it ran.
func cursorTranscript(id, dir string) string {
	root := os.Getenv("CURSOR_DATA_DIR")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		root = filepath.Join(home, ".cursor")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	project := strings.Trim(nonAlnum.ReplaceAllString(abs, "-"), "-")
	ts := filepath.Join(root, "projects", project, "agent-transcripts")
	for _, f := range []string{filepath.Join(ts, id, id+".jsonl"), filepath.Join(ts, id+".jsonl")} {
		if _, err := os.Stat(f); err == nil {
			return f
		}
	}
	return ""
}

var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// cursorTurns reads cursor-agent's transcript of the chat: one line per
// message, its content text and tool_use parts.
func cursorTurns(sessionID, dir string) ([]turn, error) {
	if sessionID == "" {
		sessionID = latestCursorChat(dir, nil)
	}
	if sessionID == "" || strings.ContainsAny(sessionID, `/\`) {
		return nil, errors.New("found no cursor chat started in this worktree")
	}
	path := cursorTranscript(sessionID, dir)
	if path == "" {
		return nil, fmt.Errorf("found no transcript of cursor's chat %s", sessionID)
	}
	var turns []turn
	err := eachLine(path, func(b []byte) {
		var e struct {
			Role    string `json:"role"`
			Message struct {
				Content []struct {
					Type  string          `json:"type"`
					Text  string          `json:"text"`
					Name  string          `json:"name"`
					Input json.RawMessage `json:"input"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(b, &e) != nil {
			return
		}
		for _, p := range e.Message.Content {
			switch {
			case p.Type == "text" && e.Role == "user":
				if text := unwrapTag(p.Text, "user_query"); said(text) {
					turns = append(turns, turn{"user", text})
				}
			case p.Type == "text" && e.Role == "assistant" && strings.TrimSpace(p.Text) != "":
				turns = append(turns, turn{string(Cursor), p.Text})
			case p.Type == "tool_use" && e.Role == "assistant":
				turns = append(turns, turn{"tool", toolLine(p.Name, p.Input)})
			}
		}
	})
	return turns, err
}

// unwrapTag is text without the <tag> … </tag> around it, as cursor
// wraps what the user typed.
func unwrapTag(text, tag string) string {
	t := strings.TrimSpace(text)
	if in, ok := strings.CutPrefix(t, "<"+tag+">"); ok {
		if in, ok := strings.CutSuffix(in, "</"+tag+">"); ok {
			return strings.TrimSpace(in)
		}
	}
	return text
}

// geminiMessage is one message in Gemini CLI's log.
type geminiMessage struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Content   json.RawMessage `json:"content"`
	ToolCalls []struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"toolCalls"`
}

// geminiLog is the log of Gemini CLI's conversation sessionID in dir.
func geminiLog(sessionID, dir string) (string, error) {
	if sessionID == "" {
		_, path := latestGeminiSession(dir, nil)
		if path == "" {
			return "", errors.New("found no gemini conversation started in this worktree")
		}
		return path, nil
	}
	if !ValidSessionID(sessionID) || len(sessionID) < 8 {
		return "", fmt.Errorf("%q is no gemini conversation id", sessionID)
	}
	if dir != "" {
		chats := geminiChats(dir)
		if chats == "" {
			return "", fmt.Errorf("found no log of gemini's conversation %s", sessionID)
		}
		files, _ := filepath.Glob(filepath.Join(chats, "session-*-"+sessionID[:8]+".json*"))
		for _, f := range files {
			if geminiSessionID(f) == sessionID {
				return f, nil
			}
		}
		return "", fmt.Errorf("found no log of gemini's conversation %s", sessionID)
	}
	// The file is named after the start of the id; the id itself is in
	// the file, under the folder's project, which this one may not know.
	g := geminiHome()
	files, _ := filepath.Glob(filepath.Join(g, "tmp", "*", "chats", "session-*-"+sessionID[:8]+".json*"))
	for _, f := range files {
		if geminiSessionID(f) == sessionID {
			return f, nil
		}
	}
	return "", fmt.Errorf("found no log of gemini's conversation %s", sessionID)
}

// geminiTurns reads Gemini CLI's log: a .jsonl of records (the
// conversation's metadata, then messages, a message written again as it
// grows, and rewinds that drop the messages from one on), or, from older
// versions, a .json with them all in "messages".
func geminiTurns(sessionID, dir string) ([]turn, error) {
	path, err := geminiLog(sessionID, dir)
	if err != nil {
		return nil, err
	}
	var order []string
	byID := map[string]geminiMessage{}
	add := func(m geminiMessage) {
		if _, ok := byID[m.ID]; !ok {
			order = append(order, m.ID)
		}
		byID[m.ID] = m
	}
	if strings.HasSuffix(path, ".jsonl") {
		err = eachLine(path, func(b []byte) {
			var r struct {
				geminiMessage
				RewindTo *string `json:"$rewindTo"`
				Set      *struct {
					Messages []geminiMessage `json:"messages"`
				} `json:"$set"`
			}
			if json.Unmarshal(b, &r) != nil {
				return
			}
			switch {
			case r.RewindTo != nil:
				i := slices.Index(order, *r.RewindTo)
				if i < 0 {
					i = 0
				}
				for _, id := range order[i:] {
					delete(byID, id)
				}
				order = order[:i]
			case r.Set != nil && r.Set.Messages != nil:
				order, byID = nil, map[string]geminiMessage{}
				for _, m := range r.Set.Messages {
					add(m)
				}
			case r.ID != "":
				add(r.geminiMessage)
			}
		})
	} else {
		var data []byte
		if data, err = os.ReadFile(path); err == nil {
			var conv struct {
				Messages []geminiMessage `json:"messages"`
			}
			if err = json.Unmarshal(data, &conv); err == nil {
				for _, m := range conv.Messages {
					add(m)
				}
			}
		}
	}
	if err != nil {
		return nil, err
	}
	var turns []turn
	for _, id := range order {
		m := byID[id]
		text := geminiText(m.Content)
		switch m.Type {
		case "user":
			if said(text) {
				turns = append(turns, turn{"user", text})
			}
		case "gemini":
			if strings.TrimSpace(text) != "" {
				turns = append(turns, turn{string(Gemini), text})
			}
			for _, c := range m.ToolCalls {
				turns = append(turns, turn{"tool", toolLine(c.Name, c.Args)})
			}
		}
	}
	return turns, nil
}

// geminiText is a message's text: a string, or the text of its parts.
func geminiText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &parts)
	var out []string
	for _, p := range parts {
		if p.Text != "" {
			out = append(out, p.Text)
		}
	}
	return strings.Join(out, "\n")
}

// CodexHome is where codex keeps its sessions: CODEX_HOME, or ~/.codex.
func CodexHome() string { return codexHome() }

// CodexLog is the path of the log of codex's conversation sessionID, or
// "" when there is none.
func CodexLog(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	path, err := codexLog(sessionID, "")
	if err != nil {
		return ""
	}
	return path
}
