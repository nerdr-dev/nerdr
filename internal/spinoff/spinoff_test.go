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
		"part 3 (b c): branch: bad name", `"x y" is not a commit`,
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
	// A part's prompt may come after the plan.
	if p, err := Parse([]byte(`{"parts": [{"branch": "a", "summary": "s"}]}`), valid); err != nil || p.Parts[0].Prompt != "" {
		t.Fatalf("a plan with its prompts to come: %+v, %v", p, err)
	}
	if CheckPrompt(strings.Repeat("x", MaxPrompt+1)) == nil {
		t.Fatal("a prompt over the limit was taken")
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
		{Outcome{Elsewhere: true}, "cannot be brought into this repository"},
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

// A part made in another repository is told so, and its commits stay
// with the agent that sent it.
func TestElsewhereSaysAnotherRepository(t *testing.T) {
	pt := Part{Branch: "a", Prompt: "Do A.", Commits: []string{"c1"}}
	got := Prompt(pt, Parent{Name: "big", Dir: "/w/big"}, Outcome{Elsewhere: true})
	if strings.Contains(got, "shares the repository") || !strings.Contains(got, "another repository") {
		t.Errorf("prompt:\n%s", got)
	}
	rep := Report([]Outcome{{Part: pt, Branch: "a", Dir: "/w/a", Elsewhere: true}}, "big")
	if !strings.Contains(rep, "another repository than yours") || !strings.Contains(rep, "still yours") || strings.Contains(rep, "keep them out") {
		t.Errorf("report:\n%s", rep)
	}
	// Beside a part made here, only that part's commits are to be kept out.
	rep = Report([]Outcome{
		{Part: pt, Branch: "a", Dir: "/w/a", Elsewhere: true},
		{Part: Part{Branch: "b", Commits: []string{"c2"}}, Branch: "b", Dir: "/w/b", Reused: true},
	}, "big")
	if !strings.Contains(rep, "Commits c2 are still on this branch (big)") {
		t.Errorf("report with both:\n%s", rep)
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
		StartUnsent:  "never sent",
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

// A part made on another machine: its agent is told the tab it came from
// is there, out of reach, and the agent that sent it where the tab is.
func TestPromptAndReportOnAnotherMachine(t *testing.T) {
	pt := Part{Branch: "fix-a", Prompt: "Do A.", Commits: []string{"abc1234"}}
	from := Parent{Name: "login", Branch: "feat", Dir: "/home/u/wt/feat", Machine: "laptop"}
	cases := []struct {
		o    Outcome
		want string
	}{
		{Outcome{Reused: true}, "were brought over and are already on this one"},
		{Outcome{Reused: true, PickFailed: "conflict"}, "could not (conflict)"},
		{Outcome{}, "they are not on this machine"},
		{Outcome{Elsewhere: true}, "cannot be brought into this repository: write what this repository needs, from the prompt"},
	}
	for _, c := range cases {
		p := Prompt(pt, from, c.o)
		if !strings.Contains(p, "on the machine laptop. That is another machine") || !strings.Contains(p, c.want) {
			t.Errorf("%+v:\n%s", c.o, p)
		}
		if strings.Contains(p, "git -C") || strings.Contains(p, "shares the repository") {
			t.Errorf("%+v: tells the agent to look at what it cannot reach:\n%s", c.o, p)
		}
	}
	r := Report([]Outcome{{Part: pt, Branch: "fix-a", Dir: "/srv/wt/fix-a", Machine: "box", Reused: true}}, "feat")
	if !strings.Contains(r, "in /srv/wt/fix-a on the machine box.") || !strings.Contains(r, "another machine than yours") ||
		!strings.Contains(r, "It starts with commits abc1234") {
		t.Errorf("report:\n%s", r)
	}
}

// A repository's folder is kept as it was checked, trimmed, and neither it
// nor a commit may have a control character in it.
func TestCheckKeepsReposTrimmedAndRefusesControlCharacters(t *testing.T) {
	p, err := Parse([]byte(`{"parts": [{"branch": "a", "summary": "s", "prompt": "p", "repos": [" api\r"]}]}`), valid)
	if err != nil || p.Parts[0].Repos[0] != "api" {
		t.Fatalf("%+v, %v", p, err)
	}
	for _, bad := range []string{"a\rb", "a\vb", "a\x00b", "a\x1bb"} {
		if err := Check([]Part{{Branch: "a", Summary: "s", Prompt: "p", Repos: []string{bad}}}, valid); err == nil {
			t.Errorf("repo %q taken", bad)
		}
		if err := Check([]Part{{Branch: "a", Summary: "s", Prompt: "p", Commits: []string{"abc" + bad}}}, valid); err == nil {
			t.Errorf("commit %q taken", bad)
		}
	}
}
