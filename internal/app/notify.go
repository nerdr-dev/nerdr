package app

import (
	"slices"
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
// the tick; see flushNotes. News of an agent done waits longer, for the
// tab's work to be over: see holdDone.
func (m *Model) notifyChange(t *core.Tab, visible bool, now time.Time) {
	if !m.blurred && visible {
		return // you are looking at it
	}
	if t.Light == core.StDone {
		m.holdDone(t, now)
		return
	}
	m.tellChange(t, now)
}

// holdDone holds news of agent t done until the work in its tab is over.
// An agent that hands its work on, as to a reviewer or another agent in
// the tab, is done before what it handed on is seen at work: the review
// is started after its turn, and a message typed in to another agent is
// taken up a moment later. So the news waits HoldDone at least, and then
// for as long as the work goes on, and is told once it is over, however
// it ends: the others done, exited or closed. A review's news takes its
// place; see notifyReview.
func (m *Model) holdDone(t *core.Tab, now time.Time) {
	if _, ok := m.heldDone[t]; ok {
		return
	}
	if m.heldDone == nil {
		m.heldDone = map[*core.Tab]time.Time{}
	}
	core.Debugf("notify %s: done, held for the work in the tab", m.machineOf(t).Core.AgentName(t))
	m.heldDone[t] = now
}

// releaseDone tells of the agents held done whose tab's work is over, and
// lets go of those no longer to be told of: gone, at work again, or seen.
func (m *Model) releaseDone(now time.Time) {
	if len(m.heldDone) == 0 {
		return
	}
	live := m.fleet.AllAgents()
	for t, at := range m.heldDone {
		switch {
		case !slices.Contains(live, t) || t.Light != core.StDone:
			delete(m.heldDone, t)
		case !m.blurred && m.agentVisible(t):
			delete(m.heldDone, t) // you are looking at it
		case now.Sub(at) < core.HoldDone:
		case t.WorkGoesOn():
		default:
			delete(m.heldDone, t)
			m.tellChange(t, now)
		}
	}
}

// dropHeldDone lets go of the news held of agents in tab o done: news of
// the tab's review has taken its place.
func (m *Model) dropHeldDone(o *core.Tab) {
	for t := range m.heldDone {
		if t.Owner() == o {
			delete(m.heldDone, t)
		}
	}
}

// tellChange tells of t's light: in the bar while you are in nerdr, on the
// desktop when you are not.
func (m *Model) tellChange(t *core.Tab, now time.Time) {
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
