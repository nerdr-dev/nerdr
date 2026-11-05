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
//
// Inside herdr it goes through herdr instead (`herdr notification show`),
// which delivers it the way the user has set herdr up. herdr already
// notifies when the state nerdr reports for its pane changes — that of the
// tab most in need — so that tab is left to herdr; nerdr covers the tabs
// behind it, whose changes herdr never hears of.

// notifyChange is called when a tab's light has changed; visible is whether
// you are looking at the tab. A notification due is held until the tick's
// report to herdr is made; see flushNotes.
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
	name := m.agentName(t)
	if !m.Cfg.NotifyOn(what) {
		core.Debugf("notify %s: not sent, notify = %q", name, m.Cfg.Notify)
		return
	}
	if t.Notified == t.Light && now.Sub(t.NotifiedAt) < core.NotifyAgain {
		core.Debugf("notify %s: not sent, notified of %s %s ago", name, what, now.Sub(t.NotifiedAt).Round(time.Second))
		return
	}
	t.Notified, t.NotifiedAt = t.Light, now
	// The news is of the tab the agent is in: that is where n goes.
	o := t.Owner()
	if !m.blurred {
		core.Debugf("notify %s: in nerdr: %s", name, body)
		m.inApp = append(m.inApp, name+": "+body)
		m.inAppTabs = append(m.inAppTabs, o)
		return
	}
	m.notes = append(m.notes, core.Note{T: o, Title: "nerdr · " + name, Body: body, Blocked: t.Light == core.StBlocked})
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

// flushNotes sends the notifications gathered this tick. herdrBefore is
// the state nerdr had reported to herdr before it.
func (m *Model) flushNotes(herdrBefore string) {
	if len(m.inApp) > 0 {
		// Several in one tick share the bar.
		m.toastText = strings.Join(m.inApp, " · ") + " · " + m.pk("n") + " goes there"
		m.toastUntil = time.Now().Add(core.InAppFor)
		m.toastTabs, m.toastOf = m.inAppTabs, m.toastText
		m.inApp, m.inAppTabs = nil, nil
	}
	notes := m.notes
	m.notes = nil
	if m.Herdr == nil {
		for _, n := range notes {
			core.Debugf("notify %s: desktop: %s", n.T.Name, n.Body)
			go core.DesktopNotify(n.Title, n.Body)
		}
		return
	}
	_, top := m.Neediest()
	herdrNotified := core.HerdrNotifies(herdrBefore, m.Herdr.State())
	for _, n := range notes {
		if herdrNotified && n.T == top {
			core.Debugf("notify %s: left to herdr", n.T.Name)
			continue // herdr notifies of its own pane's change
		}
		core.Debugf("notify %s: through herdr: %s", n.T.Name, n.Body)
		sound := "done"
		if n.Blocked {
			sound = "request"
		}
		m.Herdr.Notify(n.Title, n.Body, sound)
	}
}
