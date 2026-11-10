package core

import (
	"crypto/rand"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/move"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/term"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// keptClient keeps what core tells it.
type keptClient struct {
	NoClient
	evs []Event
}

func (c *keptClient) Tell(ev Event) Cmd {
	c.evs = append(c.evs, ev)
	return nil
}

// moveCore is a core with one tab, its agent running, on a worktree of a
// repository with an origin, with a change not committed.
func moveCore(t *testing.T) (*Core, *Tab, *keptClient) {
	t.Helper()
	for k, v := range map[string]string{"GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t"} {
		t.Setenv(k, v)
	}
	root := t.TempDir()
	repo := filepath.Join(root, "proj")
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "commit", "-q", "--allow-empty", "-m", "first"},
		{"-C", repo, "remote", "add", "origin", "https://example.com/o/proj.git"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	wt := filepath.Join(root, "wt")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", "-b", "feat", wt).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	gitsafe.Trust(repo, wt)
	os.WriteFile(filepath.Join(wt, "new.txt"), []byte("new\n"), 0o644)
	cl := &keptClient{}
	c := &Core{Root: root, Wake: make(chan struct{}, 1), Cfg: config.Config{Install: "x", Agents: map[agents.Kind]agents.Spec{}}, Client: cl}
	pane, err := term.Start([]string{"sh", "-c", "stty -echo; sleep 30"}, wt, os.Environ(), 40, 10, func() {})
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(pane.Close)
	tb := &Tab{AgentState: Agent{Tab: state.Tab{ID: "1", Name: "feat", Agent: agents.Codex, Dir: wt,
		Worktrees: []workspace.Worktree{{Repo: repo, Path: wt, Branch: "feat", Origin: "https://example.com/o/proj.git"}}}}}
	tb.Pane, tb.Light = pane, StIdle
	c.Tabs = []*Tab{tb}
	return c, tb, cl
}

// replied is what cmd, run as the loop would, tells the client.
func replied(t *testing.T, cmd Cmd) Event {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command")
	}
	r, ok := cmd().(Reply)
	if !ok {
		t.Fatal("not a reply")
	}
	return r.Ev
}

// TestMoveWaitsForTheHandoff: the agent is asked for a handoff ahead of
// its queue, and once it sends it, it is stopped and the handoff goes in
// the pack.
func TestMoveWaitsForTheHandoff(t *testing.T) {
	c, tb, _ := moveCore(t)
	tb.Messages = []state.Message{{Text: "later work", Auto: true}}
	if cmd := (MoveStart{Job: "j1", T: c.Ref(tb), Handoff: true}).Do(c); cmd != nil {
		t.Fatal("packed without waiting for the handoff")
	}
	if tb.Handoff == nil || !tb.Handoff.Move || tb.Moving == nil || tb.Pane == nil {
		t.Fatalf("not waiting: %+v %+v", tb.Handoff, tb.Moving)
	}
	if !strings.Contains(tb.Messages[0].Text, "moving this tab to another machine") || tb.Messages[1].Text != "later work" {
		t.Fatalf("the request goes first: %+v", tb.Messages)
	}
	if c.AskHandoff(tb, agents.Claude); tb.Handoff.Move != true {
		t.Fatal("a switch took the move's place")
	}
	ev := replied(t, c.OnHandoff(hooks.Report{Method: hooks.Handoff, Pane: tb.HookKey(), Handoff: "THE SUMMARY"}))
	packed, ok := ev.(MovePackedMsg)
	if !ok || packed.Err != "" || packed.Size == 0 || packed.Heads[tb.Worktrees[0].Path] == "" {
		t.Fatalf("packed: %+v", ev)
	}
	if tb.Pane != nil || tb.Handoff != nil || len(tb.Messages) != 1 {
		t.Fatalf("the agent was not stopped, or the request not taken back: %+v", tb.Messages)
	}
	var man moveManifest
	b, _ := os.ReadFile(filepath.Join(c.movesOut["j1"].dir, manifestFile))
	if err := json.Unmarshal(b, &man); err != nil || man.Handoff != "THE SUMMARY" || len(man.Messages) != 1 {
		t.Fatalf("manifest %+v %v", man, err)
	}

	// Given up: the agent starts again, and the pack goes.
	dir := c.movesOut["j1"].dir
	if cmd := c.moveAbort("j1", "a test"); cmd == nil || tb.Moving != nil {
		t.Fatalf("not started again: %+v", tb.Moving)
	}
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(dir); err != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("the pack was left")
	}
}

