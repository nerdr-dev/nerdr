// Package workspace finds the repositories nerdr can work on and creates
// and removes the git worktrees each tab runs in.
//
// The worktrees go next to the repositories:
//
//	<root>/worktrees/<branch>            one repository
//	<root>/worktrees/<branch>/<repo>     several, the agent starting in <branch>
package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nerdr-dev/nerdr/internal/denied"
	gitdata "github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/proc"
)

// Repo is a primary git repository found under the root.
type Repo struct {
	Name string
	Path string
}

// Worktree is one repository's checkout for a tab.
type Worktree struct {
	Repo   string `json:"repo"`   // the primary repository's path
	Path   string `json:"path"`   // the worktree's path
	Branch string `json:"branch"` // the branch it has checked out
	// Origin is where a push to origin went when nerdr took the worktree
	// on, "" when it had no origin. Push goes nowhere else: see Push.
	Origin string `json:"origin,omitempty"`
	// Pushes is every URL a push to origin went to then, Origin first:
	// git pushes to all of origin's push URLs. Empty in a tab kept before
	// nerdr recorded them, where Origin alone is known.
	Pushes []string `json:"pushes,omitempty"`
}

// Scan returns the git repositories directly under root, and root itself
// when it is one. The worktrees folder is skipped: what is in it are
// checkouts of these repositories, not repositories of their own.
func Scan(root, worktreesDir string) ([]Repo, error) {
	var repos []Repo
	if isPrimary(root) {
		repos = append(repos, Repo{Name: filepath.Base(root), Path: root})
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return repos, err
	}
	wtAbs := absUnder(root, worktreesDir)
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(root, e.Name())
		if p == wtAbs {
			continue
		}
		if isPrimary(p) {
			repos = append(repos, Repo{Name: e.Name(), Path: p})
		}
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Name < repos[j].Name })
	return repos, nil
}

// ScanAll finds the repositories in root, as Scan does, and in each of the
// folders dirs (relative ones taken from root): a folder that is a
// repository, and those right under it. A repository found twice is
// listed once.
func ScanAll(root, worktreesDir string, dirs []string) ([]Repo, error) {
	repos, err := Scan(root, worktreesDir)
	seen := map[string]bool{}
	for _, r := range repos {
		seen[r.Path] = true
	}
	for _, d := range dirs {
		more, derr := Scan(absUnder(root, d), "")
		if derr != nil && err == nil {
			err = derr
		}
		for _, r := range more {
			if !seen[r.Path] {
				seen[r.Path] = true
				repos = append(repos, r)
			}
		}
	}
	sort.Slice(repos, func(i, j int) bool { return nameLess(repos[i].Name, repos[j].Name) })
	return repos, err
}

// nameLess orders names alphabetically, App beside app rather than before
// all the lower-case ones.
func nameLess(a, b string) bool {
	if la, lb := strings.ToLower(a), strings.ToLower(b); la != lb {
		return la < lb
	}
	return a < b
}

// isPrimary reports whether p is a repository's main checkout: its .git is
// a directory. A linked worktree's .git is a file.
func isPrimary(p string) bool {
	st, err := os.Stat(filepath.Join(p, ".git"))
	return err == nil && st.IsDir()
}

func absUnder(root, dir string) string {
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir)
	}
	return filepath.Join(root, dir)
}

// WorktreesRoot is where worktrees are created for a session rooted at root.
// When nerdr is started inside a repository, root is that repository and
// the worktrees go beside it rather than inside it.
func WorktreesRoot(root, worktreesDir string) string {
	if filepath.IsAbs(worktreesDir) {
		return worktreesDir
	}
	if isPrimary(root) {
		return filepath.Join(filepath.Dir(root), worktreesDir)
	}
	return filepath.Join(root, worktreesDir)
}

var branchOK = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// ValidBranch checks a branch name typed by the user, with git's own rules
// as the final word.
func ValidBranch(name string) error {
	if name == "" {
		return errors.New("branch name is empty")
	}
	if !branchOK.MatchString(name) {
		return errors.New("use letters, digits, '.', '_', '-' and '/'")
	}
	if err := proc.Run(proc.Detach(exec.Command("git", "check-ref-format", "--branch", name))); err != nil {
		return fmt.Errorf("%q is not a valid branch name", name)
	}
	return nil
}

// Plan is the set of worktrees a new tab will get, worked out before
// anything is created so a problem in one repository leaves nothing half
// made in the others.
type Plan struct {
	Branch string
	Dir    string // where the agent starts
	Items  []PlanItem
	// Unfetched has Sync start new branches from origin's base branch as
	// it was last fetched, without fetching it: for when the fetch failed
	// and you would rather start from that than not at all.
	Unfetched bool
	// Sandboxed says a sandboxed agent asked for the plan (nerdr tab new,
	// a spinoff). Where Sync could not pin the base to a commit origin
	// gave (see PlanItem.Pinned) the base is a ref such an agent can move,
	// so its code is not taken for the base's and setup is not run there
	// (see SetupIn), nor a hook from inside the new worktree.
	Sandboxed bool
}

