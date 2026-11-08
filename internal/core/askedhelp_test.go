package core

import "github.com/nerdr-dev/nerdr/internal/agents"

// askedHelp asks each of specs' agents what starting it on a new
// conversation asks its CLI (see agents.NewSessionKnown), as a core that
// has started them before has: then Start starts them at once.
func askedHelp(specs map[agents.Kind]agents.Spec) {
	for k := range specs {
		agents.NewSession(specs, k)
	}
}
