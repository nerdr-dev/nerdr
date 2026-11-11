// Package move is the git side of moving a tab to another machine: what a
// worktree's work is made into on the machine it leaves, and how it is
// put back on the machine it goes to.
//
// The work is carried as a git bundle of the branch and of two commits made
// for the move, which hold what is not committed: Staged, the index as it
// is, and Wip, every change in the worktree, files git does not follow
// yet among them. They are made with an index of their own, so the
// worktree and its index are never touched. The bundle leaves out what the
// target says it has (Have), so it carries only the new.
//
// On the target the bundle is fetched into the repository with the same
// origin, the branch is made or moved forward, a worktree is added on it,
// and the two commits are put back as changes not committed: Staged into
// the index, Wip into the files. Files .gitignore keeps out (.env,
// node_modules) are not moved. Submodules and git-lfs are refused (Look).
//
// A bundle from another machine is data. git fetch from it runs no hooks,
// and every git here runs as gitsafe says.
package move

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	gitdata "github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/proc"
)

const (
	// Chunk is how much of a pack goes in one request: the frames between
	// a client and a core are at most 16 MB, and a chunk goes as base64.
	Chunk = 1 << 20
	// listMax is how many new or ignored files a Look names.
	listMax = 50
)

// Limit is the largest pack a move takes; a variable for tests.
var Limit int64 = 100 << 20

// Look is what moving the worktree at a path would carry and leave.
type Look struct {
	Branch string
	// Changed is how many files differ from the branch's last commit,
	// files git does not follow yet among them; New are those, NewMore
	// how many more there are than New names.
	Changed int
	New     []string
	NewMore int
	// Ignored are the files and folders .gitignore keeps out, which are
	// not moved, and IgnoredMore how many more.
	Ignored     []string
	IgnoredMore int
	// Refuse is why the worktree cannot be moved, "" when it can.
	Refuse string
}

// LookAt looks at the worktree at path.
func LookAt(path string) Look {
	var l Look
	b, err := run(path, nil, "symbolic-ref", "-q", "--short", "HEAD")
	if err != nil || strings.TrimSpace(b) == "" {
		l.Refuse = "no branch is checked out in " + path
		return l
	}
	l.Branch = strings.TrimSpace(b)
	if why := unsupported(path); why != "" {
		l.Refuse = why
		return l
	}
	if out, err := run(path, nil, "status", "--porcelain", "-z", "--untracked-files=all"); err == nil {
		for _, e := range strings.Split(out, "\x00") {
			if len(e) > 3 && e[2] == ' ' {
				l.Changed++
				if e[:2] == "R " || e[:2] == "C " {
					continue // the next entry is where it came from
				}
			}
		}
	}
	if out, err := run(path, nil, "ls-files", "-z", "--others", "--exclude-standard"); err == nil {
		l.New, l.NewMore = cut(splitZ(out))
	}
	if out, err := run(path, nil, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory"); err == nil {
		l.Ignored, l.IgnoredMore = cut(splitZ(out))
	}
	return l
}

func cut(names []string) ([]string, int) {
	if len(names) > listMax {
		return names[:listMax], len(names) - listMax
	}
	return names, 0
}

func splitZ(s string) []string {
	var out []string
	for _, e := range strings.Split(s, "\x00") {
		if e != "" {
			out = append(out, e)
		}
	}
	return out
}

// unsupported is why the worktree at path cannot be moved, if it cannot:
// its submodules would arrive empty, and git-lfs files as pointers.
func unsupported(path string) string {
	if _, err := os.Lstat(filepath.Join(path, ".gitmodules")); err == nil {
		return "it has submodules, which a move does not carry; push the branch and check it out there instead"
	}
	if out, err := run(path, nil, "ls-files", "-s"); err == nil {
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "160000 ") {
				return "it has submodules, which a move does not carry; push the branch and check it out there instead"
			}
		}
	}
	attrs, _ := run(path, nil, "ls-files", "-z", "-co", "--exclude-standard", "--", ":(glob)**/.gitattributes")
	files := splitZ(attrs)
	if _, common, ok := gitsafe.Dirs(path); ok {
		files = append(files, filepath.Join(common, "info", "attributes"))
	}
	for _, f := range files {
		if !filepath.IsAbs(f) {
			f = filepath.Join(path, f)
		}
		b, err := os.ReadFile(f)
		if err == nil && bytes.Contains(b, []byte("filter=lfs")) {
			return "it uses git-lfs, whose files a move does not carry; push the branch and check it out there instead"
		}
	}
	return ""
}