// PlanItem is one repository's part of a Plan.
type PlanItem struct {
	Repo Repo
	Path string
	// How the branch is obtained: "new" from the base, "local" an existing
	// local branch, "remote" tracking origin's branch of that name.
	Mode string
	// Base is the ref a new branch starts from, or would, for "remote"
	// and "local". It is the name shown to the user (Describe); what a new
	// branch is made from is Pinned when Sync set it.
	Base string
	// Pinned is, for "new", the commit Sync had origin itself name for its
	// base branch, which the new branch is made from in place of Base. A
	// sandboxed agent can write the main checkout's refs, and with them
	// refs/remotes/origin/main, between the fetch and the worktree being
	// made; a commit id it cannot change. "" when there was no origin to
	// ask, or the plan is Unfetched or Stacked.
	Pinned string
	// Ahead is, for "remote" and "local", how many commits the existing
	// branch has that Base has not: 0 when it has been merged with a merge
	// or fast-forward. A squash-merged branch still counts its commits;
	// the pull request (PR, PRState) is what tells that one apart. Behind
	// is how many Base has that the branch has not.
	Ahead, Behind int
	PR            int
	PRState       string // OPEN, CLOSED, MERGED; "" when there is none or no gh
	// PRHasTip is, for "local", whether the pull request's head has every
	// commit the local branch has: a merged pull request says nothing of
	// commits made on the branch after it was merged. AfterPR is how many
	// it lacks, -1 when git does not have the head to count against.
	PRHasTip bool
	AfterPR  int
	// Tip is, for "local", the commit the existing branch is at.
	Tip string
	// Replaces is, for "new", the commit of a merged local branch of the
	// same name that the new branch takes the place of; see FromBase.
	Replaces string
	// Stacked is set, for "new", when Base is another tab's local branch
	// the new one starts on top of, with whatever it has not pushed; see
	// MakePlanOn.
	Stacked bool
}

// Merged says the existing branch was merged: its pull request was, with
// all a local branch has in it, or, with no open one, the branch holds
// nothing that is not in Base.
func (it PlanItem) Merged() bool {
	if it.PRState == "MERGED" && (it.Mode != "local" || it.PRHasTip) {
		return true
	}
	return it.Ahead == 0 && it.PRState != "OPEN"
}

// SetPR records the branch's pull request, and for a local branch whether
// the commit the pull request's head was at has all of the branch's.
func (it *PlanItem) SetPR(number int, state, head string) {
	it.PR, it.PRState = number, state
	it.PRHasTip, it.AfterPR = false, -1
	if it.Mode != "local" || it.Tip == "" || head == "" {
		return
	}
	if head == it.Tip {
		it.PRHasTip, it.AfterPR = true, 0
		return
	}
	// Not revCount, which takes a failure for none: here that would call
	// the branch safe to replace.
	out, err := git(it.Repo.Path, "rev-list", "--count", head+"^{commit}.."+it.Tip)
	if err != nil {
		return
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return
	}
	it.AfterPR, it.PRHasTip = n, n == 0
}

// CanStartAnew says whether a new branch from the base can be made here.
// Only a local branch stands in the way: the new one would take its name,
// and with it whatever the old one has that is not merged.
func (it PlanItem) CanStartAnew() bool {
	return it.Mode != "local" || it.Base != "" && it.Tip != "" && it.Merged()
}

// MakePlan checks every repository and decides how its worktree will be made.
func MakePlan(wtRoot, branch string, repos []Repo) (Plan, error) {
	return MakePlanOn(wtRoot, branch, repos, "")
}

// MakePlanOn is MakePlan with a new branch started on top of the local
// branch on, another tab's, rather than from main: a stacked branch, whose
// work and pull request are against on's. Only for one repository.
func MakePlanOn(wtRoot, branch string, repos []Repo, on string) (Plan, error) {
	p := Plan{Branch: branch}
	if on != "" && len(repos) != 1 {
		return p, errors.New("a branch can be stacked on another in one repository only")
	}
	p.Dir = filepath.Join(wtRoot, dirName(branch, repos))
	if _, err := os.Stat(p.Dir); err == nil {
		return p, fmt.Errorf("%s already exists", p.Dir)
	}
	for _, r := range repos {
		path := p.Dir
		if len(repos) > 1 {
			path = filepath.Join(p.Dir, r.Name)
		}
		it, err := planItem(r, path, branch, on)
		if err != nil {
			return p, err
		}
		p.Items = append(p.Items, it)
	}
	return p, nil
}

// PlanAdd plans the worktrees on branch of repos to join a tab's in dir,
// its folder, each in dir under its name: the layout of a tab over several
// repositories, which a tab with one has to be moved to first (see Nest).
// An existing branch of the name is continued, as the tab's work goes on
// in it.
func PlanAdd(dir, branch string, repos []Repo) (Plan, error) {
	p := Plan{Branch: branch, Dir: dir}
	for _, r := range repos {
		path := filepath.Join(dir, r.Name)
		if _, err := os.Lstat(path); err == nil {
			return p, fmt.Errorf("%s already exists", path)
		}
		it, err := planItem(r, path, branch, "")
		if err != nil {
			return p, err
		}
		p.Items = append(p.Items, it)
	}
	return p, nil
}

// planItem decides how repository r's worktree on branch will be made at
// path, a new branch starting on on when that is given.
func planItem(r Repo, path, branch, on string) (PlanItem, error) {
	it := PlanItem{Repo: r, Path: path}
	if out, err := git(r.Path, "worktree", "list", "--porcelain"); err == nil &&
		strings.Contains(out, "branch refs/heads/"+branch+"\n") {
		return it, fmt.Errorf("%s: branch %s is already checked out in another worktree", r.Name, branch)
	}
	switch {
	case refExists(r.Path, "refs/heads/"+branch):
		it.Mode = "local"
	case remoteBranch(r.Path, branch):
		it.Mode = "remote"
	default:
		it.Mode = "new"
	}
	if it.Mode == "local" {
		// Counted against the base as it is now, as for "remote".
		fetchBase(r.Path)
		if tip, err := git(r.Path, "rev-parse", "--verify", "refs/heads/"+branch); err == nil {
			it.Tip = strings.TrimSpace(tip)
		}
	}
	it.Base = defaultBase(r.Path)
	if on != "" && it.Mode == "new" {
		if !refExists(r.Path, "refs/heads/"+on) {
			return it, fmt.Errorf("%s: there is no branch %s to start on", r.Name, on)
		}
		it.Base, it.Stacked = on, true
	}
	if it.Base == "" && it.Mode != "local" {
		return it, fmt.Errorf("%s: found no main or master branch to start from", r.Name)
	}
	if it.Base != "" && it.Mode != "new" {
		ref := "origin/" + branch
		if it.Mode == "local" {
			ref = "refs/heads/" + branch
		}
		it.Ahead = revCount(r.Path, it.Base+".."+ref)
		it.Behind = revCount(r.Path, ref+".."+it.Base)
	}
	return it, nil
}

