package core

import (
	"errors"
	"fmt"
	"slices"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/github"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// The git and gh the new-tab dialog needs: the plan for a branch's
// worktrees, the issue a branch is for, and the worktrees made. The
// dialog names itself by Job in each, and the answers by it.

func init() { registerRequests(MakePlan{}, LookUpIssue{}, MakeWorktrees{}) }

// MakePlan works out the worktrees of a branch Name in repositories
// Repos, the new branch starting on On if set: told as a PlanMsg. Fork is
// what the client's list of the pull requests knows of a fork's pull
// request of the branch, when it has the list.
type MakePlan struct {
	Job   string
	Name  string
	Repos []workspace.Repo
	On    string
	Fork  *ForkPR
}

// ForkPR is a fork's pull request of a branch, as the client's list knows
// it: Number from Owner's fork, when Is.
type ForkPR struct {
	Is     bool
	Number int
	Owner  string
}

// PlanMsg is the plan MakePlan of Job worked out, or why there is none.
type PlanMsg struct {
	Job  string
	Plan workspace.Plan
	Err  string
}

func (q MakePlan) Do(m *Core) Cmd {
	wtRoot := m.WtRoot
	return Later(func() Event {
		plan, err := makePlan(wtRoot, q)
		msg := PlanMsg{Job: q.Job, Plan: plan}
		if err != nil {
			msg.Err = err.Error()
		}
		return msg
	})
}

func makePlan(wtRoot string, q MakePlan) (workspace.Plan, error) {
	name := q.Name
	if err := workspace.ValidBranch(name); err != nil {
		return workspace.Plan{}, err
	}
	plan, err := workspace.MakePlanOn(wtRoot, name, q.Repos, q.On)
	for i, it := range plan.Items {
		if it.Mode == "remote" || it.Mode == "local" {
			n, state, head, _ := github.BranchPR(it.Repo.Path, name)
			plan.Items[i].SetPR(n, state, head)
		}
		// Not on origin, but a fork's pull request has it: a new branch
		// of the name would start from main, and look like the pull
		// request without being it.
		if it.Mode != "new" || err != nil {
			continue
		}
		var f ForkPR
		if q.Fork != nil {
			f = *q.Fork
		} else {
			f.Number, f.Owner, f.Is = github.ForkPR(it.Repo.Path, name)
		}
		if f.Is {
			return workspace.Plan{}, fmt.Errorf("%s: %s is pull request #%d from %s's fork, not on origin: pick #%d among the pull requests to fetch it",
				it.Repo.Name, name, f.Number, f.Owner, f.Number)
		}
	}
	return plan, err
}

// LookUpIssue reads issue N of the repository at Dir, for the branch and
// the task: told as an IssueMsg.
type LookUpIssue struct {
	Job string
	Dir string
	N   int
}

// IssueMsg is the issue LookUpIssue of Job read, or why it could not.
type IssueMsg struct {
	Job   string
	Issue github.Issue
	Err   string
}

func (q LookUpIssue) Do(m *Core) Cmd {
	return Later(func() Event {
		is, err := github.FetchIssue(q.Dir, q.N)
		msg := IssueMsg{Job: q.Job, Issue: is}
		if err != nil {
			msg.Err = err.Error()
		}
		return msg
	})
}

// MakeWorktrees makes Plan's worktrees, and sets them up as the
// repositories the user allowed say: what the setup does is told as
// SetupProgressMsgs, and the worktrees made as a WorktreesMadeMsg. The
// rest is what the tab is to have, given back with them: its agent Kind,
// Task and Group.
type MakeWorktrees struct {
	Job   string
	Plan  workspace.Plan
	Kind  agents.Kind
	Task  string
	Group string
}

// SetupProgressMsg says what the setup of MakeWorktrees of Job is doing.
type SetupProgressMsg struct {
	Job  string
	Line string
}

// WorktreesMadeMsg is the worktrees MakeWorktrees of Job made (Wts), with
// what went wrong in their setup (Warnings), and the rest of the request
// given back; or why they were not made (Err), Unfetched when the base
// branch could not be fetched.
type WorktreesMadeMsg struct {
	Job       string
	Plan      workspace.Plan
	Kind      agents.Kind
	Task      string
	Group     string
	Wts       []workspace.Worktree
	Warnings  []string
	Err       string
	Unfetched bool
}

// setupProgress is a line of a setup's progress, and the rest to come.
type setupProgress struct {
	msg  SetupProgressMsg
	rest <-chan string
}

func (q MakeWorktrees) Do(m *Core) Cmd {
	progress := make(chan string, 16)
	allowed := SetupAllowedFile()
	plan := q.Plan
	plan.Items = slices.Clone(plan.Items)
	work := Later(func() Event {
		defer close(progress)
		msg := WorktreesMadeMsg{Job: q.Job, Plan: q.Plan, Kind: q.Kind, Task: q.Task, Group: q.Group}
		warnings, err := plan.Sync()
		if err == nil {
			msg.Wts, err = plan.Execute()
		}
		if err == nil {
			warnings = append(warnings, workspace.Prepare(msg.Wts, plan.SetupIn(), allowed, func(s string) {
				select {
				case progress <- s:
				default: // nobody is listening; fine
				}
			})...)
		}
		msg.Warnings = warnings
		if err != nil {
			msg.Err = err.Error()
			var fe *workspace.FetchError
			msg.Unfetched = errors.As(err, &fe)
		}
		return msg
	})
	return Batch(work, listenSetup(q.Job, progress))
}

// listenSetup passes on the progress of a setup until it is done.
func listenSetup(job string, ch <-chan string) Cmd {
	return func() Event {
		s, ok := <-ch
		if !ok {
			return nil
		}
		return setupProgress{msg: SetupProgressMsg{Job: job, Line: s}, rest: ch}
	}
}

// onSetupProgress tells the client a line of a setup's progress, and
// listens for the next.
func (m *Core) onSetupProgress(p setupProgress) Cmd {
	return Batch(m.Tell(p.msg), listenSetup(p.msg.Job, p.rest))
}
