package term

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// heldProc is a program as a holder keeps it: what it wrote, read from a
// position on. It ends once all of it is read.
type heldProc struct {
	mu   sync.Mutex
	out  *bytes.Reader
	read chan struct{}
	once sync.Once
}

func newHeldProc(out []byte) *heldProc {
	return &heldProc{out: bytes.NewReader(out), read: make(chan struct{})}
}

func (p *heldProc) Read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n, err := p.out.Read(b[:min(len(b), 37)])
	if err == io.EOF {
		p.once.Do(func() { close(p.read) })
	}
	return n, err
}
func (p *heldProc) Write(b []byte) (int, error)             { return len(b), nil }
func (p *heldProc) Resize(w, h int) error                   { return nil }
func (p *heldProc) Wait() error                             { <-p.read; return nil }
func (p *heldProc) Pid() int                                { return 0 }
func (p *heldProc) Foreground() (int, bool)                 { return 0, false }
func (p *heldProc) Signal(pg int, sig syscall.Signal) error { return nil }
func (p *heldProc) Close() error                            { return nil }

// attached is a pane of out taken up at, once it has read all of out.
func attached(t *testing.T, out []byte, at Taken) *Pane {
	t.Helper()
	p := Attach(newHeldProc(out), 20, 4, at, func() {})
	select {
	case <-p.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("output never all read")
	}
	t.Cleanup(func() {
		p.CloseReplies()
		<-p.replyDone
		p.Close()
	})
	return p
}

// A pane taken up from a checkpoint and the output after it shows what one
// that read all of it does, and its checkpoints are at the position in the
// whole output.
func TestAttachFromCheckpoint(t *testing.T) {
	stream := agentStream(3, 6000)
	whole := attached(t, stream, Taken{})
	tried := 0
	for cut := 0; cut < len(stream); cut += 397 {
		first := attached(t, stream[:cut], Taken{})
		block, pos, err := first.CheckpointAt()
		if err == ErrCheckpointBusy {
			continue
		}
		tried++
		if err != nil || pos != int64(cut) {
			t.Fatalf("cut %d: checkpoint at %d, %v", cut, pos, err)
		}
		again := attached(t, stream[pos:], Taken{Pos: pos, Block: block})
		if got, want := again.Snapshot(), whole.Snapshot(); got != want {
			t.Fatalf("cut %d: %+v\nwant %+v", cut, got, want)
		}
		if _, end, err := again.CheckpointAt(); err == nil && end != int64(len(stream)) {
			t.Fatalf("cut %d: next checkpoint at %d of %d", cut, end, len(stream))
		}
		if again.Incomplete() {
			t.Fatalf("cut %d: marked incomplete", cut)
		}
	}
	if tried < 8 {
		t.Fatalf("only %d cuts outside a sequence", tried)
	}
}

// One taken up with output lost, or with a block it cannot read, is marked
// incomplete and shows what it has.
func TestAttachIncomplete(t *testing.T) {
	lost := attached(t, []byte("the rest"), Taken{Pos: 100, Incomplete: true})
	if !lost.Incomplete() || !strings.Contains(lost.Snapshot().Screen, "the rest") {
		t.Fatalf("lost: %v %q", lost.Incomplete(), lost.Snapshot().Screen)
	}
	bad := attached(t, []byte("the rest"), Taken{Pos: 100, Block: []byte("from a newer nerdr")})
	if !bad.Incomplete() || !strings.Contains(bad.Snapshot().Screen, "the rest") {
		t.Fatalf("bad block: %v %q", bad.Incomplete(), bad.Snapshot().Screen)
	}
}
