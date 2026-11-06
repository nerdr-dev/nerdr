package core

import (
	"encoding/json"
	"errors"
	"os/exec"
	"slices"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/spawn"
)

// RunProc is the program of a plugin's run, which nerdr runs, and what it
// has printed: kept by the goroutines that read it and wait for it, and
// read by the model and by `nerdr plugin run` asking for it, under mu.
type RunProc struct {
	Proc     spawn.Proc
	Notify   func()
	StopOnce sync.Once
	Stopped  chan struct{} // closed when stopping the process group is complete

	Mu sync.Mutex
	// Raw is what it printed, its standard output and error as they came,
	// the last maxRaw bytes of it: base is how many came before.
	Raw  []byte
	Base int
	Done bool
	Exit int
	// Waiting are the reads waiting for more.
	Waiting []*RunRead
}

// RunRead is a read of a run's output waiting for more, from offset on.
type RunRead struct {
	Offset   int
	Reply    func(hooks.Answer)
	Answered bool
}

// MaxRaw is the most of a run's output kept, in bytes.
const MaxRaw = 1 << 20

// ReadWait is the longest a read waits for more: a little, so the
// command waiting on the run is answered well within its request's time.
const ReadWait = 5 * time.Second

// StartProc starts s through sp with no terminal, its output to be read
// from the run, and notify called when there is more of it, or it ended.
func StartProc(sp spawn.Spawner, s spawn.Spec, notify func()) (*RunProc, error) {
	s.PTY = false
	proc, err := sp.Spawn(s)
	if err != nil {
		return nil, err
	}
	p := &RunProc{Proc: proc, Notify: notify, Stopped: make(chan struct{})}
	read := make(chan struct{})
	go func() {
		defer close(read)
		buf := make([]byte, 32<<10)
		for {
			n, err := proc.Read(buf)
			if n > 0 {
				p.Add(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		code := 0
		var exit *exec.ExitError
		if err := proc.Wait(); errors.As(err, &exit) {
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
		proc.Close()
		<-read
		p.Finish(code)
	}()
	return p, nil
}

// Add takes what the program printed.
func (p *RunProc) Add(b []byte) {
	p.Mu.Lock()
	p.Raw = append(p.Raw, b...)
	if over := len(p.Raw) - MaxRaw; over > 0 {
		p.Raw = append([]byte(nil), p.Raw[over:]...)
		p.Base += over
	}
	p.AnswerLocked(false)
	p.Mu.Unlock()
	p.Notify()
}

// Note adds a line of nerdr's to what the program printed, for whoever
// reads it.
func (p *RunProc) Note(s string) { p.Add([]byte("\n[nerdr: " + s + "]\n")) }

func (p *RunProc) Finish(code int) {
	p.Mu.Lock()
	p.Done, p.Exit = true, code
	p.AnswerLocked(true)
	p.Mu.Unlock()
	p.Notify()
}

// AnswerLocked answers the reads waiting that have something to be told;
// all of them when all is set.
func (p *RunProc) AnswerLocked(all bool) {
	left := p.Waiting[:0]
	for _, w := range p.Waiting {
		if all || p.Base+len(p.Raw) > w.Offset {
			p.Answer(w)
		} else {
			left = append(left, w)
		}
	}
	p.Waiting = left
}

// Answer answers w with what it has not read, as much as one answer
// holds.
func (p *RunProc) Answer(w *RunRead) {
	if w.Answered {
		return
	}
	w.Answered = true
	out := hooks.RunOutput{Lost: w.Offset < p.Base}
	from := max(w.Offset, p.Base) - p.Base
	chunk := p.Raw[min(from, len(p.Raw)):]
	if len(chunk) > hooks.MaxPluginOutput {
		n := hooks.MaxPluginOutput
		for n > hooks.MaxPluginOutput-utf8.UTFMax && !utf8.RuneStart(chunk[n]) {
			n-- // a character is not cut in two
		}
		chunk = chunk[:n]
	}
	out.Text = string(chunk)
	out.Offset = p.Base + min(from, len(p.Raw)) + len(chunk)
	out.Ended = p.Done && out.Offset == p.Base+len(p.Raw)
	out.Exit = p.Exit
	b, _ := json.Marshal(out)
	w.Reply(hooks.Answer{Text: string(b)})
}

// Read answers with what the program printed from offset on, waiting a
// little for it while there is nothing yet.
func (p *RunProc) Read(offset int, reply func(hooks.Answer)) {
	p.Mu.Lock()
	defer p.Mu.Unlock()
	w := &RunRead{Offset: offset, Reply: reply}
	if p.Done || p.Base+len(p.Raw) > offset {
		p.Answer(w)
		return
	}
	p.Waiting = append(p.Waiting, w)
	time.AfterFunc(ReadWait, func() {
		p.Mu.Lock()
		defer p.Mu.Unlock()
		p.Answer(w) // nothing yet, if it was not answered
		// Let go of it: a run that prints nothing for long is read every
		// few seconds, and would otherwise keep every read.
		p.Waiting = slices.DeleteFunc(p.Waiting, func(x *RunRead) bool { return x == w })
	})
}

// Since is what the program printed from offset on, where that ends, and
// whether it has ended, with what exit status.
func (p *RunProc) Since(offset int) (text string, next int, done bool, exit int) {
	p.Mu.Lock()
	defer p.Mu.Unlock()
	from := max(offset, p.Base) - p.Base
	return string(p.Raw[min(from, len(p.Raw)):]), p.Base + len(p.Raw), p.Done, p.Exit
}

// Stop stops the program and what it started, as ctrl+c in a terminal
// would, and for good if it has not ended in a few seconds.
func (p *RunProc) Stop() {
	p.StopOnce.Do(func() {
		pid := p.Proc.Pid()
		// Wait reaps only the leader: what it started may still be in its
		// process group after p.done, so ask the group, not p.done. If
		// the number has been reused as another group in the meantime,
		// this cannot tell; the tick below stops once the group is gone.
		if p.Proc.Signal(pid, 0) != nil {
			close(p.Stopped)
			return
		}
		err := p.Proc.Signal(pid, syscall.SIGTERM)
		go func() {
			defer close(p.Stopped)
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
					if p.Proc.Signal(pid, 0) == syscall.ESRCH {
						return
					}
				case <-deadline.C:
					// Wait reaps only the leader. Children may still be
					// running even though p.done is true.
					_ = p.Proc.Signal(pid, syscall.SIGKILL)
					return
				}
			}
		}()
	})
}
