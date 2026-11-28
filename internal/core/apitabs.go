package core

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// What the API does to a tab's panes and to the tab itself: a terminal or
// an agent more, a code review, and closing the tab, as the user does in
// the TUI. Nothing it opens takes the keyboard there.

// apiTab is the tab id names, the tab of agent id too, or why nothing is
// to be done in it now.
func (m *Core) apiTab(id string) (*Tab, error) {
	if m.Quitting {
		return nil, ErrAPIQuitting
	}
	t := TabList(m.Tabs).Find(id)
	if t == nil {
		return nil, fmt.Errorf("there is no tab %q", id)
	}
	t = t.Owner()
	if t.ReposBusy {
		return nil, fmt.Errorf("%s: its repositories are being changed; try again in a moment", t.Name)
	}
	return t, nil
}

// APINewTerminal opens a terminal in tab id's worktree, in a new pane,
// last, and gives its ID.
func (m *Core) APINewTerminal(id string) (string, Cmd, error) {
	t, err := m.apiTab(id)
	if err != nil {
		return "", nil, err
	}
	p, cmd := m.NewShellIn(t, true)
	if p == nil {
		return "", cmd, errors.New("the terminal did not start; a notice says why")
	}
	return m.PaneRef(p).Pane, cmd, nil
}

// APINewAgent starts agent kind, with model when it is not "", beside tab
// id's own, in a new pane, last, given task as its first message when it
// is not "", and gives its ID.
func (m *Core) APINewAgent(id, kind, model, task string) (string, Cmd, error) {
	t, err := m.apiTab(id)
	if err != nil {
		return "", nil, err
	}
	k := agents.Kind(strings.ToLower(kind))
	if k == "" {
		return "", nil, fmt.Errorf("name the agent to start; those there are: %s", m.KindList())
	}
	if !slices.Contains(m.Available(), k) {
		return "", nil, fmt.Errorf("there is no agent %q here; those there are: %s", k, m.KindList())
	}
	if model != "" && !agents.TakesModel(k) {
		return "", nil, fmt.Errorf("nerdr does not know how to give %s a model", k)
	}
	a, cmd := m.NewAgentWith(t, k, model, strings.TrimSpace(task), true, false, nil)
	if a == nil {
		return "", nil, fmt.Errorf("there is no tab %q", id)
	}
	return m.Ref(a).Tab, cmd, nil
}

// APIClosePane closes the pane of the agent or terminal id, stopping what
// runs in it, as the user does in the TUI. The tab's first agent stays as
// long as the tab, so it is refused: close_tab closes that.
func (m *Core) APIClosePane(id string) (Cmd, error) {
	if m.Quitting {
		return nil, ErrAPIQuitting
	}
	var t *Tab
	var p *PaneTab
	if a := TabList(m.Tabs).Find(id); a != nil {
		t, p = a.Owner(), a.PaneOf()
		if p == nil {
			return nil, fmt.Errorf("the agent %q has no pane", id)
		}
	} else if t, p = TabList(m.Tabs).FindPane(id); p == nil || p.Kind != PaneShell {
		return nil, fmt.Errorf("there is no agent or terminal %q", id)
	}
	if t.ReposBusy {
		return nil, fmt.Errorf("%s: its repositories are being changed; try again in a moment", t.Name)
	}
	if p == t.OwnPane() {
		return nil, fmt.Errorf("%s is the tab's first agent: it stays as long as the tab; close the tab with close_tab", m.AgentLabel(t, t))
	}
	return m.ClosePane(t, p, nil), nil
}

// APIReview starts a code review of agent id's work by kind, with model
// when it is not "" (else the config's), or adds that reviewer to the
// review going on. With no kind the reviewer is the one the config gives.
// The review starts once the worktree is looked at: the reviewer comes as
// an agent, or a notice says why not (nothing changed, say).
func (m *Core) APIReview(id, kind, model string) (Cmd, error) {
	if m.Quitting {
		return nil, ErrAPIQuitting
	}
	of, err := m.apiAgentByID(id)
	if err != nil {
		return nil, err
	}
	if of.ReviewOf != nil {
		return nil, fmt.Errorf("agent %q is a reviewer; review the work of the agent it reviews", id)
	}
	if of.Owner().ReposBusy {
		return nil, fmt.Errorf("%s: its repositories are being changed; try again in a moment", of.Owner().Name)
	}
	k := agents.Kind(strings.ToLower(kind))
	if k == "" {
		k = m.ReviewKind(of)
	}
	if !slices.Contains(m.Available(), k) {
		return nil, fmt.Errorf("there is no agent %q here; those there are: %s", k, m.KindList())
	}
	if model != "" && !agents.TakesModel(k) {
		return nil, fmt.Errorf("nerdr does not know how to give %s a model", k)
	}
	if m.Confined(hooks.Report{}, of) && !m.SandboxedKind(k) {
		return nil, fmt.Errorf("the agent runs in a sandbox, and so must its reviewer; those that can: %s", m.SandboxedKinds())
	}
	if run := of.Review; run != nil {
		if run.Phase != Reviewing {
			return nil, fmt.Errorf("%s is on the findings; another reviewer can join when the work is reviewed again", of.Agent)
		}
		for _, s := range run.Seats {
			if s.R.Agent == k {
				return nil, fmt.Errorf("%s is reviewing already", k)
			}
		}
	}
	return StartReview{Of: m.Ref(of), By: Reviewer{Kind: k, Model: model}}.Do(m), nil
}

