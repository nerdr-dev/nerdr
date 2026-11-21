package main

import (
	"bytes"
	"io"
	"os"
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
