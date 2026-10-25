package hooks

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Command is `nerdr pane <subcommand> …`, the part of herdr's CLI that
// integrations call as "$HERDR_BIN_PATH pane report-agent …" rather than
// using the socket themselves. nerdr gives its agents HERDR_BIN_PATH
// pointing at itself, so those reports reach it the same way. It returns
// the exit status.
func Command(args []string, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: nerdr pane report-agent|report-agent-session|release-agent|report-metadata <pane> [--flags]")
		return 2
	}
	method, ok := map[string]string{
		"report-agent":         ReportAgent,
		"report-agent-session": ReportAgentSession,
		"release-agent":        ReleaseAgent,
		"report-metadata":      ReportMetadata,
	}[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "nerdr: pane %s is herdr's; nerdr takes agent reports only\n", args[0])
		return 2
	}
	pane, flags, argv := parse(args[1:])
	if pane == "" {
		pane = os.Getenv("HERDR_PANE_ID")
	}
	p := map[string]any{"pane_id": pane}
	if argv != nil {
		p["resume_argv"] = argv
	}
	for flag, key := range map[string]string{
		"source": "source", "agent": "agent", "state": "state", "message": "message",
		"agent-session-id": "agent_session_id", "agent-session-path": "agent_session_path",
	} {
		if v, ok := flags[flag]; ok {
			p[key] = v
		}
	}
	if v, ok := flags["seq"]; ok {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			fmt.Fprintln(stderr, "nerdr: --seq must be a number")
			return 2
		}
		p["seq"] = n
	}
	sock := os.Getenv("HERDR_SOCKET_PATH")
	if sock == "" {
		fmt.Fprintln(stderr, "nerdr: HERDR_SOCKET_PATH is not set: not running in a nerdr tab")
		return 1
	}
	if err := Send(sock, method, p); err != nil {
		fmt.Fprintln(stderr, "nerdr:", err)
		return 1
	}
	return 0
}

// parse splits herdr-style arguments into the positional pane id,
// --flag value / --flag=value pairs, and the command that resumes the
// agent's conversation, the words after "--". Flags nerdr has no use for
// (--token, --title, …) are read past like the rest.
func parse(args []string) (string, map[string]string, []string) {
	pane := ""
	flags := map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return pane, flags, append([]string{}, args[i+1:]...)
		}
		name, isFlag := strings.CutPrefix(a, "--")
		if !isFlag {
			if pane == "" {
				pane = a
			}
			continue
		}
		if k, v, ok := strings.Cut(name, "="); ok {
			flags[k] = v
			continue
		}
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
			flags[name] = args[i+1]
			i++
		} else {
			flags[name] = ""
		}
	}
	return pane, flags, nil
}
