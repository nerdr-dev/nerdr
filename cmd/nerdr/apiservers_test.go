package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/api"
	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
)

// fakeCore is a machine's core as the aggregator reaches it: it welcomes,
// answers each request with who it is and what it was asked, and answers
// "hang" never.
type fakeCore struct {
	name string
	tr   *transport
	// reqs are the requests it was sent.
	reqs chan map[string]json.RawMessage

	wmu sync.Mutex
	w   *io.PipeWriter // to the aggregator
	r   *io.PipeReader // from it

	// st is its state; onSubscribe is sent before subscribe's answer.
	smu         sync.Mutex
	st          core.APIState
	onSubscribe []any
}

// set changes the state of fc.
func (fc *fakeCore) set(f func(st *core.APIState)) {
	fc.smu.Lock()
	defer fc.smu.Unlock()
	f(&fc.st)
}

func newFakeCore(name string, apiVersion int) *fakeCore {
	toAgg, fromCore := io.Pipe()
	toCore, fromAgg := io.Pipe()
	fc := &fakeCore{name: name, reqs: make(chan map[string]json.RawMessage, 100), w: fromCore, r: toCore,
		st: core.APIState{Root: "/root/" + name, Tabs: []core.APITab{}, Waiting: []string{}, Questions: []core.APIQuestion{}, Agents: []string{}}}
	var once sync.Once
	fc.tr = &transport{in: bufio.NewReader(toAgg), out: fromAgg,
		close: func() {
			once.Do(func() {
				_ = toAgg.Close()
				_ = fromAgg.Close()
			})
		},
		why: func(err error) error { return fmt.Errorf("%s gone: %v", name, err) }}
	go func() {
		fc.line(map[string]any{"api": apiVersion, "version": "v1-" + name, "root": "/root/" + name})
		sc := bufio.NewScanner(fc.r)
		for sc.Scan() {
			var r map[string]json.RawMessage
			_ = json.Unmarshal(sc.Bytes(), &r)
			fc.reqs <- r
			var op string
			_ = json.Unmarshal(r["op"], &op)
			switch op {
			case "hang":
			case "state":
				fc.smu.Lock()
				st := fc.st
				fc.smu.Unlock()
				fc.line(map[string]any{"id": r["id"], "result": st})
			case "subscribe":
				fc.smu.Lock()
				pre := fc.onSubscribe
				fc.smu.Unlock()
				for _, l := range pre {
					fc.line(l)
				}
				fc.line(map[string]any{"id": r["id"], "result": map[string]any{}})
			default:
				fc.line(map[string]any{"id": r["id"], "result": map[string]any{"from": name, "op": op, "target": r["target"]}})
			}
		}
	}()
	return fc
}

func (fc *fakeCore) line(v any) {
	b, _ := json.Marshal(v)
	fc.wmu.Lock()
	defer fc.wmu.Unlock()
	_, _ = fc.w.Write(append(b, '\n'))
}

// kill ends the connection, as when the network goes.
func (fc *fakeCore) kill() {
	_ = fc.w.Close()
	_ = fc.r.Close()
}

// next is the next request it was sent.
func (fc *fakeCore) next(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	select {
	case r := <-fc.reqs:
		return r
	case <-time.After(5 * time.Second):
		t.Fatalf("%s was sent nothing", fc.name)
	}
	return nil
}

// harness is an aggregator for local and servers, of fake cores, the
// client's lines written to in and read from lines.
type harness struct {
	t     *testing.T
	a     *aggregator
	in    *io.PipeWriter
	lines chan []byte
	exit  chan int
	local *fakeCore
	log   *tail

	mu    sync.Mutex
	api   map[string]int
	cores map[string]chan *fakeCore
	// prepare, when set, is done to each core dialed before it is used.
	prepare func(fc *fakeCore)
	// offs are the servers disconnected from in the TUI.
	offs map[string]bool
}

