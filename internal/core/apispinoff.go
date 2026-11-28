package core

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/spinoff"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// The parts an agent split its work into, as the API has them: they wait
// on its tab, whose agent is blocked until the user has been through them,
// first to last, making a tab for each (spinoff_create) or not
// (spinoff_skip), as the TUI's spinoff dialog does. Parts made on another
// machine, as the TUI can, are not the API's.

// APISpinoff is the parts a tab's agent sent to split off into new tabs,
// waiting for the user: Parts in the order they are gone through, only the
// first answered.
type APISpinoff struct {
	Parts []APISpinoffPart `json:"parts"`
	// Done is how many parts of the plan have been gone through already:
	// the first of Parts is part Done+1 of Done+len(Parts).
	Done int `json:"done,omitempty"`
	// Busy says what is being done about the first part, while it is
	// ("making fix-a…", waiting for the answer to whether the
	// repositories' setup may run, or being made on another machine from
	// nerdr's window): it cannot be answered meanwhile.
	Busy string `json:"busy,omitempty"`
	// Failed is why the tab for the first part was not made the last time
	// it was tried, for the user to put right, such as a branch name
	// taken.
	Failed *APISpinoffFailed `json:"failed,omitempty"`
}

// APISpinoffPart is one part to split off: ID is what spinoff_create and
// spinoff_skip name it by, Branch the name the agent proposes, Summary
// what the user reads of it, Commits those in the tab's branch with its
// work already. Repos are the folders of the tab's repositories the agent
// named for it, InRepos, when it named none, those its commits are in.
// Sandboxed is set when an agent in a sandbox sent it: only an agent set
// sandboxed may run it. PromptReady is set once the agent has sent the
// prompt the new agent is to be given; the tab cannot be made before.
type APISpinoffPart struct {
	ID          string   `json:"id"`
	Branch      string   `json:"branch"`
	Summary     string   `json:"summary"`
	Commits     []string `json:"commits,omitempty"`
	Repos       []string `json:"repos,omitempty"`
	InRepos     []string `json:"in_repos,omitempty"`
	Sandboxed   bool     `json:"sandboxed,omitempty"`
	PromptReady bool     `json:"prompt_ready"`
	// Kinds are the agents that may run in its tab, and Kind the one
	// picked when spinoff_create names none; "" when there is none.
	Kinds []string `json:"kinds"`
	Kind  string   `json:"kind,omitempty"`
}

// APISpinoffFailed is why a part's tab was not made: Branch is the name
// it was to have, Err why, in a sentence for people.
type APISpinoffFailed struct {
	Branch string `json:"branch"`
	Err    string `json:"err"`
}

// apiSpinoff is t's parts waiting, nil when none do.
func (m *Core) apiSpinoff(t *Tab) *APISpinoff {
	p := t.Spinoff
	if p == nil || len(p.Next) == 0 {
		return nil
	}
	s := &APISpinoff{Parts: []APISpinoffPart{}, Done: len(p.Done), Busy: t.SpinoffBusy}
	if f := t.SpinoffFailed; f != nil {
		s.Failed = &APISpinoffFailed{Branch: f.Branch, Err: f.Err}
	}
	for _, pt := range p.Next {
		j := APISpinoffPart{ID: pt.ID, Branch: pt.Branch, Summary: pt.Summary, Commits: pt.Commits, Repos: pt.Repos,
			InRepos: pt.InRepos, Sandboxed: pt.Sandboxed, PromptReady: pt.Prompt != "", Kinds: []string{},
			Kind: string(m.SpinoffKind(t, pt))}
		for _, k := range m.SpinoffKinds(pt) {
			j.Kinds = append(j.Kinds, string(k))
		}
		s.Parts = append(s.Parts, j)
	}
	return s
}

// APISpinoffMake is a part's tab the API asks for: tab Tab's part Part,
// on branch Branch (the agent's when ""), with agent Kind (as the dialog
// picks when ""), starting with the part's commits unless Reuse is false,
// in the repositories named Repos (as the agent asked when none).
type APISpinoffMake struct {
	Tab, Part, Branch, Kind string
	Reuse                   *bool
	Repos                   []string
}

// apiSpinoffFirst is the tab id names, and its part named part, which must
// be the first of those waiting, and not being made.
func (m *Core) apiSpinoffFirst(id, part string) (*Tab, spinoff.Part, error) {
	if m.Quitting {
		return nil, spinoff.Part{}, ErrAPIQuitting
	}
	t := TabList(m.Tabs).Find(id)
	if t == nil {
		return nil, spinoff.Part{}, fmt.Errorf("there is no tab %q", id)
	}
	t = t.Owner()
	p := t.Spinoff
	if p == nil || len(p.Next) == 0 {
		return nil, spinoff.Part{}, fmt.Errorf("no parts to split off wait in %s", t.Name)
	}
	pt := p.Next[0]
	switch {
	case part == "":
		return nil, pt, errors.New("name the part, by its id: the first is " + pt.ID)
	case pt.ID != part && slices.ContainsFunc(p.Next, func(x spinoff.Part) bool { return x.ID == part }):
		return nil, pt, fmt.Errorf("part %s is not the first: the parts are gone through in order, and %s (%s) is first", part, pt.ID, pt.Branch)
	case pt.ID != part:
		return nil, pt, fmt.Errorf("no part %s waits in %s: it has been gone through, or is not one of its", part, t.Name)
	case t.SpinoffBusy != "":
		return nil, pt, errors.New("the part's tab is being made: " + t.SpinoffBusy)
	}
	return t, pt, nil
}

