package app

import (
	"bytes"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/move"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
)

func moveGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// moveSetup gives this machine's tab of twoMachines a worktree on branch
// feat of a clone of origin, with work not committed in it, a note and a
// message waiting; and box a clone of the same origin, and codex as an
// agent that only echoes. It gives the worktree.
func moveSetup(t *testing.T, m *Model, told chan any, here, box *core.Core, sandboxedHere bool) (wt, boxRepo string) {
	t.Helper()
	for k, v := range map[string]string{"GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t"} {
		t.Setenv(k, v)
	}
	root := t.TempDir()
	seed := filepath.Join(root, "seed")
	moveGit(t, root, "init", "-q", "-b", "main", seed)
	for _, f := range []string{"a.txt", "b.txt", "gone.txt"} {
		os.WriteFile(filepath.Join(seed, f), []byte(f+"\n"), 0o644)
	}
	os.WriteFile(filepath.Join(seed, ".gitignore"), []byte(".env\n"), 0o644)
	moveGit(t, seed, "add", "-A")
	moveGit(t, seed, "commit", "-q", "-m", "first")
	origin := filepath.Join(root, "origin.git")
	moveGit(t, root, "clone", "-q", "--bare", seed, origin)
	src := filepath.Join(root, "here", "proj")
	boxRepo = filepath.Join(root, "box", "proj")
	moveGit(t, root, "clone", "-q", origin, src)
	moveGit(t, root, "clone", "-q", origin, boxRepo)
	wt = filepath.Join(root, "here", "wt", "feat-proj")
	moveGit(t, src, "worktree", "add", "-q", "-b", "feat", wt)
	gitsafe.Trust(src, wt)
	os.WriteFile(filepath.Join(wt, "a.txt"), []byte("committed\n"), 0o644)
	moveGit(t, wt, "commit", "-q", "-am", "work")
	os.WriteFile(filepath.Join(wt, "b.txt"), []byte("staged\n"), 0o644)
	moveGit(t, wt, "add", "b.txt")
	os.Remove(filepath.Join(wt, "gone.txt"))
	os.WriteFile(filepath.Join(wt, "new.txt"), []byte("new\n"), 0o644)
	os.WriteFile(filepath.Join(wt, ".env"), []byte("SECRET=1\n"), 0o644)

	echo := agents.Spec{Command: []string{"sh", "-c", `stty -echo; echo moved-agent-ready; while read l; do echo "moved got $l"; done`}}
	here.Mu.Lock()
	tb := here.Tabs[0]
	tb.Dir = wt
	tb.Worktrees = []workspace.Worktree{{Repo: src, Path: wt, Branch: "feat", Origin: origin}}
	tb.Note = "remember the migration"
	tb.Messages = []state.Message{{Text: "then write the docs"}}
	// An agent that only echoes here too: one started again after a move
	// given up must not be a real codex, nor fail for want of one.
	hereSpec := echo
	hereSpec.Sandboxed = sandboxedHere
	here.Cfg.Agents = map[agents.Kind]agents.Spec{agents.Codex: hereSpec}
	here.Mu.Unlock()
	box.Mu.Lock()
	box.Scan.Repos = []workspace.Repo{{Name: "proj", Path: boxRepo}}
	box.Scan.NewRepoDir = filepath.Join(root, "box")
	box.WtRoot = filepath.Join(root, "box", "wt")
	box.Agents, box.AgentsAt = []agents.Kind{agents.Codex}, time.Now()
	box.Cfg.Agents = map[agents.Kind]agents.Spec{agents.Codex: echo}
	box.Mu.Unlock()
	// The copies hear of it with the next change each core sends.
	locked(m, func() {
		m.do(core.RenameTab{T: core.TabRef{Tab: "1"}, Name: "login"})
		m.do(core.RenameTab{T: core.TabRef{Core: "box", Tab: "1"}, Name: "deploy"})
	})
	pumpUntil(t, m, told, "the copies", func() bool {
		var ok bool
		locked(m, func() {
			tabs := m.fleet.Tabs()
			ok = len(tabs) == 2 && tabs[0].Dir == wt && len(m.fleet.Named("box").Core.Available()) == 1
		})
		return ok
	})
	return wt, boxRepo
}

// pumpUntil has m take what the cores tell until ok.
func pumpUntil(t *testing.T, m *Model, told chan any, what string, ok func() bool) {
	t.Helper()
	pump(t, m, told, what, func(string) bool { return ok() })
}

