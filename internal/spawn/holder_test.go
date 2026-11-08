package spawn

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestIsNerdr(t *testing.T) {
	for _, c := range []struct {
		args   []string
		holder bool
		nerdr  bool
	}{
		{[]string{"nerdr", "holder", "--", "nerdr", "core"}, true, true},
		{[]string{"nerdr-new", "holder", "--resume", "5"}, true, true},
		{[]string{"nerdr", "holder", "--watch"}, true, true},
		{[]string{"nerdr", LocalArg}, false, true},
		{[]string{"nerdr"}, false, false},
		{[]string{"nerdr", "connect"}, false, false},
		{[]string{"vim", "holder"}, false, false},
		{[]string{"nerdr", LocalArg, "x"}, false, false},
	} {
		if got := IsHolder(c.args); got != c.holder {
			t.Errorf("IsHolder(%q) = %v", c.args, got)
		}
		if got := IsNerdr(c.args); got != c.nerdr {
			t.Errorf("IsNerdr(%q) = %v", c.args, got)
		}
	}
}

func TestProcsRoundTrip(t *testing.T) {
	ps := []Process{{12, 345}, {1, 2}}
	if got := ParseProcs(FormatProcs(ps)); !reflect.DeepEqual(got, ps) {
		t.Errorf("got %v", got)
	}
	if got := ParseProcs("0 x.1 3.0 4.5 junk"); !reflect.DeepEqual(got, []Process{{4, 5}}) {
		t.Errorf("got %v", got)
	}
}

// TestBranch: from a process under a holder, the branch is it and those
// up to the holder.
func TestBranch(t *testing.T) {
	if _, err := os.Stat("/proc/self/cmdline"); err != nil {
		t.Skip("no /proc")
	}
	dir := t.TempDir()
	// The script "holder" sh runs as nerdr's holder starts a child.
	if err := os.WriteFile(filepath.Join(dir, "holder"), []byte("sleep 30 & echo $! > child; wait\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := &exec.Cmd{Path: "/bin/sh", Args: []string{"nerdr", "holder", "--"}, Dir: dir}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	var child int
	for deadline := time.Now().Add(5 * time.Second); child == 0; time.Sleep(10 * time.Millisecond) {
		b, _ := os.ReadFile(filepath.Join(dir, "child"))
		child, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		if time.Now().After(deadline) {
			t.Fatal("no child")
		}
	}
	defer func() { _ = syscall.Kill(child, syscall.SIGKILL) }()
	branch, under := Branch(child)
	if !under || len(branch) != 1 || branch[0].Pid != child || branch[0].Start == 0 {
		t.Errorf("Branch(child) = %v, %v", branch, under)
	}
	if _, under := Branch(os.Getpid()); under && os.Getenv("NERDR") == "" {
		t.Error("this test is under a holder")
	}
}
