package input

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

func TestPrefixSplitsPassthrough(t *testing.T) {
	r := New(nil, 0x11)
	var sunk bytes.Buffer
	var msgs []any
	r.SetSend(func(m any) { msgs = append(msgs, m) })
	r.Passthrough(func(b []byte) { sunk.Write(b) })

	got := make(chan []byte)
	go func() { b, _ := io.ReadAll(r.pr); got <- b }()

	r.route([]byte("hello\x1b[I world\x11n more"))
	_ = r.pw.Close()
	app := <-got

	if sunk.String() != "hello world" {
		t.Fatalf("sink got %q", sunk.String())
	}
	if string(app) != "\x1b[In more" {
		t.Fatalf("app got %q", app)
	}
	if len(msgs) != 1 {
		t.Fatalf("msgs = %v", msgs)
	}
	if r.pass.Load() {
		t.Fatal("still passing through after prefix")
	}
}

func TestSequenceSplitAcrossReadsIsStillTakenOut(t *testing.T) {
	r := New(nil, 0x11)
	var sunk bytes.Buffer
	r.Passthrough(func(b []byte) { sunk.Write(b) })

	got := make(chan []byte)
	go func() { b, _ := io.ReadAll(r.pr); got <- b }()

	r.route([]byte("abc\x1b["))
	r.route([]byte("Idef\x1b[1"))
	r.route([]byte("2~ghi"))
	_ = r.pw.Close()
	app := <-got

	if sunk.String() != "abcdefghi" {
		t.Fatalf("sink got %q", sunk.String())
	}
	if string(app) != "\x1b[I\x1b[12~" {
		t.Fatalf("app got %q", app)
	}
}

func TestHeldBytesAreFlushedWhenNothingFollows(t *testing.T) {
	r := New(nil, 0x11)
	sunk := make(chan string, 4)
	r.Passthrough(func(b []byte) { sunk <- string(b) })

	r.route([]byte("x\x1b["))
	if s := <-sunk; s != "x" {
		t.Fatalf("first sink %q", s)
	}
	select {
	case s := <-sunk:
		if s != "\x1b[" {
			t.Fatalf("flushed %q", s)
		}
	case <-time.After(time.Second):
		t.Fatal("held bytes were never flushed")
	}
}

func TestMouseReportsGoToTheProgram(t *testing.T) {
	r := New(nil, 0x11)
	var sunk bytes.Buffer
	r.Passthrough(func(b []byte) { sunk.Write(b) })

	got := make(chan []byte)
	go func() { b, _ := io.ReadAll(r.pr); got <- b }()

	r.route([]byte("ab\x1b[<0;12;3Mcd\x1b[<0;1"))
	r.route([]byte("2;3mef\x1b[<35;4;5M"))
	_ = r.pw.Close()
	app := <-got

	if sunk.String() != "abcdef" {
		t.Fatalf("sink got %q", sunk.String())
	}
	if string(app) != "\x1b[<0;12;3M\x1b[<0;12;3m\x1b[<35;4;5M" {
		t.Fatalf("app got %q", app)
	}
}

// bubbletea reads 256 bytes at a time and takes a mouse report cut off
// at the end of a read for keys: an escape that closes a dialog, and the
// rest typed. A quick turn of the wheel sends more than that at once, cut
// off at the end of the terminal's read as well.
func TestWheelBurstReachesTheProgramWhole(t *testing.T) {
	r := New(nil, 0x11)

	reads := make(chan []string)
	go func() {
		var got []string
		buf := make([]byte, 256)
		for {
			n, err := r.pr.Read(buf)
			if err != nil {
				reads <- got
				return
			}
			got = append(got, string(buf[:n]))
		}
	}()

	report := "\x1b[<65;24;58M"
	burst := strings.Repeat(report, 40)
	r.route([]byte(burst + report[:7]))
	r.route([]byte(report[7:] + "q"))
	_ = r.pw.Close()
	got := <-reads

	if strings.Join(got, "") != burst+report+"q" {
		t.Fatalf("program got %q", strings.Join(got, ""))
	}
	for _, s := range got {
		if s != report && s != "q" {
			t.Fatalf("read %q is not one report", s)
		}
	}
}

// F1-F5 are nerdr's, for the review panel's screens, while an agent has
// the keyboard; F6 and up are the agent's.
func TestF5GoesToNerdrAndF6ToTheAgent(t *testing.T) {
	r := New(nil, 0x11)
	var sunk bytes.Buffer
	r.Passthrough(func(b []byte) { sunk.Write(b) })

	got := make(chan []byte)
	go func() { b, _ := io.ReadAll(r.pr); got <- b }()

	r.route([]byte("a\x1bOSb\x1b[15~c\x1b[17~d"))
	_ = r.pw.Close()
	app := <-got

	if sunk.String() != "abc\x1b[17~d" || string(app) != "\x1bOS\x1b[15~" {
		t.Fatalf("sink got %q, app got %q", sunk.String(), app)
	}
}

// A shell or an editor has the F-keys for itself: htop and mc use them.
func TestFKeysPassToAShell(t *testing.T) {
	r := New(nil, 0x11)
	var sunk bytes.Buffer
	r.Passthrough(func(b []byte) { sunk.Write(b) })
	r.PassFKeys(true)

	got := make(chan []byte)
	go func() { b, _ := io.ReadAll(r.pr); got <- b }()

	r.route([]byte("a\x1bOPb\x11"))
	_ = r.pw.Close()
	app := <-got

	if sunk.String() != "a\x1bOPb" || len(app) != 0 {
		t.Fatalf("sink got %q, app got %q", sunk.String(), app)
	}
}

// A mouse report cut off right after its ESC — the wheel turned over the
// review panel while the agent has the keyboard — is still taken out: the
// agent gets neither an escape nor the rest of it as text.
func TestMouseReportCutAfterItsESC(t *testing.T) {
	r := New(nil, 0x11)
	var sunk bytes.Buffer
	r.Passthrough(func(b []byte) { sunk.Write(b) })

	got := make(chan []byte)
	go func() { b, _ := io.ReadAll(r.pr); got <- b }()

	r.route([]byte("ab\x1b"))
	r.route([]byte("[<64;155;25Mcd"))
	_ = r.pw.Close()
	app := <-got

	if sunk.String() != "abcd" || string(app) != "\x1b[<64;155;25M" {
		t.Fatalf("sink got %q, app got %q", sunk.String(), app)
	}
}

// A lone ESC is the escape key: it reaches the agent once nothing follows.
func TestLoneESCReachesTheAgent(t *testing.T) {
	r := New(nil, 0x11)
	sunk := make(chan string, 4)
	r.Passthrough(func(b []byte) { sunk <- string(b) })

	r.route([]byte("\x1b"))
	select {
	case s := <-sunk:
		if s != "\x1b" {
			t.Fatalf("flushed %q", s)
		}
	case <-time.After(time.Second):
		t.Fatal("escape was never sent")
	}
}

// The start of a mouse report whose rest comes too late for holdFor is
// dropped, and the rest with it when it comes: none of it is typed.
func TestLateMouseReportRestIsDropped(t *testing.T) {
	r := New(nil, 0x11)
	var sunk bytes.Buffer
	r.Passthrough(func(b []byte) { sunk.Write(b) })

	r.route([]byte("ab\x1b[<64;15"))
	time.Sleep(3 * holdFor)
	r.route([]byte("4;25Mcd"))

	r.routeMu.Lock()
	defer r.routeMu.Unlock()
	if sunk.String() != "abcd" {
		t.Fatalf("sink got %q", sunk.String())
	}
}
