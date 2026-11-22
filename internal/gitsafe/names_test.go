package gitsafe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNamesFileInReadsCommandsAsShDoes covers the ways an ssh command or
// credential helper can name a file in the repository that a plain split
// on spaces missed, and the user's own settings that must still pass.
func TestNamesFileInReadsCommandsAsShDoes(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	roots := []string{"/r/app", "/w/feat app"}
	for command, want := range map[string]bool{
		// The user's own, as SECURITY.md has them work.
		"osxkeychain":                         false,
		"cache":                               false,
		"cache --timeout=3600":                false,
		"store":                               false,
		"store --file ~/.git-credentials":     false,
		"store --file=$HOME/.git-credentials": false,
		"!gh auth git-credential":             false,
		"!/opt/homebrew/bin/gh auth git-credential":                            false,
		"!aws codecommit credential-helper $@":                                 false,
		"/usr/local/share/gcm-core/git-credential-manager":                     false,
		"ssh -i ~/.ssh/id_ed25519":                                             false,
		"ssh -i ${HOME}/.ssh/id_ed25519 -o IdentitiesOnly=yes":                 false,
		"ssh -o 'IdentityAgent=~/Library/Group Containers/x.agent/agent.sock'": false,
		`ssh -i "/home/u/my keys/id"`:                                          false,
		"ssh -F none -vvv -p2222":                                              false,
		"ssh -o ProxyCommand=none":                                             false,
		"ssh -o 'ProxyCommand=ssh -W %h:%p bastion'":                           false,
		"ssh -F/home/u/.ssh/work_config":                                       false,
		"/r/application/cred.sh":                                               false,

		// 1: a relative path, which git resolves in the repository.
		"ssh -F .ssh/config":                true,
		"sh scripts/ssh.sh":                 true,
		"!scripts/cred.sh":                  true,
		"store --file=.git-credentials":     true,
		"store --file .git-credentials":     true,
		"ssh -F ssh_config":                 true,
		"ssh -i key":                        true,
		"sh ssh.sh":                         true,
		"bash -c ssh.sh":                    true,
		"python3 cred.py":                   true,
		"env FOO=1 sh cred.sh":              true,
		"ssh -o IdentityFile=key":           true,
		"ssh -oUserKnownHostsFile=kh":       true,
		"ssh -o 'ProxyCommand=sh proxy.sh'": true,
		"sh -c 'ssh -F ./c \"$@\"'":         true,
		"~user/x/cred":                      true,

		// 2: a short flag written together with its value.
		"ssh -F/r/app/ssh_config":  true,
		"ssh -i/r/app/key":         true,
		"ssh -ikey":                true,
		"ssh -I/w/feat app/p11.so": true,
		"ssh -Z/r/app/x":           true,

		// 3: a path with spaces, quoted or escaped.
		`ssh -F "/w/feat app/ssh_config"`: true,
		`ssh -F '/w/feat app/ssh_config'`: true,
		`ssh -F /w/feat\ app/ssh_config`:  true,
		"ssh -F /w/feat app/ssh_config":   true,

		// 4: shell syntax nerdr does not follow.
		"sh <./ssh.sh":           true,
		"sh < /tmp/x":            true,
		"X=/r/app/s.sh; $X":      true,
		"X=/r/app/s.sh ssh":      true,
		"ssh $SSH_OPTS":          true,
		`ssh "$SSH_OPTS"`:        true,
		"ssh `cat /tmp/x`":       true,
		"ssh $(cat /tmp/x)":      true,
		"ssh -F /r/app/*":        true,
		"ssh && /tmp/x":          true,
		"ssh | tee":              true,
		"ssh # comment":          true,
		"ssh\n/r/app/x":          true,
		"ssh -F 'unterminated":   true,
		"!f() { /r/app/x; }; f":  true,
		"ssh -F ~/../../r/app/c": true,
	} {
		if got := namesFileIn(command, roots); got != want {
			t.Errorf("%q: %v, want %v", command, got, want)
		}
	}
}

// TestNamesFileInResolvesLinks covers 5: a repository reached through a
// link (macOS's /var and /private/var), and a path that leads into the
// repository through one.
func TestNamesFileInResolvesLinks(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "real", "repo")
	if err := os.MkdirAll(filepath.Join(repo, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(filepath.Join(base, "real"), alias); err != nil {
		t.Fatal(err)
	}
	into := filepath.Join(base, "into")
	if err := os.Symlink(filepath.Join(repo, "tools"), into); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		root, command string
	}{
		{filepath.Join(alias, "repo"), "ssh -F " + filepath.Join(repo, "tools", "c")},
		{repo, "ssh -F " + filepath.Join(alias, "repo", "tools", "c")},
		{repo, "sh " + filepath.Join(into, "ssh.sh")},
		{repo, "sh " + filepath.Join(into, "not-yet", "ssh.sh")},
	} {
		if !namesFileIn(c.command, []string{c.root}) {
			t.Errorf("root %s: %q let through", c.root, c.command)
		}
	}
	if namesFileIn("ssh -F "+filepath.Join(base, "real", "elsewhere"), []string{filepath.Join(alias, "repo")}) {
		t.Error("a file beside the repository was taken for one in it")
	}
}

