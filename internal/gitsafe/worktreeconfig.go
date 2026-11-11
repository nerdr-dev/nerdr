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
	wcMu.Lock()
	defer wcMu.Unlock()
	sandboxedPaths[clean(path)] = true
}

// Refusals hands out the repositories nerdr refused to run git in because
// of extensions.worktreeConfig since it was last called, by main checkout,
// each with why, to show the user. One notice per repository is enough:
// the reason, and what to do, is the same for all its worktrees. A
// repository is handed out again only after git has run in it again.
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
// that repository has extensions.worktreeConfig on. ok is dirs' ok: with
// nothing pinned, there is nothing to guard.
func guardWorktreeConfig(d gitDirs, ok bool) gitDirs {
	if !ok || d.gitDir == untrustedDir {
		return d
	}
	if !repoSandboxed(d.common) || !extensionOn(d.common) {
		wcMu.Lock()
		delete(refusals, d.common)
		delete(reported, d.common)
		wcMu.Unlock()
		return d
	}
	// The reason is found once, not on every call: it reads the
	// config.worktree files with git, and only says what is in them for the
	// user.
	wcMu.Lock()
	known := refusals[d.common] != ""
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
	paths := make([]string, 0, len(sandboxedPaths))
	for p := range sandboxedPaths {
		paths = append(paths, p)
	}
	wcMu.Unlock()
	for _, p := range paths {
		c := commonOf(p)
		if c != "" && (c == common || sameDir(c, common)) {
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
func configCommand(args ...string) *exec.Cmd {
	cmd := proc.Detach(exec.Command("git", append([]string{"config"}, args...)...))
	cmd.Dir = os.TempDir()
	var e []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_DIR=") && !strings.HasPrefix(kv, "GIT_COMMON_DIR=") &&
			!strings.HasPrefix(kv, "GIT_WORK_TREE=") {
			e = append(e, kv)
		}
	}
	cmd.Env = append(e, "GIT_CEILING_DIRECTORIES="+os.TempDir())
	return cmd
}

func exitCode(err error) int {
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}
