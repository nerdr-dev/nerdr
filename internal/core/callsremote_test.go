package core

import (
	"os/exec"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/denied"
	"github.com/nerdr-dev/nerdr/internal/proc"
)

// TestCallsOfACoreElsewhere: a client in a process of its own is told the
// calls core's process made (ReadCalls), and has in its copy what core's
// process was turned down for, as of core's last InfoTick: nearly all of
// both are core's.
func TestCallsOfACoreElsewhere(t *testing.T) {
	c := &Core{}
	cc, told := remoteCore(t, c)
	proc.Detach(exec.Command("git", "-C", "/zz-calls-elsewhere", "status"))
	denied.Note("git status in /zz-calls-elsewhere", "BLOCKED by sandbox")

	c.Mu.Lock()
	c.noteDenied() // as InfoTick does
	c.FlushClient()
	c.Mu.Unlock()
	cc.C.Mu.Lock()
	_, err := cc.Run(ReadCalls{Job: "j"})
	cc.C.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	var got *CallsMsg
	for deadline := time.Now().Add(5 * time.Second); got == nil || cc.C.DeniedNoted == 0; {
		cc.C.Mu.Lock()
		for _, ev := range cc.Take() {
			if m, ok := ev.(CallsMsg); ok && m.Job == "j" {
				got = &m
			}
		}
		cc.C.Mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("told %+v, denials %d", got, cc.C.DeniedNoted)
		}
		select {
		case <-told:
		case <-time.After(20 * time.Millisecond):
		}
	}
	if n := CallsOf(got.Calls)[proc.Call{Program: "git", Dir: "/zz-calls-elsewhere", Sub: "status"}]; n == 0 || got.Since.IsZero() {
		t.Fatalf("the git call is not among %+v", got)
	}
	cc.C.Mu.Lock()
	defer cc.C.Mu.Unlock()
	if cc.C.DeniedNoted != denied.Noted() || len(cc.C.Denied) == 0 || cc.C.Denied[0].What != "git status in /zz-calls-elsewhere" {
		t.Fatalf("the copy has %d: %+v", cc.C.DeniedNoted, cc.C.Denied)
	}
}
