package core

import (
	"sync"
	"time"

	"github.com/nerdr-dev/nerdr/internal/term"
)

// Who shows a program, and so what size it gets. Each client that shows a
// pane's program claims a size for it: the TUI by the size it lays the
// pane out at (ResizeProgram), a connection to the API by its view op. The
// program gets the size of the claim whose client was active in it last:
// that opened it, typed, clicked or focused in it, or changed the size it
// shows it at. The others show the same screen, at a size not their own.
// When that client goes, the one active before it takes over; with no
// claim left, the program keeps the size it has.
//
// A claim of the API's runs out when it is not renewed in time: a phone
// locked, or a machine asleep, says nothing, and its connection can hang a
// long while. The TUI's lasts while it is connected.

// ClientViewer is the TUI's name among the viewers when it says nothing
// of which TUI it is; each connected names its own (see AttachClient), as
// each API connection does.
const ClientViewer = "client"

// ViewTTL is how long an API connection's claim lasts unless renewed.
const ViewTTL = 60 * time.Second

// The sizes a view may claim; one outside is brought within.
const (
	MinViewCols, MaxViewCols = 20, 500
	MinViewRows, MaxViewRows = 5, 300
)

// The size a program starts at that no client has said how to show: see
// Core.DefaultSize.
const (
	DefaultCols = 120
	DefaultRows = 40
)

// ClampView is w×h brought within the sizes a view may claim.
func ClampView(w, h int) (int, int) {
	return min(max(w, MinViewCols), MaxViewCols), min(max(h, MinViewRows), MaxViewRows)
}

// viewClaim is a viewer's claim on a program's size: active orders the
// claims by when their viewer was last active there, the latest first;
// until is when it runs out, never when zero.
type viewClaim struct {
	w, h   int
	active uint64
	until  time.Time
}

func (c *viewClaim) live(now time.Time) bool { return c.until.IsZero() || now.Before(c.until) }

// Views are the claims on the programs' sizes, by pane ID and viewer. They
// have a lock of their own, for the TUI's keys to be counted as they come,
// away from the loop.
type Views struct {
	mu    sync.Mutex
	panes map[string]map[string]*viewClaim
	seq   uint64
	// gone are the viewers that have gone while what they said may still
	// be on its way: it counts no more, and what they typed does not go
	// in. Each is forgotten once nothing more of its can come.
	gone map[string]bool
}

// leader is the live claim on pane, viewer active last, and its viewer;
// nil when there is none. v.mu is held.
func (v *Views) leader(pane string, now time.Time) (string, *viewClaim) {
	var who string
	var best *viewClaim
	for w, c := range v.panes[pane] {
		if c.live(now) && (best == nil || c.active > best.active) {
			who, best = w, c
		}
	}
	return who, best
}

// Show has who show pane at w×h, for ttl (for as long as who is there when
// 0), and reports whether its claim leads now. Its first claim there, or
// one at another size, counts as who being active there; one renewed as
// it was does not.
func (v *Views) Show(pane PaneRef, who string, w, h int, ttl time.Duration, now time.Time) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.show(pane, who, w, h, ttl, now, false)
}

// sized is a program, as far as its size goes.
type sized interface {
	Size() (int, int)
	Resize(w, h int)
}

// ShowApply is Show, giving prog the size that leads then, in one go: no
// other viewer's size comes between. prog may be nil. Unseen is set when
// who does not show the program on screen now, as the TUI sizing a tab in
// the background: its claim is kept, but it is not who being active there.
func (v *Views) ShowApply(pane PaneRef, who string, w, h int, ttl time.Duration, now time.Time, prog sized, unseen bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.show(pane, who, w, h, ttl, now, unseen)
	v.apply(pane, prog, now)
}

// Act is who active in pane: if it shows it, prog gets the size that
// leads then; and then do, what who does there, if not nil, in the same
// go, so no other viewer's size comes between. do must not wait.
func (v *Views) Act(pane PaneRef, who string, now time.Time, prog sized, do func()) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.gone[who] {
		return // what it did, on its way when it went, does not go in
	}
	if v.touch(pane, who, now) {
		v.apply(pane, prog, now)
	}
	if do != nil {
		do()
	}
}

// Apply gives prog the size that leads on pane, if any does and it is
// another than prog has: under the lock, so no size chosen before it can
// land after it.
func (v *Views) Apply(pane PaneRef, prog sized, now time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.apply(pane, prog, now)
}

