package move

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/gitsafe"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// machines is an origin, and a clone of it on each of two machines.
func machines(t *testing.T) (origin, src, dst string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	root := t.TempDir()
	seed := filepath.Join(root, "seed")
	git(t, root, "init", "-q", "-b", "main", seed)
	for i, name := range []string{"a.txt", "b.txt", "c.txt", "gone.txt"} {
		write(t, filepath.Join(seed, name), strings.Repeat(name, i+1)+"\n")
	}
	write(t, filepath.Join(seed, ".gitignore"), ".env\nnode_modules/\n")
	git(t, seed, "add", "-A")
	git(t, seed, "commit", "-q", "-m", "first")
	origin = filepath.Join(root, "origin.git")
	git(t, root, "clone", "-q", "--bare", seed, origin)
	src = filepath.Join(root, "laptop", "proj")
	dst = filepath.Join(root, "box", "proj")
	git(t, root, "clone", "-q", origin, src)
	git(t, root, "clone", "-q", origin, dst)
	return origin, src, dst
}

// work is a tab's worktree on branch feat in src, with the probe's work in
// it: a new commit, a change staged and one not, a file deleted, a new
// file and a new folder, and files .gitignore keeps out.
func work(t *testing.T, src string) string {
	t.Helper()
	wt := filepath.Join(filepath.Dir(src), "worktrees", "feat-proj")
	git(t, src, "worktree", "add", "-q", "-b", "feat", wt)
	gitsafe.Trust(src, wt)
	write(t, filepath.Join(wt, "a.txt"), "committed change\n")
	git(t, wt, "commit", "-q", "-am", "work")
	write(t, filepath.Join(wt, "b.txt"), "staged\n")
	git(t, wt, "add", "b.txt")
	write(t, filepath.Join(wt, "b.txt"), "staged, then changed again\n")
	write(t, filepath.Join(wt, "c.txt"), "not staged\n")
	git(t, wt, "rm", "-q", "gone.txt")
	write(t, filepath.Join(wt, "new.txt"), "new\n")
	write(t, filepath.Join(wt, "dir", "deep", "x.go"), "package x\n")
	write(t, filepath.Join(wt, ".env"), "SECRET=1\n")
	write(t, filepath.Join(wt, "node_modules", "m", "i.js"), "x\n")
	return wt
}

