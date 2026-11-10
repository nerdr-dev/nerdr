package app

import (
	"errors"
	"os"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"

	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/fleet"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// The servers are the machines of [[servers]], each reached over ssh: the
// TUI connects to them as it starts, after this machine's core, and from
// the Machines dialog, where they are added, disconnected, removed and
// connected to again.

// Dialer connects to the core of server s over ssh. again is a connection
// that never asks for a password; tty, when it is not nil, is the
// terminal ssh asks on, the TUI paused meanwhile (see login).
type Dialer func(s config.Server, again bool, tty *os.File) (*attach.Conn, error)

// SetDial gives the TUI its way to the servers' cores.
func (m *Model) SetDial(d Dialer) { m.dial = d }

// AddServer has the TUI show server s too, its core reached over conn,
// or grey for err when it could not be: as the TUI starts, the fleet not
// locked. What comes of it is for the program to run.
func (m *Model) AddServer(s config.Server, conn *attach.Conn, err error) tea.Cmd {
	mc := m.serverMachine(s)
	if err != nil {
		mc.Gone = err.Error()
	}
	m.fleet.Lock()
	defer m.fleet.Unlock()
	cmd, _ := m.joinMachine(mc, conn)
	return cmd
}

// serverMachine is the machine of server s, not yet the fleet's: tried
// again without a password when its connection closes.
func (m *Model) serverMachine(s config.Server) *fleet.Machine {
	mc := newMachine(s.Name, s.Host(), *m.cfg())
	if dial := m.dial; dial != nil {
		mc.Redial = func() (*attach.Conn, error) { return dial(s, true, nil) }
	}
	return mc
}

// server is the server of machine mc, as [[servers]] has it.
func (m *Model) server(mc *fleet.Machine) (config.Server, bool) {
	for _, s := range m.cfg().Servers {
		if s.Name == mc.Name && mc.Name != "" {
			return s, true
		}
	}
	return config.Server{}, false
}

// errDisconnected is why a server the user disconnected from is grey.
var errDisconnected = errors.New("disconnected: its agents work on")

// disconnect closes the connection to the core of mc, its agents working
// on: grey until connected to again. Not the last machine left.
func (m *Model) disconnect(mc *fleet.Machine) {
	if !m.othersLeft(mc) {
		m.toast("the last machine left: close nerdr instead")
		return
	}
	mc.Gone, mc.Away = errDisconnected.Error(), false
	if mc.Conn != nil {
		mc.Conn.Close("")
	}
	m.fleet.Bump()
	m.syncInput()
}

// removeServer disconnects from mc and takes it out of [[servers]] and of
// the TUI: its tabs go, its agents working on there.
func (m *Model) removeServer(mc *fleet.Machine) tea.Cmd {
	if mc.Name == "" {
		m.toast("this machine is no server to remove")
		return nil
	}
	if !m.othersLeft(mc) {
		m.toast("the last machine left: close nerdr instead")
		return nil
	}
	if err := config.RemoveServer(mc.Name); err != nil {
		m.toast("could not remove " + mc.Name + ": " + err.Error())
		return nil
	}
	m.disconnect(mc)
	cur := m.Cur()
	if m.split != nil && m.machineOf(m.split) == mc {
		m.split = nil
	}
	m.fleet.Remove(mc)
	mc.Core.Mu.Unlock() // the fleet's no longer, nor locked with it
	if i := m.fleet.TabIndex(cur); i >= 0 {
		m.Active = i
	} else {
		m.Active = min(m.Active, max(len(m.fleet.Tabs())-1, 0))
	}
	cfg := m.cfg()
	cfg.Servers = deleteServer(cfg.Servers, mc.Name)
	m.toast("removed " + mc.Name + ": its agents work on there")
	return tea.Batch(m.selectTab(m.Active), m.resize())
}

func deleteServer(ss []config.Server, name string) []config.Server {
	var out []config.Server
	for _, s := range ss {
		if s.Name != name {
			out = append(out, s)
		}
	}
	return out
}

// login connects to server s while the TUI shows ssh logging in: ssh gets
// a terminal of its own, a pty, whose output a dialog shows, and what is
// typed goes there (see syncInput), so ssh may ask for a password. The
// TUI keeps the terminal: bubbletea, reading it through the input router,
// would not read it again once let go. Then serverDialedMsg tells what
// came of it; the prefix and esc gives up.
func (m *Model) login(s config.Server) tea.Cmd {
	if m.dial == nil {
		m.toast("nerdr cannot reach other machines here")
		return nil
	}
	d, err := newLoginDialog(s, m.dial, m.width-10, m.height-10)
	if err != nil {
		m.toast(s.Name + ": " + err.Error())
		return nil
	}
	if old := m.loggingIn; old != nil {
		old.close()
	}
	m.loggingIn = d
	m.dialog = d
	m.syncInput()
	return tea.Batch(d.dialCmd(), d.readCmd())
}

// serverDialedMsg is what came of connecting to server s from the TUI.
type serverDialedMsg struct {
	s    config.Server
	conn *attach.Conn
	err  error
	// login is the dialog it was shown in, if any.
	login *loginDialog
}

// loginDialog shows ssh logging in to a server: what it writes on its
// terminal, the pty, to which what is typed goes.
type loginDialog struct {
	s    config.Server
	dial Dialer
	ptmx *os.File
	tty  *os.File

	mu     sync.Mutex
	out    []byte
	closed bool
}

func newLoginDialog(s config.Server, dial Dialer, w, h int) (*loginDialog, error) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		return nil, err
	}
	_ = pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(max(w, 40)), Rows: uint16(max(h, 5))})
	return &loginDialog{s: s, dial: dial, ptmx: ptmx, tty: tty}, nil
}

