package app

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/fleet"
	"github.com/nerdr-dev/nerdr/internal/move"

	tea "github.com/charmbracelet/bubbletea"
)

// A tab can be moved to another machine of the fleet's (a m): its branch
// and every change not committed, its conversation as a handoff, and its
// queue, name, group and note. The cores do not know each other: the TUI
// asks each its part and carries the work over, a chunk at a time (see
// core/move.go for the steps). Until the target says it has the tab, the
// tab carries on where it was.

// moveStage is where a move stands.
type moveStage int

const (
	moveChecking moveStage = iota // the target is asked what it has
	moveLooking                   // the source, what would go
	moveAsking                    // the user is asked
	moveWeaker                    // ... to say yes to a weaker sandbox
	moveCloning                   // the target clones what it lacks
	movePacking                   // the agent's handoff, then the pack
	moveSending                   // the pack is carried over
	moveLanding                   // the target makes the tab
	moveLanded                    // what of the worktree left here
	moveFailed
)

// moveRun is the move going on: one at a time.
type moveRun struct {
	job      string
	ref      core.TabRef
	name     string
	agent    agents.Kind
	from, to *fleet.Machine
	repos    []core.MoveRepo
	stage    moveStage
	check    core.MoveCheckMsg
	look     core.MoveLookMsg
	// kinds are the agents on the target's PATH, kind the one picked.
	kinds []agents.Kind
	kind  agents.Kind
	// sandboxed is whether the tab's agent is in a sandbox here; handoff
	// whether it is asked for a handoff first, when it runs.
	sandboxed, running, handoff bool
	// stops is what stops here and does not go.
	stops []string
	// size is the pack's, sum its SHA-256, sent how much of it the target
	// has, and asked how large the chunk asked for is.
	size, sent int64
	sum        string
	heads      map[string]string
	// started is set once the source was asked to stop the agent, put
	// once the target has had a chunk: what has to be undone.
	started, put bool
	id           string
	landed       core.TabRef
	err          string
}

// openMove offers the machines the tab in front can be moved to.
func (m *Model) openMove() tea.Cmd {
	t := m.Cur()
	if t == nil {
		return nil
	}
	if m.moving != nil {
		m.dialog = &moveDialog{}
		return nil
	}
	from := m.machineOf(t)
	if why := from.Core.CanMove(t); why != "" {
		m.toast("cannot move " + t.Name + ": " + why)
		return nil
	}
	var to []*fleet.Machine
	for _, mc := range m.fleet.Machines() {
		if mc != from && !grey(mc) && !mc.Away {
			to = append(to, mc)
		}
	}
	switch len(to) {
	case 0:
		m.toast("no other machine to move " + t.Name + " to; " + m.pk("k") + " adds one")
		return nil
	case 1:
		return m.startMove(t, to[0])
	}
	items := []menuItem{{label: "Carries the branch, its changes and the conversation"}}
	for i, mc := range to {
		items = append(items, menuItem{label: machineLabel(mc), key: fmt.Sprint(i + 1),
			run: func(m *Model) tea.Cmd { return m.startMove(t, mc) }})
	}
	m.dialog = &menu{title: "Move " + t.Name + " to", items: items, x: m.width / 3, y: m.height / 3, hi: 1}
	return nil
}

// startMove starts moving t to machine to: the target is asked first.
func (m *Model) startMove(t *core.Tab, to *fleet.Machine) tea.Cmd {
	from := m.machineOf(t)
	if !from.Core.Live(t) || from == to {
		return nil
	}
	r := &moveRun{job: core.NewJobID(), ref: m.fleet.Ref(t), name: t.Name, agent: t.Agent, from: from, to: to}
	for _, w := range t.Worktrees {
		r.repos = append(r.repos, core.MoveRepo{Name: filepath.Base(w.Repo), Origin: w.Origin, Branch: w.Branch})
	}
	r.running = t.AgentRunning() && !t.QueuePaused
	r.handoff = r.running
	r.sandboxed = t.InSandbox || from.Core.SandboxedKind(t.Agent) || t.HasSandboxed()
	r.kinds = to.Core.Available()
	r.kind = t.Agent
	if !slices.Contains(r.kinds, r.kind) && len(r.kinds) > 0 {
		r.kind = r.kinds[0]
	}
	r.stops = m.moveStops(t)
	m.moving = r
	m.dialog = &moveDialog{}
	return m.doOn(to, core.MoveCheck{Job: r.job, Repos: r.repos})
}

