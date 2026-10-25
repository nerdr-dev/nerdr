// Package walkthrough keeps the guided walkthrough an agent wrote of the
// change it made, and turns it into a page to read it in.
//
// The reading order (package order) says where to start and the notes
// (package why) say why a line is the way it is, but neither tells the
// story: what the change sets out to do, in which steps, and how they hang
// together. A reviewer reading file by file has to piece that together
// alone. The agent that made the change knows it, so when the user asks
// it writes a walkthrough: five to ten steps, grouped by what they do
// rather than by file, each pointing at the code it is about, with the
// risks it sees at the end.
//
// The agent writes only the structure. nerdr draws the page — the flow of
// the steps, and for each the hunks of the diff it points at — from the
// change as it is when the page is opened, so the code shown is never the
// agent's copy of it.
//
// A walkthrough is one record, appended as one line as described in
// internal/store; the last one written for the branch is the one in force.
package walkthrough

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"nerdr/internal/clean"
	"nerdr/internal/git"
	"nerdr/internal/store"
)

// FileName is the walkthroughs' file, in the folder internal/store keeps
// records in.
const FileName = "walkthrough.jsonl"

// Walkthrough is the story of one change, in steps.
type Walkthrough struct {
	// Title names the change, in a few words.
	Title string `json:"title"`
	// Summary is what the change does and why, in a paragraph or two.
	Summary string `json:"summary"`
	// Steps are the order to go through the change in: where it starts
	// first. The main path is the steps that are not Side, in this order.
	Steps []Step `json:"steps"`
	// Risks are what the agent thinks a reviewer should weigh. Optional.
	Risks []Risk `json:"risks,omitempty"`
	// Branch is the branch it was written on; see order.Entry.Branch.
	Branch string `json:"branch,omitempty"`
	// Agent names who wrote it. Optional.
	Agent string `json:"agent,omitempty"`
	// Time is when it was written, as an unparsed string. Optional.
	Time string `json:"ts,omitempty"`
}

// Step is one part of the change, as a reviewer should take it in.
type Step struct {
	// ID names the step for From: short, lower case, unique.
	ID string `json:"id"`
	// Title is the step in a few words, as it is shown on its node.
	Title string `json:"title"`
	// Text is what happens in this step and why, in plain paragraphs.
	Text string `json:"text"`
	// From lists the steps this one follows from: an arrow is drawn from
	// each. Empty for a main step means the main step before it.
	From []string `json:"from,omitempty"`
	// Side marks a step off the main path: a related change, drawn beside
	// the step it follows from, that the story can be read without.
	Side bool `json:"side,omitempty"`
	// Code is the code the step is about.
	Code []Ref `json:"code,omitempty"`
}

// Ref points at code: a file of the change, or a line in it.
type Ref struct {
	// File is relative to the worktree root.
	File string `json:"file"`
	// Anchor is a fragment of one line, found as why.Note.Anchor is: the
	// hunk holding it is shown, or the code around it when it is code the
	// change did not touch. Empty shows every hunk of the file.
	Anchor string `json:"anchor,omitempty"`
	// Note is said above the code. Optional.
	Note string `json:"note,omitempty"`
}

// Risk is something the reviewer should weigh before saying yes.
type Risk struct {
	// Level is high, medium or low.
	Level string `json:"level"`
	Text  string `json:"text"`
	// File and Anchor point at the code it is about, as in Ref. Optional.
	File   string `json:"file,omitempty"`
	Anchor string `json:"anchor,omitempty"`
}

// Levels are a risk's levels, the most serious first.
var Levels = []string{"high", "medium", "low"}

// MaxSteps is the most steps a walkthrough takes: more is no longer a
// walkthrough but the diff again, a hunk at a time.
const MaxSteps = 15

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// File is where the walkthroughs for the worktree at path are appended
// to, or "" when path is not inside a git repository.
func File(path string) string { return store.Path(path, FileName) }

// Load reads the walkthrough in force for branch in the worktree's
// repository, and whether there is one. As with order.Load, one written
// on another branch, or before the branch of that name was made, is not
// this change's.
func Load(worktreePath, branch string) (Walkthrough, bool) {
	created, _ := store.BranchCreated(worktreePath, branch)
	return load(File(worktreePath), branch, created)
}

func load(path, branch string, created time.Time) (Walkthrough, bool) {
	var out Walkthrough
	found := false
	store.ReadLines(path, func(line []byte) {
		var w Walkthrough
		if json.Unmarshal(line, &w) != nil {
			return
		}
		if w.Branch != branch || before(w, created) || len(w.Steps) == 0 {
			return
		}
		out, found = tidy(w), true
	})
	return out, found
}

