package core

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/attach"
)

// remoteCore is core c, its loop stepped on a goroutine of the test's,
// with a client over a pair of connections keeping a copy of it.
func remoteCore(t *testing.T, c *Core) (*CoreConn, chan any) {
	t.Helper()
	return remoteCoreAs(t, c, "")
}

// remoteCoreAs is remoteCore, the client naming the core id.
func remoteCoreAs(t *testing.T, c *Core, id string) (*CoreConn, chan any) {
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
	cc := Connect(b, &Core{ID: id}, func(msg any) {
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

// TestCopyNamesCoreByItsID: a client that names a core "box" has the
// core's tabs, and what core tells of them, named so in its copy; what it
// asks of them by that name is done by core, which names them "" still.
func TestCopyNamesCoreByItsID(t *testing.T) {
	tb := &Tab{}
	tb.ID, tb.Name = "a", "parser"
	c := &Core{Tabs: []*Tab{tb}, Asking: []*Question{{ID: 1, A: TabRef{Tab: "a"}}}}
	cc, told := remoteCoreAs(t, c, "box")
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
	defer cc.C.Mu.Unlock()
	ref := TabRef{Core: "box", Tab: "a"}
	if cc.C.ID != "box" || cc.C.Ref(cc.C.Tabs[0]) != ref || cc.C.ByRef(ref) != cc.C.Tabs[0] {
		t.Fatalf("the copy, %q, names its tab %+v", cc.C.ID, cc.C.Ref(cc.C.Tabs[0]))
	}
	if len(cc.C.Asking) != 1 || cc.C.Asking[0].A != ref {
		t.Fatalf("the copy asks %+v", *cc.C.Asking[0])
	}
	if _, err := cc.Run(RenameTab{T: ref, Name: "lexer"}); err != nil || cc.C.Tabs[0].Name != "lexer" {
		t.Fatalf("renamed: %q, %v", cc.C.Tabs[0].Name, err)
	}
	pr := cc.C.PaneRef(cc.C.Tabs[0].OwnPane())
	if pr.Core != "box" {
		t.Fatalf("the pane is %+v", pr)
	}
	if _, err := cc.Run(ClientSays{State: ClientState{Front: ref, Shown: []TabRef{ref}, Sizes: []PaneSize{{T: ref, P: pr, W: 1, H: 1}}}}); err != nil {
		t.Fatal(err)
	}
	c.Mu.Lock()
	front := c.Client.Front()
	c.Tell(StartedMsg{A: c.Ref(tb)})
	c.Mu.Unlock()
	if front != (TabRef{Tab: "a"}) {
		t.Errorf("core has the front tab as %+v", front)
	}
	// What core tells the client of its tab comes named "box".
	evs, err := cc.Run(RenameTab{T: ref, Name: "parser"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range evs {
		if s, ok := ev.(StartedMsg); ok {
			if s.A != ref {
				t.Fatalf("told %+v", s)
			}
			return
		}
	}
	t.Fatalf("told %#v", evs)
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

// TestPartsLater: what changed with no Gen of its own, as a program
// drawing changes, is not looked for on every flush, but comes all the
// same, with nothing after to flush it.
func TestPartsLater(t *testing.T) {
	tb := &Tab{}
	tb.ID, tb.Name = "a", "parser"
	c := &Core{Tabs: []*Tab{tb}}
	cc, _ := remoteCore(t, c)
	c.Mu.Lock()
	tb.Name = "lexer"
	c.FlushClient() // just after the first: not looked at yet
	c.Mu.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for {
		cc.C.Mu.Lock()
		cc.Take()
		name := ""
		if len(cc.C.Tabs) == 1 {
			name = cc.C.Tabs[0].Name
		}
		cc.C.Mu.Unlock()
		if name == "lexer" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the copy has %q", name)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestBehindIsNoDone: a client that fell behind takes the request it
// waits for as done only once core says it has done it, with the parts
// sent afresh before; not on a mark of an earlier fall.
func TestBehindIsNoDone(t *testing.T) {
	c := &Core{}
	cc := &CoreConn{C: c, mirror: NewMirror(c), more: make(chan struct{}, 1)}
	tb := &Tab{}
	tb.ID, tb.Name = "a", "parser"
	parts, err := (&Core{Tabs: []*Tab{tb}}).Parts(&Sent{})
	if err != nil {
		t.Fatal(err)
	}
	cc.inbox = [][]byte{wire(wireBehind, behindWire{Done: 4})}
	if _, done := cc.takeUntil(5); done {
		t.Fatal("done on the mark of an earlier fall")
	}
	cc.inbox = [][]byte{wire(wireParts, parts), wire(wireBehind, behindWire{Done: 5})}
	if _, done := cc.takeUntil(5); !done {
		t.Fatal("not done once core said so")
	}
	if len(c.Tabs) != 1 || c.Tabs[0].Name != "parser" {
		t.Fatalf("done before the parts: %d tabs", len(c.Tabs))
	}
}

// TestCoreGoneSaysWhy: a client is told why core closed its connection,
// another client taking over too.
func TestCoreGoneSaysWhy(t *testing.T) {
	a, b, err := attach.Pair()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close("") })
	told := make(chan any, 8)
	Connect(b, &Core{}, func(msg any) { told <- msg })
	a.Close(attach.ByeTakenOver)
	for deadline := time.After(5 * time.Second); ; {
		select {
		case msg := <-told:
			if g, ok := msg.(CoreGoneMsg); ok {
				if g.Why != attach.ByeTakenOver {
					t.Fatalf("told %q", g.Why)
				}
				return
			}
		case <-deadline:
			t.Fatal("never told core was gone")
		}
	}
}

// TestASlowClientDoesNotGrowCore: a client that reads nothing, while core
// tells it far more than the client socket's queue holds, costs core no
// more memory than that queue: it is dropped, and sent afresh, not kept.
func TestASlowClientDoesNotGrowCore(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	a, b, err := attach.Pair()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(""); b.Close("") })
	tb := &Tab{}
	tb.ID, tb.Name = "a", "parser"
	c := &Core{Tabs: []*Tab{tb}}
	c.Mu.Lock()
	c.AttachClient(a, func(RequestEv) {})
	c.Mu.Unlock()

	heap := func() uint64 {
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return ms.HeapAlloc
	}
	before := heap()
	big := strings.Repeat("x", 64<<10)
	const n = 2000 // 128 MiB told
	for i := range n {
		c.Mu.Lock()
		c.Tell(NoticeMsg(fmt.Sprint(i, big)))
		tb.Name = fmt.Sprint("parser ", i, big) // and the tab changing
		c.FlushClient()
		c.Mu.Unlock()
	}
	if grew := int64(heap()) - int64(before); grew > 3*attach.QueueLimit {
		t.Errorf("core grew %d MiB for a client that reads nothing", grew>>20)
	}
}

func offlineClient() *RemoteClient {
	rc := &RemoteClient{}
	rc.gone.Store(true)
	return rc
}

func TestOfflineNoticeLimits(t *testing.T) {
	for _, size := range []int{1, 4096} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			rc := offlineClient()
			for i := range 1000 {
				rc.Tell(NoticeMsg(fmt.Sprintf("%d:%s", i, strings.Repeat("x", size))))
				total := 0
				for _, b := range rc.notices {
					total += len(b)
				}
				if total != rc.noticeBytes || total > offlineNoticeBytes || len(rc.notices) > offlineNoticeCount {
					t.Fatalf("unbounded notices: %d bytes, %d messages", total, len(rc.notices))
				}
			}
			if rc.noticesOmitted != uint64(1000-len(rc.notices)) {
				t.Fatalf("omitted %d", rc.noticesOmitted)
			}
			for i, b := range rc.notices {
				ev, err := DecodeEvent(b[1:])
				if err != nil || !strings.HasPrefix(string(ev.(NoticeMsg)), fmt.Sprintf("%d:", 1000-len(rc.notices)+i)) {
					t.Fatal("latest notices not retained in order")
				}
			}
		})
	}
}

func TestOfflineNoticeRepeatBecomesRecent(t *testing.T) {
	rc := offlineClient()
	for i := range offlineNoticeCount {
		rc.Tell(NoticeMsg(fmt.Sprint(i)))
	}
	rc.Tell(NoticeMsg("0"))
	rc.Tell(NoticeMsg("new"))
	first, _ := DecodeEvent(rc.notices[0][1:])
	lastRepeat, _ := DecodeEvent(rc.notices[len(rc.notices)-2][1:])
	if first != NoticeMsg("2") || lastRepeat != NoticeMsg("0") || rc.noticesOmitted != 2 {
		t.Fatalf("recent repeat lost: first=%v, repeat=%v, omitted=%d", first, lastRepeat, rc.noticesOmitted)
	}
}

func TestOfflineNoticeDedupOversizeAndSaturation(t *testing.T) {
	rc := offlineClient()
	for range 1000 {
		rc.Tell(NoticeMsg("same"))
	}
	if len(rc.notices) != 1 || rc.noticesOmitted != 999 {
		t.Fatalf("duplicates retained: %d, omitted %d", len(rc.notices), rc.noticesOmitted)
	}
	before := rc.noticeBytes
	// Both a raw oversized string and one oversized only after JSON escaping.
	for _, s := range []string{strings.Repeat("x", offlineNoticeBytes+1), strings.Repeat("\x00", offlineNoticeBytes/2)} {
		rc.Tell(NoticeMsg(s))
	}
	if rc.noticeBytes != before || len(rc.notices) != 1 || rc.noticesOmitted != 1001 {
		t.Fatal("oversized notice retained or not counted")
	}
	rc.noticesOmitted = ^uint64(0) - 1
	for range 3 {
		rc.Tell(NoticeMsg("same"))
	}
	if rc.noticesOmitted != ^uint64(0) {
		t.Fatal("omitted counter wrapped")
	}
	rc.Tell(ToastMsg("not a notice"))
	if len(rc.out) != 0 || len(rc.notices) != 1 {
		t.Fatal("offline non-notice retained")
	}
}

func TestOfflineNoticesTransferredOnceOnReconnect(t *testing.T) {
	old := offlineClient()
	old.Tell(NoticeMsg("first"))
	old.Tell(NoticeMsg("first"))
	old.Tell(NoticeMsg("second"))
	c := &Core{Client: old}
	cc, _ := remoteCore(t, c)
	// A request is a barrier: all initial events precede its completion.
	cc.C.Mu.Lock()
	evs, err := cc.Run(noRequest{})
	cc.C.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	var notices []string
	for _, ev := range evs {
		if n, ok := ev.(NoticeMsg); ok {
			notices = append(notices, string(n))
		}
	}
	if len(notices) != 3 || notices[0] != "first" || notices[1] != "second" || notices[2] != "While disconnected, 1 notices were omitted or repeated." {
		t.Fatalf("reconnect notices: %q", notices)
	}
	if old.notices != nil || old.noticeBytes != 0 || old.noticesOmitted != 0 {
		t.Fatal("old accounting not reset")
	}
	if len(old.takeNotices()) != 0 {
		t.Fatal("notices replayed")
	}
	cc.C.Mu.Lock()
	evs, err = cc.Run(noRequest{})
	cc.C.Mu.Unlock()
	if err != nil || len(evs) != 0 {
		t.Fatalf("summary replayed: %v, %v", evs, err)
	}
}

func TestOfflineNoticeSaturatedSummary(t *testing.T) {
	rc := offlineClient()
	rc.noticesOmitted = ^uint64(0)
	out := rc.takeNotices()
	if len(out) != 1 {
		t.Fatalf("summary count %d", len(out))
	}
	ev, err := DecodeEvent(out[0][1:])
	if err != nil || string(ev.(NoticeMsg)) != fmt.Sprintf("While disconnected, at least %d notices were omitted or repeated.", ^uint64(0)) {
		t.Fatalf("saturated summary %v, %v", ev, err)
	}
}
