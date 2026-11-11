package core

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/holder"
)

// stubTrust has TrustCodex count its calls, and answer trusted.
func stubTrust(t *testing.T) *atomic.Int32 {
	var n atomic.Int32
	was := TrustCodex
	TrustCodex = func(argv, env []string, write bool) (bool, bool, error) {
		n.Add(1)
		return true, write, nil
	}
	t.Cleanup(func() { TrustCodex = was })
	return &n
}

// TestSpawnAsksTheCLIAwayFromMu: codex, which nerdr asks whether it
// trusts nerdr's hooks before starting it, is not run by Spawn, which has
// Mu held, but by the Cmd it returns; the agent starts with the answer.
func TestSpawnAsksTheCLIAwayFromMu(t *testing.T) {
	asked := stubTrust(t)
	rec := &recorder{}
	m := &Core{Client: NoClient{}, Spawner: rec, HookSock: "/run/nerdr.sock"}
	m.Cfg.Agents = map[agents.Kind]agents.Spec{agents.Codex: {Command: []string{"cat"}}}
	tab := &Tab{}
	tab.Dir, tab.Agent = t.TempDir(), agents.Codex
	m.Tabs = []*Tab{tab}

	cmd := m.Spawn(tab, agents.Launch{Argv: []string{"cat", "--no-daemon"}}, false)
	if asked.Load() != 0 || len(rec.specs) != 0 {
		t.Fatalf("Spawn asked codex %d times, started %d", asked.Load(), len(rec.specs))
	}
	if cmd == nil {
		t.Fatal("nothing to ask codex")
	}
	ev, ok := cmd().(SpawnReady)
	if !ok || asked.Load() != 1 || !ev.Prep.CodexAsked || !ev.Prep.CodexTrusted {
		t.Fatalf("the Cmd gave %+v, asking %d times", ev, asked.Load())
	}
	m.OnSpawnReady(ev)
	if tab.Pane == nil || len(rec.specs) != 1 {
		t.Fatalf("codex did not start: %q", tab.Err)
	}
	defer tab.Pane.Close()
	if !m.CodexAsked || len(m.CodexHooks) == 0 {
		t.Error("codex's answer was not kept")
	}
	if argv := strings.Join(rec.specs[0].Argv, " "); !strings.Contains(argv, "hooks.Stop=") {
		t.Errorf("trusted, codex started without nerdr's hooks: %q", argv)
	}

	// Asked once: the next starts at once.
	next := &Tab{}
	next.Dir, next.Agent = tab.Dir, agents.Codex
	m.Tabs = append(m.Tabs, next)
	if cmd := m.Spawn(next, agents.Launch{Argv: []string{"cat", "--no-daemon"}}, false); next.Pane == nil {
		t.Fatalf("asked already, codex did not start at once (cmd %v)", cmd != nil)
	}
	defer next.Pane.Close()
	if asked.Load() != 1 {
		t.Errorf("codex asked %d times", asked.Load())
	}

	// Closed while codex was asked: not started.
	gone := &Tab{}
	gone.Dir, gone.Agent = tab.Dir, agents.Codex
	m.CodexAsked = false
	m.Tabs = append(m.Tabs, gone)
	cmd = m.Spawn(gone, agents.Launch{Argv: []string{"cat", "--no-daemon"}}, false)
	m.Tabs = m.Tabs[:2]
	m.OnSpawnReady(cmd().(SpawnReady))
	if gone.Pane != nil {
		gone.Pane.Close()
		t.Error("an agent closed while its CLI was asked started")
	}
}

// TestApplySkillsAsksCodexAwayFromMu: the request that puts the skills in
// place has codex trust nerdr's hooks in the Cmd it returns, not itself.
func TestApplySkillsAsksCodexAwayFromMu(t *testing.T) {
	asked := stubTrust(t)
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	codex := dir + "/codex"
	writeExec(t, codex)
	m := &Core{Client: NoClient{}}
	m.Cfg.Install = config.InstallHome
	m.Cfg.Agents = map[agents.Kind]agents.Spec{agents.Codex: {Command: []string{codex}}}
	cmd := ApplySkills{}.Do(m)
	if asked.Load() != 0 {
		t.Fatal("ApplySkills asked codex itself")
	}
	if cmd == nil {
		t.Fatal("codex was not to be asked")
	}
	ev, ok := cmd().(SkillsAppliedMsg)
	if !ok || asked.Load() != 1 {
		t.Fatalf("the Cmd gave %T, asking %d times", ev, asked.Load())
	}
	m.OnSkillsApplied(ev)
	if !m.CodexAsked || len(m.CodexHooks) == 0 {
		t.Error("codex's answer was not kept")
	}
}

