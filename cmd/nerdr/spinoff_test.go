package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/spinoff"
)

// TestPromptCommandsRunInTheShell: the commands printed for the prompts
// to come run as printed, the prompt written in place of …, and send it
// alone.
func TestPromptCommandsRunInTheShell(t *testing.T) {
	parts := []spinoff.Part{{Branch: "fix-a"}, {Branch: "fix-b", Prompt: "Do B."}}
	cmds := promptCommands(parts, map[string]string{"fix-a": "3f9a01bc", "fix-b": "0badc0de"})
	if strings.Contains(cmds, "0badc0de") {
		t.Fatalf("a command for a part with its prompt:\n%s", cmds)
	}
	_, script, _ := strings.Cut(cmds, "fix-a:\n")
	script = strings.Replace(script, "…", "Do A.\nAnd test it.", 1) + "echo after\n"
	bin := filepath.Join(t.TempDir(), "nerdr")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"args: $*\"\ncat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(), "NERDR_BIN="+bin)
	out, err := cmd.CombinedOutput()
	if want := "args: spinoff --prompt 3f9a01bc -\nDo A.\nAnd test it.\nafter\n"; err != nil || string(out) != want {
		t.Fatalf("ran as %q (%v), want %q; the commands:\n%s", out, err, want, cmds)
	}
}
