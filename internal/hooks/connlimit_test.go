package hooks

import (
	"bufio"
	"bytes"
	"net"
	"strings"
	"testing"
	"time"
)

// ping makes one request on a new connection to the server and reports
// whether nerdr answered it.
func ping(t *testing.T, s *Server) bool {
	t.Helper()
	c, err := net.DialTimeout("unix", s.Path(), time.Second)
	if err != nil {
		return false
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Write([]byte(`{"id":"1","method":"ping"}` + "\n")); err != nil {
		return false
	}
	line, err := bufio.NewReader(c).ReadString('\n')
	return err == nil && strings.Contains(line, "pong")
}

// served is how many connections the server holds.
func served(s *Server) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conn)
}

// An agent in a sandbox can connect to nerdr.sock: as many connections as
// it opens and leaves idle are not all served. Those over maxHookConns
// are closed at once, without the server stopping to accept, and once
// they go, another agent's hook is answered again.
func TestConnectionsOverTheLimitAreClosed(t *testing.T) {
	old := maxHookConns
	maxHookConns = 4
	t.Cleanup(func() { maxHookConns = old })
	s, _ := listen(t)

	var idle []net.Conn
	t.Cleanup(func() {
		for _, c := range idle {
			c.Close()
		}
	})
	for range maxHookConns {
		c, err := net.Dial("unix", s.Path())
		if err != nil {
			t.Fatal(err)
		}
		idle = append(idle, c)
	}
	deadline := time.Now().Add(2 * time.Second)
	for served(s) < maxHookConns && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := served(s); n != maxHookConns {
		t.Fatalf("served %d, want %d", n, maxHookConns)
	}

	// More are accepted and closed, not left waiting or served.
	for range 3 * maxHookConns {
		c, err := net.Dial("unix", s.Path())
		if err != nil {
			t.Fatal(err)
		}
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		if n, err := c.Read(make([]byte, 1)); n != 0 || err == nil || isTimeout(err) {
			t.Fatalf("a connection over the limit was not closed: %d %v", n, err)
		}
		c.Close()
	}
	if n := served(s); n > maxHookConns {
		t.Fatalf("served %d over the limit", n)
	}

	// The idle ones let go: nerdr answers again.
	for _, c := range idle {
		c.Close()
	}
	idle = nil
	deadline = time.Now().Add(2 * time.Second)
	for !ping(t, s) {
		if time.Now().After(deadline) {
			t.Fatal("not answered once the connections were closed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}

// A connection that sent a long request holds no long buffer once it is
// answered, and only maxBigRequests long requests are read at a time:
// another is refused as busy rather than read into memory beside them.
func TestLongRequestsAreBounded(t *testing.T) {
	s, _ := listen(t)
	long := `{"id":"1","method":"ping","params":{"x":"` + strings.Repeat("a", 2*smallRequest) + `"}}` + "\n"

	// A long request, answered, then a short one on the same connection:
	// both work, and the place the long one took is given back.
	c, err := net.Dial("unix", s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(c)
	for _, req := range []string{long, `{"id":"2","method":"ping"}` + "\n"} {
		if _, err := c.Write([]byte(req)); err != nil {
			t.Fatal(err)
		}
		if line, err := r.ReadString('\n'); err != nil || !strings.Contains(line, "pong") {
			t.Fatalf("answer %q %v", line, err)
		}
	}
	if n := len(s.big); n != 0 {
		t.Fatalf("%d long places still held", n)
	}

	// maxBigRequests connections each part way through a long request
	// hold every place: one more long request is refused as busy.
	// More than a buffer past smallRequest, which is read only in whole
	// buffers before the line end comes.
	part := long[:smallRequest+3*4096]
	var held []net.Conn
	defer func() {
		for _, h := range held {
			h.Close()
		}
	}()
	for range maxBigRequests {
		h, err := net.Dial("unix", s.Path())
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, h)
		if _, err := h.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(s.big) < maxBigRequests && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := len(s.big); n != maxBigRequests {
		t.Fatalf("%d long places held, want %d", n, maxBigRequests)
	}
	c2, err := net.Dial("unix", s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	_ = c2.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = c2.Write([]byte(long))
	if line, _ := bufio.NewReader(c2).ReadString('\n'); !strings.Contains(line, `"busy"`) {
		t.Fatalf("answer %q, want busy", line)
	}

	// A short request is still answered meanwhile, and once the long ones
	// go, a long one is again.
	if !ping(t, s) {
		t.Fatal("a short request was not answered")
	}
	for _, h := range held {
		h.Close()
	}
	held = nil
	deadline = time.Now().Add(2 * time.Second)
	for len(s.big) > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	c3, err := net.Dial("unix", s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer c3.Close()
	_ = c3.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = c3.Write([]byte(long))
	if line, _ := bufio.NewReader(c3).ReadString('\n'); !strings.Contains(line, "pong") {
		t.Fatalf("answer %q once the long ones went", line)
	}
}

// readRequest reads lines as bufio.ScanLines did: a CR before the line
// end is dropped, the last line needs none, and one over maxRequest ends
// the connection.
func TestReadRequestSplitsAsLinesDid(t *testing.T) {
	s := &Server{big: make(chan struct{}, maxBigRequests)}
	r := bufio.NewReaderSize(strings.NewReader("a\r\n\nb\nlast"), 16)
	var got []string
	for {
		line, release, err := s.readRequest(r)
		if err != nil {
			break
		}
		got = append(got, string(line))
		release()
	}
	if strings.Join(got, "|") != "a||b|last" {
		t.Fatalf("lines %q", got)
	}
	tooLong := bufio.NewReader(bytes.NewReader(bytes.Repeat([]byte("x"), maxRequest+10)))
	if _, _, err := s.readRequest(tooLong); err != bufio.ErrTooLong {
		t.Fatalf("a line too long: %v", err)
	}
	if len(s.big) != 0 {
		t.Fatal("a place was kept by a line too long")
	}
}
