// Package proc keeps the programs nerdr runs for itself — git, gh, the
// agents' CLIs asked for their conversations, notifications — off its
// terminal.
//
// A program started as it is shares nerdr's process group, the terminal's
// foreground one, and a terminal that names its tab after the program in
// the foreground (Terminal.app and iTerm on a Mac) names it git, gh, zsh
// for as long as each runs: the tab's title flickers. In a session of its
// own, a program is not in the foreground, and has no terminal to read a
// password from either, which nerdr never wants it to.
package proc

import (
	"bytes"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nerdr-dev/nerdr/internal/denied"
)

// Detach puts cmd in a session of its own when it starts, and returns it,
// counting it (see Counts). A new session is a new process group too:
// asking for one besides would fail the start, as a session's leader
// cannot change its group. cmd's Dir, or git's -C, is set by then.
//
// A cmd made with exec.CommandContext has its whole session killed when
// the context ends, not only cmd itself, and is waited for no more than
// waitDelay after that: what it started (ssh under git, an install under a
// setup command) would otherwise hold its output open, and keep Wait
// waiting long past the time limit.
func Detach(cmd *exec.Cmd) *exec.Cmd {
	count(cmd)
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
	cmd.SysProcAttr.Setpgid = false
	if cmd.Cancel != nil {
		cmd.Cancel = func() error {
			// The session's leader's pid is its process group's id.
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		if cmd.WaitDelay == 0 {
			cmd.WaitDelay = waitDelay
		}
	}
	return cmd
}

// waitDelay is how long a cmd whose context has ended is waited for once
// it has been killed.
const waitDelay = 2 * time.Second

// Call is a program nerdr ran, by name, the folder it ran in, and what it
// was asked to do: git's subcommand ("status"), gh's command ("pr view").
type Call struct{ Program, Dir, Sub string }

var (
	mu      sync.Mutex
	calls   = map[Call]int{}
	started = time.Now()
)

func count(cmd *exec.Cmd) {
	if len(cmd.Args) == 0 {
		return
	}
	c := callOf(cmd)
	mu.Lock()
	calls[c]++
	mu.Unlock()
}

func callOf(cmd *exec.Cmd) Call {
	c := Call{Program: filepath.Base(cmd.Args[0]), Dir: cmd.Dir}
	if len(cmd.Args) > 2 && cmd.Args[1] == "-C" {
		c.Dir = cmd.Args[2]
	}
	c.Sub = sub(c.Program, cmd.Args[1:])
	return c
}

// Output is cmd.Output, Run cmd.Run and CombinedOutput
// cmd.CombinedOutput, which besides keep in package denied a refusal that
// cmd's errors say: what nerdr runs behind its screen fails quietly.
func Output(cmd *exec.Cmd) ([]byte, error) {
	out, err := cmd.Output()
	note(cmd, err, nil)
	return out, err
}

// Run is cmd.Run, with what cmd writes to stderr kept to look at when
// nothing else takes it (see Output).
func Run(cmd *exec.Cmd) error {
	var tail *tailWriter
	if cmd.Stderr == nil {
		tail = &tailWriter{}
		cmd.Stderr = tail
		// stderr is a pipe now, which what cmd started could hold open.
		if cmd.WaitDelay == 0 {
			cmd.WaitDelay = waitDelay
		}
	}
	err := cmd.Run()
	if tail != nil {
		note(cmd, err, tail.b)
	} else {
		note(cmd, err, nil)
	}
	return err
}

// CombinedOutput is cmd.CombinedOutput (see Output).
func CombinedOutput(cmd *exec.Cmd) ([]byte, error) {
	out, err := cmd.CombinedOutput()
	note(cmd, err, out)
	return out, err
}

// note keeps the refusal that cmd's failing with err says, if any: in
// stderr, or, without, the ExitError's, or a bytes.Buffer cmd wrote its
// stderr to; or err itself, when cmd could not start for lack of
// permission.
func note(cmd *exec.Cmd, err error, stderr []byte) {
	if err == nil || len(cmd.Args) == 0 {
		return
	}
	var ee *exec.ExitError
	exited := errors.As(err, &ee)
	if b, ok := cmd.Stderr.(*bytes.Buffer); ok && stderr == nil {
		stderr = b.Bytes()
	} else if exited && stderr == nil {
		stderr = ee.Stderr
	}
	c := callOf(cmd)
	what := strings.TrimSpace(c.Program + " " + c.Sub)
	if c.Sub == "" && len(cmd.Args) > 1 && !strings.HasPrefix(cmd.Args[1], "-") {
		what += " " + cmd.Args[1] // "opencode session"
	}
	if c.Dir != "" {
		what += " in " + c.Dir
	}
	if line, ok := denied.Line(string(stderr)); ok {
		denied.Note(what, line)
	} else if !exited && denied.Is(err) {
		denied.Note(what, err.Error())
	}
}

// tailWriter keeps the last tailSize bytes written to it.
type tailWriter struct{ b []byte }

const tailSize = 8 << 10

func (t *tailWriter) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > tailSize {
		t.b = t.b[len(t.b)-tailSize:]
	}
	return len(p), nil
}

// Counts is how many times each program has been run in each folder since
// nerdr started, and when that was.
func Counts() (map[Call]int, time.Time) {
	mu.Lock()
	defer mu.Unlock()
	out := make(map[Call]int, len(calls))
	for c, n := range calls {
		out[c] = n
	}
	return out, started
}

// sub is what program was asked to do, from its arguments: git's
// subcommand, past the options before it, and gh's command and the
// subcommand after it ("pr view", but "api" alone, not its path).
func sub(program string, args []string) string {
	var words []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case program == "git" && (a == "-C" || a == "-c"):
			i++ // and its value
		case strings.HasPrefix(a, "-"):
			if program != "git" && len(words) > 0 {
				return strings.Join(words, " ")
			}
		default:
			words = append(words, a)
		}
		if len(words) > 0 && (program != "gh" || len(words) == 2) {
			break
		}
	}
	if program == "gh" && len(words) == 2 && strings.ContainsAny(words[1], "/{") {
		words = words[:1]
	}
	if program != "git" && program != "gh" {
		return ""
	}
	return strings.Join(words, " ")
}