// Part is one worktree's work, as it is packed and landed.
type Part struct {
	// Name is the repository's folder name, Sub the worktree's folder in
	// the tab's ("" when the tab has one worktree).
	Name, Sub string
	Origin    string
	Branch    string
	// Head is the branch's commit; Staged and Wip the commits made of what
	// is not committed, "" when nothing is.
	Head, Staged, Wip string
	// Bundle is the file in the pack that carries them, "" when the target
	// has all of it already.
	Bundle string
}

// Snapshot makes the commits of what is not committed in the worktree at
// path, without touching it or its index, and gives the Part with Branch,
// Head, Staged and Wip set.
func Snapshot(path string) (Part, error) {
	var p Part
	b, err := run(path, nil, "symbolic-ref", "-q", "--short", "HEAD")
	if err != nil {
		return p, errors.New("no branch is checked out in " + path)
	}
	p.Branch = strings.TrimSpace(b)
	head, err := run(path, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return p, fmt.Errorf("%s has no commit yet: %w", p.Branch, err)
	}
	p.Head = strings.TrimSpace(head)
	headTree, err := run(path, nil, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return p, err
	}
	gitDir, _, ok := gitsafe.Dirs(path)
	if !ok {
		return p, errors.New("git does not know " + path + " as a worktree of the repository")
	}
	tmp, err := os.MkdirTemp("", "nerdr-index-")
	if err != nil {
		return p, err
	}
	defer os.RemoveAll(tmp)
	index := filepath.Join(tmp, "index")
	if b, err := os.ReadFile(filepath.Join(gitDir, "index")); err == nil {
		if err := os.WriteFile(index, b, 0o600); err != nil {
			return p, err
		}
	} else if _, err := run(path, []string{"GIT_INDEX_FILE=" + index}, "read-tree", "HEAD"); err != nil {
		return p, err
	}
	env := []string{"GIT_INDEX_FILE=" + index}
	staged, err := run(path, env, "write-tree")
	if err != nil {
		return p, err
	}
	// Not a plain git add -A, which runs git status inside every gitlink's
	// folder, with that folder's own config: LookAt refuses submodules,
	// but one could be added to the index after it looked.
	if err := gitdata.AddAll(context.Background(), path, env...); err != nil {
		return p, err
	}
	full, err := run(path, env, "write-tree")
	if err != nil {
		return p, err
	}
	headTree, staged, full = strings.TrimSpace(headTree), strings.TrimSpace(staged), strings.TrimSpace(full)
	if staged == headTree && full == headTree {
		return p, nil
	}
	// Made the same each time, by nerdr: no identity of the user's needed.
	who := []string{"GIT_AUTHOR_NAME=nerdr", "GIT_AUTHOR_EMAIL=nerdr@localhost", "GIT_COMMITTER_NAME=nerdr",
		"GIT_COMMITTER_EMAIL=nerdr@localhost", "GIT_AUTHOR_DATE=1970-01-01T00:00:00Z", "GIT_COMMITTER_DATE=1970-01-01T00:00:00Z"}
	s, err := run(path, who, "commit-tree", staged, "-p", p.Head, "-m", "nerdr move: staged")
	if err != nil {
		return p, err
	}
	p.Staged = strings.TrimSpace(s)
	w, err := run(path, who, "commit-tree", full, "-p", p.Staged, "-m", "nerdr move: not committed")
	if err != nil {
		return p, err
	}
	p.Wip = strings.TrimSpace(w)
	return p, nil
}

// Bundle writes the bundle of p, made by Snapshot in the worktree at path,
// to out, leaving out the commits in have (the target's) that the worktree
// has as well. It sets p.Bundle to out's name, or leaves it "" when the
// target has it all and nothing is written.
func Bundle(path string, p *Part, have []string, out string) error {
	if _, _, ok := gitsafe.Dirs(path); !ok {
		return errors.New("git does not know " + path + " as a worktree of the repository")
	}
	if p.Wip == "" && slices.Contains(have, p.Head) {
		return nil
	}
	var not []string
	for _, h := range have {
		if !isHex(h) {
			continue
		}
		if _, err := run(path, nil, "cat-file", "-e", h+"^{commit}"); err == nil {
			not = append(not, "^"+h)
		}
	}
	ref := "refs/nerdr-move/" + token()
	refs := []string{"refs/heads/" + p.Branch}
	if p.Wip != "" {
		if _, err := run(path, nil, "update-ref", ref, p.Wip, ""); err != nil {
			return err
		}
		defer run(path, nil, "update-ref", "-d", ref)
		refs = append(refs, ref)
	}
	args := append([]string{"bundle", "create", "--quiet", out}, refs...)
	if _, err := run(path, nil, append(args, not...)...); err != nil {
		return err
	}
	p.Bundle = filepath.Base(out)
	return nil
}