func pressKey(m *Model, k string) {
	var msg tea.KeyMsg
	switch k {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	_, cmd := m.Update(msg)
	if cmd != nil {
		if msg := cmd(); msg != nil {
			m.Update(msg)
		}
	}
}

// TestFleetMoveTab moves this machine's tab to box: the dialog says what
// goes and what stays, box gets the branch with its changes as they were,
// the note, the queue and the handoff, and this machine's tab closes only
// then, its worktree left as it was.
func TestFleetMoveTab(t *testing.T) {
	m, here, box, _, told := twoMachines(t)
	wt, _ := moveSetup(t, m, told, here, box, false)
	// Large enough to go in three chunks, and nothing git can squeeze.
	big := make([]byte, 5<<19)
	rand.Read(big)
	os.WriteFile(filepath.Join(wt, "big.bin"), big, 0o644)
	status := moveGit(t, wt, "status", "--porcelain")

	goTo(m, 0)
	locked(m, func() { m.openMove() })
	pump(t, m, told, "what goes", func(v string) bool {
		return strings.Contains(v, "Goes: branch feat") && strings.Contains(v, "new.txt") && strings.Contains(v, "Stays: .env")
	})
	// No handoff: the echoing agent writes none.
	pressKey(m, "h")
	pressKey(m, "enter")
	pump(t, m, told, "the tab on box", func(v string) bool {
		return strings.Contains(v, "login is on box") && strings.Contains(v, "leave them there")
	})
	here.Mu.Lock()
	stillHere := len(here.Tabs)
	here.Mu.Unlock()
	if stillHere != 1 {
		t.Fatal("this machine closed its tab before being told to")
	}
	locked(m, func() {
		if m.moving.size <= 2*move.Chunk {
			t.Errorf("the pack was %d bytes: not three chunks", m.moving.size)
		}
	})
	pressKey(m, "1")
	pump(t, m, told, "closed here", func(string) bool {
		here.Mu.Lock()
		defer here.Mu.Unlock()
		return len(here.Tabs) == 0
	})

	box.Mu.Lock()
	var moved *core.Tab
	for _, tb := range box.Tabs {
		if tb.Name == "login" {
			moved = tb
		}
	}
	var dir, note string
	var msgs []state.Message
	if moved != nil {
		dir, note, msgs = moved.Dir, moved.Note, moved.Messages
	}
	box.Mu.Unlock()
	if moved == nil {
		t.Fatal("no tab login on box")
	}
	if note != "remember the migration" {
		t.Errorf("note %q", note)
	}
	var texts []string
	for _, msg := range msgs {
		texts = append(texts, msg.Text)
	}
	all := strings.Join(texts, "\n")
	if !strings.Contains(all, "moved this tab here from another machine (this machine)") || !strings.Contains(all, "then write the docs") {
		t.Errorf("messages on box:\n%s", all)
	}
	if got := moveGit(t, dir, "status", "--porcelain"); got != status {
		t.Errorf("box's status:\n%s\nthis machine's:\n%s", got, status)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "big.bin")); !bytes.Equal(b, big) {
		t.Error("big.bin did not arrive whole")
	}
	if _, err := os.Stat(filepath.Join(dir, ".env")); err == nil {
		t.Error(".env moved")
	}
	// This machine's worktree is as it was.
	if got := moveGit(t, wt, "status", "--porcelain"); got != status {
		t.Errorf("this machine's status changed:\n%s", got)
	}
}

// TestFleetMoveRefusedLeavesTheTab: a move box cannot take (a branch of
// its own of the name) is refused before anything stops, and the tab
// carries on here.
func TestFleetMoveRefusedLeavesTheTab(t *testing.T) {
	m, here, box, _, told := twoMachines(t)
	_, boxRepo := moveSetup(t, m, told, here, box, false)
	moveGit(t, boxRepo, "branch", "feat")
	os.WriteFile(filepath.Join(boxRepo, "z.txt"), []byte("z\n"), 0o644)
	moveGit(t, boxRepo, "checkout", "-q", "feat")
	moveGit(t, boxRepo, "add", "z.txt")
	moveGit(t, boxRepo, "commit", "-q", "-m", "box's own")
	moveGit(t, boxRepo, "checkout", "-q", "main")

	goTo(m, 0)
	locked(m, func() { m.openMove() })
	pump(t, m, told, "refused", func(v string) bool {
		return strings.Contains(v, "login was not moved") && strings.Contains(v, "commits this one has not")
	})
	here.Mu.Lock()
	tb := here.Tabs[0]
	running, moving := tb.Pane != nil, tb.Moving
	here.Mu.Unlock()
	if !running || moving != nil {
		t.Errorf("the tab here: running %v, moving %+v", running, moving)
	}
}