// moveStops is what stops when t moves: what runs in it but its agent,
// and what waits on it.
func (m *Model) moveStops(t *core.Tab) []string {
	mc := m.machineOf(t)
	var terms, agentsBeside []string
	for _, p := range t.Panes() {
		switch {
		case p.Kind == core.PaneShell:
			terms = append(terms, "")
		case p.Kind == core.PaneAgent && p.A != nil && p.A != t:
			agentsBeside = append(agentsBeside, string(p.A.Agent))
		}
	}
	var out []string
	if n := len(terms); n > 0 {
		out = append(out, core.Plural(n, "terminal")+" (dev servers in them too)")
	}
	if len(agentsBeside) > 0 {
		out = append(out, "the other agents in the tab ("+strings.Join(agentsBeside, ", ")+")")
	}
	if t.Review != nil {
		out = append(out, "the code review")
	}
	n := 0
	for _, r := range mc.Core.PluginRuns.ByID {
		if r.T != nil && r.T.ID == t.ID && r.Running() {
			n++
		}
	}
	if n > 0 {
		out = append(out, core.Plural(n, "plugin run"))
	}
	ref, asks := mc.Core.Ref(t), 0
	for _, q := range mc.Core.Asking {
		if q.A == ref {
			asks++
		}
	}
	if asks > 0 {
		out = append(out, core.Plural(asks, "question")+" waiting for you")
	}
	return out
}

// onMove takes what a core told of the move going on.
func (m *Model) onMove(ev core.Event) tea.Cmd {
	r := m.moving
	if r == nil {
		return nil
	}
	switch ev := ev.(type) {
	case core.MoveCheckMsg:
		if ev.Job != r.job {
			return nil
		}
		if why := checkErr(ev, r.repos); why != "" {
			return m.moveFail(why)
		}
		r.check = ev
		if r.stage == moveCloning {
			return m.movePack()
		}
		r.stage = moveLooking
		have, tips := r.haves()
		return m.doOn(r.from, core.MoveLook{Job: r.job, T: r.ref, Have: have, Tips: tips})
	case core.MoveLookMsg:
		if ev.Job != r.job || r.stage != moveLooking {
			return nil
		}
		if ev.Err != "" {
			return m.moveFail(ev.Err)
		}
		r.look, r.stage = ev, moveAsking
	case core.MovePackedMsg:
		if ev.Job != r.job {
			return nil
		}
		if ev.Err != "" {
			return m.moveFail(ev.Err)
		}
		r.size, r.sum, r.heads, r.stage = ev.Size, ev.Sum, ev.Heads, moveSending
		return m.doOn(r.from, core.MoveChunk{Job: r.job, Off: 0})
	case core.MoveChunkMsg:
		if ev.Job != r.job || r.stage != moveSending || ev.Off != r.sent {
			return nil
		}
		if ev.Err != "" {
			return m.moveFail(ev.Err)
		}
		if len(ev.Data) == 0 && r.sent < r.size {
			return m.moveFail("the pack ended early")
		}
		r.put = true
		return m.doOn(r.to, core.MovePut{Job: r.job, Off: ev.Off, Data: ev.Data})
	case core.MovePutMsg:
		if ev.Job != r.job || r.stage != moveSending || ev.Off != r.sent {
			return nil
		}
		if ev.Err != "" {
			return m.moveFail(ev.Err)
		}
		r.sent = min(r.size, ev.Off+move.Chunk)
		if r.sent < r.size {
			return m.doOn(r.from, core.MoveChunk{Job: r.job, Off: r.sent})
		}
		r.stage, r.id = moveLanding, core.NewTabID()
		var paths []string
		for _, t := range r.check.Repos {
			paths = append(paths, t.Path)
		}
		return m.doOn(r.to, core.MoveLand{Job: r.job, ID: r.id, Size: r.size, Sum: r.sum, Kind: r.kind,
			Repos: paths, Dir: r.check.Dir, From: machineLabel(r.from), Sandboxed: r.sandboxed, Weaker: r.weaker()})
	case core.MoveLandedMsg:
		if ev.Job != r.job || r.stage != moveLanding {
			return nil
		}
		if ev.Err != "" {
			r.put = false // the target dropped it
			return m.moveFail(ev.Err)
		}
		r.landed, r.stage = ev.T, moveLanded
		m.dialog = &moveDialog{}
		if t := m.fleet.ByRef(ev.T); t != nil {
			return m.selectTab(m.fleet.TabIndex(t))
		}
	}
	return nil
}

