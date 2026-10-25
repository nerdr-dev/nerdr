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

// TestFirstStartSetsUpSkillsAndNotes covers the first start's questions:
// the answers go into the config file, the skills into the tab's folder
// (kept out of git), and a note the agent records through NERDR_BIN lands
// in the notes folder chosen, inside the worktree.
func TestFirstStartSetsUpSkillsAndNotes(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	fake, _ := filepath.Abs("fakeagent.sh")
	cfgDir := filepath.Join(scratch, "config")
	cfgFile := filepath.Join(cfgDir, "nerdr", "config.toml")
	_ = os.MkdirAll(filepath.Dir(cfgFile), 0o755)
	_ = os.WriteFile(cfgFile, []byte("worktrees_dir = \"worktrees\"\ntab_bar = \"top\"\n[agents.claude]\ncommand = [\""+fake+"\"]\n"), 0o644)
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HERDR_") && !strings.HasPrefix(kv, "NERDR_") {
			env = append(env, kv)
		}
	}
	env = append(env, "XDG_CONFIG_HOME="+cfgDir, "XDG_STATE_HOME="+filepath.Join(scratch, "state"),
		"HOME="+filepath.Join(scratch, "home"), "FAKE_LOG="+filepath.Join(scratch, "a.log"), "TERM=xterm-256color")
	h := start(t, bin, root, env)

	h.waitFor("Set up nerdr", 10*time.Second)
	h.keys("2")  // in each tab's folder
	h.keys("\t") // to the data folder
	h.keys(".nerdr")
	h.keys("\r")
	h.waitFor("Repositories in", 5*time.Second)
	cfg, _ := os.ReadFile(cfgFile)
	if !strings.Contains(string(cfg), `install = "worktree"`) || !strings.Contains(string(cfg), `data_dir = ".nerdr"`) ||
		!strings.Contains(string(cfg), "[agents.claude]") {
		t.Fatalf("config not written as expected:\n%s", cfg)
	}

	h.keys(" \r")
	h.keys("sk\r")
	h.waitFor("Agent:", 2*time.Second)
	h.keys("\r")
	h.waitFor("? for shortcuts", 10*time.Second)

	wt := filepath.Join(root, "worktrees", "sk-repoA")
	if _, err := os.Stat(filepath.Join(wt, ".claude", "skills", "nerdr-explain", "SKILL.md")); err != nil {
		t.Fatalf("the skill is not in the tab's folder: %v", err)
	}
	h.keys("note\r")
	h.waitFor("noted README.md:1", 5*time.Second)
	if _, err := os.Stat(filepath.Join(wt, ".nerdr", "why.jsonl")); err != nil {
		t.Fatalf("the note is not in the worktree's notes folder: %v", err)
	}
	if out, _ := exec.Command("git", "-C", wt, "status", "--porcelain").Output(); len(out) != 0 {
		t.Fatalf("nerdr's files must be kept out of git:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(scratch, "home", ".claude")); err == nil {
		t.Fatal("with the skills in the tab's folder, nothing goes in the home folder")
	}
}
