package holder

// InProcess is a holder served in this process and a client of it, as a
// core would have: for tests of a core over a holder, with no process of
// its own. Shutdown on the client ends it.
func InProcess() (*Client, error) {
	a, b, err := connPair()
	if err != nil {
		return nil, err
	}
	go New().Serve(a)
	return newClient(b)
}
