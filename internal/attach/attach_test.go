package attach

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/hooks"
)

// dir is a folder for sockets: short, as a socket's path must be.
func dir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "att")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return filepath.Join(d, "run")
}

func takeAll(hooks.Peer) string { return "" }

// listen is a core's client socket in a new folder, handing each client
// let in to clients.
func listen(t *testing.T, check func(hooks.Peer) string) (s *Server, sock, key string, clients chan *Conn) {
	t.Helper()
	d := dir(t)
	sock, key = filepath.Join(d, "c.sock"), filepath.Join(d, "c.key")
	clients = make(chan *Conn, 4)
	s, err := Listen(sock, key, Config{
		Version: "core-1", Features: []string{"rows"}, Check: check,
		Client: func(c *Conn, _ Hello) { clients <- c },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close("") })
	return s, sock, key, clients
}

func recv(t *testing.T, c *Conn) string {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		b, err := c.Recv()
		if err != nil {
			got <- "error: " + err.Error()
			return
		}
		got <- string(b)
	}()
	select {
	case s := <-got:
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("nothing received")
		return ""
	}
}

func TestDir(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/7")
	if d, _ := Dir(); d != "/run/user/7/nerdr" {
		t.Errorf("Dir = %q", d)
	}
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("HOME", "/home/u")
	d, _ := Dir()
	if strings.HasPrefix(d, os.TempDir()) || !strings.HasPrefix(d, "/home/u/") {
		t.Errorf("Dir without XDG_RUNTIME_DIR = %q", d)
	}
	t.Setenv(EnvDir, "/x")
	a, _, _ := Paths("/repos/a")
	b, _, _ := Paths("/repos/b")
	if a == b || filepath.Dir(a) != "/x" || len(a) > 100 {
		t.Errorf("Paths: %q, %q", a, b)
	}
}

func TestDirIsGuarded(t *testing.T) {
	d := dir(t)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := checkDir(d, false); err == nil {
		t.Error("a folder others may enter passed")
	}
	if err := ensureDir(d); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(d); fi.Mode().Perm() != 0o700 {
		t.Errorf("mode %o after ensureDir", fi.Mode().Perm())
	}
	link := d + "-link"
	if err := os.Symlink(d, link); err != nil {
		t.Fatal(err)
	}
	if err := checkDir(link, true); err == nil {
		t.Error("a link in the folder's place passed")
	}
}

func TestKeyFile(t *testing.T) {
	d := dir(t)
	if err := ensureDir(d); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(d, "k.key")
	key, err := writeKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("key mode %o", fi.Mode().Perm())
	}
	if got, err := readKey(path); err != nil || got != key || len(key) != 2*keyLen {
		t.Errorf("readKey = %q, %v; wrote %q", got, err, key)
	}
	if again, _ := writeKey(path); again == key {
		t.Error("the same key twice")
	}
	_ = os.Chmod(path, 0o644)
	if _, err := readKey(path); err == nil {
		t.Error("a key others can read was taken")
	}
	_ = os.Chmod(path, 0o600)
	link := filepath.Join(d, "l.key")
	_ = os.Symlink(path, link)
	if _, err := readKey(link); err == nil {
		t.Error("a key through a link was taken")
	}
	if sameKey("", "") || !sameKey("ab", "ab") || sameKey("ab", "ac") {
		t.Error("sameKey")
	}
}

