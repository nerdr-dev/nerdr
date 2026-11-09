package core

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/config"
)

// A client in a process of its own keeps a copy of core's state: the same
// types, filled from the Parts core sends it, so it reads what it shows the
// way it would read core in its own process. What it does to that state is
// not core's: changes go to core as requests, and come back in Parts.
//
// The state goes as JSON, in parts a client may get the newest of alone:
// one per tab, with the agents in it, and one for the rest of core, with
// the order of the tabs. What is core's alone, the programs above all, is
// marked json:"-" and not sent: the client gets screens another way. A tab
// pointing at a tab, as a reviewer at the agent it reviews, goes as the
// tab's id, and the copy points at its own tab of that id again.

// Part is a piece of core's state for a client; its Key says which, and a
// newer part of the same key takes the place of the one before.
type Part struct {
	Key  string
	Body json.RawMessage
}

// The keys of the parts: the rest of core, and the tab of an id.
const (
	PartCore   = "core"
	partTabPre = "tab:"
)

// PartTab is the key of the part of the tab of id.
func PartTab(id string) string { return partTabPre + id }

// tabFields is a Tab as JSON: all of it. A *Tab within it is only a
// reference, by Tab.MarshalJSON.
type tabFields Tab

// coreFields is a Core as JSON, but for its tabs.
type coreFields Core

type tabPart struct {
	// Agents are the tab's own first, then the others in it, reviewers
	// too.
	Agents []*tabFields `json:"agents"`
}

type corePart struct {
	*coreFields
	Order []string `json:"$order"`
	// Agents are the ids of every agent in every tab: one a tab's part
	// no longer holds is kept by the copy until it is not here either,
	// as it may have moved to a tab whose part is still to come.
	Agents []string `json:"$agents"`
	// Programs are the programs core runs, which a copy has no
	// term.Pane of; see ProgramState.
	Programs []ProgramState `json:"$programs"`
	// Config is core's config: a copy takes core's own settings from it
	// (config.ScopeCore), and keeps the client's, which are the client's
	// to read where it runs.
	Config *config.Config `json:"$config,omitempty"`
}

// ProgramState is a program core runs, as a copy of its state knows it:
// where it runs, the agent whose it is if any, and whether it has exited.
type ProgramState struct {
	Ref    ProgramRef
	Agent  string `json:",omitempty"`
	Exited bool   `json:",omitempty"`
}

// programStates are the programs core runs, in the order of the tabs and
// of the plugins' screens. Mu is held.
func (m *Core) programStates() []ProgramState {
	var out []ProgramState
	for _, t := range m.Tabs {
		for _, p := range t.Panes() {
			tp := p.Term()
			if tp == nil {
				continue
			}
			ps := ProgramState{Ref: ProgramRef{Pane: m.PaneRef(p)}}
			if p.Kind != PaneShell && p.A != nil {
				ps.Agent = m.Ref(p.A).Tab
			}
			ps.Exited, _, _ = tp.Exited()
			out = append(out, ps)
		}
	}
	keys := slices.Sorted(maps.Keys(m.PluginUIs))
	for _, k := range keys {
		if u := m.PluginUIs[k]; u != nil && u.Pane != nil {
			exited, _, _ := u.Pane.Exited()
			out = append(out, ProgramState{Ref: ProgramRef{Plugin: k}, Exited: exited})
		}
	}
	return out
}

type tabRef struct {
	Ref string `json:"$ref"`
}

// MarshalJSON writes a tab met within core's state as its id alone: the
// tab itself goes in a part of its own. See Part.
func (t *Tab) MarshalJSON() ([]byte, error) { return json.Marshal(tabRef{Ref: t.ID}) }

// UnmarshalJSON reads a tab's id, for a Mirror to point at its own tab of
// that id.
func (t *Tab) UnmarshalJSON(b []byte) error {
	var r tabRef
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	*t = Tab{}
	t.ID, t.wireRef = r.Ref, true
	return nil
}

// Sent is what a client has been sent of core's state, to send it only
// what changed. The zero Sent has been sent nothing.
type Sent struct {
	sums map[string][32]byte
}

// Reset forgets what was sent: the next Parts are all of it.
func (s *Sent) Reset() { s.sums = nil }