// tidy makes what an agent wrote safe to show (see package clean), as the
// record is read: the file is written by hand too.
func tidy(w Walkthrough) Walkthrough {
	w.Title, w.Summary = clean.Line(w.Title), clean.Text(w.Summary)
	w.Agent, w.Time = clean.Line(w.Agent), clean.Line(w.Time)
	for i := range w.Steps {
		s := &w.Steps[i]
		s.ID, s.Title, s.Text = clean.Line(s.ID), clean.Line(s.Title), clean.Text(s.Text)
		for j := range s.Code {
			s.Code[j] = tidyRef(s.Code[j])
		}
	}
	for i := range w.Risks {
		r := &w.Risks[i]
		r.Level, r.Text = clean.Line(r.Level), clean.Text(r.Text)
		ref := tidyRef(Ref{File: r.File, Anchor: r.Anchor})
		r.File, r.Anchor = ref.File, ref.Anchor
	}
	return w
}

func tidyRef(r Ref) Ref {
	r.File = filepath.ToSlash(strings.TrimPrefix(clean.Line(r.File), "./"))
	if f, ok := store.Local(r.File); ok {
		r.File = f
	} else {
		r.File = ""
	}
	r.Anchor = strings.TrimSpace(clean.Line(r.Anchor))
	r.Note = clean.Text(r.Note)
	return r
}

const timeLayout = "2006-01-02 15:04"

// before is order's: whether w was written before the branch was made.
func before(w Walkthrough, created time.Time) bool {
	if created.IsZero() {
		return false
	}
	t, err := time.ParseInLocation(timeLayout, w.Time, time.Local)
	return err == nil && t.Before(created.Truncate(time.Minute))
}

// Add checks a walkthrough against the change and appends it. dir is
// where the writer stands, anywhere inside the worktree; files may be
// named relative to it, to the worktree root, or absolutely.
//
// As with why.Add, what would otherwise only show later as a step with
// nothing under it is refused while the agent can still fix it: a file
// that is not there, an anchor that is on no line or on several, a step
// that follows from one that is not there.
func Add(dir string, w Walkthrough) (Walkthrough, error) {
	root, err := store.Root(dir)
	if err != nil {
		return w, err
	}
	path := File(root)
	if path == "" {
		return w, fmt.Errorf("no data folder for %s", root)
	}
	if err := check(root, dir, &w); err != nil {
		return w, err
	}
	if w.Branch == "" {
		w.Branch = store.Branch(root)
	}
	if w.Time == "" {
		w.Time = time.Now().Format(timeLayout)
	}
	if err := store.ExcludeData(root); err != nil {
		return w, err
	}
	return w, store.Append(path, w)
}

// check refuses what would not show, and makes every file name relative
// to the worktree root.
func check(root, dir string, w *Walkthrough) error {
	w.Title, w.Summary = strings.TrimSpace(w.Title), strings.TrimSpace(w.Summary)
	switch {
	case w.Title == "":
		return fmt.Errorf(`"title" is required`)
	case strings.ContainsAny(w.Title, "\r\n"):
		return fmt.Errorf(`"title" must be one line`)
	case w.Summary == "":
		return fmt.Errorf(`"summary" is required`)
	case len(w.Steps) == 0:
		return fmt.Errorf(`"steps" is empty — a walkthrough needs at least one`)
	case len(w.Steps) > MaxSteps:
		return fmt.Errorf("%d steps — keep it to %d at most; put what is the same thing done again in one step", len(w.Steps), MaxSteps)
	}
	cmp := git.Comparison{Worktree: root, Base: git.DetectBase(root)}
	seen := map[string]bool{}
	for i := range w.Steps {
		s := &w.Steps[i]
		s.ID, s.Title, s.Text = strings.TrimSpace(s.ID), strings.TrimSpace(s.Title), strings.TrimSpace(s.Text)
		at := fmt.Sprintf("step %d", i+1)
		switch {
		case !idRe.MatchString(s.ID):
			return fmt.Errorf(`%s: "id" %q must be lower case letters, digits and -`, at, s.ID)
		case seen[s.ID]:
			return fmt.Errorf(`%s: "id" %q is used by an earlier step`, at, s.ID)
		case s.Title == "" || strings.ContainsAny(s.Title, "\r\n"):
			return fmt.Errorf(`%s (%s): "title" is required, on one line`, at, s.ID)
		case s.Text == "":
			return fmt.Errorf(`%s (%s): "text" is required`, at, s.ID)
		}
		for _, f := range s.From {
			if !seen[f] {
				return fmt.Errorf(`%s (%s): "from" names %q, which is not an earlier step`, at, s.ID, f)
			}
		}
		if s.Side && len(s.From) == 0 && i == 0 {
			return fmt.Errorf(`%s (%s): a side step follows from another, and this is the first`, at, s.ID)
		}
		seen[s.ID] = true
		for j := range s.Code {
			if err := checkRef(root, dir, cmp, &s.Code[j]); err != nil {
				return fmt.Errorf("%s (%s): %v", at, s.ID, err)
			}
		}
	}
	for i := range w.Risks {
		r := &w.Risks[i]
		r.Level, r.Text = strings.ToLower(strings.TrimSpace(r.Level)), strings.TrimSpace(r.Text)
		at := fmt.Sprintf("risk %d", i+1)
		if !validLevel(r.Level) {
			return fmt.Errorf(`%s: "level" must be one of %s`, at, strings.Join(Levels, ", "))
		}
		if r.Text == "" {
			return fmt.Errorf(`%s: "text" is required`, at)
		}
		if r.File == "" {
			if r.Anchor != "" {
				return fmt.Errorf(`%s: an "anchor" needs its "file"`, at)
			}
			continue
		}
		ref := Ref{File: r.File, Anchor: r.Anchor}
		if err := checkRef(root, dir, cmp, &ref); err != nil {
			return fmt.Errorf("%s: %v", at, err)
		}
		r.File, r.Anchor = ref.File, ref.Anchor
	}
	return nil
}

