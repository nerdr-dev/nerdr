package why

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nerdr/internal/store"
)

// Add checks a note against the code and appends it to the repository's
// notes file. dir is where the writer stands — any directory inside the
// worktree — and note.File may be relative to it, relative to the worktree
// root, or absolute. It returns the note as written and the line it
// resolved to.
//
// Everything that would otherwise fail silently later is refused here,
// while the agent that wrote the note is still there to fix it: a file
// that does not exist, an anchor that is not in it, and an anchor that is
// in it more than once, which would attach the note to whichever line
// comes first.
func Add(dir string, note Note) (Note, int, error) {
	root, err := store.Root(dir)
	if err != nil {
		return note, 0, err
	}
	path := NotesFile(root)
	if path == "" {
		return note, 0, fmt.Errorf("no data folder for %s", root)
	}

	note.Anchor = strings.TrimSpace(note.Anchor)
	note.Replaces = strings.TrimSpace(note.Replaces)
	note.Short = strings.TrimSpace(note.Short)
	note.Detail = strings.TrimSpace(note.Detail)
	switch {
	case note.File == "":
		return note, 0, fmt.Errorf("--file is required")
	case note.Anchor == "":
		return note, 0, fmt.Errorf("--anchor is required")
	case note.Short == "":
		return note, 0, fmt.Errorf("--short is required")
	case strings.ContainsAny(note.Anchor, "\r\n"):
		return note, 0, fmt.Errorf("the anchor must be a single line — take a distinctive fragment of one line")
	case strings.ContainsAny(note.Short, "\r\n"):
		return note, 0, fmt.Errorf("the short reason must be a single line — put the rest in --detail")
	}

	rel, err := store.Relative(root, dir, note.File)
	if err != nil {
		return note, 0, err
	}
	note.File = rel
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return note, 0, fmt.Errorf("cannot read %s: %v", rel, err)
	}
	lines := anchorLines(string(src), note.Anchor)
	switch {
	case len(lines) == 0:
		return note, 0, fmt.Errorf("the anchor %q is not in %s — copy it from the code as it is now, without indentation", note.Anchor, rel)
	case len(lines) > 1:
		return note, 0, fmt.Errorf("the anchor %q is on %d lines of %s (%s) — pick a fragment only the line you mean contains",
			note.Anchor, len(lines), rel, joinInts(lines))
	}

	if note.Branch == "" {
		note.Branch = store.Branch(root)
	}
	if note.Time == "" {
		note.Time = time.Now().Format("2006-01-02 15:04")
	}
	if err := store.ExcludeData(root); err != nil {
		return note, 0, err
	}
	if err := store.Append(path, note); err != nil {
		return note, 0, err
	}
	return note, lines[0], nil
}

func joinInts(v []int) string {
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = fmt.Sprint(n)
	}
	return strings.Join(s, ", ")
}
