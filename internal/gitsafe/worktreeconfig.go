package gitsafe

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nerdr-dev/nerdr/internal/proc"
	"github.com/nerdr-dev/nerdr/internal/statedir"
)

// A repository whose .git/config has extensions.worktreeConfig on (as "git
// sparse-checkout" sets it) has git read config.worktree in the worktree's
// own git directory as well: .git/worktrees/<name>/config.worktree for a
// linked worktree, .git/config.worktree for the main checkout. A sandboxed
// agent must be able to write .git/worktrees/<name>/ to commit (index,
// HEAD), and so can write a config.worktree there with a diff.external or a
// filter driver, which git would then run as the user on nerdr's next
// status or diff. Pinning GIT_DIR does not help, since that is the folder
// the file is in, and the extension cannot be turned off from the
// environment: git reads it from the common config file alone.
//
// Checking the file's keys before each call would not do: nerdr reads the
// file, git reads it again a moment later, and an agent that rewrites it in
// a loop wins that race often enough, nerdr running git on every refresh.
// What the agent cannot change is the extension, in .git/config. So in a
// repository where a sandboxed agent has run (Sandboxed), with the
// extension on, nerdr does not run git in any of its worktrees nor its main
// checkout: they get a GIT_DIR that does not exist, as an untrusted
// worktree does, and the reason is kept for the user (Refusals). It is the
// whole repository, not only the sandboxed tab's worktree, because a
// sandbox that lets an agent write .git/worktrees/<name>/ often lets it
// write the folders of the other worktrees as well.

var (
	wcMu sync.Mutex
	// sandboxedPaths are the worktrees and main checkouts where a sandboxed
	// agent has run.
	sandboxedPaths = map[string]bool{}
	// Repository identity outlives any individual worktree or its trust
	// mapping. Objects and refs survive removal of the worktree.
	sandboxedRepositories = map[string]bool{}
	// sandboxedFrom is where a repository's history is from, by main
	// checkout, for the user; see SandboxedRepoFrom.
	sandboxedFrom = map[string]string{}
	// extensionSeen caches whether a common config file turns the
	// extension on, by its path, while its size and time are as they were.
	extensionSeen = map[string]extensionLook{}
	// refusals are the repositories git was refused in, by their common
	// git directory, with why; reported those Refusals has handed out
	// already.
	refusals = map[string]string{}
	reported = map[string]string{}
)

type extensionLook struct {
	size int64
	mod  time.Time
	on   bool
}

// harmlessWorktreeKeys are the settings "git sparse-checkout" writes to
// config.worktree. They only say which files are checked out; none runs
// anything. They are named in the notice, to tell a repository that only
// uses sparse-checkout from one whose config.worktree holds something else.
var harmlessWorktreeKeys = []string{"core.sparsecheckout", "core.sparsecheckoutcone", "index.sparse"}

// Sandboxed records that a sandboxed agent has run in path, a worktree or a
// main checkout, so that git there and in the rest of its repository is
// refused while the repository has extensions.worktreeConfig on.
func Sandboxed(path string) {
	if path == "" {
		return
	}
	common := commonOf(path)
	if common != "" {
		rememberSandboxedRepository(common)
		return
	}
	// Keep only unresolved paths for the fallback: once repository
	// provenance is known it does not depend on a surviving worktree.
	wcMu.Lock()
	sandboxedPaths[clean(path)] = true
	wcMu.Unlock()
}

// SandboxedRepo restores repository provenance without requiring a live
// worktree or looking at an agent-controlled .git file.
func SandboxedRepo(repo string) {
	if repo == "" {
		return
	}
	rememberSandboxedRepository(filepath.Join(clean(repo), ".git"))
}

// SandboxedRepoFrom is SandboxedRepo, with where the history is from, for
// the user: the tab or folder an agent ran in a sandbox in. The first one
// told is kept.
func SandboxedRepoFrom(repo, from string) {
	if repo == "" {
		return
	}
	SandboxedRepo(repo)
	if from == "" {
		return
	}
	wcMu.Lock()
	if sandboxedFrom[clean(repo)] == "" {
		sandboxedFrom[clean(repo)] = from
	}
	wcMu.Unlock()
}

// SandboxSource is the main checkout of the repository dir is in, when a
// sandboxed agent has run in that repository, and where that history is
// from, "" when nerdr was not told; ok is false when none has.
func SandboxSource(dir string) (repo, from string, ok bool) {
	d, known := dirs(dir)
	if !known || d.gitDir == untrustedDir || !repoSandboxed(d.common) {
		return "", "", false
	}
	repo = filepath.Dir(d.common)
	wcMu.Lock()
	defer wcMu.Unlock()
	from = sandboxedFrom[repo]
	if from == "" {
		for r, f := range sandboxedFrom {
			if sameDir(r, repo) {
				from = f
				break
			}
		}
	}
	return repo, from, true
}

