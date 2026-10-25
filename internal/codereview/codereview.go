// Package codereview is a second agent reviewing the work of a tab's own.
//
// When a tab's agent has finished a turn that changed the worktree, nerdr
// can start another agent — another kind, by choice: codex reviewing
// claude's work — in the same worktree, beside it in the same tab. The
// reviewer is told what to look at (Request) and sends its conclusion back
// with `nerdr post-review` (Parse): a verdict, a summary, and findings anchored
// to the code the way why notes are. Several agents can review the same
// work side by side. nerdr keeps the latest review of each worktree (Save,
// Latest) for the review panel to show against the lines, and hands the
// findings to the tab's agent (FixPrompt), all reviewers' in one message.
// They go round until the reviewers approve or the rounds run out.
//
// The reviewer gets none of the author's conversation, on purpose: a
// review is worth having for the eyes that did not write the code.
package codereview

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/clean"
	"github.com/nerdr-dev/nerdr/internal/store"
	"github.com/nerdr-dev/nerdr/internal/why"
)

// fixMark is in the first line of every FixPrompt, for IsFixPrompt.
const fixMark = " reviewed your change (round "

// IsFixPrompt reports whether a message for the agent is a review's
// findings, made by FixPrompt.
func IsFixPrompt(text string) bool {
	first, _, _ := strings.Cut(text, "\n")
	return strings.HasPrefix(first, "nerdr: ") && strings.Contains(first, fixMark)
}

// FileName is the file reviews are kept in, beside the why notes.
const FileName = "reviews.jsonl"

// Verdicts.
const (
	Approve = "approve"
	Changes = "changes"
)

// Severities, worst first.
const (
	High   = "high"
	Medium = "medium"
	Low    = "low"
)

// Limits on what a reviewer may send: it is drawn in the panel and typed
// into the tab's agent as a message.
const (
	MaxFindings = 50
	MaxSummary  = 4 << 10
	MaxText     = 4 << 10
)

// Finding is one thing the reviewer wants looked at.
type Finding struct {
	// File is relative to the worktree.
	File string `json:"file"`
	// Anchor is a fragment of the line the finding is about, as a why
	// note's is. Optional: a finding about a whole file, or a file that is
	// missing, has none.
	Anchor   string `json:"anchor,omitempty"`
	Severity string `json:"severity"`
	Text     string `json:"text"`
}

// Result is what the reviewer sends.
type Result struct {
	Verdict  string    `json:"verdict"`
	Summary  string    `json:"summary"`
	Findings []Finding `json:"findings,omitempty"`
}

// Record is a Result as nerdr keeps it.
type Record struct {
	Result
	Worktree string `json:"worktree"`
	Branch   string `json:"branch,omitempty"`
	Agent    string `json:"agent,omitempty"`
	Author   string `json:"author,omitempty"`
	Round    int    `json:"round"`
	Time     string `json:"ts,omitempty"`
	// Run is the review the record belongs to, the same for all the
	// agents reviewing the work side by side.
	Run string `json:"run,omitempty"`
}

// Parse reads a Result and checks it, saying what is wrong so the reviewer
// can send it again.
func Parse(data []byte) (Result, error) {
	var r Result
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return Result{}, fmt.Errorf("not a review: %v", err)
	}
	return r, Check(&r)
}

// Check makes r safe to draw and keep, and says what is wrong with it.
// nerdr's socket checks a review with it too, since an agent can write to
// the socket without `nerdr post-review`.
func Check(r *Result) error {
	r.Verdict = strings.ToLower(strings.TrimSpace(r.Verdict))
	r.Summary = strings.TrimSpace(clean.Text(r.Summary))
	switch {
	case r.Verdict != Approve && r.Verdict != Changes:
		return errors.New(`verdict must be "approve" or "changes"`)
	case r.Summary == "":
		return errors.New("summary is empty: say in a few lines what you found")
	case len(r.Summary) > MaxSummary:
		return fmt.Errorf("summary is over %d KB", MaxSummary/1024)
	case len(r.Findings) > MaxFindings:
		return fmt.Errorf("more than %d findings: keep to what matters", MaxFindings)
	case r.Verdict == Changes && len(r.Findings) == 0:
		return errors.New(`verdict "changes" needs at least one finding`)
	}
	for i := range r.Findings {
		f := &r.Findings[i]
		f.File = filepath.ToSlash(strings.TrimPrefix(strings.TrimSpace(clean.Line(f.File)), "./"))
		f.Anchor = strings.TrimSpace(clean.Line(f.Anchor))
		f.Severity = strings.ToLower(strings.TrimSpace(f.Severity))
		f.Text = strings.TrimSpace(clean.Text(f.Text))
		file, local := store.Local(f.File)
		switch {
		case f.File == "":
			return fmt.Errorf("finding %d: file is empty", i+1)
		case !local:
			return fmt.Errorf("finding %d: file must be relative to the worktree", i+1)
		case f.Severity != High && f.Severity != Medium && f.Severity != Low:
			return fmt.Errorf(`finding %d: severity must be "high", "medium" or "low"`, i+1)
		case f.Text == "":
			return fmt.Errorf("finding %d: text is empty", i+1)
		case len(f.Text) > MaxText:
			return fmt.Errorf("finding %d: text is over %d KB", i+1, MaxText/1024)
		}
		f.File = file
	}
	return nil
}