// TestConnect: a client is let in with the key, and the two can talk.
func TestConnect(t *testing.T) {
	var peer hooks.Peer
	var mu sync.Mutex
	s, sock, key, clients := listen(t, func(p hooks.Peer) string {
		mu.Lock()
		peer = p
		mu.Unlock()
		return ""
	})
	c, w, err := Dial(sock, key, Hello{Version: "client-1", Features: []string{"rows", "gui"}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close("")
	if w.Gen != Generation || w.Version != "core-1" || !slices.Contains(w.Features, "rows") {
		t.Errorf("welcome %+v", w)
	}
	sc := <-clients
	if s.Current() != sc {
		t.Error("Current is not the client let in")
	}
	mu.Lock()
	if peer.Known && !slices.Contains(peer.Chain, os.Getpid()) {
		t.Errorf("the check was not given who connected: %+v", peer)
	}
	mu.Unlock()
	c.Send("", []byte("from the client"))
	if got := recv(t, sc); got != "from the client" {
		t.Errorf("core got %q", got)
	}
	sc.Send("", []byte("from the core"))
	if got := recv(t, c); got != "from the core" {
		t.Errorf("client got %q", got)
	}
}

func TestRefused(t *testing.T) {
	_, sock, key, _ := listen(t, takeAll)
	var re *RefusedError

	// No key, or the wrong one.
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Handshake(c, Hello{Gen: Generation, Key: strings.Repeat("0", 2*keyLen)}); !errors.As(err, &re) {
		t.Errorf("wrong key: %v", err)
	}
	_ = c.Close()

	// Another generation, told what runs.
	right, _ := readKey(key)
	c, _ = net.Dial("unix", sock)
	_, _, err = Handshake(c, Hello{Gen: Generation + 1, Version: "client-9", Key: right})
	if !errors.As(err, &re) || re.Version != "core-1" || re.Gen != Generation ||
		!strings.Contains(re.Reason, "core-1") || !strings.Contains(re.Reason, "client-9") {
		t.Errorf("other generation: %v", err)
	}
	_ = c.Close()

}

// TestSilent: a client that says nothing is refused in time.
func TestSilent(t *testing.T) {
	was := handshakeTimeout
	handshakeTimeout = 100 * time.Millisecond
	t.Cleanup(func() { handshakeTimeout = was })
	_, sock, _, _ := listen(t, takeAll)
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if typ, _, err := readFrame(c); err != nil || typ != frameRefused {
		t.Errorf("silent client: %q, %v", typ, err)
	}
}

// TestCheckFirst: a peer the check refuses is told why before anything
// is read, key or not.
func TestCheckFirst(t *testing.T) {
	_, sock, key, clients := listen(t, func(hooks.Peer) string { return "not from a program nerdr runs" })
	_, _, err := Dial(sock, key, Hello{})
	var re *RefusedError
	if !errors.As(err, &re) || re.Reason != "not from a program nerdr runs" {
		t.Errorf("Dial = %v", err)
	}
	select {
	case <-clients:
		t.Error("a refused client was let in")
	default:
	}
}

func TestListenNeedsCheck(t *testing.T) {
	d := dir(t)
	if _, err := Listen(filepath.Join(d, "s"), filepath.Join(d, "k"), Config{}); err == nil {
		t.Error("listened with no check")
	}
}

// TestTakeOver: a second client takes over, and the first is told.
func TestTakeOver(t *testing.T) {
	s, sock, key, clients := listen(t, takeAll)
	first, _, err := Dial(sock, key, Hello{})
	if err != nil {
		t.Fatal(err)
	}
	<-clients
	second, _, err := Dial(sock, key, Hello{})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close("")
	sc := <-clients
	if got := recv(t, first); got != "error: "+ByeTakenOver {
		t.Errorf("first got %q", got)
	}
	if s.Current() != sc {
		t.Error("Current is not the second")
	}
	sc.Send("", []byte("hi"))
	if got := recv(t, second); got != "hi" {
		t.Errorf("second got %q", got)
	}
}

func TestNoCore(t *testing.T) {
	d := dir(t)
	_ = ensureDir(d)
	sock, key := filepath.Join(d, "c.sock"), filepath.Join(d, "c.key")
	if _, _, err := Dial(sock, key, Hello{}); !errors.Is(err, ErrNoCore) {
		t.Errorf("no socket: %v", err)
	}
	// One left by a core that died.
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = ln.Close()
	if _, _, err := Dial(sock, key, Hello{}); !errors.Is(err, ErrNoCore) {
		t.Errorf("stale socket: %v", err)
	}
	s, err := Listen(sock, key, Config{Check: takeAll})
	if err != nil {
		t.Fatalf("listen over a stale socket: %v", err)
	}
	defer s.Close("")
	if _, err := Listen(sock, key, Config{Check: takeAll}); !errors.Is(err, ErrTaken) {
		t.Errorf("second core: %v", err)
	}
}

// TestClose: a core closing says bye, and leaves neither socket nor key.
func TestClose(t *testing.T) {
	s, sock, key, clients := listen(t, takeAll)
	c, _, err := Dial(sock, key, Hello{})
	if err != nil {
		t.Fatal(err)
	}
	<-clients
	done := make(chan string, 1)
	go func() { done <- recv(t, c) }()
	s.Close(ByeQuit)
	if got := <-done; got != "error: "+ByeQuit {
		t.Errorf("client got %q", got)
	}
	for _, p := range []string{sock, key} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s left", p)
		}
	}
}

