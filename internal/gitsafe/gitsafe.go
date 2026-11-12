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
// has not run a sandboxed agent (see SandboxedHooks). core.askpass is pointed at /dev/null and
// GIT_TERMINAL_PROMPT is off, so a repository's askpass program never runs
// and git never waits on a password nobody can type: nerdr runs git off its
// terminal, and a credential it lacks is reported (see denied), not asked
// for.
//
// In repositories with sandbox history, authentication delegates are disabled
// and signing programs are replaced with /dev/null. Other executable
// delegates are refused. Transports are disabled: trusted transport
// programs can themselves delegate to worktree code. The one exception is
// a fetch from origin over ssh or https run in the main checkout, which
// reads nothing a sandboxed agent may write but objects and refs (see
// OriginEnv). A required signature fails rather than
// silently producing an unsigned commit. See delegates.go.
// A submodule's config is in the worktree, the
// agent's to write, and git reads it when it runs status inside the
// submodule. So nerdr's status and diff do not look inside submodules
// (ignoreDirtySubmodules), and its git add leaves gitlinks out (git.AddAll).
// Repositories with executable filter, diff or merge drivers and sandbox
// history are refused (LFS conversion is disabled with required=true):
// even a trusted filter definition can execute the worktree's scripts.
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
	trusted          = map[string]string{}
	callerRepository bool
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
	// Git reports canonical paths (for example /private/var on macOS),
	// even when the registered path used an alias. Remember that identity
	// at registration; never discover trust from a worktree's .git file.
	canonical, err := filepath.EvalSymlinks(path)
	mu.Lock()
	defer mu.Unlock()
	trusted[clean(path)] = clean(repo)
	if err == nil {
		trusted[clean(canonical)] = clean(repo)
	}
}

// UseCallerRepository enables Git's own repository discovery for a short-lived
// agent CLI process. Such commands run with the caller's privileges and inside
// its sandbox; they do not inherit the host core's in-memory registrations.
// Never call this in the core, holder or client: host operations must refuse
// unregistered .git files. This is deliberately not an environment option.
func UseCallerRepository() {
	mu.Lock()
	callerRepository = true
	mu.Unlock()
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

// SandboxedHooks is the environment for what the user asked nerdr to do
// (commit, push, make a worktree) where a sandboxed agent has run: no hook
// runs. A hook the user installed in .git/hooks, or in a folder of their
// own, is not the agent's to write, but most of them run what the worktree
// says: pre-commit reads .pre-commit-config.yaml, lefthook its lefthook.yml,
// husky the scripts in .husky, a hand-written one the repository's linter
// or tests. Run by nerdr, that is the agent's code run as the user, outside
// its sandbox. LFS conversions are disabled too: see filterRefusal.
func SandboxedHooks(dir string) []string { return env(dir, hookSandboxed) }

type hookMode int

const (
	hookOff hookMode = iota
	hookAll
	hookSandboxed
)

// HasHooks reports whether the repository dir is in has git hooks that
// Hooks would run: a file that is not a sample in the folder
// core.hooksPath names, or in .git/hooks without one.
func HasHooks(dir string) bool {
	d, ok := dirs(dir)
	if !ok || d.gitDir == untrustedDir {
		return false
	}
	hp := filepath.Join(d.common, "hooks")
	cmd := proc.Detach(exec.Command("git", "-C", dir, "config", "--get", "core.hooksPath"))
	cmd.Env = env(dir, hookAll)
	if out, err := cmd.Output(); err == nil {
		if set := strings.TrimSpace(string(out)); set != "" {
			hp = set
		}
	}
	if hp == os.DevNull {
		return false
	}
	if strings.HasPrefix(hp, "~/") || hp == "~" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			hp = filepath.Join(home, strings.TrimPrefix(hp, "~"))
		}
	}
	if !filepath.IsAbs(hp) {
		hp = filepath.Join(d.work, hp)
	}
	entries, err := os.ReadDir(hp)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && !strings.HasSuffix(e.Name(), ".sample") {
			return true
		}
	}
	return false
}

// Command is `git -C dir args…` with Env, and the dirty content of
// submodules left unlooked at (see ignoreDirtySubmodules).
func Command(dir string, args ...string) *exec.Cmd {
	cmd := proc.Detach(exec.Command("git", append([]string{"-C", dir}, ignoreDirtySubmodules(args)...)...))
	cmd.Env = Env(dir)
	return cmd
}

// CommandContext is Command with a context.
func CommandContext(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := proc.Detach(exec.CommandContext(ctx, "git", append([]string{"-C", dir}, ignoreDirtySubmodules(args)...)...))
	cmd.Env = Env(dir)
	return cmd
}

