package app

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/spawn"
)

// The leader exits on TERM, but its child keeps doing work until KILL.
// A heartbeat avoids mistaking a dead, unreaped child for a live process.
func TestPluginStopKillsDescendants(t *testing.T) {
	heartbeat := filepath.Join(t.TempDir(), "heartbeat")
	p, err := core.StartProc(spawn.Local{}, spawn.Spec{Argv: []string{os.Args[0], "-test.run=^TestPluginProcessHelper$"},
		Env: append(os.Environ(), "NERDR_PLUGIN_TEST_PROCESS=parent", "NERDR_PLUGIN_TEST_HEARTBEAT="+heartbeat)}, func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Kill(-p.Proc.Pid(), syscall.SIGKILL)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		out, _, _, _ := p.Since(0)
		if strings.Contains(out, "child ready") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("child did not start: %s", out)
		}
	}
	p.Stop()
	// Repeated stops must not schedule more signals after the group is gone.
	p.Stop()
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		_, _, done, _ := p.Since(0)
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("leader did not exit on TERM")
		}
	}
	before, err := os.ReadFile(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	during, err := os.ReadFile(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) == string(during) {
		t.Fatal("child did not survive its leader to exercise escalation")
	}
	select {
	case <-p.Stopped:
	case <-time.After(6 * time.Second):
		t.Fatal("stop did not finish")
	}
	time.Sleep(100 * time.Millisecond)
	after, err := os.ReadFile(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	final, err := os.ReadFile(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(final) {
		t.Fatal("child still doing work after stop completed")
	}
}

func TestPluginProcessHelper(t *testing.T) {
	switch os.Getenv("NERDR_PLUGIN_TEST_PROCESS") {
	case "parent":
		child := exec.Command(os.Args[0], "-test.run=^TestPluginProcessHelper$")
		child.Env = append(os.Environ(), "NERDR_PLUGIN_TEST_PROCESS=child")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Run(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	case "child":
		signal.Ignore(syscall.SIGTERM)
		path := os.Getenv("NERDR_PLUGIN_TEST_HEARTBEAT")
		for i := 0; i < 1500; i++ {
			if err := os.WriteFile(path, []byte(time.Now().String()), 0o600); err != nil {
				os.Exit(1)
			}
			if i == 0 {
				os.Stdout.WriteString("child ready\n")
			}
			time.Sleep(20 * time.Millisecond)
		}
		os.Exit(0)
	}
}

// Reads that waited in vain, as a run that prints nothing is read every
// few seconds, are let go once answered, not kept for its whole run.
func TestReadsThatTimeOutAreLetGo(t *testing.T) {
	p := &core.RunProc{Notify: func() {}}
	replies := make(chan struct{}, 3)
	for range 3 {
		p.Read(0, func(hooks.Answer) { replies <- struct{}{} })
	}
	for range 3 {
		select {
		case <-replies:
		case <-time.After(core.ReadWait + time.Second):
			t.Fatal("a read was not answered")
		}
	}
	p.Mu.Lock()
	defer p.Mu.Unlock()
	if len(p.Waiting) != 0 {
		t.Errorf("%d reads kept", len(p.Waiting))
	}
}
