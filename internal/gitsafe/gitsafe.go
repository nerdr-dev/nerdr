// Package gitsafe runs git in folders an agent can write to.
//
// nerdr runs git (and gh, which runs git) in its tabs' worktrees, outside
// whatever sandbox the agent is in, and on its own: after every turn, on
// every refresh. git takes its repository from the worktree's .git file and
// its settings from that repository's config, and some settings are
// commands git runs (core.fsmonitor, hooks, core.sshCommand, filter and
// diff drivers). An agent that may write only its own worktree can still
// rewrite that .git file to point at a repository of its making, and have
// nerdr run whatever that one's config says, as the user.
//
// So nerdr does not ask the worktree where its repository is. It is told
// which worktree belongs to which main checkout (Trust) — it made them, or
// found them under a repository the user chose — and finds the worktree's
// own git directory from the main checkout's side: the one under
// .git/worktrees whose gitdir file points back at it. git is then given
// GIT_DIR, GIT_COMMON_DIR and GIT_WORK_TREE, and the .git file is never
// read. A worktree that cannot be found that way gets a GIT_DIR that does
// not exist, so git fails rather than falling back to the .git file.
//
// On top of that, core.fsmonitor is turned off for every call, and hooks
// for every call but the few the user asks for (Hooks): making a worktree
// on a new branch from the base, or committing and pushing in a tab that
// has not run a sandboxed agent. core.askpass is pointed at /dev/null and
// GIT_TERMINAL_PROMPT is off, so a repository's askpass program never runs
// and git never waits on a password nobody can type: nerdr runs git off its
// terminal, and a credential it lacks is reported (see denied), not asked
// for.
//
// The remaining settings that run commands — core.sshCommand, filter and
// diff drivers, credential helpers — are left as the pinned repository's
// config says. They cannot be blanked without breaking what nerdr itself
// does (an empty core.sshCommand makes push fail with "cannot run"), and
// they are the user's own: a sandboxed agent can write its worktree, but
// not the main checkout's .git/config that GIT_DIR and GIT_COMMON_DIR name.
package gitsafe

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/nerdr-dev/nerdr/internal/proc"
)

var (
	mu sync.Mutex
	// trusted maps a worktree's path to its main checkout's.
	trusted = map[string]string{}
	// admin caches the folder under .git/worktrees found for a worktree,
	// by main checkout and worktree.
	admin = map[[2]string]string{}
)

// Trust records that path is a worktree of the repository whose main
// checkout is repo, or that main checkout itself.
func Trust(repo, path string) {
	if repo == "" || path == "" {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	trusted[clean(path)] = clean(repo)
}

// Belongs reports whether path is a worktree of the main checkout repo, as
// repo's own .git/worktrees says, whatever path's .git file says.
func Belongs(repo, path string) bool {
	_, err := resolve(clean(repo), clean(path))
	return err == nil
}

// untrustedDir is the GIT_DIR given for a worktree that does not check out:
// a path that cannot exist, so git stops there.
var untrustedDir = filepath.Join(os.DevNull, "nerdr-untrusted-worktree")

// Env is the environment to run git, or gh, in dir with: hooks and
// core.fsmonitor off, and git's directories pinned when dir is in a
// trusted worktree or is a main checkout.
func Env(dir string) []string { return env(dir, hookOff) }

// Hooks is Env with the repository's hooks left on, for what the user
// asked nerdr to do in an unsandboxed tab (commit, push) or from the base
// branch (make a new worktree). Hooks there do what the user set them up
// for (Git LFS uploads its files before a push).
func Hooks(dir string) []string { return env(dir, hookAll) }

// SandboxedHooks is Hooks for a worktree where a sandboxed agent has run:
// .git/hooks (such as Git LFS) and global hooks outside the worktree still
// run, while a core.hooksPath that points inside the agent-writable
// worktree (such as .husky/_) is redirected to the repository's own
// .git/hooks directory.
func SandboxedHooks(dir string) []string { return env(dir, hookSandboxed) }

type hookMode int

const (
	hookOff hookMode = iota
	hookAll
	hookSandboxed
)

// WorktreeHooks reports whether git's configured core.hooksPath for dir
// points into the worktree itself (for example a relative path such as
// .husky/_), rather than .git/hooks, a global folder outside the worktree,
// or /dev/null.
func WorktreeHooks(dir string) bool {
	d, ok := dirs(dir)
	if !ok || d.gitDir == untrustedDir {
		return false
	}
	return worktreeHooks(dir, d)
}

func worktreeHooks(dir string, d gitDirs) bool {
	cmd := proc.Detach(exec.Command("git", "-C", dir, "config", "--get", "core.hooksPath"))
	cmd.Env = env(dir, hookAll)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	hp := strings.TrimSpace(string(out))
	if hp == "" || hp == os.DevNull {
		return false
	}
	if strings.HasPrefix(hp, "~/") || hp == "~" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			hp = filepath.Join(home, strings.TrimPrefix(hp, "~"))
		}
	}
	if !filepath.IsAbs(hp) {
		return true
	}
	return (withinPath(clean(hp), clean(d.work)) && !withinPath(clean(hp), clean(d.common))) ||
		(withinPath(realClean(hp), realClean(d.work)) && !withinPath(realClean(hp), realClean(d.common)))
}

func withinPath(path, root string) bool {
	if path == "" || root == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func realClean(p string) string {
	p = clean(p)
	cur := p
	var tail []string
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return filepath.Clean(resolved)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}

// Command is `git -C dir args…` with Env.
func Command(dir string, args ...string) *exec.Cmd {
	cmd := proc.Detach(exec.Command("git", append([]string{"-C", dir}, args...)...))
	cmd.Env = Env(dir)
	return cmd
}

// CommandContext is Command with a context.
func CommandContext(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := proc.Detach(exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...))
	cmd.Env = Env(dir)
	return cmd
}

