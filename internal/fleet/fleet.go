// Package fleet holds the cores a client shows, a machine's each: a copy
// of the state of a core in another process, kept by its connection, or
// core itself in the client's process. The cores do not know of each
// other; the client reads what it shows of them all here, the tabs of one
// machine after another's, and has each machine's core do what is asked of
// that machine's tabs.
package fleet

import (
	"reflect"
	"slices"
	"sync/atomic"

	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/core"
)

// Machine is one core the client shows, and the client's connection to it.
type Machine struct {
	// Name is the machine's among the client's, as TabRef.Core names it:
	// "" for this one.
	Name string
	// Core is a copy of the core's state, kept by Conn; or core itself,
	// run in the client's process, when Conn is nil.
	Core core.Core
	// Conn is the connection to a core in another process, nil when it
	// runs in the client's.
	Conn *core.CoreConn
	// Screens are copies of the screens of the core's programs, sent as
	// rows over Conn; nil when they are drawn from core's own.
	Screens *core.ScreenClient
	// Host is the machine the core runs on, as ssh names it, when it is
	// not this one: "" for a core here, in this process or another.
	Host string
	// Version is the version of nerdr the core said it is as the client
	// last connected to it, kept while the client is without it; "" when
	// it never connected, or the core runs in the client's process.
	Version string
	// Redial connects to the core again, should the connection close; nil
	// when there is no way to. Away is set while the client is without it.
	Redial func() (*attach.Conn, error)
	Away   bool
	// Err is why the last try to connect again failed, while Away.
	Err string
	// Tries counts the tries to connect again: one under way gives up,
	// and lets go of what it connected, once it is no longer the last.
	// Read by the tries' goroutines.
	Tries atomic.Int64
	// Gone is why the client is without the core for good, "" while it
	// is not: the core ended and did not come back, or another client
	// took it over. Its tabs are shown greyed, until the user connects to
	// it again.
	Gone string
	// Said is what the client last told the core it shows, and Pending
	// what the core told the client before the program started.
	Said    core.ClientState
	Pending []core.Event
}

// Told is what the client is given of a machine's core: the message, and
// the machine it is of. See Machine.Tell.
type Told struct {
	Machine string
	Msg     any
}

// Tell wraps tell, which gives the client what comes of a machine's core,
// so that what it gives says the machine it is of.
func (mc *Machine) Tell(tell func(any)) func(any) {
	return func(msg any) { tell(Told{Machine: mc.Name, Msg: msg}) }
}

// Fleet is the machines a client shows, in the order their tabs are. The
// zero Fleet is one of a machine, this one, with a zero core: a client's
// in a test.
type Fleet struct {
	machines []*Machine
}

// New is a fleet of ms, in that order.
func New(ms ...*Machine) Fleet { return Fleet{machines: ms} }

// Add puts mc last among the fleet's machines; mc.Name is not one of
// theirs. The fleet is not locked (Lock), or mc is too.
func (f *Fleet) Add(mc *Machine) { f.machines = append(f.Machines(), mc) }

// Remove takes mc out of the fleet: its tabs are no longer shown. What
// Lock locked of it stays locked.
func (f *Fleet) Remove(mc *Machine) {
	f.machines = slices.DeleteFunc(f.Machines(), func(o *Machine) bool { return o == mc })
}

// Machines are the fleet's machines, in order.
func (f *Fleet) Machines() []*Machine {
	if len(f.machines) == 0 {
		f.machines = []*Machine{{}}
	}
	return f.machines
}

// Home is the machine for what is of no tab: this one, or the first when
// none of the fleet's is.
func (f *Fleet) Home() *Machine {
	if mc := f.Named(""); mc != nil {
		return mc
	}
	return f.Machines()[0]
}

// Named is the machine called name, nil when none is.
func (f *Fleet) Named(name string) *Machine {
	for _, mc := range f.Machines() {
		if mc.Name == name {
			return mc
		}
	}
	return nil
}

// Machine is the machine called name, Home when none is: that of a
// TabRef's or PaneRef's Core.
func (f *Fleet) Machine(name string) *Machine {
	if mc := f.Named(name); mc != nil {
		return mc
	}
	return f.Home()
}

