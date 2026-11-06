package holder

// InProcess is a holder served in this process and a client of it, as a
// core would have: for tests of a core over a holder, with no process of
// its own. Shutdown on the client ends it.
func InProcess() (*Client, error) {
	return New().Connect()
}

// Connect serves a new core on h, in this process, and is that core's
// client: for tests, a core started again after another went.
func (h *Holder) Connect() (*Client, error) {
	a, b, err := connPair()
	if err != nil {
		return nil, err
	}
	go h.Serve(a)
	return newClient(b)
}
