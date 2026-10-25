package spinoff

import (
	"errors"
	"strings"
	"testing"
)

func valid(s string) error {
	if s == "" || strings.Contains(s, " ") {
		return errors.New("bad name")
	}
	return nil
}

func TestParseSaysWhatIsWrongWithEachPart(t *testing.T) {
	_, err := Parse([]byte(`{"parts": [
		{"branch": "a", "summary": "s", "prompt": "p"},
		{"branch": "a", "summary": "", "prompt": "p"},
		{"branch": "b c", "summary": "s", "prompt": " ", "commits": ["x y"], "repos": ["api", "../web"]}
	]}`), valid)
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{
		"part 2 (a): the same branch", "part 2 (a): summary is empty",
		"part 3 (b c): branch: bad name", "part 3 (b c): prompt is empty", `"x y" is not a commit`,
		`"../web" is not a repository's folder`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%v", want, err)
		}
	}
	if _, err := Parse([]byte(`{"parts": [], "extra": 1}`), valid); err == nil {
		t.Fatal("an unknown field was taken")
	}
	p, err := Parse([]byte(`{"parts": [{"branch": " a ", "summary": "s", "prompt": "p"}]}`), valid)
	if err != nil || p.Parts[0].Branch != "a" {
		t.Fatalf("%+v, %v", p, err)
	}
}

func TestPromptSaysWhatIsOnTheBranch(t *testing.T) {
	pt := Part{Branch: "a", Prompt: "Do A.", Commits: []string{"c1"}}
	from := Parent{Name: "big", Branch: "big", Dir: "/w/big"}
	for _, tc := range []struct {
		o    Outcome
		want string
	}{
		{Outcome{Reused: true}, "already on this one"},
		{Outcome{}, "written anew"},
		{Outcome{Reused: true, PickFailed: "conflict"}, "did not apply cleanly (conflict)"},
	} {
		got := Prompt(pt, from, tc.o)
		if !strings.HasPrefix(got, "Do A.") || !strings.Contains(got, tc.want) || !strings.Contains(got, "/w/big") {
			t.Errorf("%+v:\n%s", tc.o, got)
		}
	}
	if got := Prompt(Part{Prompt: "Do B."}, from, Outcome{}); strings.Contains(got, "work was started there") {
		t.Errorf("no commits, but:\n%s", got)
	}
}

func TestReportTellsTheAgentWhatBecameOfEachPart(t *testing.T) {
	got := Report([]Outcome{
		{Part: Part{Branch: "a", Commits: []string{"c1"}}, Branch: "renamed", Dir: "/w/renamed", Agent: "opencode", Reused: true},
		{Part: Part{Branch: "b"}, Skipped: true},
	}, "big")
	for _, want := range []string{
		"a: made as a new tab on branch renamed, in /w/renamed, with opencode.", "The user renamed the branch", "starts with commits c1",
		"b: skipped by the user", "still on this branch (big)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if got := Report([]Outcome{{Part: Part{Branch: "b"}, Skipped: true}}, "big"); strings.Contains(got, "still on this branch") {
		t.Errorf("nothing moved, but:\n%s", got)
	}
	for start, want := range map[string]string{
		StartBegun:   "has started on it",
		StartStalled: "did not take the prompt up",
		StartExited:  "stopped before it started",
		StartClosed:  "closed that tab",
		StartSlow:    "not been seen starting",
		"":           "has been given the prompt",
	} {
		got := Report([]Outcome{{Part: Part{Branch: "a"}, Branch: "a", Dir: "/w/a", Start: start}}, "big")
		if !strings.Contains(got, want) {
			t.Errorf("start %q: missing %q in:\n%s", start, want, got)
		}
	}
}