// Of is the machine whose tab, or agent in one, t is; nil when none's.
// The one machine of a fleet of one is every tab's, as it is that of a
// tab not yet one of its own.
func (f *Fleet) Of(t *core.Tab) *Machine {
	if ms := f.Machines(); len(ms) == 1 {
		return ms[0]
	}
	for _, mc := range f.Machines() {
		if core.TabList(mc.Core.Tabs).Holds(t) {
			return mc
		}
	}
	return nil
}

// OfPane is the machine whose tab p is in; nil when none's, as Of.
func (f *Fleet) OfPane(p *core.PaneTab) *Machine {
	if ms := f.Machines(); len(ms) == 1 {
		return ms[0]
	}
	for _, mc := range f.Machines() {
		if core.TabList(mc.Core.Tabs).HoldsPane(p) {
			return mc
		}
	}
	return nil
}

// For is the machine that is to do r: that of the first tab or pane it
// names, else Home.
func (f *Fleet) For(r core.Request) *Machine {
	if len(f.Machines()) > 1 {
		if mc := f.Naming(r); mc != nil {
			return mc
		}
	}
	return f.Home()
}

// Naming is the machine of the first tab or pane r names, nil when it
// names none.
func (f *Fleet) Naming(r core.Request) *Machine {
	if len(f.Machines()) == 1 {
		if _, ok := refCore(reflect.ValueOf(r)); ok {
			return f.Machines()[0]
		}
		return nil
	}
	if name, ok := refCore(reflect.ValueOf(r)); ok {
		return f.Machine(name)
	}
	return nil
}

var (
	tabRef  = reflect.TypeFor[core.TabRef]()
	paneRef = reflect.TypeFor[core.PaneRef]()
)

// refCore is the core named by the first TabRef or PaneRef in v that names
// one, looked for in v's fields and theirs.
func refCore(v reflect.Value) (string, bool) {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return "", false
		}
		v = v.Elem()
	}
	switch v.Type() {
	case tabRef:
		r := v.Interface().(core.TabRef)
		return r.Core, r != core.TabRef{}
	case paneRef:
		r := v.Interface().(core.PaneRef)
		return r.Core, r != core.PaneRef{}
	}
	if v.Kind() != reflect.Struct {
		return "", false
	}
	for i := range v.NumField() {
		if !v.Type().Field(i).IsExported() {
			continue
		}
		if name, ok := refCore(v.Field(i)); ok {
			return name, true
		}
	}
	return "", false
}

// Lock locks every machine's core, in the fleet's order, as the client
// does while it reads or changes its copies.
func (f *Fleet) Lock() {
	for _, mc := range f.Machines() {
		mc.Core.Mu.Lock()
	}
}

// Unlock unlocks what Lock locked.
func (f *Fleet) Unlock() {
	ms := f.Machines()
	for i := len(ms) - 1; i >= 0; i-- {
		ms[i].Core.Mu.Unlock()
	}
}

// Gen counts the changes that can change what the client draws: each
// machine's (core.Core.Gen), the client's own among them (Bump).
func (f *Fleet) Gen() uint64 {
	var n uint64
	for _, mc := range f.Machines() {
		n += mc.Core.Gen
	}
	return n
}

// Bump counts a change to the client's own state, for Gen: as Home's, as
// it was when the client had one core.
func (f *Fleet) Bump() { f.Home().Core.Gen++ }

// Live are the machines whose cores the client is with, or will be again
// by itself: not Gone.
func (f *Fleet) Live() []*Machine {
	var out []*Machine
	for _, mc := range f.Machines() {
		if mc.Gone == "" {
			out = append(out, mc)
		}
	}
	return out
}

// Tabs are every machine's tabs, one machine's after another's.
func (f *Fleet) Tabs() core.TabList {
	if ms := f.Machines(); len(ms) == 1 {
		return ms[0].Core.Tabs
	}
	var out core.TabList
	for _, mc := range f.Machines() {
		out = append(out, mc.Core.Tabs...)
	}
	return out
}

// Ref is t's TabRef, as its machine's core names it.
func (f *Fleet) Ref(t *core.Tab) core.TabRef {
	if t == nil {
		return core.TabRef{}
	}
	return f.ofOrHome(f.Of(t)).Core.Ref(t)
}

// PaneRef is p's PaneRef, as its machine's core names it.
func (f *Fleet) PaneRef(p *core.PaneTab) core.PaneRef {
	if p == nil {
		return core.PaneRef{}
	}
	return f.ofOrHome(f.OfPane(p)).Core.PaneRef(p)
}

