// Package holder keeps the programs nerdr runs for its tabs, and their
// terminals, in a process of its own, so they live on while nerdr's core
// is restarted, updated or crashes. It has no logic: it starts what it is
// told to, keeps what each program prints in a ring, writes to them and
// tells when they end. It never reads what they print and never answers
// a terminal's queries; that is the core's.
//
// The holder starts the core and is reached by it alone, over a socket
// with no path the two share (a socketpair). The core is never a parent
// of a program the holder runs: a sandboxed agent cannot then reach the
// socket through the core's /proc entry or pidfd_getfd.
//
// The protocol is in this file. Each frame is the length of its header
// and of its body, each a big-endian uint32, then the header, a Msg as
// JSON, then the body, raw bytes. Descriptors a spawn gives the program
// go with its frame, as SCM_RIGHTS. It is versioned apart from the rest
// of nerdr (Proto) and is to change seldom: a core has to speak to every
// holder still alive.
package holder

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
)

// Proto is the version of the protocol this holder and client speak.
const Proto = 1

// Ops. A request carries a Seq the reply has too; exit is sent by the
// holder unasked, with no Seq.
const (
	OpHello      = "hello"      // both ways, first: Proto and Pid
	OpSpawn      = "spawn"      // Spawn; body: its standard input
	OpWrite      = "write"      // ID; body: the bytes
	OpResize     = "resize"     // ID, W, H
	OpSignal     = "signal"     // ID, PG, Sig
	OpRead       = "read"       // ID, Pos, Max; reply: Pos, Lost, EOF, body
	OpAck        = "ack"        // ID, and Pos, Exit or Done
	OpCheckpoint = "checkpoint" // ID, Pos; body: the block, kept unread
	OpList       = "list"       // ID or all; reply: Progs
	OpShutdown   = "shutdown"   // stop every program and end
	OpUpgrade    = "upgrade"    // exec the holder again, its programs kept
	OpExit       = "exit"       // holder to core: ID ended, Exit
)

// MaxBody is the most a frame's body can hold; a read asks for no more.
const MaxBody = 1 << 20

// maxHeader keeps a frame's header from asking for more than a list of
// many programs needs.
const maxHeader = 4 << 20

// maxFiles is the most descriptors a spawn can give a program.
const maxFiles = 16

// Msg is a frame's header. Fields not used by an op are left out.
type Msg struct {
	Op  string `json:"op"`
	Seq uint64 `json:"seq,omitempty"`
	ID  string `json:"id,omitempty"`
	// Err is why a request failed; Errno is its errno, when it had one,
	// so a client can give it back as the syscall.Errno it was.
	Err   string `json:"err,omitempty"`
	Errno int    `json:"errno,omitempty"`

	Proto int `json:"proto,omitempty"`
	Pid   int `json:"pid,omitempty"`

	Spawn *Spawn `json:"spawn,omitempty"`
	// Files is how many descriptors came with the frame.
	Files int `json:"files,omitempty"`

	Pos  int64 `json:"pos,omitempty"`
	Max  int   `json:"max,omitempty"`
	Lost int64 `json:"lost,omitempty"`
	EOF  bool  `json:"eof,omitempty"`

	W   int `json:"w,omitempty"`
	H   int `json:"h,omitempty"`
	PG  int `json:"pg,omitempty"`
	Sig int `json:"sig,omitempty"`

	// Ack: Exit, any, says the core has the program's exit, so the
	// holder can reap it; Done that it wants nothing more of it, so the holder lets
	// go of its terminal and, once it is reaped, forgets it.
	Exit *Exit `json:"exit,omitempty"`
	Done bool  `json:"done,omitempty"`

	Progs []Prog `json:"progs,omitempty"`
}

