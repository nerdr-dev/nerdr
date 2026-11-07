package spawn

import (
	"runtime"
	"testing"
	"time"
)

// A program's start is read as it starts: one that has ended, not yet
// waited for, has it as the kernel had it.
func TestStartTimeIsReadAtStart(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("nerdr cannot ask the kernel here")
	}
	for _, pty := range []bool{false, true} {
		p, err := Local{}.Spawn(Spec{Argv: []string{"true"}, PTY: pty, W: 80, H: 24})
		if err != nil {
			t.Fatal(err)
		}
		_, want, err := ProcInfo(p.Pid())
		if err != nil || want == 0 || p.StartTime() != want {
			t.Errorf("pty %v: start %d, the kernel says %d (%v)", pty, p.StartTime(), want, err)
		}
		time.Sleep(50 * time.Millisecond) // ended, not reaped
		_ = p.Wait()
		_ = p.Close()
		if p.StartTime() != want {
			t.Errorf("pty %v: start %d once reaped, was %d", pty, p.StartTime(), want)
		}
	}
}
