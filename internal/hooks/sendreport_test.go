package hooks

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A hook's report reaches nerdr as Send's does.
func TestSendReportReachesNerdr(t *testing.T) {
	s, got := listen(t)
	if err := SendReport(s.Path(), ReportAgent, map[string]any{"pane_id": "p1", "state": "working", "agent": "claude"}); err != nil {
		t.Fatal(err)
	}
	if r := next(t, got); r.Pane != "p1" || r.State != "working" {
		t.Fatalf("got %+v", r)
	}
}

// A core that takes the connection but does not answer, as one that
// hangs, holds a hook up for reportWait, not for the ten seconds and
// more a request waits: the agent waits for its hooks.
func TestSendReportToACoreThatHangs(t *testing.T) {
	dir, err := os.MkdirTemp("", "nh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ln, err := net.Listen("unix", filepath.Join(dir, "hung.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var conns []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns = append(conns, c) // kept open, never answered
		}
	}()
	start := time.Now()
	err = SendReport(ln.Addr().String(), ReportAgent, map[string]any{"pane_id": "p1", "state": "working"})
	if took := time.Since(start); err == nil || took > reportWait+time.Second {
		t.Fatalf("took %v, error %v", took, err)
	}
}
