package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/core"
)

// `nerdr connect HOST:FOLDER` is the TUI here, as a client of the core of
// FOLDER on HOST, reached over ssh: ssh runs `nerdr bridge FOLDER` there
// (see runBridge), with the client socket's stream as its stdin and
// stdout. nerdr listens on no network: ssh is all that reaches another
// machine, and whoever can log in there over ssh as you can do all you
// can there, with or without nerdr.

// EnvSSH, when set, is the command run in place of ssh: for the tests,
// which have no ssh server.
const EnvSSH = "NERDR_SSH"

// parseRemote is the host and the folder of HOST:FOLDER, "~" when no
// folder is given. Only a host ssh takes as one, and a folder that is a
// plain word to a shell, are taken.
func parseRemote(arg string) (host, folder string, err error) {
	host, folder, _ = strings.Cut(arg, ":")
	if folder == "" {
		folder = "~"
	}
	if host == "" || strings.HasPrefix(host, "-") || strings.ContainsFunc(host, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-@", r))
	}) {
		return "", "", fmt.Errorf("not a host: %q", host)
	}
	if strings.ContainsAny(folder, "'\"$`\\\n\x00") || strings.HasPrefix(folder, "-") {
		return "", "", fmt.Errorf("a folder nerdr cannot name over ssh: %q", folder)
	}
	return host, folder, nil
}

// sshArgv is the command line that runs nerdr bridge for folder on host.
// The ssh agent and X are not forwarded: what a core starts there must
// not reach them. Again, after a first connection, it never asks for a
// password, the TUI having the terminal.
func sshArgv(host, folder string, again bool) []string {
	argv := strings.Fields(os.Getenv(EnvSSH))
	if len(argv) == 0 {
		argv = []string{"ssh"}
	}
	argv = append(argv, "-T",
		"-o", "ForwardAgent=no", "-o", "ForwardX11=no",
		"-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3")
	if again {
		argv = append(argv, "-o", "BatchMode=yes", "-o", "ConnectTimeout=10")
	}
	// The user's login shell finds nerdr where it is installed, as in a
	// terminal there; ssh's own PATH may not have it.
	remote := `exec "$SHELL" -lc "exec nerdr bridge '` + folder + `'"`
	return append(argv, "--", host, remote)
}

// tail keeps the last of what is written to it.
type tail struct {
	mu sync.Mutex
	b  []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > 4096 {
		t.b = t.b[len(t.b)-4096:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.b))
}

// dialRemote connects to the core of folder on host over ssh. again is a
// connection after the first, which never asks for a password.
func dialRemote(host, folder string, again bool) (*attach.Conn, error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, err
	}
	// Both ends: ssh gets its own as stdin and stdout alone, never as a
	// file more it would pass on, as to a core it starts, which lives on
	// and would keep the connection from ever closing.
	syscall.CloseOnExec(fds[0])
	syscall.CloseOnExec(fds[1])
	mine, theirs := os.NewFile(uintptr(fds[0]), "nerdr-ssh"), os.NewFile(uintptr(fds[1]), "ssh")
	argv := sshArgv(host, folder, again)
	cmd := exec.Command(argv[0], argv[1:]...)
	stderr := &tail{}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = theirs, theirs, stderr
	if err := cmd.Start(); err != nil {
		mine.Close()
		theirs.Close()
		return nil, err
	}
	theirs.Close()
	c, err := net.FileConn(mine)
	mine.Close()
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}
	fail := func(err error) (*attach.Conn, error) {
		_ = c.Close()
		// What ssh says as it ends is the reason, when it ends.
		t := time.AfterFunc(3*time.Second, func() { _ = cmd.Process.Kill() })
		_ = cmd.Wait()
		t.Stop()
		return nil, err
	}
	gen, v, err := attach.ReadPreamble(c)
	if err != nil {
		_, _ = fail(nil)
		return nil, remoteFailed(host, stderr.String(), err)
	}
	if gen != attach.Generation {
		return fail(versionMismatch(host, gen, v))
	}
	conn, _, err := attach.Handshake(c, attach.Hello{Gen: attach.Generation, Version: version()})
	if err != nil {
		return fail(fmt.Errorf("%s: %w", host, err))
	}
	go func() {
		<-conn.Done()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	return conn, nil
}

// versionMismatch says which machine has the older nerdr, the bridge on
// host being of generation gen and version v.
func versionMismatch(host string, gen int, v string) error {
	if gen > attach.Generation {
		return fmt.Errorf("nerdr on %s is %s (protocol %d), and this is nerdr %s (protocol %d): update nerdr here",
			host, v, gen, version(), attach.Generation)
	}
	return fmt.Errorf("nerdr on %s is %s (protocol %d), and this is nerdr %s (protocol %d): update nerdr on %s",
		host, v, gen, version(), attach.Generation, host)
}

// remoteFailed says why no bridge answered on host, from what ssh said.
func remoteFailed(host, said string, err error) error {
	last := said
	if i := strings.LastIndex(said, "\n"); i >= 0 {
		last = said[i+1:]
	}
	switch {
	case strings.Contains(said, `no command "bridge"`):
		return fmt.Errorf("nerdr on %s is too old for nerdr connect: update nerdr on %s", host, host)
	case strings.Contains(said, "nerdr: not found") || strings.Contains(said, "nerdr: command not found") ||
		strings.Contains(said, "Unknown command: nerdr"):
		return fmt.Errorf("no nerdr on %s, or not on the PATH of a login shell there: install nerdr on %s", host, host)
	case last != "":
		return fmt.Errorf("%s: %s", host, last)
	}
	return fmt.Errorf("%s: %w", host, err)
}

// errRemoteInATab is why a nerdr a program in a tab started does not
// connect to another machine. It tidies, as InATab does; it guards nothing.
var errRemoteInATab = errors.New("nerdr started by a program in a tab does not connect to another machine: run nerdr connect from your own terminal")

// remoteAllowed is errRemoteInATab when this nerdr was started by a
// program in a tab, other than a terminal of the user's; see
// core.InATab.
func remoteAllowed() error {
	if core.InATab() {
		return errRemoteInATab
	}
	return nil
}