func (f *Fleet) ofOrHome(mc *Machine) *Machine {
	if mc == nil {
		return f.Home()
	}
	return mc
}

// ByRef is the tab or agent r names, nil when it is none of the fleet's.
func (f *Fleet) ByRef(r core.TabRef) *core.Tab {
	for _, mc := range f.Machines() {
		if t := mc.Core.ByRef(r); t != nil {
			return t
		}
	}
	return nil
}

// PaneByRef is the pane r names, and the tab it is in; nils when it is
// none of the fleet's.
func (f *Fleet) PaneByRef(r core.PaneRef) (*core.Tab, *core.PaneTab) {
	for _, mc := range f.Machines() {
		if t, p := mc.Core.PaneByRef(r); p != nil {
			return t, p
		}
	}
	return nil, nil
}

// IndexOn is where t is among the tabs of mc, or -1 when it is not there.
func (f *Fleet) IndexOn(mc *Machine, t *core.Tab) int { return core.TabList(mc.Core.Tabs).Index(t) }

// TabIndex is where t is in Tabs, or -1 when it is not there.
func (f *Fleet) TabIndex(t *core.Tab) int { return f.Tabs().Index(t) }

// The tabs are grouped by machine first, then as each machine's core
// groups them (core.Tab.GroupName): a group of this machine's is called
// by that name, one of another machine's by the machine's name, a colon,
// and that name.

// Group is the group tab t is in.
func (f *Fleet) Group(t *core.Tab) string {
	return groupName(f.ofOrHome(f.Of(t)), t.GroupName())
}

func groupName(mc *Machine, g string) string {
	if mc.Name == "" {
		return g
	}
	return mc.Name + ":" + g
}

// GroupOf is the machine of group g, and the group as its core names it.
func (f *Fleet) GroupOf(g string) (*Machine, string) {
	for _, mc := range f.Machines() {
		if mc.Name != "" && len(g) > len(mc.Name) && g[:len(mc.Name)+1] == mc.Name+":" {
			return mc, g[len(mc.Name)+1:]
		}
	}
	return f.Home(), g
}

// GroupTabs is the indexes into Tabs of the tabs in group g, in order.
func (f *Fleet) GroupTabs(g string) []int {
	mc, name := f.GroupOf(g)
	at := 0
	for _, o := range f.Machines() {
		if o == mc {
			idx := core.TabList(o.Core.Tabs).GroupTabs(name)
			for i := range idx {
				idx[i] += at
			}
			return idx
		}
		at += len(o.Core.Tabs)
	}
	return nil
}

// Groups lists the groups that have tabs, a machine's after another's;
// see core.TabList.Groups.
func (f *Fleet) Groups() []string {
	var out []string
	for _, mc := range f.Machines() {
		for _, g := range core.TabList(mc.Core.Tabs).Groups() {
			out = append(out, groupName(mc, g))
		}
	}
	return out
}

// GroupStatus is the light group g shows; see core.TabList.GroupStatus.
func (f *Fleet) GroupStatus(g string) core.Status {
	mc, name := f.GroupOf(g)
	return core.TabList(mc.Core.Tabs).GroupStatus(name)
}

// GroupCount is how many of group g's tabs are lit st.
func (f *Fleet) GroupCount(g string, st core.Status) int {
	mc, name := f.GroupOf(g)
	return core.TabList(mc.Core.Tabs).GroupCount(name, st)
}

// CountStatus is how many tabs have status s.
func (f *Fleet) CountStatus(s core.Status) int { return f.Tabs().CountStatus(s) }

// Neediest is the tab that most wants the user, and its index in Tabs;
// see core.TabList.Neediest.
func (f *Fleet) Neediest() (int, *core.Tab) { return f.Tabs().Neediest() }

// Waiting are the agents that need the user; see core.TabList.Waiting.
func (f *Fleet) Waiting() []*core.Tab { return f.Tabs().Waiting() }

// AllAgents are every agent of every machine's.
func (f *Fleet) AllAgents() []*core.Tab { return f.Tabs().AllAgents() }

// ColdTabs are the tabs gone cold, by each one's machine's cold_days.
func (f *Fleet) ColdTabs() []*core.Tab {
	var out []*core.Tab
	for _, mc := range f.Machines() {
		out = append(out, mc.Core.ColdTabs()...)
	}
	return out
}
