package gitsafe

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func delegateScript(t *testing.T, wt string) (string, string) {
	t.Helper()
	marker := filepath.Join(wt, "delegate-ran")
	script := filepath.Join(wt, "delegate")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf ran > '"+marker+"'\nprintf 'username=test\\npassword=test\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return script, marker
}

func TestSandboxAuthDelegatesCannotExecute(t *testing.T) {
	for _, key := range []string{"core.sshCommand", "credential.helper", "credential.https://example.invalid.helper"} {
		t.Run(key, func(t *testing.T) {
			_, repo, wt := repoWithWorktree(t)
			Trust(repo, wt)
			script, marker := delegateScript(t, wt)
			value := script
			args := []string{"ls-remote", "ssh://example.invalid/repo"}
			if strings.HasPrefix(key, "credential.") {
				value = "!" + script
				args = []string{"credential", "fill"}
			}
			run(t, repo, "config", key, value)
			plain := exec.Command("git", append([]string{"-C", wt}, args...)...)
			plain.Stdin = strings.NewReader("protocol=https\nhost=example.invalid\n\n")
			_, _ = plain.CombinedOutput()
			if _, err := os.Stat(marker); err != nil {
				t.Fatal("baseline did not execute delegate")
			}
			os.Remove(marker)
			Sandboxed(wt)
			cmd := Command(wt, args...)
			cmd.Stdin = strings.NewReader("protocol=https\nhost=example.invalid\n\n")
			if out, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("delegate accepted: %s", out)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("delegate ran: %v", err)
			}
			if out, err := Command(wt, "status", "--porcelain").CombinedOutput(); err != nil {
				t.Fatalf("disabled authentication broke local Git: %v %s", err, out)
			}
		})
	}
}

func TestSandboxExecutableConfigurationRefused(t *testing.T) {
	for _, key := range []string{"core.alternateRefsCommand", "core.gitProxy", "gc.recentObjectsHook", "gpg.custom.program", "remote.origin.uploadpack", "remote.origin.receivepack", "remote.origin.vcs", "uploadpack.packObjectsHook"} {
		t.Run(key, func(t *testing.T) {
			_, repo, wt := repoWithWorktree(t)
			Trust(repo, wt)
			script, marker := delegateScript(t, wt)
			run(t, repo, "config", key, script)
			Sandboxed(wt)
			if out, err := Command(wt, "status", "--porcelain").CombinedOutput(); err == nil {
				t.Fatalf("accepted %s: %s", key, out)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("delegate ran: %v", err)
			}
		})
	}
}

func TestSandboxTransportPolicySurvivesRewritesAndOverrides(t *testing.T) {
	_, repo, wt := repoWithWorktree(t)
	Trust(repo, wt)
	Sandboxed(wt)
	run(t, repo, "config", "protocol.ext.allow", "always")
	run(t, repo, "config", "url.ext::sh -c false.insteadOf", "rewrite:")
	t.Setenv("GIT_ALLOW_PROTOCOL", "file:ssh:https:ext:custom")
	for _, target := range []string{repo, "ssh://example.invalid/repo", "https://example.invalid/repo", "custom::repo", "ext::sh -c false", "rewrite:repo"} {
		out, err := Command(wt, "ls-remote", target).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "not allowed") {
			t.Fatalf("transport %q: %v %s", target, err, out)
		}
	}
}

