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
	"github.com/nerdr-dev/nerdr/internal/detect"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// Pane is one program running in a pseudo-terminal.
type Pane struct {
	mu  sync.Mutex
	emu *vt.Emulator
	pty *os.File
	cmd *exec.Cmd

	// osc holds each OSC back from the emulator until it has ended; see
	// oscFilter.
	osc      oscFilter
	oscOut   []byte
	title    string
	progress string
	// copied is what the program last put on the clipboard with OSC 52,
	// until the app takes it; see TakeCopied.
	copied   string
	cursorOn bool
	// Modes the program has asked for that change what keys and pastes must
	// look like on the way in; see Input.
	appCursor      bool
	bracketedPaste bool
	focusEvents    bool
	// The mouse reports the program has asked for; see mouse.go.
	mouseOn  map[ansi.DECMode]bool
	mouseSgr bool
	// sel is the text chosen with the mouse, drawn highlighted.
	sel *Selection

	started time.Time
	exited  bool
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

	// dirty is set by whatever changes what Render draws: output, a
	// resize, a selection, the exit. Until it is, Render gives the lines it
	// drew last again, for the same scroll and cursor.
	dirty bool
	drawn struct {
		lines  []string
		scroll int
		cursor bool
	}
	// gen counts the changes to what Snapshot reads: output, which also
	// sets the title and progress, and a resize. snap is the snapshot
	// last taken, at snapGen.
	gen     uint64
	snap    detect.Input
	snapGen uint64
	// inputs counts what has been typed into it; see Inputs.
	inputs atomic.Uint64
	// lastOutput is when the program last wrote anything; see Idle.
	lastOutput time.Time
	// drawnOn is the frame the pane was last drawn on; see NextFrame.
	drawnOn atomic.Uint64

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
		emu:        vt.NewEmulator(w, h),
		pty:        f,
		cmd:        cmd,
		cursorOn:   true,
		started:    time.Now(),
		lastOutput: time.Now(),
		done:       make(chan struct{}),
		readDone:   make(chan struct{}),
		replyDone:  make(chan struct{}),
		outWake:    make(chan struct{}, 1),
		notify:     notify,
	}
	p.emu.SetScrollbackSize(5000)
	p.emu.SetCallbacks(vt.Callbacks{
		Title:            func(s string) { p.title = s },
		CursorVisibility: func(v bool) { p.cursorOn = v },
		EnableMode:       func(m ansi.Mode) { p.setMode(m, true) },
		DisableMode:      func(m ansi.Mode) { p.setMode(m, false) },
	})
	// OSC 9;4 is the progress report some agents use to say they are busy.
	p.emu.RegisterOscHandler(9, func(data []byte) bool {
		s := string(data)
		s = strings.TrimPrefix(s, "9;")
		if strings.HasPrefix(s, "4;") {
			p.progress = s
		}
		return true
	})
	// OSC 52 sets the clipboard: a program that takes the mouse, as Copilot
	// does, copies what is chosen in it this way, which the emulator would
	// drop. A query for what is on the clipboard is never answered.
	p.emu.RegisterOscHandler(52, func(data []byte) bool {
		if text, ok := clipboardSet(data); ok {
			p.copied = text
		}
		return true
	})
	go p.readLoop()
	go p.replyLoop()
	go p.writeLoop()
	go p.wait()
	return p, nil
}

func (p *Pane) setMode(m ansi.Mode, on bool) {
	if p.setMouseMode(m, on) {
		return
	}
	switch m {
	case ansi.ModeCursorKeys:
		p.appCursor = on
	case ansi.ModeBracketedPaste:
		p.bracketedPaste = on
	case ansi.ModeFocusEvent:
		p.focusEvents = on
	}
}

