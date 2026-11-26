package app

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/denied"
	"github.com/nerdr-dev/nerdr/internal/fleet"
	"github.com/nerdr-dev/nerdr/internal/proc"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	"github.com/charmbracelet/x/ansi"
)

// TestCallsDialog: g counts git and gh by the tab whose worktree they
// ran in, and the rest as elsewhere.
func TestCallsDialog(t *testing.T) {
	m := testModel(t)
	tb := m.Cur()
	tb.Worktrees = []workspace.Worktree{{Repo: "/r/a", Path: "/w/zz-a", Branch: "x"}}
	before, _ := proc.Counts()
	proc.Detach(exec.Command("git", "-C", "/w/zz-a/sub", "status"))
	proc.Detach(exec.Command("git", "-C", "/w/zz-a", "diff"))
	gh := exec.Command("gh", "pr", "view")
	gh.Dir = "/w/zz-a"
	proc.Detach(gh)
	proc.Detach(exec.Command("git", "-C", "/elsewhere/zz", "status"))
	calls, _ := proc.Counts()
	for c, n := range before {
		calls[c] -= n
	}
	var mine core.CallRow
	for _, r := range m.home().Core.CallRows(calls) {
		if r.Name == tb.Name {
			mine = r
		}
	}
	if mine.Git != 2 || mine.Gh != 1 {
		t.Fatalf("the tab's calls: %+v", mine)
	}
	cmd := m.run("g l", keyMsg("l"))
	if view := ansi.Strip(m.View()); !strings.Contains(view, "asking this machine's core") {
		t.Fatalf("no dialog waiting for core:\n%s", view)
	}
	m.Update(cmd()) // core's answer
	// The tab's own commands: those of all are the whole package's tests',
	// of which its one git diff may not be among the most.
	m.dialog.(*callsDialog).pick = tb.Name
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "git and gh calls") || !strings.Contains(view, "elsewhere") || !strings.Contains(view, "git diff") {
		t.Fatalf("no dialog:\n%s", view)
	}
}

// TestCallsDialogOfACoreElsewhere: with core in a process of its own, as
// by default, g shows the calls core's process made, which it is asked
// for, and what core was turned down for is said in a toast and listed.
func TestCallsDialogOfACoreElsewhere(t *testing.T) {
	m, here, _, told := remoteTUI(t, "sh", "-c", `stty -echo; echo ready; while read l; do echo "got $l"; done`)
	pump(t, m, told, "the tab", func(v string) bool { return strings.Contains(v, "ready") })
	proc.Detach(exec.Command("git", "-C", "/w/login/zz-sub", "status"))
	// Run often enough to be among the commands the dialog lists, the
	// most run first: the package's tests run many others, more on some
	// systems than on others, and the list stops at core.CallCmdsShown.
	for range 20 {
		proc.Detach(exec.Command("git", "-C", "/w/login", "zz-calls"))
	}

	locked(m, func() { m.toastDenied() }) // what was turned down before is said already
	here.Mu.Lock()
	here.Denied = append([]denied.Denial{{What: "git fetch in /w/zz-login", Why: "BLOCKED by sandbox", N: 1, Last: time.Now()}}, here.Denied...)
	here.DeniedNoted++
	here.FlushClient()
	here.Mu.Unlock()
	pumpUntil(t, m, told, "the denial in the copy", func() bool {
		var ok bool
		locked(m, func() {
			ok = len(m.home().Core.Denied) > 0 && m.home().Core.Denied[0].What == "git fetch in /w/zz-login"
		})
		return ok
	})
	m.Update(fleet.Told{Msg: core.InfoTickMsg{}})
	if !strings.Contains(m.toastText, "Turned down: git fetch in /w/zz-login") {
		t.Fatalf("bar says %q", m.toastText)
	}

	if cmd := m.run("g l", keyMsg("l")); cmd != nil {
		if msg := cmd(); msg != nil {
			m.Update(msg)
		}
	}
	v := pump(t, m, told, "core's calls", func(v string) bool {
		return strings.Contains(v, "git zz-calls") && strings.Contains(v, "git fetch in /w/zz-login")
	})
	if !strings.Contains(v, "since nerdr's core started") && !strings.Contains(v, "Since nerdr started") {
		t.Fatalf("no start:\n%s", v)
	}
}