// Have is the commits the repository at repo has of branch and of the
// branch new ones start from, for a bundle to leave out.
func Have(repo, branch string) []string {
	refs := []string{"refs/heads/" + branch, "refs/remotes/origin/" + branch, "HEAD"}
	if d, err := run(repo, nil, "symbolic-ref", "-q", "refs/remotes/origin/HEAD"); err == nil {
		refs = append(refs, strings.TrimSpace(d))
	}
	refs = append(refs, "refs/remotes/origin/main", "refs/remotes/origin/master", "refs/heads/main", "refs/heads/master")
	var have []string
	for _, r := range refs {
		if out, err := run(repo, nil, "rev-parse", "-q", "--verify", r+"^{commit}"); err == nil {
			if h := strings.TrimSpace(out); !slices.Contains(have, h) {
				have = append(have, h)
			}
		}
	}
	return have
}

// CanLand says why p cannot land in the repository at repo, "" when it
// can: its branch is checked out in a worktree there, or has commits the
// moved one has not.
func CanLand(repo string, p Part) string {
	if out, err := run(repo, nil, "worktree", "list", "--porcelain"); err == nil &&
		strings.Contains(out, "branch refs/heads/"+p.Branch+"\n") {
		return "branch " + p.Branch + " is checked out in a worktree there"
	}
	tip, err := run(repo, nil, "rev-parse", "-q", "--verify", "refs/heads/"+p.Branch)
	if err != nil {
		return ""
	}
	tip = strings.TrimSpace(tip)
	if tip == p.Head || p.Head == "" {
		return ""
	}
	if _, err := run(repo, nil, "cat-file", "-e", p.Head+"^{commit}"); err == nil && !Ancestor(repo, tip, p.Head) {
		return "branch " + p.Branch + " there has commits this one has not"
	}
	return ""
}