// setOff has server name disconnected from in the TUI, or not.
func (h *harness) setOff(name string, off bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.offs[name] = off
}

func startServers(t *testing.T, servers ...string) *harness {
	t.Helper()
	return startServersAPI(t, nil, servers...)
}

// startServersAPI is startServers, the cores of apis having those API
// versions.
func startServersAPI(t *testing.T, apis map[string]int, servers ...string) *harness {
	t.Helper()
	outR, outW := io.Pipe()
	inR, inW := io.Pipe()
	h := &harness{t: t, in: inW, lines: make(chan []byte, 1000), exit: make(chan int, 1), log: &tail{},
		api: map[string]int{}, cores: map[string]chan *fakeCore{}, offs: map[string]bool{}}
	h.a = newAggregator("vtest", outW, h.log)
	h.a.retryFirst, h.a.retryMax = 10*time.Millisecond, 50*time.Millisecond
	h.a.offEvery = 10 * time.Millisecond
	var cfg []config.Server
	for _, s := range servers {
		cfg = append(cfg, config.Server{Name: s})
		h.cores[s] = make(chan *fakeCore, 10000)
	}
	for k, v := range apis {
		h.api[k] = v
	}
	h.a.dial = func(s config.Server) (*transport, error) {
		h.mu.Lock()
		v, ok := h.api[s.Name]
		h.mu.Unlock()
		if !ok {
			v = core.APIVersion
		}
		fc := newFakeCore(s.Name, v)
		h.mu.Lock()
		prepare := h.prepare
		h.mu.Unlock()
		if prepare != nil {
			prepare(fc)
		}
		h.cores[s.Name] <- fc
		return fc.tr, nil
	}
	h.a.off = func(name string) bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.offs[name]
	}
	h.a.addServers(cfg)
	h.local = newFakeCore(localMachine, core.APIVersion)
	w, err := readServerWelcome(h.local.tr.in)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(nil, api.MaxLine)
		for sc.Scan() {
			h.lines <- append([]byte{}, sc.Bytes()...)
		}
		close(h.lines)
	}()
	go func() { h.exit <- h.a.run(h.local.tr, w, inR) }()
	t.Cleanup(func() {
		h.a.stop()
		_ = outR.Close()
		if t.Failed() {
			t.Logf("log: %s", h.log)
		}
	})
	return h
}

// core is the core server name was connected to last, waiting for it.
func (h *harness) core(name string) *fakeCore {
	h.t.Helper()
	select {
	case fc := <-h.cores[name]:
		return fc
	case <-time.After(5 * time.Second):
		h.t.Fatalf("%s never dialed", name)
	}
	return nil
}

func (h *harness) send(line string) {
	h.t.Helper()
	if _, err := h.in.Write([]byte(line + "\n")); err != nil {
		h.t.Fatal(err)
	}
}

// next is the next line out, raw and decoded.
func (h *harness) next() (map[string]json.RawMessage, []byte) {
	h.t.Helper()
	select {
	case b, ok := <-h.lines:
		if !ok {
			h.t.Fatal("the aggregator ended")
		}
		var v map[string]json.RawMessage
		if err := json.Unmarshal(b, &v); err != nil {
			h.t.Fatalf("not JSON: %s", b)
		}
		return v, b
	case <-time.After(5 * time.Second):
		h.t.Fatal("no line out")
	}
	return nil, nil
}

// until is the first line out that ok takes, the others skipped.
func (h *harness) until(what string, ok func(v map[string]json.RawMessage) bool) map[string]json.RawMessage {
	h.t.Helper()
	for range 100 {
		if v, _ := h.next(); ok(v) {
			return v
		}
	}
	h.t.Fatalf("never: %s", what)
	return nil
}

