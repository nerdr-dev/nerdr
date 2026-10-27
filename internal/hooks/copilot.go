package hooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/nerdr-dev/nerdr/internal/safefile"
)

// nerdr's own hooks in copilot, as in claude: read off the screen, copilot
// can look done between two steps, and no rule knows its idle screen. Its
// hooks say it exactly: Stop once the turn is over. They come in a plugin
// of nerdr's (see InstallCopilotPlugin), which nerdr writes along with the
// skills and gives the copilot it starts with --plugin-dir, so a copilot
// the agent runs itself has none. They report through `nerdr hook
// copilot`, below.

// CopilotSource is the source nerdr's copilot hooks report as.
const CopilotSource = "nerdr:copilot"

// CopilotMarker is in the description of the plugin nerdr writes. A plugin
// with it is nerdr's, and is brought up to date; one without it — the user
// took it out to keep their own edits — is left alone.
const CopilotMarker = "Installed and kept up to date by nerdr"

// copilotEvents are the events nerdr hears of, in the form copilot shares
// with claude's hooks. Each hook names its event: copilot leaves the name
// out of some.
var copilotEvents = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure", "Notification", "Stop", "SubagentStart", "SubagentStop"}

// CopilotPluginHome is the plugin's folder in the home folder, for every
// copilot nerdr starts.
func CopilotPluginHome(home string) string {
	return filepath.Join(home, ".copilot", "nerdr-plugin")
}

// CopilotPluginProject is its folder in dir, the folder a copilot starts
// in: for one in a sandbox that sees little more than the worktree.
func CopilotPluginProject(dir string) string {
	return filepath.Join(dir, ".copilot", "nerdr-plugin")
}

// CopilotPluginExclude is the path, from the worktree root, the plugin
// put in a worktree takes up, for keeping it out of git.
const CopilotPluginExclude = "/.copilot/nerdr-plugin/"

// copilotPlugin is the plugin's files, by their path in its folder.
func copilotPlugin() map[string][]byte {
	manifest, _ := json.MarshalIndent(map[string]any{
		"name":        "nerdr",
		"description": "Tells nerdr when copilot is working, waiting or done. " + CopilotMarker + "; remove this sentence to keep your own edits.",
		"version":     "1.0.0",
	}, "", "  ")
	hooks := map[string]any{}
	for _, ev := range copilotEvents {
		hooks[ev] = []map[string]any{{
			"type":       "command",
			"bash":       `"$NERDR_BIN" hook copilot ` + ev + ` >/dev/null 2>&1 || true`,
			"timeoutSec": 10,
		}}
	}
	hooksJSON, _ := json.MarshalIndent(map[string]any{"version": 1, "hooks": hooks}, "", "  ")
	return map[string][]byte{
		filepath.Join(".plugin", "plugin.json"): append(manifest, '\n'),
		filepath.Join("hooks", "hooks.json"):    append(hooksJSON, '\n'),
	}
}

// InstallCopilotPlugin puts the plugin in dir, and reports whether it
// wrote it. One already there is replaced only when it is nerdr's and
// differs.
func InstallCopilotPlugin(dir string) (bool, error) {
	return installCopilotPlugin(dir, !isHomeCopilotPlugin(dir))
}

// InstallCopilotPluginProject puts the plugin in a worktree's
// .copilot/nerdr-plugin, refusing symbolic links anywhere inside the
// worktree.
func InstallCopilotPluginProject(wtDir string) (bool, error) {
	return installCopilotPlugin(CopilotPluginProject(wtDir), true)
}

func isHomeCopilotPlugin(dir string) bool {
	dir = filepath.Clean(dir)
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if dir == filepath.Clean(CopilotPluginHome(home)) {
			return true
		}
		if dir == filepath.Clean(filepath.Join(home, ".local", "state", "nerdr", "copilot-plugin")) {
			return true
		}
	}
	if stateHome := os.Getenv("XDG_STATE_HOME"); stateHome != "" {
		if dir == filepath.Clean(filepath.Join(stateHome, "nerdr", "copilot-plugin")) {
			return true
		}
	}
	return false
}