// remoteBranch says whether origin has branch, asking origin itself: a
// branch pushed since the last fetch has no copy in refs/remotes yet, and
// one deleted on GitHub stays there until a fetch with --prune — taking
// the local copy's word would start a new branch of the name of one
// origin has, or offer to continue one that is gone. The base is fetched
// along with it, so what the branch has that the base has not is counted
// against the base as it is now.
func remoteBranch(repo, branch string) bool {
	ref := "refs/remotes/origin/" + branch
	known := refExists(repo, ref)
	if !known && !hasRemote(repo, "origin") {
		return false
	}
	refspecs := append([]string{"+refs/heads/" + branch + ":" + ref}, baseRefspec(repo)...)
	_, err := gitOrigin(repo, append([]string{"fetch", "--quiet", "origin"}, refspecs...)...)
	switch {
	case err == nil:
		return true
	case strings.Contains(err.Error(), "couldn't find remote ref"):
		if known {
			_, _ = git(repo, "update-ref", "-d", ref)
		}
		return false
	}
	// Any other failure (offline, say): the local copy is the best guess.
	return known
}

// baseRefspec is what to fetch to bring origin's main (or master) up to
// date, nothing when the base is not origin's.
func baseRefspec(repo string) []string {
	if b := strings.TrimPrefix(defaultBase(repo), "origin/"); b != "" && b != defaultBase(repo) {
		return []string{"+refs/heads/" + b + ":refs/remotes/origin/" + b}
	}
	return nil
}

// fetchBase brings origin's main (or master) up to date, as well as it can:
// offline, the copy there is stays the best guess.
func fetchBase(repo string) {
	if rs := baseRefspec(repo); rs != nil {
		_, _ = gitOrigin(repo, append([]string{"fetch", "--quiet", "origin"}, rs...)...)
	}
}

func revCount(repo, rng string) int {
	n, err := git(repo, "rev-list", "--count", rng)
	if err != nil {
		return 0
	}
	c, _ := strconv.Atoi(strings.TrimSpace(n))
	return c
}

// ExistingItems are the repositories where the branch already exists,
// locally or on origin.
func (p Plan) ExistingItems() []PlanItem {
	var out []PlanItem
	for _, it := range p.Items {
		if it.Mode == "remote" || it.Mode == "local" {
			out = append(out, it)
		}
	}
	return out
}

// CanStartAnew says whether FromBase can make every branch new: no local
// branch of the name has work that is not merged.
func (p Plan) CanStartAnew() bool {
	for _, it := range p.Items {
		if !it.CanStartAnew() {
			return false
		}
	}
	return true
}

// FromBase is the plan with every existing branch made new from the base
// instead. Origin's is left alone; a local one, which the new branch takes
// the name of, is replaced only when it is merged (see CanStartAnew), so
// nothing is lost, and is otherwise kept and continued.
func (p Plan) FromBase() Plan {
	items := make([]PlanItem, len(p.Items))
	copy(items, p.Items)
	for i := range items {
		switch {
		case items[i].Mode == "remote":
			items[i].Mode = "new"
		case items[i].Mode == "local" && items[i].CanStartAnew():
			items[i].Mode, items[i].Replaces = "new", items[i].Tip
		}
	}
	p.Items = items
	return p
}

// dirName is the directory a plan's worktrees go in: the branch with the
// first repository's name after it, so the same branch name in other
// repositories (fix-sonar in several apps at once) gets a directory of its
// own. A branch already ending in the name is not given it twice. With
// more repositories, not all their names, which made a directory name too
// long with many, but how many more and a hash of the names, which keeps
// apart other sets that start with the same one; each has its own
// directory inside, under its name.
func dirName(branch string, repos []Repo) string {
	name := strings.ReplaceAll(branch, "/", "-")
	if len(repos) == 0 {
		return name
	}
	if !strings.HasSuffix(name, "-"+repos[0].Name) {
		name += "-" + repos[0].Name
	}
	if len(repos) == 1 {
		return name
	}
	names := make([]string, len(repos))
	for i, r := range repos {
		names[i] = r.Name
	}
	sort.Strings(names)
	sum := sha256.Sum256([]byte(strings.Join(names, "\n")))
	return fmt.Sprintf("%s-and-%d-more-%x", name, len(repos)-1, sum[:3])
}

// DirFor is the folder MakePlan puts the worktrees on branch of repos
// in, under wtRoot.
func DirFor(wtRoot, branch string, repos []Repo) string {
	return filepath.Join(wtRoot, dirName(branch, repos))
}

// Describe says, per repository, what Execute will do.
func (p Plan) Describe() []string {
	var out []string
	for _, it := range p.Items {
		switch it.Mode {
		case "local":
			out = append(out, fmt.Sprintf("%s: existing branch %s", it.Repo.Name, p.Branch))
		case "remote":
			out = append(out, fmt.Sprintf("%s: origin/%s", it.Repo.Name, p.Branch))
		case "new":
			if it.Replaces != "" {
				out = append(out, fmt.Sprintf("%s: new branch from %s, in place of the merged local one (%s)", it.Repo.Name, it.Base, short(it.Replaces)))
				continue
			}
			fallthrough
		default:
			out = append(out, fmt.Sprintf("%s: new branch from %s", it.Repo.Name, it.Base))
		}
	}
	return out
}

