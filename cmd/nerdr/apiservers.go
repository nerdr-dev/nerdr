package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nerdr-dev/nerdr/internal/api"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// `nerdr api --servers [FOLDER]` is the API of this machine's core and of
// the core of each of the [[servers]], through one standard input and
// output: so one program, the connector of a phone app, reaches the tabs
// of every machine through this one. It is a process of its own, beside
// the cores; no core knows of the others. Each server is reached as
// `nerdr api FOLDER` there, over ssh: one hop, never --servers again.
//
// IDs mean something in their own core only (a question's is a number a
// core counts again from when it starts; a tab's, an agent's, the root
// and the agents to start are each core's), so nothing is translated or
// merged: each request names its machine, and each answer and event says
// whose it is. The client puts the machines' states together itself.
//
// Before any ssh, it is as careful as the TUI is: not from a program
// nerdr runs (remoteAllowed), and only after this machine's core has
// welcomed it, the core refusing a program it runs, an agent above all.
// The connection here stays open; when it closes, this ends.

// localMachine is this machine's name among the machines; a server of
// that name is left out.
const localMachine = "local"

// How long a machine has to welcome, and how long a connection must last
// for the wait before connecting again to start short again.
const (
	localWelcomeTimeout  = 10 * time.Second
	remoteWelcomeTimeout = 30 * time.Second
	retryReset           = time.Minute
)

// offWhy is why a server disconnected from in the TUI is down.
const offWhy = "disconnected from in nerdr's window: connect to it there to reach it here"

// Machine statuses.
const (
	statusConnected  = "connected"
	statusConnecting = "connecting"
	statusDown       = "down"
)

