package core

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/attach"
)

// TestJSONPatch: a patch made of two values makes the second of the first,
// whatever changed: a field, one gone or new, deep in an array or not.
func TestJSONPatch(t *testing.T) {
	cases := []struct{ old, new string }{
		{`{"a":1,"b":"x"}`, `{"a":1,"b":"y"}`},
		{`{"a":1,"b":"x"}`, `{"a":1}`},
		{`{"a":1}`, `{"a":1,"c":[1,2]}`},
		{`{"agents":[{"n":1,"s":"working"},{"n":2}]}`, `{"agents":[{"n":1,"s":"done"},{"n":2}]}`},
		{`{"agents":[{"n":1}]}`, `{"agents":[{"n":1},{"n":2}]}`},
		{`{"a":{"b":{"c":null}}}`, `{"a":{"b":{"c":12345678901234567890}}}`},
		{`{"a":[1,2]}`, `{"a":null}`},
		{`[1,2,3]`, `[1,5,3]`},
		{`"x"`, `{"a":1}`},
		{`{"a":"<&>"}`, `{"a":"<"}`},
	}
	for _, c := range cases {
		p := diffJSON(json.RawMessage(c.old), json.RawMessage(c.new))
		if p == nil {
			t.Fatalf("%s → %s: no patch", c.old, c.new)
		}
		enc, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		var wire jsonPatch // as the client reads it
		if err := json.Unmarshal(enc, &wire); err != nil {
			t.Fatal(err)
		}
		got, err := applyJSON(json.RawMessage(c.old), &wire)
		if err != nil {
			t.Fatalf("%s → %s: %v", c.old, c.new, err)
		}
		var g, w any
		_ = json.Unmarshal(got, &g)
		_ = json.Unmarshal([]byte(c.new), &w)
		if gb, _ := json.Marshal(g); string(gb) != string(must(json.Marshal(w))) {
			t.Fatalf("%s → %s: made %s (patch %s)", c.old, c.new, got, enc)
		}
	}
	if p := diffJSON(json.RawMessage(`{"a":1}`), json.RawMessage(`{"a":1}`)); p != nil {
		t.Fatalf("the same: %+v", p)
	}
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}

// TestMirrorPatches: a client taking patches has the same copy as one sent
// every part whole, and is sent less for a change of a field.
func TestMirrorPatches(t *testing.T) {
	m, a, b := mirrored()
	whole, patched := Sent{}, Sent{Patch: true}
	rw, rp := NewMirror(&Core{}), NewMirror(&Core{})
	step := func() (int, int) {
		t.Helper()
		pw, err := m.Parts(&whole)
		if err != nil {
			t.Fatal(err)
		}
		pp, err := m.Parts(&patched)
		if err != nil {
			t.Fatal(err)
		}
		applyAll(t, rw, pw)
		applyAll(t, rp, pp)
		cw, cp := must(json.Marshal(rw.C.Tabs)), must(json.Marshal(rp.C.Tabs))
		for i := range rw.C.Tabs {
			if x, y := must(json.Marshal((*tabFields)(rw.C.Tabs[i]))), must(json.Marshal((*tabFields)(rp.C.Tabs[i]))); string(x) != string(y) {
				t.Fatalf("tab %d differs:\n%s\n%s", i, x, y)
			}
		}
		if string(cw) != string(cp) || rw.C.Root != rp.C.Root || len(rw.C.Overlaps) != len(rp.C.Overlaps) {
			t.Fatalf("copies differ")
		}
		return len(must(json.Marshal(pw))), len(must(json.Marshal(pp)))
	}
	step()
	a.Light, a.Changed = StDone, a.Changed.Add(time.Minute)
	if w, p := step(); p*3 > w {
		t.Fatalf("a change of a field sent %d bytes as a patch, %d whole", p, w)
	}
	b.Name = "docs2"
	m.Root = "/elsewhere"
	step()
	extra := &Tab{}
	extra.ID, extra.Name = "c", "new"
	m.Tabs = append(m.Tabs, extra)
	step()
	m.Tabs = m.Tabs[:2]
	step()
	if rp.C.Root != "/elsewhere" || len(rp.C.Tabs) != 2 || rp.C.Tabs[1].Name != "docs2" {
		t.Fatalf("copy: %s %d", rp.C.Root, len(rp.C.Tabs))
	}

	// A patch the copy cannot take asks for all whole again.
	if err := NewMirror(&Core{}).Apply(Part{Key: PartCore, Patch: &jsonPatch{O: map[string]*jsonPatch{"Root": {V: json.RawMessage(`"x"`)}}}}); err == nil {
		t.Fatal("a patch of a part never sent was taken")
	}
}

