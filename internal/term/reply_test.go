package term

import (
	"bytes"
	"io"
	"syscall"
	"testing"
	"time"
)

// stuckProc is a program that asks its terminal over and over and never
// reads its input: every write to it hangs until it is let go.
type stuckProc struct {
	out   *bytes.Reader
	fed   chan struct{}
	stuck chan struct{}
	done  chan struct{}
}

func (p *stuckProc) Read(b []byte) (int, error) {
	n, err := p.out.Read(b)
	if err == io.EOF {
		select {
		case <-p.fed:
		default:
			close(p.fed)
		}
		<-p.done
	}
	return n, err
}
func (p *stuckProc) Write(b []byte) (int, error) {
	select {
	case p.stuck <- struct{}{}:
	default:
	}
	<-p.done
	return 0, io.ErrClosedPipe
}
func (p *stuckProc) Resize(w, h int) error                   { return nil }
func (p *stuckProc) Wait() error                             { <-p.done; return nil }
func (p *stuckProc) Pid() int                                { return 0 }
func (p *stuckProc) StartTime() uint64                       { return 0 }
func (p *stuckProc) Foreground() (int, bool)                 { return 0, false }
func (p *stuckProc) Signal(pg int, sig syscall.Signal) error { return nil }
func (p *stuckProc) Close() error                            { return nil }

// TestPaneNotHeldUpByUnreadReplies covers a program that asks for the
// cursor, the device and its colours far more than it reads: the answers
// it does not take must not hold up the pane, which reads its output and
// draws, resizes and checkpoints it all the same.
func TestPaneNotHeldUpByUnreadReplies(t *testing.T) {
	// In-band resizes too, which Resize answers.
	out := bytes.Repeat([]byte("\x1b[6n\x1b[c\x1b[>c\x1b]10;?\x07\x1b]11;?\x07\x1b[?2048$p"), 20000)
	out = append([]byte("\x1b[?2048h"), out...)
	proc := &stuckProc{out: bytes.NewReader(out), fed: make(chan struct{}), stuck: make(chan struct{}, 1), done: make(chan struct{})}
	p := newPane(proc, NewScreen(20, 4), time.Now(), func() {})
	p.run()
	defer func() {
		close(proc.done)
		<-p.Done()
		p.CloseReplies()
		<-p.replyDone
		p.Screen.Close()
	}()

	ok := make(chan struct{})
	go func() {
		defer close(ok)
		<-proc.fed
		<-proc.stuck
		for i := range 50 {
			p.Resize(20+i%5, 4+i%3)
			_ = p.Frame(0)
			_ = p.Snapshot()
			p.Feed([]byte("\x1b[6n"))
			if _, _, err := p.CheckpointAt(); err != nil {
				t.Error(err)
			}
		}
	}()
	select {
	case <-ok:
	case <-time.After(10 * time.Second):
		t.Fatal("the pane is held up by answers the program does not read")
	}
	p.outMu.Lock()
	backlog := p.outLen
	p.outMu.Unlock()
	if backlog > maxReplyBacklog+4096 {
		t.Fatalf("%d bytes of answers wait, more than %d", backlog, maxReplyBacklog)
	}
}