// writeExec writes a program at path that does nothing.
func writeExec(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// fakeCopilot is a copilot that knows --session-id, and notes in the
// file it returns each time its help is asked.
func fakeCopilot(t *testing.T) (cmd, asked string) {
	dir := t.TempDir()
	cmd, asked = filepath.Join(dir, "copilot"), filepath.Join(dir, "asked")
	script := "#!/bin/sh\ncase \"$*\" in *--help*) echo x >> '" + asked + "'; echo '  --session-id <id>'; exit 0 ;; esac\nexec cat\n"
	if err := os.WriteFile(cmd, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return cmd, asked
}

// TestStartAsksTheHelpAwayFromMu: an agent whose command line needs an
// answer from its CLI (copilot whether it takes --session-id) is not
// asked by Start, which has Mu held, but by the Cmd it returns; the agent
// starts with the answer, and the next at once.
func TestStartAsksTheHelpAwayFromMu(t *testing.T) {
	cmdPath, asked := fakeCopilot(t)
	m := &Core{Client: NoClient{}, Spawner: &recorder{}}
	m.Cfg.Agents = map[agents.Kind]agents.Spec{agents.Copilot: {Command: []string{cmdPath}}}
	tab := &Tab{}
	tab.Dir, tab.Agent = t.TempDir(), agents.Copilot
	m.Tabs = []*Tab{tab}

	cmd := m.Start(tab, false)
	if _, err := os.Stat(asked); err == nil || tab.Pane != nil || cmd == nil {
		t.Fatalf("Start asked copilot itself (started %v, cmd %v)", tab.Pane != nil, cmd != nil)
	}
	ev, ok := cmd().(LaunchMsg)
	if !ok || !slices.Contains(ev.L.Argv, "--session-id") {
		t.Fatalf("the Cmd gave %+v", ev)
	}
	m.loopEvent(ev)
	if tab.Pane == nil {
		t.Fatalf("copilot did not start: %q", tab.Err)
	}
	defer tab.Pane.Close()

	next := &Tab{}
	next.Dir, next.Agent = tab.Dir, agents.Copilot
	m.Tabs = append(m.Tabs, next)
	m.Start(next, false)
	if next.Pane == nil {
		t.Fatal("asked already, copilot did not start at once")
	}
	defer next.Pane.Close()
	if b, _ := os.ReadFile(asked); string(b) != "x\n" {
		t.Errorf("copilot asked %q", b)
	}
}

// TestAddReviewerAsksTheHelpAwayFromMu: a reviewer whose CLI must be
// asked first takes its seat at once, and starts once it has answered.
func TestAddReviewerAsksTheHelpAwayFromMu(t *testing.T) {
	h := holder.New()
	cl, err := h.Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cl.Shutdown() })
	m, tab := heldCore(t, cl, agentScript(t, "exec cat\n"))
	m.Root = t.TempDir()
	cmdPath, asked := fakeCopilot(t)
	m.Cfg.Agents[agents.Claude] = agents.Spec{Command: []string{agentScript(t, "exec cat\n")}}
	m.Cfg.Agents[agents.Copilot] = agents.Spec{Command: []string{cmdPath}}
	m.Start(tab, false)
	m.StartReview(tab, Reviewer{Kind: agents.Claude}, "main", "tree1", "")
	cmd := m.AddReviewer(tab, Reviewer{Kind: agents.Copilot})
	if tab.Review == nil || len(tab.Review.Seats) != 2 {
		t.Fatalf("no seat: %+v", tab.Review)
	}
	r := tab.Review.Seats[1].R
	if _, err := os.Stat(asked); err == nil || r.Pane != nil || cmd == nil {
		t.Fatalf("AddReviewer asked copilot itself (started %v)", r.Pane != nil)
	}
	ev, ok := cmd().(LaunchMsg)
	if !ok {
		t.Fatalf("the Cmd gave %+v", ev)
	}
	m.loopEvent(ev)
	if r.Pane == nil {
		t.Fatalf("the reviewer did not start: %q", r.Err)
	}
	if r.SessionID == "" {
		t.Error("started without the --session-id copilot said it takes")
	}
}

// TestSpawnPutsTheSkillsAwayFromMu: with the skills put in each worktree,
// Spawn, which has Mu held, leaves the folder alone; the Cmd it returns
// puts them there, and the agent starts after. A folder gone meanwhile is
// said so, and nothing started.
func TestSpawnPutsTheSkillsAwayFromMu(t *testing.T) {
	rec := &recorder{}
	m := &Core{Client: NoClient{}, Spawner: rec}
	m.Cfg.Install = config.InstallWorktree
	m.Cfg.Agents = map[agents.Kind]agents.Spec{agents.Claude: {Command: []string{"cat"}}}
	tab := &Tab{}
	tab.Dir, tab.Agent = t.TempDir(), agents.Claude
	m.Tabs = []*Tab{tab}
	put := filepath.Join(tab.Dir, ".claude", "skills")

	cmd := m.Spawn(tab, agents.Launch{Argv: []string{"cat"}}, false)
	if cmd == nil {
		t.Fatal("started at once, the skills put in place with Mu held")
	}
	if _, err := os.Stat(put); err == nil || len(rec.specs) != 0 {
		t.Fatalf("Spawn put the skills in place, or started the agent (%d)", len(rec.specs))
	}
	ev, ok := cmd().(SpawnReady)
	if !ok || !ev.Prep.Looked || ev.Prep.Gone {
		t.Fatalf("the Cmd gave %+v", ev)
	}
	if _, err := os.Stat(put); err != nil {
		t.Fatalf("the Cmd did not put the skills in place: %v", err)
	}
	m.OnSpawnReady(ev)
	if tab.Pane == nil || len(rec.specs) != 1 {
		t.Fatalf("the agent did not start: %q", tab.Err)
	}
	defer tab.Pane.Close()

	gone := &Tab{}
	gone.Dir, gone.Agent = filepath.Join(t.TempDir(), "gone"), agents.Claude
	m.Tabs = append(m.Tabs, gone)
	m.OnSpawnReady(m.Spawn(gone, agents.Launch{Argv: []string{"cat"}}, false)().(SpawnReady))
	if gone.Pane != nil || !strings.Contains(gone.Err, "the worktree is gone") {
		t.Fatalf("a folder gone: started %v, %q", gone.Pane != nil, gone.Err)
	}
	if _, err := os.Stat(gone.Dir); err == nil {
		t.Fatal("the folder gone was made again")
	}
}
