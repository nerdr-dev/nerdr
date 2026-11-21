package main

import (
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/api"
)

// TestAPISchema: nerdr api --schema prints the schema, with no nerdr
// running, and takes it for no folder.
func TestAPISchema(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	// Read while it writes: the schema is more than a pipe may hold.
	read := make(chan []byte)
	go func() {
		b, _ := io.ReadAll(r)
		read <- b
	}()
	out := os.Stdout
	os.Stdout = w
	code := runAPI([]string{"--schema"})
	os.Stdout = out
	_ = w.Close()
	got := <-read
	if code != 0 || !bytes.Equal(got, api.Schema) || len(got) == 0 {
		t.Fatalf("exit %d, %d bytes of %d", code, len(got), len(api.Schema))
	}
}

// TestRefusalAfterBrokenPipe: a core that refuses says why and closes
// before reading the hello; the hello's write then fails, and the reason
// is still read.
func TestRefusalAfterBrokenPipe(t *testing.T) {
	dir, err := os.MkdirTemp("", "nrefuse")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	ln, err := net.Listen("unix", filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	closed := make(chan struct{})
	go func() {
		c, err := ln.Accept()
		if err == nil {
			_, _ = c.Write([]byte(`{"error":"not you"}` + "\n"))
			_ = c.Close()
		}
		close(closed)
	}()
	c, err := net.Dial("unix", filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	<-closed
	// The first write after the close may still be taken; one of these
	// fails.
	for i := 0; i < 100; i++ {
		if _, err = c.Write([]byte("hello\n")); err != nil {
			break
		}
	}
	if err == nil {
		t.Fatal("writing to a closed connection never failed")
	}
	if why := refusalOn(c); why != "not you" {
		t.Fatalf("got %q", why)
	}
}
