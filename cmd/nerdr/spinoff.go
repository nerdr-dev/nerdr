package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/spinoff"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// spinoffUsage is written for the agent, as whyUsage is.
const spinoffUsage = `usage: nerdr spinoff FILE                  (or - to read the plan from stdin)
       nerdr spinoff --prompt PART FILE    (or - to read the prompt from stdin)

Hands parts of your work to new nerdr tabs, one tab and branch per part.
Only when the user asks for it. The plan is JSON:

  {"parts": [
    {"branch":  "fix/parser-nil",
     "summary": "a few lines for the user: what this part is",
     "commits": ["a1b2c3d"],
     "repos":   ["api"]}
  ]}

  branch   the branch name you propose; the user may change it
  summary  shown to the user when they make the tab; under 600 bytes
  commits  optional: commits in your branch with this part's work and
           nothing else, oldest or newest first; the user chooses whether
           the new branch starts with them (nerdr cherry-picks them onto a
           branch from main) or writes it anew. Leave it out for a part
           with no code yet
  repos    optional: the repositories this part changes. In a tab with
           several (a folder each in yours), the folders of those it
           changes; or, when the user asks for another repository, its
           name, which your commits cannot go to: say in the prompt what
           was done and where. The user picks in the end. Left out, the
           new tab has those the commits are in, or all of the tab's
  prompt   optional here: what the new agent is given, which knows
           nothing of your conversation. nerdr adds a note after it saying
           which tab it came from and whether your commits are already on
           its branch

Send the plan without the prompts first: nerdr asks the user about the
first part at once, and prints the id it gave each part. Then write each
part's prompt and send it with --prompt and that id, as the command it
prints; the user goes through the parts meanwhile, and a tab is made once
its prompt has come. Send every part's prompt, even for a part the user
may skip.

The plan is checked first — branch names, empty fields, that the commits
exist — and a refused plan is not sent: fix what the message says and send
it again. Sent, nerdr asks the user about each part in turn and then tells
you, as a new message, which tabs were made and which parts were skipped.
`

// runSpinoff is the "spinoff" subcommand: it checks the plan while the
// agent can still fix it, and sends it to the nerdr the agent runs in.
func runSpinoff(args []string) int {
	if len(args) == 3 && args[0] == "--prompt" {
		return runSpinoffPrompt(args[1], args[2])
	}
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
	answer, err := request(sock, hooks.Spinoff, map[string]any{"pane_id": tab, "parts": plan.Parts})
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr spinoff:", err)
		return 1
	}
	// The id nerdr gave each part taken, a line each: "id\tbranch".
	ids := map[string]string{}
	for _, line := range strings.Split(answer, "\n") {
		if id, branch, ok := strings.Cut(line, "\t"); ok {
			ids[branch] = id
		}
	}
	parts := "1 part"
	if n := len(plan.Parts); n != 1 {
		parts = fmt.Sprintf("%d parts", n)
	}
	fmt.Printf("sent %s to nerdr. The user names each branch and makes the tabs;\n"+
		"you get a message saying what became of each part once they are through.\n"+
		"Until then, leave those parts alone.\n", parts)
	if cmds := promptCommands(plan.Parts, ids); cmds != "" {
		fmt.Printf("\nThe user is being asked about them now, and a tab waits for its prompt: write each\n"+
			"part's prompt and send it at once, one command each, before you do anything else:\n\n%s", cmds)
	}
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

// promptCommands are the commands that send the prompts of parts sent
// without one, by the ids nerdr gave them (by branch), the prompt to be
// written in place of the … line; "" when none is to come.
func promptCommands(parts []spinoff.Part, ids map[string]string) string {
	var b strings.Builder
	for _, pt := range parts {
		if id := ids[pt.Branch]; pt.Prompt == "" && id != "" {
			// The heredoc's end at the start of its line, as the shell
			// wants it.
			fmt.Fprintf(&b, "%s:\n\"${NERDR_BIN:-nerdr}\" spinoff --prompt %s - <<'PROMPT'\n…\nPROMPT\n\n", pt.Branch, id)
		}
	}
	return b.String()
}

// runSpinoffPrompt is "spinoff --prompt PART FILE": the prompt, read
// from FILE or stdin, of the part nerdr gave the id PART.
func runSpinoffPrompt(part, file string) int {
	var data []byte
	var err error
	if file == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(file)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr spinoff:", err)
		return 1
	}
	prompt := strings.TrimSpace(string(data))
	switch err := spinoff.CheckPrompt(prompt); {
	case prompt == "":
		fmt.Fprintln(os.Stderr, "nerdr spinoff: the prompt is empty")
		return 2
	case err != nil:
		fmt.Fprintln(os.Stderr, "nerdr spinoff:", err)
		return 2
	}
	tab, sock, ok := tabSocket("nerdr spinoff", "not running in a nerdr tab, so there is no nerdr to make the tabs")
	if !ok {
		return 1
	}
	answer, err := request(sock, hooks.SpinoffPrompt, map[string]any{"pane_id": tab, "part": part, "text": prompt})
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr spinoff:", err)
		return 1
	}
	fmt.Println(answer)
	return 0
}
