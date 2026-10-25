// Package spinoff is how an agent hands parts of its work to new tabs.
//
// When the user asks an agent that has done a lot in one branch to split
// it up, the agent runs `nerdr spinoff` with a plan: for each part, a
// branch name, a summary for the user, a prompt for the new agent with
// everything it needs to know, and the commits in the agent's own branch
// that belong to the part. nerdr keeps the plan on the agent's tab and asks
// the user about one part at a time, in the tab's new-tab dialog; when the
// last part has been made or skipped, the agent is told what became of
// each.
package spinoff

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/clean"
)

// Part is one piece of work for a tab of its own.
type Part struct {
	// Branch is the name the agent proposes; the user may change it.
	Branch string `json:"branch"`
	// Summary is what the user reads in the dialog: a few lines at most.
	Summary string `json:"summary"`
	// Prompt is what the new agent is given: all the agent knows that the
	// new one needs.
	Prompt string `json:"prompt"`
	// Commits are the commits in the agent's branch with this part's
	// work, which the new branch can start with.
	Commits []string `json:"commits,omitempty"`
	// Repos are, in a tab with several repositories, the folders of those
	// the part is for, as the agent sees them in its own folder; the user
	// may pick others. None is for nerdr to work out.
	Repos []string `json:"repos,omitempty"`
	// From is the id of the agent that sent the part, and Sandboxed is set
	// when it ran in a sandbox: its tab then runs only an agent in one,
	// and what became of it is told to that agent alone. nerdr sets both,
	// whatever the agent sent.
	From      string `json:"from,omitempty"`
	Sandboxed bool   `json:"sandboxed,omitempty"`
}

// Plan is what `nerdr spinoff` reads.
type Plan struct {
	Parts []Part `json:"parts"`
}

// MaxSummary is the longest summary taken, in bytes: it is shown in a
// dialog, and the prompt is where the rest goes.
const MaxSummary = 600

// MaxPrompt is the longest prompt taken, in bytes: it is typed into the
// new agent as a message.
const MaxPrompt = 128 << 10

// MaxParts is the most parts a plan may have: each is a dialog for the
// user to go through.
const MaxParts = 20

// Parse reads a plan and checks every part, saying what is wrong with
// which one, so the agent can fix it while it still can. valid checks a
// branch name.
func Parse(data []byte, valid func(string) error) (Plan, error) {
	var p Plan
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, fmt.Errorf("not a plan: %w", err)
	}
	return p, Check(p.Parts, valid)
}

// Check checks the parts of a plan, and makes their text safe to show
// (see package clean). It is run on what `nerdr spinoff` reads, and again
// on what reaches nerdr's socket, which an agent can write to without it.
func Check(parts []Part, valid func(string) error) error {
	switch {
	case len(parts) == 0:
		return errors.New("the plan has no parts")
	case len(parts) > MaxParts:
		return fmt.Errorf("the plan has %d parts; at most %d", len(parts), MaxParts)
	}
	seen := map[string]bool{}
	var errs []string
	for i := range parts {
		pt := &parts[i]
		pt.Branch = strings.TrimSpace(pt.Branch)
		pt.Summary = strings.TrimSpace(clean.Text(pt.Summary))
		pt.Prompt = strings.TrimSpace(clean.Text(pt.Prompt))
		name := fmt.Sprintf("part %d", i+1)
		if pt.Branch != "" {
			name += " (" + clean.Line(pt.Branch) + ")"
		}
		switch err := valid(pt.Branch); {
		case err != nil:
			errs = append(errs, name+": branch: "+err.Error())
		case seen[pt.Branch]:
			errs = append(errs, name+": the same branch as an earlier part")
		}
		seen[pt.Branch] = true
		if pt.Summary == "" {
			errs = append(errs, name+": summary is empty")
		} else if len(pt.Summary) > MaxSummary {
			errs = append(errs, fmt.Sprintf("%s: summary is %d bytes; keep it under %d and put the rest in the prompt", name, len(pt.Summary), MaxSummary))
		}
		if pt.Prompt == "" {
			errs = append(errs, name+": prompt is empty")
		} else if len(pt.Prompt) > MaxPrompt {
			errs = append(errs, fmt.Sprintf("%s: prompt is %d KB; keep it under %d KB — the new agent can read files and git for the details", name, len(pt.Prompt)/1024, MaxPrompt/1024))
		}
		for _, r := range pt.Repos {
			if r = strings.TrimSpace(r); r == "" || r == "." || r == ".." || strings.ContainsAny(r, "/\\\n") {
				errs = append(errs, fmt.Sprintf("%s: %q is not a repository's folder: give its name alone", name, r))
			}
		}
		for _, c := range pt.Commits {
			// A commit is put on git's command line: one starting with -
			// would be read as an option.
			if strings.TrimSpace(c) == "" || strings.ContainsAny(c, " \t\n") || strings.HasPrefix(c, "-") {
				errs = append(errs, fmt.Sprintf("%s: %q is not a commit", name, c))
			}
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "\n"))
	}
	return nil
}

// FullCommit reports whether c is a full commit id, as `nerdr spinoff`
// sends them once it has resolved what the agent wrote.
func FullCommit(c string) bool {
	if len(c) != 40 && len(c) != 64 {
		return false
	}
	return strings.Trim(c, "0123456789abcdef") == ""
}

// Pending is a plan being worked through on the tab that sent it: the
// parts still to ask about, and what became of the ones asked about.
type Pending struct {
	Next []Part    `json:"next"`
	Done []Outcome `json:"done,omitempty"`
}

