package app

import (
	"os"
	"path/filepath"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/fleet"
	"github.com/nerdr-dev/nerdr/internal/move"
	"github.com/nerdr-dev/nerdr/internal/spinoff"

	tea "github.com/charmbracelet/bubbletea"
)

// A part can be made on another machine of the fleet's than its tab's
// (ctrl+t in the spinoff dialog). The cores do not know each other: the
// TUI asks the target where it has the tab's repositories, carries the
// part's commits over when the branch starts with them, has the target
// make the tab, and tells the tab's core what became of the part, for the
// agent that sent it (see core/spinoffremote.go for the steps).

// spinoffRun is a part being made on another machine: one at a time.
type spinoffRun struct {
	job      string
	ref      core.TabRef
	from, to *fleet.Machine
	land     core.SpinoffLand
	// busy is what the dialog says is being done.
	busy string
	// reuse is set when the part's commits are carried over. held is set
	// once the tab's core holds the part for the run (core.SpinoffHold),
	// packed once the source was asked for the pack, put once the target
	// has had a chunk of it: what has to be let go of. size is the
	// pack's, sent how much of it the target has.
	reuse, held bool
	packed, put bool
	size, sent  int64
	// froms are the tab's repositories the commits are bundled from.
	froms []string
}

// spinoffMachines are the machines a part can be made on: every one of
// the fleet's that can be reached.
func (m *Model) spinoffMachines() []*fleet.Machine {
	var out []*fleet.Machine
	for _, mc := range m.fleet.Machines() {
		if !grey(mc) && !mc.Away {
			out = append(out, mc)
		}
	}
	return out
}

// nextMachine has the dialog make the tab on the next machine: the
// target is asked where it has the tab's repositories.
func (d *spinoffDialog) nextMachine(m *Model) tea.Cmd {
	ms := m.spinoffMachines()
	if len(ms) < 2 {
		d.err = "no other machine to make it on; " + m.pk("d") + " adds one"
		return nil
	}
	at := d.target(m)
	i := 0
	for j, mc := range ms {
		if mc == at {
			i = j
		}
	}
	return d.machineTo(m, ms[(i+1)%len(ms)])
}

// machineTo has the dialog make the tab on mc.
func (d *spinoffDialog) machineTo(m *Model, mc *fleet.Machine) tea.Cmd {
	d.on, d.found, d.find, d.err = mc, nil, "", ""
	if !d.resetTarget(m) {
		return m.spinoffNoAgent(d.t)
	}
	if !d.remote(m) {
		return nil
	}
	d.find = core.NewJobID()
	var repos []core.MoveRepo
	for _, w := range d.t.Worktrees {
		repos = append(repos, core.MoveRepo{Name: filepath.Base(w.Repo), Origin: w.Origin})
	}
	return m.doOn(mc, core.SpinoffFind{Job: d.find, Repos: repos})
}

// spinoffFindAgain has the dialog, when it makes the tab on another
// machine, ask it again where it has the repositories: a part made there
// may have cloned one.
func (m *Model) spinoffFindAgain() tea.Cmd {
	d, ok := m.dialog.(*spinoffDialog)
	if !ok || !d.remote(m) {
		return nil
	}
	found, err := d.found, d.err
	cmd := d.machineTo(m, d.target(m))
	d.err = err
	if d.found == nil && found != nil {
		d.found = found // shown until the answer comes
		d.resetTarget(m)
	}
	return cmd
}

// spinoffFound takes the target's answer to where it has the tab's
// repositories.
func (m *Model) spinoffFound(ev core.SpinoffFoundMsg) tea.Cmd {
	d, ok := m.dialog.(*spinoffDialog)
	if !ok || d.find == "" || ev.Job != d.find || !d.remote(m) {
		return nil
	}
	d.found = &ev
	if !d.resetTarget(m) {
		return m.spinoffNoAgent(d.t)
	}
	return nil
}

// machineName names mc to an agent: this machine by its host name, as
// the user's nerdr knows no other name for it.
func machineName(mc *fleet.Machine) string {
	if mc.Name == "" && mc.Host == "" {
		if h, err := os.Hostname(); err == nil && h != "" {
			return h
		}
	}
	return machineLabel(mc)
}

