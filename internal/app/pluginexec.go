package app

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"slices"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"nerdr/internal/hooks"
)

// runProc is the program of a plugin's run, which nerdr runs, and what it
// has printed: kept by the goroutines that read it and wait for it, and
// read by the model and by `nerdr plugin run` asking for it, under mu.
type runProc struct {
	cmd      *exec.Cmd
	notify   func()
	stopOnce sync.Once
	stopped  chan struct{} // closed when stopping the process group is complete

	mu sync.Mutex
	// raw is what it printed, its standard output and error as they came,
	// the last maxRaw bytes of it: base is how many came before.
	raw  []byte
	base int
	done bool
	exit int
	// waiting are the reads waiting for more.
	waiting []*runRead
}

// runRead is a read of a run's output waiting for more, from offset on.
type runRead struct {
	offset   int
	reply    func(hooks.Answer)
	answered bool
}

// maxRaw is the most of a run's output kept, in bytes.
const maxRaw = 1 << 20

// readWait is the longest a read waits for more: a little, so the
// command waiting on the run is answered well within its request's time.
const readWait = 5 * time.Second

// startProc starts cmd, its output to be read from the run, and notify
// called when there is more of it, or it ended.
func startProc(cmd *exec.Cmd, notify func()) (*runProc, error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = pw, pw
	// Its own process group, so a stop stops what it started too.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		return nil, err
	}
	pw.Close()
	p := &runProc{cmd: cmd, notify: notify, stopped: make(chan struct{})}
	read := make(chan struct{})
	go func() {
		defer close(read)
		buf := make([]byte, 32<<10)
		for {
			n, err := pr.Read(buf)
			if n > 0 {
				p.add(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		code := 0
		var exit *exec.ExitError
		if err := cmd.Wait(); errors.As(err, &exit) {
			code = exit.ExitCode()
			if code < 0 {
				code = 128 + int(exit.Sys().(syscall.WaitStatus).Signal())
			}
		} else if err != nil {
			code = 1
		}
		// What it started may hold its output open: what came is read for
		// a moment, and the rest left.
		select {
		case <-read:
		case <-time.After(time.Second):
		}
		pr.Close()
		<-read
		p.finish(code)
	}()
	return p, nil
}

// add takes what the program printed.
func (p *runProc) add(b []byte) {
	p.mu.Lock()
	p.raw = append(p.raw, b...)
	if over := len(p.raw) - maxRaw; over > 0 {
		p.raw = append([]byte(nil), p.raw[over:]...)
		p.base += over
	}
	p.answerLocked(false)
	p.mu.Unlock()
	p.notify()
}

// note adds a line of nerdr's to what the program printed, for whoever
// reads it.
func (p *runProc) note(s string) { p.add([]byte("\n[nerdr: " + s + "]\n")) }

func (p *runProc) finish(code int) {
	p.mu.Lock()
	p.done, p.exit = true, code
	p.answerLocked(true)
	p.mu.Unlock()
	p.notify()
}

// answerLocked answers the reads waiting that have something to be told;
// all of them when all is set.
func (p *runProc) answerLocked(all bool) {
	left := p.waiting[:0]
	for _, w := range p.waiting {
		if all || p.base+len(p.raw) > w.offset {
			p.answer(w)
		} else {
			left = append(left, w)
		}
	}
	p.waiting = left
}

// answer answers w with what it has not read, as much as one answer
// holds.
func (p *runProc) answer(w *runRead) {
	if w.answered {
		return
	}
	w.answered = true
	out := hooks.RunOutput{Lost: w.offset < p.base}
	from := max(w.offset, p.base) - p.base
	chunk := p.raw[min(from, len(p.raw)):]
	if len(chunk) > hooks.MaxPluginOutput {
		n := hooks.MaxPluginOutput
		for n > hooks.MaxPluginOutput-utf8.UTFMax && !utf8.RuneStart(chunk[n]) {
			n-- // a character is not cut in two
		}
		chunk = chunk[:n]
	}
	out.Text = string(chunk)
	out.Offset = p.base + min(from, len(p.raw)) + len(chunk)
	out.Ended = p.done && out.Offset == p.base+len(p.raw)
	out.Exit = p.exit
	b, _ := json.Marshal(out)
	w.reply(hooks.Answer{Text: string(b)})
}

// read answers with what the program printed from offset on, waiting a
// little for it while there is nothing yet.
func (p *runProc) read(offset int, reply func(hooks.Answer)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	w := &runRead{offset: offset, reply: reply}
	if p.done || p.base+len(p.raw) > offset {
		p.answer(w)
		return
	}
	p.waiting = append(p.waiting, w)
	time.AfterFunc(readWait, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.answer(w) // nothing yet, if it was not answered
		// Let go of it: a run that prints nothing for long is read every
		// few seconds, and would otherwise keep every read.
		p.waiting = slices.DeleteFunc(p.waiting, func(x *runRead) bool { return x == w })
	})
}

// since is what the program printed from offset on, where that ends, and
// whether it has ended, with what exit status.
func (p *runProc) since(offset int) (text string, next int, done bool, exit int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	from := max(offset, p.base) - p.base
	return string(p.raw[min(from, len(p.raw)):]), p.base + len(p.raw), p.done, p.exit
}

// stop stops the program and what it started, as ctrl+c in a terminal
// would, and for good if it has not ended in a few seconds.
func (p *runProc) stop() {
	p.stopOnce.Do(func() {
		pid := p.cmd.Process.Pid
		// Wait reaps only the leader: what it started may still be in its
		// process group after p.done, so ask the group, not p.done. If
		// the number has been reused as another group in the meantime,
		// this cannot tell; the tick below stops once the group is gone.
		if syscall.Kill(-pid, 0) != nil {
			close(p.stopped)
			return
		}
		err := syscall.Kill(-pid, syscall.SIGTERM)
		go func() {
			defer close(p.stopped)
			if err == syscall.ESRCH {
				return
			}
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			tick := time.NewTicker(50 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-tick.C:
					// Stop checking once the group is gone, rather than
					// signalling its number later if it has been reused.
					if syscall.Kill(-pid, 0) == syscall.ESRCH {
						return
					}
				case <-deadline.C:
					// Wait reaps only the leader. Children may still be
					// running even though p.done is true.
					_ = syscall.Kill(-pid, syscall.SIGKILL)
					return
				}
			}
		}()
	})
}