// Sync brings each repository's base branch up to date with origin before a
// new branch is made from it: it asks origin which commit its main (or
// master) is at, fetches it, pins the new branch to that commit (Pinned),
// and fast-forwards the local branch of the same name to it, as `git pull
// --ff-only` would. A fetch that fails stops the tab from being made rather
// than starting it from a copy that may be out of date, with a *FetchError.
// A repository without origin has nothing to sync with. One where an agent
// has run in a sandbox, and nerdr cannot fetch from origin safely (see
// gitsafe.OriginEnv), starts from the copy it has, with a warning saying
// so.
func (p *Plan) Sync() (warnings []string, err error) {
	for i := range p.Items {
		it := &p.Items[i]
		it.Pinned = ""
		if it.Mode != "new" || it.Stacked || !hasRemote(it.Repo.Path, "origin") {
			continue // a stacked branch starts where the local one is
		}
		b := strings.TrimPrefix(it.Base, "origin/")
		if p.Unfetched {
			warnings = append(warnings, fmt.Sprintf("%s: started from %s as it was last fetched; it may be out of date", it.Repo.Name, it.Base))
			continue
		}
		if _, why := gitsafe.OriginEnv(it.Repo.Path); why != "" {
			// No fetch can run here (see gitsafe.OriginEnv), and the tab
			// is not to fail for it: it starts from the base as last
			// fetched, unpinned, so neither setup nor hooks from its code
			// run (see SetupIn, addFromBase).
			warnings = append(warnings, fmt.Sprintf("%s: started from %s as it was last fetched, without fetching it: %s. %s",
				it.Repo.Name, it.Base, why, sandboxNote(it.Repo.Path)))
			continue
		}
		// The commit is taken from origin's answer, not from the ref the
		// fetch writes: that ref is in the main checkout's .git, which a
		// sandboxed agent may write to.
		id, err := remoteCommit(it.Repo.Path, b)
		if err != nil {
			return warnings, &FetchError{Repo: it.Repo.Name, Branch: b, Err: err}
		}
		remote := "origin/" + b
		refspec := "+refs/heads/" + b + ":refs/remotes/" + remote
		if _, err := gitOrigin(it.Repo.Path, "fetch", "--quiet", "origin", refspec); err != nil {
			return warnings, &FetchError{Repo: it.Repo.Name, Branch: b, Err: err}
		}
		afterFetch(it.Repo.Path)
		if _, err := git(it.Repo.Path, "cat-file", "-e", id+"^{commit}"); err != nil {
			// Pushed over between the two: origin no longer has it.
			return warnings, &FetchError{Repo: it.Repo.Name, Branch: b,
				Err: fmt.Errorf("%s moved on origin while it was fetched; try again", b)}
		}
		it.Base, it.Pinned = remote, id
		if w := fastForward(it.Repo.Path, b, id); w != "" {
			warnings = append(warnings, it.Repo.Name+": "+w)
		}
	}
	return warnings, nil
}

// afterFetch is called by Sync once the base is fetched: a seam for the
// tests to move refs/remotes/origin/main there, as an agent could.
var afterFetch = func(repo string) {}

// remoteCommit is the commit origin says its branch b is at.
func remoteCommit(repo, b string) (string, error) {
	out, err := gitOrigin(repo, "ls-remote", "origin", "refs/heads/"+b)
	if err != nil {
		return "", err
	}
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) == 2 && f[1] == "refs/heads/"+b && isCommitID(f[0]) {
			return f[0], nil
		}
	}
	return "", fmt.Errorf("origin has no branch %s", b)
}

// isCommitID says whether s is a full commit id, SHA-1 or SHA-256, and not
// a name git would look up in refs.
func isCommitID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}

// FetchError is a fetch of the base branch from origin that failed, as
// Sync tells it.
type FetchError struct {
	Repo, Branch string
	Err          error
}

func (e *FetchError) Error() string {
	msg := fmt.Sprintf("%s: could not fetch %s from origin: %v", e.Repo, e.Branch, e.Err)
	if h := fetchHint(e.Err.Error()); h != "" {
		msg += " — " + h
	}
	return msg
}

func (e *FetchError) Unwrap() error { return e.Err }

// fetchHint says what to do about a fetch that failed for want of a
// password, a passphrase or a host key: nerdr runs git without the
// terminal, so neither git nor ssh can ask for them.
func fetchHint(reason string) string {
	switch {
	case strings.Contains(reason, "Permission denied (publickey"):
		return "ssh could not use your key. If it has a passphrase, load it into ssh-agent with ssh-add: nerdr cannot ask for it"
	case strings.Contains(reason, "Host key verification failed"):
		return "ssh does not know origin's host yet: connect to it once in a terminal to accept its key"
	case strings.Contains(reason, "terminal prompts disabled"), strings.Contains(reason, "could not read Username"):
		return "git needs a username and password, which nerdr cannot ask for: set up a credential helper, or log in with gh auth login"
	}
	return ""
}

// fastForward moves the local branch b up to the commit id when it is
// behind and has no commits of its own, and says why when it is left as it
// is.
func fastForward(repo, b, id string) string {
	old, err := git(repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+b)
	if err != nil {
		return ""
	}
	old = strings.TrimSpace(old)
	if _, err := git(repo, "merge-base", "--is-ancestor", old, id); err != nil {
		return "local " + b + " has commits that are not on origin; left as it is"
	}
	if wt := checkedOutAt(repo, b); wt != "" {
		gitsafe.Trust(repo, wt)
		if _, err := git(wt, "merge", "--ff-only", "--quiet", id); err != nil {
			return "could not bring " + b + " up to date in " + wt + ": " + err.Error()
		}
		return ""
	}
	// Only from where it was checked to be: a commit that landed on it
	// since is not lost.
	if _, err := git(repo, "update-ref", "-m", "nerdr: fast-forward to origin", "refs/heads/"+b, id, old); err != nil {
		return "could not bring " + b + " up to date: " + err.Error()
	}
	return ""
}

// checkedOutAt is the worktree that has branch checked out, or "".
func checkedOutAt(repo, branch string) string {
	out, err := git(repo, "worktree", "list", "--porcelain")
	if err != nil {
		return ""
	}
	var path string
	for _, l := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(l, "worktree "); ok {
			path = p
		} else if l == "branch refs/heads/"+branch {
			return path
		}
	}
	return ""
}

