package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/order"
	"github.com/nerdr-dev/nerdr/internal/store"
)

// orderUsage is written for the agent as much as for a person: it is what
// an agent sees when it gets the call wrong.
const orderUsage = `usage: nerdr order add [--file PATH --role ROLE] --short TEXT [--agent NAME]
       nerdr order list

Records how to read the change you are making, for nerdr to show as the
order to read it in.

  --file    the file, relative to where you stand or to the worktree root;
            leave it out to describe the change as a whole
  --role    core (the change itself) · follows (what had to follow from it) ·
            test · mechanical (the same edit repeated)
  --short   one line: what this file is here for, or what the change does
  --agent   who is recording it

"list" prints what has been recorded and which changed files are not
described yet. Read it before adding, and record only what is missing or
has changed.
`

func runOrder(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, orderUsage)
		return 2
	}
	switch args[0] {
	case "add":
		return orderAdd(args[1:])
	case "list":
		return orderList()
	}
	fmt.Fprint(os.Stderr, orderUsage)
	return 2
}

func orderAdd(args []string) int {
	fs := flag.NewFlagSet("order add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var e order.Entry
	fs.StringVar(&e.File, "file", "", "")
	fs.StringVar(&e.Role, "role", "", "")
	fs.StringVar(&e.Short, "short", "", "")
	fs.StringVar(&e.Agent, "agent", "", "")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "nerdr order: %v\n\n%s", err, orderUsage)
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "nerdr order: unexpected %q — quote each value\n\n%s",
			strings.Join(fs.Args(), " "), orderUsage)
		return 2
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr order:", err)
		return 1
	}
	written, err := order.Add(cwd, e)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr order:", err)
		return 1
	}
	if written.File == "" {
		fmt.Println("noted: the change is " + written.Short)
		return 0
	}
	fmt.Printf("noted %s as %s\n", written.File, written.Role)
	return 0
}

// orderList prints the reading order as it stands, and then the changed
// files nothing has been said about. The second half is the point: an agent
// coming back to work that is already part described needs to see what is
// missing, or it starts again and repeats itself.
func orderList() int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr order:", err)
		return 1
	}
	root, err := store.Root(cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr order:", err)
		return 1
	}
	ix := order.Load(root, store.Branch(root))

	if s := ix.Summary(); s != "" {
		fmt.Println("the change: " + s)
	} else {
		fmt.Println("the change: (not described)")
	}
	for _, role := range order.Roles() {
		for _, f := range ix.Files() {
			e, _ := ix.For(f)
			if e.Role != role {
				continue
			}
			fmt.Printf("  %-11s %-44s %s\n", e.Role, e.File, e.Short)
		}
	}

	missing := undescribed(root, ix)
	if len(missing) == 0 {
		fmt.Println("\nevery changed file is described")
		return 0
	}
	fmt.Println("\nnot described yet:")
	for _, f := range missing {
		fmt.Println("  " + f)
	}
	return 0
}

// undescribed lists changed files with no entry. Both what is uncommitted
// and what the branch has committed count: an agent describes work as it
// goes, and by then some of it is usually committed.
func undescribed(root string, ix order.Index) []string {
	seen := map[string]bool{}
	var files []string
	add := func(fs []git.FileStatus) {
		for _, f := range fs {
			if seen[f.Path] {
				continue
			}
			seen[f.Path] = true
			if _, ok := ix.For(f.Path); !ok {
				files = append(files, f.Path)
			}
		}
	}
	if fs, err := git.ChangedFiles(root); err == nil {
		add(fs)
	}
	if base := git.DetectBase(root); base != "" {
		if fs, err := git.ChangedFilesBase(root, base); err == nil {
			add(fs)
		}
	}
	sort.Strings(files)
	return files
}
