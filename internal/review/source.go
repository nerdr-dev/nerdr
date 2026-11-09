package review

import "github.com/nerdr-dev/nerdr/internal/reviewsource"

// Source is what the panel reads the repositories through; see
// reviewsource.
type Source = reviewsource.Source

// Local is the Source that reads the repositories on this machine.
type Local = reviewsource.Local
