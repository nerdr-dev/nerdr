package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseStatus(t *testing.T) {
	in := " M src/app/api/ai/route.ts\x00M  package.json\x00?? new file.ts\x00R  new.ts\x00old.ts\x00D  gone.ts\x00A  æøå.ts\x00"
	got := parseStatus(in)
	want := []FileStatus{
		{Code: " M", Path: "src/app/api/ai/route.ts"},
		{Code: "M ", Path: "package.json"},
		{Code: "??", Path: "new file.ts"},
		{Code: "R ", Path: "new.ts", OldPath: "old.ts"},
		{Code: "D ", Path: "gone.ts"},
		{Code: "A ", Path: "æøå.ts"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseStatus mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestParseWorktrees(t *testing.T) {
	in := `worktree /a/repo
HEAD abc123
branch refs/heads/main

worktree /a/repo-fix
HEAD def456
branch refs/heads/fix/bug
`
	got := parseWorktrees(in)
	want := []Worktree{
		{Path: "/a/repo", Head: "abc123", Branch: "main"},
		{Path: "/a/repo-fix", Head: "def456", Branch: "fix/bug"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseWorktrees mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestParseNameStatus(t *testing.T) {
	in := "M\x00src/a.ts\x00A\x00src/b c.ts\x00D\x00src/c.ts\x00R087\x00src/old.ts\x00src/new.ts\x00"
	got := parseNameStatus(in)
	want := []FileStatus{
		{Code: " M", Path: "src/a.ts"},
		{Code: "A ", Path: "src/b c.ts"},
		{Code: "D ", Path: "src/c.ts"},
		{Code: "R ", Path: "src/new.ts", OldPath: "src/old.ts"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseNameStatus mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

// TestChangedFilesListsUntrackedFilesNotDirectories covers a case that made
// files unopenable: by default git reports a wholly untracked directory as
// one entry ending in "/", which is not a file and cannot be read as one.
func TestChangedFilesListsUntrackedFilesNotDirectories(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "init")

	// A directory where everything inside it is untracked.
	if err := os.MkdirAll(filepath.Join(dir, "newdir", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"newdir/a.ts", "newdir/nested/b.ts"} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(p)), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files, err := ChangedFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range files {
		if strings.HasSuffix(f.Path, "/") {
			t.Fatalf("got a directory entry %q, which cannot be shown as a file", f.Path)
		}
		paths = append(paths, f.Path)
	}
	for _, want := range []string{"newdir/a.ts", "newdir/nested/b.ts"} {
		var found bool
		for _, p := range paths {
			if p == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected %q to be listed individually, got %v", want, paths)
		}
	}
}

// TestBaseComparesWithTheForkPoint covers a branch whose base has moved on:
// only the branch's own work is its change, new files not yet added
// included, and names with spaces or non-ASCII letters come through as
// they are.
func TestBaseComparesWithTheForkPoint(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "main")
	write("base.go")
	run("add", ".")
	run("commit", "-q", "-m", "base")
	run("switch", "-q", "-c", "work")
	write("my notes.md")
	run("add", ".")
	run("commit", "-q", "-m", "work")
	run("switch", "-q", "main")
	write("later.go")
	run("add", ".")
	run("commit", "-q", "-m", "main moves on")
	run("switch", "-q", "work")
	write("æøå.go")

	files, err := ChangedFilesBase(dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = f.Code
	}
	want := map[string]string{"my notes.md": "A ", "æøå.go": "??"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changed files %v, want %v", got, want)
	}
	if n, _, _ := ShortStatBase(dir, "main"); n != 2 {
		t.Fatalf("shortstat counts %d files, want the branch's 2 (one not yet added)", n)
	}

	// Everything measured through a Comparison agrees with that list.
	c := Comparison{Worktree: dir, Base: "main"}
	if d, err := c.Diff("my notes.md", false); err != nil || !strings.Contains(d, "+my notes.md") {
		t.Fatalf("diff of a listed file: %q, %v", d, err)
	}
	if d, _ := c.Diff("later.go", false); d != "" {
		t.Fatalf("main's later work showed up as this branch's change:\n%s", d)
	}
	if _, err := c.OldSource("later.go"); err == nil {
		t.Fatal("the old version should be from the fork point, before later.go existed")
	}
}

// TestShortStatCountsNewFiles covers what an agent mostly makes: files
// nobody has added yet, which git diff alone leaves out.
func TestShortStatCountsNewFiles(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "i"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("a\nb\nc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n, add, del := ShortStat(dir); n != 1 || add != 3 || del != 0 {
		t.Fatalf("got %d files +%d -%d, want 1 +3 -0", n, add, del)
	}
}

func TestNumStat(t *testing.T) {
	dir := initRepoWithCommit(t, map[string]string{"a.go": "one\ntwo\nthree\n", "old name.go": "x\n"})
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("one\n2\nthree\nfour\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("mv", "old name.go", "new name.go")
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("a\nb\nc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "img.bin"), []byte{0, 1, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	got := NumStat(dir, "HEAD")
	want := map[string]LineCount{
		"a.go":        {Add: 2, Del: 1},
		"new name.go": {},
		"new.go":      {Add: 3},
		"img.bin":     {Binary: true},
	}
	for p, w := range want {
		if got[p] != w {
			t.Errorf("%s: got %+v, want %+v", p, got[p], w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %v", got)
	}
}

func TestWorkTree(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	empty, err := WorkTree(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644)
	withNew, _ := WorkTree(dir)
	if withNew == empty {
		t.Fatal("a new file did not change the tree")
	}
	run("add", "a.txt")
	run("commit", "-qm", "a")
	if committed, _ := WorkTree(dir); committed != withNew || TreeOf(dir, "HEAD") != withNew {
		t.Fatal("the same work committed gave another tree")
	}
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("b\n"), 0o644)
	if changed, _ := WorkTree(dir); changed == withNew {
		t.Fatal("a change did not change the tree")
	}
	out, _ := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if string(out) != " M a.txt\n" {
		t.Fatalf("the worktree's own index was touched: %q", out)
	}
}
