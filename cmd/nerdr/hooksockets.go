package main

import (
	"errors"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/hooks"
)

// guardEvery is how often the socket for the agents in a sandbox is
// looked at, to listen there again when one has replaced it.
const guardEvery = 2 * time.Second

// hookSockets are the sockets the agents report to: sandbox for those
// nerdr starts in a sandbox, host for the rest. Each is nil when it
// could not be listened on, and its error says why.
type hookSockets struct {
	sandbox, host       *hooks.Server
	sandboxErr, hostErr error
	// taken is set when another nerdr had hooks.SocketPath, and sandbox
	// is one of this nerdr's own beside it.
	taken error
}

// listenHooks listens on both sockets of the nerdr of folder root, each
// passing what it gets to send.
func listenHooks(root string, send func(hooks.Report)) hookSockets {
	var h hookSockets
	// One nerdr has the socket a sandbox is told of; another, started in
	// another folder, gets one of its own beside it.
	h.sandbox, h.sandboxErr = hooks.Listen(hooks.SocketPath(), send)
	if errors.Is(h.sandboxErr, hooks.ErrTaken) {
		h.taken = h.sandboxErr
		h.sandbox, h.sandboxErr = hooks.Listen(hooks.ForRoot(hooks.SocketPath(), root), send)
	}
	// The host's is the folder's own, so never another nerdr's.
	h.host, h.hostErr = hooks.ListenHost(root, send)
	return h
}

// give gives c the sockets, or why there are none, and has the one for
// the agents in a sandbox listened at again when it is replaced, telling
// c through post.
func (h hookSockets) give(c *core.Core, post func(core.Event)) {
	var sandbox, host string
	if h.sandbox != nil {
		sandbox = h.sandbox.Path()
		h.sandbox.Guard(guardEvery, func(err error) { post(core.SocketReplacedMsg{Path: sandbox, Err: err}) })
	}
	if h.host != nil {
		host = h.host.Path()
	}
	c.SetHookSocket(sandbox, host)
	if h.sandboxErr != nil {
		c.SetHookSocketError(h.sandboxErr)
	}
	if h.hostErr != nil {
		c.SetHostSocketError(h.hostErr)
	}
	if h.taken != nil && h.sandbox != nil {
		c.SetSocketShared(h.taken)
	}
}

// close stops listening on both.
func (h hookSockets) close() {
	if h.sandbox != nil {
		h.sandbox.Close()
	}
	if h.host != nil {
		h.host.Close()
	}
}
