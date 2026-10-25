package hooks

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"nerdr/internal/denied"
)

// nerdr's own plugin in opencode, as its hooks are in claude: read off the
// screen, opencode can look done between two steps, and no rule knows its
// idle screen. Its events say it exactly: a turn's execution starts, and
// succeeds, fails or is interrupted. The plugin (opencode.js) passes them
// on to `nerdr hook opencode`, below.
//
// opencode runs plugins in its server: the one it starts with --standalone
// has the tab's environment, the shared background service has none, and
// there the plugin does nothing (see agents' startArgs). A server evicts
// an idle location, and boots it again — plugin included — in the same
// process at the next message, so the plugin remembers on globalThis that
// this process is the tab's agent's, and numbers the setups so that the
// newest alone reports if the streams of an older one live on.

// OpenCodeSource is the source nerdr's opencode plugin reports as.
const OpenCodeSource = "nerdr:opencode"

// OpenCodePlugin is the plugin, as nerdr installs it.
//
//go:embed opencode.js
var OpenCodePlugin []byte

// OpenCodeMarker is in the plugin nerdr installs. A file with it is
// nerdr's, and is brought up to date; one without it — the user took the
// line out to keep their own edits — is left alone.
const OpenCodeMarker = "// nerdr: installed and kept up to date by nerdr."

// OpenCodePluginHome is where the plugin goes in the home folder, for
// every opencode.
func OpenCodePluginHome(home string) string {
	return filepath.Join(home, ".config", "opencode", "plugins", "nerdr.js")
}

// OpenCodePluginProject is where it goes in dir, the folder an opencode
// starts in: for one in a sandbox that sees little more than the worktree.
func OpenCodePluginProject(dir string) string {
	return filepath.Join(dir, ".opencode", "plugins", "nerdr.js")
}

// OpenCodePluginExclude is the path, from the worktree root, the plugin
// put in a worktree takes up, for keeping it out of git.
const OpenCodePluginExclude = "/.opencode/plugins/nerdr.js"

// InstallOpenCodePlugin puts the plugin in file, and reports whether it
// wrote it. One already there is replaced only when it is nerdr's and
// differs.
func InstallOpenCodePlugin(file string) (bool, error) {
	old, err := os.ReadFile(file)
	switch {
	case err == nil && (bytes.Equal(old, OpenCodePlugin) || !bytes.Contains(old, []byte(OpenCodeMarker))):
		return false, nil
	case err != nil && !os.IsNotExist(err):
		return false, err
	}
	if err := denied.Err(os.MkdirAll(filepath.Dir(file), 0o755)); err != nil {
		return false, err
	}
	if err := denied.Err(os.WriteFile(file, OpenCodePlugin, 0o644)); err != nil {
		return false, err
	}
	return true, nil
}

// OpenCodeEvent is what the plugin gives `nerdr hook opencode` on stdin.
type OpenCodeEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionID"`
	// ParentID is set when the event is a subagent's session's, not the
	// one of the agent in the tab.
	ParentID string `json:"parentID"`
	// Created is when opencode made the event, in milliseconds; Seq orders
	// it among the plugin's reports.
	Created int64 `json:"created"`
	Seq     int64 `json:"seq"`
}

// OpenCodeState is the state an opencode event means, or "" for one that
// says nothing about it.
func OpenCodeState(ev OpenCodeEvent) string {
	switch ev.Type {
	case "session.execution.started":
		if ev.ParentID != "" {
			return "" // a subagent's turn: the agent's own goes on around it
		}
		return "working"
	case "session.execution.succeeded", "session.execution.failed", "session.execution.interrupted":
		if ev.ParentID != "" {
			return "" // a subagent done: the agent works on with its answer
		}
		return "idle"
	case "permission.asked":
		return "blocked" // a subagent's question waits for the user too
	case "permission.replied":
		return "working"
	}
	return ""
}

// openCodeReport is the report an event makes, or "" for none: its state,
// or, for a new conversation, which one the agent is now in.
func openCodeReport(ev OpenCodeEvent, pane string) (string, map[string]any) {
	seq := ev.Seq
	if seq <= 0 {
		seq = time.Now().UnixMicro()
	}
	p := map[string]any{"pane_id": pane, "source": OpenCodeSource, "agent": "opencode", "seq": seq}
	if ev.SessionID != "" && ev.ParentID == "" {
		p["agent_session_id"] = ev.SessionID
	}
	if state := OpenCodeState(ev); state != "" {
		p["state"] = state
		return ReportAgent, p
	}
	if ev.Type == "session.created" && p["agent_session_id"] != nil {
		return ReportAgentSession, p
	}
	return "", nil
}

// OpenCodeCommand is `nerdr hook opencode`: reads the event the plugin
// gives on stdin and reports what it means to the tab. It returns the exit
// status; the plugin does nothing with it.
func OpenCodeCommand(stdin io.Reader, stderr io.Writer) int {
	var ev OpenCodeEvent
	if err := json.NewDecoder(stdin).Decode(&ev); err != nil {
		fmt.Fprintln(stderr, "nerdr hook opencode: no event on stdin:", err)
		return 1
	}
	sock, pane := os.Getenv("NERDR_SOCKET"), os.Getenv("NERDR_TAB")
	method, p := openCodeReport(ev, pane)
	if method == "" || sock == "" || pane == "" {
		return 0
	}
	if err := Send(sock, method, p); err != nil {
		fmt.Fprintln(stderr, "nerdr hook opencode:", err)
		return 1
	}
	return 0
}
