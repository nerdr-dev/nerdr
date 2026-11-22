package agents

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/iotest"
	"time"
)

// What is looked for is found across the pieces a log is read in, and
// where it is not there it is not made up out of the tail kept between.
func TestReaderHasFindsAcrossPieces(t *testing.T) {
	text := strings.Repeat("x", 100) + `"user.message"` + strings.Repeat("y", 100)
	for size := 1; size < 40; size++ {
		if !readerHas(iotest.OneByteReader(strings.NewReader(text)), `"user.message"`, size) {
			t.Errorf("size %d: not found", size)
		}
		if readerHas(strings.NewReader(text), `"user.messages"`, size) {
			t.Errorf("size %d: found what is not there", size)
		}
	}
}

// Copilot's sessions are looked at newest first, so a newer one with
// something said in it is taken without reading the older ones' logs.
func TestCopilotReadsOnlyTheNewestSessions(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	session := func(id string, at time.Time) string {
		root := filepath.Join(home, ".copilot", "session-state", id)
		writeFile(t, filepath.Join(root, "workspace.yaml"), "id: "+id+"\ncwd: "+dir+"\n")
		_ = os.Chtimes(filepath.Join(root, "workspace.yaml"), at, at)
		return filepath.Join(root, "events.jsonl")
	}
	writeFile(t, session("new", time.Now()), `{"type":"user.message"}`)
	// Opening a named pipe for reading blocks until a writer comes: were
	// the older session read, the call would hang.
	if err := syscall.Mkfifo(session("old", time.Now().Add(-time.Hour)), 0o600); err != nil {
		t.Skip(err)
	}
	got := make(chan string, 1)
	go func() { got <- latestCopilotSession(dir, nil) }()
	select {
	case id := <-got:
		if id != "new" {
			t.Errorf("got %q", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read the older session's log")
	}
}

// A line longer than the most eachLine reads is passed over, not held
// whole, and the lines after it still come.
func TestEachLineSkipsAGiantLine(t *testing.T) {
	text := "one\n" + strings.Repeat("z", 100) + "\ntwo\nthree"
	var lines []string
	err := eachLineOf(bufio.NewReaderSize(strings.NewReader(text), 16), 20, func(b []byte) { lines = append(lines, string(b)) })
	if err != nil || strings.Join(lines, "|") != "one\n|two\n|three" {
		t.Errorf("%q, %v", lines, err)
	}
}

// A read that fails is said, not taken for the end of the file.
func TestEachLineSaysAReadFailed(t *testing.T) {
	boom := errors.New("boom")
	r := io.MultiReader(strings.NewReader("one\n"), iotest.ErrReader(boom))
	var n int
	if err := eachLineOf(bufio.NewReader(r), maxLine, func([]byte) { n++ }); !errors.Is(err, boom) || n != 1 {
		t.Errorf("%d lines, %v", n, err)
	}
	if err := eachLine(t.TempDir(), func([]byte) {}); err == nil {
		t.Error("reading a folder as a log did not fail")
	}
}