// Spawn is a program to start, as the core asks for it.
type Spawn struct {
	Argv []string `json:"argv"`
	Dir  string   `json:"dir,omitempty"`
	Env  []string `json:"env,omitempty"`
	PTY  bool     `json:"pty,omitempty"`
	W    int      `json:"w,omitempty"`
	H    int      `json:"h,omitempty"`
	// Sandboxed, Secret and Label are kept as they are given, for a core
	// started later to read in list; nothing can change them.
	Sandboxed bool   `json:"sandboxed,omitempty"`
	Secret    string `json:"secret,omitempty"`
	Label     string `json:"label,omitempty"`
	// Ring is how many bytes of output to keep; 0 is DefaultRing.
	Ring int `json:"ring,omitempty"`
}

// Exit is how a program ended: as spawn.ExitError has it, or all zero
// for exit 0.
type Exit struct {
	Code       int  `json:"code,omitempty"`
	Signal     int  `json:"signal,omitempty"`
	CoreDumped bool `json:"core,omitempty"`
}

// Prog is a program the holder has, as list tells it.
type Prog struct {
	ID        string `json:"id"`
	Pid       int    `json:"pid"`
	Started   int64  `json:"started"` // Unix nanoseconds
	Sandboxed bool   `json:"sandboxed,omitempty"`
	Secret    string `json:"secret,omitempty"`
	Label     string `json:"label,omitempty"`
	PTY       bool   `json:"pty,omitempty"`
	// State is "running", "exited" (not yet acked, so not reaped) or
	// "reaped".
	State string `json:"state"`
	Exit  *Exit  `json:"exit,omitempty"`
	// Pos is how far its output goes, Start where what is kept begins,
	// Acked how far the core said it had it.
	Pos   int64 `json:"pos"`
	Start int64 `json:"start"`
	Acked int64 `json:"acked,omitempty"`
	// Checkpoint is the position of the last checkpoint, -1 for none;
	// the block is not sent in a list.
	Checkpoint int64 `json:"checkpoint"`
	// Foreground is the foreground process group of its terminal.
	Foreground int `json:"fg,omitempty"`
}

// States of a program.
const (
	Running = "running"
	Exited  = "exited"
	Reaped  = "reaped"
)

// conn is one end of the socket, frames both ways. Sends are whole frames
// one at a time; receives come from one goroutine.
type conn struct {
	c  *net.UnixConn
	mu sync.Mutex
	// fds came with a frame read in part, not yet handed out.
	oob []byte
}

func newConn(c *net.UnixConn) *conn { return &conn{c: c} }

// fileConn makes a conn of a socket's descriptor, f, which it takes.
func fileConn(f *os.File) (*conn, error) {
	nc, err := net.FileConn(f)
	f.Close()
	if err != nil {
		return nil, err
	}
	uc, ok := nc.(*net.UnixConn)
	if !ok {
		nc.Close()
		return nil, fmt.Errorf("holder: descriptor is not a unix socket")
	}
	return newConn(uc), nil
}

