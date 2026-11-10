package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// `nerdr bridge FOLDER` is what a client on another machine runs here over
// ssh, its stream as stdin and stdout, to reach the core of FOLDER; one is
// started when none runs. See attach.Bridge.

// envMark comes before the environment `nerdr bridge --print-env` prints.
const envMark = "\x00NERDR-ENV\x00"

// loginEnvWait is how long the user's login shell may take to say its
// environment.
const loginEnvWait = 5 * time.Second

// notForPrograms are what of the environment of an ssh session the
// programs a core starts are not given: they name the session, gone once
// it is, and a forwarded ssh agent or display of the client's machine.
var notForPrograms = []string{"SSH_AUTH_SOCK", "SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY", "SSH_ORIGINAL_COMMAND", "DISPLAY"}

func runBridge(args []string) int {
	if len(args) == 1 && args[0] == "--print-env" {
		// For the login shell loginEnv starts.
		_, _ = os.Stdout.WriteString(envMark + strings.Join(os.Environ(), "\x00"))
		return 0
	}
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(os.Stderr, "usage: nerdr bridge FOLDER  (run by nerdr connect over ssh)")
		return 2
	}
	// Nothing the ssh session left open goes to a core started here,
	// which lives on: a connection it held would never close.
	for fd := 3; fd < 1024; fd++ {
		syscall.CloseOnExec(fd)
	}
	if _, err := os.Stdout.Write(attach.Preamble(version())); err != nil {
		return 1
	}
	refuse := func(err error) int {
		_ = attach.Refuse(os.Stdout, "on "+hostname()+": "+err.Error())
		return 1
	}
	root, err := bridgeRoot(args[0])
	if err != nil {
		return refuse(err)
	}
	env, why := loginEnv()
	if why != "" {
		fmt.Fprintln(os.Stderr, "nerdr bridge:", why)
	}
	core, keyFile, err := bridgeCore(root, env)
	if err != nil {
		return refuse(err)
	}
	defer core.Close()
	if err := attach.Bridge(os.Stdin, os.Stdout, core, keyFile, env); err != nil && !errors.Is(err, io.EOF) {
		return 1
	}
	return 0
}

// hostname is this machine's name, for what a client elsewhere is told.
func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "the other machine"
	}
	return h
}

// bridgeRoot is the folder a client named, as nerdr started in it would
// have it: "~" the home folder here.
func bridgeRoot(folder string) (string, error) {
	dir, err := filepath.Abs(config.ExpandHome(folder))
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", fmt.Errorf("no folder %s", folder)
	}
	return dir, nil
}

// bridgeCore is a connection to the client socket of the core of root,
// and its key file: one is started, with env, when none runs, as nerdr
// started in root would.
func bridgeCore(root string, env []string) (net.Conn, string, error) {
	sock, keyFile, err := attach.Paths(root)
	if err != nil {
		return nil, "", err
	}
	c, err := net.Dial("unix", sock)
	if err == nil {
		return c, keyFile, nil
	}
	if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ECONNREFUSED) {
		return nil, "", err
	}
	release, err := state.Lock(root)
	if errors.Is(err, state.ErrLocked) {
		return nil, "", fmt.Errorf("a nerdr with no core of its own has the session of %s: quit it there first", root)
	}
	if err != nil {
		return nil, "", err
	}
	release()
	scope, err := logindScope(logindProbe())
	if err != nil {
		return nil, "", err
	}
	if err := startCoreIn(root, env, scope); err != nil {
		return nil, "", err
	}
	for deadline := time.Now().Add(coreStartWait); ; {
		if c, err = net.Dial("unix", sock); err == nil {
			// Listening comes before the key is written: wait for it.
			for time.Now().Before(deadline) {
				if _, kerr := os.Stat(keyFile); kerr == nil {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			return c, keyFile, nil
		}
		if time.Now().After(deadline) {
			return nil, "", fmt.Errorf("the core started for %s did not answer: see %s", root, coreLog(root))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// loginEnv is the environment of the user's login shell here, as a
// terminal of theirs would have it: an ssh session's is the bare one sshd
// gives, with none of what the shell's files add to PATH, where the agents
// are. Without it, this process's, and why. What only the session's is
// left out (notForPrograms).
func loginEnv() ([]string, string) {
	exe, err := os.Executable()
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	env, why := os.Environ(), ""
	switch {
	case err != nil:
		why = err.Error()
	case strings.ContainsAny(exe, "'\n"):
		why = "nerdr's path cannot be given to a shell: " + exe
	default:
		env, err = loginEnvOf(shell, "exec '"+exe+"' bridge --print-env")
		if err != nil {
			env, why = os.Environ(), "the login shell's environment: "+err.Error()+"; the programs get this session's"
		}
	}
	return slices.DeleteFunc(env, func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return slices.Contains(notForPrograms, name)
	}), why
}

// loginEnvOf runs line in shell as a login shell with nothing to read, and
// gives the environment it prints after envMark.
func loginEnvOf(shell, line string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), loginEnvWait)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-ilc", line)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, io.Discard
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	_, dump, ok := bytes.Cut(out.Bytes(), []byte(envMark))
	if !ok {
		return nil, errors.New("it said no environment")
	}
	var env []string
	for _, kv := range strings.Split(string(dump), "\x00") {
		if strings.Contains(kv, "=") {
			env = append(env, kv)
		}
	}
	return env, nil
}

// logind is what systemd-logind does here with a user's programs as they
// log out: Kills, KillUserProcesses=yes, ends all of them, but those of a
// user who Lingers in a scope of their own. Known is unset where there is
// no logind to ask, as on macOS.
type logind struct {
	Known, Kills, Lingers bool
}

// logindProbe asks logind, through loginctl and busctl, when they are
// here.
var logindProbe = func() logind {
	out, err := exec.Command("loginctl", "show-user", os.Getenv("USER"), "--property=Linger").Output()
	if err != nil {
		return logind{}
	}
	l := logind{Known: true, Lingers: strings.TrimSpace(string(out)) == "Linger=yes"}
	out, err = exec.Command("busctl", "get-property", "org.freedesktop.login1", "/org/freedesktop/login1",
		"org.freedesktop.login1.Manager", "KillUserProcesses").Output()
	l.Kills = err == nil && strings.TrimSpace(string(out)) == "b true"
	return l
}

// logindScope says whether a core started here goes in a scope of its
// own, out of the ssh session's, so that logind leaves it be when the
// session ends; and why none can be started, when logind would end it
// whatever nerdr did.
func logindScope(l logind) (bool, error) {
	switch {
	case !l.Known || !l.Kills:
		return false, nil
	case l.Lingers:
		return true, nil
	}
	return false, fmt.Errorf("logind here ends all of your programs when you log out (KillUserProcesses=yes), "+
		"and nerdr's agents with them: run loginctl enable-linger %s on %s, and connect again", os.Getenv("USER"), hostname())
}
