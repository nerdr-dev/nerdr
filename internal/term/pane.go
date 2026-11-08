// Package term runs a program in a pseudo-terminal and keeps a virtual
// screen of what it has drawn, so a tab can show the agent and nerdr can
// read that screen to tell what the agent is doing.
package term

import (
	"encoding/base64"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nerdr-dev/nerdr/internal/spawn"
)

// Pane is one program running in a pseudo-terminal, and its Screen.
type Pane struct {
	*Screen
	proc spawn.Proc

	started time.Time
	// exitErr is how the program exited; Screen.mu guards it.
	exitErr error
	done    chan struct{}
	// readDone is closed when all output has been read, which on some
	// systems is after the program has exited.
	readDone chan struct{}
	// replyDone is closed when replyLoop has returned; see Close.
	replyDone chan struct{}
	// out is what is to be written to the program, in order, by
	// writeLoop; outWake tells it there is some. See Write.
	// outDone is set once writeLoop has stopped: what is written after
	// is dropped, as is what was still waiting.
	outMu   sync.Mutex
	out     [][]byte
	outDone bool
	outWake chan struct{}

	// inputs counts what has been typed into it; see Inputs.
	inputs atomic.Uint64
	// base is the position in the program's output the pane started
	// reading at, and answered how far the queries in it were answered
	// before; see Attach.
	base, answered int64

	notify func()
	// watched is set while a client in another process is sent the
	// screen: it is told of every change, drawn here or not. See Watch.
	watched atomic.Bool
}

// Start launches argv in dir inside a pty of the given size. notify is
// called (from another goroutine) whenever the screen changes or the
// program exits; it must not block.
func Start(argv []string, dir string, env []string, w, h int, notify func()) (*Pane, error) {
	return StartWith(spawn.Local{}, spawn.Spec{Argv: argv, Dir: dir, Env: env, W: w, H: h}, notify)
}

// StartWith starts s in a terminal through sp, as Start does: s.PTY is
// set, and the size made at least 10×3.
func StartWith(sp spawn.Spawner, s spawn.Spec, notify func()) (*Pane, error) {
	s.PTY = true
	s.W, s.H = max(s.W, 10), max(s.H, 3)
	proc, err := sp.Spawn(s)
	if err != nil {
		return nil, err
	}
	p := newPane(proc, NewScreen(s.W, s.H), time.Now(), notify)
	p.run()
	return p, nil
}

// Taken is where a program already running is taken up from: the
// position in its output reading starts at, the checkpoint of its screen
// for that position if there is one, and when it started. Incomplete is
// set when what is read from Pos on, on top of Block, is not all it drew:
// output was lost, or it wrote before Pos and there is no checkpoint.
//
// Answered is how far the output went when it was taken up: the queries
// in it before that were answered by the core before, or came while there
// was none, and an answer now would reach the program long after it
// stopped waiting, as keys typed in. They are not answered again.
type Taken struct {
	Pos        int64
	Block      []byte
	Incomplete bool
	Started    time.Time
	Answered   int64
}

// Attach is a pane of proc, a program already running in a terminal, as
// a core started after another takes it up: its screen is put back from
// at.Block, and what it wrote from at.Pos on is read onto it. The screen
// is marked incomplete when at says so or the block cannot be put back.
// It is then the size w×h, as the caller has it, and the program is told.
func Attach(proc spawn.Proc, w, h int, at Taken, notify func()) *Pane {
	sc := NewScreen(w, h)
	if at.Block != nil {
		_ = sc.Restore(at.Block)
	}
	if at.Incomplete {
		sc.MarkIncomplete()
	}
	started := at.Started
	if started.IsZero() {
		started = time.Now()
	}
	p := newPane(proc, sc, started, notify)
	p.base, p.answered = at.Pos, at.Answered
	// Running first: a program that asked for in-band resizes (mode 2048)
	// is told of this one, which waits for its reader till then.
	p.run()
	sc.Resize(w, h)
	_ = proc.Resize(max(w, 10), max(h, 3))
	return p
}

func newPane(proc spawn.Proc, sc *Screen, started time.Time, notify func()) *Pane {
	return &Pane{
		Screen:    sc,
		proc:      proc,
		started:   started,
		done:      make(chan struct{}),
		readDone:  make(chan struct{}),
		replyDone: make(chan struct{}),
		outWake:   make(chan struct{}, 1),
		notify:    notify,
	}
}

