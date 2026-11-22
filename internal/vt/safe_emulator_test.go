package vt

// nerdr's tests of safe_emulator.go. See UPSTREAM.md.

import (
	"io"
	"sync"
	"testing"
)

// TestSafeEmulatorConcurrent covers a SafeEmulator used from many
// goroutines at once, as the race detector sees it: its answers read while
// it is written to, cells and the scrollback read while lines scroll into
// it, and closed while all that goes on.
func TestSafeEmulatorConcurrent(t *testing.T) {
	se := NewSafeEmulator(20, 4)
	se.SetScrollbackSize(50)
	read := make(chan struct{})
	go func() {
		defer close(read)
		_, _ = io.Copy(io.Discard, se)
	}()
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for i := range 200 {
				_, _ = se.WriteString("line\r\n\x1b[6n\x1b[c")
				if i%50 == 0 {
					se.Resize(20+i%3, 4)
				}
			}
		})
	}
	for range 4 {
		wg.Go(func() {
			for range 200 {
				if c := se.CellAt(0, 0); c != nil {
					c.Content = "changed"
				}
				if c := se.ScrollbackCellAt(0, 0); c != nil {
					c.Content = "changed"
				}
				_ = se.String()
				_ = se.Bounds()
				_ = se.WidthMethod()
				_, _ = se.Checkpoint()
			}
		})
	}
	wg.Wait()
	// What was changed was a copy.
	if c := se.CellAt(0, 0); c == nil || c.Content == "changed" {
		t.Fatalf("cell = %+v", c)
	}
	if c := se.ScrollbackCellAt(0, 0); c == nil || c.Content == "changed" {
		t.Fatalf("scrollback cell = %+v", c)
	}
	var closing sync.WaitGroup
	for range 3 {
		closing.Go(func() { _ = se.Close() })
	}
	closing.Wait()
	<-read
	if _, err := se.Write([]byte("x")); err == nil {
		t.Fatal("written after Close")
	}
}

// TestSafeEmulatorCloseEndsWrite covers a Write that waits for its answer
// to be read, which no one does: Close ends it, and does not wait for it.
func TestSafeEmulatorCloseEndsWrite(t *testing.T) {
	se := NewSafeEmulator(20, 4)
	wrote := make(chan struct{})
	go func() {
		defer close(wrote)
		_, _ = se.Write([]byte("\x1b[6n"))
	}()
	if err := se.Close(); err != nil {
		t.Fatal(err)
	}
	<-wrote
}
