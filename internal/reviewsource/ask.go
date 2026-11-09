package reviewsource

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/nerdr-dev/nerdr/internal/analysis"
	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/github"
	"github.com/nerdr-dev/nerdr/internal/order"
	"github.com/nerdr-dev/nerdr/internal/walkthrough"
	"github.com/nerdr-dev/nerdr/internal/why"
)

// Op is a Source method, as a Query names it.
type Op string

const (
	OpListWorktrees Op = "ListWorktrees"
	OpDetectBase    Op = "DetectBase"
	OpFiles         Op = "Files"
	OpDiff          Op = "Diff"
	OpOldFile       Op = "OldFile"
	OpOldSource     Op = "OldSource"
	OpFullNewFile   Op = "FullNewFile"
	OpReadFile      Op = "ReadFile"
	OpSummary       Op = "Summary"
	OpStamps        Op = "Stamps"
	OpGhAvailable   Op = "GhAvailable"
	OpCurrentPR     Op = "CurrentPR"
	OpFailedRunLogs Op = "FailedRunLogs"

	OpNotes           Op = "Notes"
	OpOrder           Op = "Order"
	OpWalkthrough     Op = "Walkthrough"
	OpWalkthroughPage Op = "WalkthroughPage"
	OpLoadProject     Op = "LoadProject"
	OpImports         Op = "Imports"
	OpRelations       Op = "Relations"
)

// Query is a call of a Source method, Op, with its arguments, as it goes
// to where the repositories are. Each method uses the fields its own
// arguments name.
type Query struct {
	Op        Op
	Repo      string         `json:",omitempty"`
	Wt        string         `json:",omitempty"`
	Branch    string         `json:",omitempty"`
	C         git.Comparison `json:",omitzero"`
	File      string         `json:",omitempty"`
	Original  string         `json:",omitempty"`
	Diff      string         `json:",omitempty"`
	Untracked bool           `json:",omitempty"`
	Paths     []string       `json:",omitempty"`
	Checks    []github.Check `json:",omitempty"`
	Limit     int            `json:",omitempty"`
}

// Answer is what a Query's call gave. Each method sets the fields its own
// results name; Err is its error, "" for none.
type Answer struct {
	Text      string                   `json:",omitempty"`
	Worktrees []git.Worktree           `json:",omitempty"`
	Files     []git.FileStatus         `json:",omitempty"`
	Stats     map[string]git.LineCount `json:",omitempty"`
	Stamps    map[string]string        `json:",omitempty"`
	N         [3]int                   `json:",omitzero"` // Summary's files, add, del
	OK        bool                     `json:",omitempty"`
	PR        github.PR                `json:",omitzero"`
	Msg       string                   `json:",omitempty"`
	Err       string                   `json:",omitempty"`

	Notes   why.Index               `json:",omitzero"`
	Order   order.Index             `json:",omitzero"`
	Walk    walkthrough.Walkthrough `json:",omitzero"`
	Older   bool                    `json:",omitempty"`
	Version int                     `json:",omitempty"`
	Imports map[string][]string     `json:",omitempty"`
	Symbols []analysis.Symbol       `json:",omitempty"`
	Refs    [][]analysis.Ref        `json:",omitempty"`
}

