package api

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// testCore is a core with one tab, its agent at work.
func testCore() *core.Core {
	t := &core.Tab{}
	t.Tab = state.Tab{ID: "t1", Name: "fix-login", Dir: "/w/fix-login", Agent: agents.Claude}
	t.Light = core.StWorking
	return &core.Core{Root: "/w", Tabs: []*core.Tab{t}, Agents: []agents.Kind{agents.Claude}, AgentsAt: time.Now()}
}

// serve is a Server for m, its calls done as the loop would, and the
// socket and key it listens at.
func serve(t *testing.T, m *core.Core, check func(hooks.Peer) string) (*Server, string, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "napi")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if check == nil {
		check = func(hooks.Peer) string { return "" }
	}
	sock, key := filepath.Join(dir, "api.sock"), filepath.Join(dir, "api.key")
	s, err := Listen(sock, key, Config{Version: "test", Root: m.Root, Check: check, Post: func(c core.Call) {
		go func() {
			m.Mu.Lock()
			defer m.Mu.Unlock()
			c.F(m)
		}()
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, sock, key
}

// client is a connection to the API, its lines read one at a time.
type client struct {
	t  *testing.T
	c  net.Conn
	sc *bufio.Scanner
}

func dial(t *testing.T, sock string) *client {
	t.Helper()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	sc := bufio.NewScanner(c)
	sc.Buffer(nil, MaxLine)
	return &client{t: t, c: c, sc: sc}
}

func (c *client) send(v any) {
	c.t.Helper()
	b, _ := json.Marshal(v)
	if _, err := c.c.Write(append(b, '\n')); err != nil {
		c.t.Fatal(err)
	}
}

// line is the next line, decoded into a map.
func (c *client) line() map[string]any {
	c.t.Helper()
	_ = c.c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if !c.sc.Scan() {
		c.t.Fatalf("no line: %v", c.sc.Err())
	}
	var v map[string]any
	if err := json.Unmarshal(c.sc.Bytes(), &v); err != nil {
		c.t.Fatalf("%s: %v", c.sc.Bytes(), err)
	}
	return v
}

// hello says hello with the key in keyFile, and checks the welcome.
func (c *client) hello(keyFile string) {
	c.t.Helper()
	key, err := attach.ReadKey(keyFile)
	if err != nil {
		c.t.Fatal(err)
	}
	c.send(Hello{Key: key, API: 1})
	if w := c.line(); w["api"] != float64(core.APIVersion) || w["version"] != "test" || w["root"] != "/w" {
		c.t.Fatalf("welcome %v", w)
	}
}

func TestWrongKeyIsRefused(t *testing.T) {
	_, sock, _ := serve(t, testCore(), nil)
	c := dial(t, sock)
	c.send(Hello{Key: "nope"})
	if a := c.line(); !strings.Contains(a["error"].(string), "wrong key") {
		t.Fatalf("answer %v", a)
	}
	if c.sc.Scan() {
		t.Fatal("connection still open")
	}
}

func TestNewerAPIIsRefused(t *testing.T) {
	_, sock, keyFile := serve(t, testCore(), nil)
	key, _ := attach.ReadKey(keyFile)
	c := dial(t, sock)
	c.send(Hello{Key: key, API: core.APIVersion + 1})
	if a := c.line(); !strings.Contains(a["error"].(string), "update nerdr") {
		t.Fatalf("answer %v", a)
	}
}

// TestPeerIsCheckedFirst: a program nerdr runs is refused before it says
// anything, as the client socket refuses it.
func TestPeerIsCheckedFirst(t *testing.T) {
	_, sock, _ := serve(t, testCore(), func(hooks.Peer) string { return "nerdr takes no client from a program it runs" })
	c := dial(t, sock)
	if a := c.line(); a["error"] != "nerdr takes no client from a program it runs" {
		t.Fatalf("answer %v", a)
	}
}

func TestKeyFileIsPrivate(t *testing.T) {
	_, _, keyFile := serve(t, testCore(), nil)
	fi, err := os.Stat(keyFile)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file %v %v", fi.Mode(), err)
	}
}

func TestStateAndErrors(t *testing.T) {
	_, sock, keyFile := serve(t, testCore(), nil)
	c := dial(t, sock)
	c.hello(keyFile)

	c.send(map[string]any{"id": "s1", "op": "state"})
	a := c.line()
	if a["id"] != "s1" {
		t.Fatalf("id %v", a)
	}
	res := a["result"].(map[string]any)
	tabs := res["tabs"].([]any)
	tab := tabs[0].(map[string]any)
	ag := tab["agents"].([]any)[0].(map[string]any)
	if len(tabs) != 1 || tab["id"] != "t1" || tab["name"] != "fix-login" || ag["id"] != "t1" || ag["state"] != "working" || ag["num"] != float64(1) {
		t.Fatalf("state %v", res)
	}
	if res["agents"].([]any)[0] != "claude" {
		t.Fatalf("agents %v", res["agents"])
	}

	c.send(map[string]any{"id": 2, "op": "dance"})
	if a := c.line(); a["id"] != float64(2) || !strings.Contains(a["error"].(string), `no op "dance"`) {
		t.Fatalf("answer %v", a)
	}
	c.send(map[string]any{"id": 3, "op": "read", "agent": "t1"})
	if a := c.line(); !strings.Contains(a["error"].(string), "not running") {
		t.Fatalf("answer %v", a)
	}
	c.send(map[string]any{"id": 4, "op": "type", "agent": "t1", "keys": []string{"F13"}})
	if a := c.line(); !strings.Contains(a["error"].(string), `no key "F13"`) {
		t.Fatalf("answer %v", a)
	}
	c.send(map[string]any{"id": 5, "op": "answer", "question": 7, "yes": true})
	if a := c.line(); !strings.Contains(a["error"].(string), "no question 7") {
		t.Fatalf("answer %v", a)
	}
	_, _ = c.c.Write([]byte("not json\n"))
	if a := c.line(); !strings.Contains(a["error"].(string), "not a request") {
		t.Fatalf("answer %v", a)
	}
}

// TestSendQueuesAMessage: send is a message for the agent, typed in once
// it is at its prompt.
func TestSendQueuesAMessage(t *testing.T) {
	m := testCore()
	_, sock, keyFile := serve(t, m, nil)
	c := dial(t, sock)
	c.hello(keyFile)
	c.send(map[string]any{"id": 1, "op": "send", "agent": "t1", "text": "also fix the logout"})
	if a := c.line(); a["error"] != nil {
		t.Fatalf("answer %v", a)
	}
	m.Mu.Lock()
	defer m.Mu.Unlock()
	msgs := m.Tabs[0].Messages
	if len(msgs) != 1 || msgs[0].Text != "also fix the logout" || !msgs[0].Auto {
		t.Fatalf("messages %+v", msgs)
	}
}

// TestSubscriberIsToldOfChanges: an agent's state, a question, and what
// nerdr tells the user reach a subscriber.
func TestSubscriberIsToldOfChanges(t *testing.T) {
	old := pollEvery
	pollEvery = 20 * time.Millisecond
	t.Cleanup(func() { pollEvery = old })
	m := testCore()
	s, sock, keyFile := serve(t, m, nil)
	c := dial(t, sock)
	c.hello(keyFile)
	c.send(map[string]any{"id": 1, "op": "subscribe"})
	if a := c.line(); a["id"] != float64(1) || a["error"] != nil {
		t.Fatalf("answer %v", a)
	}

	m.Mu.Lock()
	m.Tabs[0].Light = core.StBlocked
	m.Mu.Unlock()
	saw := map[string]map[string]any{}
	for len(saw) < 2 {
		ev := c.line()
		saw[ev["event"].(string)] = ev
	}
	ag := saw["agent"]["agent"].(map[string]any)
	if ag["state"] != "blocked" || saw["agent"]["was"] != "working" || saw["agent"]["tab_id"] != "t1" {
		t.Fatalf("agent event %v", saw["agent"])
	}
	if w := saw["waiting"]["waiting"].([]any); len(w) != 1 || w[0] != "t1" {
		t.Fatalf("waiting event %v", saw["waiting"])
	}

	m.Mu.Lock()
	s.Observe(core.ToastMsg("made fix-logout"))
	m.Mu.Unlock()
	if ev := c.line(); ev["event"] != "notice" || ev["text"] != "made fix-logout" {
		t.Fatalf("event %v", ev)
	}
}

func TestSubscriberStaysAfterInputEnds(t *testing.T) {
	old := pollEvery
	pollEvery = 20 * time.Millisecond
	t.Cleanup(func() { pollEvery = old })
	m := testCore()
	_, sock, keyFile := serve(t, m, nil)
	c := dial(t, sock)
	c.hello(keyFile)
	c.send(map[string]any{"op": "subscribe"})
	c.line()
	_ = c.c.(*net.UnixConn).CloseWrite()
	m.Mu.Lock()
	m.Tabs[0].Light = core.StDone
	m.Mu.Unlock()
	if ev := c.line(); ev["event"] != "agent" {
		t.Fatalf("event %v", ev)
	}
}

func TestInputEndingEndsAClientNotSubscribed(t *testing.T) {
	_, sock, keyFile := serve(t, testCore(), nil)
	c := dial(t, sock)
	c.hello(keyFile)
	c.send(map[string]any{"id": 1, "op": "state"})
	_ = c.c.(*net.UnixConn).CloseWrite()
	if a := c.line(); a["id"] != float64(1) {
		t.Fatalf("answer %v", a)
	}
	_ = c.c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if c.sc.Scan() {
		t.Fatalf("more: %s", c.sc.Bytes())
	}
}

func TestCloseRemovesSocketAndKey(t *testing.T) {
	s, sock, keyFile := serve(t, testCore(), nil)
	s.Close()
	for _, p := range []string{sock, keyFile} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s still there: %v", p, err)
		}
	}
}
