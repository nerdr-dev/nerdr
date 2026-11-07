package core

// The requests that answer what core asks the user, and ask it.

func init() { registerRequests(Answer{}, AskSetup{}) }

// Answer answers question ID with Answer: only a client can, and only by
// the user's hand.
type Answer struct {
	ID     int
	Answer string
}

func (q Answer) Do(m *Core) Cmd { return m.Answer(q.ID, q.Answer) }

// AskSetup asks the user, through core, whether the .nerdr.toml of each of
// Repos not yet allowed as it is now may be done; once they have said, the
// client is told by Token (SetupAnsweredMsg) and goes on.
type AskSetup struct {
	Token int
	Repos []string
}

func (q AskSetup) Do(m *Core) Cmd {
	return m.AskSetup(q.Repos, func() Cmd { return m.Tell(SetupAnsweredMsg{Token: q.Token}) })
}

// SetupAnsweredMsg is the user done with the question of AskSetup Token.
type SetupAnsweredMsg struct{ Token int }
