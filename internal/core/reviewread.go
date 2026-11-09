package core

import "github.com/nerdr-dev/nerdr/internal/reviewsource"

// What the review panel reads of the repositories it shows, read here
// where they are: the panel may be on another machine. It names each read
// by a Job of its own, and the answer by it.

func init() { registerRequests(ReadForReview{}) }

// ReadForReview makes the panel's read Q of the repositories: told as a
// ReviewReadMsg.
type ReadForReview struct {
	Job string
	Q   reviewsource.Query
}

// ReviewReadMsg is the answer to ReadForReview's Job.
type ReviewReadMsg struct {
	Job string
	A   reviewsource.Answer
}

func (q ReadForReview) Do(m *Core) Cmd {
	return Later(func() Event {
		return ReviewReadMsg{Job: q.Job, A: q.Q.Run(reviewsource.Local{})}
	})
}