// ForgetSandboxed drops the sandbox history of the main checkout repo: the
// user has said they trust what is in the repository now. Host Git runs
// there again as in one no sandboxed agent has worked in, until one does.
func ForgetSandboxed(repo string) {
	if repo == "" {
		return
	}
	common := filepath.Join(clean(repo), ".git")
	wcMu.Lock()
	var paths []string
	for p := range sandboxedPaths {
		paths = append(paths, p)
	}
	wcMu.Unlock()
	// commonOf takes mu, which is never taken with wcMu held.
	var gone []string
	for _, p := range paths {
		if c := commonOf(p); c != "" && (c == common || sameDir(c, common)) {
			gone = append(gone, p)
		}
	}
	wcMu.Lock()
	defer wcMu.Unlock()
	for _, p := range gone {
		delete(sandboxedPaths, p)
	}
	for c := range sandboxedRepositories {
		if c == common || sameDir(c, common) {
			delete(sandboxedRepositories, c)
			delete(refusals, c)
			delete(reported, c)
		}
	}
	for r := range sandboxedFrom {
		if r == clean(repo) || sameDir(r, repo) {
			delete(sandboxedFrom, r)
		}
	}
}

// rememberSandboxedRepository retains both the original spelling and its
// resolved path. Positive taint does not expire by itself, even if a symlink
// disappears or a worktree closes: only the user takes it away
// (ForgetSandboxed). This is not a cache of permission to run host code.
func rememberSandboxedRepository(common string) {
	canonical, err := filepath.EvalSymlinks(common)
	wcMu.Lock()
	defer wcMu.Unlock()
	sandboxedRepositories[common] = true
	if err == nil {
		sandboxedRepositories[canonical] = true
	}
}

// Refusals hands out the repositories nerdr refused to run git in because
// of unsafe configuration since it was last called, by main checkout,
// each with why, to show the user. One notice per repository is enough:
// the reason, and what to do, is the same for all its worktrees. A
// repository is handed out again when the reason changes, or after Git
// has run in it again.
func Refusals() map[string]string {
	wcMu.Lock()
	defer wcMu.Unlock()
	out := map[string]string{}
	for p, why := range refusals {
		if reported[p] != why {
			out[filepath.Dir(p)] = why
			reported[p] = why
		}
	}
	return out
}

// guardWorktreeConfig is d, or a GIT_DIR that does not exist when git must
// not run there: d is in a repository where a sandboxed agent has run, and
// that repository has extensions.worktreeConfig or executable filters. ok is dirs' ok: with
// nothing pinned, there is nothing to guard.
func guardWorktreeConfig(d gitDirs, ok bool, disabled *[][2]string) gitDirs {
	if !ok || d.gitDir == untrustedDir {
		return d
	}
	if !repoSandboxed(d.common) {
		wcMu.Lock()
		delete(refusals, d.common)
		delete(reported, d.common)
		wcMu.Unlock()
		return d
	}
	if !extensionOn(d.common) {
		why := filterRefusal(d, disabled)
		wcMu.Lock()
		if why == "" {
			delete(refusals, d.common)
			delete(reported, d.common)
		} else {
			refusals[d.common] = why
		}
		wcMu.Unlock()
		if why == "" {
			return d
		}
		return gitDirs{untrustedDir, untrustedDir, d.work}
	}
	// The reason is found once, not on every call: it reads the
	// config.worktree files with git, and only says what is in them for the
	// user.
	wcMu.Lock()
	known := strings.Contains(refusals[d.common], "extensions.worktreeConfig")
	wcMu.Unlock()
	if !known {
		why := refusal(d.common)
		wcMu.Lock()
		refusals[d.common] = why
		wcMu.Unlock()
	}
	return gitDirs{untrustedDir, untrustedDir, d.work}
}

// RepoSandboxed reports whether a sandboxed agent has run in the main
// checkout repo or in a worktree of it: such an agent may have written the
// repository's objects and refs.
func RepoSandboxed(repo string) bool {
	return repoSandboxed(filepath.Join(clean(repo), ".git"))
}

// repoSandboxed reports whether a sandboxed agent has run in a worktree of
// the repository whose common git directory is common, or in its main
// checkout.
func repoSandboxed(common string) bool {
	wcMu.Lock()
	if sandboxedRepositories[common] {
		wcMu.Unlock()
		return true
	}
	wcMu.Unlock()
	// Resolve only the queried path for the usual symlink-alias case,
	// instead of statting every historical repository and worktree.
	if canonical, err := filepath.EvalSymlinks(common); err == nil {
		wcMu.Lock()
		known := sandboxedRepositories[canonical]
		if known {
			sandboxedRepositories[common] = true
		}
		wcMu.Unlock()
		if known {
			return true
		}
	}
	wcMu.Lock()
	repos := make([]string, 0, len(sandboxedRepositories))
	for repo := range sandboxedRepositories {
		repos = append(repos, repo)
	}
	paths := make([]string, 0, len(sandboxedPaths))
	for p := range sandboxedPaths {
		paths = append(paths, p)
	}
	wcMu.Unlock()
	for _, repo := range repos {
		if sameDir(repo, common) {
			rememberSandboxedRepository(common)
			return true
		}
	}
	for _, p := range paths {
		c := commonOf(p)
		if c != "" && (c == common || sameDir(c, common)) {
			rememberSandboxedRepository(common)
			wcMu.Lock()
			delete(sandboxedPaths, p)
			wcMu.Unlock()
			return true
		}
	}
	return false
}

