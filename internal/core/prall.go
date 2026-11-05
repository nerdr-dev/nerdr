package core

import "time"

// PrAllEvery is how long a repository's open pull requests, asked for by
// the list, are taken as they were.
const PrAllEvery = 2 * time.Minute