// APISpinoffCreate starts making the tab q asks for, as the spinoff
// dialog does with enter, with all the repositories nerdr has, found by
// RepoPlaces.Scan, to look for those named in. The tab comes once its
// worktrees are made, the part then gone through; when it cannot be made,
// the part stays first, with why in its tab's spinoff.failed. Before it,
// nerdr may ask whether the repositories' setup may run (repo_setup).
func (m *Core) APISpinoffCreate(q APISpinoffMake, all []workspace.Repo) (Cmd, error) {
	t, pt, err := m.apiSpinoffFirst(q.Tab, q.Part)
	if err != nil {
		return nil, err
	}
	if pt.Prompt == "" {
		return nil, errors.New("the agent has not sent this part's prompt yet: make it once prompt_ready is set")
	}
	if m.TabLimitReached() {
		return nil, errors.New("the tab limit (max_tabs) is reached: close a tab first, or skip this part")
	}
	kinds := m.SpinoffKinds(pt)
	kind := agents.Kind(strings.ToLower(strings.TrimSpace(q.Kind)))
	switch {
	case kind == "" && len(kinds) == 0 && pt.Sandboxed && len(m.Available()) > 0:
		return nil, errors.New("an agent in a sandbox sent this part, and no agent is set sandboxed = true: skip it")
	case kind == "" && len(kinds) == 0:
		return nil, errors.New("found no agent on PATH")
	case kind == "":
		kind = m.SpinoffKind(t, pt)
	case !slices.Contains(m.Available(), kind):
		return nil, fmt.Errorf("there is no agent %q here; those there are: %s", kind, m.KindList())
	case pt.Sandboxed && !m.SandboxedKind(kind):
		return nil, fmt.Errorf("an agent in a sandbox sent this part, and %s is not set sandboxed: pick one that is (%s)", kind, m.SandboxedKinds())
	}
	name := strings.TrimSpace(q.Branch)
	if name == "" {
		name = pt.Branch
	}
	if err := workspace.ValidBranch(name); err != nil {
		return nil, err
	}
	others := func(have []SpinoffRepo) []workspace.Repo {
		var out []workspace.Repo
		for _, r := range all {
			r.Path = filepath.Clean(r.Path)
			if !slices.ContainsFunc(have, func(h SpinoffRepo) bool { return h.Path == r.Path }) {
				out = append(out, r)
			}
		}
		return out
	}
	c := ChooseSpinoff(t.Worktrees, pt, nil, others)
	if len(q.Repos) > 0 {
		if c, err = pickSpinoffRepos(c, q.Repos, others); err != nil {
			return nil, err
		}
	}
	rewrite := q.Reuse != nil && !*q.Reuse
	req, err := c.Create(t, m.Ref(t), pt, kind, name, rewrite)
	if err != nil {
		return nil, err
	}
	return req.Do(m), nil
}

// pickSpinoffRepos is c with only the repositories named ticked: by their
// folder in the tab, or their name, the tab's own first, and then the
// others nerdr has.
func pickSpinoffRepos(c SpinoffChoice, names []string, others func(have []SpinoffRepo) []workspace.Repo) (SpinoffChoice, error) {
	c.On = make([]bool, len(c.Repos))
	var unknown []string
	for _, n := range names {
		n = strings.TrimSpace(n)
		if i := slices.IndexFunc(c.Repos, func(r SpinoffRepo) bool { return r.Label == n || r.Name == n }); i >= 0 {
			c.On[i] = true
			continue
		}
		all := others(c.Repos)
		if i := slices.IndexFunc(all, func(r workspace.Repo) bool { return r.Name == n }); i >= 0 {
			c.Repos = append(c.Repos, SpinoffRepo{Repo: all[i], Label: all[i].Name})
			c.On = append(c.On, true)
			continue
		}
		unknown = append(unknown, n)
	}
	if len(unknown) > 0 {
		return c, fmt.Errorf("there is no repository %s: name the tab's by their folder, or another that repos lists", strings.Join(unknown, ", "))
	}
	return c, nil
}

// APISpinoffSkip goes on past tab id's first part, named part, with no
// tab made for it, as the spinoff dialog's skip does: the agent that sent
// it is told, and keeps the work.
func (m *Core) APISpinoffSkip(id, part string) (Cmd, error) {
	t, pt, err := m.apiSpinoffFirst(id, part)
	if err != nil {
		return nil, err
	}
	return SpinoffNext{T: m.Ref(t), Outcome: spinoff.Outcome{Part: pt, Skipped: true}}.Do(m), nil
}
