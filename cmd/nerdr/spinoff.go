package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"nerdr/internal/hooks"
	"nerdr/internal/spinoff"
	"nerdr/internal/workspace"
)

// spinoffUsage is written for the agent, as whyUsage is.
const spinoffUsage = `usage: nerdr spinoff FILE    (or - to read the plan from stdin)

Hands parts of your work to new nerdr tabs, one tab and branch per part.
Only when the user asks for it. The plan is JSON:

  {"parts": [
    {"branch":  "fix/parser-nil",
     "summary": "a few lines for the user: what this part is",
     "prompt":  "everything the new agent needs to know to do it",
     "commits": ["a1b2c3d"],
     "repos":   ["api"]}
  ]}

  branch   the branch name you propose; the user may change it
  summary  shown to the user when they make the tab; under 600 bytes
  prompt   given to the new agent, which knows nothing of your conversation
  commits  optional: commits in your branch with this part's work; the user
           chooses whether the new branch starts with them or writes it anew
  repos    optional, in a tab with several repositories (a folder each in
           yours): the folders of those this part changes; the user picks
           in the end. Left out, the new tab has those the commits are in,
           or all of them

nerdr asks the user about each part in turn and then tells you, as a new
message, which tabs were made and which parts were skipped.
`

// runSpinoff is the "spinoff" subcommand: it checks the plan while the
// agent can still fix it, and sends it to the nerdr the agent runs in.
func runSpinoff(args []string) int {
	if len(args) != 1 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, spinoffUsage)
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
		fmt.Fprintln(os.Stderr, "nerdr spinoff:", err)
		return 1
	}
	plan, err := spinoff.Parse(data, workspace.ValidBranch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nerdr spinoff: %v\n\n%s", err, spinoffUsage)
		return 2
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr spinoff:", err)
		return 1
	}
	if err := resolveCommits(cwd, plan.Parts); err != nil {
		fmt.Fprintln(os.Stderr, "nerdr spinoff:", err)
		return 2
	}

	tab, sock, ok := tabSocket("nerdr spinoff", "not running in a nerdr tab, so there is no nerdr to make the tabs")
	if !ok {
		return 1
	}
	if err := send(sock, hooks.Spinoff, map[string]any{"pane_id": tab, "parts": plan.Parts}); err != nil {
		fmt.Fprintln(os.Stderr, "nerdr spinoff:", err)
		return 1
	}
	parts := "1 part"
	if n := len(plan.Parts); n != 1 {
		parts = fmt.Sprintf("%d parts", n)
	}
	fmt.Printf("sent %s to nerdr. The user names each branch and makes the tabs;\n"+
		"you get a message saying what became of each part once they are through.\n"+
		"Until then, leave those parts alone.\n", parts)
	return 0
}

// resolveCommits replaces each part's commits with their full ids, from the
// repository the agent stands in, or, in a tab with several repositories,
// from the one under it that has the commit.
func resolveCommits(cwd string, parts []spinoff.Part) error {
	dirs := []string{cwd}
	if entries, err := os.ReadDir(cwd); err == nil {
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				dirs = append(dirs, filepath.Join(cwd, e.Name()))
			}
		}
	}
	var errs []string
	for i := range parts {
		for j, c := range parts[i].Commits {
			full := ""
			for _, d := range dirs {
				if id, err := workspace.ResolveCommit(d, c); err == nil {
					full = id
					break
				}
			}
			if full == "" {
				errs = append(errs, fmt.Sprintf("%s: %s is not a commit in this worktree", parts[i].Branch, c))
				continue
			}
			parts[i].Commits[j] = full
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "\n"))
	}
	return nil
}