// Counts is how many findings r has of each severity.
func (r Result) Counts() (high, medium, low int) {
	for _, f := range r.Findings {
		switch f.Severity {
		case High:
			high++
		case Medium:
			medium++
		default:
			low++
		}
	}
	return
}

// Brief is r on one line: the verdict and its findings.
func (r Result) Brief() string {
	if r.Verdict == Approve && len(r.Findings) == 0 {
		return "approved"
	}
	h, md, l := r.Counts()
	var parts []string
	for _, c := range []struct {
		n    int
		word string
	}{{h, "high"}, {md, "medium"}, {l, "low"}} {
		if c.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", c.n, c.word))
		}
	}
	s := fmt.Sprintf("%d finding", len(r.Findings))
	if len(r.Findings) != 1 {
		s += "s"
	}
	if len(parts) > 0 {
		s += " (" + strings.Join(parts, ", ") + ")"
	}
	if r.Verdict == Approve {
		s = "approved · " + s
	}
	return s
}

// Save appends rec to the reviews kept for its worktree.
func Save(rec Record) error {
	path := store.Path(rec.Worktree, FileName)
	if path == "" {
		return errors.New("not in a git repository")
	}
	return store.Append(path, rec)
}

// Latest is the newest review of the worktree — each reviewer's latest
// record in it, when several reviewed the work side by side — and how many
// records the file held: it only grows, so it tells a caller polling for
// changes that a review came in.
func Latest(worktree string) (recs []Record, read int) {
	worktree = filepath.Clean(worktree)
	store.ReadLines(store.Path(worktree, FileName), func(line []byte) {
		read++
		var r Record
		if json.Unmarshal(line, &r) != nil || filepath.Clean(r.Worktree) != worktree {
			return
		}
		// The file is written by nerdr, but it sits where agents write:
		// made safe to draw again.
		if Check(&r.Result) != nil {
			return
		}
		r.Agent, r.Author, r.Branch, r.Time = clean.Line(r.Agent), clean.Line(r.Author), clean.Line(r.Branch), clean.Line(r.Time)
		if len(recs) > 0 && (r.Run == "" || r.Run != recs[0].Run) {
			recs = recs[:0] // a newer review
		}
		for i := range recs {
			if recs[i].Agent == r.Agent {
				recs[i] = r
				return
			}
		}
		recs = append(recs, r)
	})
	return recs, read
}

// SessionsFile is the file the reviewers' conversations are kept in.
const SessionsFile = "review-sessions.jsonl"

// session is one reviewer's conversation, as SessionsFile keeps it.
type session struct {
	Worktree string `json:"worktree"`
	Agent    string `json:"agent"`
	ID       string `json:"id"`
}

// SaveSession records that the conversation id of agent, in worktree, is
// a reviewer's. It was had in the worktree as the tab's own are, and
// would be taken for the tab's latest when its agent is switched to the
// reviewer's kind, or a conversation is looked for to carry on.
func SaveSession(worktree, agent, id string) error {
	path := store.Path(worktree, SessionsFile)
	if path == "" {
		return errors.New("not in a git repository")
	}
	return store.Append(path, session{Worktree: filepath.Clean(worktree), Agent: agent, ID: id})
}

// Sessions are the reviewers' conversations of agent in worktree, for
// resuming to pass over.
func Sessions(worktree, agent string) map[string]bool {
	worktree = filepath.Clean(worktree)
	out := map[string]bool{}
	store.ReadLines(store.Path(worktree, SessionsFile), func(line []byte) {
		var s session
		if json.Unmarshal(line, &s) == nil && s.ID != "" && s.Agent == agent && filepath.Clean(s.Worktree) == worktree {
			out[s.ID] = true
		}
	})
	return out
}

// Notes are the latest review's findings as why notes, for the review
// panel to show above the lines they are about. A finding with no anchor
// has no line to go above, and is in the summary only.
func Notes(rec Record) []why.Note {
	var out []why.Note
	by := "review"
	if rec.Agent != "" {
		by = rec.Agent + " review"
	}
	for _, f := range rec.Findings {
		if f.Anchor == "" {
			continue
		}
		short, detail := f.Text, ""
		if i := strings.IndexByte(short, '\n'); i >= 0 {
			short, detail = strings.TrimSpace(short[:i]), f.Text
		}
		out = append(out, why.Note{
			File: f.File, Anchor: f.Anchor,
			Short:  "⚑ " + f.Severity + ": " + short,
			Detail: detail, Agent: by, Time: rec.Time, Branch: rec.Branch,
		})
	}
	return out
}

