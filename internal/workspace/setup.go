package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"nerdr/internal/denied"
	gitdata "nerdr/internal/git"
	"nerdr/internal/gitsafe"
	"nerdr/internal/proc"

	"github.com/BurntSushi/toml"
)

// A new worktree has only what is committed. What git ignores — .env
// files, local certificates, installed dependencies — is in the main
// checkout alone, and the first thing an agent runs fails for the want of
// it. A repository says what a new worktree needs in .nerdr.toml:
//
//	copy  = [".env", ".env.*", "config/dev.key"]
//	setup = "npm ci"
//
// copy are paths or glob patterns, relative to the repository, copied from
// the main checkout; setup is a shell command run in the new worktree.

// RepoConfigFile is the name of a repository's own nerdr settings.
const RepoConfigFile = ".nerdr.toml"

// setupTimeout bounds the setup command: installing dependencies can take a
// while, but not for ever.
var setupTimeout = 15 * time.Minute

// RepoConfig is a repository's .nerdr.toml.
type RepoConfig struct {
	Copy  []string `toml:"copy"`
	Setup string   `toml:"setup"`
}

// LoadRepoConfig reads repo's .nerdr.toml. It looks in the main checkout,
// where an untracked file would be, before the committed one a worktree
// gets anyway. A missing file gives an empty config.
func LoadRepoConfig(repo string) (RepoConfig, error) {
	var c RepoConfig
	data, err := os.ReadFile(filepath.Join(repo, RepoConfigFile))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if _, err := toml.Decode(string(data), &c); err != nil {
		return c, fmt.Errorf("%s: %w", filepath.Join(repo, RepoConfigFile), err)
	}
	return c, nil
}

// Empty reports whether the config asks for nothing.
func (c RepoConfig) Empty() bool { return len(c.Copy) == 0 && strings.TrimSpace(c.Setup) == "" }

// The .nerdr.toml is read from the main checkout, which an agent may write
// to — its tab can be there, when the branch it works on is checked out in
// it — and setup runs as the user, outside any sandbox the agent is in;
// copy can reach through a symlink the agent left there for files its
// sandbox cannot read, and put them in a worktree it can. So nothing in it
// is done until the user has allowed it as it is now (Allow), and a
// change to it asks again, as direnv asks before it runs an .envrc.

