//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// click presses and lets go of the left button (or button b) on the first
// place the screen shows s, as a terminal reports it (SGR).
func (h *harness) click(s string, b ...int) {
	h.t.Helper()
	h.clickFrom(0, s, b...)
}

// clickFrom is click on the first place s is shown from row y0 on.
func (h *harness) clickFrom(y0 int, s string, b ...int) {
	h.t.Helper()
	h.waitFor(s, 3*time.Second)
	btn := 0
	if len(b) > 0 {
		btn = b[0]
	}
	for y, line := range strings.Split(h.screen(), "\n") {
		if y < y0 {
			continue
		}
		if i := strings.Index(line, s); i >= 0 {
			x := ansi.StringWidth(line[:i]) + 1
			h.keys(fmt.Sprintf("\x1b[<%d;%d;%dM\x1b[<%d;%d;%dm", btn, x, y+1, btn, x, y+1))
			return
		}
	}
	h.t.Fatalf("%q is not on the screen:\n%s", s, h.screen())
}

// TestMouse makes a tab and works it with the mouse alone, but for the
// branch name.
func TestMouse(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	fake, _ := filepath.Abs("fakeagent.sh")
	cfgDir := filepath.Join(scratch, "config")
	_ = os.MkdirAll(filepath.Join(cfgDir, "nerdr"), 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "nerdr", "config.toml"),
		[]byte("worktrees_dir = \"worktrees\"\ntab_bar = \"top\"\ninstall = \"off\"\n[agents.claude]\ncommand = [\""+fake+"\"]\n"), 0o644)
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HERDR_") {
			env = append(env, kv)
		}
	}
	env = append(env,
		"XDG_CONFIG_HOME="+cfgDir,
		"XDG_STATE_HOME="+filepath.Join(scratch, "state"),
		"FAKE_LOG="+filepath.Join(scratch, "agent.log"),
		"TERM=xterm-256color",
	)
	h := start(t, bin, root, env)

	// The new-tab dialog: click a repository to pick it, then its buttons.
	h.waitFor("Repositories in", 10*time.Second)
	h.click("repoB")
	h.waitFor("[✓] repoB", time.Second)
	h.click("enter continue")
	h.waitFor("branch:", 2*time.Second)
	h.keys("feat-m")
	h.click("enter continue")
	h.waitFor("Agent:", 2*time.Second)
	h.click("1  claude")
	h.waitFor("? for shortcuts", 10*time.Second)
	h.waitTabBar("1:feat-m", time.Second)

	// The tab row's +: a new tab in the same repository, at its branch.
	h.clickFrom(0, "+") // one repository: the tab row is the top one
	h.waitFor("New worktree for repoB", 2*time.Second)
	h.keys("feat-n")
	h.click("enter continue")
	h.click("1  claude")
	h.waitTabBar("2:feat-n", 10*time.Second)

	// Clicking a tab goes to it.
	h.click("1:feat-m")
	h.waitTabBar("1:feat-m claude", 2*time.Second)

	// A right click on a tab: its menu, where Rename… renames it.
	h.click("2:feat-n", 2)
	h.waitFor("Close tab…", 2*time.Second)
	h.click("Rename…")
	h.waitFor("Rename tab", 2*time.Second)
	h.keys(strings.Repeat("\x7f", 10) + "renamed")
	h.click("enter save")
	h.waitTabBar("2:renamed", 2*time.Second)

	// ≡ has everything; a click outside puts it away.
	h.click("≡ menu")
	h.waitFor("Overview and navigation", 2*time.Second)
	h.keys("\x1b[<0;3;38M\x1b[<0;3;38m")
	h.waitGone("Overview and navigation", 2*time.Second)
	h.click("≡ menu")
	h.keys(strings.Repeat("\x1b[6~", 10)) // scroll to Settings and help
	h.waitFor("All commands", 2*time.Second)
	h.click("All commands")
	h.waitFor("type to filter", 2*time.Second)
	h.keys("\x1b")
	h.waitGone("type to filter", 2*time.Second)
}

// TestMouseNewTabFromGroupMenu opens a tab for another repository from
// ≡'s New tab, and one for the same repository from the group's menu.
func TestMouseNewTabFromGroupMenu(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	fake, _ := filepath.Abs("fakeagent.sh")
	cfgDir := filepath.Join(scratch, "config")
	_ = os.MkdirAll(filepath.Join(cfgDir, "nerdr"), 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "nerdr", "config.toml"),
		[]byte("worktrees_dir = \"worktrees\"\ntab_bar = \"top\"\ninstall = \"off\"\n[agents.claude]\ncommand = [\""+fake+"\"]\n"), 0o644)
	env := append(os.Environ(),
		"XDG_CONFIG_HOME="+cfgDir,
		"XDG_STATE_HOME="+filepath.Join(scratch, "state"),
		"FAKE_LOG="+filepath.Join(scratch, "agent.log"),
		"TERM=xterm-256color",
	)
	h := start(t, bin, root, env)
	h.waitFor("Repositories in", 10*time.Second)
	h.click("repoA")
	h.click("enter continue")
	h.keys("one")
	h.click("enter continue")
	h.click("1  claude")
	h.waitTabBar("1:one", 10*time.Second)

	// With one repository there is no group bar, and the tab row's + is
	// for that repository: ≡'s New tab is for any.
	h.click("≡ menu")
	h.click("New tab")
	h.waitFor("Repositories in", 2*time.Second)
	h.click("repoB")
	h.click("enter continue")
	h.keys("two")
	h.click("enter continue")
	h.click("1  claude")
	h.waitTabBar("repoB (1)", 10*time.Second)

	// Right click on repoA's group: a new tab in repoA.
	h.click("repoA (1)", 2)
	h.click("New tab in repoA…")
	h.waitFor("New worktree for repoA", 2*time.Second)
	h.keys("three")
	h.click("enter continue")
	h.click("1  claude")
	h.waitTabBar("repoA (2)", 10*time.Second)
	if _, err := os.Stat(filepath.Join(root, "worktrees", "three-repoA", "README.md")); err != nil {
		t.Fatalf("worktree not made: %v", err)
	}
}
