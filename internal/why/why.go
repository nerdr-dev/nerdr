// Package why reads the rationale behind a change from notes the agent
// wrote while making it.
//
// Notes live in one file per repository, kept and appended to as described
// in internal/store.
//
// A note is anchored to the code by content, not by line number: it names a
// distinctive fragment of the code it explains, and is shown against
// whichever line currently contains that fragment. Line numbers drift the
// moment anything above them changes, and drift silently; an anchor either
// matches or it does not.
package why

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/clean"
	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/store"
)

// FileName is the notes file, in the folder internal/store keeps records in.
const FileName = "why.jsonl"

// Note is one recorded rationale.
type Note struct {
	// File is the path the note applies to, relative to the worktree.
	File string `json:"file"`
	// Anchor is a distinctive fragment of the code being explained; the
	// note is shown against whichever line currently contains it.
	Anchor string `json:"anchor"`
	// Short is the one-line rationale shown above the line.
	Short string `json:"short"`
	// Detail is the fuller explanation, shown on demand. Optional.
	Detail string `json:"detail,omitempty"`
	// Replaces names the anchor of an earlier note in the same file that
	// this one retires — for when the code that note explained has been
	// rewritten, so its anchor is gone and it would otherwise be listed as
	// stale forever. Optional.
	Replaces string `json:"replaces,omitempty"`
	// Branch is the branch the note was written on. Optional; notes from
	// other branches are still matched against code, but not listed as
	// stale where their code was never checked out.
	Branch string `json:"branch,omitempty"`
	// Agent names who wrote the note. Optional.
	Agent string `json:"agent,omitempty"`
	// Time is when it was written, as an unparsed string so a note is
	// never dropped over a formatting disagreement. Optional.
	Time string `json:"ts,omitempty"`
}

// Index holds the notes that are in force for one repository, in the order
// written: superseded and replaced notes are already left out.
type Index struct {
	notes []Note
	// read counts every usable line, retired or not, so a caller polling
	// for changes sees a note that retires another as a change.
	read int
}

// NotesFile is the file notes for the worktree at path are appended to, or
// "" when path is not inside a git repository.
func NotesFile(path string) string { return store.Path(path, FileName) }

// Load reads the notes for the worktree's repository. A missing or
// unreadable file yields an empty index rather than an error: notes are
// optional, and a repository nobody has annotated is a normal state, not a
// failure.
func Load(worktreePath string) Index {
	var all []Note
	if f := NotesFile(worktreePath); f != "" {
		all = append(all, readNotes(f)...)
	}
	return Index{notes: inForce(all), read: len(all)}
}

func readNotes(path string) []Note {
	var notes []Note
	store.ReadLines(path, func(line []byte) {
		var n Note
		if json.Unmarshal(line, &n) != nil {
			return // a malformed line loses one note, not the file
		}
		// Agents write this file, by hand too: what it says is drawn on
		// the terminal, so it is made safe to draw (see package clean).
		n.File = filepath.ToSlash(strings.TrimPrefix(clean.Line(n.File), "./"))
		n.Anchor = strings.TrimSpace(clean.Line(n.Anchor))
		n.Replaces = strings.TrimSpace(clean.Line(n.Replaces))
		n.Short = clean.Line(n.Short)
		n.Detail = clean.Text(n.Detail)
		n.Branch, n.Agent, n.Time = clean.Line(n.Branch), clean.Line(n.Agent), clean.Line(n.Time)
		file, local := store.Local(n.File)
		if n.File == "" || !local || n.Anchor == "" || strings.TrimSpace(n.Short) == "" {
			return
		}
		n.File = file
		notes = append(notes, n)
	})
	return notes
}

// inForce drops what later notes have overruled, keeping the order
// written: an earlier note for the same anchor (re-explaining a piece of
// code supersedes the old reason) and a note another one names in Replaces
// (the code it explained was rewritten, and the new note says why).
func inForce(all []Note) []Note {
	retired := map[string]bool{}
	var keep []Note
	for i := len(all) - 1; i >= 0; i-- {
		n := all[i]
		key := n.File + "\x00" + n.Anchor
		if retired[key] {
			continue
		}
		retired[key] = true
		if n.Replaces != "" {
			retired[n.File+"\x00"+n.Replaces] = true
		}
		keep = append(keep, n)
	}
	for i, j := 0, len(keep)-1; i < j; i, j = i+1, j-1 {
		keep[i], keep[j] = keep[j], keep[i]
	}
	return keep
}

// Len reports how many notes are in force.
func (ix Index) Len() int { return len(ix.notes) }

// Read reports how many notes were read, including those later notes
// overruled. It only grows as notes are appended, so it is what to compare
// to notice that something was written.
func (ix Index) Read() int { return ix.read }

// Match finds the note whose anchor appears in line, for file (a
// worktree-relative path). Where several do, the most recently written
// wins.
//
// Matching a line rather than a hunk is what lets a note outlive the change
// that produced it: once the work is committed there are no hunks left, but
// the line is still in the file.
func (ix Index) Match(file, line string) (Note, bool) {
	file = filepath.ToSlash(file)
	line = strings.TrimSpace(line)
	if line == "" {
		return Note{}, false
	}
	for i := len(ix.notes) - 1; i >= 0; i-- {
		n := ix.notes[i]
		if n.File == file && strings.Contains(line, n.Anchor) {
			return n, true
		}
	}
	return Note{}, false
}

// All returns every note in force, newest first.
func (ix Index) All() []Note {
	out := make([]Note, 0, len(ix.notes))
	for i := len(ix.notes) - 1; i >= 0; i-- {
		out = append(out, ix.notes[i])
	}
	return out
}

// Locate finds the line in the worktree's current copy of the note's file
// that the note is anchored to, 1-based. ok is false when the anchor is no
// longer there — the code has been rewritten since, and the note needs
// writing again rather than showing against something it no longer
// describes.
func (n Note) Locate(worktreePath string) (line int, ok bool) {
	// Not through a link, nor waiting on a FIFO an agent left: see
	// git.ReadWorktreeFile.
	data, err := git.ReadWorktreeFile(worktreePath, n.File)
	if err != nil {
		return 0, false
	}
	if lines := anchorLines(data, n.Anchor); len(lines) > 0 {
		return lines[0], true
	}
	return 0, false
}

// anchorLines lists the 1-based lines of src that contain anchor.
func anchorLines(src, anchor string) []int {
	anchor = strings.TrimSpace(anchor)
	if anchor == "" {
		return nil
	}
	var out []int
	for i, l := range strings.Split(src, "\n") {
		if strings.Contains(l, anchor) {
			out = append(out, i+1)
		}
	}
	return out
}

// With is ix with extra notes added after its own, so on a line both match
// the extra one is shown; read counts the records they were read from, for
// Read. The review panel adds a code review's findings this way.
func (ix Index) With(extra []Note, read int) Index {
	ix.notes = append(append([]Note{}, ix.notes...), extra...)
	ix.read += read
	return ix
}
