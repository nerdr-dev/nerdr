package proc

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"nerdr/internal/denied"
)

// A program detached is in a process group, and a session, of its own:
// not in the terminal's foreground, where it would name the tab.
func TestDetach(t *testing.T) {
	out, err := Detach(exec.Command("sh", "-c", "ps -o pgid= -p $$")).Output()
	if err != nil {
		t.Skip("no ps:", err)
	}
	pgid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("ps said %q", out)
	}
	if pgid == syscall.Getpgrp() {
		t.Fatalf("still in nerdr's process group %d", pgid)
	}
	cmd := exec.Command("true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := Detach(cmd).Run(); err != nil {
		t.Errorf("a program asked for a group of its own too does not start: %v", err)
	}
}

// A program that says it was turned down is kept as a denial, by what it
// was asked and where; one that fails otherwise is not.
func TestRunNotesDenial(t *testing.T) {
	dir := t.TempDir()
	cmd := Detach(exec.Command("sh", "-c", "echo 'BLOCKED by sandbox: no' >&2; exit 1"))
	cmd.Dir = dir
	if Run(cmd) == nil {
		t.Fatal("sh did not fail")
	}
	found := false
	for _, d := range denied.List() {
		if d.Why == "BLOCKED by sandbox: no" && strings.HasPrefix(d.What, "sh") && strings.HasSuffix(d.What, " in "+dir) {
			found = true
		}
	}
	if !found {
		t.Fatalf("not kept: %+v", denied.List())
	}
	n := denied.Noted()
	if _, err := Output(Detach(exec.Command("sh", "-c", "echo 'not found' >&2; exit 1"))); err == nil {
		t.Fatal("sh did not fail")
	}
	if denied.Noted() != n {
		t.Errorf("a failure that says nothing of permission was kept")
	}
}
