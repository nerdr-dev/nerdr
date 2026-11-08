package hooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/nerdr-dev/nerdr/internal/safefile"
)

// nerdr's own hooks in Grok Build: they say which conversation it is in,
// also after /new or /resume, which the screen does not, and exactly when
// a turn ends and when it asks for permission. grok takes hooks only from
// files, so nerdr keeps a file of its own among them, along with the
// skills. They report through `nerdr hook grok`, below.
//
// grok runs no hook in a folder the user has not trusted: until they say
// yes to its question the tab is read from the screen.
//
// A grok the agent runs reads the same file, and inherits the tab's
// environment. So a hook reports only for a grok whose parent is the nerdr
// named in NERDR_GROK_HOOK, which nerdr gives the grok it starts.

// GrokSource is the source nerdr's grok hooks report as.
const GrokSource = "nerdr:grok"

// grokMarker is in a hooks file of nerdr's: a file there without it is the
// user's own, and stays as it is.
const grokMarker = " hook grok "

// grokEvents are the events nerdr hears of. Each hook names its event, as
// agy's do: the name grok gives on stdin is spelled another way.
var grokEvents = []string{
	"SessionStart", "UserPromptSubmit", "PostToolUse", "PostToolUseFailure",
	"PermissionDenied", "Notification", "Stop", "StopFailure", "StopCancelled",
}

// GrokHooksHome is nerdr's hooks file in the home folder, for every grok.
func GrokHooksHome(home string) string {
	return filepath.Join(home, ".grok", "hooks", "nerdr.json")
}

// GrokHooksProject is the one in dir, the folder a grok starts in: for one
// in a sandbox that sees little more than the worktree.
func GrokHooksProject(dir string) string {
	return filepath.Join(dir, ".grok", "hooks", "nerdr.json")
}

// GrokHooksExclude is the path, from the worktree root, of the hooks file
// nerdr makes in a worktree, for keeping it out of git.
const GrokHooksExclude = "/.grok/hooks/nerdr.json"

// grokHooks is the hooks file's text.
func grokHooks() []byte {
	set := map[string]any{}
	for _, ev := range grokEvents {
		set[ev] = []any{map[string]any{"hooks": []any{map[string]any{
			"type":    "command",
			"command": `"$NERDR_BIN" hook grok ` + ev + ` "$PPID" >/dev/null 2>&1 || true`,
			"timeout": 10,
		}}}}
	}
	b, _ := json.MarshalIndent(map[string]any{"hooks": set}, "", "  ")
	return append(b, '\n')
}

// InstallGrokHooks writes nerdr's hooks file, and reports whether it wrote
// it and whether it made it. A file there that is not nerdr's is left.
func InstallGrokHooks(file string) (wrote, made bool, err error) {
	home, herr := os.UserHomeDir()
	isHome := herr == nil && home != "" && filepath.Clean(file) == filepath.Clean(GrokHooksHome(home))
	return installGrokHooks(file, !isHome)
}

// InstallGrokHooksProject writes it in a worktree, refusing symbolic links
// anywhere inside the worktree.
func InstallGrokHooksProject(wtDir string) (wrote, made bool, err error) {
	return installGrokHooks(GrokHooksProject(wtDir), true)
}

func installGrokHooks(file string, worktree bool) (wrote, made bool, err error) {
	root, rel, err := safefile.OpenFileRoot(file, ".grok", worktree)
	if err != nil {
		return false, false, err
	}
	defer root.Close()
	old, err := safefile.ReadRegular(root, rel)
	if err != nil && !os.IsNotExist(err) {
		return false, false, err
	}
	made = os.IsNotExist(err)
	if !made && !bytes.Contains(old, []byte(grokMarker)) {
		return false, false, nil
	}
	b := grokHooks()
	if bytes.Equal(old, b) {
		return false, false, nil
	}
	if err := safefile.WriteRegular(root, rel, b, 0o644); err != nil {
		return false, false, err
	}
	return true, made, nil
}

// GrokEvent is what grok gives a hook on stdin, in part, with the event's
// name from the hook's command line.
type GrokEvent struct {
	Event     string `json:"-"`
	SessionID string `json:"sessionId"`
	// SubagentType is set on an event from inside a subagent, which has a
	// conversation of its own.
	SubagentType string `json:"subagentType"`
	// NotificationType says what a Notification is about.
	NotificationType string `json:"notificationType"`
}

// GrokState is the state an event means, or "" for one that says nothing
// about it.
func GrokState(ev GrokEvent) string {
	switch ev.Event {
	case "UserPromptSubmit", "PostToolUse", "PostToolUseFailure", "PermissionDenied":
		// A permission denied by a rule, with no question: the turn goes
		// on. One the user declined ends it, in StopCancelled.
		return "working"
	case "Notification":
		switch ev.NotificationType {
		case "permission_prompt":
			return "blocked"
		case "idle_prompt":
			return "idle"
		}
		return ""
	case "Stop", "StopFailure", "StopCancelled":
		return "idle" // done, failed or interrupted
	}
	// SessionStart says which conversation, not that the agent is idle.
	return ""
}

// grokReport is the report an event makes, or "" for none: its state, or,
// for a session start, which conversation the agent is now in. grok fires
// no session start for a subagent. Only a session start says the
// conversation: at its end grok stops each it had open, after /new the old
// one last, and the tab would go back to that one.
func grokReport(ev GrokEvent, pane string) (string, map[string]any) {
	if ev.SubagentType != "" {
		return "", nil
	}
	p := map[string]any{
		"pane_id": pane, "source": GrokSource, "agent": "grok",
		"seq": time.Now().UnixNano(),
	}
	state := GrokState(ev)
	switch {
	case state != "":
		p["state"] = state
		return ReportAgent, p
	case ev.Event == "SessionStart" && ev.SessionID != "":
		p["agent_session_id"] = ev.SessionID
		return ReportAgentSession, p
	}
	return "", nil
}

// GrokCommand is `nerdr hook grok <event> <grok's pid>`: reads the event
// grok gives on stdin and reports what it means to the tab, when the grok
// is the one nerdr started. It returns the exit status; the hook's command
// line ignores it.
func GrokCommand(args []string, stdin io.Reader, stderr io.Writer) int {
	var ev GrokEvent
	if err := json.NewDecoder(stdin).Decode(&ev); err != nil {
		fmt.Fprintln(stderr, "nerdr hook grok: no event on stdin:", err)
		return 1
	}
	if len(args) < 2 {
		fmt.Fprintln(stderr, "usage: nerdr hook grok <event> <grok's pid>")
		return 2
	}
	ev.Event = args[0]
	sock, pane := os.Getenv("NERDR_SOCKET"), os.Getenv("NERDR_TAB")
	nerdr, _ := strconv.Atoi(os.Getenv("NERDR_GROK_HOOK"))
	grok, _ := strconv.Atoi(args[1])
	if sock == "" || pane == "" || nerdr <= 0 || grok <= 0 || parentOf(grok) != nerdr {
		return 0
	}
	method, p := grokReport(ev, pane)
	if method == "" {
		return 0
	}
	if err := SendReport(sock, method, p); err != nil {
		fmt.Fprintln(stderr, "nerdr hook grok:", err)
		return 1
	}
	return 0
}
