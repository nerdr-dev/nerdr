package review

import (
	"nerdr/internal/codereview"
	"nerdr/internal/why"
)

// loadNotes is the why notes for the worktree, with the findings of its
// latest code review (see internal/codereview), every reviewer's, added:
// they are shown the same way, against the lines they are about.
func loadNotes(worktree string) why.Index {
	recs, read := codereview.Latest(worktree)
	var extra []why.Note
	for _, rec := range recs {
		extra = append(extra, codereview.Notes(rec)...)
	}
	return why.Load(worktree).With(extra, read)
}
