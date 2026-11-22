package term

import (
	"bytes"
	"errors"
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
func (p *heldProc) StartTime() uint64                       { return 0 }
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

// recordingProc is a heldProc that keeps what is written to it.
type recordingProc struct {
	*heldProc
	inMu sync.Mutex
	in   bytes.Buffer
}

func (p *recordingProc) Write(b []byte) (int, error) {
	p.inMu.Lock()
	defer p.inMu.Unlock()
	return p.in.Write(b)
}

func (p *recordingProc) written() string {
	p.inMu.Lock()
	defer p.inMu.Unlock()
	return p.in.String()
}

// A pane taken up does not answer again the queries its program wrote
// before (Taken.Answered): the answer would reach it as keys typed in.
// Those after are answered, also in the read that has both.
func TestAttachAnswersOnlyWhatIsNew(t *testing.T) {
	old := strings.Repeat("o", 40) + "\x1b[6n" + strings.Repeat("o", 16) // asks where the cursor is
	now := "\x1b[c" + "new"                                              // asks what the terminal is
	proc := &recordingProc{heldProc: newHeldProc([]byte(old + now))}
	p := Attach(proc, 20, 4, Taken{Answered: int64(len(old))}, func() {})
	t.Cleanup(func() {
		p.CloseReplies()
		<-p.replyDone
		p.Close()
	})
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(proc.written(), "\x1b[?") {
		if time.Now().After(deadline) {
			t.Fatalf("the new query was not answered: %q", proc.written())
		}
		time.Sleep(10 * time.Millisecond)
	}
	<-p.Done()
	if got := proc.written(); strings.Contains(got, "R") {
		t.Errorf("the old query was answered again: %q", got)
	}
	if !strings.Contains(p.Snapshot().Screen, "new") {
		t.Errorf("screen %q", p.Snapshot().Screen)
	}
}

// TestAttachToAProgramThatAskedForInBandResizes: a program that asked for
// in-band resizes (mode 2048), taken up at another size than its
// checkpoint's, is told of the new size; taking it up does not wait on
// the answer forever.
func TestAttachToAProgramThatAskedForInBandResizes(t *testing.T) {
	first := attached(t, []byte("\x1b[?2048hready"), Taken{})
	block, pos, err := first.CheckpointAt()
	if err != nil {
		t.Fatal(err)
	}
	proc := &recordingProc{heldProc: newHeldProc(nil)}
	attachedc := make(chan *Pane, 1)
	go func() { attachedc <- Attach(proc, 30, 6, Taken{Pos: pos, Block: block}, func() {}) }()
	var p *Pane
	select {
	case p = <-attachedc:
	case <-time.After(5 * time.Second):
		t.Fatal("Attach never returned")
	}
	t.Cleanup(func() {
		p.CloseReplies()
		<-p.replyDone
		p.Close()
	})
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(proc.written(), "\x1b[48;6;30") {
		if time.Now().After(deadline) {
			t.Fatalf("not told of the new size: %q", proc.written())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestAttachReadsAtTheSizeItIsGiven: what a program taken up wrote from
// the checkpoint on lands on the screen at the size Attach is given, not
// the checkpoint's, however soon it is read.
func TestAttachReadsAtTheSizeItIsGiven(t *testing.T) {
	first := attached(t, nil, Taken{}) // 20×4
	block, pos, err := first.CheckpointAt()
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 30)
	for range 20 {
		p := Attach(newHeldProc([]byte(line)), 40, 4, Taken{Pos: pos, Block: block}, func() {})
		select {
		case <-p.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("output never all read")
		}
		got := p.Snapshot().Screen
		p.Close()
		if !strings.Contains(got, line) {
			t.Fatalf("not read at 40 columns: %q", got)
		}
	}
}

// goneProc is a program whose holder has gone: no signal reaches it, nor
// will. Its output ends once it is closed.
type goneProc struct {
	closed chan struct{}
	once   sync.Once
}

func (p *goneProc) Read(b []byte) (int, error)              { <-p.closed; return 0, io.EOF }
func (p *goneProc) Write(b []byte) (int, error)             { return 0, io.ErrClosedPipe }
func (p *goneProc) Resize(w, h int) error                   { return nil }
func (p *goneProc) Wait() error                             { <-p.closed; return nil }
func (p *goneProc) Pid() int                                { return 1 << 30 }
func (p *goneProc) StartTime() uint64                       { return 0 }
func (p *goneProc) Foreground() (int, bool)                 { return 0, false }
func (p *goneProc) Signal(pg int, sig syscall.Signal) error { return errors.New("holder: gone") }
func (p *goneProc) Close() error                            { p.once.Do(func() { close(p.closed) }); return nil }

// TestStopDoesNotWaitOutTheGraceForWhatItCannotReach: a program out of
// reach for good is not waited for, and Close lets go of its terminal,
// which ends the reading, before the screen goes.
func TestStopDoesNotWaitOutTheGraceForWhatItCannotReach(t *testing.T) {
	proc := &goneProc{closed: make(chan struct{})}
	p := newPane(proc, NewScreen(20, 4), time.Now(), func() {})
	p.run()
	start := time.Now()
	p.Stop(5 * time.Second)
	if d := time.Since(start); d > time.Second {
		t.Errorf("Stop waited %v for a program it cannot reach", d)
	}
	p.Close()
	select {
	case <-p.readDone:
	default:
		t.Error("Close returned with the output still being read")
	}
}
