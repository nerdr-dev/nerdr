package hooks

import "net"

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
}

// maxChain is how far up from a peer nerdr looks for an agent.
const maxChain = 64

// peerOf is the process at the other end of c.
func peerOf(c net.Conn) Peer {
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
	for len(p.Chain) < maxChain && pid > 0 {
		p.Chain = append(p.Chain, pid)
		if pid == 1 {
			break
		}
		next, err := parentPid(pid)
		if err != nil || next == pid {
			break
		}
		pid = next
	}
	return p
}
