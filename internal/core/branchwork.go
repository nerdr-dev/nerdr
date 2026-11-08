package core

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/github"
	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// The work F2 does on a tab's branches: committing every change, pushing,
// and opening the pull request.

func init() { registerRequests(LookChanges{}, BranchWork{}) }

// LookChanges looks up the changes in tab T's worktrees, for the commit
// dialog of Job: told as a ChangesMsg.
type LookChanges struct {
	Job string
	T   TabRef
}

// ChangesMsg is the changes LookChanges of Job found: each worktree's, by
// its path, as git status lists them. WorktreeHooks is whether any of the
// worktrees has a core.hooksPath pointing inside it; Err what could not be
// looked up.
type ChangesMsg struct {
	Job           string
	Files         map[string][]string
	WorktreeHooks bool
	Err           string
}

func (q LookChanges) Do(m *Core) Cmd {
	t := m.ByRef(q.T)
	if t == nil {
		return nil
	}
	wts := slices.Clone(t.Worktrees)
	return Later(func() Event {
		msg := ChangesMsg{Job: q.Job, Files: map[string][]string{}}
		var errs []string
		for _, w := range wts {
			if gitsafe.WorktreeHooks(w.Path) {
				msg.WorktreeHooks = true
			}
			fs, err := workspace.Changes(w)
			if err != nil {
				errs = append(errs, filepath.Base(w.Repo)+": "+err.Error())
				continue
			}
			msg.Files[w.Path] = fs
		}
		msg.Err = strings.Join(errs, "; ")
		return msg
	})
}

// BranchWork is work on tab T's branches: with Message, a commit of every
// change in the worktrees at CommitIn, by their paths; then, with Push, a
// push of every branch, or with PR a push and a pull request for each
// branch without an open one. Worktree git hooks are left out once a
// sandboxed agent has run in the tab. How it went is told as a
// BranchDoneMsg.
type BranchWork struct {
	T        TabRef
	Message  string
	CommitIn []string
	Push, PR bool
}

// BranchDoneMsg is how BranchWork on tab T went: Err when it failed, else
// Note.
type BranchDoneMsg struct {
	T         TabRef
	Note, Err string
}

func (q BranchWork) Do(m *Core) Cmd {
	t := m.ByRef(q.T)
	if t == nil {
		return nil
	}
	m.MarkSandboxed(t)
	name, wts, hooks := t.Name, slices.Clone(t.Worktrees), !t.HasSandboxed()
	var commit []workspace.Worktree
	for _, w := range wts {
		if slices.Contains(q.CommitIn, w.Path) {
			commit = append(commit, w)
		}
	}
	have := map[string]bool{}
	for _, w := range t.Info.Wts {
		if w.HasPR && w.Pr.State == "OPEN" {
			have[w.Repo] = true
		}
	}
	return Later(func() Event {
		var then func() (string, string)
		switch {
		case q.PR:
			then = func() (string, string) { return CreatePRs(name, wts, have, hooks) }
		case q.Push:
			then = func() (string, string) { return PushAll(name, wts, hooks) }
		}
		if q.Message == "" {
			if then == nil {
				return BranchDoneMsg{T: q.T}
			}
			note, err := then()
			return BranchDoneMsg{T: q.T, Note: note, Err: err}
		}
		note, err := CommitAll(name, commit, q.Message, hooks)
		if err != "" || then == nil {
			return BranchDoneMsg{T: q.T, Note: note, Err: err}
		}
		after, aerr := then()
		after = strings.TrimSpace(note + "; " + after)
		if aerr != "" {
			aerr = note + ", but " + aerr
		}
		return BranchDoneMsg{T: q.T, Note: after, Err: aerr}
	})
}

// CommitAll commits every change in wts, of the tab called name, with
// message; worktree git hooks only with hooks. It gives what to tell of it,
// or why it failed.
func CommitAll(name string, wts []workspace.Worktree, message string, hooks bool) (note, err string) {
	var errs, done []string
	skipped := false
	for _, w := range wts {
		repo := filepath.Base(w.Repo)
		if !hooks && gitsafe.WorktreeHooks(w.Path) {
			skipped = true
		}
		if err := workspace.CommitIn(w, message, hooks); err != nil {
			errs = append(errs, repo+": "+err.Error())
			continue
		}
		done = append(done, repo)
	}
	if len(errs) > 0 {
		return "", "commit failed: " + strings.Join(errs, "; ")
	}
	note = "committed " + name
	if len(wts) > 1 {
		note += " (" + strings.Join(done, ", ") + ")"
	}
	if skipped {
		note += " (worktree git hooks skipped for sandboxed tab)"
	}
	return note, ""
}

// PushAll pushes the branches of wts, of the tab called name.
func PushAll(name string, wts []workspace.Worktree, hooks bool) (note, err string) {
	var errs []string
	skipped := false
	for _, w := range wts {
		if !hooks && gitsafe.WorktreeHooks(w.Path) {
			skipped = true
		}
		if err := workspace.PushIn(w, hooks); err != nil {
			errs = append(errs, filepath.Base(w.Repo)+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return "", "push failed: " + strings.Join(errs, "; ")
	}
	note = "pushed " + name
	if skipped {
		note += " (worktree git hooks skipped for sandboxed tab)"
	}
	return note, ""
}

// CreatePRs pushes the branches of wts, of the tab called name, and opens
// a pull request for each but those of the repositories in have.
func CreatePRs(name string, wts []workspace.Worktree, have map[string]bool, hooks bool) (note, err string) {
	var urls, errs []string
	skipped := false
	for _, w := range wts {
		if have[w.Repo] {
			continue
		}
		repo := filepath.Base(w.Repo)
		// A pull request is made of commits: work not yet committed is in
		// none, and gh's refusal would not say so.
		if s, err := workspace.BranchStats(w); err == nil && s.Ahead == 0 {
			why := repo + ": no commits ahead of " + s.Base + " to open a pull request with"
			if n := workspace.Uncommitted(w); n > 0 {
				why += " — " + Plural(n, "file") + " changed but not committed: commit them first"
			}
			errs = append(errs, why)
			continue
		}
		if !hooks && gitsafe.WorktreeHooks(w.Path) {
			skipped = true
		}
		if err := workspace.PushIn(w, hooks); err != nil {
			errs = append(errs, repo+": push: "+err.Error())
			continue
		}
		url, err := github.CreatePR(w.Path, w.Origin)
		if err != nil {
			errs = append(errs, repo+": "+err.Error())
			continue
		}
		urls = append(urls, url)
	}
	switch {
	case len(errs) > 0:
		return "", strings.Join(errs, "; ")
	case len(urls) == 0:
		return name + " already has its pull request", ""
	}
	note = "opened " + strings.Join(urls, ", ")
	if skipped {
		note += " (worktree git hooks skipped for sandboxed tab)"
	}
	return note, ""
}