// tree is the files under dir but .git, and what is in them.
func tree(t *testing.T, dir string, skip ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if d.Name() == ".git" || slices.Contains(skip, rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out[rel] = string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// moveOne moves the worktree at wt in src to dstWt in dst, as a core on
// each side would, and gives the pack's size.
func moveOne(t *testing.T, src, wt, dst, dstWt string) int64 {
	t.Helper()
	p, err := Snapshot(wt)
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	if err := Bundle(wt, &p, Have(dst, p.Branch), filepath.Join(tmp, "proj.bundle")); err != nil {
		t.Fatal(err)
	}
	var files [][2]string
	if p.Bundle != "" {
		files = append(files, [2]string{p.Bundle, filepath.Join(tmp, p.Bundle)})
	}
	pack := filepath.Join(tmp, "pack.tar")
	size, sum, err := WritePack(pack, files)
	if err != nil {
		t.Fatal(err)
	}
	in := t.TempDir()
	if _, err := ReadPack(pack, sum, in); err != nil {
		t.Fatal(err)
	}
	if err := Land(dst, dstWt, p, filepath.Join(in, p.Bundle)); err != nil {
		t.Fatal(err)
	}
	return size
}

func TestMoveCarriesTheWorkAndLeavesTheSource(t *testing.T) {
	_, src, dst := machines(t)
	wt := work(t, src)
	statusBefore := git(t, wt, "status", "--porcelain")
	gitDir, _, _ := gitsafe.Dirs(wt)
	indexBefore, _ := os.ReadFile(filepath.Join(gitDir, "index"))
	filesBefore := tree(t, wt)

	l := LookAt(wt)
	if l.Refuse != "" || l.Branch != "feat" {
		t.Fatalf("look: %+v", l)
	}
	if !slices.Contains(l.New, "new.txt") || !slices.Contains(l.New, "dir/deep/x.go") {
		t.Errorf("new files: %v", l.New)
	}
	if !slices.Contains(l.Ignored, ".env") || !slices.Contains(l.Ignored, "node_modules/") {
		t.Errorf("ignored: %v", l.Ignored)
	}

	dstWt := filepath.Join(filepath.Dir(dst), "worktrees", "feat-proj")
	size := moveOne(t, src, wt, dst, dstWt)
	// Only the new: the probe's bundle was 836 bytes.
	if size > 8<<10 {
		t.Errorf("the pack is %d bytes; it should carry only what box lacks", size)
	}

	// The source is as it was, its index too.
	if s := git(t, wt, "status", "--porcelain"); s != statusBefore {
		t.Errorf("source status changed:\n%s\nwas\n%s", s, statusBefore)
	}
	if b, _ := os.ReadFile(filepath.Join(gitDir, "index")); !bytes.Equal(b, indexBefore) {
		t.Error("the source's index changed")
	}
	if got := tree(t, wt); !mapsEqual(got, filesBefore) {
		t.Error("the source's files changed")
	}
	if refs := git(t, src, "for-each-ref", "refs/nerdr-move/"); refs != "" {
		t.Errorf("refs left on the source: %s", refs)
	}

	// The target has the same work, staged as it was, without the ignored.
	if s := git(t, dstWt, "status", "--porcelain"); s != statusBefore {
		t.Errorf("target status:\n%s\nsource's:\n%s", s, statusBefore)
	}
	want := tree(t, wt, ".env", "node_modules")
	if got := tree(t, dstWt); !mapsEqual(got, want) {
		t.Errorf("target files:\n%v\nwant\n%v", got, want)
	}
	if git(t, dstWt, "rev-parse", "HEAD") != git(t, wt, "rev-parse", "HEAD") {
		t.Error("the branch is not at the same commit")
	}
	if refs := git(t, dst, "for-each-ref", "refs/nerdr-move/"); refs != "" {
		t.Errorf("refs left on the target: %s", refs)
	}
}

func TestMoveCleanBranchTheTargetHas(t *testing.T) {
	origin, src, dst := machines(t)
	wt := filepath.Join(filepath.Dir(src), "worktrees", "feat-proj")
	git(t, src, "worktree", "add", "-q", "-b", "feat", wt)
	gitsafe.Trust(src, wt)
	write(t, filepath.Join(wt, "a.txt"), "pushed\n")
	git(t, wt, "commit", "-q", "-am", "pushed")
	git(t, wt, "push", "-q", origin, "feat")
	git(t, dst, "fetch", "-q", "origin")

	p, err := Snapshot(wt)
	if err != nil || p.Wip != "" {
		t.Fatalf("snapshot of a clean worktree: %+v %v", p, err)
	}
	if err := Bundle(wt, &p, Have(dst, "feat"), filepath.Join(t.TempDir(), "x.bundle")); err != nil || p.Bundle != "" {
		t.Fatalf("a bundle of nothing box lacks: %q %v", p.Bundle, err)
	}
	dstWt := filepath.Join(filepath.Dir(dst), "worktrees", "feat-proj")
	if err := Land(dst, dstWt, p, ""); err != nil {
		t.Fatal(err)
	}
	if got := tree(t, dstWt); !mapsEqual(got, tree(t, wt)) {
		t.Error("files differ")
	}
}

// TestMoveNewBranchWithChangesOnly: a branch with no commit of its own
// yet, only changes not committed, which the bundle carries without the
// branch, whose commit the target has.
func TestMoveNewBranchWithChangesOnly(t *testing.T) {
	_, src, dst := machines(t)
	wt := filepath.Join(filepath.Dir(src), "worktrees", "fresh-proj")
	git(t, src, "worktree", "add", "-q", "-b", "fresh", wt)
	gitsafe.Trust(src, wt)
	write(t, filepath.Join(wt, "new.txt"), "new\n")
	status := git(t, wt, "status", "--porcelain")
	dstWt := filepath.Join(filepath.Dir(dst), "worktrees", "fresh-proj")
	moveOne(t, src, wt, dst, dstWt)
	if got := git(t, dstWt, "status", "--porcelain"); got != status {
		t.Errorf("target status %q, want %q", got, status)
	}
	if got := tree(t, dstWt); !mapsEqual(got, tree(t, wt)) {
		t.Error("files differ")
	}
}

func TestMoveRefusesABranchThatWentItsOwnWay(t *testing.T) {
	_, src, dst := machines(t)
	wt := work(t, src)
	git(t, dst, "branch", "feat")
	write(t, filepath.Join(dst, "a.txt"), "box's own\n")
	git(t, dst, "stash", "-q")
	other := filepath.Join(filepath.Dir(dst), "other")
	git(t, dst, "worktree", "add", "-q", other, "feat")
	p, err := Snapshot(wt)
	if err != nil {
		t.Fatal(err)
	}
	if why := CanLand(dst, p); !strings.Contains(why, "checked out") {
		t.Errorf("checked out there: %q", why)
	}
	git(t, dst, "worktree", "remove", other)
	write(t, filepath.Join(dst, "z.txt"), "z\n")
	git(t, dst, "checkout", "-q", "feat")
	git(t, dst, "add", "z.txt")
	git(t, dst, "commit", "-q", "-m", "box's own")
	git(t, dst, "checkout", "-q", "main")
	tmp := t.TempDir()
	if err := Bundle(wt, &p, Have(dst, "feat"), filepath.Join(tmp, "b.bundle")); err != nil {
		t.Fatal(err)
	}
	err = Land(dst, filepath.Join(tmp, "wt"), p, filepath.Join(tmp, "b.bundle"))
	if err == nil || !strings.Contains(err.Error(), "commits this one has not") {
		t.Errorf("landing on a branch of box's own: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "wt")); err == nil {
		t.Error("a worktree was made anyway")
	}
}

func TestMoveRefusesSubmodulesAndLFS(t *testing.T) {
	_, src, _ := machines(t)
	wt := work(t, src)
	write(t, filepath.Join(wt, ".gitattributes"), "*.bin filter=lfs diff=lfs merge=lfs -text\n")
	if l := LookAt(wt); !strings.Contains(l.Refuse, "git-lfs") {
		t.Errorf("lfs: %q", l.Refuse)
	}
	os.Remove(filepath.Join(wt, ".gitattributes"))
	write(t, filepath.Join(wt, ".gitmodules"), "[submodule \"x\"]\n")
	if l := LookAt(wt); !strings.Contains(l.Refuse, "submodules") {
		t.Errorf("submodules: %q", l.Refuse)
	}
}

func TestPackRefusesWhatIsNotItsOwn(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f")
	write(t, f, "x")
	if _, _, err := WritePack(filepath.Join(dir, "p1"), [][2]string{{"../evil", f}}); err == nil {
		t.Error("a name with a folder was packed")
	}
	pack := filepath.Join(dir, "p2")
	_, sum, err := WritePack(pack, [][2]string{{"manifest.json", f}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPack(pack, strings.Repeat("0", 64), t.TempDir()); err == nil {
		t.Error("a pack with the wrong sum was read")
	}
	if names, err := ReadPack(pack, sum, t.TempDir()); err != nil || len(names) != 1 {
		t.Errorf("read %v %v", names, err)
	}
}

func TestOriginKey(t *testing.T) {
	for _, u := range []string{"git@github.com:Nerdr-Dev/nerdr.git", "https://github.com/nerdr-dev/nerdr", "ssh://git@github.com:22/nerdr-dev/nerdr.git", "https://user@github.com/nerdr-dev/nerdr.git/"} {
		if k := OriginKey(u); k != "github.com/nerdr-dev/nerdr" {
			t.Errorf("%s: %s", u, k)
		}
	}
	if OriginKey("/srv/git/x.git") != "/srv/git/x" {
		t.Error(OriginKey("/srv/git/x.git"))
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func TestSweepTakesOnlyOldMoves(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	old, err := os.MkdirTemp("", "nerdr-move-out-")
	if err != nil {
		t.Fatal(err)
	}
	young, _ := os.MkdirTemp("", "nerdr-move-in-")
	other, _ := os.MkdirTemp("", "something-")
	past := time.Now().Add(-7 * time.Hour)
	os.Chtimes(old, past, past)
	os.Chtimes(other, past, past)
	Sweep(6 * time.Hour)
	if _, err := os.Stat(old); err == nil {
		t.Error("an old move's folder was left")
	}
	for _, d := range []string{young, other} {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("%s removed", d)
		}
	}
}

// TestMoveLandFailingLateLeavesNothing: a landing that fails once the
// worktree is added takes it away again, and the branch, so a second try
// finds the way clear.
func TestMoveLandFailingLateLeavesNothing(t *testing.T) {
	_, src, dst := machines(t)
	wt := work(t, src)
	p, err := Snapshot(wt)
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	bundle := filepath.Join(tmp, "b.bundle")
	if err := Bundle(wt, &p, Have(dst, p.Branch), bundle); err != nil {
		t.Fatal(err)
	}
	dstWt := filepath.Join(tmp, "wt")
	afterAdd = func(string) error { return errors.New("disk full") }
	err = Land(dst, dstWt, p, bundle)
	afterAdd = nil
	if err == nil {
		t.Fatal("landed")
	}
	if _, err := os.Stat(dstWt); err == nil {
		t.Error("the worktree was left")
	}
	if Tip(dst, p.Branch) != "" {
		t.Error("the branch was left")
	}
	if out := git(t, dst, "worktree", "list"); strings.Contains(out, dstWt) {
		t.Errorf("git still lists it:\n%s", out)
	}
	if err := Land(dst, dstWt, p, bundle); err != nil {
		t.Fatalf("the second try: %v", err)
	}
}