func env(dir string, mode hookMode) []string {
	e := os.Environ()
	d, ok := dirs(dir)
	// core.askpass is a program git runs to ask for a credential. nerdr
	// never wants one: it runs git off its terminal, and a credential it
	// lacks is reported rather than prompted for. Pointing it at /dev/null
	// makes git fail the ask rather than run whatever the repository named.
	settings := [][2]string{{"core.fsmonitor", "false"}, {"core.askpass", os.DevNull}}
	switch mode {
	case hookOff:
		settings = append(settings, [2]string{"core.hooksPath", os.DevNull})
	case hookSandboxed:
		if !ok || d.gitDir == untrustedDir {
			settings = append(settings, [2]string{"core.hooksPath", os.DevNull})
		} else if worktreeHooks(dir, d) {
			settings = append(settings, [2]string{"core.hooksPath", filepath.Join(d.common, "hooks")})
		}
	}
	// GIT_CONFIG_COUNT rather than -c, so the settings reach the git that
	// gh runs as well. Ones the user's environment already has are kept.
	n, _ := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT"))
	for i, s := range settings {
		e = append(e,
			"GIT_CONFIG_KEY_"+strconv.Itoa(n+i)+"="+s[0],
			"GIT_CONFIG_VALUE_"+strconv.Itoa(n+i)+"="+s[1])
	}
	e = append(e, "GIT_CONFIG_COUNT="+strconv.Itoa(n+len(settings)))
	// nerdr only looks: a status or diff of its must not write the index
	// (refreshing it is optional), which would wake the watch on it and
	// have it look again, and could take the lock an agent's git needs.
	e = append(e, "GIT_OPTIONAL_LOCKS=0")
	// git is run in a session of its own with no terminal; a credential
	// prompt would hang for ever. Never prompt: fail and be reported.
	e = append(e, "GIT_TERMINAL_PROMPT=0")
	if ok {
		e = append(e, "GIT_DIR="+d.gitDir, "GIT_COMMON_DIR="+d.common, "GIT_WORK_TREE="+d.work)
	}
	return e
}

// Dirs is the git directory of the worktree or main checkout dir is in,
// and the repository's common one, as git would be told them; ok is false
// when they are not known.
func Dirs(dir string) (gitDir, common string, ok bool) {
	d, ok := dirs(dir)
	if !ok || d.gitDir == untrustedDir {
		return "", "", false
	}
	return d.gitDir, d.common, true
}

// gitDirs is what git is told instead of reading the .git file.
type gitDirs struct{ gitDir, common, work string }

// dirs finds the git directories for dir: those of the trusted worktree it
// is in, or, for a main checkout nerdr was not told of, its own .git
// folder. ok is false when there is nothing to pin.
func dirs(dir string) (gitDirs, bool) {
	dir = clean(dir)
	mu.Lock()
	var repo, path string
	for p := dir; ; p = filepath.Dir(p) {
		if r, ok := trusted[p]; ok {
			repo, path = r, p
			break
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	mu.Unlock()
	if path == "" {
		if st, err := os.Lstat(filepath.Join(dir, ".git")); err == nil && st.IsDir() {
			g := filepath.Join(dir, ".git")
			return gitDirs{g, g, dir}, true
		}
		return gitDirs{}, false
	}
	d, err := resolve(repo, path)
	if err != nil {
		return gitDirs{untrustedDir, untrustedDir, path}, true
	}
	return d, true
}

// resolve finds the git directories of path, a worktree of the main
// checkout repo, from repo's side.
func resolve(repo, path string) (gitDirs, error) {
	common := filepath.Join(repo, ".git")
	if st, err := os.Stat(common); err != nil || !st.IsDir() {
		return gitDirs{}, errors.New(repo + " is not a main checkout")
	}
	if path == repo || sameDir(path, repo) {
		// The main checkout itself, perhaps by another name: git names it
		// with links followed (/private/var for /var on macOS).
		return gitDirs{common, common, path}, nil
	}
	mu.Lock()
	cached := admin[[2]string{repo, path}]
	mu.Unlock()
	if cached != "" && pointsAt(cached, path) {
		return gitDirs{cached, common, path}, nil
	}
	entries, err := os.ReadDir(filepath.Join(common, "worktrees"))
	if err != nil {
		return gitDirs{}, err
	}
	for _, e := range entries {
		a := filepath.Join(common, "worktrees", e.Name())
		if e.IsDir() && pointsAt(a, path) {
			mu.Lock()
			admin[[2]string{repo, path}] = a
			mu.Unlock()
			return gitDirs{a, common, path}, nil
		}
	}
	return gitDirs{}, errors.New(path + " is not a worktree of " + repo)
}

// pointsAt reports whether the worktree folder a, under .git/worktrees,
// belongs to the worktree at path: its gitdir file names path's .git.
func pointsAt(a, path string) bool {
	b, err := os.ReadFile(filepath.Join(a, "gitdir"))
	if err != nil {
		return false
	}
	g := strings.TrimSpace(string(b))
	if filepath.Base(g) != ".git" {
		return false
	}
	want, err1 := os.Stat(path)
	got, err2 := os.Stat(filepath.Dir(g))
	return err1 == nil && err2 == nil && os.SameFile(want, got)
}

// sameDir reports whether a and b are the same folder.
func sameDir(a, b string) bool {
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(sa, sb)
}

func clean(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}