// TestFleetMoveFailsLate: box fails to land the tab after the agent here
// was stopped; the tab carries on here with its agent started again.
func TestFleetMoveFailsLate(t *testing.T) {
	m, here, box, _, told := twoMachines(t)
	moveSetup(t, m, told, here, box, false)
	goTo(m, 0)
	locked(m, func() { m.openMove() })
	pump(t, m, told, "what goes", func(v string) bool { return strings.Contains(v, "Goes: branch feat") })
	// The folder for the worktree is taken meanwhile.
	var dir string
	locked(m, func() { dir = m.moving.check.Dir })
	os.MkdirAll(dir, 0o755)
	pressKey(m, "h")
	pressKey(m, "enter")
	pump(t, m, told, "refused", func(v string) bool {
		return strings.Contains(v, "login was not moved")
	})
	pumpUntil(t, m, told, "the agent here again", func() bool {
		here.Mu.Lock()
		defer here.Mu.Unlock()
		return len(here.Tabs) == 1 && here.Tabs[0].Moving == nil && here.Tabs[0].Light != core.StExited
	})
	box.Mu.Lock()
	n := len(box.Tabs)
	box.Mu.Unlock()
	if n != 1 {
		t.Errorf("%d tabs on box", n)
	}
}

// TestFleetMoveAsksForAWeakerSandbox: a tab whose agent is sandboxed here
// moves to box, whose agent is not, only once the user says yes to that
// as well; and box's core refuses it without that yes.
func TestFleetMoveAsksForAWeakerSandbox(t *testing.T) {
	m, here, box, _, told := twoMachines(t)
	moveSetup(t, m, told, here, box, true)
	goTo(m, 0)
	locked(m, func() { m.openMove() })
	pump(t, m, told, "the warning", func(v string) bool {
		return strings.Contains(v, "Not in a sandbox there")
	})
	pressKey(m, "h")
	pressKey(m, "enter")
	pump(t, m, told, "asked again", func(v string) bool {
		return strings.Contains(v, "runs without a sandbox. Move anyway?")
	})
	pressKey(m, "n")
	locked(m, func() {
		if m.moving.stage != moveAsking || m.moving.started {
			t.Errorf("no: stage %v, started %v", m.moving.stage, m.moving.started)
		}
	})
	pressKey(m, "enter")
	pressKey(m, "y")
	pump(t, m, told, "moved", func(v string) bool { return strings.Contains(v, "login is on box") })
}

// TestFleetMoveSandboxDroppedAfterTheYes: box's agent was sandboxed when
// the user said yes, and is not by the time the tab lands: box refuses
// it, and the tab carries on here.
func TestFleetMoveSandboxDroppedAfterTheYes(t *testing.T) {
	m, here, box, _, told := twoMachines(t)
	moveSetup(t, m, told, here, box, true)
	box.Mu.Lock()
	s := box.Cfg.Agents[agents.Codex]
	s.Sandboxed = true
	box.Cfg.Agents[agents.Codex] = s
	box.Mu.Unlock()
	locked(m, func() { m.do(core.RenameTab{T: core.TabRef{Core: "box", Tab: "1"}, Name: "deploy"}) })
	pumpUntil(t, m, told, "box sandboxed", func() bool {
		var ok bool
		locked(m, func() { ok = m.fleet.Named("box").Core.SandboxedKind(agents.Codex) })
		return ok
	})
	goTo(m, 0)
	locked(m, func() { m.openMove() })
	v := pump(t, m, told, "what goes", func(v string) bool { return strings.Contains(v, "Goes: branch feat") })
	if strings.Contains(v, "Not in a sandbox there") {
		t.Fatal("warned though box is sandboxed")
	}
	pressKey(m, "h")
	pressKey(m, "enter")
	box.Mu.Lock()
	s.Sandboxed = false
	box.Cfg.Agents[agents.Codex] = s
	box.Mu.Unlock()
	pump(t, m, told, "refused", func(v string) bool {
		return strings.Contains(v, "login was not moved") && strings.Contains(v, "sandbox")
	})
	pumpUntil(t, m, told, "the tab here again", func() bool {
		here.Mu.Lock()
		defer here.Mu.Unlock()
		return len(here.Tabs) == 1 && here.Tabs[0].Moving == nil
	})
	box.Mu.Lock()
	n := len(box.Tabs)
	box.Mu.Unlock()
	if n != 1 {
		t.Errorf("%d tabs on box", n)
	}
}
