package sandboxcheck

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A signal that comes to a process while the check traces it reaches it
// all the same: the holder is not to miss a SIGTERM for it.
func TestTracingLosesNoSignal(t *testing.T) {
	if b, err := os.ReadFile("/proc/sys/kernel/yama/ptrace_scope"); err == nil && strings.TrimSpace(string(b)) >= "2" {
		t.Skip("ptrace_scope " + strings.TrimSpace(string(b)) + " lets no process trace its child")
	}
	t.Cleanup(func() { beforeStop = func(int) {} })
	beforeStop = func(pid int) {
		_ = syscall.Kill(pid, syscall.SIGTERM)
		time.Sleep(20 * time.Millisecond)
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	if !traces(cmd.Process.Pid) {
		t.Fatal("could not trace its own child")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		ws, _ := cmd.ProcessState.Sys().(syscall.WaitStatus)
		if !ws.Signaled() || ws.Signal() != syscall.SIGTERM {
			t.Errorf("ended %v, not by the SIGTERM", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("the SIGTERM sent while it was traced was lost")
	}
}
