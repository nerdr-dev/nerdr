package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"

	"github.com/nerdr-dev/nerdr/internal/app"
	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/browser"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/editor"
	"github.com/nerdr-dev/nerdr/internal/input"
	"github.com/nerdr-dev/nerdr/internal/spawn"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// coreVersion is the version of the core last connected to.
var coreVersion string

// coreStartWait is how long a client waits for the core it started to
// take connections.
const coreStartWait = 15 * time.Second

// logMax is how large a core's log may grow before it is begun again, the
// last kept beside it.
const logMax = 1 << 20

// runClient is nerdr in the terminal as a client of the core of the
// folder: one in a process of its own, which keeps the agents at work when
// the client is closed. With start, a core is started when none runs.
// With remote, HOST:FOLDER, the core is that of FOLDER on HOST, reached
// over ssh (see dialRemote), and started there when none runs.
func runClient(start bool, remote string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	browser.Setting = cfg.Browser
	editor.File, editor.Worktree = cfg.Editor, cfg.WorktreeEditor
	prefix, err := cfg.PrefixByte()
	if err != nil {
		return err
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("needs a terminal")
	}
	var root, host string
	var redial func() (*attach.Conn, error)
	var conn *attach.Conn
	if remote != "" {
		if err := remoteAllowed(); err != nil {
			return err
		}
		var folder string
		if host, folder, err = parseRemote(remote); err != nil {
			return err
		}
		// Before the terminal is the TUI's: ssh may ask for a password.
		if conn, err = dialRemote(host, folder, false); err != nil {
			return err
		}
		root = folder
		redial = func() (*attach.Conn, error) { return dialRemote(host, folder, true) }
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		if root, _ = filepath.EvalSymlinks(cwd); root == "" {
			root = cwd
		}
		if conn, err = dialCore(root, start); err != nil {
			return err
		}
		if coreVersion != version() {
			fmt.Fprintf(os.Stderr, "nerdr: the core runs version %s, this is %s: quit and start nerdr again to use this one\n", coreVersion, version())
		}
		redial = func() (*attach.Conn, error) { return dialCore(root, false) }
	}

	old, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		conn.Close("")
		return err
	}
	defer func() { _ = term.Restore(int(os.Stdin.Fd()), old) }()

	router := input.New(os.Stdin, prefix)
	var p *tea.Program
	ready := make(chan struct{})
	model, err := app.NewRemote(cfg, root, router, conn, tellProgram(func() *tea.Program { <-ready; return p }))
	if err != nil {
		conn.Close("")
		return err
	}
	opts := []tea.ProgramOption{
		tea.WithAltScreen(),
		tea.WithInput(router.Reader()),
		tea.WithReportFocus(),
		tea.WithFPS(cfg.FPS),
	}
	if *cfg.Mouse {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	model.SetRedial(redial)
	model.SetCoreHost(host)
	p = tea.NewProgram(model, opts...)
	close(ready)
	router.SetSend(func(m any) { p.Send(m) })
	stop := make(chan struct{})
	defer close(stop)
	model.StartLocal(p.Send, stop)
	go router.Run()

	// The window closing closes the client; the agents work on.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGHUP, syscall.SIGTERM)
	go func() {
		<-sig
		p.Kill()
	}()

	_, _ = os.Stdout.WriteString(ansi.ResetModeAutoWrap)
	_, err = p.Run()
	_, _ = os.Stdout.WriteString(ansi.SetModeAutoWrap)
	model.Close()
	// Gone from the herdr pane it ran in, if any, however it ended: the
	// core it leaves working is not in that pane.
	model.Herdr.Release()
	if why := model.GoneErr(); why != "" {
		return errors.New(why)
	}
	if err != nil && !errors.Is(err, tea.ErrProgramKilled) {
		return err
	}
	return nil
}

// tellProgram gives the program what comes of the connection to core,
// never waiting for it: one InboxMsg or ScreensMsg waiting is enough for
// all that came.
func tellProgram(prog func() *tea.Program) func(any) {
	inbox, screens := make(chan struct{}, 1), make(chan struct{}, 1)
	go func() {
		p := prog()
		for {
			select {
			case <-inbox:
				p.Send(core.InboxMsg{})
			case <-screens:
				p.Send(core.ScreensMsg{})
			}
		}
	}()
	return func(msg any) {
		switch msg.(type) {
		case core.InboxMsg:
			select {
			case inbox <- struct{}{}:
			default:
			}
		case core.ScreensMsg:
			select {
			case screens <- struct{}{}:
			default:
			}
		default:
			go func() { prog().Send(msg) }()
		}
	}
}