// Parts are the parts of core's state that changed since s, and s marked
// as sent them. A part that cannot be encoded is left out, and its error
// returned with the rest. Mu is held.
func (m *Core) Parts(s *Sent) ([]Part, error) {
	if s.sums == nil {
		s.sums = map[string][32]byte{}
	}
	var out []Part
	add := func(key string, v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		sum := sha256.Sum256(b)
		if old, ok := s.sums[key]; ok && old == sum {
			return nil
		}
		s.sums[key] = sum
		out = append(out, Part{Key: key, Body: b})
		return nil
	}
	m.NoteUnseen() // a tab closed since may leave one
	order := make([]string, 0, len(m.Tabs))
	var ids []string
	var errs []error
	live := map[string]bool{PartCore: true}
	for _, t := range m.Tabs {
		var p tabPart
		for _, pane := range t.Panes() {
			m.PaneRef(pane) // an id to be known by
		}
		for _, a := range agentsOf(t) {
			m.Ref(a) // an id to be known by
			p.Agents = append(p.Agents, (*tabFields)(a))
			ids = append(ids, a.ID)
		}
		order = append(order, t.ID)
		live[PartTab(t.ID)] = true
		// A tab that cannot be sent is left out, not every tab with it.
		errs = append(errs, add(PartTab(t.ID), p))
	}
	errs = append(errs, add(PartCore, corePart{coreFields: (*coreFields)(m), Order: order, Agents: ids, Programs: m.programStates(), Config: &m.Cfg}))
	for k := range s.sums {
		if !live[k] {
			delete(s.sums, k) // closed: should it be back, it goes again
		}
	}
	return out, errors.Join(errs...)
}

// agentsOf is tab t and the agents in it, reviewers too, each once: also
// a reviewer still starting, in a pane but not yet at its seat.
func agentsOf(t *Tab) []*Tab {
	out := t.AgentsIn()
	for _, p := range t.Panes() {
		if p.A != nil && !slices.Contains(out, p.A) {
			out = append(out, p.A)
		}
	}
	return out
}

// Mirror keeps a copy of a core's state in C, from the Parts that core
// sends. A tab or agent stays the same *Tab from one part to the next, so a
// client may keep what it shows of one by its pointer.
type Mirror struct {
	C *Core

	tabs   map[string]*Tab     // the tabs by id
	agents map[string]*Tab     // every agent by id, the tabs too
	in     map[string][]string // the ids of the agents in each tab
	owner  map[string]string   // the tab each agent is in, by id
	order  []string
	listed map[string]bool // the agents core's part says there are
	progs  []ProgramState  // the programs core's part says run
	// started is set when a program came that the copy did not know.
	started bool
}

// NewMirror is a Mirror of nothing yet, into c.
func NewMirror(c *Core) *Mirror {
	return &Mirror{C: c, tabs: map[string]*Tab{}, agents: map[string]*Tab{}, in: map[string][]string{}, owner: map[string]string{}, listed: map[string]bool{}}
}

// Apply takes part p into the copy. C.Mu is held.
//
// The copy's Gen is its own: a change taken in counts in it, as the
// client's own changes do (see Core.Gen), and core's is not taken, so it
// never goes back to a number already drawn at.
func (r *Mirror) Apply(p Part) error {
	gen := r.C.Gen
	defer func() { r.C.Gen = gen + 1 }()
	switch {
	case p.Key == PartCore:
		if err := r.applyCore(p.Body); err != nil {
			return err
		}
	case strings.HasPrefix(p.Key, partTabPre):
		if err := r.applyTab(strings.TrimPrefix(p.Key, partTabPre), p.Body); err != nil {
			return err
		}
	default:
		return fmt.Errorf("no part %q", p.Key)
	}
	// An agent no tab holds and core does not list is gone; one core
	// still lists has moved to a tab whose part is to come, and keeps
	// its pointer for it.
	for id := range r.agents {
		if _, held := r.owner[id]; !held && !r.listed[id] {
			delete(r.agents, id)
		}
	}
	tabs := make([]*Tab, 0, len(r.order))
	for _, id := range r.order {
		if t := r.tabs[id]; t != nil {
			tabs = append(tabs, t)
		}
	}
	r.C.Tabs = tabs
	r.programs()
	relink(reflect.ValueOf(r.C).Elem(), r.agents, map[uintptr]bool{})
	for _, a := range r.agents {
		relink(reflect.ValueOf(a).Elem(), r.agents, map[uintptr]bool{})
	}
	return nil
}

func (r *Mirror) applyTab(id string, body []byte) error {
	var p tabPart
	if err := json.Unmarshal(body, &p); err != nil {
		return err
	}
	if len(p.Agents) == 0 || p.Agents[0].ID != id {
		return fmt.Errorf("tab part %s does not start with the tab", id)
	}
	var ids []string
	for _, f := range p.Agents {
		a := r.agents[f.ID]
		if a == nil {
			a = (*Tab)(f)
			r.agents[f.ID] = a
		} else {
			panes := a.PaneList
			*a = Tab(*f)
			keepPanes(a, panes)
		}
		ids = append(ids, f.ID)
		r.owner[f.ID] = id
	}
	for _, old := range r.in[id] {
		if !slices.Contains(ids, old) && r.owner[old] == id {
			// Gone, or moved to a tab whose part is to come.
			delete(r.owner, old)
		}
	}
	r.in[id] = ids
	r.tabs[id] = r.agents[id]
	return nil
}