// Outcome is what became of one part.
type Outcome struct {
	Part Part `json:"part"`
	// Skipped is set when the user chose not to make a tab for it.
	Skipped bool `json:"skipped,omitempty"`
	// Branch and Dir are where the tab was made: the branch as the user
	// named it, and its worktree.
	Branch string `json:"branch,omitempty"`
	Dir    string `json:"dir,omitempty"`
	// Agent is the agent the user chose to run in it.
	Agent string `json:"agent,omitempty"`
	// Reused is set when the part's commits were to start the branch, and
	// PickFailed when that was tried and did not apply cleanly.
	Reused     bool   `json:"reused,omitempty"`
	PickFailed string `json:"pick_failed,omitempty"`
	// Err is why the tab could not be made.
	Err string `json:"err,omitempty"`
	// Tab is the id of the tab made, and Start what became of its agent
	// and the part's prompt by the time the agent that sent the part is
	// told: one of the Start values.
	Tab   string `json:"tab,omitempty"`
	Start string `json:"start,omitempty"`
}

// What became of a made tab's agent and its prompt: see Outcome.Start.
// The agent that sent the parts is told once each is one of these, so
// that it does not look for itself while the new agents are starting and
// take one that has not got going yet for one that never got its prompt.
const (
	StartBegun   = "begun"   // seen taking the prompt up
	StartStalled = "stalled" // the prompt typed in, and not taken up
	StartExited  = "exited"  // the agent stopped before it took it up
	StartClosed  = "closed"  // the user closed the tab first
	StartSlow    = "slow"    // not seen taking it up in the time waited
)

// Count is the place of the part being asked about, and how many there
// are in all, for "2 of 5".
func (p *Pending) Count() (int, int) {
	return len(p.Done) + 1, len(p.Done) + len(p.Next)
}

// Parent is the tab a plan came from, as the new agent is told of it.
type Parent struct {
	Name   string
	Branch string
	Dir    string
}

// Prompt is what the new tab's agent is given: the part's prompt, and
// where the work came from and what is already on its branch.
func Prompt(pt Part, from Parent, o Outcome) string {
	var b strings.Builder
	b.WriteString(pt.Prompt)
	b.WriteString("\n\n---\n")
	fmt.Fprintf(&b, "This tab was split off from the tab %q by the agent there", from.Name)
	if from.Branch != "" {
		fmt.Fprintf(&b, ", on branch %s", from.Branch)
	}
	fmt.Fprintf(&b, ", in %s. Its worktree shares the repository with this one, so its commits can be looked at here with git show.", from.Dir)
	if len(pt.Commits) > 0 {
		list := strings.Join(pt.Commits, " ")
		switch {
		case o.PickFailed != "":
			fmt.Fprintf(&b, "\n\nThe work was started there, in commits %s. nerdr tried to bring them onto this branch, but they did not apply cleanly (%s), so this branch starts from the base without them. Bring them in yourself (git cherry-pick, resolving the conflicts), or write the change again with them as a guide.", list, o.PickFailed)
		case o.Reused:
			fmt.Fprintf(&b, "\n\nThe work was started there, and commits %s from that branch are already on this one: carry on from them.", list)
		default:
			fmt.Fprintf(&b, "\n\nThe work was started there, in commits %s, but the user chose to have it written anew: this branch starts from the base without them. Use them as a guide, not as code to copy.", list)
		}
	}
	return b.String()
}

// Report is what the agent that sent the plan is told once every part has
// been made or skipped, and each made tab's agent has started (see
// Outcome.Start). branch is its own branch.
func Report(done []Outcome, branch string) string {
	var b strings.Builder
	b.WriteString("nerdr: the user has gone through the parts you split off.\n")
	moved := false
	for _, o := range done {
		name := o.Part.Branch
		switch {
		case o.Skipped:
			fmt.Fprintf(&b, "\n- %s: skipped by the user; no tab was made, so it is still yours.", name)
		case o.Err != "":
			fmt.Fprintf(&b, "\n- %s: could not be made (%s); it is still yours.", name, o.Err)
		default:
			fmt.Fprintf(&b, "\n- %s: made as a new tab on branch %s, in %s", name, o.Branch, o.Dir)
			if o.Agent != "" {
				fmt.Fprintf(&b, ", with %s", o.Agent)
			}
			b.WriteString(".")
			if o.Branch != name {
				b.WriteString(" The user renamed the branch.")
			}
			if len(o.Part.Commits) > 0 {
				moved = true
				list := strings.Join(o.Part.Commits, " ")
				switch {
				case o.PickFailed != "":
					fmt.Fprintf(&b, " Commits %s did not apply cleanly there, so its agent brings them in or writes them again.", list)
				case o.Reused:
					fmt.Fprintf(&b, " It starts with commits %s.", list)
				default:
					fmt.Fprintf(&b, " Commits %s are being written anew there.", list)
				}
			}
			switch o.Start {
			case StartBegun:
				b.WriteString(" Its agent has started on it: leave this part to it.")
			case StartStalled:
				b.WriteString(" Its agent did not take the prompt up; the user has been told and sees to it: leave this part alone.")
			case StartExited:
				b.WriteString(" Its agent stopped before it started on it; the user has been told: leave this part alone.")
			case StartClosed:
				b.WriteString(" The user has closed that tab since; its branch and worktree are still there. Ask the user before taking this part up again.")
			case StartSlow:
				b.WriteString(" Its agent has its prompt but has not been seen starting on it yet; it may still be starting: leave this part to it.")
			default:
				b.WriteString(" Its agent has been given the prompt: leave this part to it.")
			}
		}
	}
	if moved {
		on := "this branch"
		if branch != "" {
			on = "this branch (" + branch + ")"
		}
		fmt.Fprintf(&b, "\n\nThe commits listed above are still on %s. They belong to the new branches now: keep them out of any pull request from here, but do not remove them unless the user asks.", on)
	}
	return b.String()
}