// dialCmd connects, ssh asking on the dialog's terminal; what came of it
// is a serverDialedMsg.
func (d *loginDialog) dialCmd() tea.Cmd {
	return func() tea.Msg {
		conn, err := d.dial(d.s, false, d.tty)
		_ = d.tty.Close()
		return serverDialedMsg{s: d.s, conn: conn, err: err, login: d}
	}
}

// loginOutputMsg is that ssh wrote on the dialog's terminal; done once it
// is closed.
type loginOutputMsg struct {
	d    *loginDialog
	done bool
}

// readCmd reads what ssh writes next on its terminal.
func (d *loginDialog) readCmd() tea.Cmd {
	return func() tea.Msg {
		buf := make([]byte, 4096)
		n, err := d.ptmx.Read(buf)
		d.mu.Lock()
		d.out = append(d.out, buf[:n]...)
		if len(d.out) > 16<<10 {
			d.out = d.out[len(d.out)-16<<10:]
		}
		d.mu.Unlock()
		return loginOutputMsg{d: d, done: err != nil}
	}
}

// write sends what is typed to ssh's terminal.
func (d *loginDialog) write(b []byte) { _, _ = d.ptmx.Write(b) }

// close ends the dialog's terminal: ssh, still asking, ends with it.
func (d *loginDialog) close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.closed {
		d.closed = true
		_ = d.ptmx.Close()
	}
}

// text is the last n lines ssh wrote, as a terminal would show them.
func (d *loginDialog) text(n int) []string {
	d.mu.Lock()
	out := ansi.Strip(string(d.out))
	d.mu.Unlock()
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if i := strings.LastIndexByte(l, '\r'); i >= 0 {
			l = l[i+1:]
		}
		lines = append(lines, l)
	}
	return lines[max(len(lines)-n, 0):]
}

func (d *loginDialog) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	// What is typed goes to ssh by the router; a key here slipped past it
	// while it switched over.
	if b := keyBytes(msg); len(b) > 0 {
		d.write(b)
	}
	return nil
}

func (d *loginDialog) view(m *Model, w, h int) string {
	lines := []string{dim("ssh " + d.s.Host() + ", for " + d.s.Dir() + ":"), ""}
	lines = append(lines, d.text(max(min(h-12, 12), 3))...)
	lines = append(lines, "", dim("What you type goes to ssh. ")+m.pk("esc")+dim(" gives up."))
	return box("Connecting to "+d.s.Name, lines, w)
}

