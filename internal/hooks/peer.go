package hooks

import (
	"net"

	"github.com/nerdr-dev/nerdr/internal/spawn"
)

// Peer is the process at the other end of a connection to the socket,
// and those it runs under. The kernel says which process it is, so one
// cannot pass for another the way it can with a tab's secret: nerdr
// takes a report as from the agent of the tab it names only when that
// agent's process is in Chain (see app's vouch).
type Peer struct {
	// Known is false where nerdr cannot ask the kernel.
	Known bool
	// Chain is the process, then its parent, and so on up to the first.
	Chain []int
	// Starts are when each process in Chain started, by the kernel's
	// clock: a process given the id of one that has ended has another.
	Starts []uint64
	// Cut is true when Chain stops at maxChain, short of the first
	// process: what it runs under is not known.
	Cut bool
}

// Started is when the process pid started, as Peer's Starts have it;
// 0 when nerdr cannot tell.
func Started(pid int) uint64 {
	if pid <= 0 {
		return 0
	}
	_, start, err := spawn.ProcInfo(pid)
	if err != nil {
		return 0
	}
	return start
}

// StartOf is when the process pid in p's Chain started, 0 when it is not
// there or nerdr cannot tell.
func (p Peer) StartOf(pid int) uint64 {
	for i, c := range p.Chain {
		if c == pid && i < len(p.Starts) {
			return p.Starts[i]
		}
	}
	return 0
}

// maxChain is how far up from a peer nerdr looks for an agent.
const maxChain = 64

// PeerOf is the process at the other end of c: as the hook socket asks it,
// and the client socket (see internal/attach).
func PeerOf(c net.Conn) Peer {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return Peer{}
	}
	rc, err := uc.SyscallConn()
	if err != nil {
		return Peer{}
	}
	pid, perr := 0, error(nil)
	if err := rc.Control(func(fd uintptr) { pid, perr = peerPid(int(fd)) }); err != nil || perr != nil || pid <= 0 {
		// Asked and not told: a process nerdr cannot place.
		return Peer{Known: peerSupported}
	}
	p := Peer{Known: true}
	for pid > 0 {
		if len(p.Chain) == maxChain {
			p.Cut = true
			break
		}
		next, start, err := spawn.ProcInfo(pid)
		p.Chain = append(p.Chain, pid)
		p.Starts = append(p.Starts, start) // 0 when not known
		if err != nil || pid == 1 || next == pid {
			break
		}
		pid = next
	}
	return p
}