func validLevel(l string) bool {
	for _, v := range Levels {
		if v == l {
			return true
		}
	}
	return false
}

// checkRef finds the file and the anchor a reference names. An anchor is
// looked for in the file as it is, where it must be on one line only, and
// failing that among the lines the change removed, so a step can point at
// code that is gone.
func checkRef(root, dir string, cmp git.Comparison, r *Ref) error {
	r.Anchor, r.Note = strings.TrimSpace(r.Anchor), strings.TrimSpace(r.Note)
	if r.File == "" {
		return fmt.Errorf(`a code reference needs "file"`)
	}
	if strings.ContainsAny(r.Anchor, "\r\n") {
		return fmt.Errorf("the anchor in %s must be a single line — take a distinctive fragment of one line", r.File)
	}
	rel, err := store.Relative(root, dir, r.File)
	gone := false
	if err != nil {
		g, ok := store.Removed(root, dir, r.File)
		if !ok {
			return err
		}
		rel, gone = g, true
	}
	r.File = rel
	if r.Anchor == "" {
		return nil
	}
	if !gone {
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return fmt.Errorf("cannot read %s: %v", rel, err)
		}
		switch lines := matching(string(src), r.Anchor); {
		case len(lines) == 1:
			return nil
		case len(lines) > 1:
			return fmt.Errorf("the anchor %q is on %d lines of %s (%s) — pick a fragment only the line you mean contains",
				r.Anchor, len(lines), rel, joinInts(lines))
		}
	}
	diff, _ := cmp.Diff(rel, false)
	for _, h := range parseHunks(diff) {
		for _, l := range h.Lines {
			if l.Kind == "-" && strings.Contains(l.Text, r.Anchor) {
				return nil
			}
		}
	}
	return fmt.Errorf("the anchor %q is not in %s, nor among the lines the change removed from it — copy it from the code as it is now, without indentation", r.Anchor, rel)
}

// matching lists the 1-based lines of src that contain anchor.
func matching(src, anchor string) []int {
	var out []int
	for i, l := range strings.Split(src, "\n") {
		if strings.Contains(l, anchor) {
			out = append(out, i+1)
		}
	}
	return out
}

func joinInts(v []int) string {
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = fmt.Sprint(n)
	}
	return strings.Join(s, ", ")
}

// Prune drops the walkthroughs no branch will show again: those of
// branches that are gone, those written before the branch now holding
// their name, and on each branch every one but the last. It does what
// order.Prune does, and leaves an absolute data_dir alone for its reason.
func Prune(worktreePath string) (int, error) {
	if filepath.IsAbs(store.DataDir) {
		return 0, nil
	}
	path := File(worktreePath)
	if path == "" {
		return 0, nil
	}
	branches, err := store.Branches(worktreePath)
	if err != nil {
		return 0, err
	}
	// Which line is each branch's last: the file is read once to count,
	// and Rewrite then reads it in the same order. A line appended in
	// between comes after the n counted, and is kept.
	last := map[string]int{}
	n := 0
	store.ReadLines(path, func(line []byte) {
		var w Walkthrough
		if json.Unmarshal(line, &w) == nil && w.Branch != "" {
			last[w.Branch] = n
		}
		n++
	})
	created := map[string]time.Time{}
	i := -1
	return store.Rewrite(path, func(line []byte) bool {
		i++
		if i >= n {
			return true
		}
		var w Walkthrough
		if json.Unmarshal(line, &w) != nil || w.Branch == "" {
			return true // not ours to judge: kept as it is
		}
		if !branches[w.Branch] || last[w.Branch] != i {
			return false
		}
		c, ok := created[w.Branch]
		if !ok {
			c, _ = store.BranchCreated(worktreePath, w.Branch)
			created[w.Branch] = c
		}
		return !before(w, c)
	})
}
