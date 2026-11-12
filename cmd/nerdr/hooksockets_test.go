package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/rundir"
)

// TestAgentsInAndOutOfASandboxGetSocketsApart: nerdr listens on the
// socket a sandbox is told of and on one of the folder's own beside the
// client sockets, and a second nerdr gets a pair of its own.
func TestAgentsInAndOutOfASandboxGetSocketsApart(t *testing.T) {
	dir, err := os.MkdirTemp("", "nh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("HOME", dir)
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "s"))
	t.Setenv(rundir.EnvDir, filepath.Join(dir, "r"))

	first := listenHooks("/repo/a", func(hooks.Report) {})
	defer first.close()
	if first.sandboxErr != nil || first.hostErr != nil {
		t.Fatal(first.sandboxErr, first.hostErr)
	}
	if first.sandbox.Path() != hooks.SocketPath() || filepath.Dir(first.host.Path()) != filepath.Join(dir, "r") {
		t.Fatalf("sockets %s and %s", first.sandbox.Path(), first.host.Path())
	}
	second := listenHooks("/repo/b", func(hooks.Report) {})
	defer second.close()
	if !errors.Is(second.taken, hooks.ErrTaken) || second.sandbox == nil || second.host == nil {
		t.Fatalf("second nerdr: %+v", second)
	}
	paths := []string{first.sandbox.Path(), first.host.Path(), second.sandbox.Path(), second.host.Path()}
	slices.Sort(paths)
	if len(slices.Compact(paths)) != 4 {
		t.Fatalf("sockets shared: %q", paths)
	}

	var c core.Core
	c.Client = core.NoClient{}
	first.give(&c, func(core.Event) {})
	if c.HookSock != first.sandbox.Path() || c.HostSock != first.host.Path() {
		t.Fatalf("given %s and %s", c.HookSock, c.HostSock)
	}
}