// key is what the user allows of c: all of it.
func (c RepoConfig) key() string {
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Allowed reports whether the user has allowed repo's config c, as kept
// in file.
func Allowed(file, repo string, c RepoConfig) bool {
	return loadAllowed(file)[filepath.Clean(repo)] == c.key()
}

// Allow keeps in file that the user allows repo's config c.
func Allow(file, repo string, c RepoConfig) error {
	all := loadAllowed(file)
	all[filepath.Clean(repo)] = c.key()
	b, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	if err := denied.Err(os.MkdirAll(filepath.Dir(file), 0o700)); err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := denied.Err(os.WriteFile(tmp, b, 0o600)); err != nil {
		return err
	}
	return denied.Err(os.Rename(tmp, file))
}

func loadAllowed(file string) map[string]string {
	all := map[string]string{}
	if b, err := os.ReadFile(file); err == nil {
		_ = json.Unmarshal(b, &all)
	}
	return all
}

// ToAllow is the configs of repos that ask for something the user has not
// allowed, by repository; those that cannot be read are left to Prepare
// to say so.
func ToAllow(file string, repos []string) map[string]RepoConfig {
	out := map[string]RepoConfig{}
	for _, r := range repos {
		if c, err := LoadRepoConfig(r); err == nil && !c.Empty() && !Allowed(file, r, c) {
			out[r] = c
		}
	}
	return out
}

// Prepare gets each new worktree ready as its repository's .nerdr.toml
// asks, if the user has allowed it as it is now (see Allowed, with file).
// Problems are returned as warnings: a worktree without its .env is still
// a worktree, and the tab is opened either way. progress is told what is
// being done, for the wizard to show.
//
// setup is run only in the worktrees setupIn has, by path: those that
// hold the base's code (see Plan.SetupIn). What the user allowed is the
// command, and a command such as npm ci runs the code it finds — a
// postinstall an agent wrote on its branch, or a fork's pull request
// put there — as the user, outside any sandbox. Elsewhere it is left for
// the user to run once they have read the code.
func Prepare(wts []Worktree, setupIn map[string]bool, file string, progress func(string)) []string {
	var warnings []string
	for _, w := range wts {
		name := filepath.Base(w.Repo)
		c, err := LoadRepoConfig(w.Repo)
		if err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		if c.Empty() {
			continue
		}
		if !Allowed(file, w.Repo, c) {
			warnings = append(warnings, name+": "+RepoConfigFile+" not allowed, so nothing in it was done")
			continue
		}
		if len(c.Copy) > 0 {
			progress(name + ": copying " + strings.Join(c.Copy, ", "))
			if err := CopyInto(w.Repo, w.Path, c.Copy); err != nil {
				warnings = append(warnings, name+": "+err.Error())
			}
		}
		if s := strings.TrimSpace(c.Setup); s != "" && !setupIn[w.Path] {
			warnings = append(warnings, name+": setup not run — the branch has code that is not the base's; run "+s+" yourself once you have read it")
		} else if s != "" {
			progress(name + ": " + s)
			if err := RunSetup(w.Path, s); err != nil {
				warnings = append(warnings, name+": setup failed: "+err.Error())
			}
		}
	}
	return warnings
}

// CopyInto copies the files matching patterns from the main checkout src
// into the worktree dst, keeping their paths. A file the worktree already
// has is left alone: it came from the branch, and the branch wins. A
// pattern that matches a directory copies all of it.
func CopyInto(src, dst string, patterns []string) error {
	if err := denied.Err(os.MkdirAll(dst, 0o755)); err != nil {
		return err
	}
	// Root constrains every write, including traversal through parent
	// symlinks that can change between a check and the file creation.
	root, err := os.OpenRoot(dst)
	if err != nil {
		return err
	}
	defer root.Close()
	// What a pattern matches must be in the repository once symlinks are
	// followed: a symlink in the main checkout must not reach out of it.
	realSrc, err := filepath.EvalSymlinks(src)
	if err != nil {
		return err
	}
	// And read from a root of its own as well: a folder in the main
	// checkout an agent swaps for a link once it has been looked at does
	// not lead the copy out of it either.
	from, err := os.OpenRoot(realSrc)
	if err != nil {
		return err
	}
	defer from.Close()
	var errs []string
	for _, pat := range patterns {
		if filepath.IsAbs(pat) || strings.HasPrefix(filepath.Clean(pat), "..") {
			errs = append(errs, fmt.Sprintf("%s: must be a path inside the repository", pat))
			continue
		}
		matches, err := filepath.Glob(filepath.Join(src, pat))
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", pat, err))
			continue
		}
		for _, m := range matches {
			rel, err := filepath.Rel(src, m)
			if err != nil || rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
				continue
			}
			if dir, err := filepath.EvalSymlinks(filepath.Dir(m)); err != nil || !inside(realSrc, dir) {
				errs = append(errs, fmt.Sprintf("%s: is through a symlink out of the repository", rel))
				continue
			}
			if err := copyTree(from, rel, root, rel); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", rel, err))
			}
		}
	}
	if len(errs) > 0 {
		return errors.New("copy: " + strings.Join(errs, "; "))
	}
	return nil
}