// apply is Apply, v.mu held.
func (v *Views) apply(pane PaneRef, prog sized, now time.Time) {
	if prog == nil {
		return
	}
	_, c := v.leader(pane.Pane, now)
	if c == nil {
		return
	}
	if w, h := prog.Size(); w == c.w && h == c.h {
		return
	}
	prog.Resize(c.w, c.h)
}

// show is Show, v.mu held.
func (v *Views) show(pane PaneRef, who string, w, h int, ttl time.Duration, now time.Time, unseen bool) bool {
	if v.gone[who] {
		return false
	}
	if v.panes == nil {
		v.panes = map[string]map[string]*viewClaim{}
	}
	cs := v.panes[pane.Pane]
	if cs == nil {
		cs = map[string]*viewClaim{}
		v.panes[pane.Pane] = cs
	}
	c := cs[who]
	if c == nil {
		c = &viewClaim{}
		cs[who] = c
		if !unseen {
			v.seq++
			c.active = v.seq
		}
	} else if (!c.live(now) || c.w != w || c.h != h) && !unseen {
		v.seq++
		c.active = v.seq
	}
	c.w, c.h = w, h
	c.until = time.Time{}
	if ttl > 0 {
		c.until = now.Add(ttl)
	}
	lead, _ := v.leader(pane.Pane, now)
	return lead == who
}

// Touch counts who as active in pane now, if it shows it, and reports
// whether it does.
func (v *Views) Touch(pane PaneRef, who string, now time.Time) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.touch(pane, who, now)
}

// touch is Touch, v.mu held. Its size is the program's then, even when it
// led already: one that led after it may have run out since.
func (v *Views) touch(pane PaneRef, who string, now time.Time) bool {
	c := v.panes[pane.Pane][who]
	if c == nil || !c.live(now) {
		return false
	}
	v.seq++
	c.active = v.seq
	return true
}

// By does do, what who does, unless who has gone: in one go, so a viewer
// gone meanwhile has nothing go in. do must not wait.
func (v *Views) By(who string, do func()) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.gone[who] {
		do()
	}
}

// Gone reports whether who has gone, what it says counting no more.
func (v *Views) Gone(who string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.gone[who]
}

// Forget lets go of who gone: nothing more of its can come.
func (v *Views) Forget(who string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.gone, who)
}

// Shows reports whether who has a live claim on pane.
func (v *Views) Shows(pane PaneRef, who string, now time.Time) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	c := v.panes[pane.Pane][who]
	return c != nil && c.live(now)
}

// Drop takes who's claim on pane away, and reports whether it had one.
func (v *Views) Drop(pane PaneRef, who string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, ok := v.panes[pane.Pane][who]; !ok {
		return false
	}
	v.drop(pane.Pane, who)
	return true
}

// drop is Drop, v.mu held.
func (v *Views) drop(pane, who string) {
	delete(v.panes[pane], who)
	if len(v.panes[pane]) == 0 {
		delete(v.panes, pane)
	}
}

// DropViewer takes every claim of who away: it has gone, and what it says
// from now on counts for nothing. It is the panes it showed.
func (v *Views) DropViewer(who string) []PaneRef {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.gone == nil {
		v.gone = map[string]bool{}
	}
	v.gone[who] = true
	var out []PaneRef
	for pane, cs := range v.panes {
		if _, ok := cs[who]; ok {
			v.drop(pane, who)
			out = append(out, PaneRef{Pane: pane})
		}
	}
	return out
}

// Expire takes away the claims run out by now, and those on panes keep
// does not keep, and is the panes left with claims whose leader may have
// changed.
func (v *Views) Expire(now time.Time, keep func(pane string) bool) []PaneRef {
	v.mu.Lock()
	defer v.mu.Unlock()
	var out []PaneRef
	for pane, cs := range v.panes {
		if keep != nil && !keep(pane) {
			delete(v.panes, pane)
			continue
		}
		gone := false
		for who, c := range cs {
			if !c.live(now) {
				v.drop(pane, who)
				gone = true
			}
		}
		if gone && v.panes[pane] != nil {
			out = append(out, PaneRef{Pane: pane})
		}
	}
	return out
}

// Size is the size pane's program is to have, by the claim that leads;
// false when there is none.
func (v *Views) Size(pane PaneRef, now time.Time) (int, int, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, c := v.leader(pane.Pane, now); c != nil {
		return c.w, c.h, true
	}
	return 0, 0, false
}

