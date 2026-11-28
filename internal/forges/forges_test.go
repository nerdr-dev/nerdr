package forges

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/forge"
	"github.com/nerdr-dev/nerdr/internal/github"
	"github.com/nerdr-dev/nerdr/internal/gitlab"
)

// fakeRemotes has the folders' remotes be those given, by folder.
func fakeRemotes(t *testing.T, by map[string][]string) {
	t.Helper()
	old := readRemotes
	readRemotes = func(dir string) ([]forge.Remote, bool, error) {
		var out []forge.Remote
		odd := false
		for _, nameURL := range by[dir] {
			name, url := nameURL[:indexByte(nameURL, ' ')], nameURL[indexByte(nameURL, ' ')+1:]
			r, ok := forge.ParseRemote(url)
			if !ok {
				odd = true
				continue
			}
			r.Name = name
			out = append(out, r)
		}
		return out, odd, nil
	}
	cache.Lock()
	cache.m = map[string]remotesAt{}
	cache.Unlock()
	t.Cleanup(func() {
		readRemotes = old
		cache.Lock()
		cache.m = map[string]remotesAt{}
		cache.Unlock()
	})
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// Each repository is asked of its own origin's service: one tab can hold
// a GitHub repository and a GitLab one.
func TestForPicksTheOriginsService(t *testing.T) {
	fakeRemotes(t, map[string][]string{
		"/r/gh":    {"origin https://github.com/acme/app.git"},
		"/r/gl":    {"origin git@gitlab.com:me/app.git", "upstream https://gitlab.com/grp/app.git"},
		"/r/ghe":   {"origin https://ghe.corp.example/acme/app.git"},
		"/r/local": {"origin /srv/git/app.git"},
		"/r/none":  nil,
	})
	if _, ok := For("/r/gh").(github.Forge); !ok {
		t.Error("github.com should be GitHub")
	}
	gl, ok := For("/r/gl").(gitlab.Forge)
	if !ok || gl.Origin.Path != "me/app" || gl.Upstream.Path != "grp/app" {
		t.Errorf("gitlab: %#v", For("/r/gl"))
	}
	for _, d := range []string{"/r/ghe", "/r/local", "/r/none"} {
		if KindOf(d) != forge.GitHub {
			t.Errorf("%s should be left to gh", d)
		}
	}
	if PRRef("/r/gl", 3) != "refs/merge-requests/3/head" || PRRef("/r/gh", 3) != "refs/pull/3/head" {
		t.Error("refs")
	}
	if _, ok := ForURL("git@gitlab.com:grp/app.git").(gitlab.Forge); !ok {
		t.Error("a merge request goes to the service of the origin URL recorded")
	}
}

func TestRepoNamesAndHosts(t *testing.T) {
	forge.SetHosts(map[string]string{"git.corp.example": "gitlab"})
	defer forge.SetHosts(nil)
	fakeRemotes(t, map[string][]string{
		"/r/a": {"origin https://github.com/Acme/App.git"},
		"/r/b": {"origin https://git.corp.example/grp/sub/lib.git", "fork git@github.com:me/lib.git"},
		"/r/c": {"origin https://ghe.corp.example/acme/app.git"},
	})
	names, elsewhere, err := RepoNames("/r/b")
	if err != nil || elsewhere || len(names) != 2 || names[0] != "git.corp.example/grp/sub/lib" || names[1] != "me/lib" {
		t.Fatalf("names: %v %v %v", names, elsewhere, err)
	}
	if _, elsewhere, _ := RepoNames("/r/c"); !elsewhere {
		t.Error("GitHub Enterprise is not covered by a search")
	}
	hosts := Hosts([]string{"/r/a", "/r/b", "/r/c"})
	if len(hosts) != 2 || hosts[0] != "git.corp.example" || hosts[1] != "github.com" {
		t.Fatalf("hosts: %v", hosts)
	}
}

// A search on hosts asks each, and a host that fails is said, by name,
// without the others' answers going.
func TestSearchHosts(t *testing.T) {
	forge.SetHosts(map[string]string{"git.corp.example": "gitlab"})
	defer forge.SetHosts(nil)
	gh := func() (int, error) { return 1, nil }
	gl := func(host string) (int, error) {
		if host == "git.corp.example" {
			return 0, errors.New("glab timed out")
		}
		return 2, nil
	}
	bin := t.TempDir()
	for _, cli := range []string{"gh", "glab"} {
		if err := os.WriteFile(filepath.Join(bin, cli), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	s := searchHosts([]string{"github.com", "gitlab.com", "git.corp.example", "ghe.corp.example"}, gh, gl)
	if !s.Searched("github.com") || !s.Searched("gitlab.com") || s.Searched("git.corp.example") || s.Searched("ghe.corp.example") {
		t.Fatalf("searched: %+v", s)
	}
	if s.Err() == nil || s.Err().Error() != "git.corp.example: glab timed out" {
		t.Fatalf("err: %v", s.Err())
	}
}

// An issue assigned to you is read of the remote it is of: #7 of a
// fork's upstream is not #7 of the fork.
func TestFetchIssueOfItsRemote(t *testing.T) {
	fakeRemotes(t, map[string][]string{
		"/r/gl": {"origin git@gitlab.com:me/app.git", "upstream https://gitlab.com/grp/app.git"},
	})
	bin := t.TempDir()
	script := "#!/bin/sh\ncase \"$2\" in\n" +
		"projects/grp%2Fapp/issues/7) echo '{\"iid\":7,\"title\":\"upstream\"}';;\n" +
		"projects/me%2Fapp/issues/7) echo '{\"iid\":7,\"title\":\"fork\"}';;\n" +
		"*) exit 1;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "glab"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	is, err := FetchIssueOf("/r/gl", "gitlab.com/grp/app", 7)
	if err != nil || is.Title != "upstream" {
		t.Fatalf("upstream's: %+v %v", is, err)
	}
	if is, err := FetchIssueOf("/r/gl", "", 7); err != nil || is.Title != "fork" {
		t.Fatalf("origin's: %+v %v", is, err)
	}
}