// spinoffRemote has the part asked about in d made on the machine picked
// there: with reuse, its commits are packed and carried over first.
func (m *Model) spinoffRemote(d *spinoffDialog, kind agents.Kind, name string, reuse, elsewhere bool, group string) tea.Cmd {
	if m.spinoffRun != nil {
		d.err = "a part is being made on another machine already: wait for it"
		return nil
	}
	t, pt, from, to := d.t, d.part(), m.machineOf(d.t), d.target(m)
	var wheres []core.SpinoffWhere
	var froms []string
	for i, r := range d.repos {
		if !d.repoOn[i] {
			continue
		}
		if r.Path == "" && r.CloneInto == "" {
			d.err = r.Label + ": " + r.Err
			return nil
		}
		wheres = append(wheres, core.SpinoffWhere{Name: r.Name, Origin: r.Origin, Path: r.Path, Own: r.Own})
		if r.Own {
			froms = append(froms, r.From)
		}
	}
	parent := spinoff.Parent{Name: t.Name, Dir: t.Dir, Machine: machineName(from)}
	if len(t.Worktrees) > 0 {
		parent.Branch = t.Worktrees[0].Branch
	}
	r := &spinoffRun{job: core.NewJobID(), ref: m.fleet.Ref(t), from: from, to: to}
	r.land = core.SpinoffLand{Job: r.job, Part: pt, Kind: kind, Name: name, Group: group, Repos: wheres,
		Parent: parent, Reuse: reuse, Elsewhere: elsewhere}
	r.reuse, r.froms = reuse, froms
	m.spinoffRun = r
	// Held first, so nothing else answers the part while it is made here:
	// the tab's core knows of it only at the end.
	r.busy = "making " + name + " on " + machineLabel(to) + "…"
	cmd := m.doOn(from, core.SpinoffHold{Job: r.job, T: r.ref, Part: pt.ID, Busy: r.busy})
	if spinoffHoldWait > 0 {
		job := r.job
		cmd = tea.Batch(cmd, tea.Tick(spinoffHoldWait, func(time.Time) tea.Msg { return spinoffHoldLateMsg{job: job} }))
	}
	return cmd
}

// spinoffHoldWait is how long the tab's core has to say it holds the part:
// one of a nerdr from before SpinoffHold says nothing. 0 waits for ever,
// for the tests, which run a command's batch through before going on.
var spinoffHoldWait = 10 * time.Second

// spinoffHoldLateMsg is spinoffHoldWait gone by since job asked for the
// hold.
type spinoffHoldLateMsg struct{ job string }

// spinoffHoldLate gives up the run of job if the tab's core has not said
// it holds the part yet.
func (m *Model) spinoffHoldLate(job string) tea.Cmd {
	r := m.spinoffRun
	if r == nil || r.job != job || r.held {
		return nil
	}
	return m.spinoffRunFail(machineLabel(r.from) + " did not say it holds the part for it to be made elsewhere: " +
		"update nerdr there, or make the part on its own machine")
}

// spinoffHeld goes on with the part being made on another machine, once
// the tab's core holds it.
func (m *Model) spinoffHeld() tea.Cmd {
	r := m.spinoffRun
	r.held = true
	if r.reuse {
		r.busy, r.packed = "bundling the commits…", true
		return m.doOn(r.from, core.SpinoffPack{Job: r.job, T: r.ref, Repos: r.froms, Commits: r.land.Part.Commits})
	}
	return m.spinoffLand()
}

// spinoffLand has the target make the tab of the part being made there.
func (m *Model) spinoffLand() tea.Cmd {
	r := m.spinoffRun
	r.busy = "making " + r.land.Name + " on " + machineLabel(r.to) + "…"
	return m.doOn(r.to, r.land)
}