// Run makes q's call of s.
func (q Query) Run(s Source) Answer {
	var a Answer
	var err error
	switch q.Op {
	case OpListWorktrees:
		a.Worktrees, err = s.ListWorktrees(q.Repo)
	case OpDetectBase:
		a.Text = s.DetectBase(q.Wt)
	case OpFiles:
		a.Files, a.Stats, err = s.Files(q.C)
	case OpDiff:
		a.Text, err = s.Diff(q.C, q.File, q.Untracked, q.Original)
	case OpOldFile:
		a.Text, err = s.OldFile(q.C, q.File)
	case OpOldSource:
		a.Text, err = s.OldSource(q.C, q.File)
	case OpFullNewFile:
		a.Text, err = s.FullNewFile(q.Wt, q.File, q.Diff)
	case OpReadFile:
		a.Text, err = s.ReadFile(q.Wt, q.File)
	case OpSummary:
		a.N[0], a.N[1], a.N[2] = s.Summary(q.Wt)
	case OpStamps:
		a.Stamps = s.Stamps(q.Wt, q.Paths)
	case OpGhAvailable:
		a.OK = s.GhAvailable()
	case OpCurrentPR:
		a.PR, a.OK, a.Msg = s.CurrentPR(q.Wt)
	case OpFailedRunLogs:
		a.Text, err = s.FailedRunLogs(q.Wt, q.Checks, q.Limit)
	case OpNotes:
		a.Notes = s.Notes(q.Wt)
	case OpOrder:
		a.Order = s.Order(q.Wt, q.Branch)
	case OpWalkthrough:
		a.Walk, a.OK, a.Older = s.Walkthrough(q.Wt, q.Branch, q.Paths)
	case OpWalkthroughPage:
		var page []byte
		page, a.OK, err = s.WalkthroughPage(q.Wt, q.Branch)
		a.Text = string(page)
	case OpLoadProject:
		a.Version = s.LoadProject(q.Wt)
	case OpImports:
		a.Imports = s.Imports(q.Wt, q.Paths)
	case OpRelations:
		a.Symbols, a.Refs = s.Relations(q.Wt, q.File)
	default:
		err = fmt.Errorf("no such read: %q", q.Op)
	}
	if err != nil {
		a.Err = err.Error()
	}
	return a
}

func (a Answer) err() error {
	if a.Err == "" {
		return nil
	}
	return errors.New(a.Err)
}

// AskWait is how long an Asker waits for an answer: gh reading a failed
// build's logs can take a while.
var AskWait = 2 * time.Minute

// Asker is the Source that asks somewhere else: send sends a Query, named
// by a job of the Asker's, and the answer is handed to Answer by that job.
// A call waits for its answer, at most AskWait, so it is made off the
// screen's time, as the panel makes them.
type Asker struct {
	send func(job string, q Query) bool

	mu   sync.Mutex
	next int
	jobs map[string]chan Answer
}

// NewAsker is an Asker sending its queries by send, which reports false
// when they cannot go.
func NewAsker(send func(job string, q Query) bool) *Asker {
	return &Asker{send: send, jobs: map[string]chan Answer{}}
}

// Answer hands the answer to job its caller, if it still waits. It never
// waits itself.
func (k *Asker) Answer(job string, a Answer) {
	k.mu.Lock()
	ch := k.jobs[job]
	delete(k.jobs, job)
	k.mu.Unlock()
	if ch != nil {
		ch <- a // buffered, and handed one answer only
	}
}

// FailAll answers every job still waiting with why: what was asked will
// not be answered, as when core has gone.
func (k *Asker) FailAll(why string) {
	k.mu.Lock()
	jobs := k.jobs
	k.jobs = map[string]chan Answer{}
	k.mu.Unlock()
	for _, ch := range jobs {
		ch <- Answer{Err: why}
	}
}

// ask sends q and waits for its answer.
func (k *Asker) ask(q Query) Answer {
	ch := make(chan Answer, 1)
	k.mu.Lock()
	k.next++
	job := "review-" + strconv.Itoa(k.next)
	k.jobs[job] = ch
	k.mu.Unlock()
	drop := func() {
		k.mu.Lock()
		delete(k.jobs, job)
		k.mu.Unlock()
	}
	if !k.send(job, q) {
		drop()
		return Answer{Err: "nerdr's core cannot be reached"}
	}
	t := time.NewTimer(AskWait)
	defer t.Stop()
	select {
	case a := <-ch:
		return a
	case <-t.C:
		drop()
		return Answer{Err: fmt.Sprintf("nerdr's core did not answer %s in %v", q.Op, AskWait)}
	}
}

func (k *Asker) ListWorktrees(repo string) ([]git.Worktree, error) {
	a := k.ask(Query{Op: OpListWorktrees, Repo: repo})
	return a.Worktrees, a.err()
}