// TestAPatchNotTakenWaitsForTheResync: a client that cannot take a patch
// asks for every part whole again, and a request done meanwhile, and what
// core told with it, wait until they have come: what Run returns is read
// against the state core had.
func TestAPatchNotTakenWaitsForTheResync(t *testing.T) {
	a, b, err := attach.Pair()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(""); b.Close("") })
	cc := Connect(b, &Core{}, func(any) {})

	// asked is the next request the client asks to be told is done.
	asked := func() askedWire {
		t.Helper()
		for {
			msg, err := a.Recv()
			if err != nil {
				t.Fatal(err)
			}
			var aw askedWire
			if msg[0] == wireAsked && json.Unmarshal(msg[1:], &aw) == nil {
				return aw
			}
		}
	}
	type result struct {
		evs []Event
		err error
	}
	ran := make(chan result, 1)
	go func() {
		evs, err := cc.Run(StartWaiting{})
		ran <- result{evs, err}
	}()
	first := asked()
	notice, _ := EncodeEvent(NoticeMsg("told"))
	a.Send("", wire(wireParts, []Part{{Key: PartCore, Patch: &jsonPatch{O: map[string]*jsonPatch{"Root": {V: json.RawMessage(`"/r"`)}}}}}))
	a.Send("", append([]byte{wireEvent}, notice...))
	a.Send("", wire(wireDone, doneWire{ID: first.ID}))

	resync := asked()
	if r, err := DecodeRequest(resync.Req); err != nil || reflect.TypeOf(r) != reflect.TypeOf(ResendParts{}) {
		t.Fatalf("asked %s, want a resync", resync.Req)
	}
	select {
	case r := <-ran:
		t.Fatalf("done before the resync: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}
	whole, err := (&Core{Root: "/r"}).Parts(&Sent{})
	if err != nil {
		t.Fatal(err)
	}
	a.Send("", wire(wireParts, whole))
	a.Send("", wire(wireDone, doneWire{ID: resync.ID}))
	select {
	case r := <-ran:
		if r.err != nil {
			t.Fatal(r.err)
		}
		if len(r.evs) != 1 || r.evs[0] != NoticeMsg("told") {
			t.Fatalf("told %+v", r.evs)
		}
		if cc.C.Root != "/r" {
			t.Fatalf("copy's root %q", cc.C.Root)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("never done")
	}
}

// TestAReconnectEndsTheResync: a core started again sends every part
// whole, and what waited for a resync asked of the one before is let go,
// not held for a done that never comes.
func TestAReconnectEndsTheResync(t *testing.T) {
	pair := func() (*attach.Conn, *attach.Conn) {
		a, b, err := attach.Pair()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { a.Close(""); b.Close("") })
		return a, b
	}
	a, b := pair()
	cc := Connect(b, &Core{}, func(any) {})
	notice, _ := EncodeEvent(NoticeMsg("told"))
	a.Send("", wire(wireParts, []Part{{Key: PartCore, Patch: &jsonPatch{D: []string{"Root"}}}}))
	a.Send("", append([]byte{wireEvent}, notice...))
	waitTaken := func(want int) []Event {
		t.Helper()
		var evs []Event
		for deadline := time.Now().Add(5 * time.Second); len(evs) < want; {
			if time.Now().After(deadline) {
				t.Fatalf("taken %+v", evs)
			}
			evs = append(evs, cc.Take()...)
			time.Sleep(10 * time.Millisecond)
		}
		return evs
	}
	time.Sleep(100 * time.Millisecond)
	if evs := cc.Take(); len(evs) != 0 || cc.resync == 0 {
		t.Fatalf("told %+v before the resync (%d)", evs, cc.resync)
	}
	a2, b2 := pair()
	cc.Reconnect(b2)
	if evs := waitTaken(1); evs[0] != NoticeMsg("told") {
		t.Fatalf("told %+v", evs)
	}
	whole, _ := (&Core{Root: "/r"}).Parts(&Sent{})
	a2.Send("", wire(wireParts, whole))
	for deadline := time.Now().Add(5 * time.Second); cc.C.Root != "/r"; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("copy's root %q", cc.C.Root)
		}
		cc.Take()
	}
}
