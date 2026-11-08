package core

import (
	"os"
	"slices"

	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/spawn"
)

// RefusedQuitting is why a core that is quitting takes no client: one
// started once it has ended is.
const RefusedQuitting = "nerdr's core is quitting"

// ClientRefusal is why a client connecting to the client socket from
// peer p is refused, "" when it is not; see internal/attach. Mu is held.
//
// A client can do anything the user can, so one that may be a program
// nerdr runs is refused: one under an agent, as Vouch places reports; one
// under nerdr or its holder, which run every program of the tabs, agents,
// terminals and plugins alike; one nerdr cannot place at all, unknown
// counting as in a sandbox. A program that has left the tree gets past
// this, so the socket's folder and key guard it too. A program another
// nerdr runs, in another folder, is refused by ClientUnderNerdr.
func (m *Core) ClientRefusal(p hooks.Peer) string {
	if m.Quitting {
		return RefusedQuitting
	}
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
		// By its id alone, not as Vouch places a report: refusing one
		// given an ended agent's id costs nothing, and a start not known
		// must not let a client in.
		if a.Pane != nil && slices.Contains(p.Chain, a.Pane.Pid()) {
			return why
		}
	}
	return ""
}

// procArgs is spawn.Args, for tests to stand in for.
var procArgs = spawn.Args

// ClientUnderNerdr is why a client connecting from peer p is refused for
// running under a nerdr holder, that of any folder, "" when it does not:
// an agent in one folder must not drive the nerdr of another, whose core
// knows nothing of it to refuse it by (see ClientRefusal). The holder is
// known by its command line, `nerdr holder ...`, however nerdr was named
// and from whichever version: a program cannot change the command lines
// of the processes it runs under, and a chain the kernel would not give
// is refused already. A terminal of a tab is under a holder too, and is
// refused alike. It reads the process table, so it is not called with Mu
// held.
//
// A core that a program under another nerdr started (see
// holder.EnvNested) does not ask it: its own client is under that nerdr,
// as when an agent runs nerdr's tests, and the core is the program's, in
// its sandbox, as much as anything else it starts.
func ClientUnderNerdr(p hooks.Peer) string {
	for _, pid := range p.Chain {
		if args, err := procArgs(pid); err == nil && spawn.IsHolder(args) {
			return "nerdr takes no client from a program a nerdr runs, in any folder: connect from your own terminal"
		}
	}
	return ""
}
