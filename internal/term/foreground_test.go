package term

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestRerun: a command running in a shell makes its terminal busy; Rerun
// stops it, one that does not stop for ctrl+c too, and runs another in
// its place.
func TestRerun(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	p, err := Start([]string{"bash", "--norc", "--noprofile", "-i"}, t.TempDir(), append(os.Environ(), "PS1=$ "), 60, 10, func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		for end := time.Now().Add(5 * time.Second); !ok(); {
			if time.Now().After(end) {
				t.Fatalf("%s; the screen: %q", what, p.Text(10))
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	waitFor("the shell should start", func() bool { return strings.Contains(p.Text(10), "$") })
	if p.Busy() {
		t.Fatal("a shell at its prompt is not busy")
	}
	p.Write([]byte("trap '' INT; sleep 30\r"))
	waitFor("sleep should make it busy", p.Busy)
	defer func(w time.Duration) { rerunWait = w }(rerunWait)
	rerunWait = 500 * time.Millisecond
	if !p.Rerun("echo again-$((40+2))") {
		t.Fatal("Rerun should get the shell back")
	}
	waitFor("the new command should run", func() bool { return strings.Contains(p.Text(10), "again-42") })
}