// DefaultSize is the size a program starts at that no client has said how
// to show: the config's headless_cols × headless_rows.
func (m *Core) DefaultSize() (int, int) {
	w, h := m.Cfg.HeadlessCols, m.Cfg.HeadlessRows
	if w <= 0 {
		w = DefaultCols
	}
	if h <= 0 {
		h = DefaultRows
	}
	return ClampView(w, h)
}

// startSize is the size a program starting in pane is to have: what its
// views claim, else w×h, the client's size for it, else the default.
func (m *Core) startSize(pane PaneRef, w, h int) (int, int) {
	if pane.Pane != "" {
		if vw, vh, ok := m.Views.Size(pane, time.Now()); ok {
			return vw, vh
		}
	}
	if w > 0 && h > 0 {
		return w, h
	}
	return m.DefaultSize()
}

// applyView gives pane's program the size its views claim, if any does and
// it is another than the program has. Mu is held.
func (m *Core) applyView(pane PaneRef) {
	if prog := m.paneProgram(pane); prog != nil {
		m.Views.Apply(pane, prog, time.Now())
	}
}

// paneProgram is the program in pane, nil when none runs there. Mu is
// held.
func (m *Core) paneProgram(pane PaneRef) Program {
	if _, p := TabList(m.Tabs).FindPane(pane.Pane); p != nil {
		pane = m.PaneRef(p)
	}
	return m.Program(ProgramRef{Pane: pane})
}

// ExpireViews takes away the claims run out, and those on panes gone, and
// gives the programs whose leader changed their size. Mu is held.
func (m *Core) ExpireViews(now time.Time) {
	for _, p := range m.Views.Expire(now, func(pane string) bool {
		_, p := TabList(m.Tabs).FindPane(pane)
		return p != nil
	}) {
		m.applyView(p)
	}
}

// viewerGone takes who's claims away, what it still says counting no
// more, and gives the programs it showed the size of the claims left. Mu
// is held.
func (m *Core) viewerGone(who string) {
	for _, p := range m.Views.DropViewer(who) {
		m.applyView(p)
	}
}

// clientShows is the TUI who sizing program ref, prog, at w×h, on screen
// or, unseen, in a tab in the background: prog gets that size, or that of
// a viewer active there since.
func clientShows(v *Views, who string, ref ProgramRef, w, h int, prog sized, unseen bool) {
	if v == nil || ref.Plugin != "" || ref.Pane.Pane == "" {
		prog.Resize(w, h)
		return
	}
	v.ShowApply(ref.Pane, who, w, h, 0, time.Now(), prog, unseen)
}

// clientActive reports the program r has the user active in, in the TUI:
// typing, clicking, focusing it or giving it the keyboard. ok is false for
// any other request.
func clientActive(r Request) (ProgramRef, bool) {
	switch q := r.(type) {
	case Type:
		return q.Ref, q.User
	case Mouse:
		return q.Ref, q.User && term.IsClick(q.Ev)
	case FocusProgram:
		return q.Ref, q.In
	case KeysTo:
		return q.Ref, true
	}
	return ProgramRef{}, false
}

func init() { registerRequests(clientDid{}) }

// clientDid is r, what the TUI who did, done on the loop, its size and
// activity counted with it, in one go (see Views.Act): act is set when r
// is the user active in its program, unseen when r sizes a program the
// TUI does not show on screen. Core's own: as it comes from the wire, it
// is nothing.
type clientDid struct {
	who    string
	r      Request
	act    bool
	unseen bool
}

func (q clientDid) Do(m *Core) Cmd {
	if q.r == nil || m.Views.Gone(q.who) {
		return nil // a client gone: what it did, on its way, does not go in
	}
	if r, ok := q.r.(ResizeProgram); ok {
		if p := m.Program(r.Ref); p != nil {
			clientShows(&m.Views, q.who, r.Ref, r.W, r.H, p, q.unseen)
		}
		return nil
	}
	ref, _ := clientActive(q.r)
	if !q.act || ref.Plugin != "" {
		return q.r.Do(m)
	}
	var cmd Cmd
	m.Views.Act(ref.Pane, q.who, time.Now(), m.paneProgram(ref.Pane), func() { cmd = q.r.Do(m) })
	return cmd
}
