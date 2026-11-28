package proc

import "testing"

// TestSub: what a call was asked to do, past the options before it.
func TestSub(t *testing.T) {
	for _, c := range []struct {
		program string
		args    []string
		want    string
	}{
		{"git", []string{"-C", "/w", "status", "--porcelain"}, "status"},
		{"git", []string{"-c", "a=b", "--no-pager", "log", "-n", "1"}, "log"},
		{"gh", []string{"pr", "view", "--json", "state"}, "pr view"},
		{"gh", []string{"api", "repos/o/r/pulls"}, "api"},
		{"gh", []string{"api", "-X", "GET", "user"}, "api"},
		{"gh", []string{"auth", "status"}, "auth status"},
		{"glab", []string{"api", "projects/g%2Fr/merge_requests?state=opened", "--hostname", "gitlab.com"}, "api"},
		{"glab", []string{"api", "user", "--hostname", "gitlab.com"}, "api"},
		{"glab", []string{"repo", "clone", "g/r", "r"}, "repo clone"},
		{"claude", []string{"--resume", "x"}, ""},
	} {
		if got := sub(c.program, c.args); got != c.want {
			t.Errorf("%s %v: %q, want %q", c.program, c.args, got, c.want)
		}
	}
}
