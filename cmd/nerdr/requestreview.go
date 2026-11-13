package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/codereview"
	"github.com/nerdr-dev/nerdr/internal/hooks"
)

// requestReviewUsage is written for the agent, as whyUsage is.
const requestReviewUsage = `usage: nerdr request-review [--agent KIND [--model MODEL]]... [FOCUS]    (or - to read FOCUS from stdin)

Says your work in this tab is ready for a code review: a second agent
reviews it once your turn ends, and its findings come
back to you as a message. FOCUS, if given, is what you want it to look at
most — a part you are unsure of — in a sentence or two.

--agent is the kind of agent to review with (codex, claude, opencode, …),
and --model the model it reviews with, in that agent's own names, as the
user means it: nerdr agents shows the models the tab's agents run, and
the agent's own listing (codex's /model, cursor-agent models, opencode
models) the rest. Only when the user asked for them; without, the
reviewer the user set up reviews.

Give --agent once for each agent the user named, up to 4, each followed
by its own --model if the user gave one: they review together, and their
findings come to you as one message. Each kind reviews once.

Run it once, when you have finished what you were asked to do, then end
your turn. Unless nerdr told you code review is on in this tab, run it
only when the user has asked for a review or said yes to one.
`

// runRequestReview is the "request-review" subcommand: it tells the nerdr
// it runs in that the agent's work is ready for a review.
func runRequestReview(args []string) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprint(os.Stderr, requestReviewUsage)
		return 2
	}
	var by []hooks.Reviewer
	for len(args) > 1 && (args[0] == "--agent" || args[0] == "--model") {
		switch {
		case args[0] == "--agent":
			by = append(by, hooks.Reviewer{Kind: args[1]})
		case len(by) == 0:
			fmt.Fprintln(os.Stderr, "nerdr request-review: --model is the model of an agent: give --agent before it")
			return 2
		case by[len(by)-1].Model != "":
			fmt.Fprintln(os.Stderr, "nerdr request-review: one --model for each --agent")
			return 2
		default:
			by[len(by)-1].Model = args[1]
		}
		args = args[2:]
	}
	if len(by) > hooks.MaxReviewers {
		fmt.Fprintf(os.Stderr, "nerdr request-review: name at most %d agents to review\n", hooks.MaxReviewers)
		return 2
	}
	focus := strings.Join(args, " ")
	if len(args) == 1 && args[0] == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "nerdr request-review:", err)
			return 1
		}
		focus = string(data)
	}
	focus = strings.TrimSpace(focus)
	if len(focus) > codereview.MaxFocus {
		fmt.Fprintf(os.Stderr, "nerdr request-review: the focus is %d bytes; keep it under %d — the reviewer reads the change itself\n", len(focus), codereview.MaxFocus)
		return 2
	}
	tab, sock, ok := tabSocket("nerdr request-review", "not running in a nerdr tab, so there is no nerdr to review the work")
	if !ok {
		return 1
	}
	if err := send(sock, hooks.RequestReview, map[string]any{"pane_id": tab, "focus": focus, "reviewers": by}); err != nil {
		fmt.Fprintln(os.Stderr, "nerdr request-review:", err)
		return 1
	}
	fmt.Println("nerdr has it. End your turn now: the review starts then, and its findings come to you as a message.")
	return 0
}
