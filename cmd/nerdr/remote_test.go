package main

import (
	"slices"
	"strings"
	"testing"
)

func TestParseRemote(t *testing.T) {
	for _, c := range []struct{ arg, host, folder string }{
		{"box", "box", "~"},
		{"box:", "box", "~"},
		{"me@box.lan:~/src/app", "me@box.lan", "~/src/app"},
		{"box:/srv/code", "box", "/srv/code"},
	} {
		host, folder, err := parseRemote(c.arg)
		if err != nil || host != c.host || folder != c.folder {
			t.Errorf("%q: %q %q %v", c.arg, host, folder, err)
		}
	}
	for _, bad := range []string{"", ":x", "-oProxyCommand=sh:x", "box:x'; rm -rf ~", "box:$(id)", "box:-x", "b ox:x", "box:a\nb"} {
		if h, f, err := parseRemote(bad); err == nil {
			t.Errorf("%q taken as %q %q", bad, h, f)
		}
	}
}

// TestSSHArgv: no agent or display is forwarded, a connection again never
// asks for a password, and nerdr is found by the login shell there.
func TestSSHArgv(t *testing.T) {
	t.Setenv(EnvSSH, "")
	argv := sshArgv("box", "~/src", false)
	if argv[0] != "ssh" || !slices.Contains(argv, "ForwardAgent=no") || !slices.Contains(argv, "ForwardX11=no") || slices.Contains(argv, "BatchMode=yes") {
		t.Fatalf("%q", argv)
	}
	i := slices.Index(argv, "--")
	if i < 0 || argv[i+1] != "box" || argv[i+2] != `exec "$SHELL" -lc "exec nerdr bridge '~/src'"` || len(argv) != i+3 {
		t.Fatalf("%q", argv)
	}
	if again := sshArgv("box", "~", true); !slices.Contains(again, "BatchMode=yes") {
		t.Fatalf("again: %q", again)
	}
	t.Setenv(EnvSSH, "/x/fakessh -v")
	if argv := sshArgv("box", "~", false); argv[0] != "/x/fakessh" || argv[1] != "-v" {
		t.Fatalf("%q", argv)
	}
}

// TestNoRemoteFromAProgramInATab: a nerdr an agent started does not
// connect to another machine; one in the user's own terminal of a tab
// does.
func TestNoRemoteFromAProgramInATab(t *testing.T) {
	t.Setenv("NERDR_BY_AGENT", "")
	t.Setenv("NERDR", "")
	t.Setenv("NERDR_SHELL", "")
	t.Setenv("NERDR_TAB", "t1")
	if remoteAllowed() == nil {
		t.Error("an agent's nerdr connects to another machine")
	}
	t.Setenv("NERDR_SHELL", "1")
	if err := remoteAllowed(); err != nil {
		t.Errorf("a terminal of a tab: %v", err)
	}
}

func TestRemoteFailedSaysWhatToDo(t *testing.T) {
	for said, want := range map[string]string{
		"nerdr: no command \"bridge\"\n\nnerdr — agents…":      "too old for nerdr connect: update nerdr on box",
		"bash: line 1: nerdr: command not found":               "no nerdr on box",
		"ssh: connect to host box port 22: Connection refused": "box: ssh: connect to host box port 22: Connection refused",
	} {
		if got := remoteFailed("box", said, nil).Error(); !strings.Contains(got, want) {
			t.Errorf("%q: %q", said, got)
		}
	}
	if got := versionMismatch("box", 0, "v0.1.0").Error(); !strings.HasSuffix(got, "update nerdr on box") {
		t.Errorf("older there: %q", got)
	}
	if got := versionMismatch("box", 99, "v9").Error(); !strings.HasSuffix(got, "update nerdr here") {
		t.Errorf("newer there: %q", got)
	}
}