// MaxFocus is the most an agent asking for a review can say about what to
// look at: a pointer or two, not a brief.
const MaxFocus = 2 << 10

// Request is the first message for the reviewer; focus is what the author
// asked to have looked at most, if anything.
func Request(author, reviewer, base, focus string, round, rounds int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "nerdr: you (%s) are reviewing the work %s has done in this worktree, as a second pair of eyes. You have none of its conversation, on purpose.", reviewer, author)
	b.WriteString("\n\nLook at the change: ")
	if base != "" {
		fmt.Fprintf(&b, "the branch's commits since it left %s (git log %s..HEAD, git diff $(git merge-base %s HEAD)), ", base, base, base)
	}
	b.WriteString("and what is not committed (git status, git diff HEAD, new files). Read the code around it as you need, and any why notes and reading order recorded for it.")
	b.WriteString("\n\nLook for what would make the change wrong: bugs, cases it misses, broken behaviour elsewhere, races, security problems, missing or wrong tests. Mention style only where it hides a bug. Do not change, commit or push anything: the author makes the fixes. Running tests and builds to check something is fine.")
	if focus != "" {
		fmt.Fprintf(&b, "\n\n%s asked for this review and wants you to look most at:\n%s\nLook at the rest of the change too: this is where it is unsure, not all there is.", author, focus)
	}
	fmt.Fprintf(&b, "\n\nThis is round %d of at most %d.", round, rounds)
	b.WriteString(sendHow)
	return b.String()
}

// Again is the message for a reviewer whose findings the author has worked
// on.
func Again(author string, round, rounds int) string {
	return fmt.Sprintf("nerdr: %s has worked on your findings; this is round %d of at most %d. Look at the change again — what it did about each finding, and anything new it broke — and send a new review the same way.%s",
		author, round, rounds, sendHow)
}

const sendHow = `

When you are done, send your review to nerdr as JSON:

"${NERDR_BIN:-nerdr}" post-review - <<'EOF'
{"verdict": "changes", "summary": "a few lines: what you looked at and what you found",
 "findings": [{"file": "path/from/worktree/root.go", "anchor": "a fragment of the line, found on no other line",
               "severity": "high", "text": "what is wrong, why, and what to do about it"}]}
EOF

verdict is "approve" when nothing needs fixing (findings may still hold small remarks) and "changes" when something does. severity is high, medium or low. Leave anchor out for a finding about a whole file. Write in the language the user works in. Once it is sent, do nothing more and wait.

If the command fails, show the user its message, put your review in your reply, and wait. Do not look for, build or run another nerdr, and do not save the review in a file for the user to send: the command only passes it on to the nerdr that started you, for this review, and no one else can send it for you.`

// Review is one reviewer's Result.
type Review struct {
	Agent string
	Result
}

// FixPrompt is the message for the tab's agent with the reviewers'
// findings, numbered through.
func FixPrompt(rs []Review, round, rounds int) string {
	var b strings.Builder
	var who []string
	approved := len(rs) > 0
	for _, r := range rs {
		who = append(who, r.Agent)
		approved = approved && r.Verdict == Approve
	}
	ask := "asks"
	if len(rs) > 1 {
		ask = "ask"
	}
	if approved {
		fmt.Fprintf(&b, "nerdr: %s"+fixMark+"%d of %d) and approved it with remarks.", strings.Join(who, " and "), round, rounds)
	} else {
		fmt.Fprintf(&b, "nerdr: %s"+fixMark+"%d of %d) and %s for changes.", strings.Join(who, " and "), round, rounds, ask)
	}
	n := 0
	for _, r := range rs {
		b.WriteString("\n\n")
		if len(rs) > 1 {
			b.WriteString(r.Agent + ": ")
		}
		if r.Verdict == Approve {
			b.WriteString("Approved with optional remarks. ")
		}
		b.WriteString(r.Summary + "\n")
		for _, f := range r.Findings {
			n++
			where := f.File
			if f.Anchor != "" {
				where += " at `" + f.Anchor + "`"
			}
			fmt.Fprintf(&b, "\n%d. [%s] %s: %s", n, f.Severity, where, f.Text)
		}
	}
	if len(rs) > 1 {
		b.WriteString("\n\nThey reviewed it each on their own, so findings may overlap or disagree.")
	}
	if approved {
		b.WriteString("\n\nThe review is complete; these remarks do not request another round. Consider them and address what you agree with. Explain any you leave unchanged.")
		return b.String()
	}
	b.WriteString("\n\nFix what you agree with. Where you disagree, say why in your answer rather than changing the code. When you are done, finish your turn: ")
	if round < rounds {
		b.WriteString("nerdr then has the change reviewed again.")
	} else {
		b.WriteString("this was the last round, so nerdr does not have it reviewed again by itself.")
	}
	return b.String()
}
