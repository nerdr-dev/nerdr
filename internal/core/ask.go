package core

import (
	"slices"
	"sort"
	"time"

	"github.com/nerdr-dev/nerdr/internal/plugin"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// Asked is what only the user can answer, waiting in core until a client
// does. The client is told of it with AskMsg, shows it, and answers with
// Answer; core goes on from there.

// AskKind is what a Question is about.
type AskKind int

const (
	// AskRepoSetup is whether the .nerdr.toml of Repos may be done in a new
	// worktree: Setup is what each asks for. AnswerAllowed allows it until it
	// changes; any other answer goes on without it.
	AskRepoSetup AskKind = iota + 1
	// AskPluginRun is whether agent A may run plugin P with Vals, in the
	// folder Dir; Confined, that A is in a sandbox and P runs outside it.
	// Unanswered by Until, it is RunUnanswered. AnswerAllowed runs it;
	// RunDeclined or RunBusy do not.
	AskPluginRun
)

// AnswerAllowed is yes, to any Question.
const AnswerAllowed = "allowed"

// Question is one thing asked of the user; see Asked.
type Question struct {
	ID   int
	Kind AskKind

	Repos []string
	Setup map[string]workspace.RepoConfig

	A        *Tab
	P        plugin.Plugin
	Vals     map[string]any
	Dir      string
	Confined bool
	Until    time.Time

	then func(answer string) Cmd
}

// AskMsg tells the client of Q, to show; AnsweredMsg, that Q is answered, by
// the client or by its time running out, and is to be shown no more.
type (
	AskMsg      struct{ Q *Question }
	AnsweredMsg struct{ Q *Question }
)

// Ask puts q to the user, then goes on with its answer.
func (m *Core) Ask(q *Question, then func(answer string) Cmd) Cmd {
	m.lastAsk++
	q.ID, q.then = m.lastAsk, then
	m.Asking = append(m.Asking, q)
	return m.Tell(AskMsg{Q: q})
}

// Answer answers q, once: a second answer, or one to a question gone, is
// nothing.
func (m *Core) Answer(q *Question, answer string) Cmd {
	i := slices.Index(m.Asking, q)
	if i < 0 {
		return nil
	}
	m.Asking = slices.Delete(m.Asking, i, i+1)
	m.Tell(AnsweredMsg{Q: q})
	if q.Kind == AskRepoSetup && answer == AnswerAllowed {
		file := SetupAllowedFile()
		for _, r := range q.Repos {
			if err := workspace.Allow(file, r, q.Setup[r]); err != nil {
				m.Toast("could not keep that it is allowed: " + err.Error())
			}
		}
	}
	return q.then(answer)
}

// Asked reports whether a question of kind is waiting.
func (m *Core) Asked(kind AskKind) bool {
	return slices.ContainsFunc(m.Asking, func(q *Question) bool { return q.Kind == kind })
}

// ExpireAsks answers RunUnanswered to the runs asked about too long ago.
func (m *Core) ExpireAsks(now time.Time) Cmd {
	var cmds []Cmd
	for _, q := range slices.Clone(m.Asking) {
		if q.Kind == AskPluginRun && now.After(q.Until) {
			cmds = append(cmds, m.Answer(q, RunUnanswered))
			m.Toast(m.AgentLabel(q.A.Owner(), q.A) + "'s run of " + q.P.Name + " was not answered in time, and did not run")
		}
	}
	return Batch(cmds...)
}

// AskSetup goes on with then once the user has said whether the .nerdr.toml
// of each of repos may be done, asking only about those not yet allowed as
// they are now. With no client the question waits for one.
func (m *Core) AskSetup(repos []string, then func() Cmd) Cmd {
	ask := workspace.ToAllow(SetupAllowedFile(), repos)
	if len(ask) == 0 {
		return then()
	}
	var names []string
	for r := range ask {
		names = append(names, r)
	}
	sort.Strings(names)
	return m.Ask(&Question{Kind: AskRepoSetup, Repos: names, Setup: ask}, func(string) Cmd { return then() })
}
