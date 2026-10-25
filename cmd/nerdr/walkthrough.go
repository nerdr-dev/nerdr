package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"nerdr/internal/browser"
	"nerdr/internal/store"
	"nerdr/internal/walkthrough"
)

// walkthroughUsage is written for the agent as much as for a person: it is
// what an agent sees when it gets the call wrong.
const walkthroughUsage = `usage: nerdr walkthrough add FILE|-
       nerdr walkthrough show
       nerdr walkthrough page [--open]

Records a guided walkthrough of the change you made, for nerdr to show the
user as a page they go through step by step (g in the review panel).

"add" reads the walkthrough as JSON, from FILE or from stdin ("-"):

  {
    "title":   "the change in a few words",
    "summary": "what it does and why, a paragraph or two",
    "steps": [
      {"id": "cache", "title": "a few words", "text": "what happens here, and why",
       "code": [{"file": "internal/cache/cache.go", "anchor": "func (c *Cache) Config(", "note": "optional"}]},
      {"id": "callers", "title": "…", "text": "…", "from": ["cache"]},
      {"id": "flag", "title": "…", "text": "…", "side": true, "from": ["cache"]}
    ],
    "risks": [{"level": "high|medium|low", "text": "…", "file": "optional", "anchor": "optional"}],
    "agent": "who you are"
  }

  steps   the order to read the change in, %d at most. A step that names no
          "from" follows the main step before it; "side": true puts it
          beside the main path, as a related change.
  code    an anchor is a fragment of one line, as for nerdr why: the hunk
          holding it is shown, or the code around it if the change did not
          touch it. No anchor shows every hunk of the file.

A new walkthrough replaces the one before. "show" prints the one recorded,
"page" writes it as a page and prints where, --open opens it too.
`

func runWalkthrough(args []string) int {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, walkthroughUsage, walkthrough.MaxSteps)
		return 2
	}
	switch args[0] {
	case "add":
		return walkthroughAdd(args[1:])
	case "show":
		return walkthroughShow()
	case "page":
		return walkthroughPage(args[1:])
	}
	fmt.Fprintf(os.Stderr, walkthroughUsage, walkthrough.MaxSteps)
	return 2
}

func walkthroughAdd(args []string) int {
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "nerdr walkthrough: add takes one FILE, or - for stdin\n\n"+walkthroughUsage, walkthrough.MaxSteps)
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
		fmt.Fprintln(os.Stderr, "nerdr walkthrough:", err)
		return 1
	}
	var w walkthrough.Walkthrough
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		fmt.Fprintf(os.Stderr, "nerdr walkthrough: the JSON does not read: %v\n\n"+walkthroughUsage, err, walkthrough.MaxSteps)
		return 2
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr walkthrough:", err)
		return 1
	}
	written, err := walkthrough.Add(cwd, w)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr walkthrough:", err)
		return 1
	}
	side := 0
	for _, s := range written.Steps {
		if s.Side {
			side++
		}
	}
	fmt.Printf("noted the walkthrough: %d steps (%d side), %d risks\n", len(written.Steps), side, len(written.Risks))
	return 0
}

// current is the walkthrough recorded for the worktree you stand in.
func current() (root string, w walkthrough.Walkthrough, ok bool) {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr walkthrough:", err)
		return "", w, false
	}
	root, err = store.Root(cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr walkthrough:", err)
		return "", w, false
	}
	w, ok = walkthrough.Load(root, store.Branch(root))
	if !ok {
		fmt.Fprintln(os.Stderr, "nerdr walkthrough: none recorded for this branch")
	}
	return root, w, ok
}

func walkthroughShow() int {
	_, w, ok := current()
	if !ok {
		return 1
	}
	fmt.Printf("%s\n\n%s\n\n", w.Title, w.Summary)
	for i, s := range w.Steps {
		kind := ""
		if s.Side {
			kind = " (side)"
		}
		fmt.Printf("%2d. %s%s [%s]\n", i+1, s.Title, kind, s.ID)
		for _, c := range s.Code {
			fmt.Printf("      %s %s\n", c.File, c.Anchor)
		}
	}
	for _, r := range w.Risks {
		fmt.Printf("risk %s: %s\n", r.Level, r.Text)
	}
	return 0
}

func walkthroughPage(args []string) int {
	open := len(args) == 1 && args[0] == "--open"
	if len(args) > 0 && !open {
		fmt.Fprintf(os.Stderr, walkthroughUsage, walkthrough.MaxSteps)
		return 2
	}
	root, w, ok := current()
	if !ok {
		return 1
	}
	path, err := walkthrough.Write(root, w)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr walkthrough:", err)
		return 1
	}
	fmt.Println(path)
	if open {
		if err := browser.OpenFile(path); err != nil {
			fmt.Fprintln(os.Stderr, "nerdr walkthrough:", err)
			return 1
		}
	}
	return 0
}
