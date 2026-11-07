package core

// The requests a client may make of core; see Request. Each is done with
// Mu held, the tabs it names looked up afresh.

func init() {
	registerRequests(
		Save{},
	)
}

// Save saves the session now, with how the client lays out the tabs.
type Save struct{}

func (Save) Do(m *Core) Cmd {
	m.SaveSession()
	return nil
}
