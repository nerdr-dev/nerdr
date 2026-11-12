package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/state"
)

const sandboxUsage = `usage: nerdr sandbox
       nerdr sandbox clear REPO…
       nerdr sandbox clear --all

Where an agent has run in a sandbox, nerdr does not run what it wrote as
you, outside the sandbox: in that repository nerdr does not fetch or push,
and runs no Git hooks or setup on that code. nerdr sandbox lists the
repositories it holds back in, and where each one's history is from.

clear has nerdr forget it for REPO, a repository's main checkout, or for
all of them: do that only when you trust what is in the repository and its
worktrees now. nerdr then works there as in any other, until an agent runs
in a sandbox there again.

Run it in the folder you start nerdr in, with nerdr not running there.
While it runs, use Forget sandbox history in the tab's menu instead.
`

// runSandbox is `nerdr sandbox`: the user's own look at, and say over,
// the sandbox history of the session of the folder it is run in. It works
// on the saved session, so only while no nerdr has it: an agent in a tab,
// which runs under one, gets nowhere with it.
func runSandbox(args []string) int {
	if helpAsked(args) {
		fmt.Fprint(os.Stderr, sandboxUsage)
		return 2
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr sandbox:", err)
		return 1
	}
	if len(args) == 0 {
		sess, err := state.Load(root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "nerdr sandbox:", err)
			return 1
		}
		listSandboxed(sess)
		return 0
	}
	if args[0] != "clear" {
		fmt.Fprint(os.Stderr, sandboxUsage)
		return 2
	}
	fs := flag.NewFlagSet("nerdr sandbox clear", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, sandboxUsage) }
	all := fs.Bool("all", false, "")
	if err := parseAnywhere(fs, args[1:]); err != nil {
		return 2
	}
	if *all == (fs.NArg() > 0) {
		fmt.Fprint(os.Stderr, sandboxUsage)
		return 2
	}
	out, err := clearSandboxed(root, fs.Args())
	for _, l := range out {
		fmt.Println(l)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr sandbox clear:", err)
		return 1
	}
	return 0
}

// listSandboxed prints the repositories of sess with sandbox history.
func listSandboxed(sess state.Session) {
	repos := make([]string, 0, len(sess.SandboxedRepos))
	for r := range sess.SandboxedRepos {
		repos = append(repos, r)
	}
	sort.Strings(repos)
	for _, r := range repos {
		if from := sess.SandboxSources[r]; from != "" {
			fmt.Printf("%s\t%s\n", r, from)
		} else {
			fmt.Println(r)
		}
	}
	if len(repos) == 0 {
		fmt.Println("nerdr holds back in no repository of this folder's session")
	}
	if sess.SandboxHistoryVersion == 0 && len(sess.SandboxedDirs) > 0 {
		fmt.Printf("%d folder(s) from an older nerdr, whose repositories are found when nerdr starts\n", len(sess.SandboxedDirs))
	}
}

// clearSandboxed drops the sandbox history of repos, or of every
// repository with none given, from the saved session of root, and says
// what it did, a line for each.
func clearSandboxed(root string, repos []string) ([]string, error) {
	release, err := state.Lock(root)
	if errors.Is(err, state.ErrLocked) {
		return nil, fmt.Errorf("nerdr runs in %s: use Forget sandbox history in the tab's menu there, or quit nerdr first", root)
	}
	if err != nil {
		return nil, err
	}
	defer release()
	sess, err := state.Load(root)
	if err != nil {
		return nil, err
	}
	var out []string
	if len(repos) == 0 {
		if !core.ForgetSandboxedIn(&sess, "") {
			return []string{"there was no sandbox history to forget"}, nil
		}
		out = append(out, "sandbox history forgotten for every repository")
	}
	for _, r := range repos {
		abs, err := filepath.Abs(r)
		if err != nil {
			return out, err
		}
		if core.ForgetSandboxedIn(&sess, abs) {
			out = append(out, abs+": sandbox history forgotten")
		} else {
			out = append(out, abs+": had no sandbox history")
		}
	}
	return out, state.Save(sess)
}
