package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"nerdr/internal/why"
)

// whyUsage is written for the agent as much as for a person: it is what an
// agent sees when it gets the call wrong.
const whyUsage = `usage: nerdr why add --file PATH --anchor TEXT --short TEXT [--detail TEXT|-] [--replaces TEXT] [--agent NAME]

Records why a change was made, for nerdr to show above the line it explains.

  --file      the file, relative to where you stand or to the worktree root
  --anchor    a fragment of one line of the code as it is now, found on no other line
  --short     the reason, on one line (aim for under 100 characters)
  --detail    the longer explanation; "-" reads it from stdin
  --replaces  the anchor of an earlier note in this file whose code you rewrote
  --agent     who is writing the note
`

// runWhy is the "why" subcommand. It exists so an agent never writes the
// notes file by hand: JSON escaping of code is easy to get subtly wrong,
// the file lives in the notes folder rather than anywhere obvious,
// and an anchor that is missing or ambiguous only shows up later, as a note
// nobody sees — here it is refused while the agent can still fix it.
func runWhy(args []string) int {
	if len(args) == 0 || args[0] != "add" {
		fmt.Fprint(os.Stderr, whyUsage)
		return 2
	}
	fs := flag.NewFlagSet("why add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var n why.Note
	fs.StringVar(&n.File, "file", "", "")
	fs.StringVar(&n.Anchor, "anchor", "", "")
	fs.StringVar(&n.Short, "short", "", "")
	fs.StringVar(&n.Detail, "detail", "", "")
	fs.StringVar(&n.Replaces, "replaces", "", "")
	fs.StringVar(&n.Agent, "agent", "", "")
	if err := fs.Parse(args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "nerdr why: %v\n\n%s", err, whyUsage)
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "nerdr why: unexpected %q — quote each value\n\n%s", strings.Join(fs.Args(), " "), whyUsage)
		return 2
	}
	if n.Detail == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "nerdr why:", err)
			return 1
		}
		n.Detail = string(b)
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr why:", err)
		return 1
	}
	written, line, err := why.Add(cwd, n)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr why:", err)
		return 1
	}
	fmt.Printf("noted %s:%d\n", written.File, line)
	return 0
}