// SetupIn is the worktrees of p, by path, whose code is the base's when
// made: new branches from the base, not an existing branch, origin's or
// a stacked one, which hold code written on them. Prepare runs setup in
// these alone. Call it after Execute, before anything else is done in the
// worktrees.
//
// A branch pinned to origin's commit (Pinned) counts only while its
// worktree is still at that commit. One that is not pinned started from a
// ref in the main checkout — the local main with no origin, or origin's as
// last fetched — and counts only when the user asked for it and no
// sandboxed agent has run in the repository: one that asked (Sandboxed),
// or has worked there, could have moved that ref to code of its own.
func (p Plan) SetupIn() map[string]bool {
	out := map[string]bool{}
	for _, it := range p.Items {
		if it.Mode != "new" || it.Stacked {
			continue
		}
		switch {
		case it.Pinned != "":
			if head, err := git(it.Path, "rev-parse", "--verify", "HEAD"); err == nil && strings.TrimSpace(head) == it.Pinned {
				out[it.Path] = true
			}
		case !p.Sandboxed && !gitsafe.RepoSandboxed(it.Repo.Path):
			out[it.Path] = true
		}
	}
	return out
}

// Execute creates the worktrees. If one fails, the ones already made are
// removed again.
func (p Plan) Execute() ([]Worktree, error) {
	if len(p.Items) > 1 {
		if err := denied.Err(os.MkdirAll(p.Dir, 0o755)); err != nil {
			return nil, err
		}
	} else if err := denied.Err(os.MkdirAll(filepath.Dir(p.Dir), 0o755)); err != nil {
		return nil, err
	}
	var made []Worktree
	for _, it := range p.Items {
		if err := it.add(p.Branch, p.Sandboxed); err != nil {
			for j, w := range made {
				_, _ = git(w.Repo, "worktree", "remove", "--force", w.Path)
				p.Items[j].undo(p.Branch)
			}
			if len(p.Items) > 1 {
				_ = os.Remove(p.Dir)
			}
			return nil, fmt.Errorf("%s: %w", it.Repo.Name, err)
		}
		if it.Stacked {
			_ = gitdata.SetStackedBase(it.Repo.Path, p.Branch, it.Base)
		}
		wt := Worktree{Repo: it.Repo.Path, Path: it.Path, Branch: p.Branch, Pushes: OriginURLs(it.Path)}
		if len(wt.Pushes) > 0 {
			wt.Origin = wt.Pushes[0]
		}
		made = append(made, wt)
	}
	for _, it := range p.Items {
		if it.Replaces != "" {
			// The old branch's upstream (origin's merged branch, as a
			// rule) is not the new one's, which tracks nothing, as any
			// new branch does. Left until now so undo has it to put back.
			_, _ = git(it.Repo.Path, "branch", "--unset-upstream", p.Branch)
		}
	}
	return made, nil
}

// add makes the item's worktree on branch. sandboxed is Plan.Sandboxed.
func (it PlanItem) add(branch string, sandboxed bool) error {
	if it.Mode == "new" && !it.Stacked {
		return it.addFromBase(branch, sandboxed)
	}
	var args []string
	switch {
	case it.Mode == "local":
		args = []string{"worktree", "add", it.Path, branch}
	case it.Mode == "remote":
		args = []string{"worktree", "add", "--track", "-b", branch, it.Path, "origin/" + branch}
	default:
		args = []string{"worktree", "add", "--no-track", "-b", branch, it.Path, it.Base}
	}
	if _, err := git(it.Repo.Path, args...); err != nil {
		return err
	}
	gitsafe.Trust(it.Repo.Path, it.Path)
	return nil
}

// addFromBase makes the worktree of a new branch from the base, with the
// repository's hooks run, as for a checkout of the base's code.
//
// git worktree add would check the branch out by its name, and a sandboxed
// agent can move refs/heads/<branch> between git making it and checking
// it out, so the worktree is made empty and the files are then checked out
// from the commit itself, which no ref names. The commit is Pinned, or,
// where there is none, what Base is at when looked at, once.
func (it PlanItem) addFromBase(branch string, sandboxed bool) error {
	start := it.Pinned
	if start == "" {
		out, err := git(it.Repo.Path, "rev-parse", "--verify", "--end-of-options", it.Base+"^{commit}")
		if err != nil {
			return fmt.Errorf("could not find %s: %w", it.Base, err)
		}
		start = strings.TrimSpace(out)
	}
	if it.Replaces != "" {
		// The merged local branch is moved to the base rather than
		// deleted and made anew, and only if it is still where it was
		// when it was found merged: had a commit landed on it since, that
		// would be lost. Its reflog keeps where it was.
		if _, err := git(it.Repo.Path, "update-ref", "-m", "nerdr: new branch from "+it.Base,
			"refs/heads/"+branch, start, it.Replaces); err != nil {
			return fmt.Errorf("could not move the merged local %s to %s: %w", branch, it.Base, err)
		}
		_, err := git(it.Repo.Path, "worktree", "add", "--no-checkout", it.Path, branch)
		if err != nil {
			it.undo(branch)
			return err
		}
	} else if _, err := git(it.Repo.Path, "worktree", "add", "--no-checkout", "--no-track", "-b", branch, it.Path, start); err != nil {
		return err
	}
	gitsafe.Trust(it.Repo.Path, it.Path)
	// From here a failure leaves nothing behind, as one of git worktree
	// add does: Execute undoes only the worktrees it was given.
	fail := func(err error) error {
		_, _ = git(it.Repo.Path, "worktree", "remove", "--force", it.Path)
		it.undo(branch)
		return err
	}
	beforeCheckout(it.Path)
	// reset --hard to the commit both checks out its files and puts the
	// branch back on it, wherever it was moved to.
	checkout := gitsafe.Env(it.Path)
	if it.Pinned != "" && (sandboxed || gitsafe.RepoSandboxed(it.Repo.Path)) {
		// A sandboxed agent can write the repository's objects: the files
		// are read from a copy of the commit nerdr has checked instead
		// (see verifiedObjects).
		objects, done, err := verifiedObjects(it.Repo.Path, start)
		if err != nil {
			return fail(err)
		}
		defer done()
		checkout = append(checkout, "GIT_OBJECT_DIRECTORY="+objects, "GIT_NO_REPLACE_OBJECTS=1")
	}
	if _, err := gitRunEnv(gitDefaultTimeout, checkout, it.Path, "reset", "--hard", "--quiet", start); err != nil {
		return fail(fmt.Errorf("could not check out %s: %w", short(start), err))
	}
	// The post-checkout hook git worktree add would have run. A hook from
	// inside the worktree (core.hooksPath = .husky/_, say) is code from
	// the commit, which is the base's only when origin named it or the
	// user asked for the tab.
	env := gitsafe.Hooks(it.Path)
	if it.Pinned == "" && (sandboxed || gitsafe.RepoSandboxed(it.Repo.Path)) {
		env = gitsafe.SandboxedHooks(it.Path)
	}
	zero := strings.Repeat("0", len(start))
	if !hasHookCommand() {
		return nil
	}
	if _, err := gitRunEnv(gitDefaultTimeout, env, it.Path, "hook", "run", "--ignore-missing", "post-checkout", "--", zero, start, "1"); err != nil {
		return fail(fmt.Errorf("post-checkout hook: %w", err))
	}
	return nil
}

