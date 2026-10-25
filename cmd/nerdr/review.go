package main

import (
	"fmt"
	"io"
	"os"

	"nerdr/internal/codereview"
	"nerdr/internal/hooks"
)

// reviewUsage is written for the agent, as whyUsage is.
const reviewUsage = `usage: nerdr post-review FILE    (or - to read it from stdin)

Sends your code review of this worktree to nerdr, as JSON:

  {"verdict": "approve" | "changes",
   "summary": "a few lines: what you looked at and what you found",
   "findings": [{"file": "path/from/the/worktree/root",
                 "anchor": "a fragment of the line it is about (optional)",
                 "severity": "high" | "medium" | "low",
                 "text": "what is wrong, why, and what to do about it"}]}

nerdr shows the findings against the code and hands them to the agent that
wrote it. Do nothing more once it is sent.
`

// runReview is the "post-review" subcommand: it sends a reviewer's conclusion
// to the nerdr it runs in.
func runReview(args []string) int {
	if len(args) != 1 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, reviewUsage)
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
		fmt.Fprintln(os.Stderr, "nerdr post-review:", err)
		return 1
	}
	r, err := codereview.Parse(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nerdr post-review: %v\n\n%s", err, reviewUsage)
		return 2
	}
	tab, sock, ok := tabSocket("nerdr post-review", "not running in a nerdr tab, so there is no nerdr to send it to")
	if !ok {
		return 1
	}
	if err := send(sock, hooks.Review, map[string]any{"pane_id": tab, "review": r}); err != nil {
		fmt.Fprintln(os.Stderr, "nerdr post-review:", err)
		return 1
	}
	fmt.Println("nerdr has the review. Do nothing more: wait.")
	return 0
}