// inside reports whether path is dir or under it.
func inside(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// copyTree copies src, in the main checkout from, to dst in the worktree
// root: a folder with all in it, a link as a link, a file as a file.
func copyTree(from *os.Root, src string, root *os.Root, dst string) error {
	st, err := from.Lstat(src)
	if err != nil {
		return err
	}
	existing, err := root.Lstat(dst)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil && (!st.IsDir() || !existing.IsDir()) {
		return nil // existing branch files and symlinks win
	}
	switch {
	case st.IsDir():
		dir, err := from.Open(src)
		if err != nil {
			return err
		}
		entries, err := dir.ReadDir(-1)
		dir.Close()
		if err != nil {
			return err
		}
		if err := root.MkdirAll(dst, st.Mode().Perm()); err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyTree(from, filepath.Join(src, e.Name()), root, filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	case st.Mode()&os.ModeSymlink != 0:
		target, err := from.Readlink(src)
		if err != nil {
			return err
		}
		if err := root.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return root.Symlink(target, dst)
	case !st.Mode().IsRegular():
		return nil // sockets, fifos: nothing to copy
	}
	if err := root.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	// Not a FIFO put in its place since it was looked at, which would
	// wait, nor any other file.
	in, err := from.OpenFile(src, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer in.Close()
	if now, err := in.Stat(); err != nil || !os.SameFile(now, st) {
		return fmt.Errorf("%s changed while it was copied", src)
	}
	out, err := root.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, st.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// RunSetup runs a repository's setup command in the worktree dir with the
// user's shell, away from the terminal (see gitTimeout). The error carries
// the last line the command printed, which is usually the one that says
// what went wrong.
func RunSetup(dir, command string) error {
	ctx, cancel := context.WithTimeout(context.Background(), setupTimeout)
	defer cancel()
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}
	cmd := proc.Detach(exec.CommandContext(ctx, sh, "-c", command))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "CI=1")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := proc.Run(cmd); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("timed out after %s", setupTimeout)
		}
		if l := lastLine(out.String()); l != "" {
			return errors.New(l)
		}
		return err
	}
	return nil
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(s)
}

// OriginURLs is every URL a push to origin from dir goes to, with any
// insteadOf in the config applied: git push sends to each of origin's
// push URLs, not only the first. None when there is no origin.
func OriginURLs(dir string) []string {
	out, err := git(dir, "remote", "get-url", "--push", "--all", "origin")
	if err != nil {
		return nil
	}
	// One a line: a URL that is a path may have spaces in it.
	var urls []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimRight(l, "\r"); strings.TrimSpace(l) != "" {
			urls = append(urls, l)
		}
	}
	return urls
}

// Trust tells gitsafe about w, and records where its origin is when that
// is not known yet: for a tab from before nerdr kept it, or on a
// repository that had no origin then.
func Trust(w *Worktree) {
	gitsafe.Trust(w.Repo, w.Path)
	if w.Origin == "" {
		if w.Pushes = OriginURLs(w.Path); len(w.Pushes) > 0 {
			w.Origin = w.Pushes[0]
		}
	}
}

// CheckOrigin makes sure a push from w goes where origin was when nerdr
// took the worktree on, to every one of its push URLs. The agent works in
// the worktree, and may be able to change the repository's config; nerdr
// pushes with the user's keys, and must not take the branch somewhere the
// agent chose.
func CheckOrigin(w Worktree) error {
	now := OriginURLs(w.Path)
	was := w.Pushes
	if len(was) == 0 && w.Origin != "" {
		was = []string{w.Origin} // kept before nerdr recorded them all
	}
	switch {
	case len(now) == 0:
		return errors.New("the repository has no origin to push to")
	case len(was) == 0:
		return fmt.Errorf("origin (%s) was added after nerdr opened the tab; push it yourself if it is right", strings.Join(now, ", "))
	case !sameURLs(now, was):
		return fmt.Errorf("origin now pushes to %s, but pushed to %s when nerdr opened the tab; nerdr will not push there — check the repository's .git/config",
			strings.Join(now, ", "), strings.Join(was, ", "))
	}
	return nil
}

