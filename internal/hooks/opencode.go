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

	"github.com/nerdr-dev/nerdr/internal/safefile"
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

// OpenCodePluginHome is where the plugin goes for every opencode: in
// opencode's global config folder in the environment getenv reads, the
// one the agent starts with (see OpenCodeConfigDir).
func OpenCodePluginHome(home string, getenv func(string) string) string {
	return filepath.Join(OpenCodeConfigDir(home, getenv), "plugins", "nerdr.js")
}

// OpenCodeConfigDir is the global config folder opencode loads plugins
// from, in an environment getenv reads: OPENCODE_CONFIG_DIR, as it is
// given, when set; else opencode under XDG_CONFIG_HOME, when set; else
// ~/.config/opencode — the folder opencode itself takes for its config.
// opencode loads plugins from the XDG one too with OPENCODE_CONFIG_DIR
// set, so a plugin nerdr put there before is not lost, and a relative
// OPENCODE_CONFIG_DIR, which each opencode takes from the folder it
// starts in, is passed over for it. A relative XDG_CONFIG_HOME is passed
// over too, as XDG says: nerdr would write it under its own working
// folder, not the worktree an opencode starts in.
func OpenCodeConfigDir(home string, getenv func(string) string) string {
	if dir := getenv("OPENCODE_CONFIG_DIR"); filepath.IsAbs(dir) {
		return dir
	}
	if x := getenv("XDG_CONFIG_HOME"); filepath.IsAbs(x) {
		return filepath.Join(x, "opencode")
	}
	return filepath.Join(home, ".config", "opencode")
}

// OpenCodePluginProject is where it goes in dir, the folder an opencode
// starts in: for one in a sandbox that sees little more than the worktree.
func OpenCodePluginProject(dir string) string {
	return filepath.Join(dir, ".opencode", "plugins", "nerdr.js")
}

// OpenCodePluginExclude is the path, from the worktree root, the plugin
// put in a worktree takes up, for keeping it out of git.
const OpenCodePluginExclude = "/.opencode/plugins/nerdr.js"

// InstallOpenCodePluginHome puts the plugin in file, OpenCodePluginHome,
// and reports whether it wrote it. One already there is replaced only
// when it is nerdr's and differs. As in the home folder, symbolic links
// to the folder — dotfiles — are followed.
func InstallOpenCodePluginHome(file string) (bool, error) {
	return installOpenCodePlugin(file, false)
}

// InstallOpenCodePluginProject puts the plugin in a worktree's
// .opencode/plugins/nerdr.js, refusing symbolic links anywhere inside the
// worktree.
func InstallOpenCodePluginProject(wtDir string) (bool, error) {
	return installOpenCodePlugin(OpenCodePluginProject(wtDir), true)
}

func installOpenCodePlugin(file string, worktree bool) (bool, error) {
	root, rel, err := safefile.OpenFileRoot(file, ".opencode", worktree)
	if err != nil {
		return false, err
	}
	defer root.Close()
	old, err := safefile.ReadRegular(root, rel)
	switch {
	case err == nil && (bytes.Equal(old, OpenCodePlugin) || !bytes.Contains(old, []byte(OpenCodeMarker))):
		return false, nil
	case err != nil && !os.IsNotExist(err):
		return false, err
	}
	if err := safefile.WriteRegular(root, rel, OpenCodePlugin, 0o644); err != nil {
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
	if err := SendReport(sock, method, p); err != nil {
		fmt.Fprintln(stderr, "nerdr hook opencode:", err)
		return 1
	}
	return 0
}