// dialCore connects to the core of root; with start, it starts one when
// none runs, and waits for it.
func dialCore(root string, start bool) (*attach.Conn, error) {
	sock, keyFile, err := attach.Paths(root)
	if err != nil {
		return nil, err
	}
	hello := attach.Hello{Version: version(), Env: os.Environ()}
	conn, w, err := attach.Dial(sock, keyFile, hello)
	// A core quitting is waited for, to start one afresh.
	for deadline := time.Now().Add(coreStartWait); quitting(err) && start && time.Now().Before(deadline); {
		time.Sleep(100 * time.Millisecond)
		conn, w, err = attach.Dial(sock, keyFile, hello)
	}
	if errors.Is(err, attach.ErrNoCore) && start {
		if conn, w, err = waitForLock(root, sock, keyFile, hello); conn != nil || err != nil {
			if err != nil {
				return nil, err
			}
			coreVersion = w.Version
			return conn, nil
		}
		if err := startCore(root, os.Environ()); err != nil {
			return nil, err
		}
		deadline := time.Now().Add(coreStartWait)
		for {
			conn, w, err = attach.Dial(sock, keyFile, hello)
			if err == nil || !errors.Is(err, attach.ErrNoCore) && !errors.Is(err, os.ErrNotExist) || time.Now().After(deadline) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err != nil {
			return nil, fmt.Errorf("%w; see %s", err, coreLog(root))
		}
	}
	if err != nil {
		return nil, err
	}
	coreVersion = w.Version
	return conn, nil
}

// lockWait is how long a client waits for a core that holds the folder's
// session, to start listening, before it takes the holder of the session
// for a nerdr with no core.
const lockWait = 3 * time.Second

// waitForLock sees, before a core is started for root, that no other
// nerdr has the folder's session: a core still starting, which it waits
// for and is the connection to, or a nerdr that runs its programs itself,
// an older one or one with NERDR_HOLDER=0, which it is an error to say so.
// A core started then would only find the session taken and end. Neither,
// when the session is free.
func waitForLock(root, sock, keyFile string, hello attach.Hello) (*attach.Conn, attach.Welcome, error) {
	deadline := time.Now().Add(lockWait)
	for {
		release, err := state.Lock(root)
		if err == nil {
			release()
			return nil, attach.Welcome{}, nil
		}
		if !errors.Is(err, state.ErrLocked) {
			return nil, attach.Welcome{}, err
		}
		conn, w, err := attach.Dial(sock, keyFile, hello)
		if err == nil || !errors.Is(err, attach.ErrNoCore) && !errors.Is(err, os.ErrNotExist) {
			return conn, w, err
		}
		if time.Now().After(deadline) {
			return nil, attach.Welcome{}, fmt.Errorf("another nerdr has the session of %s, and no core of it answers here. "+
				"It may be one with no core, from before it or started with %s=0: quit it there first. "+
				"Or it is a core still starting, or one started with another XDG_RUNTIME_DIR: try again", root, core.EnvHolder)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// quitting reports whether err is a core refusing a client as it quits,
// or closing on it as it ends.
func quitting(err error) bool {
	var r *attach.RefusedError
	return errors.As(err, &r) && r.Reason == core.RefusedQuitting ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) || errors.Is(err, io.EOF)
}

// startCore starts the holder and the core of root, with env, in a session
// of their own with no terminal: they live on when the client and its
// terminal go, an ssh session too. What they write goes to the core's log.
func startCore(root string, env []string) error {
	bin, err := spawn.Bin()
	if err != nil {
		return err
	}
	logPath := coreLog(root)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return err
	}
	if st, err := os.Stat(logPath); err == nil && st.Size() > logMax {
		_ = os.Rename(logPath, logPath+".old")
	}
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	null, err := os.Open(os.DevNull)
	if err != nil {
		return err
	}
	defer null.Close()
	fmt.Fprintf(logf, "--- %s: nerdr %s starts its core in %s\n", time.Now().Format(time.RFC3339), version(), root)
	cmd := exec.Command(bin, "holder", "--", bin, "core")
	cmd.Dir = root
	cmd.Env = append(slices.Clone(env), core.EnvHolder+"=1")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = null, logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // reaped should it end while the client runs
	return nil
}

// coreLog is where the core of root and its holder write what they would
// to a terminal.
func coreLog(root string) string {
	sock, _, err := attach.Paths(root)
	name := "core.log"
	if err == nil {
		name = filepath.Base(sock[:len(sock)-len(".sock")]) + ".log"
	}
	return filepath.Join(filepath.Dir(state.Dir()), name)
}
