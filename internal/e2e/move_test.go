//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMoveTabToServer covers moving a tab (a m) from this machine to a
// server, "box", over (a fake) ssh: box has no clone of the repository,
// so it clones it from origin; the branch and a file not committed land
// in a worktree there, the tab is on box, and this machine's tab closes
// with its worktree left as it was.
func TestMoveTabToServer(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	home := filepath.Join(scratch, "home")
	_ = os.MkdirAll(home, 0o755)
	ssh := filepath.Join(scratch, "fakessh")
	if err := os.WriteFile(ssh, []byte(fakeSSH), 0o755); err != nil {
		t.Fatal(err)
	}
	env := append(holderEnv(t, bin, scratch),
		"HOME="+home, "SHELL=/bin/sh",
		"PATH="+filepath.Dir(bin)+":"+os.Getenv("PATH"),
		"NERDR_SSH="+ssh, "FAKE_SSH_LOG="+filepath.Join(scratch, "ssh.log"))
	// box's folder holds no repository: it clones repoA there.
	boxDir := filepath.Join(scratch, "box")
	_ = os.MkdirAll(boxDir, 0o755)
	cfg := filepath.Join(scratch, "config", "nerdr", "config.toml")
	f, err := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("\n[[servers]]\nname = \"box\"\nfolder = \"" + boxDir + "\"\n")
	f.Close()

	h := startArgs(t, bin, root, isolated(t, env), 160, 40)
	newCrashTab(h)
	out, err := exec.Command("git", "-C", filepath.Join(root, "repoA"), "worktree", "list", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	var wt string
	for _, l := range strings.Split(string(out), "\n") {
		if p, ok := strings.CutPrefix(l, "worktree "); ok && strings.Contains(p, "crash") {
			wt = p
		}
	}
	if wt == "" {
		t.Fatalf("no worktree of crash in\n%s", out)
	}
	if err := os.WriteFile(filepath.Join(wt, "moved.txt"), []byte("not committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(wt, ".env"), []byte("SECRET=1\n"), 0o644)
	_ = os.WriteFile(filepath.Join(wt, ".gitignore"), []byte(".env\n"), 0o644)

	if log := readFile(filepath.Join(scratch, "ssh.log")); !strings.HasPrefix(log, "box ") {
		t.Fatalf("ssh was asked %q", log)
	}
	h.keys(prefix + "a")
	h.keys("m")
	h.waitFor("Goes: branch crash", 15*time.Second)
	h.waitFor("moved.txt", 2*time.Second)
	h.waitFor("it clones", 2*time.Second)
	h.waitFor("Stays: .env", 2*time.Second)
	// The fake agent writes no handoff.
	if strings.Contains(h.screen(), "for a handoff first") {
		h.keys("h")
	}
	h.keys("\r")
	h.waitFor("crash is on box", 30*time.Second)
	h.keys("1")
	h.waitFor("moved this tab here from another machine (this", 10*time.Second)

	var landed string
	_ = filepath.Walk(boxDir, func(p string, info os.FileInfo, err error) error {
		if err == nil && info.Name() == "moved.txt" {
			landed = p
		}
		return nil
	})
	if landed == "" || readFile(landed) != "not committed\n" {
		t.Fatalf("moved.txt on box: %q", landed)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(landed), ".env")); err == nil {
		t.Error(".env moved")
	}
	if readFile(filepath.Join(wt, "moved.txt")) != "not committed\n" {
		t.Error("the worktree here changed")
	}
	// The tab is box's alone now.
	h.waitFor("closed crash — the worktree is kept", 10*time.Second)
	h.keys(prefix + "k")
	h.waitFor("this machine  0 tabs", 5*time.Second)
	h.waitFor("box  1 tab", 5*time.Second)
	h.keys("\x1b")
	time.Sleep(200 * time.Millisecond)
	endClient(t, h, "q")
}