// onSpinoffRun takes what a core told of the part being made on another
// machine.
func (m *Model) onSpinoffRun(ev core.Event) tea.Cmd {
	r := m.spinoffRun
	if r == nil {
		return nil
	}
	switch ev := ev.(type) {
	case core.SpinoffHeldMsg:
		if ev.Job != r.job || r.held {
			return nil
		}
		if ev.Err != "" {
			return m.spinoffRunFail(ev.Err)
		}
		return m.spinoffHeld()
	case core.SpinoffPackedMsg:
		if ev.Job != r.job {
			return nil
		}
		if ev.Err != "" {
			return m.spinoffRunFail(ev.Err)
		}
		r.size, r.land.Size, r.land.Sum = ev.Size, ev.Size, ev.Sum
		r.busy = "carrying the commits over to " + machineLabel(r.to) + "…"
		return m.doOn(r.from, core.MoveChunk{Job: r.job, Off: 0})
	case core.MoveChunkMsg:
		if ev.Job != r.job || ev.Off != r.sent {
			return nil
		}
		if ev.Err != "" {
			return m.spinoffRunFail(ev.Err)
		}
		if len(ev.Data) == 0 && r.sent < r.size {
			return m.spinoffRunFail("the pack ended early")
		}
		r.put = true
		return m.doOn(r.to, core.MovePut{Job: r.job, Off: ev.Off, Data: ev.Data})
	case core.MovePutMsg:
		if ev.Job != r.job || ev.Off != r.sent {
			return nil
		}
		if ev.Err != "" {
			return m.spinoffRunFail(ev.Err)
		}
		r.sent = min(r.size, ev.Off+move.Chunk)
		if r.sent < r.size {
			return m.doOn(r.from, core.MoveChunk{Job: r.job, Off: r.sent})
		}
		return m.spinoffLand()
	case core.SpinoffLandedMsg:
		if ev.Job != r.job {
			return nil
		}
		if ev.Err != "" {
			// The target dropped what it had of the commits; MoveDrop
			// again is nothing, should it not have.
			return tea.Batch(m.spinoffRunFail(ev.Err), m.spinoffFindAgain())
		}
		m.spinoffRun = nil
		o := ev.Outcome
		o.Machine = machineName(r.to)
		cmds := []tea.Cmd{m.doOn(r.from, core.SpinoffNext{T: r.ref, Outcome: o})}
		if r.packed {
			cmds = append(cmds, m.doOn(r.from, core.SpinoffPackDrop{Job: r.job}))
		}
		return tea.Batch(append(cmds, m.spinoffFindAgain())...)
	}
	return nil
}

// spinoffRunFail gives up making the part on the other machine, with why:
// the part stays to be asked about, for the user to try again, pick
// another machine, or skip it.
func (m *Model) spinoffRunFail(why string) tea.Cmd {
	r := m.spinoffRun
	if r == nil {
		return nil
	}
	m.spinoffRun = nil
	var cmds []tea.Cmd
	if r.held && !grey(r.from) {
		// Gone, its core lets go of it in time (core.SpinoffHoldFor).
		cmds = append(cmds, m.doOn(r.from, core.SpinoffHold{Job: r.job, T: r.ref, Release: true}))
	}
	if r.packed && !grey(r.from) {
		cmds = append(cmds, m.doOn(r.from, core.SpinoffPackDrop{Job: r.job}))
	}
	if r.put && !grey(r.to) {
		cmds = append(cmds, m.doOn(r.to, core.MoveDrop{Job: r.job}))
	}
	if t := m.fleet.ByRef(r.ref); t != nil {
		m.spinoffFailed(t, r.land.Name, why)
	} else {
		m.toast("spinoff " + r.land.Name + ": " + why)
	}
	return tea.Batch(cmds...)
}

// spinoffMachineGone gives up the part being made when a machine in it
// goes, and has the dialog make the next on the tab's own machine when
// the one picked was it.
func (m *Model) spinoffMachineGone(mc *fleet.Machine) tea.Cmd {
	var cmd tea.Cmd
	if r := m.spinoffRun; r != nil && (r.from == mc || r.to == mc) {
		cmd = m.spinoffRunFail(machineLabel(mc) + " went away before it said whether the tab was made: look there before you try again")
	}
	if d, ok := m.dialog.(*spinoffDialog); ok && d.on == mc && m.machineOf(d.t) != mc && d.t.Spinoff != nil && len(d.t.Spinoff.Next) > 0 {
		err := d.err
		cmd = tea.Batch(cmd, d.machineTo(m, nil))
		if err == "" {
			err = machineLabel(mc) + " went away"
		}
		d.err = err + ": the tab is to be made on " + machineLabel(m.machineOf(d.t)) + " now"
	}
	return cmd
}