// beforeCheckout is called by addFromBase between making the branch and
// checking out its files: a seam for the tests to move the branch there,
// as an agent could.
var beforeCheckout = func(path string) {}

// undo puts the branch back the way it was before add, once its worktree
// is gone: a branch the plan created goes, so trying again does not find
// it and take it for the user's own, and a local one it replaced is moved
// back to where it was.
func (it PlanItem) undo(branch string) {
	switch {
	case it.Mode == "local":
	case it.Replaces != "":
		_, _ = git(it.Repo.Path, "update-ref", "refs/heads/"+branch, it.Replaces)
	default:
		_, _ = git(it.Repo.Path, "branch", "-D", branch)
	}
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// baseRef is the ref the work in the worktree at path is counted against:
// the branch its own was stacked on, else defaultBase.
func baseRef(path string) string {
	if b := gitdata.StackedBase(path); b != "" {
		return b
	}
	return defaultBase(path)
}

// defaultBase is the ref a new branch starts from: origin's default branch
// (origin/HEAD), main or master when there is a remote, else the local
// one. Never what the branch checked out in repo is stacked on: that says
// nothing of where a new tab's branch starts (see MakePlanOn).
func defaultBase(repo string) string {
	names := []string{"main", "master"}
	if d := gitdata.DefaultBranch(repo); d != "" {
		names = append([]string{d}, names...)
	}
	for _, b := range names {
		if refExists(repo, "refs/remotes/origin/"+b) {
			return "origin/" + b
		}
	}
	for _, b := range names {
		if refExists(repo, "refs/heads/"+b) {
			return b
		}
	}
	if out, err := git(repo, "symbolic-ref", "--short", "HEAD"); err == nil {
		return strings.TrimSpace(out)
	}
	return ""
}

// ForkBranch is the local branch a pull request from a fork is fetched
// into: the fork's branch name may be taken here, by origin's own branch
// of that name, or be main.
func ForkBranch(number int, branch string) string {
	if b := fmt.Sprintf("pr-%d-%s", number, branch); ValidBranch(b) == nil {
		return b
	}
	return fmt.Sprintf("pr-%d", number)
}

// FetchForkPR fetches pull request number, from a fork, into the local
// branch, as gh pr checkout does: from origin's refs/pull/<number>/head,
// with the branch set to pull from there, so gh finds its pull request.
// An existing branch is only moved forward, so work on it is not lost.
func FetchForkPR(repo string, number int, branch string) error {
	pull := fmt.Sprintf("refs/pull/%d/head", number)
	if _, err := gitTimeout(fetchTimeout, repo, "fetch", "--quiet", "origin", pull+":refs/heads/"+branch); err != nil {
		return fmt.Errorf("could not fetch #%d from origin: %w", number, err)
	}
	for _, kv := range [][2]string{{"remote", "origin"}, {"merge", pull}} {
		if _, err := git(repo, "config", "branch."+branch+"."+kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}

// ForkPRNumber is the pull request from a fork that branch was fetched
// from by FetchForkPR (or gh pr checkout), 0 when it is not one.
func ForkPRNumber(dir, branch string) int {
	if branch == "" {
		return 0
	}
	out, err := git(dir, "config", "--get", "branch."+branch+".merge")
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(out), "refs/pull/"), "/head"))
	if err != nil || !strings.HasPrefix(strings.TrimSpace(out), "refs/pull/") {
		return 0
	}
	return n
}

func refExists(repo, ref string) bool {
	_, err := git(repo, "show-ref", "--verify", "--quiet", ref)
	return err == nil
}

func hasRemote(repo, name string) bool {
	out, err := git(repo, "remote")
	if err != nil {
		return false
	}
	for _, r := range strings.Fields(out) {
		if r == name {
			return true
		}
	}
	return false
}

// Existing lists worktrees under wtRoot of the repositories repos that are
// not open in a tab, grouped the way Plan lays them out: a directory that
// is itself a worktree, or a directory whose children are.
func Existing(wtRoot string, repos []Repo, open map[string]bool) []Group {
	entries, err := os.ReadDir(wtRoot)
	if err != nil {
		return nil
	}
	var groups []Group
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(wtRoot, e.Name())
		if open[dir] {
			continue
		}
		if wt, ok := Inspect(dir, repos); ok {
			groups = append(groups, Group{Name: groupName(e.Name(), []Worktree{wt}), Dir: dir, Worktrees: []Worktree{wt}})
			continue
		}
		subs, _ := os.ReadDir(dir)
		var wts []Worktree
		for _, s := range subs {
			if !s.IsDir() {
				continue
			}
			if wt, ok := Inspect(filepath.Join(dir, s.Name()), repos); ok {
				wts = append(wts, wt)
			}
		}
		if len(wts) > 0 {
			groups = append(groups, Group{Name: groupName(e.Name(), wts), Dir: dir, Worktrees: wts})
		}
	}
	sort.SliceStable(groups, func(i, j int) bool { return nameLess(groups[i].Name, groups[j].Name) })
	return groups
}

