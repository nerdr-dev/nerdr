package core

import "github.com/nerdr-dev/nerdr/internal/agents"

// ReviewedBy reports whether kind k is among t's reviewers.
func (t *Tab) ReviewedBy(k agents.Kind) bool {
	if t.Review == nil {
		return false
	}
	for _, s := range t.Review.Seats {
		if s.R.Agent == k {
			return true
		}
	}
	return false
}
