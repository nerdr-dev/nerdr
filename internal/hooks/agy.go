package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/denied"
)

// nerdr's own hooks in Antigravity (agy), as in claude: read off the
// screen, agy can look done between two steps, and no rule knows its idle
// screen. Its hooks say it exactly: Stop once the turn is over. agy takes
// hooks only from files — ~/.gemini/config/hooks.json, or the project's
// .agents/hooks.json — whose top-level keys each name a set of hooks; nerdr
// keeps its own under "nerdr" (see InstallAgyHooks), along with the skills.
// They report through `nerdr hook agy`, below.
//
// An agy the agent runs reads the same files, and inherits the tab's
// environment. So a hook reports only for an agy whose parent is the nerdr
// named in NERDR_AGY_HOOK, which nerdr gives the agy it starts: that one
// is nerdr's own child, one the agent runs is not.

// AgySource is the source nerdr's agy hooks report as.
const AgySource = "nerdr:agy"

// AgyHookName is the key nerdr's hooks are under in agy's hooks.json.
const AgyHookName = "nerdr"

// agyEvents are the events nerdr hears of. agy leaves the event's name out
// of what a hook gets, so each hook names it. PreToolUse is only for the
// tool that starts subagents: see agyTurn.
var agyEvents = []string{"SessionStart", "PreInvocation", "PreToolUse", "PostToolUse", "Stop"}

// agySubagentTool is the tool agy starts subagents with.
const agySubagentTool = "invoke_subagent"

// AgyHooksHome is agy's hooks file in the home folder, for every agy.
func AgyHooksHome(home string) string {
	return filepath.Join(home, ".gemini", "config", "hooks.json")
}

// AgyHooksProject is the one in dir, the folder an agy starts in: for one
// in a sandbox that sees little more than the worktree.
func AgyHooksProject(dir string) string {
	return filepath.Join(dir, ".agents", "hooks.json")
}

// AgyHooksExclude is the path, from the worktree root, of the hooks file
// nerdr makes in a worktree, for keeping it out of git.
const AgyHooksExclude = "/.agents/hooks.json"

// agyHooks is nerdr's set of hooks, as it goes under AgyHookName.
func agyHooks() map[string]any {
	hook := func(ev string) map[string]any {
		return map[string]any{
			"type":    "command",
			"command": `"$NERDR_BIN" hook agy ` + ev + ` "$PPID" >/dev/null 2>&1 || true`,
			"timeout": 10,
		}
	}
	set := map[string]any{}
	for _, ev := range agyEvents {
		switch ev {
		case "PreToolUse":
			// A tool's events go in a group with a matcher. This one
			// prints nothing, which leaves the decision as it was.
			set[ev] = []any{map[string]any{"matcher": agySubagentTool, "hooks": []any{hook(ev)}}}
		case "PostToolUse":
			set[ev] = []any{map[string]any{"matcher": "*", "hooks": []any{hook(ev)}}}
		default:
			set[ev] = []any{hook(ev)}
		}
	}
	return set
}

// InstallAgyHooks puts nerdr's hooks in the hooks file, keeping the rest
// of it as it was, and reports whether it wrote the file and whether it
// made it. A set of nerdr's the user turned off ("enabled": false) stays
// off.
func InstallAgyHooks(file string) (wrote, made bool, err error) {
	old, err := os.ReadFile(file)
	if err != nil && !os.IsNotExist(err) {
		return false, false, err
	}
	made = os.IsNotExist(err)
	keys, values, err := jsonObject(old)
	if err != nil {
		return false, false, fmt.Errorf("%s: %w", file, err)
	}
	set := agyHooks()
	if prev, ok := values[AgyHookName]; ok {
		var p struct {
			Enabled *bool `json:"enabled"`
		}
		if json.Unmarshal(prev, &p) == nil && p.Enabled != nil && !*p.Enabled {
			set["enabled"] = false
		}
	} else {
		keys = append(keys, AgyHookName)
	}
	values[AgyHookName], _ = json.Marshal(set)
	b, err := writeJSONObject(keys, values)
	if err != nil {
		return false, false, err
	}
	if bytes.Equal(old, b) {
		return false, false, nil
	}
	if err := denied.Err(os.MkdirAll(filepath.Dir(file), 0o755)); err != nil {
		return false, false, err
	}
	if err := denied.Err(os.WriteFile(file, b, 0o644)); err != nil {
		return false, false, err
	}
	return true, made, nil
}

// jsonObject reads a JSON object's members in their order; nothing reads
// as an empty object.
func jsonObject(b []byte) ([]string, map[string]json.RawMessage, error) {
	values := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, values, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, nil, errors.New("not a JSON object")
	}
	var keys []string
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		k, _ := t.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, nil, err
		}
		if _, dup := values[k]; !dup {
			keys = append(keys, k)
		}
		values[k] = v
	}
	if _, err := dec.Token(); err != nil {
		return nil, nil, err
	}
	return keys, values, nil
}

// writeJSONObject writes the members back in their order, indented.
func writeJSONObject(keys []string, values map[string]json.RawMessage) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			buf.WriteString(",")
		}
		name, _ := json.Marshal(k)
		var v bytes.Buffer
		if err := json.Indent(&v, values[k], "  ", "  "); err != nil {
			return nil, err
		}
		buf.WriteString("\n  ")
		buf.Write(name)
		buf.WriteString(": ")
		buf.Write(v.Bytes())
	}
	buf.WriteString("\n}\n")
	return buf.Bytes(), nil
}

