// Package term runs a program in a pseudo-terminal and keeps a virtual
// screen of what it has drawn, so a tab can show the agent and nerdr can
// read that screen to tell what the agent is doing.
package term

import (
	"encoding/base64"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nerdr-dev/nerdr/internal/denied"

	"github.com/creack/pty"
)

// Pane is one program running in a pseudo-terminal, and its Screen.
type Pane struct {
	*Screen
	pty *os.File
	cmd *exec.Cmd

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

	notify func()
}

// Start launches argv in dir inside a pty of the given size. notify is
// called (from another goroutine) whenever the screen changes or the
// program exits; it must not block.
func Start(argv []string, dir string, env []string, w, h int, notify func()) (*Pane, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	return StartCmd(cmd, w, h, notify)
}

// StartCmd starts a command already prepared, as Start does: its own
// argv, environment and extra files, and nerdr's re-exec helper among
// them.
func StartCmd(cmd *exec.Cmd, w, h int, notify func()) (*Pane, error) {
	w, h = max(w, 10), max(h, 3)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(w), Rows: uint16(h)})
	if denied.Err(err) != nil {
		return nil, err
	}
	p := &Pane{
		Screen:    NewScreen(w, h),
		pty:       f,
		cmd:       cmd,
		started:   time.Now(),
		done:      make(chan struct{}),
		readDone:  make(chan struct{}),
		replyDone: make(chan struct{}),
		outWake:   make(chan struct{}, 1),
		notify:    notify,
	}
	go p.readLoop()
	go p.replyLoop()
	go p.writeLoop()
	go p.wait()
	return p, nil
}

func (p *Pane) readLoop() {
	defer close(p.readDone)
	buf := make([]byte, 32*1024)
	for {
		n, err := p.pty.Read(buf)
		if n > 0 {
			p.Feed(buf[:n])
			if p.drawnOn.Load() == frame.Load() {
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
			_, _ = p.pty.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func (p *Pane) wait() {
	err := p.cmd.Wait()
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
	_ = p.pty.Close()
	p.notify()
}

// Done is closed when the program exits.
func (p *Pane) Done() <-chan struct{} { return p.done }

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
			if _, err := p.pty.Write(buf[:n]); err != nil {
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
		_ = pty.Setsize(p.pty, &pty.Winsize{Cols: uint16(w), Rows: uint16(h)})
	}
}

// Pid is the process of the program started, 0 when there is none.
func (p *Pane) Pid() int {
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Stop hangs up the program's process group and the terminal's foreground
// job, then kills what remains after grace. Interactive shells put jobs in
// their own group, so stopping only the shell's group can leave a server alive.
// Once a group disappears, forget it: its id could be reused.
func (p *Pane) Stop(grace time.Duration) {
	if p.cmd.Process == nil {
		return
	}
	groups := []int{p.cmd.Process.Pid}
	if pg, ok := p.foreground(); ok && pg > 0 && pg != groups[0] {
		groups = append(groups, pg)
	}
	alive := groups[:0]
	for _, pg := range groups {
		if syscall.Kill(-pg, syscall.SIGHUP) != syscall.ESRCH {
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
				if syscall.Kill(-pg, 0) != syscall.ESRCH {
					remaining = append(remaining, pg)
				}
			}
			alive = remaining
		case <-deadline.C:
			for _, pg := range alive {
				_ = syscall.Kill(-pg, syscall.SIGKILL)
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
