package hooks

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// nerdr's own hooks in codex, as in claude: read off the screen, codex can
// look done between two steps, and its hooks say it exactly — Stop when the
// turn is over, Interrupt when it was cut short. nerdr passes them to the
// codex it starts with -c (see CodexArgs), so a codex the agent runs itself
// has none; they report through `nerdr hook codex`, below.
//
// codex runs a hook only once the user has trusted it, which it keeps in
// ~/.codex/config.toml: trust given on the command line is ignored, and an
// untrusted hook stops codex at start to ask. So nerdr has codex trust its
// hooks (TrustCodexHooks) where the skills go in the home folder, and
// passes them only once they are trusted.

// CodexSource is the source nerdr's codex hooks report as.
const CodexSource = "nerdr:codex"

// codexHookCommand is what each of nerdr's codex hooks runs. A hook that
// cannot run (a sandbox without nerdr in it) says nothing.
const codexHookCommand = `"$NERDR_BIN" hook codex >/dev/null 2>&1 || true`

// codexEvents are the events nerdr hears of. SubagentStart and
// SubagentStop say nothing about the agent in the tab.
var codexEvents = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PermissionRequest", "PostToolUse", "Stop", "Interrupt"}

// CodexArgs are the arguments that give codex nerdr's hooks.
func CodexArgs() []string {
	var args []string
	for _, ev := range codexEvents {
		// codex allows Interrupt at most 3 s, and warns at start of any
		// longer: "clamping Interrupt hook timeout to 3s".
		timeout := 10
		if ev == "Interrupt" {
			timeout = 3
		}
		// A TOML literal string: the command has no single quote in it.
		args = append(args, "-c", fmt.Sprintf(`hooks.%s=[{hooks=[{type="command",command='%s',timeout=%d}]}]`, ev, codexHookCommand, timeout))
	}
	return args
}

