package app

import (
	"errors"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/creack/pty"
	xterm "golang.org/x/term"

	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/fleet"
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

// login connects to server s, pausing the TUI while ssh may ask for a
// password: ssh gets a terminal of its own, which what is typed goes to
// and whose output is shown in place of the TUI's. Then serverDialedMsg
// tells what came of it.
func (m *Model) login(s config.Server) tea.Cmd {
	if m.dial == nil || m.router == nil {
		m.toast("nerdr cannot reach other machines here")
		return nil
	}
	l := &loginExec{s: s, dial: m.dial, router: m.router}
	return tea.Exec(l, func(err error) tea.Msg {
		return serverDialedMsg{s: s, conn: l.conn, err: err}
	})
}

// serverDialedMsg is what came of connecting to server s from the TUI.
type serverDialedMsg struct {
	s    config.Server
	conn *attach.Conn
	err  error
}

// loginExec is a login to a server, for tea.Exec: the TUI has let go of
// the terminal while it runs. The input router still reads the terminal
// and passes all of it to ssh's own.
type loginExec struct {
	s      config.Server
	dial   Dialer
	router interface{ Passthrough(func([]byte)) }
	out    io.Writer
	conn   *attach.Conn
}

func (l *loginExec) SetStdin(io.Reader)    {}
func (l *loginExec) SetStdout(w io.Writer) { l.out = w }
func (l *loginExec) SetStderr(io.Writer)   {}

func (l *loginExec) Run() error {
	out := l.out
	if out == nil {
		out = os.Stdout
	}
	ptmx, tty, err := pty.Open()
	if err != nil {
		return err
	}
	defer ptmx.Close()
	if w, h, err := xterm.GetSize(int(os.Stdout.Fd())); err == nil {
		_ = pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(w), Rows: uint16(h)})
	}
	_, _ = io.WriteString(out, "\x1b[2J\x1b[H"+"nerdr: connecting to "+l.s.Name+" ("+l.s.Host()+")…\r\n")
	l.router.Passthrough(func(b []byte) { _, _ = ptmx.Write(b) })
	defer l.router.Passthrough(nil)
	go func() { _, _ = io.Copy(out, ptmx) }()
	conn, err := l.dial(l.s, false, tty)
	tty.Close()
	l.conn = conn
	return err
}

// onServerDialed takes what came of connecting to a server from the TUI:
// the machine there is shown, connected or grey, a new one last.
func (m *Model) onServerDialed(msg serverDialedMsg) tea.Cmd {
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