// checkErr is why the target cannot take the tab, by what it said.
func checkErr(ev core.MoveCheckMsg, repos []core.MoveRepo) string {
	if ev.Err != "" {
		return ev.Err
	}
	if len(ev.Repos) != len(repos) {
		return "the machine did not answer for every repository"
	}
	for i, t := range ev.Repos {
		if t.Err != "" {
			return repos[i].Name + ": " + t.Err
		}
	}
	return ""
}

// haves are what the target has of each repository, and where its
// branches of the tab's names are.
func (r *moveRun) haves() ([][]string, []string) {
	var have [][]string
	var tips []string
	for _, t := range r.check.Repos {
		have = append(have, t.Have)
		tips = append(tips, t.Tip)
	}
	return have, tips
}

// weaker is whether the agent on the target is in less of a sandbox
// than the tab's is here.
func (r *moveRun) weaker() bool {
	return r.sandboxed && !r.to.Core.SandboxedKind(r.kind)
}

// clones are the repositories the target lacks and would clone.
func (r *moveRun) clones() []string {
	var out []string
	for i, t := range r.check.Repos {
		if t.CloneInto != "" {
			out = append(out, r.repos[i].Origin+" into "+t.CloneInto)
		}
	}
	return out
}

// moveGo goes ahead with the move the user said yes to.
func (m *Model) moveGo() tea.Cmd {
	r := m.moving
	if r == nil || (r.stage != moveAsking && r.stage != moveWeaker) {
		return nil
	}
	if r.weaker() && r.stage != moveWeaker {
		r.stage = moveWeaker
		return nil
	}
	if len(r.clones()) > 0 {
		r.stage = moveCloning
		return m.doOn(r.to, core.MoveCheck{Job: r.job, Repos: r.repos, Clone: true})
	}
	return m.movePack()
}

// movePack has the source stop the agent, after its handoff, and pack.
func (m *Model) movePack() tea.Cmd {
	r := m.moving
	r.stage, r.started = movePacking, true
	have, _ := r.haves()
	return m.doOn(r.from, core.MoveStart{Job: r.job, T: r.ref, Have: have, Handoff: r.handoff})
}

// moveFail ends the move with why: the tab carries on where it was.
func (m *Model) moveFail(why string) tea.Cmd {
	r := m.moving
	if r == nil || r.stage == moveFailed || r.stage == moveLanded {
		return nil
	}
	r.stage, r.err = moveFailed, why
	m.dialog = &moveDialog{}
	return m.moveUndo(why)
}

// moveUndo has the cores let go of the move: the source carries on with
// the tab, the target drops what it got.
func (m *Model) moveUndo(why string) tea.Cmd {
	r := m.moving
	var cmds []tea.Cmd
	if r.started && !grey(r.from) {
		cmds = append(cmds, m.doOn(r.from, core.MoveAbort{Job: r.job, T: r.ref, Why: why}))
	}
	if r.put && !grey(r.to) {
		cmds = append(cmds, m.doOn(r.to, core.MoveDrop{Job: r.job}))
	}
	r.started, r.put = false, false
	return tea.Batch(cmds...)
}

// moveCancel gives the move up, at the user's word.
func (m *Model) moveCancel() tea.Cmd {
	r := m.moving
	if r == nil {
		return nil
	}
	cmd := m.moveUndo("you cancelled it")
	m.moving, m.dialog = nil, nil
	return cmd
}