func (p *Pane) run() {
	go p.readLoop()
	go p.replyLoop()
	go p.writeLoop()
	go p.wait()
}

// HeldID is the program's id in the holder that keeps it, "" for one
// that runs in this process.
func (p *Pane) HeldID() string {
	if h, ok := p.proc.(interface{ ID() string }); ok {
		return h.ID()
	}
	return ""
}

// Position is how far into the program's output the screen has got: the
// position a checkpoint taken now would be for.
func (p *Pane) Position() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.base + p.fed
}

// CheckpointAt is the screen's checkpoint, as Screen.Checkpoint, and the
// position in the program's output it is for: what was read before Attach
// and all that has reached the screen since.
func (p *Pane) CheckpointAt() ([]byte, int64, error) {
	block, fed, err := p.checkpoint()
	return block, p.base + fed, err
}

func (p *Pane) readLoop() {
	defer close(p.readDone)
	buf := make([]byte, 32*1024)
	for {
		n, err := p.proc.Read(buf)
		if n > 0 {
			b := buf[:n]
			if old := p.answered - p.Position(); old > 0 {
				k := min(int64(len(b)), old)
				p.FeedAnswered(b[:k])
				b = b[k:]
			}
			if len(b) > 0 {
				p.Feed(b)
			}
			if p.drawnOn.Load() == frame.Load() || p.watched.Load() {
				p.notify()
			}
		}
		if err != nil {
			return
		}
	}
}

