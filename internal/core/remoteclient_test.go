package core

import (
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/attach"
)

// remoteCore is core c, its loop stepped on a goroutine of the test's,
// with a client over a pair of connections keeping a copy of it.
func remoteCore(t *testing.T, c *Core) (*CoreConn, chan any) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	a, b, err := attach.Pair()
	if err != nil {
		t.Fatal(err)
	}
	l := NewLoop(c, nil)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case ev := <-l.in:
				l.step(ev)
			}
		}
	}()
	t.Cleanup(func() {
		a.Close("")
		b.Close("")
		close(stop)
		<-done
	})
	c.Mu.Lock()
	c.AttachClient(a, func(ev RequestEv) { l.Post(ev) })
	c.Mu.Unlock()
	told := make(chan any, 64)
	cc := Connect(b, &Core{}, func(msg any) {
		select {
		case told <- msg:
		default:
		}
	})
	return cc, told
}

// TestRemoteClient: a client in another process gets a copy of core's
// state; a request it waits for is done and shows in the copy when Do
// returns, with what core told it meanwhile; what it shows is what core
// goes by; and once it has gone, core has no client.
func TestRemoteClient(t *testing.T) {
	tb := &Tab{}
	tb.ID, tb.Name = "a", "parser"
	c := &Core{ID: "", Tabs: []*Tab{tb}}
	cc, told := remoteCore(t, c)

	deadline := time.Now().Add(5 * time.Second)
	for {
		cc.C.Mu.Lock()
		cc.Take()
		n := len(cc.C.Tabs)
		cc.C.Mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the copy never got the tab")
		}
		select {
		case <-told:
		case <-time.After(50 * time.Millisecond):
		}
	}

	cc.C.Mu.Lock()
	ref := cc.C.Ref(cc.C.Tabs[0])
	evs, err := cc.Run(RenameTab{T: ref, Name: "lexer"})
	if err != nil || cc.C.Tabs[0].Name != "lexer" {
		t.Fatalf("renamed: %q, %v", cc.C.Tabs[0].Name, err)
	}
	// Closing the tab's own pane is not done: core says so.
	evs, err = cc.Run(ClosePane{P: cc.C.PaneRef(cc.C.Tabs[0].OwnPane())})
	if err != nil || len(evs) != 1 {
		t.Fatalf("told %#v, %v", evs, err)
	}
	if _, ok := evs[0].(ToastMsg); !ok {
		t.Fatalf("told %#v", evs[0])
	}
	_, err = cc.Run(ClientSays{State: ClientState{Front: ref, Shown: []TabRef{ref}, DialogClosed: true,
		Sizes: []PaneSize{{A: ref, W: 90, H: 30}}}})
	cc.C.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	c.Mu.Lock()
	w, h := c.Client.TabSize(ref)
	if c.Client.Front() != ref || !c.Client.TabShown(ref) || !c.Client.DialogClosed() || w != 90 || h != 30 || !c.HasClient() {
		t.Errorf("core goes by %+v", c.Client)
	}
	c.Mu.Unlock()

	cc.Close("")
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		c.Mu.Lock()
		gone := !c.HasClient() && c.Screens == nil
		c.Mu.Unlock()
		if gone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("core still has the client gone")
		}
	}
	cc.C.Mu.Lock()
	if _, err := cc.Run(RenameTab{T: ref, Name: "x"}); err == nil {
		t.Error("a request done with the connection closed")
	}
	cc.C.Mu.Unlock()
}

// TestStartWhenSized: a core of its own starts the agents restored once a
// client says how large it shows them, not before; at once when one has
// said so already; and at StartWaiting with none.
func TestStartWhenSized(t *testing.T) {
	sized := ClientState{Fresh: PaneSize{W: 80, H: 24}}
	for _, c := range []struct {
		name   string
		before ClientState // what the client said before
		then   Request     // what comes after StartWhenSized
	}{
		{"client says", ClientState{}, ClientSays{State: sized}},
		{"client said", sized, nil},
		{"no client", ClientState{}, StartWaiting{}},
	} {
		m := &Core{}
		m.Client = &RemoteClient{state: c.before}
		StartWhenSized{}.Do(m)
		if c.then == nil {
			if m.waitingStart != nil {
				t.Errorf("%s: waits though the client said its size", c.name)
			}
			continue
		}
		if m.waitingStart == nil {
			t.Fatalf("%s: started before a client said its size", c.name)
		}
		ClientSays{State: ClientState{}}.Do(m)
		if c.name == "client says" && m.waitingStart == nil {
			t.Errorf("%s: started on a client saying no size", c.name)
		}
		c.then.Do(m)
		if m.waitingStart != nil {
			t.Errorf("%s: still waiting", c.name)
		}
	}
}