// moveMachineGone gives up the move when a machine in it goes.
func (m *Model) moveMachineGone(mc *fleet.Machine) tea.Cmd {
	if r := m.moving; r != nil && (r.from == mc || r.to == mc) && r.stage < moveLanded {
		return m.moveFail(machineLabel(mc) + " went away")
	}
	return nil
}

// moveFinish closes the tab on the source once it is on the target, its
// worktrees deleted with remove, and with branch their branches too.
func (m *Model) moveFinish(remove, branch bool) tea.Cmd {
	r := m.moving
	if r == nil || r.stage != moveLanded {
		return nil
	}
	m.moving, m.dialog = nil, nil
	q := core.MoveDone{Job: r.job, T: r.ref, Remove: remove}
	if branch {
		q.BranchHeads = r.heads
	}
	cmd := m.doOn(r.from, q)
	if t := m.fleet.ByRef(r.landed); t != nil {
		return tea.Batch(cmd, m.selectTab(m.fleet.TabIndex(t)))
	}
	return cmd
}

// moveDialog shows the move going on, and asks what is to be asked.
type moveDialog struct{}

func (d *moveDialog) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	r := m.moving
	if r == nil {
		m.dialog = nil
		return nil
	}
	k := msg.String()
	switch r.stage {
	case moveAsking:
		switch k {
		case "enter", "y":
			return m.moveGo()
		case "a":
			if i := slices.Index(r.kinds, r.kind); len(r.kinds) > 0 {
				r.kind = r.kinds[(i+1)%len(r.kinds)]
			}
		case "h":
			if r.running {
				r.handoff = !r.handoff
			}
		case "esc", "n", "q", "ctrl+c":
			return m.moveCancel()
		}
	case moveWeaker:
		switch k {
		case "y":
			return m.moveGo()
		default:
			r.stage = moveAsking
		}
	case movePacking:
		switch k {
		case "enter":
			if t := m.fleet.ByRef(r.ref); t != nil && t.Handoff != nil && t.Handoff.Move {
				return m.doOn(r.from, core.MoveNow{Job: r.job, T: r.ref})
			}
		case "esc", "ctrl+c":
			return m.moveCancel()
		}
	case moveLanded:
		switch k {
		case "1", "esc", "enter":
			return m.moveFinish(false, false)
		case "2":
			return m.moveFinish(true, false)
		case "3":
			return m.moveFinish(true, true)
		}
	case moveFailed:
		switch k {
		case "esc", "enter", "q", "ctrl+c":
			m.moving, m.dialog = nil, nil
		}
	case moveLanding:
		// The target is making the tab: it cannot be had back now.
		if k == "esc" {
			m.dialog = nil
		}
	default:
		if k == "esc" || k == "ctrl+c" {
			return m.moveCancel()
		}
	}
	return nil
}

func (d *moveDialog) view(m *Model, w, h int) string {
	r := m.moving
	if r == nil {
		return ""
	}
	to := machineLabel(r.to)
	title := "Move " + r.name + " to " + to
	var lines []string
	switch r.stage {
	case moveChecking:
		lines = append(lines, "Asking "+to+" what it has of the repository…", "", m.button("esc", "cancel"))
	case moveLooking:
		lines = append(lines, "Looking at what would go…", "", m.button("esc", "cancel"))
	case moveAsking, moveWeaker:
		lines = append(lines, r.askLines(m)...)
	case moveCloning:
		lines = append(lines, "Cloning on "+to+": "+strings.Join(r.clones(), ", ")+"…", "", m.button("esc", "cancel"))
	case movePacking:
		t := m.fleet.ByRef(r.ref)
		if t != nil && t.Handoff != nil && t.Handoff.Move {
			lines = append(lines, "Waiting for "+string(t.Agent)+"'s handoff…", "",
				m.button("enter", "move now: "+string(r.kind)+" reads its conversation")+"   "+m.button("esc", "cancel the move"))
		} else {
			lines = append(lines, "Packing the work…", "", m.button("esc", "cancel"))
		}
	case moveSending:
		lines = append(lines, fmt.Sprintf("Carrying it over: %s of %s", move.Size(r.sent), move.Size(r.size)), "", m.button("esc", "cancel"))
	case moveLanding:
		lines = append(lines, "Making the tab on "+to+"…")
	case moveLanded:
		title = r.name + " is on " + to
		lines = append(lines, "Its worktree and branch are still on "+machineLabel(r.from)+":")
		for _, rp := range r.repos {
			lines = append(lines, "  "+dim(rp.Name+" · "+rp.Branch))
		}
		lines = append(lines, "",
			m.button("1", "leave them there"),
			m.button("2", "delete the worktree there (the branch stays)"),
			m.button("3", "delete the worktree and the branch there"))
	case moveFailed:
		title = r.name + " was not moved"
		lines = append(lines, warn(r.err), "", "It carries on on "+machineLabel(r.from)+".", "", m.button("enter", "close"))
	}
	return box(title, lines, w)
}

