package core

import (
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/proc"
)

// NotifyAgain is how soon the same tab may be notified again of the same
// state, so an agent flickering between states does not spam.
const NotifyAgain = 20 * time.Second

// InAppFor is how long a tab's news stays in the bar.
const InAppFor = 6 * time.Second

// Note is a notification waiting for the end of the tick.
type Note struct {
	T           *Tab
	Title, Body string
	Blocked     bool
}

// HerdrNotifies reports whether herdr notifies of its pane's state changing
// from before to after, as nerdr reported them: when it comes to ask
// something, and when it goes idle from working or asking (herdr's
// notification_toast_for_state_change). From unknown to idle it says
// nothing, so a tab done then is nerdr's to tell of.
func HerdrNotifies(before, after string) bool {
	switch {
	case before == after:
		return false
	case after == "blocked":
		return true
	case after == "idle":
		return before == "working" || before == "blocked"
	}
	return false
}

// desktopNotify shows a notification, if the system has a way to.
func DesktopNotify(title, body string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		q := func(s string) string { return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"` }
		cmd = proc.Detach(exec.Command("osascript", "-e", "display notification "+q(body)+" with title "+q(title)))
	default:
		if _, err := exec.LookPath("notify-send"); err != nil {
			Debugf("notify-send: not found")
			return
		}
		cmd = proc.Detach(exec.Command("notify-send", "--app-name=nerdr", title, body))
	}
	done := make(chan struct{})
	go func() {
		if out, err := proc.CombinedOutput(cmd); err != nil {
			Debugf("%s: %v %s", cmd.Args[0], err, out)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		Debugf("%s: no answer in 10s, killed", cmd.Args[0])
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
}
