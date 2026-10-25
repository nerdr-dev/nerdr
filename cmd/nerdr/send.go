package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/hooks"
)

// sendUsage is written for the agent, as readyUsage is.
const sendUsage = `usage: nerdr send [--wait] [--timeout 10m] N MESSAGE    (or - to read MESSAGE from stdin)
       nerdr send                 lists the agents of this tab

Sends MESSAGE to agent number N in this nerdr tab: the agents working in
the same worktree, each in a pane the user sees numbered, "2:codex". It
reaches the other agent as a message from you, typed in when it is idle,
and it can answer the same way.

Write it for an agent with none of your conversation: what you want of
it, and what it needs to know to do it.

--wait waits for agent N to answer with nerdr send, and prints its answer
here instead of it coming to you as a message, saving you a turn: for a
back and forth, such as a question you need answered to go on. Exits 3
when --timeout comes first; the answer then comes as a message, as any
other. Mind your own tool's time limit: keep --timeout under it.
`

// runSend is the "send" subcommand: a message to another agent of the
// tab, through the nerdr running it, which says whether it went.
func runSend(args []string) int {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	wait := fs.Bool("wait", false, "")
	timeout := fs.Duration("timeout", 10*time.Minute, "")
	args, err := takeFlags(fs, args)
	if helpAsked(args) || err != nil {
		fmt.Fprint(os.Stderr, sendUsage)
		return 2
	}
	if *wait && len(args) == 0 {
		fmt.Fprintf(os.Stderr, "nerdr send: --wait needs an agent and a message\n\n%s", sendUsage)
		return 2
	}
	to, text := 0, ""
	if len(args) > 0 {
		n, err := strconv.Atoi(strings.TrimPrefix(args[0], "#"))
		if err != nil || n < 1 {
			fmt.Fprintf(os.Stderr, "nerdr send: %q is not an agent's number\n\n%s", args[0], sendUsage)
			return 2
		}
		to = n
		text = strings.Join(args[1:], " ")
		if len(args) == 2 && args[1] == "-" {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				fmt.Fprintln(os.Stderr, "nerdr send:", err)
				return 1
			}
			text = string(data)
		}
		if strings.TrimSpace(text) == "" {
			fmt.Fprintf(os.Stderr, "nerdr send: no message for agent %d\n\n%s", to, sendUsage)
			return 2
		}
		if len(text) > hooks.MaxText {
			fmt.Fprintf(os.Stderr, "nerdr send: the message is %d bytes; keep it under %d\n", len(text), hooks.MaxText)
			return 2
		}
	}
	tab, sock, ok := tabSocket("nerdr send", "not running in a nerdr tab, so there are no other agents to send to")
	if !ok {
		return 1
	}
	answer, err := request(sock, hooks.Message, map[string]any{"pane_id": tab, "to": to, "text": text})
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr send:", err)
		return 1
	}
	fmt.Println(answer)
	if !*wait {
		return 0
	}
	return waitReply(to, *timeout)
}

// replyEvery is how often nerdr send --wait asks for the answer.
var replyEvery = time.Second

// waitReply waits for agent n's answer, and prints it.
func waitReply(n int, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	for {
		answer, ok := ask("nerdr send", hooks.Query, map[string]any{"what": hooks.QueryReply, "to": n})
		if !ok {
			return 1
		}
		if answer != "" {
			fmt.Println()
			fmt.Println(answer)
			return 0
		}
		if time.Now().After(deadline) {
			fmt.Fprintf(os.Stderr, "nerdr send: agent %d has not answered yet; its answer comes to you as a message when it does\n", n)
			return 3
		}
		time.Sleep(min(replyEvery, time.Until(deadline)+time.Millisecond))
	}
}
