package gitsafe

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/proc"
)

// delegateEnv removes executable overrides before both the configuration
// inspection and the actual Git invocation. Git's protocol allowlist is an
// environment restriction, so URL rewrites and protocol.*.allow cannot open
// another transport. No transport is safe by name alone: even SSH can load a
// worktree's ProxyCommand, and HTTP uses an external remote helper.
func delegateEnv(env []string) []string {
	out := make([]string, 0, len(env)+5)
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		switch key {
		case "GIT_PROXY_COMMAND", "GIT_SSH_COMMAND", "GIT_SSH", "GIT_SSH_VARIANT", "GIT_EXEC_PATH",
			"GIT_ASKPASS", "SSH_ASKPASS", "GIT_EXTERNAL_DIFF", "GIT_DIFF_OPTS",
			"GIT_ALLOW_PROTOCOL", "GIT_PROTOCOL_FROM_USER", "GIT_PAGER", "PAGER":
			continue
		}
		out = append(out, kv)
	}
	return append(out, "GIT_ALLOW_PROTOCOL=", "GIT_PROTOCOL_FROM_USER=0",
		"GIT_ASKPASS=/dev/null", "SSH_ASKPASS=/dev/null", "GIT_PAGER=")
}

// These settings suppress default executables too. Do not set commit.gpgSign
// or tag.gpgSign false: a user requiring signatures must get a failure, not
// an unsigned commit. filterRefusal exempts only these exact signing keys.
var delegateSettings = [][2]string{
	{"core.sshCommand", "/dev/null"},
	{"credential.helper", ""},
	{"gpg.program", "/dev/null"},
	{"gpg.openpgp.program", "/dev/null"},
	{"gpg.x509.program", "/dev/null"},
	{"gpg.ssh.program", "/dev/null"},
	{"gpg.ssh.defaultKeyCommand", "/dev/null"},
	{"maintenance.auto", "false"},
	{"gc.auto", "0"},
}

// A fetch from origin is the one transport nerdr runs in a repository with
// sandbox history: without it no new tab there starts from origin's base,
// nor gets its setup. It is run in the main checkout, never in a worktree,
// and there git takes the remote, its URL, the ssh command and the
// credential helpers from .git/config and the user's own config, which
// the sandbox must keep the agent from writing (nerdr checks it before
// every sandboxed start; all its local git rests on the same). What such
// an agent may write of the main checkout's git directory, objects and
// refs, a fetch reads as data. What is left is closed here:
//
//   - only ssh and https are allowed, whatever a URL rewrite makes of
//     origin: no ext::, no helper of another name, no local path with a
//     repository of the agent's making behind it;
//   - origin's URL must be in the config, not in the .git/remotes or
//     .git/branches files of old, which are the agent's to write;
//   - an ssh command or credential helper that names a file in the
//     repository or one of its worktrees is the agent's code, and stops it;
//   - the caller adds --no-recurse-submodules to a fetch: a submodule's
//     config is in the worktree; hooks are off as for any other call.
//
// A push stays refused: it runs in the worktree, on what the agent wrote.

// originEnv is delegateEnv for a fetch from origin: ssh and https allowed,
// and the ssh the user's environment names kept, that environment being
// nerdr's own.
func originEnv(env []string) []string {
	var ssh []string
	for _, kv := range env {
		switch key, _, _ := strings.Cut(kv, "="); key {
		case "GIT_SSH_COMMAND", "GIT_SSH", "GIT_SSH_VARIANT":
			ssh = append(ssh, kv)
		}
	}
	out := make([]string, 0, len(env)+len(ssh))
	for _, kv := range delegateEnv(env) {
		if kv != "GIT_ALLOW_PROTOCOL=" {
			out = append(out, kv)
		}
	}
	return append(append(out, ssh...), "GIT_ALLOW_PROTOCOL=https:ssh")
}

// originSettings are delegateSettings with the ssh command and the
// credential helpers left as the user's config has them.
func originSettings() [][2]string {
	var out [][2]string
	for _, s := range delegateSettings {
		if s[0] != "core.sshCommand" && s[0] != "credential.helper" {
			out = append(out, s)
		}
	}
	return out
}

