package order

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitRepo makes a repository with a file to describe.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"pkg/a.go", "pkg/b.go"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("package pkg\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q", "-b", "work"}, {"add", "."},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestAddAndLoadFromAnywhereInTheWorktree(t *testing.T) {
	dir := gitRepo(t)

	if _, err := Add(filepath.Join(dir, "pkg"), Entry{File: "a.go", Role: RoleCore, Short: "the new parser"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(dir, Entry{Short: "parse the config once, at startup"}); err != nil {
		t.Fatal(err)
	}

	ix := Load(dir, "work")
	if ix.Summary() != "parse the config once, at startup" {
		t.Fatalf("summary %q", ix.Summary())
	}
	e, ok := ix.For("pkg/a.go")
	if !ok || e.Role != RoleCore || e.Branch != "work" {
		t.Fatalf("entry %+v ok=%v", e, ok)
	}
}

func TestAddRefusesWhatWouldNeverBeShown(t *testing.T) {
	dir := gitRepo(t)
	for name, e := range map[string]Entry{
		"unknown role": {File: "pkg/a.go", Role: "misc", Short: "x"},
		"no role":      {File: "pkg/a.go", Short: "x"},
		"no text":      {File: "pkg/a.go", Role: RoleCore},
		"no such file": {File: "pkg/c.go", Role: RoleCore, Short: "x"},
		"role alone":   {Role: RoleCore, Short: "x"},
	} {
		if _, err := Add(dir, e); err == nil {
			t.Errorf("%s: expected a refusal", name)
		}
	}
	if Load(dir, "work").Read() != 0 {
		t.Fatal("a refused entry must not be written")
	}
}

// TestLaterEntryWinsAndSequenceHolds covers an agent carrying on with work
// it has already described: it appends, and the file keeps its place in the
// sequence rather than jumping to the end.
func TestLaterEntryWinsAndSequenceHolds(t *testing.T) {
	dir := gitRepo(t)
	for _, e := range []Entry{
		{File: "pkg/a.go", Role: RoleCore, Short: "first thought"},
		{File: "pkg/b.go", Role: RoleMechanical, Short: "call sites"},
		{File: "pkg/a.go", Role: RoleCore, Short: "what it really is"},
	} {
		if _, err := Add(dir, e); err != nil {
			t.Fatal(err)
		}
	}
	ix := Load(dir, "work")
	if e, _ := ix.For("pkg/a.go"); e.Short != "what it really is" {
		t.Fatalf("expected the later entry to win, got %q", e.Short)
	}
	if ix.Len() != 2 || ix.Read() != 3 {
		t.Fatalf("len %d read %d", ix.Len(), ix.Read())
	}
	if got := strings.Join(ix.Files(), " "); got != "pkg/a.go pkg/b.go" {
		t.Fatalf("sequence %q — core comes before mechanical", got)
	}
}

// TestOtherBranchesAreLeftOut covers a reading order describing one change:
// the file is shared by every worktree, and another branch's order would
// describe code that isn't here.
func TestOtherBranchesAreLeftOut(t *testing.T) {
	dir := gitRepo(t)
	if _, err := Add(dir, Entry{File: "pkg/a.go", Role: RoleCore, Short: "on work", Branch: "work"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(dir, Entry{File: "pkg/b.go", Role: RoleCore, Short: "on other", Branch: "other"}); err != nil {
		t.Fatal(err)
	}
	ix := Load(dir, "work")
	if _, ok := ix.For("pkg/b.go"); ok {
		t.Fatal("another branch's entry must not be shown")
	}
	if _, ok := ix.For("pkg/a.go"); !ok {
		t.Fatal("this branch's entry is missing")
	}
}

// TestDeletedFileCanBeDescribed covers a change that removes a file: the
// file is still listed as changed, so it has to be describable.
func TestDeletedFileCanBeDescribed(t *testing.T) {
	dir := gitRepo(t)
	if err := os.Remove(filepath.Join(dir, "pkg", "b.go")); err != nil {
		t.Fatal(err)
	}
	e, err := Add(filepath.Join(dir, "pkg"), Entry{File: "b.go", Role: RoleMechanical, Short: "folded into a.go"})
	if err != nil {
		t.Fatal(err)
	}
	if e.File != "pkg/b.go" {
		t.Fatalf("file %q", e.File)
	}
	if _, err := Add(dir, Entry{File: "pkg/never.go", Role: RoleCore, Short: "x"}); err == nil {
		t.Fatal("a file git never knew must still be refused")
	}
}

// TestDetachedHeadKeepsToItself covers the branch that has no name: it
// must neither see every branch's order nor show up on every branch.
func TestDetachedHeadKeepsToItself(t *testing.T) {
	dir := gitRepo(t)
	path := File(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"file":"pkg/a.go","role":"core","short":"on work","branch":"work"}
{"file":"pkg/b.go","role":"core","short":"detached"}
`
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	if ix := Load(dir, ""); ix.Len() != 1 || ix.Files()[0] != "pkg/b.go" {
		t.Fatalf("detached HEAD sees %v", ix.Files())
	}
	if ix := Load(dir, "work"); ix.Len() != 1 || ix.Files()[0] != "pkg/a.go" {
		t.Fatalf("work sees %v", ix.Files())
	}
}

// git runs a git command in dir for a test.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestBranchMadeAgainStartsClean covers a branch name used again after the
// first branch was merged and deleted: what was said about the old change
// must not show up as the summary of the new one.
func TestBranchMadeAgainStartsClean(t *testing.T) {
	dir := gitRepo(t)
	git(t, dir, "branch", "again")
	old := time.Now().Add(-48 * time.Hour).Format(timeLayout)
	for _, e := range []Entry{
		{Short: "the old change", Branch: "again", Time: old},
		{File: "pkg/a.go", Role: RoleCore, Short: "old reason", Branch: "again", Time: old},
	} {
		if _, err := Add(dir, e); err != nil {
			t.Fatal(err)
		}
	}
	if Load(dir, "again").Summary() != "" {
		t.Fatal("an entry older than the branch must not be shown")
	}

	// Written now, in the minute the branch was made: shown, even though
	// the time is only to the minute.
	if _, err := Add(dir, Entry{File: "pkg/b.go", Role: RoleCore, Short: "new", Branch: "again"}); err != nil {
		t.Fatal(err)
	}
	if ix := Load(dir, "again"); ix.Len() != 1 || ix.Files()[0] != "pkg/b.go" {
		t.Fatalf("again sees %v", ix.Files())
	}
}

// TestPruneKeepsWhatCanStillBeShown covers clearing out the entries no
// branch will show again, and only those.
func TestPruneKeepsWhatCanStillBeShown(t *testing.T) {
	dir := gitRepo(t)
	git(t, dir, "branch", "again")
	path := File(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour).Format(timeLayout)
	now := time.Now().Format(timeLayout)
	lines := `{"short":"work's change","branch":"work","ts":"` + now + `"}
{"short":"merged and deleted","branch":"gone","ts":"` + now + `"}
{"short":"before the name was used again","branch":"again","ts":"` + old + `"}
{"short":"again's change","branch":"again","ts":"` + now + `"}
{"short":"detached"}
not json
`
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := Prune(dir)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	if n != 2 || strings.Contains(got, "merged and deleted") || strings.Contains(got, "before the name") {
		t.Fatalf("dropped %d, left:\n%s", n, got)
	}
	for _, keep := range []string{"work's change", "again's change", "detached", "not json"} {
		if !strings.Contains(got, keep) {
			t.Errorf("%q was dropped:\n%s", keep, got)
		}
	}

	// What is appended afterwards lands in the file that is read.
	if _, err := Add(dir, Entry{Short: "after", Branch: "work"}); err != nil {
		t.Fatal(err)
	}
	if Load(dir, "work").Summary() != "after" {
		t.Fatal("an append after the prune was lost")
	}
}
