package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/handoff"
	"github.com/nerdr-dev/nerdr/internal/hooks"
)

// handoffUsage is written for the agent, as whyUsage is.
const handoffUsage = `usage: nerdr handoff FILE    (or - to read it from stdin)

Hands your work to the agent this tab is being switched to, which has none
of your conversation. Write everything it needs to carry on: the goal and
why, what has been decided and why (options rejected too), what you found
out, what is done and what is left, and how to check it.

nerdr types it into the new agent as its first message. When the user is
switching the tab's agent, nerdr stops you once it has the handoff; else it
is kept for the next switch.
`

// runHandoff is the "handoff" subcommand: it sends what the agent wrote to
// the nerdr the agent runs in.
func runHandoff(args []string) int {
	if len(args) != 1 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, handoffUsage)
		return 2
	}
	var data []byte
	var err error
	if args[0] == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(args[0])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr handoff:", err)
		return 1
	}
	text := strings.TrimSpace(string(data))
	switch {
	case text == "":
		fmt.Fprintf(os.Stderr, "nerdr handoff: the handoff is empty\n\n%s", handoffUsage)
		return 2
	case len(text) > handoff.MaxSummary:
		fmt.Fprintf(os.Stderr, "nerdr handoff: the handoff is %d KB; keep it under %d KB — the new agent can read files and git for the details\n", len(text)/1024, handoff.MaxSummary/1024)
		return 2
	}
	tab, sock, ok := tabSocket("nerdr handoff", "not running in a nerdr tab, so there is no nerdr to hand over to")
	if !ok {
		return 1
	}
	if err := send(sock, hooks.Handoff, map[string]any{"pane_id": tab, "summary": text}); err != nil {
		fmt.Fprintln(os.Stderr, "nerdr handoff:", err)
		return 1
	}
	fmt.Println("nerdr has the handoff. If the user is switching this tab's agent, nerdr stops you now:\n" +
		"do nothing more. Else it is kept for when they do.")
	return 0
}
