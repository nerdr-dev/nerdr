package core

import (
	"os"

	"github.com/nerdr-dev/nerdr/internal/agents"
)

// The search of the agents' conversations: their logs are read here,
// where the agents run, into an index core keeps (see agents.Index), and
// searched away from the loop. A client names each read and search by a
// Job of its own, and the answer by it.

func init() { registerRequests(ReadConversations{}, SearchConversations{}) }

// conversations is the index of the agents' conversations, made on the
// first read.
func (m *Core) conversations() *agents.Index {
	if m.convIndex == nil {
		m.convIndex = &agents.Index{}
	}
	return m.convIndex
}

// ReadConversations reads the conversations had in the folders nerdr
// works in (SearchRoots), again only from a log that has changed: told as
// a ConversationsReadMsg.
type ReadConversations struct{ Job string }

// ConversationsReadMsg says ReadConversations of Job is done, and how many
// conversations there are.
type ConversationsReadMsg struct {
	Job string
	N   int
}

func (q ReadConversations) Do(m *Core) Cmd {
	idx, roots := m.conversations(), m.SearchRoots()
	return Later(func() Event {
		idx.Update(roots)
		return ConversationsReadMsg{Job: q.Job, N: idx.Len()}
	})
}

// SearchConversations finds the conversations that have all of Words:
// told as a SearchHitsMsg.
type SearchConversations struct {
	Job   string
	Words []string
}

// SearchHitsMsg is what SearchConversations of Job found, at most
// SearchLimit. Each hit's conversation has only what was said where it
// was found (At 0), not all of it; Gone is whether the folder it was had
// in is gone, so it cannot be carried on there.
type SearchHitsMsg struct {
	Job  string
	Hits []agents.Hit
	Gone []bool
}

func (q SearchConversations) Do(m *Core) Cmd {
	idx := m.conversations()
	return Later(func() Event {
		msg := SearchHitsMsg{Job: q.Job}
		for _, h := range idx.Search(q.Words, SearchLimit) {
			c := *h.Conv
			c.Said = c.Said[h.At : h.At+1]
			msg.Hits = append(msg.Hits, agents.Hit{Conv: &c, Count: h.Count})
			st, err := os.Stat(c.Dir)
			msg.Gone = append(msg.Gone, err != nil || !st.IsDir())
		}
		return msg
	})
}
