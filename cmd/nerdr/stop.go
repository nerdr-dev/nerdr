package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"syscall"
	"time"

	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/hooks"
)

const stopUsage = `usage: nerdr stop [--timeout 1m] [FOLDER]

Stops the nerdr running for FOLDER (this folder when not given) and its
agents, as quitting in its window does, without opening it: over ssh, say.
Every agent, terminal and plugin stops; the tabs and the agents'
conversations are kept for the next time nerdr starts there. A window open
on it closes. It does not ask first.

Waits until nerdr has stopped and let go of the folder, so nerdr can start
there again at once; --timeout (1m) bounds all of it. nerdr list shows
what runs. Programs nerdr runs, its agents among them, are refused.
`

// stopPoll is how often nerdr stop looks whether the core has gone.
const stopPoll = 100 * time.Millisecond

// runStop is `nerdr stop`.
func runStop(args []string) int {
	fl := flag.NewFlagSet("stop", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	timeout := fl.Duration("timeout", time.Minute, "")
	if parseAnywhere(fl, args) != nil || fl.NArg() > 1 || *timeout <= 0 {
		fmt.Fprint(os.Stderr, stopUsage)
		return 2
	}
	rest := fl.Args()
	folder := "."
	if len(rest) == 1 {
		folder = rest[0]
	}
	root, err := bridgeRoot(folder)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr stop:", err)
		return 1
	}
	sock, keyFile, err := attach.APIPaths(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr stop:", err)
		return 1
	}
	if err := stopCore(sock, keyFile, *timeout); err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
			err = fmt.Errorf("no nerdr runs for %s", root)
		}
		fmt.Fprintln(os.Stderr, "nerdr stop:", err)
		return 1
	}
	fmt.Println("nerdr stopped for", root)
	return 0
}

// peerOf is hooks.PeerOf: which process listens at the other end. A
// test has it name a process of its own.
var peerOf = hooks.PeerOf

// stopCore asks the core at API socket sock to quit, and waits until it
// has: until nothing listens at sock and the core's process has ended,
// which lets go of its session, so a nerdr can start in its folder at
// once. Where nerdr cannot tell the process, the socket alone. All of it
// within timeout.
func stopCore(sock, keyFile string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	c, err := dialAPIBy(sock, keyFile, deadline)
	if err != nil {
		return err
	}
	ended := processEnded(peerOf(c))
	in := bufio.NewReaderSize(c, 64<<10)
	if _, err := readWelcome(in); err != nil {
		_ = c.Close()
		return stopTimedOut(err, timeout)
	}
	if _, err := c.Write([]byte(`{"op":"quit"}` + "\n")); err != nil {
		_ = c.Close()
		return stopTimedOut(err, timeout)
	}
	var answer struct {
		Error string `json:"error"`
	}
	// The connection closing first is an answer too: the core went. So
	// is its saying it quits already.
	err = readLine(in, &answer)
	_ = c.Close()
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return stopTimedOut(err, timeout)
	}
	if err == nil && answer.Error != "" && answer.Error != core.ErrAPIQuitting.Error() {
		return errors.New(answer.Error)
	}
	for {
		gone, err := coreGone(sock, deadline, ended)
		if err != nil {
			return err
		}
		if gone {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("nerdr is still stopping its agents after %s; nerdr list shows whether it runs", timeout)
		}
		time.Sleep(min(stopPoll, time.Until(deadline)))
	}
}

// coreGone is whether the core has ended: no API socket to connect to,
// and its process gone (ended).
func coreGone(sock string, deadline time.Time, ended func() bool) (bool, error) {
	d := net.Dialer{Deadline: deadline}
	probe, err := d.Dial("unix", sock)
	switch {
	case err == nil:
		_ = probe.Close()
		return false, nil
	case errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED):
	default:
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return false, nil
		}
		return false, fmt.Errorf("cannot tell whether nerdr has stopped: %w", err)
	}
	return ended(), nil
}

// processEnded tells whether the process p is has ended: none of that id,
// or one started at another time, which took the id after it. It only
// looks, as the session lock would not let it: a nerdr starting meanwhile
// could find the lock taken. A process nerdr cannot place counts as ended.
// One ended that its parent has not yet waited for still counts as there,
// for the moment that lasts: the holder waits for its core.
func processEnded(p hooks.Peer) func() bool {
	if !p.Known || len(p.Chain) == 0 || p.Chain[0] <= 0 {
		return func() bool { return true }
	}
	pid := p.Chain[0]
	start := p.StartOf(pid)
	return func() bool {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		return start != 0 && hooks.Started(pid) != start
	}
}

// stopTimedOut is err, said as the timeout it is when it is one.
func stopTimedOut(err error, timeout time.Duration) error {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return fmt.Errorf("nerdr did not answer within %s", timeout)
	}
	return err
}