// groupName names a group by the branch its worktrees share, as a new tab
// is named, rather than by its directory, which has the repositories'
// names after it; see dirName.
func groupName(dir string, wts []Worktree) string {
	branch := wts[0].Branch
	for _, wt := range wts {
		if wt.Branch == "" || wt.Branch != branch {
			return dir
		}
	}
	return branch
}

// Group is a set of worktrees that belong to one tab.
type Group struct {
	Name      string
	Dir       string
	Worktrees []Worktree
}

// Inspect reads a linked worktree's repository, one of repos, and branch.
// The repository is not asked of the worktree, whose .git file an agent
// can rewrite, but found from the repositories' side; see gitsafe.
func Inspect(dir string, repos []Repo) (Worktree, bool) {
	st, err := os.Stat(filepath.Join(dir, ".git"))
	if err != nil || st.IsDir() {
		return Worktree{}, false
	}
	repo := ""
	for _, r := range repos {
		if gitsafe.Belongs(r.Path, dir) {
			repo = r.Path
			break
		}
	}
	if repo == "" {
		return Worktree{}, false
	}
	gitsafe.Trust(repo, dir)
	return Worktree{Repo: repo, Path: dir, Branch: currentBranch(dir)}, true
}

// currentBranch is the branch checked out in the trusted worktree dir, ""
// when detached. It is read from the worktree's HEAD in the git directory
// gitsafe found, not asked of git: the new-tab dialog lists every
// worktree no tab has open, and a git per worktree there kept the screen
// waiting.
func currentBranch(dir string) string {
	if gitDir, _, ok := gitsafe.Dirs(dir); ok {
		if b, err := os.ReadFile(filepath.Join(gitDir, "HEAD")); err == nil {
			ref, isRef := strings.CutPrefix(strings.TrimSpace(string(b)), "ref: ")
			if !isRef {
				return "" // detached
			}
			if branch, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
				return branch
			}
			return ""
		}
	}
	branch, _ := git(dir, "branch", "--show-current")
	return strings.TrimSpace(branch)
}

// Dirty lists the worktrees with uncommitted or untracked changes, which
// removing would throw away.
func Dirty(wts []Worktree) []string {
	var out []string
	for _, w := range wts {
		s, err := git(w.Path, "status", "--porcelain")
		if err == nil && strings.TrimSpace(s) != "" {
			out = append(out, filepath.Base(w.Path))
		}
	}
	return out
}

// Uncommitted is how many files in the worktree have changes not yet
// committed, untracked ones included.
func Uncommitted(w Worktree) int {
	s, err := git(w.Path, "status", "--porcelain", "-z")
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range strings.Split(s, "\x00") {
		// A rename's entry is followed by its old name, which is not a
		// file of its own.
		if len(e) > 3 && e[2] == ' ' {
			n++
		}
	}
	return n
}

// Unpushed lists the worktrees whose branch has commits that are on no
// remote, which removing the worktree does not lose (the branch stays) but
// which the user may not realise.
func Unpushed(wts []Worktree) []string {
	var out []string
	for _, w := range wts {
		if UnpushedCount(w) > 0 {
			out = append(out, filepath.Base(w.Path))
		}
	}
	return out
}

