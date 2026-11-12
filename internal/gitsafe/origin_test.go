package gitsafe

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestTransportOf: what git reaches a URL by, as OriginEnv judges it.
func TestTransportOf(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/o/r.git":   "https",
		"HTTPS://github.com/o/r.git":   "https",
		"ssh://git@github.com/o/r.git": "ssh",
		"git@github.com:o/r.git":       "ssh",
		"host:path/to/r":               "ssh",
		"http://example.com/r.git":     "http",
		"git://example.com/r.git":      "git",
		"file:///srv/r.git":            "file",
		"/srv/r.git":                   "a local path",
		"./r:odd":                      "a local path",
		"../r.git":                     "a local path",
		"ext::sh -c 'x' %S":            "the helper ext",
		"ext::ssh://host/r":            "the helper ext",
	} {
		if got := transportOf(url); got != want {
			t.Errorf("%s: %q, want %q", url, got, want)
		}
	}
}

// TestNamesFileIn: an ssh command or credential helper that names a file
// where a sandboxed agent may write is told from the user's own.
func TestNamesFileIn(t *testing.T) {
	roots := []string{"/r/app", "/w/feat-app"}
	for command, want := range map[string]bool{
		"":                                 false,
		"osxkeychain":                      false,
		"cache --timeout=300":              false,
		"store --file ~/.git-credentials":  false,
		"!gh auth git-credential":          false,
		"!/usr/bin/gh auth git-credential": false,
		"ssh -i ~/.ssh/id_work":            false,
		"!./tools/cred.sh":                 true,
		"../cred":                          true,
		"/r/app/tools/cred.sh":             true,
		"!sh '/w/feat-app/cred.sh' get":    true,
		"store --file=/r/app/.creds":       true,
		"ssh -F /w/feat-app/ssh_config":    true,
		"/r/application/cred.sh":           false,
	} {
		if got := namesFileIn(command, roots); got != want {
			t.Errorf("%q: %v, want %v", command, got, want)
		}
	}
}

// TestOriginEnvOpensOnlySSHAndHTTPS: the environment for a fetch from
// origin where a sandboxed agent has run allows ssh and https alone, and
// keeps the user's ssh and credential settings, which every other call
// there replaces.
func TestOriginEnvOpensOnlySSHAndHTTPS(t *testing.T) {
	t.Setenv("GIT_SSH_COMMAND", "ssh -i /home/u/.ssh/id")
	t.Setenv("GIT_ALLOW_PROTOCOL", "ext:file")
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	run(t, root, "init", "-q", repo)
	run(t, repo, "remote", "add", "origin", "git@example.com:o/r.git")
	if env, why := OriginEnv(repo); why != "" || slices.Contains(env, "GIT_ALLOW_PROTOCOL=https:ssh") {
		t.Fatalf("a repository with no sandbox history: %q", why)
	}
	SandboxedRepo(repo)
	env, why := OriginEnv(repo)
	if why != "" {
		t.Fatal(why)
	}
	for _, want := range []string{"GIT_ALLOW_PROTOCOL=https:ssh", "GIT_SSH_COMMAND=ssh -i /home/u/.ssh/id"} {
		if !slices.Contains(env, want) {
			t.Errorf("no %s", want)
		}
	}
	for _, kv := range env {
		if kv == "GIT_ALLOW_PROTOCOL=ext:file" || strings.HasSuffix(kv, "=core.sshCommand") || strings.HasSuffix(kv, "=credential.helper") {
			t.Fatalf("%s in the origin environment", kv)
		}
	}
	if other := Env(repo); !slices.Contains(other, "GIT_ALLOW_PROTOCOL=") || slices.Contains(other, "GIT_SSH_COMMAND=ssh -i /home/u/.ssh/id") {
		t.Fatal("every other call there lost its restrictions")
	}
	run(t, repo, "remote", "set-url", "origin", "/srv/r.git")
	if _, why := OriginEnv(repo); why == "" {
		t.Fatal("a local origin was let through")
	}
}