// TestSlowClient: what waits for a client that does not read never grows
// past the limit; the newest of each keyed message wins, and falling
// behind drops what waits and asks for everything afresh.
func TestSlowClient(t *testing.T) {
	a, b := net.Pipe() // writes wait for a read: b never reads
	defer b.Close()
	c := newConn(a)
	defer c.Close("")
	var behind atomic.Int32
	c.OnBehind(func() { behind.Add(1) })

	big := make([]byte, 1<<20)
	for range 100 {
		c.Send("screen 1", big)
	}
	c.mu.Lock()
	n, bytes := len(c.queue), c.bytes
	c.mu.Unlock()
	if n > 2 || bytes > 2<<20 {
		t.Errorf("keyed: %d waiting, %d bytes", n, bytes)
	}
	for range 100 {
		c.Send("", big)
	}
	c.mu.Lock()
	bytes = c.bytes
	c.mu.Unlock()
	if bytes > QueueLimit {
		t.Errorf("%d bytes waiting, over the limit", bytes)
	}
	if behind.Load() == 0 {
		t.Error("never told it fell behind")
	}
}

// TestSlowClientGrowing: keyed messages that grow while the client does
// not read are held to the limit too.
func TestSlowClientGrowing(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	c := newConn(a)
	defer c.Close("")
	var behind atomic.Int32
	c.OnBehind(func() { behind.Add(1) })

	small := make([]byte, 1<<10)
	for i := range 64 {
		c.Send(fmt.Sprint("screen ", i), small)
	}
	big := make([]byte, 1<<20)
	for i := range 64 {
		c.Send(fmt.Sprint("screen ", i), big)
		c.mu.Lock()
		bytes := c.bytes
		c.mu.Unlock()
		if bytes > QueueLimit {
			t.Fatalf("%d bytes waiting, over the limit", bytes)
		}
	}
	if behind.Load() == 0 {
		t.Error("never told it fell behind")
	}
}

// TestFrameLimit: a frame says how long it is, and one too long is not
// read.
func TestFrameLimit(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	go func() { _, _ = a.Write([]byte{frameMsg, 0xff, 0xff, 0xff, 0xff}) }()
	if _, _, err := readFrame(b); err == nil {
		t.Error("read a frame over the limit")
	}
	if err := writeFrame(a, frameMsg, make([]byte, MaxFrame+1)); err == nil {
		t.Error("wrote a frame over the limit")
	}
}

// TestSendFunc: a body made as it is written is made once, the newest
// with its key; the ends of a Pair hear each other.
func TestSendFunc(t *testing.T) {
	a, b, err := Pair()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close("")
	defer b.Close("")
	var built atomic.Int32
	block := make(chan struct{})
	a.SendFunc("first", func() []byte { <-block; return []byte("first") })
	for i := range 10 {
		a.SendFunc("screen", func() []byte { built.Add(1); return fmt.Appendf(nil, "screen %d", i) })
	}
	a.Send("screen", []byte("sent"))
	a.SendFunc("screen", func() []byte { built.Add(1); return []byte("newest") })
	a.SendFunc("none", func() []byte { return nil })
	a.Send("", []byte("last"))
	close(block)
	for _, want := range []string{"first", "newest", "last"} {
		if got := recv(t, b); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
	if n := built.Load(); n != 1 {
		t.Fatalf("built %d times", n)
	}
	b.Send("", []byte("back"))
	if got := recv(t, a); got != "back" {
		t.Fatalf("back: %q", got)
	}
}

// Closed a second time, a server leaves alone what another core has made
// since: the lock let go, the socket may be that core's.
func TestCloseTwice(t *testing.T) {
	s, sock, key, _ := listen(t, takeAll)
	s.Close(ByeQuit)
	next, err := Listen(sock, key, Config{Check: func(hooks.Peer) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close("")
	s.Close(ByeQuit)
	if _, err := os.Stat(sock); err != nil {
		t.Errorf("the next core's socket is gone: %v", err)
	}
}
