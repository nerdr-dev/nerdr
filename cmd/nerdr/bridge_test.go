package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// fakeShell is a login shell that says something first, as the files a
// shell reads may, then prints out as the environment.
func fakeShell(t *testing.T, out string) string {
	t.Helper()
	sh := filepath.Join(t.TempDir(), "sh")
	script := "#!/bin/sh\necho 'Welcome to box'\nprintf '" + out + "'\n"
	if err := os.WriteFile(sh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return sh
}

// TestLoginEnvIsTheLoginShellsLessTheSession: the programs a core started
// over ssh runs get the environment of the user's login shell there, with
// what names the ssh session, and a forwarded agent, left out.
func TestLoginEnvIsTheLoginShellsLessTheSession(t *testing.T) {
	t.Setenv("SHELL", fakeShell(t, `\0NERDR-ENV\0PATH=/home/me/.local/bin:/usr/bin\0SSH_AUTH_SOCK=/tmp/ssh-x/agent.1\0SSH_CONNECTION=1 2 3 4\0DISPLAY=localhost:10\0HOME=/home/me`))
	env, why := loginEnv()
	if why != "" {
		t.Fatal(why)
	}
	if !slices.Equal(env, []string{"PATH=/home/me/.local/bin:/usr/bin", "HOME=/home/me"}) {
		t.Fatalf("env %q", env)
	}
}

// TestLoginEnvFallsBackToItsOwn: a shell that says nothing of use leaves
// the programs this session's environment, the session's own left out,
// and says why.
func TestLoginEnvFallsBackToItsOwn(t *testing.T) {
	t.Setenv("SHELL", fakeShell(t, "nothing"))
	t.Setenv("SSH_AUTH_SOCK", "/tmp/ssh-x/agent.1")
	t.Setenv("NERDR_TEST_OWN", "1")
	env, why := loginEnv()
	if why == "" || !slices.Contains(env, "NERDR_TEST_OWN=1") || slices.Contains(env, "SSH_AUTH_SOCK=/tmp/ssh-x/agent.1") {
		t.Fatalf("why %q, env has own %v, agent %v", why, slices.Contains(env, "NERDR_TEST_OWN=1"), slices.Contains(env, "SSH_AUTH_SOCK=/tmp/ssh-x/agent.1"))
	}
}

func TestBridgeRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Mkdir(filepath.Join(home, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(filepath.Join(home, "src"))
	if got, err := bridgeRoot("~/src"); err != nil || got != real {
		t.Fatalf("got %q %v", got, err)
	}
	if _, err := bridgeRoot("~/nope"); err == nil {
		t.Fatal("a folder that is not there was taken")
	}
}