// APIClosing is a tab the API is closing: looked at on the loop
// (APIStartClose), then, with Remove, its worktrees looked at away from
// it (Look), and closed on it again (APIClose).
type APIClosing struct {
	T      TabRef
	Name   string
	Remove bool
	wts    []workspace.Worktree
	// heads are the worktrees' heads, by path, as Look found them: what
	// each must still have once the programs have stopped, a commit made
	// since being on no remote, maybe on no branch. With merged, every one
	// is merged with that head on GitHub, and the branches go too.
	heads  map[string]string
	merged bool
}

// APIStartClose begins closing tab id, its worktrees deleted with remove.
// An agent's ID beside the tab's own is refused, as not a tab's.
func (m *Core) APIStartClose(id string, remove bool) (*APIClosing, error) {
	t, err := m.apiTab(id)
	if err != nil {
		return nil, err
	}
	if t.ID != id {
		return nil, fmt.Errorf("%q is an agent of tab %q, not a tab; close the tab by its own ID", id, t.ID)
	}
	return &APIClosing{T: m.Ref(t), Name: t.Name, Remove: remove, wts: slices.Clone(t.Worktrees)}, nil
}

// Look refuses, away from the loop, a close that would delete work the
// user has nowhere else: changes not committed, or commits on no remote
// in a branch not merged. When every branch's pull request is merged with
// the head it has, the branches are deleted with the worktrees, as enter
// does in the TUI's close dialog; else they are kept.
func (c *APIClosing) Look() error {
	if !c.Remove {
		return nil
	}
	// The heads first: the agents run still, and what is checked below is
	// these commits, which the worktrees must still have once they have
	// stopped (see closeTab). A commit made meanwhile keeps the worktree.
	heads := make(map[string]string, len(c.wts))
	for _, w := range c.wts {
		h := workspace.Head(w)
		if h == "" {
			return fmt.Errorf("not closed: git could not say what %s has checked out", filepath.Base(w.Path))
		}
		heads[w.Path] = h
	}
	if afterHeads != nil {
		afterHeads()
	}
	if d := workspace.Dirty(c.wts); len(d) > 0 {
		return fmt.Errorf("not closed: uncommitted changes in %s would be lost; commit them, or close without remove", strings.Join(d, ", "))
	}
	got := mergedHeads(c.wts)
	merged := 0
	var unpushed []string
	for _, w := range c.wts {
		if got[w.Path] != "" && got[w.Path] == heads[w.Path] {
			merged++
			continue
		}
		if workspace.UnpushedAt(w, heads[w.Path]) != 0 {
			unpushed = append(unpushed, filepath.Base(w.Path))
		}
	}
	if len(unpushed) > 0 {
		return fmt.Errorf("not closed: commits not pushed in %s; push them, or close without remove", strings.Join(unpushed, ", "))
	}
	c.heads = heads
	c.merged = len(c.wts) > 0 && merged == len(c.wts)
	return nil
}

// afterHeads, when set, is called by Look once it has the heads: for a
// test to commit then. mergedHeads is mergedBranchHeads, for a test to
// stand in for GitHub.
var (
	afterHeads  func()
	mergedHeads = mergedBranchHeads
)

// APIClose closes the tab c began closing, if it is there still with the
// worktrees it was looked at with. The Cmd stops its programs and deletes
// what is to go, and its event, a TabClosedMsg, says how that went.
func (m *Core) APIClose(c *APIClosing) (Cmd, error) {
	if m.Quitting {
		return nil, ErrAPIQuitting
	}
	t := m.ByRef(c.T)
	if t == nil || m.TabIndex(t) < 0 {
		return nil, fmt.Errorf("the tab %s is gone already", c.Name)
	}
	if t.Owner().ReposBusy {
		return nil, fmt.Errorf("%s: its repositories are being changed; try again in a moment", t.Name)
	}
	if c.Remove && !slices.EqualFunc(t.Worktrees, c.wts, func(a, b workspace.Worktree) bool { return a.Path == b.Path }) {
		return nil, fmt.Errorf("not closed: the repositories of %s changed while it was looked at; ask again", t.Name)
	}
	if c.Remove && c.heads == nil {
		return nil, errors.New("not closed: the worktrees were not looked at")
	}
	return m.closeTab(t, c.Remove, false, c.heads, c.merged), nil
}