// ignoreDirtySubmodules gives the git commands that compare the work tree
// --ignore-submodules=dirty. To tell whether a submodule has changes of
// its own, git runs `git status` inside the submodule's folder, with that
// folder's own .git and config — and an agent can make any folder of its
// worktree a repository of its own, with a filter driver in its config
// for every file, and put it in the index as a gitlink. The status would
// run the filter, as the user. The setting diff.ignoreSubmodules (in env)
// is not enough on its own: a submodule.<name>.ignore in .gitmodules,
// which the agent writes, overrides it; the command-line option overrides
// both. A submodule's commit still shows when it moves.
//
// git add takes no such option; see git.AddAll for it.
func ignoreDirtySubmodules(args []string) []string {
	i := 0
	// Options to git itself come before the subcommand.
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		if args[i] == "-c" || args[i] == "-C" {
			i++
		}
		i++
	}
	if i >= len(args) {
		return args
	}
	switch args[i] {
	case "status", "diff", "diff-files", "diff-index":
		out := make([]string, 0, len(args)+1)
		out = append(out, args[:i+1]...)
		out = append(out, "--ignore-submodules=dirty")
		return append(out, args[i+1:]...)
	}
	return args
}

func env(dir string, mode hookMode) []string { return envFor(dir, mode, false) }

// envFor is env; with origin, for a fetch from origin in a main checkout
// with sandbox history, with ssh and https left open (see OriginEnv).
func envFor(dir string, mode hookMode, origin bool) []string {
	e := os.Environ()
	d, ok := dirs(dir)
	// A repository whose config.worktree a sandboxed agent can write is
	// given no git at all: see guardWorktreeConfig.
	sandboxed := ok && d.gitDir != untrustedDir && repoSandboxed(d.common)
	var disabled [][2]string
	d = guardWorktreeConfig(d, ok, &disabled)
	// core.askpass is a program git runs to ask for a credential. nerdr
	// never wants one: it runs git off its terminal, and a credential it
	// lacks is reported rather than prompted for. Pointing it at /dev/null
	// makes git fail the ask rather than run whatever the repository named.
	settings := [][2]string{{"core.fsmonitor", "false"}, {"core.askpass", os.DevNull}}
	if sandboxed {
		if origin {
			e = originEnv(e)
			settings = append(settings, originSettings()...)
		} else {
			e = delegateEnv(e)
			settings = append(settings, delegateSettings...)
			settings = append(settings, disabled...)
		}
		// Never run a PATH-resolved git-lfs or a user-defined LFS command.
		// required=true makes Git fail on conversion instead of storing raw
		// content. Global LFS installation then leaves non-LFS repos usable.
		settings = append(settings,
			[2]string{"filter.lfs.clean", ""},
			[2]string{"filter.lfs.smudge", ""},
			[2]string{"filter.lfs.process", ""},
			[2]string{"filter.lfs.required", "true"})
	}
	// A diff or status looks inside each submodule by running git status
	// there, with that folder's own config, which an agent can write (see
	// ignoreDirtySubmodules). The command-line option nerdr gives its own
	// commands is what holds; this is for the git that gh runs, as far as
	// a .gitmodules without an ignore setting of its own lets it.
	settings = append(settings, [2]string{"diff.ignoreSubmodules", "dirty"})
	switch mode {
	case hookOff, hookSandboxed:
		settings = append(settings, [2]string{"core.hooksPath", os.DevNull})
	}
	// GIT_CONFIG_COUNT rather than -c, so the settings reach the git that
	// gh runs as well. Ones the user's environment already has are kept.
	n, _ := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT"))
	parameters := os.Getenv("GIT_CONFIG_PARAMETERS")
	for i, s := range settings {
		// Git applies GIT_CONFIG_PARAMETERS after GIT_CONFIG_COUNT. Append
		// our policy there too, or inherited -c settings can undo the
		// credential resets (and fsmonitor/hook restrictions). Git uses
		// separately quoted keys and values (Git >= 2.31), so an equals
		// sign inside a URL credential scope is not mistaken for the separator.
		if parameters != "" {
			parameters += " "
		}
		parameters += quoteConfigParameter(s[0]) + "=" + quoteConfigParameter(s[1])
		e = append(e,
			"GIT_CONFIG_KEY_"+strconv.Itoa(n+i)+"="+s[0],
			"GIT_CONFIG_VALUE_"+strconv.Itoa(n+i)+"="+s[1])
	}
	e = append(e, "GIT_CONFIG_COUNT="+strconv.Itoa(n+len(settings)), "GIT_CONFIG_PARAMETERS="+parameters)
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

// quoteConfigParameter uses Git's shell-style single-quote escaping.
func quoteConfigParameter(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
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
	caller := callerRepository
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
		if caller {
			return gitDirs{}, false
		}
		for p := dir; ; p = filepath.Dir(p) {
			g := filepath.Join(p, ".git")
			if st, err := os.Lstat(g); err == nil {
				if st.IsDir() {
					return gitDirs{g, g, p}, true
				}
				// Never let Git follow an unregistered worktree's .git file
				// (or symlink), including when called from a subdirectory.
				return gitDirs{untrustedDir, untrustedDir, p}, true
			} else if !os.IsNotExist(err) {
				return gitDirs{untrustedDir, untrustedDir, p}, true
			}
			if filepath.Dir(p) == p {
				break
			}
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
