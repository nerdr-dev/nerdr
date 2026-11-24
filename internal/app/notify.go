package app

import (
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
)

// When nerdr's window does not have focus, an agent that stops to ask
// something, or finishes, could wait unseen for a long time. So nerdr
// sends a desktop notification (notify-send on Linux, osascript on macOS).
// While you are in nerdr, in another tab, it says so in the bar instead:
// the tab bar shows it too, but its light is easy to miss, and a desktop
// notification for something on screen is noise.

// notifyChange is called when a tab's light has changed; visible is whether
// you are looking at the tab. A notification due is held until the end of
// the tick; see flushNotes.
func (m *Model) notifyChange(t *core.Tab, visible bool, now time.Time) {
	if !m.blurred && visible {
		return // you are looking at it
	}
	var what, body string
	switch t.Light {
	case core.StBlocked:
		what, body = "blocked", string(t.Agent)+" needs input"
	case core.StDone:
		what, body = "done", string(t.Agent)+" is done"
	default:
		return
	}
	name := m.machineOf(t).Core.AgentName(t)
	if !m.cfg().NotifyOn(what) {
		core.Debugf("notify %s: not sent, notify = %q", name, m.cfg().Notify)
		return
	}
	tl := m.tl(t)
	if tl.notified == t.Light && now.Sub(tl.notifiedAt) < core.NotifyAgain {
		core.Debugf("notify %s: not sent, notified of %s %s ago", name, what, now.Sub(tl.notifiedAt).Round(time.Second))
		return
	}
	tl.notified, tl.notifiedAt = t.Light, now
	// The news is of the tab the agent is in: that is where n goes.
	o := t.Owner()
	if !m.blurred {
		core.Debugf("notify %s: in nerdr: %s", name, body)
		m.inApp = append(m.inApp, name+": "+body)
		m.inAppTabs = append(m.inAppTabs, o)
		return
	}
	m.notes = append(m.notes, core.Note{T: o, Title: "nerdr · " + name, Body: body})
}

// toastSeen is called for a tab you are looking at: news of it in the bar
// has done its job, and once every tab the news is of has been seen, it
// goes — else it stays over the bar's hints after you went there.
func (m *Model) toastSeen(t *core.Tab) {
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

// flushNotes sends the notifications gathered this tick.
func (m *Model) flushNotes() {
	if len(m.inApp) > 0 {
		// Several in one tick share the bar.
		m.say(catAgents, strings.Join(m.inApp, " · ")+" · "+m.pk("n")+" goes there", core.InAppFor)
		m.toastTabs, m.toastOf = m.inAppTabs, m.toastText
		m.inApp, m.inAppTabs = nil, nil
	}
	notes := m.notes
	m.notes = nil
	for _, n := range notes {
		core.Debugf("notify %s: desktop: %s", n.T.Name, n.Body)
		go core.DesktopNotify(n.Title, n.Body)
	}
}

// Notice shows a message for longer than a toast: for what went wrong
// before the screen was up.
func (m *Model) Notice(s string) { m.say(catWarning, s, 20*time.Second) }