// OriginEnv is the environment to run a fetch or ls-remote from origin
// with, in the main checkout repo: Env where no sandboxed agent has run.
// Where one has, ssh and https are open in it when that is safe; when it
// is not, why says so, for the user, and there is no environment to run
// with. Add --no-recurse-submodules to a fetch.
func OriginEnv(repo string) (env []string, why string) {
	d, ok := dirs(repo)
	if !ok || d.gitDir == untrustedDir || !repoSandboxed(d.common) {
		return Env(repo), ""
	}
	if d.gitDir != d.common {
		return nil, repo + " is a worktree, and nerdr fetches only in a main checkout there"
	}
	var disabled [][2]string
	if guardWorktreeConfig(d, true, &disabled).gitDir == untrustedDir {
		wcMu.Lock()
		why = refusals[d.common]
		wcMu.Unlock()
		if why == "" {
			why = "nerdr runs no git in " + repo
		}
		return nil, why
	}
	config := func(args ...string) (string, error) {
		cmd := proc.Detach(exec.Command("git", append([]string{"-C", repo, "config"}, args...)...))
		cmd.Env = Env(repo)
		out, err := cmd.Output()
		return string(out), err
	}
	if url, err := config("--get", "remote.origin.url"); err != nil || strings.TrimSpace(url) == "" {
		return nil, "origin's URL is not in the Git configuration of " + repo
	}
	cmd := proc.Detach(exec.Command("git", "-C", repo, "ls-remote", "--get-url", "origin"))
	cmd.Env = Env(repo)
	out, err := cmd.Output()
	if err != nil {
		return nil, "nerdr could not tell where origin of " + repo + " is"
	}
	if t := transportOf(strings.TrimSpace(string(out))); t != "ssh" && t != "https" {
		return nil, "origin of " + repo + " is reached by " + t + ", and nerdr fetches only over ssh or https there"
	}
	set, err := config("--null", "--get-regexp", `^(core\.sshcommand|credential(\..*)?\.helper)$`)
	if err != nil && exitCode(err) != 1 {
		return nil, "nerdr could not read the ssh and credential settings of " + repo
	}
	roots := worktreesOf(repo, d.common)
	for _, entry := range strings.Split(set, "\x00") {
		key, value, _ := strings.Cut(entry, "\n")
		if namesFileIn(value, roots) {
			return nil, key + " names a file in " + repo + " or its worktrees, which an agent in a sandbox may have written"
		}
	}
	return envFor(repo, hookOff, true), ""
}

// transportOf is what git reaches url by: the scheme of a URL, ssh for
// the scp-like user@host:path, the helper's name for helper::address, and
// "a local path" for anything else.
func transportOf(url string) string {
	if scheme, _, ok := strings.Cut(url, "://"); ok && !strings.Contains(scheme, ":") {
		return strings.ToLower(scheme)
	}
	if helper, _, ok := strings.Cut(url, "::"); ok {
		return "the helper " + helper
	}
	// As git tells them apart: a colon before any slash names a host.
	if i := strings.IndexAny(url, ":/"); i > 0 && url[i] == ':' {
		return "ssh"
	}
	return "a local path"
}

// worktreesOf are the main checkout repo and the worktrees of it nerdr
// was told of or its .git/worktrees names: the folders a sandboxed agent
// there may have written.
func worktreesOf(repo, common string) []string {
	roots := []string{clean(repo)}
	mu.Lock()
	for path, r := range trusted {
		if r == clean(repo) {
			roots = append(roots, path)
		}
	}
	mu.Unlock()
	files, _ := filepath.Glob(filepath.Join(common, "worktrees", "*", "gitdir"))
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil {
			roots = append(roots, filepath.Dir(strings.TrimSpace(string(b))))
		}
	}
	return roots
}

// namesFileIn reports whether command, an ssh command or a credential
// helper as git's config has it, names a file in one of roots, or one
// relative to where git runs, which is in them.
func namesFileIn(command string, roots []string) bool {
	for _, w := range strings.Fields(strings.TrimPrefix(strings.TrimSpace(command), "!")) {
		w = strings.Trim(w, `'"`)
		if _, v, ok := strings.Cut(w, "="); ok && strings.HasPrefix(w, "-") {
			w = v // --file=/path
		}
		if strings.HasPrefix(w, "./") || strings.HasPrefix(w, "../") {
			return true
		}
		if strings.HasPrefix(w, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				w = filepath.Join(home, w[2:])
			}
		}
		if !filepath.IsAbs(w) {
			continue
		}
		for _, root := range roots {
			if rel, err := filepath.Rel(root, filepath.Clean(w)); err == nil && filepath.IsLocal(rel) {
				return true
			}
		}
	}
	return false
}