// AgyEvent is what agy gives a hook on stdin, as far as nerdr reads it.
// Event is the one the hook was set up for.
type AgyEvent struct {
	Event          string `json:"-"`
	ConversationID string `json:"conversationId"`
	// FullyIdle is on a Stop: false while work the agent started goes on
	// in the background, a subagent's say, whose answer it takes up.
	FullyIdle *bool `json:"fullyIdle"`
	// ToolCall is the tool a tool's event is about.
	ToolCall struct {
		Name string `json:"name"`
		Args struct {
			Subagents []json.RawMessage `json:"Subagents"`
		} `json:"args"`
	} `json:"toolCall"`
	// Error is why the tool failed, on PostToolUse.
	Error string `json:"error"`
}

// AgyState is the state an event of the agent's own conversation means,
// or "" for one that says nothing about it.
func AgyState(ev AgyEvent) string {
	switch ev.Event {
	case "PreInvocation", "PostToolUse":
		// The model is called, or goes on after a tool: after a
		// permission too, which agy has no hook for.
		return "working"
	case "Stop":
		if ev.FullyIdle != nil && !*ev.FullyIdle {
			return "working" // its subagents are still at it
		}
		return "idle" // done, failed or cancelled
	}
	// SessionStart says which conversation, not that the agent is idle.
	return ""
}

// agyTurn is what the hooks of one agy have seen, kept between them: its
// own conversation, and how many subagents it has started that have not
// yet. A subagent runs in the same agy, in a conversation of its own, with
// hooks of its own — its Stop too — and nothing in them says it is one. But
// the agent starts it with invoke_subagent, and then its conversation
// starts; another conversation starting is the agent's next (/clear).
type agyTurn struct {
	Main    string `json:"main"`
	Pending int    `json:"pending"`
}

// see takes ev, and reports whether it is of the agent's own conversation.
func (t *agyTurn) see(ev AgyEvent) bool {
	c := ev.ConversationID
	if t.Main == "" {
		t.Main = c // a resumed conversation has no SessionStart
	}
	if ev.Event == "SessionStart" && c != t.Main {
		if t.Pending == 0 {
			t.Main = c
			return true
		}
		t.Pending--
	}
	if c != t.Main {
		return false
	}
	if ev.ToolCall.Name == agySubagentTool {
		n := len(ev.ToolCall.Args.Subagents)
		switch {
		case ev.Event == "PreToolUse":
			t.Pending += n
		case ev.Event == "PostToolUse" && ev.Error != "":
			t.Pending = max(0, t.Pending-n) // none of them started
		}
	}
	return true
}

// agyTurnFile is where the hooks of the agy with pid keep their agyTurn.
func agyTurnFile(nerdr, agy int) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("nerdr-agy-%d-%d.json", nerdr, agy))
}

// seeAgy takes ev in the agyTurn kept in file, and reports whether it is
// of the agent's own conversation.
func seeAgy(file string, ev AgyEvent) bool {
	var t agyTurn
	if b, err := os.ReadFile(file); err == nil {
		_ = json.Unmarshal(b, &t)
	}
	before := t
	own := t.see(ev)
	if t != before {
		// Whole or not at all, for a subagent's hook reading it meanwhile.
		b, _ := json.Marshal(t)
		tmp := fmt.Sprintf("%s.%d", file, os.Getpid())
		if os.WriteFile(tmp, b, 0o600) == nil {
			_ = os.Rename(tmp, file)
		}
	}
	return own
}

// agyReport is the report an event makes, or "" for none: its state, or,
// for a session start, which conversation the agent is now in.
func agyReport(ev AgyEvent, pane string) (string, map[string]any) {
	state := AgyState(ev)
	p := map[string]any{
		"pane_id": pane, "source": AgySource, "agent": "agy",
		"seq": time.Now().UnixNano(),
	}
	if ev.ConversationID != "" {
		p["agent_session_id"] = ev.ConversationID // a subagent's is not reported
	}
	switch {
	case state != "":
		p["state"] = state
		return ReportAgent, p
	case ev.Event == "SessionStart" && p["agent_session_id"] != nil:
		return ReportAgentSession, p
	}
	return "", nil
}

// parentOf is the parent of process pid, or 0 when it is not known.
var parentOf = func(pid int) int {
	out, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return n
}

// AgyCommand is `nerdr hook agy <event> <agy's pid>`: reads the event agy
// gives on stdin and reports the state it means to the tab, when the agy
// is the one nerdr started and the event is not a subagent's. It returns
// the exit status; the hook's command line ignores it.
func AgyCommand(args []string, stdin io.Reader, stderr io.Writer) int {
	var ev AgyEvent
	if err := json.NewDecoder(stdin).Decode(&ev); err != nil {
		fmt.Fprintln(stderr, "nerdr hook agy: no event on stdin:", err)
		return 1
	}
	if len(args) < 2 {
		fmt.Fprintln(stderr, "usage: nerdr hook agy <event> <agy's pid>")
		return 2
	}
	ev.Event = args[0]
	sock, pane := os.Getenv("NERDR_SOCKET"), os.Getenv("NERDR_TAB")
	nerdr, _ := strconv.Atoi(os.Getenv("NERDR_AGY_HOOK"))
	agy, _ := strconv.Atoi(args[1])
	if sock == "" || pane == "" || nerdr <= 0 || agy <= 0 || parentOf(agy) != nerdr {
		return 0
	}
	if !seeAgy(agyTurnFile(nerdr, agy), ev) {
		return 0
	}
	method, p := agyReport(ev, pane)
	if method == "" {
		return 0
	}
	if err := Send(sock, method, p); err != nil {
		fmt.Fprintln(stderr, "nerdr hook agy:", err)
		return 1
	}
	return 0
}
