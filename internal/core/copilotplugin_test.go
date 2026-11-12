package core

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/statedir"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// copilotTab is a core whose copilot is sandboxed as sandboxed says, with
// the install setting install, and a tab of copilot's in a repository.
func copilotTab(t *testing.T, install string, sandboxed bool) (*Core, *Tab, string) {
	t.Helper()
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	m := &Core{}
	m.Cfg.Install = install
	m.Cfg.Agents = map[agents.Kind]agents.Spec{agents.Copilot: {Sandboxed: sandboxed}}
	tb := &Tab{AgentState: Agent{Tab: state.Tab{ID: "1", Name: "c", Agent: agents.Copilot, Dir: repo,
		Worktrees: []workspace.Worktree{{Repo: repo, Path: repo, Branch: "main"}}}}}
	return m, tb, repo
}

// With install = "home", the plugin given to copilot is chosen as with
// "worktree": the worktree's for one in a sandbox, kept out of git,
// nerdr's own in the state folder for one outside; never one in the home
// folder, which a sandbox may let its copilot write.
func TestCopilotPluginWithInstallHomeIsNotInTheHomeFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, install := range []string{config.InstallHome, config.InstallWorktree} {
		t.Run(install, func(t *testing.T) {
			m, tb, repo := copilotTab(t, install, true)
			if failed := m.projectSkillsOf(tb).install(); len(failed) > 0 {
				t.Fatalf("install: %q", failed)
			}
			tb.InSandbox = true
			if got := m.CopilotPlugin(tb); got != hooks.CopilotPluginProject(repo) {
				t.Fatalf("plugin for the sandboxed copilot: %q", got)
			}
			out, err := exec.Command("git", "-C", repo, "status", "--porcelain", "--untracked-files=all").Output()
			if err != nil || strings.Contains(string(out), "nerdr-plugin") {
				t.Errorf("the plugin is not kept out of git: %q %v", out, err)
			}
			tb.InSandbox = false
			got := m.CopilotPlugin(tb)
			if got != CopilotPluginState() || !strings.HasPrefix(got, statedir.Dir()) || !hooks.HasCopilotPlugin(got) {
				t.Fatalf("plugin for the copilot outside a sandbox: %q, want %q", got, CopilotPluginState())
			}
			if hooks.HasCopilotPlugin(hooks.CopilotPluginHome(home)) {
				t.Fatal("a plugin was put in the home folder")
			}
		})
	}
}

// A copilot outside a sandbox, with install = "home", has nothing put in
// its worktree; with "off" none is given at all.
func TestCopilotPluginOutsideASandboxAndOff(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m, tb, repo := copilotTab(t, config.InstallHome, false)
	if failed := m.projectSkillsOf(tb).install(); len(failed) > 0 {
		t.Fatalf("install: %q", failed)
	}
	if hooks.HasCopilotPlugin(hooks.CopilotPluginProject(repo)) {
		t.Error("a plugin in the worktree of a copilot outside a sandbox")
	}
	if got := m.CopilotPlugin(tb); got != CopilotPluginState() {
		t.Errorf("plugin: %q", got)
	}
	m.Cfg.Install = config.InstallOff
	for _, in := range []bool{false, true} {
		tb.InSandbox = in
		if got := m.CopilotPlugin(tb); got != "" {
			t.Errorf("in a sandbox %v, with install off: %q", in, got)
		}
	}
}