// Tip is the commit branch is at in the repository at repo, "" when it
// has no such branch.
func Tip(repo, branch string) string {
	out, err := run(repo, nil, "rev-parse", "-q", "--verify", "refs/heads/"+branch+"^{commit}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// Ancestor says whether commit a is b or comes before it, in the
// repository at dir; false when dir does not have a.
func Ancestor(dir, a, b string) bool {
	if a == b {
		return true
	}
	_, err := run(dir, nil, "merge-base", "--is-ancestor", a, b)
	return err == nil
}

// Land puts p back in a new worktree at wt of the repository at repo,
// from the bundle file (when p has one): the branch is made, or moved
// forward to p.Head, and what was not committed comes back as it was,
// staged and not.
func Land(repo, wt string, p Part, bundle string) error {
	if why := CanLand(repo, p); why != "" {
		return errors.New(why)
	}
	tmp := "refs/nerdr-move/" + token()
	defer func() {
		for _, r := range []string{tmp + "/head", tmp + "/wip"} {
			_, _ = run(repo, nil, "update-ref", "-d", r)
		}
	}()
	if p.Bundle != "" {
		if _, err := run(repo, nil, "bundle", "verify", "--quiet", bundle); err != nil {
			return fmt.Errorf("the bundle does not fit this repository: %w", err)
		}
		// Only what the bundle has: a branch with nothing the target
		// lacks is left out of it, though what is not committed is in.
		heads, err := run(repo, nil, "bundle", "list-heads", bundle)
		if err != nil {
			return err
		}
		var specs []string
		for _, l := range strings.Split(heads, "\n") {
			f := strings.Fields(l)
			switch {
			case len(f) != 2:
			case f[1] == "refs/heads/"+p.Branch && f[0] == p.Head:
				specs = append(specs, f[1]+":"+tmp+"/head")
			case p.Wip != "" && f[0] == p.Wip && strings.HasPrefix(f[1], "refs/nerdr-move/"):
				specs = append(specs, f[1]+":"+tmp+"/wip")
			}
		}
		if len(specs) == 0 {
			return errors.New("the bundle has nothing of " + p.Branch)
		}
		args := append([]string{"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", bundle}, specs...)
		if _, err := run(repo, nil, args...); err != nil {
			return fmt.Errorf("git fetch from the bundle: %w", err)
		}
	}
	for _, c := range []string{p.Head, p.Staged, p.Wip} {
		if c == "" {
			continue
		}
		if _, err := run(repo, nil, "cat-file", "-e", c+"^{commit}"); err != nil {
			return fmt.Errorf("commit %s did not come with the bundle", short(c))
		}
	}
	old, err := run(repo, nil, "rev-parse", "-q", "--verify", "refs/heads/"+p.Branch)
	old = strings.TrimSpace(old)
	if err != nil {
		old = ""
	}
	if old != "" && !Ancestor(repo, old, p.Head) {
		return errors.New("branch " + p.Branch + " there has commits this one has not")
	}
	// What Land made goes again if it fails after: the branch put back
	// as it was, and the worktree.
	branchBack := func() {
		switch {
		case old == p.Head:
		case old == "":
			_, _ = run(repo, nil, "update-ref", "-d", "refs/heads/"+p.Branch, p.Head)
		default:
			_, _ = run(repo, nil, "update-ref", "refs/heads/"+p.Branch, old, p.Head)
		}
	}
	if old != p.Head {
		if _, err := run(repo, nil, "update-ref", "-m", "nerdr: moved here", "refs/heads/"+p.Branch, p.Head, old); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		branchBack()
		return err
	}
	if _, err := run(repo, nil, "worktree", "add", "--quiet", wt, p.Branch); err != nil {
		branchBack()
		return err
	}
	gitsafe.Trust(repo, wt)
	if err := restoreWork(wt, p); err != nil {
		_, _ = run(repo, nil, "worktree", "remove", "--force", wt)
		_ = os.RemoveAll(wt)
		_, _ = run(repo, nil, "worktree", "prune")
		branchBack()
		return err
	}
	return nil
}

// afterAdd, when set, fails the landing after the worktree is added: for
// tests.
var afterAdd func(wt string) error

// restoreWork puts what p had not committed back in the worktree at wt.
func restoreWork(wt string, p Part) error {
	if afterAdd != nil {
		if err := afterAdd(wt); err != nil {
			return err
		}
	}
	if p.Wip == "" {
		return nil
	}
	// The files first, while the index is the branch's: what Wip has not
	// of what the index has is then deleted. Then the index, as staged.
	if _, err := run(wt, nil, "restore", "--source="+p.Wip, "--worktree", "--", ":/"); err != nil {
		return err
	}
	if _, err := run(wt, nil, "read-tree", p.Staged+"^{tree}"); err != nil {
		return err
	}
	return nil
}

// Repos that fit: OriginKey is the repository an origin URL names, the
// same for its ssh and https forms.
func OriginKey(url string) string {
	u := strings.TrimSpace(url)
	if u == "" {
		return ""
	}
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
		if j := strings.Index(u, "/"); j >= 0 {
			host := u[:j]
			if k := strings.LastIndex(host, "@"); k >= 0 {
				host = host[k+1:]
			}
			if k := strings.Index(host, ":"); k >= 0 {
				host = host[:k] // a port
			}
			u = host + "/" + u[j+1:]
		}
	} else if i := strings.Index(u, ":"); i > 0 && !strings.Contains(u[:i], "/") {
		host := u[:i]
		if k := strings.LastIndex(host, "@"); k >= 0 {
			host = host[k+1:]
		}
		u = host + "/" + u[i+1:]
	}
	u = strings.TrimSuffix(strings.TrimSuffix(u, "/"), ".git")
	return strings.ToLower(u)
}

// Origins are the URLs origin of the repository at repo fetches from and
// pushes to.
func Origins(repo string) []string {
	var urls []string
	for _, args := range [][]string{{"remote", "get-url", "--all", "origin"}, {"remote", "get-url", "--push", "--all", "origin"}} {
		out, err := run(repo, nil, args...)
		if err != nil {
			continue
		}
		for _, l := range strings.Split(out, "\n") {
			if l = strings.TrimSpace(l); l != "" && !slices.Contains(urls, l) {
				urls = append(urls, l)
			}
		}
	}
	return urls
}

// SameOrigin says whether the repository at repo has origin url.
func SameOrigin(repo, url string) bool {
	k := OriginKey(url)
	if k == "" {
		return false
	}
	for _, u := range Origins(repo) {
		if OriginKey(u) == k {
			return true
		}
	}
	return false
}

func isHex(s string) bool {
	if len(s) < 40 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func token() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// run runs git in dir as gitsafe says, with env added, and gives what it
// wrote, or what its stderr says went wrong.
func run(dir string, env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := gitsafe.CommandContext(ctx, dir, args...)
	cmd.Env = append(cmd.Env, env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := proc.Output(cmd)
	if err != nil {
		return string(out), gitErr(args[0], stderr.String(), err)
	}
	return string(out), nil
}

func gitErr(op, stderr string, err error) error {
	var last string
	sc := bufio.NewScanner(strings.NewReader(stderr))
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			last = l
		}
	}
	var ee *exec.ExitError
	if last == "" || !errors.As(err, &ee) {
		return fmt.Errorf("git %s: %w", op, err)
	}
	return fmt.Errorf("git %s: %s", op, last)
}