func TestSandboxEnvironmentDelegatesAndLocalGit(t *testing.T) {
	_, repo, wt := repoWithWorktree(t)
	Trust(repo, wt)
	script, marker := delegateScript(t, wt)
	for _, key := range []string{"GIT_SSH_COMMAND", "GIT_SSH", "GIT_EXEC_PATH", "GIT_ASKPASS", "SSH_ASKPASS", "GIT_EXTERNAL_DIFF", "GIT_PAGER", "PAGER"} {
		t.Setenv(key, script)
	}
	Sandboxed(wt)
	for _, args := range [][]string{{"status", "--porcelain"}, {"diff"}, {"add", "delegate"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-m", "ordinary"}} {
		if out, err := Command(wt, args...).CombinedOutput(); err != nil {
			t.Fatalf("local Git %v: %v %s", args, err, out)
		}
	}
	if err := os.WriteFile(script, []byte("changed\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if out, err := Command(wt, "diff", "--ext-diff").CombinedOutput(); err != nil || !strings.Contains(string(out), "changed") {
		t.Fatalf("ordinary diff failed: %v %s", err, out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("environment delegate ran: %v", err)
	}
}

func TestSandboxRequiredSigningFailsWithoutUnsignedCommit(t *testing.T) {
	for _, format := range []string{"openpgp", "x509", "ssh"} {
		t.Run(format, func(t *testing.T) {
			_, repo, wt := repoWithWorktree(t)
			Trust(repo, wt)
			run(t, repo, "config", "commit.gpgSign", "true")
			run(t, repo, "config", "gpg.format", format)
			Sandboxed(wt)
			before := run(t, wt, "rev-parse", "HEAD")
			if out, err := Command(wt, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "--allow-empty", "-m", "signed").CombinedOutput(); err == nil {
				t.Fatalf("signed commit succeeded: %s", out)
			}
			if after := run(t, wt, "rev-parse", "HEAD"); after != before {
				t.Fatal("failed signing changed HEAD")
			}
		})
	}
}

func TestSandboxCredentialEnvironmentConfiguration(t *testing.T) {
	for _, mode := range []string{"count", "parameters"} {
		t.Run(mode, func(t *testing.T) {
			_, repo, wt := repoWithWorktree(t)
			Trust(repo, wt)
			script, marker := delegateScript(t, wt)
			key := "credential.https://example.invalid.helper"
			if mode == "count" {
				t.Setenv("GIT_CONFIG_COUNT", "1")
				t.Setenv("GIT_CONFIG_KEY_0", key)
				t.Setenv("GIT_CONFIG_VALUE_0", "!"+script)
			} else {
				t.Setenv("GIT_CONFIG_PARAMETERS", "'"+key+"=!"+script+"'")
			}
			Sandboxed(wt)
			cmd := Command(wt, "credential", "fill")
			cmd.Stdin = strings.NewReader("protocol=https\nhost=example.invalid\n\n")
			if out, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("credential delegate accepted: %s", out)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("environment config delegate ran: %v", err)
			}
		})
	}
}

func TestSandboxCredentialScopeWithEqualsAndQuote(t *testing.T) {
	_, repo, wt := repoWithWorktree(t)
	Trust(repo, wt)
	script, marker := delegateScript(t, wt)
	key := "credential.https://example=a'b.invalid.helper"
	run(t, repo, "config", key, "!"+script)
	input := "protocol=https\nhost=example=a'b.invalid\n\n"
	plain := exec.Command("git", "-C", wt, "credential", "fill")
	plain.Stdin = strings.NewReader(input)
	if out, err := plain.CombinedOutput(); err != nil {
		t.Fatalf("baseline credential helper: %v %s", err, out)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatalf("baseline helper did not run: %v", err)
	}
	Sandboxed(wt)
	cmd := Command(wt, "credential", "fill")
	cmd.Stdin = strings.NewReader(input)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("credential helper accepted: %s", out)
	}
	for _, args := range [][]string{{"status", "--porcelain"}, {"diff"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "--allow-empty", "-m", "ordinary"}} {
		if out, err := Command(wt, args...).CombinedOutput(); err != nil {
			t.Fatalf("credential scope broke local Git %v: %v %s", args, err, out)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("helper ran: %v", err)
	}
}

func TestSandboxGlobalCredentialHelpersKeepLocalGitUsable(t *testing.T) {
	_, repo, wt := repoWithWorktree(t)
	Trust(repo, wt)
	script, marker := delegateScript(t, wt)
	global := filepath.Join(t.TempDir(), "gitconfig")
	run(t, repo, "config", "--file", global, "credential.helper", "!"+script)
	run(t, repo, "config", "--file", global, "credential.https://example.invalid.helper", "!"+script)
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	Sandboxed(wt)
	for _, args := range [][]string{{"status", "--porcelain"}, {"diff"}, {"add", "delegate"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-m", "ordinary"}} {
		if out, err := Command(wt, args...).CombinedOutput(); err != nil {
			t.Fatalf("global credentials broke local Git %v: %v %s", args, err, out)
		}
	}
	// Use Env directly, as gh does for its child Git processes.
	cmd := exec.Command("git", "-C", wt, "credential", "fill")
	cmd.Env = Env(wt)
	cmd.Stdin = strings.NewReader("protocol=https\nhost=example.invalid\n\n")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("global helper accepted: %s", out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("global helper ran: %v", err)
	}
}

func TestSandboxPreservesInheritedSigningRequirement(t *testing.T) {
	_, repo, wt := repoWithWorktree(t)
	Trust(repo, wt)
	before := run(t, wt, "rev-parse", "HEAD")
	t.Setenv("GIT_CONFIG_PARAMETERS", "'commit.gpgSign=true'")
	Sandboxed(wt)
	if out, err := Command(wt, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "--allow-empty", "-m", "must be signed").CombinedOutput(); err == nil {
		t.Fatalf("inherited signing requirement was discarded: %s", out)
	}
	if after := run(t, wt, "rev-parse", "HEAD"); after != before {
		t.Fatal("failed signing changed HEAD")
	}
}

func TestSandboxDisablesPagerCommands(t *testing.T) {
	_, repo, wt := repoWithWorktree(t)
	Trust(repo, wt)
	script, marker := delegateScript(t, wt)
	run(t, repo, "config", "core.pager", script)
	run(t, repo, "config", "pager.log", script)
	t.Setenv("GIT_PAGER", script)
	Sandboxed(wt)
	cmd := Command(wt, "--paginate", "log", "-1", "--oneline")
	found := false
	for _, kv := range cmd.Env {
		if kv == "GIT_PAGER=" {
			found = true
		}
	}
	if !found {
		t.Fatal("pager not explicitly disabled")
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("log failed: %v %s", err, out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("pager ran: %v", err)
	}
}

func TestSandboxGlobalSigningDelegatesKeepLocalGitUsable(t *testing.T) {
	for _, format := range []string{"openpgp", "x509", "ssh"} {
		t.Run(format, func(t *testing.T) {
			_, repo, wt := repoWithWorktree(t)
			Trust(repo, wt)
			script, marker := delegateScript(t, wt)
			global := filepath.Join(t.TempDir(), "gitconfig")
			for _, key := range []string{"gpg.program", "gpg.openpgp.program", "gpg.x509.program", "gpg.ssh.program", "gpg.ssh.defaultKeyCommand"} {
				run(t, repo, "config", "--file", global, key, script)
			}
			run(t, repo, "config", "--file", global, "gpg.format", format)
			t.Setenv("GIT_CONFIG_GLOBAL", global)
			Sandboxed(wt)
			for _, args := range [][]string{{"status", "--porcelain"}, {"diff"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "--allow-empty", "-m", "ordinary"}} {
				if out, err := Command(wt, args...).CombinedOutput(); err != nil {
					t.Fatalf("global signing config broke local Git %v: %v %s", args, err, out)
				}
			}
			run(t, repo, "config", "--file", global, "commit.gpgSign", "true")
			before := run(t, wt, "rev-parse", "HEAD")
			if out, err := Command(wt, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "--allow-empty", "-m", "signed").CombinedOutput(); err == nil {
				t.Fatalf("signing unexpectedly succeeded: %s", out)
			}
			if after := run(t, wt, "rev-parse", "HEAD"); after != before {
				t.Fatal("failed signing changed HEAD")
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("signing delegate ran: %v", err)
			}
		})
	}
}