// machineInfo is a machine as the client is told of it: in the welcome,
// machines' answer, and machine events.
type machineInfo struct {
	Machine string `json:"machine"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
	API     int    `json:"api,omitempty"`
	Version string `json:"version,omitempty"`
	Root    string `json:"root,omitempty"`
}

// serversWelcome is the first line out: API is this nerdr's, and the
// machines, this one first.
type serversWelcome struct {
	API      int           `json:"api"`
	Version  string        `json:"version"`
	Machines []machineInfo `json:"machines"`
}

// machineEvent is a machine coming, going or failing to come; State is
// its state, the client being subscribed to it, as it connects.
type machineEvent struct {
	Event string `json:"event"`
	machineInfo
	State json.RawMessage `json:"state,omitempty"`
}

// errUnknownOutcome is the answer to a request whose machine went away
// before it answered: it may or may not have been done, and is never
// sent again by itself.
const errUnknownOutcome = "the connection to %s was lost before it answered: whether it was done is not known; look at its state when it is back"

// transport is a connection to a machine's API, after the hello.
type transport struct {
	in  *bufio.Reader
	wmu sync.Mutex
	out io.Writer
	// close ends the connection; it may be called more than once.
	close func()
	// why is why the connection ended, from err and what else is known
	// (what ssh said).
	why func(err error) error
}

func (t *transport) send(b []byte) error {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	_, err := t.out.Write(append(b, '\n'))
	return err
}

// pending is what is done with the answer to a request sent to a machine:
// ans is nil when the machine went away first.
type pending func(ans map[string]json.RawMessage)

// machine is a machine the aggregator reaches.
type machine struct {
	info   machineInfo
	server config.Server
	// fixed is set for a server that is never tried (its folder cannot be
	// named over ssh); info.Error says why.
	fixed bool

	tr      *transport
	pending map[int64]pending
	// subscribed is set once the core is asked for events on tr. The
	// client is not sent the core's own events of what changed: they are
	// told from states the core gives, in order, so none is older than
	// the state the client has. last is the state the client was last
	// told of, nil until it has one; fetching while a state is on the
	// way, and stale when the core said something changed meanwhile.
	subscribed bool
	last       *core.APIState
	fetching   bool
	stale      bool
	// closedFor is why the connection was closed from here, when it was.
	closedFor string
	// told is the machine as the client was last told of it in an event.
	told machineInfo
}

// aggregator is nerdr api --servers.
type aggregator struct {
	version string
	// dial connects to server m, saying hello; off reports whether the
	// user disconnected from it in the TUI.
	dial func(s config.Server) (*transport, error)
	off  func(name string) bool
	log  io.Writer
	// retryFirst is how long to wait before connecting to a server again,
	// twice as long each time it fails, up to retryMax.
	retryFirst, retryMax time.Duration
	// offEvery is how often whether the user disconnected from a server
	// in the TUI, or connected to it again, is looked at.
	offEvery time.Duration

	omu sync.Mutex
	out io.Writer
	// outErr is closed when writing out fails: the client is gone.
	outErr  chan struct{}
	outOnce sync.Once

	mu       sync.Mutex
	machines []*machine
	subAll   bool
	subs     map[string]bool
	next     atomic.Int64
	inflight sync.WaitGroup

	done     chan struct{}
	doneOnce sync.Once
}

// runAPIServers is `nerdr api --servers FOLDER`.
func runAPIServers(folder string) int {
	fail := func(err error) int {
		fmt.Fprintln(os.Stderr, "nerdr api:", err)
		return 1
	}
	if err := remoteAllowed(); err != nil {
		return fail(err)
	}
	root, err := bridgeRoot(folder)
	if err != nil {
		return fail(err)
	}
	cfg, err := config.Load()
	if err != nil {
		return fail(err)
	}
	c, err := dialAPI(root)
	if err != nil {
		return fail(err)
	}
	defer c.Close()
	in := bufio.NewReaderSize(c, 64<<10)
	_ = c.SetReadDeadline(time.Now().Add(localWelcomeTimeout))
	w, err := readServerWelcome(in)
	if err != nil {
		return fail(fmt.Errorf("this machine's core: %w", err))
	}
	_ = c.SetReadDeadline(time.Time{})
	var once sync.Once
	local := &transport{in: in, out: c,
		close: func() { once.Do(func() { _ = c.Close() }) },
		why:   func(err error) error { return err }}

	a := newAggregator(version(), os.Stdout, os.Stderr)
	a.dial = dialServerAPI
	a.off = func(name string) bool { return slices.Contains(state.LoadClient(root).Off, name) }
	a.addServers(cfg.Servers)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-sig
		a.stop()
	}()
	return a.run(local, w, os.Stdin)
}

func newAggregator(version string, out, log io.Writer) *aggregator {
	return &aggregator{version: version, out: out, log: log, outErr: make(chan struct{}),
		retryFirst: time.Second, retryMax: time.Minute, offEvery: 10 * time.Second,
		subs: map[string]bool{}, done: make(chan struct{})}
}

// addServers adds servers to the machines, after this one, leaving out
// with a word on log those that cannot be told apart by name.
func (a *aggregator) addServers(servers []config.Server) {
	var before []config.Server
	for _, s := range servers {
		err := config.CheckServerName(s.Name, before)
		if err == nil && s.Name == localMachine {
			err = fmt.Errorf("a server cannot be named %s, this machine's name here", localMachine)
		}
		if err != nil {
			fmt.Fprintln(a.log, "nerdr api: left out of [[servers]]:", err)
			continue
		}
		before = append(before, s)
		m := &machine{server: s, info: machineInfo{Machine: s.Name, Status: statusConnecting}}
		if err := s.Check(); err != nil {
			m.fixed = true
			m.info.Status, m.info.Error = statusDown, err.Error()
		}
		a.machines = append(a.machines, m)
	}
}

// readServerWelcome reads a core's welcome from in, and says why it is
// not one this nerdr can go by.
func readServerWelcome(in *bufio.Reader) (api.Welcome, error) {
	line, err := in.ReadBytes('\n')
	if err != nil {
		return api.Welcome{}, fmt.Errorf("%w: %w", errNoWelcome, err)
	}
	return checkWelcome(line)
}

// errNoWelcome is a connection that ended, or timed out, before its
// core's welcome.
var errNoWelcome = errors.New("no welcome")

// checkWelcome is the welcome in line, or why it is not one this nerdr
// can go by: a refusal, another API, or another nerdr api --servers.
func checkWelcome(line []byte) (api.Welcome, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		return api.Welcome{}, fmt.Errorf("not a welcome: %.80q", line)
	}
	if e, ok := raw["error"]; ok {
		var why string
		_ = json.Unmarshal(e, &why)
		if why == "" {
			why = string(e)
		}
		return api.Welcome{}, fmt.Errorf("refused: %s", why)
	}
	if _, ok := raw["machines"]; ok {
		return api.Welcome{}, errors.New("it is nerdr api --servers itself: a server is reached in one hop only")
	}
	var w api.Welcome
	if err := json.Unmarshal(line, &w); err != nil || w.API < 1 || w.Root == "" || w.Version == "" {
		return api.Welcome{}, fmt.Errorf("not a welcome: %.80q", line)
	}
	switch {
	case w.API > core.APIVersion:
		return w, fmt.Errorf("nerdr %s there has API %d, and this nerdr has API %d: update nerdr here", w.Version, w.API, core.APIVersion)
	case w.API < core.APIVersion:
		return w, fmt.Errorf("nerdr %s there has API %d, and this nerdr has API %d: update nerdr there", w.Version, w.API, core.APIVersion)
	}
	return w, nil
}

// run is the aggregator, this machine's core reached on local, which
// welcomed with w, and the client's requests read from in. It ends when
// the client's input has ended and every request is answered, unless it
// subscribed; or when the connection here ends, or the client is gone.
func (a *aggregator) run(local *transport, w api.Welcome, in io.Reader) int {
	lm := &machine{info: machineInfo{Machine: localMachine}}
	a.connected(lm, local, w)
	a.mu.Lock()
	a.machines = append([]*machine{lm}, a.machines...)
	a.mu.Unlock()
	defer a.stop()

	a.write(serversWelcome{API: core.APIVersion, Version: a.version, Machines: a.infos()})

	localGone := make(chan error, 1)
	go func() { localGone <- a.serve(lm, local) }()
	for _, m := range a.machines[1:] {
		if !m.fixed {
			go a.keep(m)
		}
	}
	inputDone := make(chan error, 1)
	go func() { inputDone <- a.read(in) }()

	var drained chan struct{}
	for {
		select {
		case err := <-localGone:
			fmt.Fprintln(a.log, "nerdr api: the connection to this machine's core ended:", err)
			return 1
		case <-a.outErr:
			return 1
		case <-a.done:
			return 0
		case err := <-inputDone:
			if err != nil {
				fmt.Fprintln(a.log, "nerdr api:", err)
				return 1
			}
			drained = make(chan struct{})
			go func(d chan struct{}) {
				a.inflight.Wait()
				close(d)
			}(drained)
		case <-drained:
			a.mu.Lock()
			sub := a.subAll || len(a.subs) > 0
			a.mu.Unlock()
			if !sub {
				return 0
			}
			drained = nil
		}
	}
}

// stop ends the aggregator, closing every connection: the machines' cores
// are not told anything, a client going being no reason to quit.
func (a *aggregator) stop() {
	a.doneOnce.Do(func() {
		close(a.done)
		a.mu.Lock()
		var trs []*transport
		for _, m := range a.machines {
			if m.tr != nil {
				trs = append(trs, m.tr)
			}
		}
		a.mu.Unlock()
		for _, tr := range trs {
			tr.close()
		}
	})
}

func (a *aggregator) stopped() bool {
	select {
	case <-a.done:
		return true
	default:
		return false
	}
}

// write sends v to the client, a line.
func (a *aggregator) write(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	a.omu.Lock()
	defer a.omu.Unlock()
	if _, err := a.out.Write(append(b, '\n')); err != nil {
		a.outOnce.Do(func() { close(a.outErr) })
	}
}

// infos is every machine as it is now.
func (a *aggregator) infos() []machineInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]machineInfo, 0, len(a.machines))
	for _, m := range a.machines {
		out = append(out, m.info)
	}
	return out
}

// subscribedTo reports whether the client wants the events of machine
// name. a.mu is held.
func (a *aggregator) subscribedTo(name string) bool { return a.subAll || a.subs[name] }

// tell has the client told of m as it is now, if it is subscribed to it
// and was told otherwise last. a.mu is held.
func (a *aggregator) tell(m *machine, st json.RawMessage) *machineEvent {
	if !a.subscribedTo(m.info.Machine) || m.told == m.info && st == nil {
		return nil
	}
	m.told = m.info
	return &machineEvent{Event: "machine", machineInfo: m.info, State: st}
}

// setDown has m down for why.
func (a *aggregator) setDown(m *machine, why string) {
	a.mu.Lock()
	m.info = machineInfo{Machine: m.info.Machine, Status: statusDown, Error: why}
	ev := a.tell(m, nil)
	a.mu.Unlock()
	if ev != nil {
		a.write(ev)
	}
}

// keep keeps server m connected, connecting again after it is lost,
// waiting longer each time it fails, until the aggregator stops.
func (a *aggregator) keep(m *machine) {
	wait := a.retryFirst
	sleep := func(d time.Duration) bool {
		select {
		case <-a.done:
			return false
		case <-time.After(d):
			return true
		}
	}
	for !a.stopped() {
		name := m.server.Name
		if a.off != nil && a.off(name) {
			a.setDown(m, offWhy)
			if !sleep(a.offEvery) {
				return
			}
			continue
		}
		tr, err := a.dial(m.server)
		var w api.Welcome
		if err == nil {
			w, err = a.welcome(tr)
			if err != nil {
				tr.close()
				// Why no welcome came is what ssh said; one that came is
				// its own reason.
				if errors.Is(err, errNoWelcome) {
					err = tr.why(err)
				}
			}
		}
		if err != nil {
			fmt.Fprintf(a.log, "nerdr api: %s: %v\n", name, err)
			a.setDown(m, err.Error())
			if !sleep(wait) {
				return
			}
			wait = min(wait*2, a.retryMax)
			continue
		}
		since := time.Now()
		a.connected(m, tr, w)
		if a.stopped() {
			tr.close()
			return
		}
		// Disconnected from in the TUI meanwhile, it is so here too.
		served := make(chan struct{})
		go func() {
			t := time.NewTicker(a.offEvery)
			defer t.Stop()
			for {
				select {
				case <-served:
					return
				case <-t.C:
				}
				if a.off != nil && a.off(name) {
					a.mu.Lock()
					m.closedFor = offWhy
					a.mu.Unlock()
					tr.close()
					return
				}
			}
		}()
		err = a.serve(m, tr)
		close(served)
		fmt.Fprintf(a.log, "nerdr api: %s: connection lost: %v\n", name, err)
		if time.Since(since) > retryReset {
			wait = a.retryFirst
		}
		if !sleep(wait) {
			return
		}
		wait = min(wait*2, a.retryMax)
	}
}

// welcome reads the welcome on tr, within a while.
func (a *aggregator) welcome(tr *transport) (api.Welcome, error) {
	t := time.AfterFunc(remoteWelcomeTimeout, tr.close)
	w, err := readServerWelcome(tr.in)
	if !t.Stop() && err != nil {
		err = fmt.Errorf("%w within %s", errNoWelcome, remoteWelcomeTimeout)
	}
	return w, err
}

// connected has m connected on tr, its core having welcomed with w; it is
// subscribed to, and its state sent the client, when the client wants its
// events: meanwhile, as what is sent waits for what comes to be read.
func (a *aggregator) connected(m *machine, tr *transport, w api.Welcome) {
	a.mu.Lock()
	m.tr, m.pending = tr, map[int64]pending{}
	m.subscribed, m.last, m.fetching, m.stale = false, nil, false, false
	m.closedFor = ""
	m.info = machineInfo{Machine: m.info.Machine, Status: statusConnected, API: w.API, Version: w.Version, Root: w.Root}
	sync := a.subscribedTo(m.info.Machine)
	a.mu.Unlock()
	if sync {
		go a.sync(m)
	}
}

// sync subscribes to m's events and sends the client its state, in a
// machine event.
func (a *aggregator) sync(m *machine) {
	a.mu.Lock()
	if m.tr == nil || m.subscribed {
		a.mu.Unlock()
		return
	}
	m.subscribed, m.fetching = true, true
	tr := m.tr
	a.mu.Unlock()
	a.ask(m, map[string]json.RawMessage{"op": json.RawMessage(`"subscribe"`)}, func(ans map[string]json.RawMessage) {
		if e, ok := ans["error"]; ok {
			fmt.Fprintf(a.log, "nerdr api: %s: %s\n", m.info.Machine, e)
			tr.close()
		}
	})
	a.fetch(m, tr)
}

// fetch asks m's core, on tr, for its state, and tells the client what
// changed since the state it has, or, when it has none, sends it the
// state in a machine event. One is fetched at a time: the core saying
// something changed meanwhile, it is fetched again after. a.mu is not
// held, and m.fetching is set.
func (a *aggregator) fetch(m *machine, tr *transport) {
	a.ask(m, map[string]json.RawMessage{"op": json.RawMessage(`"state"`)}, func(ans map[string]json.RawMessage) {
		if ans == nil {
			return
		}
		var st core.APIState
		if _, bad := ans["error"]; bad || json.Unmarshal(ans["result"], &st) != nil {
			fmt.Fprintf(a.log, "nerdr api: %s: no state: %.200s\n", m.info.Machine, ans["error"])
			tr.close()
			return
		}
		a.mu.Lock()
		if m.tr != tr {
			a.mu.Unlock()
			return
		}
		var lines []any
		if m.last == nil {
			if ev := a.tell(m, ans["result"]); ev != nil {
				lines = append(lines, ev)
			}
		} else {
			for _, ev := range api.Diff(*m.last, st) {
				lines = append(lines, machineTagged{Machine: m.info.Machine, Event: ev})
			}
		}
		m.last = &st
		again := m.stale
		m.fetching, m.stale = again, false
		a.mu.Unlock()
		for _, l := range lines {
			a.write(l)
		}
		if again {
			go a.fetch(m, tr)
		}
	})
}

// machineTagged is an event of a machine's, saying whose.
type machineTagged struct {
	Machine string `json:"machine"`
	api.Event
}

// MarshalJSON is the event as it marshals itself, with machine added: the
// Event's own MarshalJSON, promoted, would leave Machine out.
func (t machineTagged) MarshalJSON() ([]byte, error) {
	b, err := json.Marshal(t.Event)
	if err != nil {
		return nil, err
	}
	name, err := json.Marshal(t.Machine)
	if err != nil {
		return nil, err
	}
	if string(b) == "{}" {
		return append(append([]byte(`{"machine":`), name...), '}'), nil
	}
	return append(append(append([]byte(`{"machine":`), name...), ','), b[1:]...), nil
}

// ask sends request r to m, then for its answer. It is false, and then is
// not called, when m is not connected.
func (a *aggregator) ask(m *machine, r map[string]json.RawMessage, then pending) bool {
	id := a.next.Add(1)
	r["id"] = json.RawMessage(fmt.Sprint(id))
	b, err := json.Marshal(r)
	if err != nil {
		return false
	}
	a.mu.Lock()
	tr := m.tr
	if tr == nil {
		a.mu.Unlock()
		return false
	}
	m.pending[id] = then
	a.mu.Unlock()
	if err := tr.send(b); err != nil {
		// It may have gone in part: what it did is not known.
		tr.close()
		a.mu.Lock()
		f := m.pending[id]
		if m.tr == tr {
			delete(m.pending, id)
		} else {
			f = nil
		}
		a.mu.Unlock()
		if f != nil {
			f(nil)
		}
	}
	return true
}

// serve passes on what m's core sends on tr until the connection ends;
// then m is down, and the requests it did not answer are answered so.
func (a *aggregator) serve(m *machine, tr *transport) error {
	var err error
	for {
		var line []byte
		line, err = tr.in.ReadBytes('\n')
		if len(line) > 0 && (err == nil || err == io.EOF) {
			a.fromMachine(m, line)
		}
		if err != nil {
			break
		}
	}
	tr.close()
	why := tr.why(err)
	a.mu.Lock()
	lost := m.pending
	m.tr, m.pending, m.last = nil, nil, nil
	if m.closedFor != "" {
		why = errors.New(m.closedFor)
	}
	a.mu.Unlock()
	a.setDown(m, why.Error())
	for _, f := range lost {
		f(nil)
	}
	return why
}

// fromMachine passes on a line m's core sent: an event, or an answer.
func (a *aggregator) fromMachine(m *machine, line []byte) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(line, &obj); err != nil {
		fmt.Fprintf(a.log, "nerdr api: %s sent what is not JSON: %.80q\n", m.info.Machine, line)
		return
	}
	name := m.info.Machine
	if ev, ok := obj["event"]; ok {
		a.mu.Lock()
		if !a.subscribedTo(name) {
			a.mu.Unlock()
			return
		}
		if str := string(ev); str == `"notice"` {
			// Not of the state: passed on as it is.
			a.mu.Unlock()
			obj["machine"], _ = json.Marshal(name)
			a.write(obj)
			return
		}
		// Something changed, or events were lost: told from the state.
		tr := m.tr
		if m.fetching || tr == nil {
			m.stale = true
			a.mu.Unlock()
			return
		}
		m.fetching = true
		a.mu.Unlock()
		// Not from here, which reads what the core sends: asking waits
		// for it to read, which may wait for this to.
		go a.fetch(m, tr)
		return
	}
	var id int64
	if err := json.Unmarshal(obj["id"], &id); err != nil {
		fmt.Fprintf(a.log, "nerdr api: %s: %.200s\n", name, line)
		return
	}
	a.mu.Lock()
	f := m.pending[id]
	delete(m.pending, id)
	a.mu.Unlock()
	if f != nil {
		f(obj)
	}
}

// read answers the client's requests, read from in, until it ends.
func (a *aggregator) read(in io.Reader) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), api.MaxLine)
	for sc.Scan() {
		if a.stopped() {
			return nil
		}
		a.request(sc.Bytes())
	}
	if err := sc.Err(); errors.Is(err, bufio.ErrTooLong) {
		a.write(api.Answer{Error: fmt.Sprintf("a line is at most %d bytes", api.MaxLine)})
		return err
	} else if err != nil {
		return err
	}
	return nil
}

// request answers one of the client's requests, or sends it on to its
// machine to answer.
func (a *aggregator) request(line []byte) {
	var r map[string]json.RawMessage
	if err := json.Unmarshal(line, &r); err != nil || r == nil {
		if err == nil {
			err = errors.New("not an object")
		}
		a.write(api.Answer{Error: "not a request: " + err.Error()})
		return
	}
	id, hasID := r["id"]
	rawMachine, hasMachine := r["machine"]
	answer := func(result any, why string) {
		out := map[string]any{}
		if hasID {
			out["id"] = id
		}
		if hasMachine {
			out["machine"] = rawMachine
		}
		if why != "" {
			out["error"] = why
		} else {
			out["result"] = result
		}
		a.write(out)
	}
	var op, name string
	if err := json.Unmarshal(r["op"], &op); err != nil && r["op"] != nil {
		answer(nil, "op is a string")
		return
	}
	if hasMachine {
		if err := json.Unmarshal(rawMachine, &name); err != nil {
			answer(nil, "machine is a machine's name, a string")
			return
		}
	}
	switch {
	case op == "":
		answer(nil, "a request needs an op")
		return
	case op == "machines":
		answer(map[string]any{"machines": a.infos()}, "")
		return
	case op == "subscribe" && name == "":
		answer(struct{}{}, "")
		a.subscribe("")
		return
	case name == "" && op == "quit":
		answer(nil, "quit stops nerdr and every program it runs on a machine: say which, with machine")
		return
	case name == "":
		answer(nil, "a request needs a machine: "+a.names())
		return
	}
	a.mu.Lock()
	var m *machine
	for _, mm := range a.machines {
		if mm.info.Machine == name {
			m = mm
		}
	}
	var info machineInfo
	if m != nil {
		info = m.info
	}
	a.mu.Unlock()
	switch {
	case m == nil:
		answer(nil, fmt.Sprintf("no machine %q: %s", name, a.names()))
		return
	case info.Status == statusConnecting:
		answer(nil, name+" is not connected yet")
		return
	case info.Status != statusConnected:
		answer(nil, name+" is not connected: "+info.Error)
		return
	case op == "subscribe":
		answer(struct{}{}, "")
		a.subscribe(name)
		return
	}
	delete(r, "machine")
	// As the core reads it: one it cannot is answered here, by the id
	// sent, the core's answer to it having none.
	b, _ := json.Marshal(r)
	if err := json.Unmarshal(b, new(api.Request)); err != nil {
		answer(nil, "not a request: "+err.Error())
		return
	}
	a.inflight.Add(1)
	sent := a.ask(m, r, func(ans map[string]json.RawMessage) {
		defer a.inflight.Done()
		if ans == nil {
			answer(nil, fmt.Sprintf(errUnknownOutcome, name))
			return
		}
		delete(ans, "id")
		if hasID {
			ans["id"] = id
		}
		ans["machine"] = rawMachine
		a.write(ans)
	})
	if !sent {
		a.inflight.Done()
		answer(nil, name+" is not connected")
	}
}

// subscribe has the client told the events of machine name, or of every
// machine for "", from now on and after each connects again; each one
// connected is subscribed to now, and its state sent in a machine event.
func (a *aggregator) subscribe(name string) {
	a.mu.Lock()
	if name == "" {
		a.subAll = true
	} else {
		a.subs[name] = true
	}
	var now []*machine
	var evs []*machineEvent
	for _, m := range a.machines {
		if name != "" && m.info.Machine != name {
			continue
		}
		if m.tr != nil {
			now = append(now, m)
		} else if ev := a.tell(m, nil); ev != nil {
			evs = append(evs, ev)
		}
	}
	a.mu.Unlock()
	for _, ev := range evs {
		a.write(ev)
	}
	for _, m := range now {
		a.sync(m)
	}
}

// names lists the machines, for a request that names none or another.
func (a *aggregator) names() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var n []string
	for _, m := range a.machines {
		n = append(n, m.info.Machine)
	}
	return "the machines are " + strings.Join(n, ", ")
}

// dialServerAPI connects to the API of server s's core, running nerdr api
// there over ssh, which never asks for a password: there is no terminal
// to ask on, and BatchMode is on from the first try.
func dialServerAPI(s config.Server) (*transport, error) {
	if err := s.Check(); err != nil {
		return nil, err
	}
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, err
	}
	argv := sshNerdr(s.Host(), "api", s.Dir(), true)
	cmd := exec.Command(argv[0], argv[1:]...)
	stderr := &tail{}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, stderr
	// A session of its own: no terminal ssh could open to ask on.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = cmd.Start()
	inR.Close()
	outW.Close()
	if err != nil {
		inW.Close()
		outR.Close()
		return nil, err
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	var once sync.Once
	closeIt := func() {
		once.Do(func() {
			_ = inW.Close()
			_ = outR.Close()
			_ = cmd.Process.Kill()
		})
	}
	return &transport{in: bufio.NewReaderSize(outR, 64<<10), out: inW, close: closeIt,
		why: func(err error) error {
			closeIt()
			// What ssh said as it ended is the reason, when it ends.
			select {
			case <-exited:
			case <-time.After(3 * time.Second):
			}
			return apiRemoteFailed(s.Host(), stderr.String(), err)
		}}, nil
}

// apiRemoteFailed says why nerdr api on host ended, or never welcomed,
// from what ssh said, and err.
func apiRemoteFailed(host, said string, err error) error {
	last := said
	if i := strings.LastIndex(said, "\n"); i >= 0 {
		last = said[i+1:]
	}
	last = strings.TrimPrefix(last, "nerdr api: ")
	switch {
	case strings.Contains(said, `no command "api"`):
		return fmt.Errorf("nerdr on %s is too old for its API: update nerdr on %s", host, host)
	case strings.Contains(said, "nerdr: not found") || strings.Contains(said, "nerdr: command not found") ||
		strings.Contains(said, "Unknown command: nerdr"):
		return fmt.Errorf("no nerdr on %s, or not on the PATH of a login shell there: install nerdr on %s", host, host)
	case strings.Contains(said, "Permission denied") || strings.Contains(said, "Host key verification failed"):
		return fmt.Errorf("%s (ssh asks nothing here: the server needs a key that logs in by itself, and a known host key)", last)
	case last != "":
		return errors.New(last)
	case err == nil || err == io.EOF:
		return errors.New("the connection ended")
	}
	return err
}
