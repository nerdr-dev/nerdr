// Package order reads the reading order an agent recorded for the change
// it made: which files carry the change, which ones only follow from it,
// and what the whole thing is for.
//
// A diff is hard to read not because any single line is unclear, but
// because nothing says where to start. The agent that made the change is
// the one that knows, so it records this itself when the user asks it to
// explain the change (see the nerdr-explain skill), rather than nerdr
// asking another agent, which would only be guessing at what it knew.
//
// Records are kept and appended to as described in internal/store. An entry
// names a file rather than a line: a role does not drift when the code
// above it moves, so there is nothing to anchor. A later entry for the same
// file supersedes the earlier one, which is how an agent that carries on
// working updates what it said without rewriting anything.
//
// The sequence is not a number the agent has to keep in order. It follows
// from the role, so a file added to the change later falls into place on
// its own.
package order

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/clean"
	"github.com/nerdr-dev/nerdr/internal/store"
)

// FileName is the reading order's file, in the folder internal/store keeps
// records in.
const FileName = "order.jsonl"

// The roles, in the sequence they are meant to be read: what the change
// really is, what had to follow from it, the tests that pin it down, and
// last the edits that are the same thing done many times.
const (
	RoleCore       = "core"
	RoleFollows    = "follows"
	RoleTest       = "test"
	RoleMechanical = "mechanical"
)

var roles = []string{RoleCore, RoleFollows, RoleTest, RoleMechanical}

// Label is what a role is called on screen.
func Label(role string) string {
	switch role {
	case RoleCore:
		return "the change itself"
	case RoleFollows:
		return "follows from it"
	case RoleTest:
		return "tests"
	case RoleMechanical:
		return "mechanical"
	}
	return role
}

// Roles lists the roles in reading sequence.
func Roles() []string { return append([]string(nil), roles...) }

// Valid reports whether role is one this package knows.
func Valid(role string) bool {
	for _, r := range roles {
		if r == role {
			return true
		}
	}
	return false
}

// Entry is one recorded line of the reading order: a file and its part in
// the change, or — with no file — what the change as a whole does.
type Entry struct {
	// File is the path the entry is about, relative to the worktree. Empty
	// for the summary of the change.
	File string `json:"file,omitempty"`
	// Role is the file's part in the change; see the Role constants.
	Role string `json:"role,omitempty"`
	// Short is the one-line description, of the file or of the change.
	Short string `json:"short"`
	// Branch is the branch the entry was recorded on. A reading order
	// describes one change, so entries from another branch are not shown.
	// Empty when recorded on a detached HEAD, and then shown only there.
	Branch string `json:"branch,omitempty"`
	// Agent names who recorded it. Optional.
	Agent string `json:"agent,omitempty"`
	// Time is when it was recorded, as an unparsed string. Optional.
	Time string `json:"ts,omitempty"`
}

// Index holds the reading order in force for one branch.
type Index struct {
	summary string
	byFile  map[string]Entry
	// seen keeps the order files were first described in, so files sharing
	// a role stay in the sequence the agent introduced them rather than
	// jumping about as it updates them.
	seen []string
	read int
}

// File is where the reading order for the worktree at path is appended to,
// or "" when path is not inside a git repository.
func File(path string) string { return store.Path(path, FileName) }

// Load reads the reading order recorded for branch in the worktree's
// repository. Entries recorded on another branch are left out: they
// describe a different change, and a stale order is worse than none. So
// are entries older than the branch, left by an earlier branch of the same
// name that has since been deleted.
func Load(worktreePath, branch string) Index {
	created, _ := store.BranchCreated(worktreePath, branch)
	return load(File(worktreePath), branch, created)
}

// timeLayout is how Add writes an entry's time: local, to the minute.
const timeLayout = "2006-01-02 15:04"

// before reports whether e was recorded before created. The time is only
// to the minute, so an entry from the minute the branch was made counts as
// after it; an entry with no readable time cannot be placed and does not.
func before(e Entry, created time.Time) bool {
	if created.IsZero() {
		return false
	}
	t, err := time.ParseInLocation(timeLayout, e.Time, time.Local)
	return err == nil && t.Before(created.Truncate(time.Minute))
}

func load(path, branch string, created time.Time) Index {
	ix := Index{byFile: map[string]Entry{}}
	store.ReadLines(path, func(line []byte) {
		var e Entry
		if json.Unmarshal(line, &e) != nil {
			return // a malformed line loses one entry, not the file
		}
		// Drawn on the terminal: see package clean.
		e.File = filepath.ToSlash(strings.TrimPrefix(clean.Line(e.File), "./"))
		e.Short = strings.TrimSpace(clean.Line(e.Short))
		e.Agent, e.Time = clean.Line(e.Agent), clean.Line(e.Time)
		if e.Short == "" {
			return
		}
		// An exact match, the empty branch of a detached HEAD included: one
		// side having no name must not let every branch's order through.
		if e.Branch != branch || before(e, created) {
			return
		}
		ix.read++
		if e.File == "" {
			ix.summary = e.Short
			return
		}
		file, local := store.Local(e.File)
		if !local {
			return
		}
		e.File = file
		if !Valid(e.Role) {
			return
		}
		if _, ok := ix.byFile[e.File]; !ok {
			ix.seen = append(ix.seen, e.File)
		}
		ix.byFile[e.File] = e
	})
	return ix
}

// Summary is what the change as a whole does, or "" when nobody said.
func (ix Index) Summary() string { return ix.summary }

// For returns the entry recorded for file.
func (ix Index) For(file string) (Entry, bool) {
	e, ok := ix.byFile[filepath.ToSlash(file)]
	return e, ok
}

// Len reports how many files have been described.
func (ix Index) Len() int { return len(ix.byFile) }

// Read reports how many usable lines were read, superseded ones included,
// so a caller polling for changes sees an update as a change.
func (ix Index) Read() int { return ix.read }

// Files lists the described files in reading sequence: by role, and within
// a role in the order they were first described.
func (ix Index) Files() []string {
	var out []string
	for _, role := range roles {
		for _, f := range ix.seen {
			if ix.byFile[f].Role == role {
				out = append(out, f)
			}
		}
	}
	return out
}

// LoadFile reads a reading order from an explicit path, for tests and for
// tools that know where the file is.
func LoadFile(path, branch string) Index { return load(path, branch, time.Time{}) }

// Prune drops the entries no branch will show again from the reading order
// of the worktree's repository: those of branches that are gone, and those
// older than the branch now holding their name. A reading order lasts as
// long as its branch; once the change is merged, what it is for is in the
// history, and why.jsonl keeps the reasons that still matter.
//
// With the notes of every repository in one folder (an absolute data_dir)
// it does nothing: two repositories with the same name share a file there,
// and one's branches are not the other's.
func Prune(worktreePath string) (int, error) {
	if filepath.IsAbs(store.DataDir) {
		return 0, nil
	}
	path := File(worktreePath)
	if path == "" {
		return 0, nil
	}
	branches, err := store.Branches(worktreePath)
	if err != nil {
		return 0, err
	}
	created := map[string]time.Time{}
	return store.Rewrite(path, func(line []byte) bool {
		var e Entry
		if json.Unmarshal(line, &e) != nil || e.Branch == "" {
			return true // not ours to judge: kept as it is
		}
		if !branches[e.Branch] {
			return false
		}
		c, ok := created[e.Branch]
		if !ok {
			c, _ = store.BranchCreated(worktreePath, e.Branch)
			created[e.Branch] = c
		}
		return !before(e, c)
	})
}