// askLines are what the user is asked before the move: what goes, what
// does not, and what stops.
func (r *moveRun) askLines(m *Model) []string {
	var lines []string
	size := move.Size(r.look.Size)
	changed := 0
	var newFiles, ignored []string
	more, ignMore := 0, 0
	for _, l := range r.look.Repos {
		changed += l.Changed
		newFiles = append(newFiles, l.New...)
		ignored = append(ignored, l.Ignored...)
		more += l.NewMore
		ignMore += l.IgnoredMore
	}
	branches := []string{}
	for _, rp := range r.repos {
		branches = append(branches, rp.Branch)
	}
	lines = append(lines, "Goes: branch "+strings.Join(slices.Compact(branches), ", ")+", "+
		core.Plural(changed, "file")+" not committed, the conversation, queue and note ("+size+")")
	if len(newFiles) > 0 {
		lines = append(lines, "  New files: "+listSome(newFiles, more, 6))
	}
	for i, t := range r.check.Repos {
		if t.CloneInto != "" {
			lines = append(lines, warn("  "+r.repos[i].Name+" is not on "+machineLabel(r.to)+": it clones "+r.repos[i].Origin+" into "+t.CloneInto))
		} else {
			lines = append(lines, dim("  Into "+t.Path+" on "+machineLabel(r.to)))
		}
	}
	lines = append(lines, dim("  Worktree there: "+r.check.Dir))
	if len(ignored) > 0 {
		lines = append(lines, "Stays: "+listSome(ignored, ignMore, 6)+dim(" (git ignores them)"))
	}
	if len(r.stops) > 0 {
		lines = append(lines, "Stops: "+strings.Join(r.stops, ", "))
	}
	lines = append(lines, "")
	agent := "Agent there: " + string(r.kind)
	if len(r.kinds) > 1 {
		agent += "  " + m.button("a", "another")
	}
	lines = append(lines, agent)
	if r.weaker() {
		lines = append(lines, warn("  Not in a sandbox there, but the tab's agent is in one here"))
	}
	if r.running {
		if r.handoff {
			lines = append(lines, "Asks "+string(r.agent)+" for a handoff first  "+m.button("h", "don't: its conversation goes as it is"))
		} else {
			lines = append(lines, "Its conversation goes as it is, no handoff  "+m.button("h", "ask for one"))
		}
	}
	lines = append(lines, dim("Until "+machineLabel(r.to)+" has the tab, it stays here."), "")
	if r.stage == moveWeaker {
		lines = append(lines, warn(string(r.kind)+" on "+machineLabel(r.to)+" runs without a sandbox. Move anyway?"),
			m.button("y", "yes, without a sandbox")+"   "+m.button("n", "no"))
		return lines
	}
	lines = append(lines, m.button("enter", "move")+"   "+m.button("esc", "cancel"))
	return lines
}

// listSome is up to n of names, and how many more there are.
func listSome(names []string, more, n int) string {
	if len(names) > n {
		more += len(names) - n
		names = names[:n]
	}
	s := strings.Join(names, ", ")
	if more > 0 {
		s += fmt.Sprintf(" and %d more", more)
	}
	return s
}
