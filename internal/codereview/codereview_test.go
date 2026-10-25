package codereview

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	r, err := Parse([]byte(`{"verdict": "Changes", "summary": " two things ",
		"findings": [{"file": "./a.go", "anchor": "x := 1", "severity": "high", "text": "first line\nmore"},
		             {"file": "b.go", "severity": "low", "text": "small"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != Changes || r.Summary != "two things" || r.Findings[0].File != "a.go" {
		t.Fatalf("parsed = %+v", r)
	}
	if got := r.Brief(); got != "2 findings (1 high, 1 low)" {
		t.Fatalf("brief = %q", got)
	}
	for _, bad := range []string{
		`{"verdict": "approve"}`,
		`{"verdict": "changes", "summary": "x"}`,
		`{"verdict": "approve", "summary": "x", "extra": 1}`,
		`{"verdict": "approve", "summary": "x", "findings": [{"file": "../up.go", "severity": "low", "text": "x"}]}`,
		`{"verdict": "approve", "summary": "x", "findings": [{"file": "src/../../up.go", "severity": "low", "text": "x"}]}`,
		`{"verdict": "approve", "summary": "x", "findings": [{"file": "/etc/passwd", "severity": "low", "text": "x"}]}`,
		`{"verdict": "approve", "summary": "x", "findings": [{"file": "a.go", "severity": "huge", "text": "x"}]}`,
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("taken: %s", bad)
		}
	}
	if got := (Result{Verdict: Approve, Summary: "fine"}).Brief(); got != "approved" {
		t.Fatalf("brief = %q", got)
	}
}

func TestLatestAndNotes(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	wt, _ := filepath.EvalSymlinks(repo)
	if recs, _ := Latest(wt); len(recs) != 0 {
		t.Fatal("a review where none was kept")
	}
	first := Record{Worktree: wt, Agent: "codex", Round: 1, Result: Result{Verdict: Changes, Summary: "s",
		Findings: []Finding{{File: "a.go", Anchor: "x := 1", Severity: High, Text: "wrong\nbecause"}, {File: "b.go", Severity: Low, Text: "whole file"}}}}
	_ = os.Mkdir(filepath.Join(wt, "elsewhere"), 0o755) // another worktree's record, in the same file
	other := Record{Worktree: filepath.Join(wt, "elsewhere"), Round: 1, Result: Result{Verdict: Approve, Summary: "s"}}
	for _, r := range []Record{first, other} {
		if err := Save(r); err != nil {
			t.Fatal(err)
		}
	}
	recs, read := Latest(wt)
	if len(recs) != 1 || read != 2 || recs[0].Round != 1 || len(recs[0].Findings) != 2 {
		t.Fatalf("latest = %+v, %d", recs, read)
	}
	notes := Notes(recs[0])
	if len(notes) != 1 || notes[0].Anchor != "x := 1" || notes[0].Short != "⚑ high: wrong" || notes[0].Agent != "codex review" {
		t.Fatalf("notes = %+v", notes)
	}

	// Two agents reviewing side by side, over two rounds: each one's
	// latest is kept, and an older review is not.
	for _, r := range []Record{
		{Worktree: wt, Agent: "codex", Round: 1, Run: "b", Result: Result{Verdict: Changes, Summary: "c1", Findings: first.Findings}},
		{Worktree: wt, Agent: "claude", Round: 1, Run: "b", Result: Result{Verdict: Approve, Summary: "a1"}},
		{Worktree: wt, Agent: "codex", Round: 2, Run: "b", Result: Result{Verdict: Changes, Summary: "c2", Findings: first.Findings}},
	} {
		if err := Save(r); err != nil {
			t.Fatal(err)
		}
	}
	recs, _ = Latest(wt)
	if len(recs) != 2 || recs[0].Summary != "c2" || recs[1].Summary != "a1" {
		t.Fatalf("latest of two reviewers = %+v", recs)
	}
	if _, err := os.Stat(filepath.Join(wt, ".git", "nerdr", FileName)); err != nil {
		t.Fatal(err)
	}
}

func TestPrompts(t *testing.T) {
	req := Request("claude", "codex", "main", "", 1, 2)
	for _, want := range []string{"you (codex) are reviewing", "git log main..HEAD", "round 1 of at most 2", `post-review - <<'EOF'`} {
		if !strings.Contains(req, want) {
			t.Errorf("request lacks %q", want)
		}
	}
	if strings.Contains(req, "look most at") {
		t.Error("a request with no focus asks to look most at something")
	}
	if req := Request("claude", "codex", "main", "the locking in store.go", 1, 2); !strings.Contains(req, "claude asked for this review and wants you to look most at:\nthe locking in store.go") {
		t.Errorf("request lacks the focus:\n%s", req)
	}
	one := Review{Agent: "codex", Result: Result{Summary: "sum", Findings: []Finding{{File: "a.go", Anchor: "x", Severity: High, Text: "t"}}}}
	fix := FixPrompt([]Review{one}, 1, 2)
	if !strings.Contains(fix, "codex reviewed your change (round 1 of 2) and asks") || !strings.Contains(fix, "1. [high] a.go at `x`: t") {
		t.Errorf("fix prompt:\n%s", fix)
	}
	two := Review{Agent: "claude", Result: Result{Summary: "other", Findings: []Finding{{File: "b.go", Severity: Low, Text: "u"}}}}
	fix = FixPrompt([]Review{one, two}, 1, 2)
	for _, want := range []string{"codex and claude reviewed your change", "codex: sum", "1. [high] a.go", "claude: other", "2. [low] b.go: u"} {
		if !strings.Contains(fix, want) {
			t.Errorf("fix prompt of two lacks %q:\n%s", want, fix)
		}
	}
}

func TestSessions(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	wt, _ := filepath.EvalSymlinks(repo)
	_ = os.Mkdir(filepath.Join(wt, "elsewhere"), 0o755)
	for _, s := range []struct{ wt, agent, id string }{
		{wt, "claude", "a"}, {wt, "codex", "b"}, {filepath.Join(wt, "elsewhere"), "claude", "c"},
	} {
		if err := SaveSession(s.wt, s.agent, s.id); err != nil {
			t.Fatal(err)
		}
	}
	// Only this worktree's, and only the agent's: another kind's ids, or
	// another worktree's, are no concern of its resuming.
	if got := Sessions(wt, "claude"); len(got) != 1 || !got["a"] {
		t.Errorf("Sessions = %v", got)
	}
}

// TestFixPromptSaysWhetherItIsReviewedAgain: only a round before the last
// promises another review.
func TestFixPromptSaysWhetherItIsReviewedAgain(t *testing.T) {
	rs := []Review{{Agent: "codex", Result: Result{Summary: "s"}}}
	if p := FixPrompt(rs, 1, 2); !strings.Contains(p, "reviewed again.") || strings.Contains(p, "last round") {
		t.Fatalf("round 1 of 2: %q", p)
	}
	if p := FixPrompt(rs, 1, 1); strings.Contains(p, "nerdr then has") || !strings.Contains(p, "last round") {
		t.Fatalf("round 1 of 1: %q", p)
	}
}

func TestApprovedRemarksPrompt(t *testing.T) {
	rs := []Review{{Agent: "claude", Result: Result{Verdict: Approve, Summary: "fine",
		Findings: []Finding{{File: "a.go", Severity: Low, Text: "a small remark"}}}}}
	p := FixPrompt(rs, 1, 3)
	for _, want := range []string{"approved it with remarks", "optional remarks", "a small remark", "review is complete"} {
		if !strings.Contains(p, want) {
			t.Errorf("missing %q: %s", want, p)
		}
	}
	if !IsFixPrompt(p) || strings.Contains(p, "asks for changes") || strings.Contains(p, "reviewed again") {
		t.Fatalf("approval should not request changes or another round: %s", p)
	}
}
