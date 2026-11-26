// Package prask writes the messages nerdr puts to a tab's agent about its
// pull request: fix the failing build, draft replies to the reviews,
// rebase a branch that conflicts with its base. F2's f and a write them
// for the user to read and send; core writes them by itself for the
// [auto] settings.
//
// Two of them quote what other people wrote — review comments, CI logs —
// and say plainly that nothing is to be posted: the quoted text is not
// instructions.
package prask

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/github"
)

// LogLimit is how much failing-build log goes into the message. The agent
// can read the repository itself, so this only has to carry the failure.
const LogLimit = 6000

// Failing is how many of checks failed, and how many are still to end.
func Failing(checks []github.Check) (fail, pending int) {
	for _, c := range checks {
		switch st := strings.ToUpper(c.Status); {
		case github.Failed(st):
			fail++
		case st == "SUCCESS" || st == "NEUTRAL" || st == "SKIPPED":
		default:
			pending++
		}
	}
	return fail, pending
}

// FixMessage is the message f writes for pr: the failing jobs' logs, as
// github.FailedRunLogs read them (at most LogLimit of each), and the ask
// to fix the build. Without a failing check it is an error saying so.
func FixMessage(pr github.PR, logs string) (string, error) {
	if fail, _ := Failing(pr.Checks); fail == 0 {
		return "", fmt.Errorf("no failing checks on #%d", pr.Number)
	}
	return FixPrompt(pr, logs), nil
}

// ReplyMessage is the message A writes for pr: drafts for every review and
// comment; "" when there is nothing to answer.
func ReplyMessage(pr github.PR) string {
	if len(pr.Comments) == 0 && !HasReviewBody(pr) {
		return ""
	}
	return ReplyPrompt(pr)
}

// Said is the reviews that came with words: all of them when the PR was
// fetched by itself, else each reviewer's latest.
func Said(pr github.PR) []github.Review {
	if len(pr.Said) > 0 {
		return pr.Said
	}
	return pr.Reviews
}

// Where is the line a comment is on, "" for one in the conversation.
func Where(c github.Comment) string {
	switch {
	case c.Path == "":
		return ""
	case c.Line > 0:
		return c.Path + ":" + strconv.Itoa(c.Line)
	}
	return c.Path
}

// HasReviewBody reports whether some review of pr came with words.
func HasReviewBody(pr github.PR) bool {
	for _, r := range Said(pr) {
		if strings.TrimSpace(r.Body) != "" {
			return true
		}
	}
	return false
}

// Strangers is who, of those whose words ReplyMessage quotes, has no say
// in the repository (github.Trusted), pr's author aside: sorted, each
// once. None means every word quoted is the author's own or a member's.
func Strangers(pr github.PR) []string {
	seen := map[string]bool{}
	var out []string
	add := func(who, association string) {
		if who == pr.Author || github.Trusted(association) || seen[who] {
			return
		}
		seen[who] = true
		out = append(out, who)
	}
	for _, r := range Said(pr) {
		if strings.TrimSpace(r.Body) != "" {
			add(r.Author, r.Association)
		}
	}
	for _, c := range pr.Comments {
		add(c.Author, c.Association)
	}
	sort.Strings(out)
	return out
}

// ReplyPrompt asks for drafts of replies to every review and comment.
func ReplyPrompt(pr github.PR) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Help me answer the review feedback on pull request #%d (%s).\n\n", pr.Number, pr.Title)
	n := 0
	for _, r := range Said(pr) {
		if strings.TrimSpace(r.Body) == "" {
			continue
		}
		n++
		fmt.Fprintf(&b, "[%d] review by %s (%s):\n%s\n\n", n, r.Author,
			strings.ToLower(r.State), strings.TrimSpace(r.Body))
	}
	for _, c := range pr.Comments {
		n++
		on := ""
		if w := Where(c); w != "" {
			on = " on " + w
		}
		fmt.Fprintf(&b, "[%d] comment by %s%s:\n%s\n\n", n, c.Author, on, strings.TrimSpace(c.Body))
	}
	b.WriteString("Draft a reply to each, numbered to match. Check what each one claims " +
		"against the code before agreeing with it: say so plainly when a comment is " +
		"mistaken, and say what you would change when it is right. A few sentences each, " +
		"plain text. These are drafts for me to review and send — do not post anything, " +
		"and do not change any files yet.")
	return b.String()
}

// FixPrompt asks for the failing checks of pr to be fixed, with logs.
func FixPrompt(pr github.PR, logs string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "A CI check on pull request #%d (%s) failed. Find out why, and fix it.\n\n", pr.Number, pr.Title)
	b.WriteString("Failing checks:\n")
	for _, c := range pr.Checks {
		// Only what failed: one still queued or running has not.
		if !github.Failed(c.Status) {
			continue
		}
		fmt.Fprintf(&b, "  - %s (%s)\n", c.Name, strings.ToLower(c.Status))
	}
	b.WriteString("\nFrom the job log — the lines leading up to each error the runner " +
		"reported, with the rest cut:\n\n")
	b.WriteString(logs)
	b.WriteString("\n\nWork out the cause from the log and the code, make the smallest change " +
		"that fixes it, and run the project's own tests or linter for the part you touched. " +
		"Do not commit, push, or comment on the pull request. Then tell me what was wrong, " +
		"what you changed, and what you ran to check it.")
	return b.String()
}

// RebasePrompt asks for the branch of pr, which conflicts with its base,
// to be rebased onto it. It quotes nobody: the title is the author's.
func RebasePrompt(pr github.PR) string {
	base := pr.Base
	if base == "" {
		base = "its base branch"
	}
	return fmt.Sprintf("Pull request #%d (%s) has conflicts with %s on GitHub. "+
		"Fetch the remote, rebase this branch onto the remote's %s, and resolve the conflicts, "+
		"keeping what each side meant to do. Run the project's own tests for the parts that "+
		"conflicted. Do not push or comment on the pull request. Then tell me what conflicted, "+
		"how you resolved it, and what you ran to check it.",
		pr.Number, pr.Title, base, base)
}