func (k *Asker) DetectBase(wt string) string {
	return k.ask(Query{Op: OpDetectBase, Wt: wt}).Text
}

func (k *Asker) Files(c git.Comparison) ([]git.FileStatus, map[string]git.LineCount, error) {
	a := k.ask(Query{Op: OpFiles, C: c})
	return a.Files, a.Stats, a.err()
}

func (k *Asker) Diff(c git.Comparison, file string, untracked bool, original string) (string, error) {
	a := k.ask(Query{Op: OpDiff, C: c, File: file, Untracked: untracked, Original: original})
	return a.Text, a.err()
}

func (k *Asker) OldFile(c git.Comparison, file string) (string, error) {
	a := k.ask(Query{Op: OpOldFile, C: c, File: file})
	return a.Text, a.err()
}

func (k *Asker) OldSource(c git.Comparison, file string) (string, error) {
	a := k.ask(Query{Op: OpOldSource, C: c, File: file})
	return a.Text, a.err()
}

func (k *Asker) FullNewFile(wt, file, diff string) (string, error) {
	a := k.ask(Query{Op: OpFullNewFile, Wt: wt, File: file, Diff: diff})
	return a.Text, a.err()
}

func (k *Asker) ReadFile(wt, file string) (string, error) {
	a := k.ask(Query{Op: OpReadFile, Wt: wt, File: file})
	return a.Text, a.err()
}

func (k *Asker) Summary(wt string) (files, add, del int) {
	a := k.ask(Query{Op: OpSummary, Wt: wt})
	return a.N[0], a.N[1], a.N[2]
}

func (k *Asker) Stamps(wt string, paths []string) map[string]string {
	a := k.ask(Query{Op: OpStamps, Wt: wt, Paths: paths})
	if a.Stamps == nil {
		return map[string]string{}
	}
	return a.Stamps
}

// GhAvailable reports whether gh is installed where the repositories are.
// Unanswered, it reports true: the PR's reading then says what went wrong.
func (k *Asker) GhAvailable() bool {
	a := k.ask(Query{Op: OpGhAvailable})
	return a.OK || a.Err != ""
}

func (k *Asker) CurrentPR(wt string) (github.PR, bool, string) {
	a := k.ask(Query{Op: OpCurrentPR, Wt: wt})
	if a.Err != "" {
		return github.PR{}, false, a.Err
	}
	return a.PR, a.OK, a.Msg
}

func (k *Asker) FailedRunLogs(wt string, checks []github.Check, limit int) (string, error) {
	a := k.ask(Query{Op: OpFailedRunLogs, Wt: wt, Checks: checks, Limit: limit})
	return a.Text, a.err()
}

func (k *Asker) Notes(wt string) why.Index {
	return k.ask(Query{Op: OpNotes, Wt: wt}).Notes
}

func (k *Asker) Order(wt, branch string) order.Index {
	return k.ask(Query{Op: OpOrder, Wt: wt, Branch: branch}).Order
}

func (k *Asker) Walkthrough(wt, branch string, files []string) (walkthrough.Walkthrough, bool, bool) {
	a := k.ask(Query{Op: OpWalkthrough, Wt: wt, Branch: branch, Paths: files})
	return a.Walk, a.OK, a.Older
}

func (k *Asker) WalkthroughPage(wt, branch string) ([]byte, bool, error) {
	a := k.ask(Query{Op: OpWalkthroughPage, Wt: wt, Branch: branch})
	var page []byte
	if a.Text != "" {
		page = []byte(a.Text)
	}
	return page, a.OK || a.Err != "", a.err()
}

func (k *Asker) LoadProject(wt string) int {
	return k.ask(Query{Op: OpLoadProject, Wt: wt}).Version
}

func (k *Asker) Imports(wt string, paths []string) map[string][]string {
	return k.ask(Query{Op: OpImports, Wt: wt, Paths: paths}).Imports
}

func (k *Asker) Relations(wt, file string) ([]analysis.Symbol, [][]analysis.Ref) {
	a := k.ask(Query{Op: OpRelations, Wt: wt, File: file})
	return a.Symbols, a.Refs
}
