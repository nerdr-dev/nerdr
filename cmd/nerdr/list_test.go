package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/api"
	"github.com/nerdr-dev/nerdr/internal/attach"
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

	all, alive, failed := listRunning(dir)
	if len(all) != 2 || all[0].Root != "/w/a" || all[1].Root != "/w/b" {
		t.Fatalf("listed %+v", all)
	}
	if b := all[1]; b.Tabs != 2 || b.Agents != 2 || b.Version != "test" || b.API != core.APIVersion {
		t.Errorf("/w/b: %+v", b)
	}
	if len(failed) != 1 || failed[0] != "not you" {
		t.Errorf("failed %q", failed)
	}
	// The one that refused runs all the same; the dead one does not.
	for name, want := range map[string]bool{"api-1": true, "api-2": true, "api-3": true, "api-4": false} {
		if alive[filepath.Join(dir, name+".sock")] != want {
			t.Errorf("%s alive: %v", name, !want)
		}
	}

	if all, alive, failed := listRunning(t.TempDir()); all == nil || len(all) != 0 || len(alive) != 0 || failed != nil {
		t.Errorf("an empty folder: %+v, %q", all, failed)
	}
}

// TestAPITabs: outside nerdr, nerdr tabs FOLDER asks the API of the core
// running for the folder; with none, it says to start one.
func TestAPITabs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NERDR_CLIENT_DIR", dir)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := apiTabs(root); err == nil || !strings.Contains(err.Error(), "no nerdr runs") {
		t.Fatalf("no core: %v", err)
	}
	sock, _, err := attach.APIPaths(root)
	if err != nil {
		t.Fatal(err)
	}
	listCore(t, dir, strings.TrimSuffix(filepath.Base(sock), ".sock"), root, 2, "")
	tabs, err := apiTabs(root)
	if err != nil || len(tabs) != 2 || tabs[0].ID != "t1" {
		t.Fatalf("tabs %+v, %v", tabs, err)
	}
}

// TestTabsInATabTakesNoFolder: in a tab, nerdr tabs goes by the tab's
// socket alone, never by the API, and takes no FOLDER.
func TestTabsInATabTakesNoFolder(t *testing.T) {
	t.Setenv("NERDR", "1")
	t.Setenv("NERDR_TAB", "")
	t.Setenv("NERDR_SOCKET", "")
	if !inNerdr() {
		t.Fatal("NERDR=1 is in nerdr")
	}
	if got := runTabs([]string{"/tmp"}); got != 2 {
		t.Fatalf("a FOLDER in a tab: exit %d", got)
	}
	// Without the tab's socket it fails, rather than ask the API.
	if got := runTabs(nil); got != 1 {
		t.Fatalf("no socket: exit %d", got)
	}
}

// TestListSaved: a folder with saved tabs is listed while no nerdr runs
// for it; not one with no tabs, one gone, one that runs, nor a file that
// is not a session nerdr saved for its folder.
func TestListSaved(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	sessions := state.Dir()
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(path, root string, tabs int) {
		s := state.Session{Root: root, Tabs: []state.Tab{}}
		for i := 0; i < tabs; i++ {
			s.Tabs = append(s.Tabs, state.Tab{ID: "t", Name: "tab", Dir: root})
		}
		b, _ := json.Marshal(s)
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stopped, empty, runs, copied := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	write(state.Path(stopped), stopped, 2)
	write(state.Path(empty), empty, 0)
	write(state.Path(runs), runs, 1)
	write(state.Path("/no/such/folder"), "/no/such/folder", 3)
	write(filepath.Join(sessions, "copy.json"), copied, 1)
	if err := os.WriteFile(filepath.Join(sessions, "claude-limits.json"), []byte(`{"five_hour":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessions, "bad-1234.json"), []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}

	got := listSaved(sessions, func(root string) bool { return root == runs })
	if len(got) != 1 || got[0].Root != stopped || got[0].Tabs != 2 || got[0].Running || got[0].Saved == nil {
		t.Fatalf("listed %+v", got)
	}
	b, _ := json.Marshal(got[0])
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if _, ok := m["version"]; ok || m["running"] != false || m["saved"] == nil {
		t.Errorf("a stopped folder as JSON: %s", b)
	}

	if got := listSaved(filepath.Join(sessions, "none"), func(string) bool { return false }); got == nil || len(got) != 0 {
		t.Errorf("no sessions folder: %+v", got)
	}
}

// TestCoreRuns: a folder is running when its API socket is alive, when
// its core listens at the client socket with no API socket, and stopped
// only when neither is there.
func TestCoreRuns(t *testing.T) {
	dir, err := os.MkdirTemp("", "nruns")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv(attach.EnvDir, dir)
	if coreRuns("/w/a", map[string]bool{}) {
		t.Error("nothing there, and it runs")
	}
	apiSock, _, _ := attach.APIPaths("/w/a")
	if !coreRuns("/w/a", map[string]bool{apiSock: true}) {
		t.Error("its API socket is alive, and it does not run")
	}
	sock, _, _ := attach.Paths("/w/a")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	if !coreRuns("/w/a", map[string]bool{}) {
		t.Error("its core listens with no API socket, and it does not run")
	}
}

// TestListRunningWithNoKey: an API socket listened at, with its key file
// gone, is a core alive, not one dead.
func TestListRunningWithNoKey(t *testing.T) {
	dir, err := os.MkdirTemp("", "nlist")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	listCore(t, dir, "api-1", "/w/a", 1, "")
	if err := os.Remove(filepath.Join(dir, "api-1.key")); err != nil {
		t.Fatal(err)
	}
	all, alive, failed := listRunning(dir)
	if len(all) != 0 || !alive[filepath.Join(dir, "api-1.sock")] || len(failed) != 1 {
		t.Fatalf("listed %+v, alive %v, failed %q", all, alive, failed)
	}
}
