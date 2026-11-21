package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/api"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// listCore serves the API of a core for root with tabs tabs, at the socket
// called name in dir; refuse, when set, is why it refuses who connects.
func listCore(t *testing.T, dir, name, root string, tabs int, refuse string) {
	t.Helper()
	m := &core.Core{Root: root, Agents: []agents.Kind{agents.Claude}, AgentsAt: time.Now()}
	for i := 0; i < tabs; i++ {
		tab := &core.Tab{}
		tab.Tab = state.Tab{ID: "t" + string(rune('1'+i)), Name: "tab", Dir: root, Agent: agents.Claude}
		tab.Light = core.StWorking
		m.Tabs = append(m.Tabs, tab)
	}
	s, err := api.Listen(filepath.Join(dir, name+".sock"), filepath.Join(dir, name+".key"), api.Config{
		Version: "test", Root: root,
		Check: func(hooks.Peer) string { return refuse },
		Post: func(c core.Call) {
			go func() {
				m.Mu.Lock()
				defer m.Mu.Unlock()
				c.F(m)
			}()
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
}

// TestListRunning: each core that answers is listed with its folder and
// what it runs, in order of folder; a socket left by a core that died is
// passed by, and one that refuses is said why of.
func TestListRunning(t *testing.T) {
	dir, err := os.MkdirTemp("", "nlist")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	listCore(t, dir, "api-1", "/w/b", 2, "")
	listCore(t, dir, "api-2", "/w/a", 0, "")
	listCore(t, dir, "api-3", "/w/c", 1, "not you")
	dead, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, "api-4.sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	dead.SetUnlinkOnClose(false)
	_ = dead.Close()

	all, failed := listRunning(dir)
	if len(all) != 2 || all[0].Root != "/w/a" || all[1].Root != "/w/b" {
		t.Fatalf("listed %+v", all)
	}
	if b := all[1]; b.Tabs != 2 || b.Agents != 2 || b.Version != "test" || b.API != core.APIVersion {
		t.Errorf("/w/b: %+v", b)
	}
	if len(failed) != 1 || failed[0] != "not you" {
		t.Errorf("failed %q", failed)
	}

	if all, failed := listRunning(t.TempDir()); all == nil || len(all) != 0 || failed != nil {
		t.Errorf("an empty folder: %+v, %q", all, failed)
	}
}
