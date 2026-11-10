package core

import (
	"reflect"
	"sync"
)

// TabRef names a tab among those of every core a client shows: the core's
// ID, and the tab's own, which is only that core's. A client of one core
// alone, on the same machine, has them all with Core "". The zero TabRef is
// no tab. Any agent is named so: a tab's own, another in its panes, or a
// reviewer.
type TabRef struct {
	Core string
	Tab  string
}

// PaneRef names a pane the same way: the core's ID, and the pane's own.
type PaneRef struct {
	Core string
	Pane string
}

// Ref is t's TabRef; the zero one for nil. A tab or agent with no id yet, as
// one from an old session may be, is given one.
func (m *Core) Ref(t *Tab) TabRef {
	if t == nil {
		return TabRef{}
	}
	if t.ID == "" {
		t.ID = NewTabID()
	}
	return TabRef{Core: m.ID, Tab: t.ID}
}

// PaneRef is p's PaneRef; the zero one for nil.
func (m *Core) PaneRef(p *PaneTab) PaneRef {
	if p == nil {
		return PaneRef{}
	}
	if p.ID == "" {
		p.ID = NewTabID()
	}
	return PaneRef{Core: m.ID, Pane: p.ID}
}

// ByRef is the tab or agent r names, nil when it is not one of this core's.
func (m *Core) ByRef(r TabRef) *Tab {
	if r.Core != m.ID {
		return nil
	}
	return TabList(m.Tabs).Find(r.Tab)
}

// PaneByRef is the pane r names, and the tab it is in; nils when it is not
// one of this core's.
func (m *Core) PaneByRef(r PaneRef) (*Tab, *PaneTab) {
	if r.Core != m.ID {
		return nil, nil
	}
	return TabList(m.Tabs).FindPane(r.Pane)
}

// The core of a client in another process names its tabs and panes by the
// ID the client gave its copy, the machine's name among the client's;
// the core itself keeps ID "", knowing nothing of the client's names. The
// connection swaps the one for the other where it crosses: renameRefs.

var (
	tabRefType  = reflect.TypeFor[TabRef]()
	paneRefType = reflect.TypeFor[PaneRef]()
	refsIn      sync.Map // reflect.Type → bool, of holdsRefs
)

// renameRefs has every TabRef and PaneRef within v that names core from,
// and a tab or pane, name core to instead. A zero ref stays zero. v is
// changed in place, and is to be fresh, as one decoded is: what it points
// at is changed too.
func renameRefs(v reflect.Value, from, to string) {
	if from == to {
		return
	}
	rename(v, from, to, map[uintptr]bool{})
}

func rename(v reflect.Value, from, to string, seen map[uintptr]bool) {
	if !holdsRefs(v.Type()) {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() || seen[v.Pointer()] {
			return
		}
		seen[v.Pointer()] = true
		rename(v.Elem(), from, to, seen)
	case reflect.Interface:
		if v.IsNil() || !v.CanSet() {
			return
		}
		e := reflect.New(v.Elem().Type()).Elem()
		e.Set(v.Elem())
		rename(e, from, to, seen)
		v.Set(e)
	case reflect.Struct:
		switch v.Type() {
		case tabRefType:
			if r := v.Addr().Interface().(*TabRef); r.Core == from && r.Tab != "" {
				r.Core = to
			}
			return
		case paneRefType:
			if r := v.Addr().Interface().(*PaneRef); r.Core == from && r.Pane != "" {
				r.Core = to
			}
			return
		}
		ty := v.Type()
		for i := range ty.NumField() {
			if f := ty.Field(i); f.IsExported() && f.Tag.Get("json") != "-" {
				rename(v.Field(i), from, to, seen)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			rename(v.Index(i), from, to, seen)
		}
	case reflect.Map:
		if v.IsNil() {
			return
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			k := reflect.New(v.Type().Key()).Elem()
			k.Set(iter.Key())
			rename(k, from, to, seen)
			e := reflect.New(v.Type().Elem()).Elem()
			e.Set(iter.Value())
			rename(e, from, to, seen)
			out.SetMapIndex(k, e)
		}
		v.Set(out)
	}
}

// holdsRefs reports whether a value of type ty may hold a TabRef or a
// PaneRef, through what goes as JSON.
func holdsRefs(ty reflect.Type) bool {
	if b, ok := refsIn.Load(ty); ok {
		return b.(bool)
	}
	b := typeHoldsRefs(ty, map[reflect.Type]bool{})
	refsIn.Store(ty, b)
	return b
}

func typeHoldsRefs(ty reflect.Type, seen map[reflect.Type]bool) bool {
	if ty == tabRefType || ty == paneRefType {
		return true
	}
	if seen[ty] {
		return false
	}
	seen[ty] = true
	switch ty.Kind() {
	case reflect.Interface:
		return true
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return typeHoldsRefs(ty.Elem(), seen)
	case reflect.Map:
		return typeHoldsRefs(ty.Key(), seen) || typeHoldsRefs(ty.Elem(), seen)
	case reflect.Struct:
		for i := range ty.NumField() {
			f := ty.Field(i)
			if f.IsExported() && f.Tag.Get("json") != "-" && typeHoldsRefs(f.Type, seen) {
				return true
			}
		}
	}
	return false
}

// renamedRequest is a copy of r with its refs renamed from core from to
// core to, by way of JSON: what r points at is not changed.
func renamedRequest(r Request, from, to string) (Request, error) {
	b, err := EncodeRequest(r)
	if err != nil || from == to || !holdsRefs(reflect.TypeOf(r)) {
		return r, err
	}
	fresh, err := DecodeRequest(b)
	if err != nil {
		return nil, err
	}
	v := reflect.New(reflect.TypeOf(fresh)).Elem()
	v.Set(reflect.ValueOf(fresh))
	renameRefs(v, from, to)
	return v.Interface().(Request), nil
}