// replyLoop forwards what the emulator answers to the program's queries
// (cursor position, device attributes, colours) back into the pty.
func (p *Pane) replyLoop() {
	defer close(p.replyDone)
	buf := make([]byte, 4096)
	for {
		n, err := p.ReadReply(buf)
		if n > 0 {
			_, _ = p.proc.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func (p *Pane) wait() {
	err := p.proc.Wait()
	// Let the last of the output reach the screen before saying the
	// program is gone; a background child holding the pty open must not
	// keep the tab waiting, though.
	select {
	case <-p.readDone:
	case <-time.After(time.Second):
	}
	p.mu.Lock()
	p.exited, p.exitErr = true, err
	p.drawGen++
	p.mu.Unlock()
	close(p.done)
	p.dropWrites()
	_ = p.proc.Close()
	p.notify()
}

// Done is closed when the program exits.
func (p *Pane) Done() <-chan struct{} { return p.done }

// Watch has the pane call its notify on every change while on, as it
// otherwise does only for one drawn on the last frame (see NextFrame): its
// screen is sent to a client in another process, which draws it there.
func (p *Pane) Watch(on bool) { p.watched.Store(on) }

// Exited reports whether the program has exited, how, and how long it ran.
func (p *Pane) Exited() (bool, error, time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exited, p.exitErr, time.Since(p.started)
}

// Write sends raw input to the program. It does not wait for the program
// to take it: a program busy with other things reads its input late, and
// a large paste into one would otherwise hold up the keyboard, the prefix
// key included, and the screen, until it did. What is written goes in in
// the order it was written.
func (p *Pane) Write(b []byte) {
	p.mu.Lock()
	exited := p.exited
	p.mu.Unlock()
	if exited || len(b) == 0 {
		return
	}
	p.outMu.Lock()
	if p.outDone {
		p.outMu.Unlock()
		return
	}
	p.out = append(p.out, append([]byte(nil), b...))
	p.outMu.Unlock()
	select {
	case p.outWake <- struct{}{}:
	default:
	}
}

// writeLoop writes what Write was given to the program, until it exits
// or takes no more. It writes a little at a time from a buffer of its
// own: a write the program never takes, one that has stopped reading,
// can hang for good, and then holds no more than that buffer. What is
// still waiting is let go when the program exits (see wait).
func (p *Pane) writeLoop() {
	defer p.dropWrites()
	buf := make([]byte, 4<<10)
	for {
		select {
		case <-p.outWake:
		case <-p.done:
			return
		}
		for {
			p.outMu.Lock()
			if p.outDone || len(p.out) == 0 {
				p.outMu.Unlock()
				break
			}
			n := copy(buf, p.out[0])
			if p.out[0] = p.out[0][n:]; len(p.out[0]) == 0 {
				p.out[0] = nil
				p.out = p.out[1:]
			}
			p.outMu.Unlock()
			if _, err := p.proc.Write(buf[:n]); err != nil {
				return
			}
		}
	}
}

// dropWrites lets go of what was written but not taken, and of whatever
// is written from now on.
func (p *Pane) dropWrites() {
	p.outMu.Lock()
	p.out, p.outDone = nil, true
	p.outMu.Unlock()
}

// Input sends typed bytes, first rewriting what the outer terminal sent
// into what the program has asked for. The outer terminal encodes keys for
// nerdr's own modes, not the program's: cursor keys arrive as CSI even
// when the program wants SS3, and a paste arrives bracketed whether or not
// the program turned bracketed paste on.
func (p *Pane) Input(b []byte) {
	p.inputs.Add(1)
	p.mu.Lock()
	app, bp := p.appCursor, p.bracketedPaste
	p.mu.Unlock()
	s := string(b)
	if app {
		for _, k := range []string{"A", "B", "C", "D", "H", "F"} {
			s = strings.ReplaceAll(s, "\x1b["+k, "\x1bO"+k)
		}
	}
	if !bp {
		s = strings.ReplaceAll(s, "\x1b[200~", "")
		s = strings.ReplaceAll(s, "\x1b[201~", "")
	}
	p.Write([]byte(s))
}

// Inputs is how many times something has been typed into it: a view
// scrolled back goes to the bottom again when it changes, as a terminal's
// does when you type.
func (p *Pane) Inputs() uint64 { return p.inputs.Load() }

// Focus tells the program its window gained or lost focus, if it asked.
func (p *Pane) Focus(in bool) {
	p.mu.Lock()
	on := p.focusEvents
	p.mu.Unlock()
	if !on {
		return
	}
	if in {
		p.Write([]byte("\x1b[I"))
	} else {
		p.Write([]byte("\x1b[O"))
	}
}

// Resize changes the pty and the screen to w×h.
func (p *Pane) Resize(w, h int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.resize(w, h) && !p.exited {
		w, h = p.emu.Width(), p.emu.Height()
		_ = p.proc.Resize(w, h)
	}
}

// Pid is the process of the program started, 0 when there is none.
func (p *Pane) Pid() int {
	if p.proc == nil {
		return 0
	}
	return p.proc.Pid()
}

// StartTime is when the program's process started, as spawn.Proc has
// it; 0 when not known.
func (p *Pane) StartTime() uint64 {
	if p.proc == nil {
		return 0
	}
	return p.proc.StartTime()
}

// Stop hangs up the program's process group and the terminal's foreground
// job, then kills what remains after grace. Interactive shells put jobs in
// their own group, so stopping only the shell's group can leave a server alive.
// Once a group disappears, forget it: its id could be reused.
func (p *Pane) Stop(grace time.Duration) {
	if p.Pid() == 0 {
		return
	}
	groups := []int{p.Pid()}
	if pg, ok := p.foreground(); ok && pg > 0 && pg != groups[0] {
		groups = append(groups, pg)
	}
	alive := groups[:0]
	for _, pg := range groups {
		if p.proc.Signal(pg, syscall.SIGHUP) != syscall.ESRCH {
			alive = append(alive, pg)
		}
	}
	deadline := time.NewTimer(grace)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for len(alive) > 0 {
		select {
		case <-tick.C:
			remaining := alive[:0]
			for _, pg := range alive {
				if p.proc.Signal(pg, 0) != syscall.ESRCH {
					remaining = append(remaining, pg)
				}
			}
			alive = remaining
		case <-deadline.C:
			for _, pg := range alive {
				_ = p.proc.Signal(pg, syscall.SIGKILL)
			}
			return
		}
	}
}

// clipboardSet is the text an OSC 52 ("52;Pc;Pd", Pd in base64) puts on
// the clipboard, and whether it is one that sets it.
func clipboardSet(data []byte) (string, bool) {
	_, rest, ok := strings.Cut(string(data), ";")
	if !ok {
		return "", false
	}
	_, pd, ok := strings.Cut(rest, ";")
	if !ok || pd == "?" {
		return "", false
	}
	b, err := base64.StdEncoding.DecodeString(pd)
	if err != nil || len(b) == 0 {
		return "", false
	}
	return string(b), true
}
