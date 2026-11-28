package forge

import (
	"strings"
	"testing"
)

func TestParseRemote(t *testing.T) {
	for _, c := range []struct {
		url, host, path string
		ok              bool
	}{
		{"https://github.com/o/r.git", "github.com", "o/r", true},
		{"git@github.com:o/r.git", "github.com", "o/r", true},
		{"ssh://git@github.com/o/r", "github.com", "o/r", true},
		{"https://token@github.com/o/r/", "github.com", "o/r", true},
		{"https://github.com/o/r/extra", "", "", false}, // not a repository on GitHub
		{"git@gitlab.com:grp/sub/name.git", "gitlab.com", "grp/sub/name", true},
		{"https://GitLab.Example.com:8443/grp/name.git", "gitlab.example.com:8443", "grp/name", true},
		{"ssh://git@gitlab.example.com:2222/grp/name.git", "gitlab.example.com", "grp/name", true},
		{"/srv/git/r.git", "", "", false},
		{"file:///srv/git/r.git", "", "", false},
		{"https://example.com/justone", "", "", false},
	} {
		r, ok := ParseRemote(c.url)
		if ok != c.ok || r.Host != c.host || r.Path != c.path {
			t.Errorf("%s: %+v %v, want %s %s %v", c.url, r, ok, c.host, c.path, c.ok)
		}
	}
}

func TestKindOf(t *testing.T) {
	defer SetHosts(nil)
	SetHosts(map[string]string{"git.corp.example": "gitlab", "gitlab.fake.example": "github"})
	for host, want := range map[string]Kind{
		"github.com":            GitHub,
		"gitlab.com":            GitLab,
		"gitlab.example.com":    GitLab,
		"git.corp.example":      GitLab,
		"git.corp.example:8443": GitLab, // the port is no other host
		"gitlab.fake.example":   GitHub, // the config says so
		"ghe.example.com":       GitHub,
	} {
		if got := KindOf(host); got != want {
			t.Errorf("%s: %q, want %q", host, got, want)
		}
	}
	if KindWith("git.other.example:8443", map[string]string{"git.other.example": "gitlab"}) != GitLab {
		t.Error("KindWith should go by the map it is given, the port aside")
	}
	if KindWith("git.corp.example", nil) != GitHub {
		t.Error("KindWith should not go by the config SetHosts took")
	}
}

// A GitHub key is owner/name, as nerdr kept them before any other host:
// state from then still matches. Any other has its host, and they never
// meet.
func TestRepoKey(t *testing.T) {
	if k := RepoKey("github.com", "Acme/Nerdr"); k != "acme/nerdr" || KeyHost(k) != "github.com" {
		t.Errorf("github: %q %q", k, KeyHost(k))
	}
	k := RepoKey("gitlab.com", "grp/sub/App")
	if k != "gitlab.com/grp/sub/app" || KeyHost(k) != "gitlab.com" || KeyHost(RequestKey(k, 4)) != "gitlab.com" {
		t.Errorf("gitlab: %q %q", k, KeyHost(k))
	}
	if KeyHost(RequestKey("acme/nerdr", 12)) != "github.com" {
		t.Error("a GitHub request key's host")
	}
}

func TestKindWords(t *testing.T) {
	if GitHub.Ref(12) != "#12" || GitLab.Ref(12) != "!12" || GitLab.PRTitle() != "Merge request" || GitHub.CLI() != "gh" {
		t.Error("words wrong")
	}
	is := Issue{Kind: GitLab, Number: 3, Title: "Fix it"}
	if !strings.HasPrefix(is.Task(), "Resolve GitLab issue #3: Fix it") {
		t.Errorf("task: %q", is.Task())
	}
}

func TestStripANSIAndCondense(t *testing.T) {
	log := "section_start:1700000000:step_script\r\x1b[0K\x1b[36;1m$ make test\x1b[0;m\n" +
		"FAIL foo_test.go:12\n" +
		"section_end:1700000001:step_script\r\x1b[0K\n" +
		"\x1b[31;1mERROR: Job failed: exit code 1\x1b[0;m\n"
	got := CondenseLog(StripANSI(log), 1000, "ERROR: ")
	if strings.Contains(got, "\x1b") || strings.Contains(got, "section_") {
		t.Fatalf("codes left in: %q", got)
	}
	if !strings.Contains(got, "$ make test") || !strings.Contains(got, "FAIL foo_test.go:12") || !strings.Contains(got, "ERROR: Job failed") {
		t.Fatalf("lost the failure: %q", got)
	}
}