// onServerDialed takes what came of connecting to a server from the TUI:
// the machine there is shown, connected or grey, a new one last.
func (m *Model) onServerDialed(msg serverDialedMsg) tea.Cmd {
	if d := msg.login; d != nil {
		d.close()
		if m.dialog == d {
			m.dialog = nil
		}
		if m.loggingIn == d {
			m.loggingIn = nil
		}
		m.syncInput()
	}
	mc := m.fleet.Named(msg.s.Name)
	if mc == nil {
		mc = m.serverMachine(msg.s)
		if msg.err != nil {
			mc.Gone = msg.err.Error()
		}
		cmd, err := m.joinMachine(mc, msg.conn)
		if err != nil || msg.err != nil {
			m.toast(msg.s.Name + ": " + mc.Gone)
		} else {
			m.toast("connected to " + msg.s.Name)
		}
		return tea.Batch(cmd, m.resize())
	}
	if msg.err != nil {
		mc.Gone = msg.err.Error()
		m.toast(msg.s.Name + ": " + mc.Gone)
		m.fleet.Bump()
		return nil
	}
	mc.Gone, mc.Away = "", true
	return m.coreBack(mc, msg.conn)
}

// machinesDialog lists the machines the TUI shows, with what can be done
// to each: connect again, disconnect, remove; and adds a server.
type machinesDialog struct {
	cursor int
	// adding is set while a server is being added: its name, ssh host
	// and folder, field the one being typed.
	adding bool
	inputs [3]textinput.Model
	field  int
	err    string
}

func (m *Model) openMachines() tea.Cmd {
	m.dialog = &machinesDialog{}
	return nil
}

func (d *machinesDialog) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	if d.adding {
		return d.addKey(m, msg)
	}
	ms := m.fleet.Machines()
	d.cursor = min(d.cursor, len(ms)-1)
	mc := ms[d.cursor]
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		m.dialog = nil
	case "up", "k":
		d.cursor = max(d.cursor-1, 0)
	case "down", "j":
		d.cursor = min(d.cursor+1, len(ms)-1)
	case "a":
		d.startAdding()
		return textinput.Blink
	case "enter":
		for i, t := range m.fleet.Tabs() {
			if m.machineOf(t) == mc {
				m.dialog = nil
				return m.selectTab(i)
			}
		}
	case "r":
		if s, ok := m.server(mc); ok && mc.Gone != "" && !takenOver(mc) {
			m.dialog = nil
			return m.login(s)
		}
		return m.greyKey(mc, "r")
	case "d":
		if mc.Name == "" || mc.Gone != "" {
			return nil
		}
		m.disconnect(mc)
	case "x":
		if _, ok := m.server(mc); !ok {
			d.err = machineLabel(mc) + " is not one of [[servers]]"
			return nil
		}
		m.dialog = &confirm{
			title:     "Remove " + mc.Name + "?",
			lines:     []string{"Its tabs leave nerdr here, and it leaves [[servers]].", "Its agents work on there; nerdr connect " + mc.Host + " shows them."},
			defaultNo: true,
			yesLabel:  "Remove", noLabel: "Cancel",
			yes: func(m *Model) tea.Cmd { return m.removeServer(mc) },
		}
	}
	return nil
}

func (d *machinesDialog) startAdding() {
	d.adding, d.field, d.err = true, 0, ""
	for i, p := range []struct{ prompt, hold string }{
		{"name:   ", "box: as the tab bar shows it"},
		{"ssh:    ", "the host as ssh knows it; the name if empty"},
		{"folder: ", "~"},
	} {
		in := textinput.New()
		in.Prompt, in.Placeholder, in.CharLimit = p.prompt, p.hold, 200
		d.inputs[i] = in
	}
	d.inputs[0].Focus()
}

func (d *machinesDialog) addKey(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+c":
		d.adding, d.err = false, ""
		return nil
	case "tab", "down":
		d.focus((d.field + 1) % len(d.inputs))
		return nil
	case "shift+tab", "up":
		d.focus((d.field + len(d.inputs) - 1) % len(d.inputs))
		return nil
	case "enter":
		if d.field < len(d.inputs)-1 {
			d.focus(d.field + 1)
			return nil
		}
		s := config.Server{
			Name:   strings.TrimSpace(d.inputs[0].Value()),
			SSH:    strings.TrimSpace(d.inputs[1].Value()),
			Folder: strings.TrimSpace(d.inputs[2].Value()),
		}
		if err := config.AddServer(s); err != nil {
			d.err = err.Error()
			return nil
		}
		cfg := m.cfg()
		cfg.Servers = append(cfg.Servers, s)
		m.dialog = nil
		return m.login(s)
	}
	var cmd tea.Cmd
	d.inputs[d.field], cmd = d.inputs[d.field].Update(msg)
	return cmd
}