func installCopilotPlugin(dir string, worktree bool) (bool, error) {
	files := copilotPlugin()
	root, base, err := safefile.OpenDir(dir, ".copilot", worktree, true)
	if err != nil {
		return false, err
	}
	defer root.Close()
	manifest := filepath.Join(base, ".plugin", "plugin.json")
	old, err := safefile.ReadRegular(root, manifest)
	switch {
	case err == nil && !bytes.Contains(old, []byte(CopilotMarker)):
		return false, nil // the user's own
	case err != nil && !os.IsNotExist(err):
		return false, err
	}
	wrote := false
	for name, b := range files {
		file := filepath.Join(base, name)
		old, err := safefile.ReadRegular(root, file)
		if err == nil && bytes.Equal(old, b) {
			continue
		}
		if err != nil && !os.IsNotExist(err) {
			return false, err
		}
		if err := safefile.WriteRegular(root, file, b, 0o644); err != nil {
			return false, err
		}
		wrote = true
	}
	return wrote, nil
}

// HasCopilotPlugin reports whether there is a plugin in dir, nerdr's or
// the user's own copy of it.
func HasCopilotPlugin(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".plugin", "plugin.json"))
	return err == nil
}

// CopilotEvent is what copilot gives a hook on stdin, as far as nerdr
// reads it. Event is the one the hook was set up for.
type CopilotEvent struct {
	Event     string `json:"-"`
	SessionID string `json:"session_id"`
	// Source is why a session started: new or resume.
	Source string `json:"source"`
	// Type is a Notification's type.
	Type string `json:"notification_type"`
	// AgentID is set when the event comes from a subagent, not the agent
	// in the tab.
	AgentID string `json:"agent_id"`
}

// CopilotState is the state a copilot hook event means, or "" for one that
// says nothing about it, with w its subagents.
func CopilotState(ev CopilotEvent, w SubagentWait) string {
	if ev.Event == "Notification" && ev.Type == "permission_prompt" {
		// It waits for the user. Not PermissionRequest, which comes for
		// tools that are allowed anyway.
		return "blocked" // a subagent's question waits for the user too
	}
	switch ev.Event {
	case "SubagentStart", "SubagentStop":
		return subagentState(w)
	}
	if ev.AgentID != "" {
		// A subagent's events say nothing about the tab's agent: it has
		// a Stop of its own, in the middle of the agent's turn.
		return ""
	}
	switch ev.Event {
	case "Stop":
		return stopState(w)
	case "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure":
		return "working"
	}
	// SessionStart comes with the first prompt, after UserPromptSubmit: it
	// says which conversation, not that the agent is idle.
	return ""
}

// copilotReport is the report an event makes, or "" for none: its state,
// or, for a session start, which conversation the agent is now in, with w
// its subagents.
func copilotReport(ev CopilotEvent, pane string, w SubagentWait) (string, map[string]any) {
	state := CopilotState(ev, w)
	p := map[string]any{
		"pane_id": pane, "source": CopilotSource, "agent": "copilot",
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

// CopilotCommand is `nerdr hook copilot <event>`: reads the event copilot
// gives on stdin and reports the state it means to the tab. It returns the
// exit status; the hook's command line ignores it.
func CopilotCommand(args []string, stdin io.Reader, stderr io.Writer) int {
	var ev CopilotEvent
	if err := json.NewDecoder(stdin).Decode(&ev); err != nil {
		fmt.Fprintln(stderr, "nerdr hook copilot: no event on stdin:", err)
		return 1
	}
	if len(args) > 0 {
		ev.Event = args[0]
	}
	sock, pane := os.Getenv("NERDR_SOCKET"), os.Getenv("NERDR_TAB")
	if sock == "" || pane == "" {
		return 0
	}
	w := seeSubagents(subagentsFile("copilot", pane), ev.Event, ev.AgentID, ev.Event == "SessionStart" && ev.AgentID == "")
	method, p := copilotReport(ev, pane, w)
	if method == "" {
		return 0
	}
	if err := Send(sock, method, p); err != nil {
		fmt.Fprintln(stderr, "nerdr hook copilot:", err)
		return 1
	}
	return 0
}