// TrustCodexHooks reports whether codex, run as argv with env, trusts
// nerdr's hooks, asking codex's app server: the hash a hook is trusted by
// is codex's to work out. With write, the hooks not yet trusted are, in
// the user's config.toml, and wrote says so. A hook the user turned off
// stays off.
func TrustCodexHooks(argv, env []string, write bool) (trusted, wrote bool, err error) {
	if len(argv) == 0 {
		return false, false, errors.New("no codex command")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], append(append(append([]string{}, argv[1:]...), "app-server"), CodexArgs()...)...)
	cmd.Env = env
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home // no project's own hooks
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return false, false, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return false, false, err
	}
	if err := cmd.Start(); err != nil {
		return false, false, err
	}
	defer func() {
		_ = in.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	rpc := codexRPC{in: in, out: bufio.NewScanner(out)}
	rpc.out.Buffer(nil, 16<<20)
	if _, err := rpc.call(1, "initialize", map[string]any{"clientInfo": map[string]any{"name": "nerdr", "version": "1"}}); err != nil {
		return false, false, err
	}
	if err := rpc.send(map[string]any{"jsonrpc": "2.0", "method": "initialized"}); err != nil {
		return false, false, err
	}
	res, err := rpc.call(2, "hooks/list", map[string]any{})
	if err != nil {
		return false, false, err
	}
	var list struct {
		Data []struct {
			Hooks []codexHook `json:"hooks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res, &list); err != nil {
		return false, false, err
	}
	var hooks []codexHook
	for _, e := range list.Data {
		for _, h := range e.Hooks {
			if h.Source == "sessionFlags" && h.Command == codexHookCommand {
				hooks = append(hooks, h)
			}
		}
	}
	if len(hooks) < len(codexEvents) {
		return false, false, fmt.Errorf("codex took %d of nerdr's %d hooks", len(hooks), len(codexEvents))
	}
	var edits []map[string]any
	for _, h := range hooks {
		if h.Enabled && h.Trust != "trusted" {
			edits = append(edits, map[string]any{
				"keyPath":       fmt.Sprintf(`hooks.state.%s.trusted_hash`, tomlQuote(h.Key)),
				"mergeStrategy": "upsert",
				"value":         h.Hash,
			})
		}
	}
	if len(edits) == 0 {
		return true, false, nil
	}
	if !write {
		return false, false, nil
	}
	if _, err := rpc.call(3, "config/batchWrite", map[string]any{"edits": edits}); err != nil {
		return false, false, err
	}
	return true, true, nil
}

// codexHook is a hook as codex's app server lists it.
type codexHook struct {
	Key     string `json:"key"`
	Command string `json:"command"`
	Source  string `json:"source"`
	Enabled bool   `json:"enabled"`
	Hash    string `json:"currentHash"`
	Trust   string `json:"trustStatus"`
}

func tomlQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// codexRPC talks JSON-RPC, a message a line, with codex's app server.
type codexRPC struct {
	in  io.Writer
	out *bufio.Scanner
}

func (c codexRPC) send(m map[string]any) error {
	b, _ := json.Marshal(m)
	_, err := c.in.Write(append(b, '\n'))
	return err
}

// call sends a request and returns its result, passing over the
// notifications that come before it.
func (c codexRPC) call(id int, method string, params any) (json.RawMessage, error) {
	if err := c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for c.out.Scan() {
		var r struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(c.out.Bytes(), &r) != nil || r.ID == nil || *r.ID != id {
			continue
		}
		if r.Error != nil {
			return nil, fmt.Errorf("codex %s: %s", method, r.Error.Message)
		}
		return r.Result, nil
	}
	if err := c.out.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("codex %s: no answer", method)
}

// CodexEvent is what codex gives a hook on stdin, as far as nerdr reads it.
type CodexEvent struct {
	Event     string `json:"hook_event_name"`
	SessionID string `json:"session_id"`
	// Source is why a session started: startup, resume or clear.
	Source string `json:"source"`
	// AgentID is set when the event comes from a subagent, not the agent
	// in the tab. Its session_id is the agent's.
	AgentID string `json:"agent_id"`
}

// CodexState is the state a codex hook event means, or "" for one that
// says nothing about it.
func CodexState(ev CodexEvent) string {
	if ev.Event == "PermissionRequest" {
		return "blocked" // a subagent's question waits for the user too
	}
	if ev.AgentID != "" {
		// A subagent's tools say nothing about the tab's agent, and its
		// end (SubagentStop) is not the end of the turn.
		return ""
	}
	switch ev.Event {
	case "SessionStart":
		if ev.Source == "compact" {
			return "" // in the middle of a turn
		}
		return "idle"
	case "Stop", "Interrupt":
		return "idle"
	case "UserPromptSubmit", "PreToolUse", "PostToolUse":
		return "working"
	}
	return ""
}

// codexReport is the report an event makes, or "" for none: its state,
// or, for a session start that says nothing about the state, which
// conversation the agent is now in.
func codexReport(ev CodexEvent, pane string) (string, map[string]any) {
	state := CodexState(ev)
	p := map[string]any{
		"pane_id": pane, "source": CodexSource, "agent": "codex",
		// Hooks can run side by side: the newest wins.
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
		return ReportAgent, p
	case ev.Event == "SessionStart" && p["agent_session_id"] != nil:
		return ReportAgentSession, p
	}
	return "", nil
}

// CodexCommand is `nerdr hook codex`: reads the event codex gives on stdin
// and reports the state it means to the tab. It returns the exit status;
// the hook's command line ignores it.
func CodexCommand(stdin io.Reader, stderr io.Writer) int {
	var ev CodexEvent
	if err := json.NewDecoder(stdin).Decode(&ev); err != nil {
		fmt.Fprintln(stderr, "nerdr hook codex: no event on stdin:", err)
		return 1
	}
	sock, pane := os.Getenv("NERDR_SOCKET"), os.Getenv("NERDR_TAB")
	method, p := codexReport(ev, pane)
	if method == "" || sock == "" || pane == "" {
		return 0
	}
	if err := Send(sock, method, p); err != nil {
		fmt.Fprintln(stderr, "nerdr hook codex:", err)
		return 1
	}
	return 0
}
