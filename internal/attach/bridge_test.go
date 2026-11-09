package attach

import (
	"bytes"
	"errors"
	"io"
	"net"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestPreambleIsFoundAfterWhatAShellSaid: a client finds the bridge's
// generation and version after whatever came before, and reads nothing
// past the preamble.
func TestPreambleIsFoundAfterWhatAShellSaid(t *testing.T) {
	r := strings.NewReader("Welcome to box!\n\x00\x00" + string(Preamble("v1.2.3")) + "H rest")
	gen, v, err := ReadPreamble(r)
	if err != nil || gen != Generation || v != "v1.2.3" {
		t.Fatalf("got %d %q %v", gen, v, err)
	}
	if rest, _ := io.ReadAll(r); string(rest) != "H rest" {
		t.Fatalf("read past the preamble: left %q", rest)
	}
	if _, _, err := ReadPreamble(strings.NewReader("bash: nerdr: command not found\n")); err == nil {
		t.Fatal("no bridge, and yet a preamble")
	}
}

// bridged is a client connection through a bridge to a core listening at
// sock, with env as the bridge's machine's: the client's end, after the
// preamble, as over ssh.
func bridged(t *testing.T, sock, key string, env []string) net.Conn {
	t.Helper()
	client, far := net.Pipe()
	core, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		defer far.Close()
		_ = Bridge(far, far, core, key, env)
	}()
	t.Cleanup(func() { client.Close(); core.Close() })
	return client
}

// TestBridgeHandsTheCoreItsOwnKeyAndEnvironment: through a bridge, the
// core gets the key from its key file and the environment of its own
// machine, never the client's, and what they say then goes both ways.
func TestBridgeHandsTheCoreItsOwnKeyAndEnvironment(t *testing.T) {
	_, sock, key, clients, hellos := listenHellos(t, takeAll)
	c := bridged(t, sock, key, []string{"PATH=/box/bin", "HOME=/home/box"})
	conn, w, err := Handshake(c, Hello{Gen: Generation, Version: "laptop", Key: "not the key", Env: []string{"SECRET=laptop"}})
	if err != nil {
		t.Fatal(err)
	}
	if w.Version != "core-1" {
		t.Fatalf("welcome %+v", w)
	}
	h := <-hellos
	if !slices.Equal(h.Env, []string{"PATH=/box/bin", "HOME=/home/box"}) || h.Version != "laptop" {
		t.Fatalf("the core was told %+v", h)
	}
	cc := <-clients
	conn.Send("", []byte("ping"))
	if b, err := cc.Recv(); err != nil || string(b) != "ping" {
		t.Fatalf("core got %q %v", b, err)
	}
	cc.Send("", []byte("pong"))
	if b, err := conn.Recv(); err != nil || string(b) != "pong" {
		t.Fatalf("client got %q %v", b, err)
	}
	cc.Close(ByeQuit)
	if _, err := conn.Recv(); !errors.As(err, new(*ByeError)) {
		t.Fatalf("the bye did not come through: %v", err)
	}
}

// TestBridgeRefusalComesThrough: a core that refuses the client, as for
// another generation, says why to the client on the other machine.
func TestBridgeRefusalComesThrough(t *testing.T) {
	_, sock, key, _ := listen(t, takeAll)
	c := bridged(t, sock, key, nil)
	_, _, err := Handshake(c, Hello{Gen: Generation + 1, Version: "newer"})
	var r *RefusedError
	if !errors.As(err, &r) || !strings.Contains(r.Reason, "protocol") {
		t.Fatalf("got %v", err)
	}
}

// TestBridgeWithNoKeySaysWhy: a bridge that cannot read the key, as when
// the core has gone, refuses the client with the reason.
func TestBridgeWithNoKeySaysWhy(t *testing.T) {
	_, sock, key, _ := listen(t, takeAll)
	c := bridged(t, sock, key+".gone", nil)
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, _, err := Handshake(c, Hello{Gen: Generation})
	var r *RefusedError
	if !errors.As(err, &r) || r.Reason == "" {
		t.Fatalf("got %v", err)
	}
}

func TestRefuseIsAFrameAClientReads(t *testing.T) {
	var b bytes.Buffer
	if err := Refuse(&b, "no core"); err != nil {
		t.Fatal(err)
	}
	typ, _, err := readFrame(&b)
	if err != nil || typ != frameRefused {
		t.Fatalf("%q %v", typ, err)
	}
}
