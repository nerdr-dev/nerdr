package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/api"
	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// TestStopCore: nerdr stop has the core quit, and waits until its
// process has ended, a while after its API socket closes. It never takes
// the session lock, so a nerdr starting meanwhile is not kept out.
func TestStopCore(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	sock, key := filepath.Join(dir, "api.sock"), filepath.Join(dir, "api.key")
	// The core's process, as far as stop can tell: one of the test's own.
	proc := exec.Command("sleep", "30")
	if err := proc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proc.Process.Kill(); _ = proc.Wait() })
	pid := proc.Process.Pid
	peerOf = func(net.Conn) hooks.Peer {
		return hooks.Peer{Known: true, Chain: []int{pid}, Starts: []uint64{hooks.Started(pid)}}
	}
	t.Cleanup(func() { peerOf = hooks.PeerOf })

	m := &core.Core{Root: dir}
	var s *api.Server
	var ended atomic.Bool
	// As runCore: the API socket closes, then the process ends a while
	// later.
	m.OnQuit = func() {
		s.Close()
		go func() {
			time.Sleep(200 * time.Millisecond)
			ended.Store(true)
			_ = proc.Process.Kill()
			_ = proc.Wait()
		}()
	}
	s, err := api.Listen(sock, key, api.Config{Version: "test", Root: dir,
		Check: func(hooks.Peer) string { return "" },
		Post: func(c core.Call) {
			go func() {
				m.Mu.Lock()
				cmd := c.F(m)
				m.Mu.Unlock()
				if cmd != nil {
					cmd()
				}
			}()
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	// A nerdr starting in the folder all the while finds it free.
	done := make(chan struct{})
	locked := make(chan error, 1)
	go func() {
		for {
			select {
			case <-done:
				locked <- nil
				return
			default:
			}
			release, err := state.Lock(dir)
			if err != nil {
				locked <- err
				return
			}
			release()
		}
	}()
	err = stopCore(sock, key, 5*time.Second)
	close(done)
	if err != nil {
		t.Fatal(err)
	}
	if !ended.Load() {
		t.Fatal("stop returned before the core's process ended")
	}
	if err := <-locked; err != nil {
		t.Fatalf("a nerdr starting meanwhile: %v", err)
	}
	if err := stopCore(sock, key, time.Second); err == nil {
		t.Fatal("stopping a core that is not there")
	}
}

// TestStopTimeout: --timeout bounds all of it, a core that never answers
// as well.
func TestStopTimeout(t *testing.T) {
	dir := t.TempDir()
	sock, key := filepath.Join(dir, "api.sock"), filepath.Join(dir, "api.key")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() }) // takes the hello, says nothing
		}
	}()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := attach.WriteKey(key); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = stopCore(sock, key, 200*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not answer within") {
		t.Fatalf("got %v", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("took %s for a 200ms timeout", took)
	}
}

// TestStopRefused: a core that refuses who connects is not stopped, and
// says why.
func TestStopRefused(t *testing.T) {
	dir := t.TempDir()
	listCore(t, dir, "api", "/w", 1, "not you")
	err := stopCore(filepath.Join(dir, "api.sock"), filepath.Join(dir, "api.key"), time.Second)
	if err == nil || !strings.Contains(err.Error(), "not you") {
		t.Fatalf("got %v", err)
	}
}