func str(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

// machineEvent is the next machine event for name with status.
func (h *harness) machineEvent(name, status string) map[string]json.RawMessage {
	h.t.Helper()
	return h.until(name+" "+status, func(v map[string]json.RawMessage) bool {
		return str(v["event"]) == "machine" && str(v["machine"]) == name && str(v["status"]) == status
	})
}

// waitConnected waits for each of names to be connected, asking machines.
func (h *harness) waitConnected(names ...string) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		all := true
		for _, m := range h.a.infos() {
			for _, n := range names {
				if m.Machine == n && m.Status != statusConnected {
					all = false
				}
			}
		}
		if all {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatalf("not connected: %v", h.a.infos())
}

// TestServersWelcome: the first line out lists the machines, this one
// first and connected, by its reserved name; a server named so, or with
// a folder that cannot be named over ssh, is left out or never tried.
func TestServersWelcome(t *testing.T) {
	a := newAggregator("vtest", io.Discard, io.Discard)
	a.addServers([]config.Server{{Name: "box"}, {Name: localMachine}, {Name: "box"}, {Name: "bad", Folder: "x'; rm -rf ~"}})
	if len(a.machines) != 2 || a.machines[0].info.Machine != "box" || a.machines[1].info.Status != statusDown || !a.machines[1].fixed {
		t.Fatalf("%+v %+v", a.machines[0].info, a.machines[1].info)
	}

	s := startServers(t, "box")
	v, _ := s.next()
	var w serversWelcome
	b, _ := json.Marshal(v)
	_ = json.Unmarshal(b, &w)
	if w.API != core.APIVersion || len(w.Machines) != 2 || w.Machines[0].Machine != localMachine ||
		w.Machines[0].Status != statusConnected || w.Machines[0].Root != "/root/local" || w.Machines[1].Machine != "box" {
		t.Fatalf("welcome %+v", w)
	}
}

// TestServersRouting: each request goes to the machine it names, without
// the machine, and its answer comes back saying whose it is.
func TestServersRouting(t *testing.T) {
	h := startServers(t, "box", "gpu")
	h.next()
	box, gpu := h.core("box"), h.core("gpu")
	h.waitConnected("box", "gpu")
	for i, fc := range []*fakeCore{h.local, box, gpu} {
		h.send(fmt.Sprintf(`{"id":%d,"machine":%q,"op":"read","target":"t-%s"}`, i, fc.name, fc.name))
		got := fc.next(t)
		if _, ok := got["machine"]; ok || str(got["target"]) != "t-"+fc.name {
			t.Fatalf("%s was sent %v", fc.name, got)
		}
		v, _ := h.next()
		var res struct{ From, Target string }
		_ = json.Unmarshal(v["result"], &res)
		if str(v["machine"]) != fc.name || res.From != fc.name || res.Target != "t-"+fc.name || string(v["id"]) != fmt.Sprint(i) {
			t.Fatalf("%s answered %v", fc.name, v)
		}
	}
	// No machine, or no such machine: answered here.
	h.send(`{"id":9,"op":"state"}`)
	if v, _ := h.next(); !strings.Contains(str(v["error"]), "needs a machine") {
		t.Fatalf("%v", v)
	}
	h.send(`{"id":10,"machine":"nope","op":"state"}`)
	if v, _ := h.next(); !strings.Contains(str(v["error"]), `no machine "nope"`) || str(v["machine"]) != "nope" {
		t.Fatalf("%v", v)
	}
	h.send(`{"op":"machines"}`)
	v, _ := h.next()
	var ms struct{ Machines []machineInfo }
	_ = json.Unmarshal(v["result"], &ms)
	if len(ms.Machines) != 3 || ms.Machines[2].Machine != "gpu" || ms.Machines[2].Version != "v1-gpu" {
		t.Fatalf("%s", v["result"])
	}
}

// TestServersIDEcho: a request's id comes back as it was sent, whatever
// it is, and stays out of the answer when the request had none: from a
// machine, and from the aggregator itself.
func TestServersIDEcho(t *testing.T) {
	h := startServers(t)
	h.next()
	for _, id := range []string{`"abc"`, `{"k":[1,2]}`, `7`, `null`, `1.5`, ``} {
		for _, req := range []string{`"machine":"local","op":"seen"`, `"machine":"nope","op":"seen"`} {
			line := "{" + req + "}"
			if id != "" {
				line = `{"id":` + id + "," + req + "}"
			}
			h.send(line)
			v, raw := h.next()
			got, has := v["id"]
			if id == "" && has || id != "" && !bytes.Equal(got, []byte(id)) {
				t.Fatalf("%s answered %s", line, raw)
			}
		}
	}
}

// TestServersQuitNeedsMachine: quit stops every program of a machine, so
// it is never sent without one named.
func TestServersQuitNeedsMachine(t *testing.T) {
	h := startServers(t)
	h.next()
	h.send(`{"id":1,"op":"quit"}`)
	if v, _ := h.next(); !strings.Contains(str(v["error"]), "say which, with machine") {
		t.Fatalf("%v", v)
	}
	h.send(`{"id":2,"machine":"local","op":"quit"}`)
	if r := h.local.next(t); str(r["op"]) != "quit" {
		t.Fatalf("%v", r)
	}
	select {
	case r := <-h.local.reqs:
		t.Fatalf("more was sent: %v", r)
	default:
	}
}

// TestServersMachineLostAndBack: a server whose connection is lost
// answers what it had not with an unknown outcome, never sending it
// again; the client is told it is down, and when it is back, its state,
// the aggregator subscribed to it again; its events say whose they are.
func TestServersMachineLostAndBack(t *testing.T) {
	h := startServers(t, "box")
	h.next()
	box := h.core("box")
	h.waitConnected("box")
	h.send(`{"id":"s","op":"subscribe"}`)
	if v, _ := h.next(); string(v["id"]) != `"s"` || v["result"] == nil {
		t.Fatalf("%v", v)
	}
	for _, fc := range []*fakeCore{h.local, box} {
		if r := fc.next(t); str(r["op"]) != "subscribe" {
			t.Fatalf("%s: %v", fc.name, r)
		}
		if r := fc.next(t); str(r["op"]) != "state" {
			t.Fatalf("%s: %v", fc.name, r)
		}
	}
	for range 2 {
		v, _ := h.next()
		if str(v["event"]) != "machine" || str(v["status"]) != statusConnected || !strings.Contains(string(v["state"]), "/root/") {
			t.Fatalf("%v", v)
		}
	}
	box.line(map[string]any{"event": "notice", "text": "hi"})
	if v, _ := h.next(); str(v["event"]) != "notice" || str(v["machine"]) != "box" {
		t.Fatalf("%v", v)
	}

	h.send(`{"id":5,"machine":"box","op":"hang"}`)
	box.next(t)
	box.kill()
	v := h.until("the answer", func(v map[string]json.RawMessage) bool { return string(v["id"]) == "5" })
	if !strings.Contains(str(v["error"]), "whether it was done is not known") || str(v["machine"]) != "box" {
		t.Fatalf("%v", v)
	}
	again := h.core("box")
	if r := again.next(t); str(r["op"]) != "subscribe" {
		t.Fatalf("%v", r)
	}
	if r := again.next(t); str(r["op"]) != "state" {
		t.Fatalf("%v", r)
	}
	ev := h.machineEvent("box", statusConnected)
	if !strings.Contains(string(ev["state"]), "/root/box") {
		t.Fatalf("%v", ev)
	}
	select {
	case r := <-again.reqs:
		t.Fatalf("sent again: %v", r)
	case <-time.After(50 * time.Millisecond):
	}
	again.set(func(st *core.APIState) { st.Waiting = []string{"a1"} })
	again.line(map[string]any{"event": "waiting", "waiting": []string{"a1"}})
	if v, _ := h.next(); str(v["event"]) != "waiting" || str(v["machine"]) != "box" || string(v["waiting"]) != `["a1"]` {
		t.Fatalf("%v", v)
	}
}

// TestServersDownEventOnce: the client subscribed hears that a server is
// down, with why, once, not again at each try.
func TestServersDownEventOnce(t *testing.T) {
	h := startServers(t, "box")
	h.next()
	h.waitConnected("box")
	box := h.core("box")
	h.send(`{"op":"subscribe"}`)
	h.machineEvent("box", statusConnected)
	box.kill()
	ev := h.machineEvent("box", statusDown)
	if !strings.Contains(str(ev["error"]), "box gone") {
		t.Fatalf("%v", ev)
	}
	h.machineEvent("box", statusConnected)
}

// TestServersIncompatibleAPI: a server whose core has another API is held
// out, saying why; the others work on.
func TestServersIncompatibleAPI(t *testing.T) {
	s := startServersAPI(t, map[string]int{"box": core.APIVersion + 1}, "box", "gpu")
	s.next()
	s.core("box")
	s.core("gpu")
	s.waitConnected("gpu")
	deadline := time.Now().Add(5 * time.Second)
	for {
		info := s.a.infos()[1]
		if info.Status == statusDown && strings.Contains(info.Error, "update nerdr here") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%+v", info)
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.send(`{"id":1,"machine":"box","op":"state"}`)
	if v, _ := s.next(); !strings.Contains(str(v["error"]), "box is not connected: nerdr v1-box there has API") {
		t.Fatalf("%v", v)
	}
	s.send(`{"id":2,"machine":"gpu","op":"read"}`)
	if v, _ := s.next(); str(v["machine"]) != "gpu" || v["result"] == nil {
		t.Fatalf("%v", v)
	}
}

// TestServersEndsWithInput: with its input ended, and nothing subscribed
// to, the aggregator answers what it was asked and ends; the connection
// here lost, it ends too.
func TestServersEndsWithInput(t *testing.T) {
	h := startServers(t)
	h.next()
	h.send(`{"id":1,"machine":"local","op":"seen"}`)
	_ = h.in.Close()
	if v, _ := h.next(); string(v["id"]) != "1" {
		t.Fatalf("%v", v)
	}
	select {
	case code := <-h.exit:
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not end")
	}

	h = startServers(t)
	h.next()
	h.local.kill()
	select {
	case code := <-h.exit:
		if code != 1 {
			t.Fatalf("exit %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not end with this machine's core gone")
	}
}

func TestCheckWelcome(t *testing.T) {
	if w, err := checkWelcome([]byte(`{"api":1,"version":"v1","root":"/w"}`)); err != nil || w.Root != "/w" {
		t.Fatalf("%v %v", w, err)
	}
	for line, want := range map[string]string{
		`{"error":"not you"}`:                        "refused: not you",
		`{"api":1,"version":"v","machines":[]}`:      "one hop",
		`{"api":1,"version":"v1"}`:                   "not a welcome",
		`nope`:                                       "not a welcome",
		`{"api":99,"version":"v9","root":"/w"}`:      "update nerdr here",
		`{"api":0,"version":"v0","root":"/w"}`:       "not a welcome",
		`{"api":1,"version":"v1","root":"/w","x":1}`: "",
	} {
		_, err := checkWelcome([]byte(line))
		if want == "" && err != nil || want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
			t.Errorf("%s: %v", line, err)
		}
	}
	if _, err := readServerWelcome(bufio.NewReader(strings.NewReader(""))); !errors.Is(err, errNoWelcome) {
		t.Errorf("%v", err)
	}
}

func TestAPIRemoteFailed(t *testing.T) {
	for said, want := range map[string]string{
		"nerdr: no command \"api\"":                                "too old for its API",
		"sh: 1: nerdr: not found":                                  "no nerdr on box",
		"me@box: Permission denied (publickey).":                   "needs a key that logs in by itself",
		"nerdr api: no nerdr runs for /w: start nerdr there first": "no nerdr runs for /w",
	} {
		if got := apiRemoteFailed("box", said, io.EOF).Error(); !strings.Contains(got, want) {
			t.Errorf("%q: %q", said, got)
		}
	}
}

// TestServersSSHArgv: a server's API is reached as nerdr api there, never
// --servers, with nothing forwarded and no password asked, from the first
// try.
func TestServersSSHArgv(t *testing.T) {
	t.Setenv(EnvSSH, "")
	argv := sshNerdr("box", "api", "~/src", true)
	for _, o := range []string{"-T", "ForwardAgent=no", "ForwardX11=no", "BatchMode=yes", "ConnectTimeout=10"} {
		if !strings.Contains(strings.Join(argv, " "), o) {
			t.Errorf("no %s: %q", o, argv)
		}
	}
	if last := argv[len(argv)-1]; last != `exec "$SHELL" -lc "exec nerdr api '~/src'"` {
		t.Fatalf("%q", last)
	}
}

// noSSH isolates nerdr api --servers: a config with a server, a fake ssh
// that leaves a mark, and its own socket folder. It is the mark's path.
func noSSH(t *testing.T) (ran, root string) {
	dir := t.TempDir()
	ran = filepath.Join(dir, "ran")
	ssh := filepath.Join(dir, "ssh")
	if err := os.WriteFile(ssh, []byte("#!/bin/sh\ntouch "+ran+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvSSH, ssh)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	_ = os.MkdirAll(filepath.Join(dir, "config", "nerdr"), 0o755)
	if err := os.WriteFile(filepath.Join(dir, "config", "nerdr", "config.toml"), []byte("[[servers]]\nname = \"box\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run, err := os.MkdirTemp("", "nsrv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(run) })
	t.Setenv(attach.EnvDir, run)
	t.Setenv("NERDR", "")
	t.Setenv("NERDR_TAB", "")
	t.Setenv("NERDR_SHELL", "")
	t.Setenv(core.EnvByAgent, "")
	root, _ = filepath.EvalSymlinks(t.TempDir())
	return ran, root
}

// TestServersRefusedWithoutLocalWelcome: before any ssh, nerdr api
// --servers must be let in by this machine's core: not from a program in
// a tab, not with no core running, not when the core refuses it.
func TestServersRefusedWithoutLocalWelcome(t *testing.T) {
	ran, root := noSSH(t)
	check := func(what string) {
		t.Helper()
		if code := runAPIServers(root); code != 1 {
			t.Errorf("%s: exit %d", what, code)
		}
		if _, err := os.Stat(ran); err == nil {
			t.Fatalf("%s: ssh ran", what)
		}
	}
	check("no core")

	t.Setenv("NERDR_TAB", "t1")
	check("in a tab")
	t.Setenv("NERDR_TAB", "")

	sock, keyFile, err := attach.APIPaths(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := attach.WriteKey(keyFile); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	answers := make(chan string, 10)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = bufio.NewReader(c).ReadBytes('\n')
			_, _ = c.Write([]byte(<-answers + "\n"))
			_ = c.Close()
		}
	}()
	answers <- `{"error":"nerdr refuses a connection from a program it runs"}`
	check("refused")
	answers <- `{"api":1,"version":"v1"}`
	check("no root")
	answers <- `{"api":1,"version":"v1","root":"/w","machines":[]}`
	check("another aggregator")
	answers <- ``
	check("nothing said")
}

// TestServersInvalidRequest: a request the core could not read, a field
// of the wrong type, is answered here by its id, never sent on; with the
// input ended, the aggregator ends.
func TestServersInvalidRequest(t *testing.T) {
	h := startServers(t)
	h.next()
	h.send(`{"id":7,"machine":"local","op":"read","lines":"bad"}`)
	if v, _ := h.next(); string(v["id"]) != "7" || str(v["machine"]) != "local" || !strings.Contains(str(v["error"]), "not a request") {
		t.Fatalf("%v", v)
	}
	select {
	case r := <-h.local.reqs:
		t.Fatalf("sent on: %v", r)
	default:
	}
	_ = h.in.Close()
	select {
	case code := <-h.exit:
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not end")
	}
}

// TestServersNoEventOlderThanState: an event the core sent before the
// state the client is given, of an agent working that the state has
// blocked, is never passed on after it, as the client first subscribes
// and as a server is connected to again; what changes after is told.
func TestServersNoEventOlderThanState(t *testing.T) {
	agent := func(state string) core.APITab {
		return core.APITab{ID: "t1", Name: "fix", Agents: []core.APIAgent{{ID: "t1", Num: 1, Kind: "claude", State: state}}}
	}
	h := startServers(t, "box")
	h.mu.Lock()
	h.prepare = func(fc *fakeCore) {
		fc.set(func(st *core.APIState) { st.Tabs = []core.APITab{agent("blocked")} })
		working := agent("working")
		fc.smu.Lock()
		fc.onSubscribe = []any{api.Event{Event: "agent", TabID: "t1", Name: "fix", Agent: &working.Agents[0], Was: "idle"}}
		fc.smu.Unlock()
	}
	h.mu.Unlock()
	h.next()
	box := h.core("box")
	// The first was dialed before prepare was set.
	h.prepare(box)
	h.waitConnected("box")

	check := func(fc *fakeCore) {
		t.Helper()
		ev := h.machineEvent("box", statusConnected)
		var st core.APIState
		if err := json.Unmarshal(ev["state"], &st); err != nil || len(st.Tabs) != 1 || st.Tabs[0].Agents[0].State != "blocked" {
			t.Fatalf("state %s", ev["state"])
		}
		fc.set(func(st *core.APIState) { st.Tabs = []core.APITab{agent("done")} })
		fc.line(map[string]any{"event": "agent"})
		v := h.until("an agent event", func(v map[string]json.RawMessage) bool { return str(v["event"]) == "agent" })
		var ag core.APIAgent
		_ = json.Unmarshal(v["agent"], &ag)
		if ag.State != "done" || str(v["was"]) != "blocked" || str(v["machine"]) != "box" {
			t.Fatalf("after the state: %v", v)
		}
	}
	h.send(`{"op":"subscribe"}`)
	check(box)
	box.kill()
	check(h.core("box"))
}

// TestServersOffWhileConnected: a server the user disconnects from in the
// TUI while it is connected here is disconnected from here too, its
// request answered so, and connected to again once the user does there.
func TestServersOffWhileConnected(t *testing.T) {
	h := startServers(t, "box")
	h.next()
	box := h.core("box")
	h.waitConnected("box")
	h.send(`{"op":"subscribe"}`)
	h.machineEvent("box", statusConnected)
	h.send(`{"id":1,"machine":"box","op":"hang"}`)
	box.next(t)

	h.setOff("box", true)
	var answered, down bool
	for !answered || !down {
		v, _ := h.next()
		switch {
		case string(v["id"]) == "1":
			if !strings.Contains(str(v["error"]), "not known") {
				t.Fatalf("%v", v)
			}
			answered = true
		case str(v["event"]) == "machine" && str(v["status"]) == statusDown:
			if str(v["error"]) != offWhy {
				t.Fatalf("%v", v)
			}
			down = true
		}
	}
	h.send(`{"id":2,"machine":"box","op":"state"}`)
	if v := h.until("the answer", func(v map[string]json.RawMessage) bool { return string(v["id"]) == "2" }); !strings.Contains(str(v["error"]), "disconnected from in nerdr's window") {
		t.Fatalf("%v", v)
	}
	select {
	case fc := <-h.cores["box"]:
		t.Fatalf("dialed while off: %v", fc.name)
	case <-time.After(100 * time.Millisecond):
	}

	h.setOff("box", false)
	h.core("box")
	h.machineEvent("box", statusConnected)
}