func (d *machinesDialog) focus(i int) {
	d.inputs[d.field].Blur()
	d.field = i
	d.inputs[i].Focus()
}

func (d *machinesDialog) view(m *Model, w, _ int) string {
	if d.adding {
		lines := []string{"A server: another machine with nerdr, reached over ssh.", ""}
		for _, in := range d.inputs {
			lines = append(lines, in.View())
		}
		if d.err != "" {
			lines = append(lines, "", warn(d.err))
		}
		lines = append(lines, "", dim("Kept in [[servers]] in "+core.TildePath(config.Path(), core.HomeDir())+"."),
			"", m.keys("tab", "next", "enter", "add and connect", "esc", "back"))
		return box("Add a server", lines, w)
	}
	var lines []string
	for i, mc := range m.fleet.Machines() {
		mark := "  "
		if i == d.cursor {
			mark = keyHint("▸ ")
		}
		name := machineLabel(mc)
		if mc.Host != "" && mc.Host != mc.Name {
			name += dim(" (" + mc.Host + ")")
		}
		state := "connected"
		switch {
		case mc.Gone != "":
			state = mc.Gone
		case mc.Away && mc.Err != "":
			state = mc.Err + " — trying again"
		case mc.Away:
			state = "connecting again…"
		}
		n := 0
		for _, t := range m.fleet.Tabs() {
			if m.machineOf(t) == mc {
				n++
			}
		}
		lines = append(lines, mark+name+dim("  "+core.Plural(n, "tab")+" · "+state))
	}
	if d.err != "" {
		lines = append(lines, "", warn(d.err))
	}
	lines = append(lines, "", m.keys("enter", "go to", "a", "add a server", "r", "connect again", "d", "disconnect", "x", "remove", "esc", "close"))
	return box("Machines", lines, w)
}

// ClientLayout is what the TUI keeps of its own as it ends, for the next
// start (see state.Client): the servers disconnected from, and the tab
// in front and beside it when they are a server's.
func (m *Model) ClientLayout(key string) state.Client {
	m.fleet.Lock()
	defer m.fleet.Unlock()
	c := state.Client{Key: key}
	for _, mc := range m.fleet.Machines() {
		if mc.Name != "" && mc.Gone == errDisconnected.Error() {
			c.Off = append(c.Off, mc.Name)
		}
	}
	at := func(t *core.Tab) state.MachineTab {
		if t == nil {
			return state.MachineTab{}
		}
		if mc := m.machineOf(t); mc.Name != "" {
			return state.MachineTab{Machine: mc.Name, Tab: t.ID}
		}
		return state.MachineTab{}
	}
	c.Active = at(m.Cur())
	if t := m.splitTab(); t != nil && t.ReviewOf == nil {
		c.Split, c.SplitLeft = at(t), m.splitLeft
	}
	return c
}

// AddServerOff has the TUI show server s grey, disconnected from as the
// last client left it: not connected to until the user says.
func (m *Model) AddServerOff(s config.Server) tea.Cmd {
	return m.AddServer(s, nil, errDisconnected)
}

// RestoreClient lays the TUI out as c keeps it, once the servers' tabs
// are there: the tab in front and the one beside it, when they are a
// server's and still there. The fleet is not locked.
func (m *Model) RestoreClient(c state.Client) {
	m.fleet.Lock()
	defer m.fleet.Unlock()
	find := func(at state.MachineTab) *core.Tab {
		if at.Machine == "" {
			return nil
		}
		return m.fleet.ByRef(core.TabRef{Core: at.Machine, Tab: at.Tab})
	}
	if t := find(c.Active); t != nil {
		m.Active = m.fleet.TabIndex(t)
		m.entered = t
	}
	if t := find(c.Split); t != nil && t != m.Cur() && m.fleet.Group(t) == m.curGroup() {
		m.split, m.splitLeft = t, c.SplitLeft
		m.reviewOpen = false
	}
}
