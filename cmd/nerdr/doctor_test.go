package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/attach"
)

// TestSocketsCheck: the sockets' folder is private, not there yet, or open
// to others, which doctor says without changing it.
func TestSocketsCheck(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	t.Setenv("NERDR_CLIENT_DIR", dir)
	if c := socketsCheck(false); c.Status != checkOK || !strings.Contains(c.Text, "not there yet") {
		t.Fatalf("not there: %+v", c)
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if c := socketsCheck(false); c.Status != checkFail || !strings.Contains(c.Text, "chmod 700") {
		t.Fatalf("open: %+v", c)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o755 {
		t.Fatal("doctor changed the folder")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if c := socketsCheck(false); c.Status != checkOK {
		t.Fatalf("private: %+v", c)
	}
}

// TestRunningCheck: no nerdr is no fault; one of this version is ok, of
// another a warning; in a tab it is not asked, and says why.
func TestRunningCheck(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NERDR_CLIENT_DIR", dir)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c := runningCheck(root, false); c.Status != checkOK || !strings.Contains(c.Text, "no nerdr runs") {
		t.Fatalf("none: %+v", c)
	}
	if c := runningCheck(root, true); c.Status != checkOK || !strings.Contains(c.Text, "your own terminal") {
		t.Fatalf("in a tab: %+v", c)
	}
	sock, _, err := attach.APIPaths(root)
	if err != nil {
		t.Fatal(err)
	}
	listCore(t, dir, strings.TrimSuffix(filepath.Base(sock), ".sock"), root, 1, "")
	// listCore's version is "test", not this binary's.
	if c := runningCheck(root, false); c.Status != checkWarn || !strings.Contains(c.Text, "nerdr stop") {
		t.Fatalf("another version: %+v", c)
	}
}

// TestRunningCheckRefusedUnderNerdr: a core refusing a program under a
// nerdr is said plainly, not as a fault.
func TestRunningCheckRefusedUnderNerdr(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NERDR_CLIENT_DIR", dir)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sock, _, _ := attach.APIPaths(root)
	listCore(t, dir, strings.TrimSuffix(filepath.Base(sock), ".sock"), root, 1,
		"nerdr takes no client from a program a nerdr runs, in any folder: connect from your own terminal")
	if c := runningCheck(root, false); c.Status != checkOK || !strings.Contains(c.Text, "your own terminal") {
		t.Fatalf("refused: %+v", c)
	}
}