// keepPanes has t's panes that were in old, by id, be the same panes as
// before, taking in what changed: a client keeps how it shows each pane,
// and which has its keyboard, by the pane.
func keepPanes(t *Tab, old []*PaneTab) {
	for i, p := range t.PaneList {
		if p.ID == "" {
			continue
		}
		for _, o := range old {
			if o.ID == p.ID {
				*o = *p
				t.PaneList[i] = o
				break
			}
		}
	}
}

func (r *Mirror) applyCore(body []byte) error {
	fresh := &Core{}
	p := corePart{coreFields: (*coreFields)(fresh)}
	if err := json.Unmarshal(body, &p); err != nil {
		return err
	}
	copyWire(reflect.ValueOf(r.C).Elem(), reflect.ValueOf(fresh).Elem())
	if p.Config != nil {
		r.C.Cfg = config.Merge(*p.Config, r.C.Cfg)
	}
	r.order, r.progs = p.Order, p.Programs
	r.listed = map[string]bool{}
	for _, id := range p.Agents {
		r.listed[id] = true
	}
	for id := range r.tabs {
		if !slices.Contains(p.Order, id) {
			for _, a := range r.in[id] {
				if r.owner[a] == id {
					delete(r.owner, a)
				}
			}
			delete(r.in, id)
			delete(r.tabs, id)
		}
	}
	return nil
}

// programs marks in the copy the programs core runs, which it has no
// term.Pane of: the agents', and the plugins' screens; see Core.Runs.
func (r *Mirror) programs() {
	known := r.C.programs
	r.C.programs = map[ProgramRef]bool{}
	for _, a := range r.agents {
		a.remote = remoteNone
	}
	for _, ps := range r.progs {
		r.C.programs[ps.Ref] = true
		if !known[ps.Ref] {
			r.started = true
		}
		if a := r.agents[ps.Agent]; a != nil && ps.Agent != "" {
			a.remote = remoteRuns
			if ps.Exited {
				a.remote = remoteExited
			}
		}
		if ps.Ref.Plugin != "" {
			if u := r.C.PluginUIs[ps.Ref.Plugin]; u != nil {
				u.remoteRuns = !ps.Exited
			}
		}
	}
}

// copyWire sets the fields of dst that go as JSON to those of src: not
// the lock, nor the client, nor anything else core keeps to itself.
func copyWire(dst, src reflect.Value) {
	ty := dst.Type()
	for i := range ty.NumField() {
		f := ty.Field(i)
		if !f.IsExported() || f.Tag.Get("json") == "-" {
			continue
		}
		dst.Field(i).Set(src.Field(i))
	}
}

var tabType = reflect.TypeFor[Tab]()

// relink points every reference to a tab within v, as Tab.UnmarshalJSON
// left it, at the agent of its id in agents. One whose agent is not there
// yet, its tab's part still to come, stays a reference, a tab of an id
// alone, until it is. It does not go into the agents, which are relinked
// each on their own.
func relink(v reflect.Value, agents map[string]*Tab, seen map[uintptr]bool) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		if v.Type().Elem() == tabType {
			if t := v.Interface().(*Tab); t.wireRef && v.CanSet() {
				if a := agents[t.ID]; a != nil {
					v.Set(reflect.ValueOf(a))
				}
			}
			return
		}
		if seen[v.Pointer()] {
			return
		}
		seen[v.Pointer()] = true
		relink(v.Elem(), agents, seen)
	case reflect.Struct:
		ty := v.Type()
		for i := range ty.NumField() {
			f := ty.Field(i)
			if !f.IsExported() || f.Tag.Get("json") == "-" {
				continue
			}
			relink(v.Field(i), agents, seen)
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			relink(v.Index(i), agents, seen)
		}
	case reflect.Map:
		if v.IsNil() || !holdsTabs(v.Type().Elem(), map[reflect.Type]bool{}) {
			return
		}
		iter := v.MapRange()
		for iter.Next() {
			e := reflect.New(v.Type().Elem()).Elem()
			e.Set(iter.Value())
			relink(e, agents, seen)
			v.SetMapIndex(iter.Key(), e)
		}
	}
}

// holdsTabs reports whether a value of type ty may point at a tab, through
// what goes as JSON.
func holdsTabs(ty reflect.Type, seen map[reflect.Type]bool) bool {
	if seen[ty] {
		return false
	}
	seen[ty] = true
	switch ty.Kind() {
	case reflect.Pointer:
		return ty.Elem() == tabType || holdsTabs(ty.Elem(), seen)
	case reflect.Slice, reflect.Array, reflect.Map:
		return holdsTabs(ty.Elem(), seen)
	case reflect.Struct:
		for i := range ty.NumField() {
			f := ty.Field(i)
			if f.IsExported() && f.Tag.Get("json") != "-" && holdsTabs(f.Type, seen) {
				return true
			}
		}
	}
	return false
}
