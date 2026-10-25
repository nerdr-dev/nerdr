package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/hooks"
)

// agentUsage is written for the agent, as sendUsage is.
const agentUsage = `usage: nerdr agent [--kind KIND] [--model MODEL] TASK    (or - to read TASK from stdin)

Starts another agent in this nerdr tab, in a pane beside yours, working in
the same worktree, with TASK as its first message. nerdr answers with its
number, N: it reports back to you with nerdr send when it is done, and you
reach it with nerdr send N.

KIND is the kind of agent (claude, codex, opencode, …); yours when left
out. nerdr says which kinds there are if it has none of that name.

MODEL is the model it runs, in the agent's own names ("haiku", "opus" or
"sonnet" for claude, "gpt-5.5" for codex, "provider/model" for opencode);
the one the user's config gives the kind when left out. A model the agent
does not have makes it stop at start, or say so.

Write TASK for an agent with none of your conversation: what to do, what
it needs to know, and which files are its to change. The agents share the
worktree, so give each its own part.
`

// runAgent is the "agent" subcommand: another agent in the tab, through
// the nerdr running it, which says its number.
func runAgent(args []string) int {
	kind, model := "", ""
flags:
	for len(args) > 0 {
		switch a := args[0]; {
		case a == "-h" || a == "--help":
			fmt.Fprint(os.Stderr, agentUsage)
			return 2
		case a == "--kind" && len(args) > 1:
			kind, args = args[1], args[2:]
			continue
		case strings.HasPrefix(a, "--kind="):
			kind, args = strings.TrimPrefix(a, "--kind="), args[1:]
			continue
		case a == "--model" && len(args) > 1:
			model, args = args[1], args[2:]
			continue
		case strings.HasPrefix(a, "--model="):
			model, args = strings.TrimPrefix(a, "--model="), args[1:]
			continue
		}
		break flags
	}
	text := strings.Join(args, " ")
	if len(args) == 1 && args[0] == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "nerdr agent:", err)
			return 1
		}
		text = string(data)
	}
	if strings.TrimSpace(text) == "" {
		fmt.Fprintf(os.Stderr, "nerdr agent: no task for the new agent\n\n%s", agentUsage)
		return 2
	}
	if len(text) > hooks.MaxText {
		fmt.Fprintf(os.Stderr, "nerdr agent: the task is %d bytes; keep it under %d\n", len(text), hooks.MaxText)
		return 2
	}
	tab, sock, ok := tabSocket("nerdr agent", "not running in a nerdr tab, so there is no tab to start an agent in")
	if !ok {
		return 1
	}
	answer, err := request(sock, hooks.NewAgent, map[string]any{"pane_id": tab, "kind": kind, "model": model, "text": text})
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr agent:", err)
		return 1
	}
	fmt.Println(answer)
	return 0
}