// TestProgramIn covers GIT_SSH, a program git runs without sh.
func TestProgramIn(t *testing.T) {
	roots := []string{"/r/app"}
	for program, want := range map[string]bool{
		"ssh":             false,
		"/usr/bin/plink":  false,
		"/r/app/ssh":      true,
		"bin/ssh":         true,
		"./ssh":           true,
		"/r/app/my ssh":   true,
		"/opt/my ssh/ssh": false,
	} {
		if got := programIn(program, roots); got != want {
			t.Errorf("%q: %v, want %v", program, got, want)
		}
	}
}

// TestShellWords covers the quoting namesFileIn reads.
func TestShellWords(t *testing.T) {
	for s, want := range map[string]string{
		`a 'b c' "d e" f\ g`: "a|b c|d e|f g",
		`a"b"'c'`:            "abc",
		`"\$x \"y\" \a"`:     `$x "y" \a`,
		`x $HOME ${HOME}/y`:  "x|/h|/h/y",
		`x $HOMEY`:           "!",
		`"$@" $1`:            "get|get",
		`'$(x)'`:             "$(x)",
		"":                   "",
	} {
		words, ok := shellWords(s, "/h")
		got := strings.Join(words, "|")
		if !ok {
			got = "!"
		}
		if got != want {
			t.Errorf("%q: %q, want %q", s, got, want)
		}
	}
}

// TestOriginEnvChecksTheEnvironmentsSSH covers 6: the GIT_SSH_COMMAND and
// GIT_SSH that the fetch from origin keeps are judged as the config's.
func TestOriginEnvChecksTheEnvironmentsSSH(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	run(t, root, "init", "-q", repo)
	run(t, repo, "remote", "add", "origin", "git@example.com:o/r.git")
	SandboxedRepo(repo)
	for _, kv := range [][2]string{
		{"GIT_SSH_COMMAND", "ssh -F " + filepath.Join(repo, "ssh_config")},
		{"GIT_SSH_COMMAND", "sh ssh.sh"},
		{"GIT_SSH", filepath.Join(repo, "ssh")},
		{"GIT_SSH", "tools/ssh"},
	} {
		t.Run(kv[0]+"="+kv[1], func(t *testing.T) {
			t.Setenv(kv[0], kv[1])
			if env, why := OriginEnv(repo); why == "" || env != nil {
				t.Fatalf("let through: %q", why)
			} else if !strings.Contains(why, kv[0]) {
				t.Fatalf("why does not name %s: %q", kv[0], why)
			}
		})
	}
	t.Setenv("GIT_SSH_COMMAND", "ssh -i ~/.ssh/id_ed25519")
	t.Setenv("GIT_SSH", "ssh")
	if _, why := OriginEnv(repo); why != "" {
		t.Fatalf("the user's own ssh was refused: %s", why)
	}
}

// TestOriginEnvRefusesConfiguredDelegatesInTheRepository covers the
// config's ssh command and credential helpers end to end, written in the
// ways 1-4 list.
func TestOriginEnvRefusesConfiguredDelegatesInTheRepository(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	run(t, root, "init", "-q", repo)
	run(t, repo, "remote", "add", "origin", "git@example.com:o/r.git")
	SandboxedRepo(repo)
	for _, kv := range [][2]string{
		{"core.sshCommand", "ssh -F .ssh/config"},
		{"core.sshCommand", "ssh -F" + filepath.Join(repo, "c")},
		{"core.sshCommand", "sh <./ssh.sh"},
		{"credential.helper", "store --file=.git-credentials"},
		{"credential.https://example.com.helper", "!scripts/cred.sh"},
	} {
		run(t, repo, "config", kv[0], kv[1])
		if _, why := OriginEnv(repo); why == "" {
			t.Errorf("%s = %s let through", kv[0], kv[1])
		}
		run(t, repo, "config", "--unset-all", kv[0])
	}
	run(t, repo, "config", "credential.helper", "!gh auth git-credential")
	run(t, repo, "config", "core.sshCommand", "ssh -i ~/.ssh/id_ed25519")
	if _, why := OriginEnv(repo); why != "" {
		t.Fatalf("the user's own settings were refused: %s", why)
	}
}
