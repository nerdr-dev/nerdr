package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// nerdr's own hooks in claude. Read off the screen, claude can look done
// while it is not — a screen no rule knows, a pause between steps — and a
// finished tab notifies. Claude's hooks say it exactly: Stop comes once the
// whole turn is over. So nerdr starts claude with these hooks (see
// ClaudeSettings), whether or not herdr's integration is installed; they
// report through `nerdr hook claude`, below.

// ClaudeSource is the source nerdr's claude hooks report as.
const ClaudeSource = "nerdr:claude"

// ClaudeSettings is the settings JSON nerdr passes claude with --settings:
// its hooks, each running `"$NERDR_BIN" hook claude`. A hook that cannot run
// (a sandbox without nerdr in it) says nothing, and the screen is read as
// before. With statusLine, nerdr's status line too, which reports the
// account's rate limits; see StatusLineCommand.
func ClaudeSettings(statusLine bool) string {
	cmd := `"$NERDR_BIN" hook claude >/dev/null 2>&1 || true`
	hook := func(matcher string) map[string]any {
		h := map[string]any{"hooks": []map[string]any{{"type": "command", "command": cmd}}}
		if matcher != "" {
			h["matcher"] = matcher
		}
		return h
	}
	settings := map[string]any{"hooks": map[string]any{
		"SessionStart":     []any{hook("")},
		"UserPromptSubmit": []any{hook("")},
		"PreToolUse":       []any{hook("*")},
		"PostToolUse":      []any{hook("*")},
		// Only the notifications that mean it waits for an answer.
		"Notification": []any{hook("permission_prompt|elicitation_dialog")},
		"Stop":         []any{hook("")},
		// Counted, for an agent waiting on one in the background; see
		// subagents.go.
		"SubagentStart": []any{hook("")},
		"SubagentStop":  []any{hook("")},
	}}
	if statusLine {
		settings["statusLine"] = map[string]any{"type": "command", "command": statusLineCommand}
	}
	b, _ := json.Marshal(settings)
	return string(b)
}

// ClaudeEvent is what claude gives a hook on stdin, as far as nerdr reads it.
type ClaudeEvent struct {
	Event     string `json:"hook_event_name"`
	SessionID string `json:"session_id"`
	Tool      string `json:"tool_name"`
	// Type is a Notification's type, and Message its text, for a claude
	// that does not give the type.
	Type    string `json:"notification_type"`
	Message string `json:"message"`
	// Source is why a session started: startup, resume, clear or compact.
	Source string `json:"source"`
	// AgentID is set when the event comes from a subagent, not the agent
	// in the tab.
	AgentID string `json:"agent_id"`
}

// ClaudeState is the state a claude hook event means, or "" for one that
// says nothing about it, with w its subagents.
func ClaudeState(ev ClaudeEvent, w SubagentWait) string {
	switch ev.Event {
	case "SubagentStart", "SubagentStop":
		return subagentState(w)
	}
	if ev.AgentID != "" {
		// A subagent's tools say nothing about the tab's agent: it may be
		// waiting on the subagent, or on the user's next prompt.
		return ""
	}
	switch ev.Event {
	case "SessionStart":
		if ev.Source == "compact" {
			// A compaction, which can come in the middle of a turn: the
			// agent goes on working after it.
			return ""
		}
		return "idle"
	case "Stop":
		return stopState(w)
	case "UserPromptSubmit", "PostToolUse":
		return "working"
	case "PreToolUse":
		switch ev.Tool {
		case "AskUserQuestion", "ExitPlanMode":
			return "blocked" // these tools are a question for the user
		}
		return "working"
	case "Notification":
		switch ev.Type {
		case "permission_prompt", "elicitation_dialog":
			return "blocked"
		case "":
			// "Claude needs your permission to use Bash"; not "Claude is
			// waiting for your input", which comes a minute into idling.
			if strings.Contains(strings.ToLower(ev.Message), "permission") {
				return "blocked"
			}
		}
	}
	return ""
}

// claudeReport is the report an event makes, or "" for none: its state,
// or, for a session start that says nothing about the state, which
// conversation the agent is now in, with w its subagents.
func claudeReport(ev ClaudeEvent, pane string, w SubagentWait) (string, map[string]any) {
	state := ClaudeState(ev, w)
	p := map[string]any{
		"pane_id": pane, "source": ClaudeSource, "agent": "claude",
		// Hooks can run side by side (tools in parallel): the newest wins.
		"seq": time.Now().UnixNano(),
	}
	if ev.SessionID != "" && ev.AgentID == "" {
		p["agent_session_id"] = ev.SessionID
	}
	if ev.Event == "SessionStart" && ev.Source != "" {
		p["session_start_source"] = ev.Source
	}
	switch {
	case state != "":
		p["state"] = state
		if ev.Event == "Stop" && ev.AgentID == "" && w.Going > 0 {
			p["background"] = true
		}
		return ReportAgent, p
	case ev.Event == "SessionStart" && p["agent_session_id"] != nil:
		return ReportAgentSession, p
	}
	return "", nil
}

// ClaudeCommand is `nerdr hook claude`: reads the event claude gives on
// stdin and reports the state it means to the tab. It returns the exit
// status; claude does nothing with it.
func ClaudeCommand(stdin io.Reader, stderr io.Writer) int {
	var ev ClaudeEvent
	if err := json.NewDecoder(stdin).Decode(&ev); err != nil {
		fmt.Fprintln(stderr, "nerdr hook claude: no event on stdin:", err)
		return 1
	}
	sock, pane := os.Getenv("NERDR_SOCKET"), os.Getenv("NERDR_TAB")
	if sock == "" || pane == "" {
		return 0
	}
	// A compaction goes on in the same conversation, its subagents too.
	newSession := ev.Event == "SessionStart" && ev.AgentID == "" && ev.Source != "compact"
	method, p := claudeReport(ev, pane, seeSubagents(subagentsFile("claude", pane), ev.Event, ev.AgentID, newSession))
	if method == "" {
		return 0
	}
	if err := Send(sock, method, p); err != nil {
		fmt.Fprintln(stderr, "nerdr hook claude:", err)
		return 1
	}
	return 0
}