func (p *Pane) readLoop() {
	defer close(p.readDone)
	buf := make([]byte, 32*1024)
	for {
		n, err := p.pty.Read(buf)
		if n > 0 {
			p.mu.Lock()
			p.oscOut = p.osc.write(p.oscOut[:0], buf[:n], func(s string) { p.title = s })
			_, _ = p.emu.Write(p.oscOut)
			p.dirty = true
			p.gen++
			p.lastOutput = time.Now()
			p.mu.Unlock()
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
		n, err := p.emu.Read(buf)
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
	p.dirty = true
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

// Resize changes the pty and the virtual screen to w×h.
func (p *Pane) Resize(w, h int) {
	w, h = max(w, 10), max(h, 3)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.emu.Width() == w && p.emu.Height() == h {
		return
	}
	p.emu.Resize(w, h)
	p.dirty = true
	p.gen++
	if !p.exited {
		_ = pty.Setsize(p.pty, &pty.Winsize{Cols: uint16(w), Rows: uint16(h)})
	}
}

// Size is the virtual screen's width and height.
func (p *Pane) Size() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.emu.Width(), p.emu.Height()
}

// Pid is the process of the program started, 0 when there is none.
func (p *Pane) Pid() int {
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Stop asks the program, and what it started, to exit (SIGHUP, as a
// closing terminal would), and kills what is left of them if they have not
// gone within the grace period.
//
// pty.Start makes the child a session leader, so its pid is also its
// process group, and the group is what is waited for, not the program: it
// may have exited, at the hangup or before, leaving a child that ignores
// it, which would go on writing to the worktree after the tab is closed
// and its worktree deleted. The group's id is not given to another while
// any of it is left; once it is all gone the number may be reused, as in
// runProc.stop, which a hangup and a kill could then reach.
func (p *Pane) Stop(grace time.Duration) {
	if p.cmd.Process == nil {
		return
	}
	pid := p.cmd.Process.Pid
	if syscall.Kill(-pid, syscall.SIGHUP) == syscall.ESRCH {
		return // nothing of it is left
	}
	deadline := time.NewTimer(grace)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			if syscall.Kill(-pid, 0) == syscall.ESRCH {
				return
			}
		case <-deadline.C:
			_ = syscall.Kill(-pid, syscall.SIGKILL)
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

// TakeCopied is what the program has put on the clipboard since it was
// last asked, or "".
func (p *Pane) TakeCopied() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.copied
	p.copied = ""
	return s
}

// Title is the window title the program last set.
func (p *Pane) Title() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.title
}

// Snapshot is the input for status detection: the screen as plain text
// plus the title and progress reports. It is taken again only once the
// program has drawn since: an agent's state is looked at every few tenths
// of a second, and most of the time nothing has changed.
func (p *Pane) Snapshot() detect.Input {
	in, _ := p.SnapshotGen()
	return in
}

// SnapshotGen is Snapshot, and a number that changes whenever the
// snapshot can have: a caller that worked something out from the last
// one need not again while it is the same.
func (p *Pane) SnapshotGen() (detect.Input, uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.snapGen != p.gen || p.snapGen == 0 {
		p.snap, p.snapGen = p.snapshot(), p.gen
	}
	return p.snap, p.gen
}

func (p *Pane) snapshot() detect.Input {
	w, h := p.emu.Width(), p.emu.Height()
	var b strings.Builder
	for y := 0; y < h; y++ {
		var line strings.Builder
		for x := 0; x < w; x++ {
			c := p.emu.CellAt(x, y)
			if c == nil {
				continue
			}
			if c.Width == 0 {
				if c.Content != "" {
					line.WriteString(c.Content)
				}
				continue
			}
			if c.Content == "" {
				line.WriteByte(' ')
			} else {
				line.WriteString(c.Content)
			}
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		if y < h-1 {
			b.WriteByte('\n')
		}
	}
	return detect.Input{Screen: b.String(), Title: p.title, Progress: p.progress}
}

// Quiet reports how long it has been since the program last drew anything.
func (p *Pane) Quiet() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return time.Since(p.lastOutput)
}

// LastOutput is when the program last drew anything.
func (p *Pane) LastOutput() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastOutput
}
