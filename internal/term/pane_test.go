package term

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestPaneRunsAndCapturesScreen(t *testing.T) {
	p, err := Start([]string{"sh", "-c", `printf '\033]0;hello\007'; printf '\033[31mred\033[0m plain\n'; printf '\033]9;4;1;50\007'; sleep 0.3`}, t.TempDir(), os.Environ(), 40, 5, func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	<-p.Done()
	in := p.Snapshot()
	if !strings.HasPrefix(in.Screen, "red plain") {
		t.Fatalf("screen = %q", in.Screen)
	}
	if in.Title != "hello" {
		t.Fatalf("title = %q", in.Title)
	}
	if in.Progress != "4;1;50" {
		t.Fatalf("progress = %q", in.Progress)
	}
	lines := p.Render(0, false)
	if len(lines) != 5 || !strings.Contains(lines[0], "red") || !strings.Contains(lines[0], "\x1b[") {
		t.Fatalf("render = %q", lines)
	}
	if ex, _, _ := p.Exited(); !ex {
		t.Fatal("not exited")
	}
	_ = time.Second
}

// TestPaneTakesWhatIsCopied covers OSC 52: what a program copies is kept
// for the app to put on the clipboard, once, and a query is not.
func TestPaneTakesWhatIsCopied(t *testing.T) {
	p, err := Start([]string{"sh", "-c", `printf '\033]52;c;aGVsbG8=\007'; printf '\033]52;c;?\007'; sleep 0.3`}, t.TempDir(), os.Environ(), 40, 5, func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	<-p.Done()
	if got := p.TakeCopied(); got != "hello" {
		t.Fatalf("copied = %q", got)
	}
	if got := p.TakeCopied(); got != "" {
		t.Fatalf("copied again = %q", got)
	}
}

func TestClipboardSet(t *testing.T) {
	for data, want := range map[string]string{
		"52;c;aGVsbG8=": "hello",
		"52;;aGk=":      "hi",
		"52;c;?":        "",
		"52;c;!!":       "",
		"52;c;":         "",
		"52":            "",
	} {
		got, ok := clipboardSet([]byte(data))
		if got != want || ok != (want != "") {
			t.Errorf("clipboardSet(%q) = %q, %v", data, got, ok)
		}
	}
}

// TestPrintableTakesEmojiApart covers the emoji a terminal and nerdr
// would measure differently: each character is left on its own.
func TestPrintableTakesEmojiApart(t *testing.T) {
	for in, want := range map[string]string{
		"ok ⚠️ 👍🏽":  "ok ⚠ 👍",
		"👨‍👩‍👧":     "👨👩👧",
		"plain æøå": "plain æøå",
		// Copilot's paths, a break hinted after each /
		"packages/\u200btypes/\u200bsrc": "packages/types/src",
		"\ufeffbom \u202eright":          "bom right",
	} {
		if got := Printable(in); got != want {
			t.Errorf("Printable(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCellTextKeepsTheCellsWidth covers characters of several code points
// in the agent's pane: drawn so a terminal drawing each code point makes
// them as wide as the emulator's cell.
func TestCellTextKeepsTheCellsWidth(t *testing.T) {
	for _, c := range []struct {
		in   string
		w    int
		want string
	}{
		{"👍🏽", 2, "👍"},
		{"👨‍👩‍👧", 2, "👨"},
		{"⚠️", 2, "⚠ "},
		{"🇳🇴", 2, "🇳🇴"},
		{"é", 1, "é"},
		{"a", 1, "a"},
	} {
		if got := cellText(c.in, c.w); got != c.want {
			t.Errorf("cellText(%q, %d) = %q, want %q", c.in, c.w, got, c.want)
		}
	}
}

// TestWriteDoesNotWaitForTheProgram: a large paste into a program that is
// not reading does not hold up the one writing it, and reaches the
// program, in order, once it reads.
func TestWriteDoesNotWaitForTheProgram(t *testing.T) {
	out := t.TempDir() + "/got"
	p, err := Start([]string{"sh", "-c", "stty raw -echo; echo raw; sleep 1; head -c 1048580 > " + out}, t.TempDir(), os.Environ(), 40, 5, func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	// Written before the terminal is raw, a line this long is cut short
	// (to MAX_CANON on macOS).
	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(p.Snapshot().Screen, "raw"); {
		if time.Now().After(deadline) {
			t.Fatal("the terminal was not made raw")
		}
		time.Sleep(10 * time.Millisecond)
	}
	start := time.Now()
	p.Write([]byte(strings.Repeat("a", 1<<20)))
	p.Write([]byte("bcde"))
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("writing waited %s for the program", d)
	}
	select {
	case <-p.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the program did not get it all")
	}
	got, err := os.ReadFile(out)
	if err != nil || len(got) != 1<<20+4 || string(got[len(got)-4:]) != "bcde" {
		t.Fatalf("got %d bytes, ending %q: %v", len(got), got[max(len(got)-4, 0):], err)
	}
}

// TestWritesLeftAreLetGo: what a program never read is let go once it
// has exited, though a write to it hangs, and what is written after is
// not kept either.
func TestWritesLeftAreLetGo(t *testing.T) {
	p, err := Start([]string{"sh", "-c", "stty raw -echo; sleep 0.3"}, t.TempDir(), os.Environ(), 40, 5, func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for range 4 {
		p.Write([]byte(strings.Repeat("a", 1<<20)))
	}
	<-p.Done()
	deadline := time.Now().Add(5 * time.Second)
	for {
		p.outMu.Lock()
		n, done := len(p.out), p.outDone
		p.outMu.Unlock()
		if n == 0 && done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d writes still kept after the program exited", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.Write([]byte("late"))
	p.outMu.Lock()
	defer p.outMu.Unlock()
	if len(p.out) != 0 {
		t.Fatal("a write after the end was kept")
	}
}

// A program that exits at the hangup, leaving a child that ignores it,
// does not leave the child running once Stop has returned: it would go on
// writing to a worktree being deleted.
func TestStopStopsWhatIsLeftOfTheGroup(t *testing.T) {
	beat := t.TempDir() + "/beat"
	// wait, unlike sleep, is cut short by the hangup's trap.
	p, err := Start([]string{"sh", "-c", `trap 'exit 0' HUP; sh -c 'trap "" HUP; while :; do date +%s%N > ` + beat + `; sleep 0.02; done' & wait`},
		t.TempDir(), os.Environ(), 40, 5, func() {})
	if err != nil {
		t.Fatal(err)
	}
	for until := time.Now().Add(2 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(beat); err == nil {
			break
		}
		if time.Now().After(until) {
			t.Fatal("the child did not start")
		}
	}
	// As Close stops it: the program is done well within the grace, the
	// child not.
	p.Stop(2 * time.Second)
	before, _ := os.ReadFile(beat)
	time.Sleep(300 * time.Millisecond)
	if after, _ := os.ReadFile(beat); string(after) != string(before) {
		t.Error("the child still writes after Stop returned")
	}
}
