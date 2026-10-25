package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"nerdr/internal/codereview"
	"nerdr/internal/hooks"
)

// readyUsage is written for the agent, as whyUsage is.
const readyUsage = `usage: nerdr ready [--agent KIND [--model MODEL]] [FOCUS]    (or - to read FOCUS from stdin)

Says your work in this tab is ready for a code review: a second agent
reviews it once your turn ends, and its findings come
back to you as a message. FOCUS, if given, is what you want it to look at
most — a part you are unsure of — in a sentence or two.

--agent is the kind of agent to review with (codex, claude, …), and
--model the model it reviews with, in that agent's own names — only when
the user asked for them; without, the reviewer the user set up reviews.

Run it once, when you have finished what you were asked to do, then end
your turn. Unless nerdr told you code review is on in this tab, run it
only when the user has asked for a review or said yes to one.
`

// runReady is the "ready" subcommand: it tells the nerdr it runs in that
// the agent's work is ready for a review.
func runReady(args []string) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprint(os.Stderr, readyUsage)
		return 2
	}
	var kind, model string
	for len(args) > 1 && (args[0] == "--agent" || args[0] == "--model") {
		if args[0] == "--agent" {
			kind = args[1]
		} else {
			model = args[1]
		}
		args = args[2:]
	}
	if model != "" && kind == "" {
		fmt.Fprintln(os.Stderr, "nerdr ready: --model is the model of an agent: give --agent too")
		return 2
	}
	focus := strings.Join(args, " ")
	if len(args) == 1 && args[0] == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "nerdr ready:", err)
			return 1
		}
		focus = string(data)
	}
	focus = strings.TrimSpace(focus)
	if len(focus) > codereview.MaxFocus {
		fmt.Fprintf(os.Stderr, "nerdr ready: the focus is %d bytes; keep it under %d — the reviewer reads the change itself\n", len(focus), codereview.MaxFocus)
		return 2
	}
	tab, sock, ok := tabSocket("nerdr ready", "not running in a nerdr tab, so there is no nerdr to review the work")
	if !ok {
		return 1
	}
	if err := send(sock, hooks.Ready, map[string]any{"pane_id": tab, "focus": focus, "kind": kind, "model": model}); err != nil {
		fmt.Fprintln(os.Stderr, "nerdr ready:", err)
		return 1
	}
	fmt.Println("nerdr has it. End your turn now: the review starts then, and its findings come to you as a message.")
	return 0
}