// TestMoveTooLarge: a move past the limit is refused as it is looked at,
// as it is packed, and as the target is given it.
func TestMoveTooLarge(t *testing.T) {
	old := move.Limit
	move.Limit = 64 << 10
	t.Cleanup(func() { move.Limit = old })
	c, tb, _ := moveCore(t)
	big := make([]byte, 128<<10)
	rand.Read(big)
	os.WriteFile(filepath.Join(tb.Dir, "big.bin"), big, 0o644)

	look, _ := replied(t, (MoveLook{Job: "j", T: c.Ref(tb)}).Do(c)).(MoveLookMsg)
	if !strings.Contains(look.Err, "more than the") {
		t.Errorf("look: %+v", look)
	}
	packed, _ := replied(t, (MoveStart{Job: "j", T: c.Ref(tb)}).Do(c)).(MovePackedMsg)
	if !strings.Contains(packed.Err, "more than the") {
		t.Errorf("pack: %+v", packed)
	}
	c.moveAbort("j", "too large")

	cl := c.Client.(*keptClient)
	cl.evs = nil
	(MovePut{Job: "k", Off: move.Limit - 10, Data: make([]byte, 20)}).Do(c)
	if len(cl.evs) != 1 || !strings.Contains(cl.evs[0].(MovePutMsg).Err, "more than the") {
		t.Errorf("put past the limit: %+v", cl.evs)
	}
}

// TestMoveLandRefusesAWeakerSandbox: the target lands a tab whose agent
// was sandboxed with one that is not only with the user's yes.
func TestMoveLandRefusesAWeakerSandbox(t *testing.T) {
	c := &Core{Cfg: config.Config{Agents: map[agents.Kind]agents.Spec{agents.Codex: {}}}, Agents: []agents.Kind{agents.Codex}, AgentsAt: time.Now()}
	c.movesIn = map[string]string{"j": t.TempDir()}
	q := MoveLand{Job: "j", ID: "abc", Kind: agents.Codex, Sandboxed: true}
	if why := c.landRefusal(q); !strings.Contains(why, "sandbox") {
		t.Errorf("no yes: %q", why)
	}
	q.Weaker = true
	if why := c.landRefusal(q); why != "" {
		t.Errorf("with the yes: %q", why)
	}
	c.Cfg.Agents[agents.Codex] = agents.Spec{Sandboxed: true}
	q.Weaker = false
	if why := c.landRefusal(q); why != "" {
		t.Errorf("sandboxed there too: %q", why)
	}
}

// TestMoveManifestFromElsewhere: what a pack says is checked before it is
// used: no folders out of the tab's, no files not in the pack.
func TestMoveManifestFromElsewhere(t *testing.T) {
	ok := moveManifest{Agent: agents.Codex, Parts: []move.Part{{Branch: "feat", Bundle: "repo-0.bundle"}}}
	if err := checkManifest(&ok, []string{"manifest.json", "repo-0.bundle"}, 1); err != nil {
		t.Fatal(err)
	}
	for name, man := range map[string]moveManifest{
		"sub":    {Agent: agents.Codex, Sub: "../x", Parts: []move.Part{{Branch: "feat"}}},
		"branch": {Agent: agents.Codex, Parts: []move.Part{{Branch: "-x"}}},
		"bundle": {Agent: agents.Codex, Parts: []move.Part{{Branch: "feat", Bundle: "other"}}},
		"agent":  {Agent: "rm -rf", Parts: []move.Part{{Branch: "feat"}}},
		"parts":  {Agent: agents.Codex},
	} {
		if err := checkManifest(&man, []string{"manifest.json"}, 1); err == nil {
			t.Errorf("%s: taken", name)
		}
	}
}