// commonOf is the common git directory of the repository path is a
// worktree or main checkout of, as nerdr was told it (Trust) or, for a
// main checkout it was not told of, its .git folder; "" when neither.
func commonOf(path string) string {
	mu.Lock()
	for p := path; ; p = filepath.Dir(p) {
		if r, ok := trusted[p]; ok {
			mu.Unlock()
			return filepath.Join(r, ".git")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	mu.Unlock()
	if st, err := os.Lstat(filepath.Join(path, ".git")); err == nil && st.IsDir() {
		return filepath.Join(path, ".git")
	}
	return ""
}

// extensionOn reports whether common's config file turns
// extensions.worktreeConfig on, as git reads it: from that file alone, with
// no includes. A value git cannot read as a boolean counts as on. The
// answer is kept while the file's size and time stay as they were: a
// sandboxed agent cannot write that file.
func extensionOn(common string) bool {
	f := filepath.Join(common, "config")
	st, err := os.Stat(f)
	if err != nil {
		return false
	}
	wcMu.Lock()
	seen, ok := extensionSeen[f]
	wcMu.Unlock()
	if ok && seen.size == st.Size() && seen.mod.Equal(st.ModTime()) {
		return seen.on
	}
	cmd := configCommand("--file", f, "--type=bool", "--get", "extensions.worktreeConfig")
	out, err := cmd.Output()
	on := true
	switch {
	case err == nil:
		on = strings.TrimSpace(string(out)) == "true"
	case exitCode(err) == 1:
		// Not set.
		on = false
	}
	wcMu.Lock()
	extensionSeen[f] = extensionLook{st.Size(), st.ModTime(), on}
	wcMu.Unlock()
	return on
}

// refusal is why git is refused in the repository whose common git
// directory is common, for the user: what its config.worktree files hold
// now, and what to do.
func refusal(common string) string {
	files, _ := filepath.Glob(filepath.Join(common, "worktrees", "*", "config.worktree"))
	files = append([]string{filepath.Join(common, "config.worktree")}, files...)
	var found []string
	for _, f := range files {
		keys, ok := worktreeKeys(f)
		if !ok {
			continue
		}
		if other := slices.DeleteFunc(keys, func(k string) bool {
			return slices.Contains(harmlessWorktreeKeys, k)
		}); len(other) > 0 {
			found = append(found, f+" sets "+strings.Join(other, ", "))
		}
	}
	what := "Only sparse-checkout's own settings are there now, but an agent could add one that runs a command " +
		"(diff.external, a filter driver) between nerdr's look and git's."
	if len(found) > 0 {
		what = strings.Join(found, "; ") + ": look at them, and remove what you did not set."
	}
	return "nerdr runs no git in " + filepath.Dir(common) + " or its worktrees (no changes, commits, pushes, " +
		"pull requests or new worktrees there): an agent has run in a sandbox in this repository, and " +
		filepath.Join(common, "config") + " has extensions.worktreeConfig on, so git also reads each worktree's " +
		"config.worktree, which such an agent can write. " + what + " " +
		"To have nerdr work there again, turn the extension off: git sparse-checkout disable in each sparse " +
		"worktree (they become full checkouts), then git config --unset extensions.worktreeConfig in the main checkout."
}

// worktreeKeys are the settings config file f holds, as git reads them,
// sorted and each once; ok is false when f cannot be read.
func worktreeKeys(f string) (keys []string, ok bool) {
	if _, err := os.Stat(f); err != nil {
		return nil, false
	}
	out, err := configCommand("--file", f, "--null", "--list").Output()
	if err != nil {
		return nil, false
	}
	for _, e := range bytes.Split(out, []byte{0}) {
		if len(e) == 0 {
			continue
		}
		k, _, _ := bytes.Cut(e, []byte{'\n'})
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	return slices.Compact(keys), true
}

// configCommand is `git config args…` on a file named in args, run outside
// any repository so that git reads that file and nothing else of one.
//
// git still looks for a .git in the folder it runs in, ceiling or not, so
// that is the state folder's temporary folder, which a sandboxed agent
// cannot write: not os.TempDir, where it often can, and could put a .git
// of its own that makes every such command fail. When the folder cannot
// be made, the command fails to start, which callers take as they take a
// failure of git's.
func configCommand(args ...string) *exec.Cmd {
	cmd := proc.Detach(exec.Command("git", append([]string{"config"}, args...)...))
	dir, _ := statedir.EnsureTempDir()
	if dir == "" {
		dir = statedir.TempDir()
	}
	cmd.Dir = dir
	var e []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_DIR=") && !strings.HasPrefix(kv, "GIT_COMMON_DIR=") &&
			!strings.HasPrefix(kv, "GIT_WORK_TREE=") {
			e = append(e, kv)
		}
	}
	cmd.Env = append(e, "GIT_CEILING_DIRECTORIES="+dir)
	return cmd
}

func exitCode(err error) int {
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}
