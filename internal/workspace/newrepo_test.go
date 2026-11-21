package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/gitsafe"
)

// gitIdentity gives the commits made in a test an author, whatever the
// user's own settings are (CI has none; a signing one would ask for a key).
func gitIdentity(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "t")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "t@t")
	}
}

// A new repository is found by the next scan, on main with a commit to
// branch from, and is not made over a folder that is there.
func TestInitRepo(t *testing.T) {
	gitIdentity(t)
	root := t.TempDir()
	p, err := InitRepo(root, "fresh")
	if err != nil {
		t.Fatal(err)
	}
	repos, _ := Scan(root, "worktrees")
	if len(repos) != 1 || repos[0].Path != p {
		t.Fatalf("scan found %+v, want %s", repos, p)
	}
	if b := baseRef(p); b != "main" {
		t.Fatalf("base %q, want main", b)
	}
	if _, err := InitRepo(root, "fresh"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("made over an existing folder: %v", err)
	}
	for _, bad := range []string{"", "a/b", "../up", ".hidden", "with space"} {
		if err := CheckNewRepo(root, bad); err == nil {
			t.Errorf("%q taken for a name", bad)
		}
	}
}

// A repository made or cloned in a folder inside another repository's
// work tree (a dotfiles repository in the home folder) is one of its own,
// and the one around it is left as it was.
func TestNewRepoInsideAnother(t *testing.T) {
	gitIdentity(t)
	home := t.TempDir()
	run := func(dir string, args ...string) string {
		t.Helper()
		out, err := gitRunEnv(gitDefaultTimeout, gitsafe.EnvNoRepo(), dir, args...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(out)
	}
	run(home, "init", "--quiet", "--initial-branch=main")
	run(home, "commit", "--quiet", "--allow-empty", "-m", "dotfiles")
	head := run(home, "rev-parse", "HEAD")
	parent := filepath.Join(home, "code")

	p, err := InitRepo(parent, "fresh")
	if err != nil {
		t.Fatal(err)
	}
	if !isPrimary(p) {
		t.Fatalf("%s is not a repository of its own", p)
	}
	// git names the top with the links resolved: on a Mac, /var is
	// /private/var.
	if got, want := run(p, "rev-parse", "--show-toplevel"), resolved(t, p); got != want {
		t.Fatalf("the new repository's top is %s, not %s", got, want)
	}
	if run(home, "rev-parse", "HEAD") != head {
		t.Fatal("the repository around it got a commit")
	}

	src := newRepo(t, t.TempDir(), "lib")
	c, err := CloneRepo(parent, "lib", src.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !isPrimary(c) || run(c, "rev-parse", "--show-toplevel") != resolved(t, c) {
		t.Fatalf("clone %s is not a repository of its own", c)
	}
}

func TestCloneSource(t *testing.T) {
	for _, c := range []struct {
		src, name, url string
		github         bool
	}{
		{"me/app", "app", "https://github.com/me/app.git", true},
		{"https://github.com/me/app.git", "app", "https://github.com/me/app.git", false},
		{"git@github.com:me/app.git", "app", "git@github.com:me/app.git", false},
		{"/srv/git/lib/", "lib", "/srv/git/lib/", false},
		{"./me/app", "app", "./me/app", false},
	} {
		name, url, gh, err := CloneSource(c.src)
		if err != nil || name != c.name || url != c.url || gh != c.github {
			t.Errorf("%q: %q %q %v %v", c.src, name, url, gh, err)
		}
	}
	if _, _, _, err := CloneSource("  "); err == nil {
		t.Error("nothing typed taken for a source")
	}
}

func TestCloneRepo(t *testing.T) {
	gitIdentity(t)
	src := newRepo(t, t.TempDir(), "lib")
	root := t.TempDir()
	p, err := CloneRepo(root, "lib", src.Path)
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(root, "lib") || !isPrimary(p) {
		t.Fatalf("cloned to %s", p)
	}
}

// Started inside a repository, new ones go in the first folder the config
// lists, not inside it.
func TestNewRepoDir(t *testing.T) {
	root := t.TempDir()
	if d := NewRepoDir(root, nil); d != root {
		t.Fatalf("%s, want %s", d, root)
	}
	repo := newRepo(t, root, "app").Path
	if d := NewRepoDir(repo, nil); d != "" {
		t.Fatalf("inside a repository with no repo_dirs: %s", d)
	}
	if d := NewRepoDir(repo, []string{"../code"}); d != filepath.Join(root, "code") {
		t.Fatalf("%s, want the first of repo_dirs", d)
	}
}

// resolved is path with its links resolved.
func resolved(t *testing.T, path string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
