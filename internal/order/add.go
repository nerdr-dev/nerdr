package order

import (
	"fmt"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/store"
)

// Add records one line of the reading order and returns it as written. dir
// is where the writer stands — any directory inside the worktree — and a
// file may be named relative to it, to the worktree root, or absolutely.
//
// An entry with no file is the summary of the whole change; one with a file
// needs a role, checked here rather than ignored later, so an agent that
// invents a role is told instead of writing a line nothing shows.
func Add(dir string, e Entry) (Entry, error) {
	root, err := store.Root(dir)
	if err != nil {
		return e, err
	}
	path := File(root)
	if path == "" {
		return e, fmt.Errorf("no data folder for %s", root)
	}

	e.Short = strings.TrimSpace(e.Short)
	e.Role = strings.TrimSpace(e.Role)
	switch {
	case e.Short == "":
		return e, fmt.Errorf("--short is required")
	case strings.ContainsAny(e.Short, "\r\n"):
		return e, fmt.Errorf("--short must be a single line")
	case e.File == "" && e.Role != "":
		return e, fmt.Errorf("--role describes a file, so it needs --file")
	case e.File != "" && !Valid(e.Role):
		return e, fmt.Errorf("--role must be one of %s", strings.Join(roles, ", "))
	}

	if e.File != "" {
		rel, err := store.Relative(root, dir, e.File)
		if err != nil {
			gone, ok := store.Removed(root, dir, e.File)
			if !ok {
				return e, err
			}
			rel = gone
		}
		e.File = rel
	}
	if e.Branch == "" {
		e.Branch = store.Branch(root)
	}
	if e.Time == "" {
		e.Time = time.Now().Format(timeLayout)
	}
	if err := store.ExcludeData(root); err != nil {
		return e, err
	}
	return e, store.Append(path, e)
}
