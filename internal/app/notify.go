package app

import (
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/proc"
)

// When nerdr's window does not have focus, an agent that stops to ask
// something, or finishes, could wait unseen for a long time. So nerdr
// sends a desktop notification (notify-send on Linux, osascript on macOS).
// While you are in nerdr, in another tab, it says so in the bar instead:
// the tab bar shows it too, but its light is easy to miss, and a desktop
// notification for something on screen is noise.
//
// Inside herdr it goes through herdr instead (`herdr notification show`),
// which delivers it the way the user has set herdr up. herdr already
// notifies when the state nerdr reports for its pane changes — that of the
// tab most in need — so that tab is left to herdr; nerdr covers the tabs
// behind it, whose changes herdr never hears of.

// notifyAgain is how soon the same tab may be notified again of the same
// state, so an agent flickering between states does not spam.
const notifyAgain = 20 * time.Second

// inAppFor is how long a tab's news stays in the bar.
const inAppFor = 6 * time.Second

// notifyChange is called when a tab's light has changed; visible is whether
// you are looking at the tab. A notification due is held until the tick's
// report to herdr is made; see flushNotes.
func (m *Model) notifyChange(t *tab, visible bool, now time.Time) {
	if !m.blurred && visible {
		return // you are looking at it
	}
	var what, body string
	switch t.status {
	case stBlocked:
		what, body = "blocked", string(t.Agent)+" needs input"
	case stDone:
		what, body = "done", string(t.Agent)+" is done"
	default:
		return
	}
	name := m.agentName(t)
	if !m.cfg.NotifyOn(what) {
		debugf("notify %s: not sent, notify = %q", name, m.cfg.Notify)
		return
	}
	if t.notified == t.status && now.Sub(t.notifiedAt) < notifyAgain {
		debugf("notify %s: not sent, notified of %s %s ago", name, what, now.Sub(t.notifiedAt).Round(time.Second))
		return
	}
	t.notified, t.notifiedAt = t.status, now
	// The news is of the tab the agent is in: that is where n goes.
	o := t.owner()
	if !m.blurred {
		debugf("notify %s: in nerdr: %s", name, body)
		m.inApp = append(m.inApp, name+": "+body)
		m.inAppTabs = append(m.inAppTabs, o)
		return
	}
	m.notes = append(m.notes, note{t: o, title: "nerdr · " + name, body: body, blocked: t.status == stBlocked})
}

// toastSeen is called for a tab you are looking at: news of it in the bar
// has done its job, and once every tab the news is of has been seen, it
// goes — else it stays over the bar's hints after you went there.
func (m *Model) toastSeen(t *tab) {
	if len(m.toastTabs) == 0 || m.toastText != m.toastOf {
		return
	}
	left := m.toastTabs[:0:0]
	for _, o := range m.toastTabs {
		if o != t {
			left = append(left, o)
		}
	}
	m.toastTabs = left
	if len(left) == 0 {
		m.toastText = ""
	}
}

// note is a notification waiting for the end of the tick.
type note struct {
	t           *tab
	title, body string
	blocked     bool
}

// flushNotes sends the notifications gathered this tick. herdrBefore is
// the state nerdr had reported to herdr before it.
func (m *Model) flushNotes(herdrBefore string) {
	if len(m.inApp) > 0 {
		// Several in one tick share the bar.
		m.toastText = strings.Join(m.inApp, " · ") + " · " + m.pk("n") + " goes there"
		m.toastUntil = time.Now().Add(inAppFor)
		m.toastTabs, m.toastOf = m.inAppTabs, m.toastText
		m.inApp, m.inAppTabs = nil, nil
	}
	notes := m.notes
	m.notes = nil
	if m.herdr == nil {
		for _, n := range notes {
			debugf("notify %s: desktop: %s", n.t.Name, n.body)
			go desktopNotify(n.title, n.body)
		}
		return
	}
	_, top := m.neediest()
	herdrNotified := herdrNotifies(herdrBefore, m.herdr.State())
	for _, n := range notes {
		if herdrNotified && n.t == top {
			debugf("notify %s: left to herdr", n.t.Name)
			continue // herdr notifies of its own pane's change
		}
		debugf("notify %s: through herdr: %s", n.t.Name, n.body)
		sound := "done"
		if n.blocked {
			sound = "request"
		}
		m.herdr.Notify(n.title, n.body, sound)
	}
}

// herdrNotifies reports whether herdr notifies of its pane's state changing
// from before to after, as nerdr reported them: when it comes to ask
// something, and when it goes idle from working or asking (herdr's
// notification_toast_for_state_change). From unknown to idle it says
// nothing, so a tab done then is nerdr's to tell of.
func herdrNotifies(before, after string) bool {
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
func desktopNotify(title, body string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		q := func(s string) string { return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"` }
		cmd = proc.Detach(exec.Command("osascript", "-e", "display notification "+q(body)+" with title "+q(title)))
	default:
		if _, err := exec.LookPath("notify-send"); err != nil {
			debugf("notify-send: not found")
			return
		}
		cmd = proc.Detach(exec.Command("notify-send", "--app-name=nerdr", title, body))
	}
	done := make(chan struct{})
	go func() {
		if out, err := proc.CombinedOutput(cmd); err != nil {
			debugf("%s: %v %s", cmd.Args[0], err, out)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		debugf("%s: no answer in 10s, killed", cmd.Args[0])
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
}
