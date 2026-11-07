package core

import (
	"os"
	"slices"

	"github.com/nerdr-dev/nerdr/internal/hooks"
)

// ClientRefusal is why a client connecting to the client socket from
// peer p is refused, "" when it is not; see internal/attach. Mu is held.
//
// A client can do anything the user can, so one that may be a program
// nerdr runs is refused: one under an agent, as Vouch places reports; one
// under nerdr or its holder, which run every program of the tabs, agents,
// terminals and plugins alike; one nerdr cannot place at all, unknown
// counting as in a sandbox. A program that has left the tree gets past
// this, so the socket's folder and key guard it too.
func (m *Core) ClientRefusal(p hooks.Peer) string {
	const why = "nerdr takes no client from a program it runs: connect from your own terminal"
	if !p.Known || len(p.Chain) == 0 {
		return "nerdr cannot tell which program connected, and takes no client it cannot place"
	}
	own := []int{os.Getpid()}
	if m.Held != nil && m.Held.Client != nil {
		// The holder is the parent of every program.
		own = append(own, m.Held.Client.Pid())
	}
	for _, pid := range p.Chain {
		if slices.Contains(own, pid) {
			return why
		}
	}
	for _, a := range m.AllAgents() {
		if !Vouch(hooks.Report{Peer: p}, &a.AgentState).Unvouched {
			return why
		}
	}
	return ""
}
