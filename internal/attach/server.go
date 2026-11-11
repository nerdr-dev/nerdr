package attach

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/nerdr-dev/nerdr/internal/hooks"
)

// Config is what a core's client socket goes by.
type Config struct {
	// Version and Features are the core's, for Welcome and Refused.
	Version  string
	Features []string
	// Check is why a connection from peer p is refused, "" when it is
	// not. It is asked before anything is read from the connection, and
	// must refuse a peer it cannot place. Required.
	Check func(p hooks.Peer) string
	// Client is given each client let in, on a goroutine of the server's:
	// the one before it has been told it was taken over.
	Client func(c *Conn, h Hello)
}

// ErrTaken is returned by Listen when another core has the socket.
var ErrTaken = errors.New("another nerdr core has the client socket")

// Server is a core's client socket.
type Server struct {
	cfg  Config
	ln   net.Listener
	lock *os.File
	sock string
	keyf string
	key  string

	closed sync.Once

	mu  sync.Mutex
	cur *Conn
	all map[net.Conn]bool
	wg  sync.WaitGroup
}

// Listen opens the client socket at sock, with a new key in keyFile. The
// socket is claimed with a lock beside it, held until Close: a second
// core gets ErrTaken. One left by a core that died is removed first.
func Listen(sock, keyFile string, cfg Config) (*Server, error) {
	if cfg.Check == nil {
		return nil, errors.New("attach: no check of who connects")
	}
	if filepath.Dir(sock) != filepath.Dir(keyFile) {
		return nil, errors.New("attach: socket and key must share a folder")
	}
	if err := ensureDir(filepath.Dir(sock)); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(sock+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w (%s)", ErrTaken, sock)
		}
		return nil, err
	}
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		_ = ln.Close()
		_ = lock.Close()
		return nil, err
	}
	key, err := writeKey(keyFile)
	if err != nil {
		_ = ln.Close()
		_ = os.Remove(sock)
		_ = lock.Close()
		return nil, err
	}
	s := &Server{cfg: cfg, ln: ln, lock: lock, sock: sock, keyf: keyFile, key: key, all: map[net.Conn]bool{}}
	s.wg.Add(1)
	go s.accept()
	return s, nil
}

// Current is the client connected now, nil when none is.
func (s *Server) Current() *Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

// Close stops listening and says bye to the client, for reason, and
// removes the socket and the key. Only the first call does: once the lock
// is let go, the socket may be another core's.
func (s *Server) Close(reason string) { s.closed.Do(func() { s.close(reason) }) }

func (s *Server) close(reason string) {
	_ = s.ln.Close()
	s.mu.Lock()
	cur := s.cur
	s.cur = nil
	for c := range s.all {
		if cur == nil || c != cur.c {
			_ = c.Close()
		}
	}
	s.mu.Unlock()
	if cur != nil {
		cur.Close(reason)
		select {
		case <-cur.Done():
		case <-time.After(byeTimeout):
			cur.shut()
		}
	}
	s.wg.Wait()
	_ = os.Remove(s.sock)
	_ = os.Remove(s.keyf)
	_ = s.lock.Close()
}

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.all[c] = true
		s.mu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.serve(c)
		}()
	}
}

// serve lets c in, or refuses it: who connected first, by the kernel's
// word, then the key, then the generation.
func (s *Server) serve(c net.Conn) {
	forget := func() {
		s.mu.Lock()
		delete(s.all, c)
		s.mu.Unlock()
	}
	refuse := func(reason string) {
		_ = c.SetWriteDeadline(time.Now().Add(byeTimeout))
		_ = writeJSON(c, frameRefused, Refused{Reason: reason, Gen: Generation, Version: s.cfg.Version})
		_ = c.Close()
		forget()
	}
	if why := s.cfg.Check(hooks.PeerOf(c)); why != "" {
		refuse(why)
		return
	}
	_ = c.SetDeadline(time.Now().Add(handshakeTimeout))
	typ, body, err := readFrame(c)
	var h Hello
	if err == nil && typ == frameHello {
		err = json.Unmarshal(body, &h)
	}
	switch {
	case err != nil || typ != frameHello:
		refuse("a client must say hello first")
		return
	case !sameKey(h.Key, s.key):
		refuse("wrong key for this core")
		return
	case h.Gen != Generation:
		refuse(genMismatch(h, s.cfg.Version))
		return
	}
	if err := writeJSON(c, frameWelcome, Welcome{Gen: Generation, Version: s.cfg.Version, Features: s.cfg.Features}); err != nil {
		_ = c.Close()
		forget()
		return
	}
	_ = c.SetDeadline(time.Time{})
	cn := newConn(c)
	cn.version = h.Version
	s.mu.Lock()
	old := s.cur
	s.cur = cn
	s.mu.Unlock()
	if old != nil {
		old.Close(ByeTakenOver)
	}
	if s.cfg.Client != nil {
		s.cfg.Client(cn, h)
	}
	<-cn.Done()
	s.mu.Lock()
	if s.cur == cn {
		s.cur = nil
	}
	s.mu.Unlock()
	forget()
}

// genMismatch is what a client of another generation is told.
func genMismatch(h Hello, version string) string {
	return fmt.Sprintf("the nerdr core running for this folder is nerdr %s (protocol %d), and this is nerdr %s (protocol %d): "+
		"connect with nerdr %s, or quit that core, to start this one", version, Generation, h.Version, h.Gen, version)
}
