package move

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundleCommitsCarriesOnlyThemToAnotherClone(t *testing.T) {
	_, src, dst := machines(t)
	// Main moves on, on origin and in src: dst gets it with a fetch, as the
	// new branch is made, and the bundle leaves it out.
	write(t, filepath.Join(src, "main.txt"), "on main\n")
	git(t, src, "add", "-A")
	git(t, src, "commit", "-q", "-m", "main moves on")
	git(t, src, "push", "-q", "origin", "main")
	git(t, src, "switch", "-q", "-c", "feat")
	write(t, filepath.Join(src, "one.txt"), "one\n")
	git(t, src, "add", "-A")
	git(t, src, "commit", "-q", "-m", "one")
	write(t, filepath.Join(src, "two.txt"), "two\n")
	git(t, src, "add", "-A")
	git(t, src, "commit", "-q", "-m", "two")
	two := strings.TrimSpace(git(t, src, "rev-parse", "HEAD"))
	commits := FullCommits(src, []string{two[:7], "nope"})
	if len(commits) != 1 || commits[0] != two {
		t.Fatalf("FullCommits = %v, want [%s]", commits, two)
	}
	bundle := filepath.Join(t.TempDir(), "c.bundle")
	if err := BundleCommits(src, commits, bundle); err != nil {
		t.Fatal(err)
	}
	if refs := git(t, src, "for-each-ref", "refs/nerdr-spinoff/"); refs != "" {
		t.Fatalf("refs left behind: %s", refs)
	}
	// Without main's new commit, dst cannot take it.
	if err := UnbundleCommits(dst, bundle, commits); err == nil {
		t.Fatal("unbundled without what it builds on")
	}
	git(t, dst, "fetch", "-q", "origin", "main")
	if err := UnbundleCommits(dst, bundle, commits); err != nil {
		t.Fatal(err)
	}
	git(t, dst, "cat-file", "-e", two+"^{commit}")
	if refs := git(t, dst, "for-each-ref", "refs/nerdr-spinoff/"); refs != "" {
		t.Fatalf("unbundle made refs: %s", refs)
	}
	// It names no other commit than those it was asked for.
	other := strings.TrimSpace(git(t, src, "rev-parse", "HEAD~1"))
	if err := UnbundleCommits(dst, bundle, []string{other}); err == nil {
		t.Fatal("took a commit the bundle does not name")
	}
	if _, err := os.Stat(bundle); err != nil {
		t.Fatal(err)
	}
}