// sameURLs reports whether a and b are the same URLs, in any order.
func sameURLs(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(slices.Compact(a), slices.Compact(b))
}

// Changes are the worktree's files that are not committed, as git status
// --short lists them: two letters for what changed, then the path.
func Changes(w Worktree) ([]string, error) {
	out, err := git(w.Path, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) != "" {
			files = append(files, l)
		}
	}
	return files, nil
}

// Commit commits all the worktree's changes, new files too, with message,
// the repository's hooks run.
func Commit(w Worktree, message string) error {
	if _, err := gitRun(gitDefaultTimeout, true, w.Path, "add", "-A"); err != nil {
		return err
	}
	_, err := gitRun(gitDefaultTimeout, true, w.Path, "commit", "--quiet", "-m", message)
	return err
}

// Push pushes the worktree's branch to origin, setting it as upstream,
// once CheckOrigin is satisfied. A pull request from a fork is not pushed:
// its branch is in the fork, and a push to origin would make a branch
// there that is not the pull request's.
func Push(w Worktree) error {
	if err := CheckOrigin(w); err != nil {
		return err
	}
	if n := ForkPRNumber(w.Path, w.Branch); n > 0 {
		return fmt.Errorf("%s is pull request #%d from a fork: nerdr does not push it to origin; push to the fork yourself if you may", w.Branch, n)
	}
	_, err := gitRun(fetchTimeout, true, w.Path, "push", "--quiet", "-u", "origin", "HEAD")
	return err
}

// DeleteBranch deletes the branch only if it still points at head, the
// commit the caller checked. Checking and deleting the ref atomically also
// protects commits made after the worktree was removed. It must no longer
// be checked out anywhere.
func DeleteBranch(w Worktree, head string) error {
	if w.Branch == "" || head == "" {
		return errors.New("cannot delete a branch without a verified head")
	}
	wts, err := gitdata.ListWorktrees(w.Repo)
	if err != nil {
		return err
	}
	for _, wt := range wts {
		if wt.Branch == w.Branch {
			return fmt.Errorf("branch %s is still checked out", w.Branch)
		}
	}
	if _, err := git(w.Repo, "update-ref", "-d", "refs/heads/"+w.Branch, head); err != nil {
		return err
	}
	// update-ref removes the ref and reflog; remove its optional upstream
	// settings too, as git branch -D would. A missing section is normal.
	_, _ = git(w.Repo, "config", "--remove-section", "branch."+w.Branch)
	return nil
}

// Stats is what a worktree's branch has done, compared with the base it
// was made from.
type Stats struct {
	Base    string   // the ref compared with, e.g. origin/main
	Ahead   int      // commits on the branch not in base
	Behind  int      // commits in base not on the branch
	Changed []string // files that differ from the merge base, committed or not
}

// BranchStats compares the worktree with its base: origin's main or master
// when there is one — that is what the branch will be merged into — else
// the local one.
func BranchStats(w Worktree) (Stats, error) {
	s := Stats{Base: baseRef(w.Path)}
	if s.Base == "" {
		return s, errors.New("found no main or master branch")
	}
	out, err := git(w.Path, "rev-list", "--left-right", "--count", s.Base+"...HEAD")
	if err != nil {
		return s, err
	}
	if f := strings.Fields(out); len(f) == 2 {
		fmt.Sscan(f[0], &s.Behind)
		fmt.Sscan(f[1], &s.Ahead)
	}
	seen := map[string]bool{}
	add := func(out string) {
		for _, p := range strings.Split(out, "\x00") {
			if p != "" && !seen[p] {
				seen[p] = true
				s.Changed = append(s.Changed, p)
			}
		}
	}
	if out, err := git(w.Path, "diff", "--merge-base", s.Base, "--name-only", "-z"); err == nil {
		add(out)
	}
	if out, err := git(w.Path, "ls-files", "--others", "--exclude-standard", "-z"); err == nil {
		add(out)
	}
	return s, nil
}