// send sends m with body, and files as SCM_RIGHTS with its first byte.
func (c *conn) send(m *Msg, body []byte, files []*os.File) error {
	h, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(body) > MaxBody {
		return fmt.Errorf("holder: body of %d bytes", len(body))
	}
	frame := make([]byte, 8, 8+len(h)+len(body))
	binary.BigEndian.PutUint32(frame[0:], uint32(len(h)))
	binary.BigEndian.PutUint32(frame[4:], uint32(len(body)))
	frame = append(append(frame, h...), body...)
	var oob []byte
	if len(files) > 0 {
		fds := make([]int, len(files))
		for i, f := range files {
			rc, err := f.SyscallConn()
			if err != nil {
				return err
			}
			// The descriptor is only read here, while f is open.
			if err := rc.Control(func(fd uintptr) { fds[i] = int(fd) }); err != nil {
				return err
			}
		}
		oob = syscall.UnixRights(fds...)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	n, oobn, err := c.c.WriteMsgUnix(frame, oob, nil)
	if err != nil {
		return err
	}
	if oobn != len(oob) {
		return fmt.Errorf("holder: descriptors not sent")
	}
	if n < len(frame) {
		_, err = c.c.Write(frame[n:])
	}
	return err
}

// recv reads a frame: its header, body and the descriptors that came with
// it, which are the caller's.
func (c *conn) recv() (*Msg, []byte, []*os.File, error) {
	var lens [8]byte
	fds, err := c.readFull(lens[:])
	if err != nil {
		return nil, nil, nil, err
	}
	hl, bl := binary.BigEndian.Uint32(lens[0:]), binary.BigEndian.Uint32(lens[4:])
	if hl > maxHeader || bl > MaxBody {
		closeAll(fds)
		return nil, nil, nil, fmt.Errorf("holder: frame of %d+%d bytes", hl, bl)
	}
	buf := make([]byte, int(hl)+int(bl))
	more, err := c.readFull(buf)
	fds = append(fds, more...)
	if err != nil {
		closeAll(fds)
		return nil, nil, nil, err
	}
	m := new(Msg)
	if err := json.Unmarshal(buf[:hl], m); err != nil {
		closeAll(fds)
		return nil, nil, nil, fmt.Errorf("holder: header: %w", err)
	}
	return m, buf[hl:], fds, nil
}

// readFull fills b, and gives the descriptors that came on the way.
func (c *conn) readFull(b []byte) ([]*os.File, error) {
	var files []*os.File
	oob := make([]byte, syscall.CmsgSpace(4*maxFiles))
	for len(b) > 0 {
		n, oobn, _, _, err := c.c.ReadMsgUnix(b, oob)
		n, oobn = max(n, 0), max(oobn, 0)
		if oobn > 0 {
			got, perr := parseRights(oob[:oobn])
			files = append(files, got...)
			if perr != nil && err == nil {
				err = perr
			}
		}
		b = b[n:]
		if err != nil {
			if err == io.EOF && n > 0 && len(b) == 0 {
				break
			}
			closeAll(files)
			return nil, err
		}
		if n == 0 {
			closeAll(files)
			return nil, io.ErrUnexpectedEOF
		}
	}
	return files, nil
}

func parseRights(oob []byte) ([]*os.File, error) {
	msgs, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, err
	}
	var files []*os.File
	for i := range msgs {
		fds, err := syscall.ParseUnixRights(&msgs[i])
		if err != nil {
			continue
		}
		for _, fd := range fds {
			syscall.CloseOnExec(fd)
			files = append(files, os.NewFile(uintptr(fd), "holder file"))
		}
	}
	return files, nil
}

func (c *conn) Close() error { return c.c.Close() }

func closeAll(fs []*os.File) {
	for _, f := range fs {
		f.Close()
	}
}

// errMsg is err as a reply has it.
func errMsg(m *Msg, err error) {
	m.Err = err.Error()
	var errno syscall.Errno
	if errors.As(err, &errno) {
		m.Errno = int(errno)
	}
}

// msgErr is a reply's error as the client gives it back.
func msgErr(m *Msg) error {
	if m.Err == "" {
		return nil
	}
	if m.Errno != 0 {
		errno := syscall.Errno(m.Errno)
		if errno.Error() == m.Err {
			return errno
		}
		return fmt.Errorf("%s: %w", m.Err, errno)
	}
	return errors.New(m.Err)
}

// socketpair is the two ends of a new socket with no path. Both are
// closed on exec; the end given to the core is passed to it by name.
func socketpair() (a, b *os.File, err error) {
	syscall.ForkLock.RLock()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err == nil {
		syscall.CloseOnExec(fds[0])
		syscall.CloseOnExec(fds[1])
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, nil, os.NewSyscallError("socketpair", err)
	}
	return os.NewFile(uintptr(fds[0]), "holder socket"), os.NewFile(uintptr(fds[1]), "core socket"), nil
}

// connPair is two conns joined, for a holder and a client in one process.
func connPair() (*conn, *conn, error) {
	a, b, err := socketpair()
	if err != nil {
		return nil, nil, err
	}
	ca, err := fileConn(a)
	if err != nil {
		b.Close()
		return nil, nil, err
	}
	cb, err := fileConn(b)
	if err != nil {
		ca.Close()
		return nil, nil, err
	}
	return ca, cb, nil
}
