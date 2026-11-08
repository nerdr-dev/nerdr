package holder

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// A holder that crashes leaves its programs: those on a terminal are hung
// up as it closes, but one with none, a plugin's run, or one that ignores
// the hangup goes on, unseen, as the user, with no holder to stop it. So a
// holder of its own starts a watch, a process of nerdr's with nothing to
// do but read, from a pipe, the process groups the holder's programs are
// in. Once the pipe ends, the holder has: the watch kills those groups,
// and ends. It needs no more than a pipe, so it works the same on Linux
// and macOS, and it ignores the signals that end the holder's group.
//
// The holder tells it the groups as the holder itself would signal them
// (see prog.groupsLeft): a group's id is not reused while it has a
// process in it, and one seen empty is left out. An upgrade keeps the
// pipe across its exec, so the watch sees no end.

// watchArg starts a watch: nerdr holder --watch, the pipe on descriptor 3.
const watchArg = "--watch"

// watchEvery is how often the holder tells the watch what changed.
var watchEvery = 200 * time.Millisecond

// watch is the holder's side of its watch.
type watch struct {
	pid  int
	w    *os.File
	last string // the groups last told, as sent
}

// watchExe is the program a watch is started as.
var watchExe = os.Executable

// startWatch starts a watch of the holder, nil and an error when it
// cannot: the holder runs on without one.
func startWatch() (*watch, error) {
	exe, err := watchExe()
	if err != nil {
		return nil, err
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	null, err := os.Open(os.DevNull)
	if err != nil {
		w.Close()
		return nil, err
	}
	defer null.Close()
	p, err := os.StartProcess(exe, []string{exe, "holder", watchArg}, &os.ProcAttr{
		Files: []*os.File{null, null, os.Stderr, r},
	})
	if err != nil {
		w.Close()
		return nil, err
	}
	pid := p.Pid
	_ = p.Release() // waited for by its pid, as across an upgrade
	return &watch{pid: pid, w: w}, nil
}

// watchGroups are the process groups the holder's programs may still
// have a process in. h.mu is held.
func (h *Holder) watchGroups() []int {
	var out []int
	for _, id := range h.order {
		p := h.progs[id]
		p.groupsLeft()
		if !p.pgGone {
			out = append(out, p.pid)
		}
		for pg := range p.groups {
			if pg != p.pid {
				out = append(out, pg)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// tellWatch tells the watch the groups now, when they changed. It is
// done under h.mu, so an upgrade never execs in the middle of a line.
// h.mu is held.
func (h *Holder) tellWatch() {
	if h.watcher == nil || h.watcher.w == nil {
		return
	}
	var b strings.Builder
	for i, pg := range h.watchGroups() {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(strconv.Itoa(pg))
	}
	line := b.String()
	if line == h.watcher.last {
		return
	}
	// Never held up under h.mu by a watch that does not read: what is not
	// told now is told on the next tick. A line of up to PIPE_BUF bytes
	// goes whole or not at all.
	_ = h.watcher.w.SetWriteDeadline(time.Now().Add(50 * time.Millisecond))
	if _, err := h.watcher.w.WriteString(line + "\n"); err != nil {
		fmt.Fprintf(os.Stderr, "%s nerdr holder: watch: %v\n", time.Now().Format(time.TimeOnly), err)
		return
	}
	h.watcher.last = line
}

// keepWatch keeps the holder watched while it runs: it tells the watch
// what changed, and starts another should it end.
func (h *Holder) keepWatch() {
	h.mu.Lock()
	wt := h.watcher
	h.mu.Unlock()
	if wt == nil {
		return
	}
	go h.waitWatch(wt)
	go func() {
		t := time.NewTicker(watchEvery)
		defer t.Stop()
		for {
			select {
			case <-h.shut:
				return
			case <-t.C:
			}
			h.mu.Lock()
			h.tellWatch()
			h.mu.Unlock()
		}
	}()
}

// waitWatch reaps watch wt once it ends, and starts another while the
// holder runs on.
func (h *Holder) waitWatch(wt *watch) {
	var ws syscall.WaitStatus
	for {
		_, err := syscall.Wait4(wt.pid, &ws, 0, nil)
		if err != syscall.EINTR {
			break
		}
	}
	select {
	case <-h.shut:
		return
	default:
	}
	fmt.Fprintf(os.Stderr, "%s nerdr holder: its watch ended (%v): starting another\n", time.Now().Format(time.TimeOnly), ws)
	next, err := startWatch()
	h.mu.Lock()
	if h.watcher == wt {
		wt.w.Close()
		h.watcher = next
		if err == nil {
			h.tellWatch()
		}
	}
	h.mu.Unlock()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s nerdr holder: watch: %v\n", time.Now().Format(time.TimeOnly), err)
		return
	}
	go h.waitWatch(next)
}

// watchMain is the watch: it keeps the last groups the holder told it,
// and kills them once the holder is gone.
func watchMain() int {
	signal.Ignore(syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGPIPE)
	f := os.NewFile(3, "holder")
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	var groups []int
	for sc.Scan() {
		groups = groups[:0]
		for _, s := range strings.Fields(sc.Text()) {
			if pg, err := strconv.Atoi(s); err == nil && pg > 1 {
				groups = append(groups, pg)
			}
		}
	}
	// The holder is gone, or let go of the pipe: nothing it ran goes on.
	for _, pg := range groups {
		_ = syscall.Kill(-pg, syscall.SIGKILL)
	}
	if len(groups) > 0 {
		fmt.Fprintf(os.Stderr, "%s nerdr holder: watch: the holder is gone; killed the process groups %v\n", time.Now().Format(time.TimeOnly), groups)
	}
	return 0
}