// UnpushedCount is how many commits w's HEAD has that are on no remote:
// what a push would send. 0 when git cannot say.
func UnpushedCount(w Worktree) int {
	s, err := git(w.Path, "rev-list", "--count", "HEAD", "--not", "--remotes")
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// Head is the commit w has checked out, or "" when git cannot say.
func Head(w Worktree) string {
	s, err := git(w.Path, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

// CommitsSince lists the subjects of the commits on w's HEAD that from
// does not have, newest first, at most limit of them, and how many there
// are in all. Commits on the base branch are not counted: a rebase onto
// main or a merge of it brings those in, and they are not the tab's work.
// Nor are commits a rebase rewrote, when from still had the same change.
// A from git does not know gives nothing.
func CommitsSince(w Worktree, from string, limit int) ([]string, int) {
	if from == "" {
		return nil, 0
	}
	rng := []string{"--cherry-pick", "--right-only", "--no-merges", from + "...HEAD"}
	if b := baseRef(w.Path); b != "" && b != w.Branch {
		rng = append(rng, "^"+b)
	}
	n, err := git(w.Path, append([]string{"rev-list", "--count"}, rng...)...)
	if err != nil {
		return nil, 0
	}
	count, _ := strconv.Atoi(strings.TrimSpace(n))
	if count == 0 {
		return nil, 0
	}
	s, err := git(w.Path, append([]string{"log", "--format=%s", "-n", strconv.Itoa(limit)}, rng...)...)
	if err != nil {
		return nil, count
	}
	return strings.Split(strings.TrimSpace(s), "\n"), count
}

// Remove deletes the worktrees, and the group directory when it is left
// empty. The branches are kept.
func Remove(dir string, wts []Worktree, force bool) error {
	var errs []string
	for _, w := range wts {
		if _, err := os.Stat(w.Path); os.IsNotExist(err) {
			// Already gone (removed by hand, or by an earlier try that
			// failed on another worktree): let git forget it too.
			_, _ = git(w.Repo, "worktree", "prune")
			continue
		}
		args := []string{"worktree", "remove"}
		if force {
			args = append(args, "--force")
		}
		args = append(args, w.Path)
		if _, err := git(w.Repo, args...); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", filepath.Base(w.Path), err))
		}
	}
	if dir != "" {
		_ = os.Remove(dir) // only succeeds when empty, which is the point
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// Nest moves the worktree w of a tab with one repository, which is the
// tab's folder itself, into that folder under its repository's name, so
// other repositories' worktrees can go beside it: the layout of a tab over
// several. The folder keeps its path, and with it the agent's
// conversations, which its agent finds by the folder it starts in. What
// runs in the worktree must be stopped first. If a step fails, the
// worktree is put back where it was.
func Nest(w Worktree) (Worktree, error) {
	dir := filepath.Clean(w.Path)
	name := filepath.Base(w.Repo)
	tmp := dir + ".nerdr-nest"
	if _, err := os.Lstat(tmp); err == nil {
		return w, fmt.Errorf("%s is in the way", tmp)
	}
	if _, err := git(w.Repo, "worktree", "move", dir, tmp); err != nil {
		return w, fmt.Errorf("could not move %s: %w", dir, err)
	}
	if err := denied.Err(os.Mkdir(dir, 0o755)); err != nil {
		_, _ = git(w.Repo, "worktree", "move", tmp, dir)
		return w, err
	}
	to := filepath.Join(dir, name)
	if _, err := git(w.Repo, "worktree", "move", tmp, to); err != nil {
		_ = os.Remove(dir)
		_, _ = git(w.Repo, "worktree", "move", tmp, dir)
		return w, fmt.Errorf("could not move %s into %s: %w", dir, to, err)
	}
	gitsafe.Trust(w.Repo, to)
	w.Path = to
	return w, nil
}

// gitDefaultTimeout bounds every git command: one that hangs (a remote that does
// not answer) must not leave nerdr waiting for ever.
const (
	gitDefaultTimeout = 2 * time.Minute
	fetchTimeout      = time.Minute
)

func git(dir string, args ...string) (string, error) {
	return gitRun(gitDefaultTimeout, false, dir, args...)
}

// gitHooks is git for what the user asked nerdr to do in an unsandboxed
// tab (commit, push) or from the base branch (make a new worktree), with
// the repository's hooks run; see gitsafe.Hooks.
func gitHooks(dir string, args ...string) (string, error) {
	return gitRun(gitDefaultTimeout, true, dir, args...)
}

// gitReason is what git's stderr says went wrong, in a line: its last
// line, as a rule. When git could not reach a remote, its last lines are
// advice that fits any such failure, and the line from ssh or the server
// before them says what this one was.
func gitReason(stderr string) string {
	var lines []string
	for _, l := range strings.Split(stderr, "\n") {
		switch l = strings.TrimSpace(l); l {
		case "", "Please make sure you have the correct access rights", "and the repository exists.":
			continue
		}
		lines = append(lines, l)
	}
	if len(lines) == 0 {
		return ""
	}
	last := lines[len(lines)-1]
	if last == "fatal: Could not read from remote repository." && len(lines) > 1 {
		return lines[len(lines)-2]
	}
	return last
}

func gitTimeout(timeout time.Duration, dir string, args ...string) (string, error) {
	return gitRun(timeout, false, dir, args...)
}

// gitRun runs git in dir, as gitsafe says to. It runs in a session of its
// own, without the terminal: nerdr's screen owns the terminal in raw mode,
// and a password or passphrase prompt written to /dev/tty would both
// garble it and wait for an answer nobody can type. Without a terminal git
// and ssh fail instead.
func gitRun(timeout time.Duration, hooks bool, dir string, args ...string) (string, error) {
	var env []string
	if hooks {
		env = gitsafe.Hooks(dir)
	}
	return gitRunEnv(timeout, env, dir, args...)
}

func gitRunEnv(timeout time.Duration, env []string, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := gitsafe.CommandContext(ctx, dir, args...)
	if env != nil {
		cmd.Env = env
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := proc.Output(cmd)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return string(out), fmt.Errorf("git %s: timed out after %s", args[0], timeout)
		}
		msg := gitReason(stderr.String())
		if strings.Contains(msg, "transport '") && strings.Contains(msg, "not allowed") {
			if note := sandboxNote(dir); note != "" {
				msg += ". " + note
			}
		}
		if msg == "" {
			msg = err.Error()
		}
		return string(out), errors.New(msg)
	}
	return string(out), nil
}

// gitOrigin runs a fetch or ls-remote from origin in the main checkout
// repo. Where an agent has run in a sandbox it is the one transport nerdr
// runs, over ssh or https alone, and not at all when gitsafe.OriginEnv
// says why not; a fetch then leaves the submodules alone, their config
// being in the worktree.
func gitOrigin(repo string, args ...string) (string, error) {
	env, why := gitsafe.OriginEnv(repo)
	if why != "" {
		return "", errors.New(why + ". " + sandboxNote(repo))
	}
	if gitsafe.RepoSandboxed(repo) && args[0] == "fetch" {
		args = append([]string{"fetch", "--no-recurse-submodules"}, args[1:]...)
	}
	return gitRunEnv(fetchTimeout, env, repo, args...)
}

// sandboxNote is what nerdr holds back in the repository dir is in, an
// agent having run in a sandbox there, "" when it does not: which
// repository, where that history is from, and how the user has nerdr
// forget it.
func sandboxNote(dir string) string {
	repo, from, ok := gitsafe.SandboxSource(dir)
	if !ok {
		return ""
	}
	where := ""
	if from != "" {
		where = " (" + from + ")"
	}
	return "An agent has run in a sandbox in " + repo + where +
		": there nerdr does not push, and fetches only from origin over ssh or https, in the main checkout, " +
		"since the programs git fetches and pushes with can run what such an agent left there, outside its sandbox. " +
		"Push or fetch in your own terminal; or, when you trust what is in the repository, have nerdr forget it: " +
		"Forget sandbox history in the tab's menu, or nerdr sandbox clear " + repo + " with nerdr not running"
}

// hasHookCommand reports whether git has `git hook`, which came with git
// 2.36: an older git cannot be asked to run the post-checkout hook, and
// the worktree is made without it rather than not at all. A version that
// cannot be read counts as having it, so a failure is told.
var hasHookCommand = sync.OnceValue(func() bool {
	out, err := exec.Command("git", "version").Output()
	if err != nil {
		return true
	}
	var major, minor int
	if n, _ := fmt.Sscanf(string(out), "git version %d.%d", &major, &minor); n != 2 {
		return true
	}
	return major > 2 || major == 2 && minor >= 36
})
