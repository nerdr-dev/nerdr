package holder

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/nerdr-dev/nerdr/internal/spawn"
)

// EnvFD names the descriptor a core started by a holder has its end of
// the socket on.
const EnvFD = "NERDR_HOLDER_FD"

// Backoff between starts of a core that keeps ending: from the first to
// the most, doubling, and back to the first once a core has run steady.
var (
	backoffFirst  = 500 * time.Millisecond
	backoffMost   = 30 * time.Second
	backoffSteady = time.Minute
)

// Run is a holder for its life: it starts core, the argv of nerdr's core,
// with its end of a new socket pair as descriptor 3, serves it, and
// starts it again with a new pair whenever it ends, waiting longer each
// time it ends soon. It never stops a program to get on. It returns once
// a core has it shut down.
func Run(core []string) error {
	if len(core) == 0 {
		return fmt.Errorf("holder: no core to start")
	}
	return New().serveCores(core)
}

func (h *Holder) serveCores(core []string) error {
	h.mu.Lock()
	h.coreArgv = core
	h.upgrade = h.doUpgrade
	h.mu.Unlock()
	return h.keepCoreUp(core, nil)
}

// keepCoreUp starts the core again each time it ends, after first, when
// there is a core already: the one an upgraded holder serves on.
func (h *Holder) keepCoreUp(core []string, first func() error) error {
	wait := backoffFirst
	for {
		began := time.Now()
		var err error
		if first != nil {
			err, first = first(), nil
		} else {
			err = h.runCore(core)
		}
		select {
		case <-h.shut:
			return nil
		default:
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "nerdr holder: core: %v\n", err)
		}
		if time.Since(began) >= backoffSteady {
			wait = backoffFirst
		}
		select {
		case <-time.After(wait):
		case <-h.shut:
			return nil
		}
		wait = min(2*wait, backoffMost)
	}
}

// runCore starts the core with a new socket pair and serves it until it
// ends.
func (h *Holder) runCore(argv []string) error {
	ours, theirs, err := socketpair()
	if err != nil {
		return err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.ExtraFiles = []*os.File{theirs}
	cmd.Env = append(os.Environ(), EnvFD+"=3")
	err = cmd.Start()
	theirs.Close()
	if err != nil {
		ours.Close()
		return err
	}
	c, err := fileConn(ours)
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return err
	}
	h.mu.Lock()
	h.corePid = cmd.Process.Pid
	h.mu.Unlock()
	served := make(chan error, 1)
	go func() { served <- h.Serve(c) }()
	return h.superviseCore(cmd.Process.Pid, c, served)
}

// superviseCore waits for the core pid, served on c, to end. The core is
// not one of the programs: it is reaped as it ends, and its socket closed
// then, though a child of its own may hold the other end.
func (h *Holder) superviseCore(pid int, c *conn, served chan error) error {
	exited := make(chan error, 1)
	go func() {
		var ws syscall.WaitStatus
		for {
			_, err := syscall.Wait4(pid, &ws, 0, nil)
			if err == syscall.EINTR {
				continue
			}
			if err != nil {
				exited <- err
				return
			}
			break
		}
		if e := exitOf(ws); e != nil {
			exited <- &spawn.ExitError{Code: e.Code, Signal: syscall.Signal(e.Signal), CoreDumped: e.CoreDumped}
			return
		}
		exited <- nil
	}()
	var err error
	select {
	case err = <-exited:
		c.Close()
		<-served
	case serr := <-served:
		if serr == ErrShutdown {
			// The core asked for it and is ending; give it a moment.
			select {
			case <-exited:
			case <-time.After(5 * time.Second):
				syscall.Kill(pid, syscall.SIGKILL)
				<-exited
			}
			return nil
		}
		err = <-exited
	}
	return err
}

// FromEnv is the client of the holder that started this core, from the
// descriptor EnvFD names. The descriptor is closed on exec, and EnvFD
// unset, so nothing this process starts has it.
func FromEnv() (*Client, error) {
	v := os.Getenv(EnvFD)
	if v == "" {
		return nil, fmt.Errorf("holder: %s is not set", EnvFD)
	}
	fd, err := strconv.Atoi(v)
	if err != nil || fd < 3 {
		return nil, fmt.Errorf("holder: %s=%q", EnvFD, v)
	}
	os.Unsetenv(EnvFD)
	syscall.CloseOnExec(fd)
	return Dial(os.NewFile(uintptr(fd), "holder socket"))
}

// Main is `nerdr holder -- CORE...`, which nothing uses yet: a holder
// that starts CORE as nerdr's core. It is left out of the help. An
// upgraded holder is started as `nerdr holder --resume FD`.
func Main(args []string) int {
	var err error
	switch {
	case len(args) == 2 && args[0] == resumeArg:
		fd, aerr := strconv.Atoi(args[1])
		if aerr != nil {
			err = fmt.Errorf("%s %q", resumeArg, args[1])
		} else {
			err = Resume(fd)
		}
	default:
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		err = Run(args)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "nerdr holder: %v\n", err)
		return 1
	}
	return 0
}
