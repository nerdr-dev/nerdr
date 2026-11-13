package core

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/codereview"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/state"
)

// Live reports whether t is still an agent nerdr runs: a tab open in the
// tab row, an agent in one of its panes, or the reviewer of one of those.
func (m *Core) Live(t *Tab) bool {
	switch {
	case t.ReviewOf != nil:
		return t.ReviewOf.SeatOf(t) != nil && m.Live(t.ReviewOf)
	case t.PartOf != nil:
		return t.PaneOf() != nil && m.TabIndex(t.PartOf) >= 0
	}
	return m.TabIndex(t) >= 0
}

// ReviewKind is the agent that reviews t's work: the config's, or with
// "other", another kind than t's that is installed — codex for claude and
// claude for the rest, where they are — or t's own when there is no other.
func (m *Core) ReviewKind(t *Tab) agents.Kind {
	avail := m.Available()
	confined := m.Confined(hooks.Report{}, t)
	if k := agents.Kind(m.Cfg.Review.Agent); k != config.ReviewOther && k != "" {
		if !confined || m.SandboxedKind(k) {
			return k
		}
	}
	prefer := []agents.Kind{agents.Claude, agents.Codex}
	if t.Agent == agents.Claude {
		prefer = []agents.Kind{agents.Codex}
	}
	for _, k := range prefer {
		if k != t.Agent && Contains(avail, k) && (!confined || m.SandboxedKind(k)) {
			return k
		}
	}
	for _, k := range avail {
		if k != t.Agent && (!confined || m.SandboxedKind(k)) {
			return k
		}
	}
	return t.Agent
}

func Contains(ks []agents.Kind, k agents.Kind) bool {
	for _, x := range ks {
		if x == k {
			return true
		}
	}
	return false
}

// TreeMsg is the worktree as it is now, looked at for a review.
type TreeMsg struct {
	T    *Tab
	Tree string
	// Changed is whether the work differs from the branch's base at all.
	Changed bool
	Base    string
	Err     error
	// Why is what the look was for.
	Why TreeWhy
	// By is the reviewer asked for, for a review started by hand.
	By Reviewer
}

type TreeWhy int

const (
	TreeAuto  TreeWhy = iota // a turn ended: start a review if it is due
	TreeHand                 // the user asked for a review
	TreeFixed                // the agent worked on the findings: look again
)

// ReviewerNames are the agents reviewing t's work, as words.
func (m *Core) ReviewerNames(t *Tab) string {
	var names []string
	for _, s := range t.Review.Seats {
		names = append(names, string(s.R.Agent))
	}
	return strings.Join(names, " and ")
}

// RoundDone reports whether all of t's reviewers have sent their review.
func (m *Core) RoundDone(t *Tab) bool {
	for _, s := range t.Review.Seats {
		if s.Res == nil {
			return false
		}
	}
	return len(t.Review.Seats) > 0
}

// ReviewSays is what the reviewers at seats said this round.
func ReviewSays(seats []*Seat) []state.ReviewSaid {
	var out []state.ReviewSaid
	for _, s := range seats {
		if s.Res != nil {
			out = append(out, state.ReviewSaid{Agent: string(s.R.Agent), Brief: s.Res.Brief(), Summary: s.Res.Summary,
				Approved: s.Res.Verdict == codereview.Approve})
		}
	}
	return out
}

// lookAtTree finds out, in the background, how t's worktree stands.
func (m *Core) LookAtTree(t *Tab, why TreeWhy, by Reviewer) Cmd {
	var paths []string
	for _, w := range t.Worktrees {
		paths = append(paths, w.Path)
	}
	if len(paths) == 0 {
		paths = []string{t.Dir}
	}
	name := t.Name
	return func() Event {
		start := time.Now()
		defer func() { Debugf("review %s: worktree looked at in %s", name, time.Since(start).Round(time.Millisecond)) }()
		msg := TreeMsg{T: t, Why: why, By: by}
		var trees []string
		for _, p := range paths {
			tree, err := git.WorkTree(p)
			if err != nil {
				msg.Err = err
				return msg
			}
			trees = append(trees, tree)
			base := git.DetectBase(p)
			if len(paths) == 1 {
				msg.Base = base
			}
			from := "HEAD"
			if base != "" {
				from = git.MergeBase(p, base)
			}
			if git.TreeOf(p, from) != tree {
				msg.Changed = true
			}
		}
		msg.Tree = strings.Join(trees, ",")
		return msg
	}
}

// afterTurn is called when t's agent has finished a turn: a review is
// started if one is due, or one waiting on the agent's fixes goes on.
func (m *Core) AfterTurn(t *Tab) Cmd {
	if t.ReviewOf != nil || t.Handoff != nil || t.Spinoff != nil {
		return nil
	}
	if run := t.Review; run != nil {
		if run.Phase == Fixing && t.FindMessage(run.FixMsg) < 0 {
			return m.LookAtTree(t, TreeFixed, Reviewer{})
		}
		return nil
	}
	if t.NextAuto() >= 0 {
		return nil // more is coming: the review waits for the end of it
	}
	if t.ReviewAsk == nil && (!m.Cfg.Review.Auto || m.WaitsForReady(t)) {
		return nil
	}
	return m.LookAtTree(t, TreeAuto, Reviewer{})
}

// waitsForReady reports whether a review of t's work starts by itself only
// once its agent has said the work is ready for one. An agent without the
// skill that tells it how is not waited for: its turns are reviewed as
// with when = "turn".
func (m *Core) WaitsForReady(t *Tab) bool {
	return m.Cfg.Review.When == config.WhenReady && m.SkillOn(t, "nerdr-request-review")
}

// onReady takes an agent's word that its work is ready for a review: one
// starts when its turn ends, with what it asked to have looked at. With
// auto off too: the skill has the agent say so then only when the user
// asked for a review, or said yes to one.
func (m *Core) OnReady(rep hooks.Report) Cmd {
	t := m.AgentByHook(rep.Pane)
	if t == nil || t.ReviewOf != nil {
		return nil
	}
	if t.Review != nil {
		return nil // the review going on looks again when the agent's turn ends
	}
	ask := &ReadyAsk{Focus: rep.Focus}
	for _, r := range rep.Reviewers {
		k := agents.Kind(r.Kind)
		switch {
		case k == "":
		case slices.ContainsFunc(ask.By, func(b Reviewer) bool { return b.Kind == k }):
			m.Toast(t.Name + ": " + string(k) + " reviews once; a second " + string(k) + " is left out")
		case !Contains(m.Available(), k):
			m.Toast(t.Name + ": there is no agent " + string(k) + " here to review with")
		case r.Model != "" && !agents.TakesModel(k):
			m.Toast(t.Name + ": nerdr cannot start " + string(k) + " with a model of its choosing; it reviews with its own")
			ask.By = append(ask.By, Reviewer{Kind: k})
		default:
			ask.By = append(ask.By, Reviewer{Kind: k, Model: r.Model})
		}
	}
	if len(rep.Reviewers) > 0 && len(ask.By) == 0 {
		m.Toast(t.Name + ": none of the reviewers asked for is here; the configured reviewer reviews")
	}
	t.ReviewAsk = ask
	Debugf("review %s: ready, agent %s", m.AgentName(t), t.Light.Label())
	if t.Light == StIdle || t.Light == StDone {
		return m.AfterTurn(t) // said between turns: no turn end to wait for
	}
	return nil
}

// setSession is the conversation t's agent is in. A reviewer's is recorded
// as one, so the tab it reviews never carries it on as its own: it was had
// in the same worktree.
func (m *Core) SetSession(t *Tab, id string) {
	t.SessionID = id
	m.SaveSession()
	if t.ReviewOf != nil && id != "" {
		_ = codereview.SaveSession(t.Dir, string(t.Agent), id)
	}
}

// onReview takes a reviewer's conclusion.
func (m *Core) OnReview(rep hooks.Report) Cmd {
	r := m.AgentByHook(rep.Pane)
	if r == nil || r.ReviewOf == nil || rep.Review == nil {
		return nil
	}
	t := r.ReviewOf
	run := t.Review
	if run.Phase != Reviewing {
		return nil // sent again after the round ended: the agent has the findings
	}
	res := *rep.Review
	t.SeatOf(r).Res = &res
	m.saveReview()
	Debugf("review %s: %s sent its review", m.AgentName(t), r.Agent)
	branch := ""
	worktree := t.Dir
	if len(t.Worktrees) > 0 {
		worktree, branch = t.Worktrees[0].Path, t.Worktrees[0].Branch
	}
	rec := codereview.Record{Result: res, Worktree: worktree, Branch: branch, Agent: string(r.Agent),
		Author: string(t.Agent), Round: run.Round, Time: time.Now().Format(time.RFC3339), Run: run.Id}
	if err := codereview.Save(rec); err != nil {
		m.Toast(t.Name + ": could not keep the review: " + err.Error())
	}
	if !m.RoundDone(t) {
		m.Toast(t.Name + ": " + string(r.Agent) + ": " + res.Brief() + " · waiting for the other reviewers")
		m.Tell(PanesMsg{})
		return m.Tell(ReviewMsg{})
	}
	return m.EndRound(t)
}

// endRound goes on from a round all of t's reviewers have sent their
// review in: the findings go to the agent, or the review ends.
func (m *Core) EndRound(t *Tab) Cmd {
	run := t.Review
	briefs := append([]string{}, run.Approved...)
	var changes []codereview.Review
	var stay []*Seat
	for _, s := range run.Seats {
		briefs = append(briefs, string(s.R.Agent)+": "+s.Res.Brief())
		if s.Res.Verdict != codereview.Approve || len(s.Res.Findings) > 0 {
			changes = append(changes, codereview.Review{Agent: string(s.R.Agent), Result: *s.Res})
		}
		if s.Res.Verdict == codereview.Approve {
			run.Approved = append(run.Approved, string(s.R.Agent)+": approved")
			continue
		}
		stay = append(stay, s)
	}
	brief := strings.Join(briefs, " · ")
	run.Brief = brief
	says := ReviewSays(run.Seats)
	t.Reviewed = run.Tree
	sandboxed := false
	for _, s := range run.Seats {
		if s.R.InSandbox {
			sandboxed = true
			break
		}
	}
	// Findings from a sandboxed reviewer wait for the user before they go
	// to an agent outside a sandbox, asked for or not.
	auto := (run.Asked || m.Cfg.Review.SendFindings == config.FindingsAuto) && (!sandboxed || t.InSandbox)
	fixPrompt := func() string {
		if run.Asked {
			return codereview.AskedFixPrompt(changes, run.Round, run.Rounds, askAgain(run.Seats))
		}
		return codereview.FixPrompt(changes, run.Round, run.Rounds)
	}
	switch {
	case len(stay) == 0:
		// Approval ends the review, but does not discard its remarks.
		// Queue them before endReview saves the session. No reviewer stays
		// around waiting for optional remarks to be addressed.
		var remarks state.Message
		if len(changes) > 0 {
			remarks = state.Message{Text: fixPrompt(),
				Auto: auto, Sandboxed: sandboxed}
			t.Messages = append([]state.Message{remarks}, t.Messages...)
		}
		m.EndReview(t, brief, true)
		if !auto {
			// Asked about the remarks; with none, told of the approval,
			// which is otherwise only a line in the panel.
			t.ReviewDone = &state.ReviewDone{Brief: brief, Msg: remarks, Approved: true, Says: says}
		}
	case run.Asked:
		// The agent that asked has all of it, and asks again if it wants
		// another look: first, ahead of what waited for the review.
		fix := state.Message{Text: fixPrompt(), Auto: auto, Sandboxed: sandboxed}
		t.Messages = append([]state.Message{fix}, t.Messages...)
		m.EndReview(t, brief, true)
		if !auto {
			t.ReviewDone = &state.ReviewDone{Brief: brief, Msg: fix, Says: says}
		}
	case run.Round >= run.Rounds:
		// The user decides what is left: the findings wait in F3. Where
		// they are is not in the brief, which outlives them there.
		fix := state.Message{Text: fixPrompt(), Sandboxed: sandboxed}
		t.Messages = append(t.Messages, fix)
		brief += " after " + Plural(run.Round, "round")
		m.EndReview(t, brief, true)
		t.ReviewDone = &state.ReviewDone{Brief: brief, Msg: fix, Says: says}
	default:
		// The ones that approved have had their say.
		for _, s := range run.Seats {
			if s.Res.Verdict == codereview.Approve {
				if s.R.Pane != nil {
					go s.R.Pane.Close()
				}
				m.DropReviewPane(s.R)
			}
		}
		run.Seats = stay
		run.FixMsg = state.Message{Text: fixPrompt(), Auto: auto, Sandboxed: sandboxed}
		// Ahead of what waited for the review: the fixes are for the
		// work as it was reviewed.
		t.Messages = append([]state.Message{run.FixMsg}, t.Messages...)
		run.Phase = Fixing
		t.ReviewBrief = brief
		m.SaveSession()
		if auto {
			m.Toast(t.Name + ": " + brief + " — sent to " + string(t.Agent))
		} else {
			m.Toast(t.Name + ": " + brief + " — in " + ScreenRef(3) + " for you to send to " + string(t.Agent))
			t.ReviewDone = &state.ReviewDone{Brief: brief, Msg: run.FixMsg, Says: says}
		}
		m.Tell(ReviewNewsMsg{T: m.Ref(t), Text: brief})
	}
	m.SaveSession() // with what is still to be told of
	m.Tell(PanesMsg{})
	return m.Tell(ReviewMsg{}) // the panel shows the findings
}

// sendFindings sets the findings msg in t's messages to go to its agent by
// themselves, as space does in F3, the next time it is idle.
func (m *Core) SendFindings(t *Tab, msg state.Message) {
	i := t.FindMessage(msg)
	if !m.Live(t) || i < 0 {
		return
	}
	t.Messages[i].Auto = true
	t.Messages[i].Sandboxed = false
	if run := t.Review; run != nil && run.FixMsg == msg {
		run.FixMsg = t.Messages[i] // still the findings the review waits on
	}
	m.SaveSession()
	m.Tell(PanesMsg{})
	m.Toast(t.Name + ": the findings go to " + string(t.Agent))
}

// endReview stops t's reviewers, with brief as how the review ended.
func (m *Core) EndReview(t *Tab, brief string, notify bool) {
	run := t.Review
	if run == nil {
		return
	}
	if run.Phase == Fixing {
		if i := t.FindMessage(run.FixMsg); i >= 0 && run.FixMsg.Auto {
			// Sent by itself to an agent the review no longer waits on:
			// left for the user instead.
			t.Messages[i].Auto = false
		}
	}
	for _, s := range run.Seats {
		if p := s.R.Pane; p != nil {
			go p.Close()
		}
		m.DropReviewPane(s.R)
	}
	approved := len(run.Seats) > 0
	for _, s := range run.Seats {
		if s.Res == nil || s.Res.Verdict != codereview.Approve {
			approved = false
		}
	}
	t.Review = nil
	t.ReviewBrief, t.ReviewApproved, t.FindingsSent = brief, approved, false
	m.SaveSession()
	m.Tell(PanesMsg{})
	m.Toast(t.Name + ": review " + brief)
	if notify {
		m.Tell(ReviewNewsMsg{T: m.Ref(t), Text: brief})
	}
}

// dropReviewer stops r, one of t's reviewers, for why; the review goes on
// with the others, or ends with it when it was the last.
func (m *Core) DropReviewer(t, r *Tab, why string, notify bool) Cmd {
	run := t.Review
	if len(run.Seats) <= 1 {
		m.EndReview(t, why, notify)
		return nil
	}
	for i, s := range run.Seats {
		if s.R == r {
			run.Seats = append(run.Seats[:i:i], run.Seats[i+1:]...)
			break
		}
	}
	m.saveReview()
	if p := r.Pane; p != nil {
		go p.Close()
	}
	m.DropReviewPane(r)
	m.Toast(t.Name + ": " + why + " · the review goes on with " + m.ReviewerNames(t))
	if run.Phase == Reviewing && m.RoundDone(t) {
		return m.EndRound(t)
	}
	m.Tell(PanesMsg{})
	return nil
}

// detectReviewer is detect for t's reviewers.
func (m *Core) DetectReviewer(t *Tab, now time.Time) Cmd {
	if t.Review == nil {
		return nil
	}
	var cmds []Cmd
	for _, s := range append([]*Seat{}, t.Review.Seats...) {
		cmds = append(cmds, m.DetectSeat(t, s, now))
		if t.Review == nil {
			break
		}
	}
	return Batch(cmds...)
}

// detectSeat is detect for s, one of t's reviewers.
func (m *Core) DetectSeat(t *Tab, s *Seat, now time.Time) Cmd {
	r := s.R
	if t.SeatOf(r) == nil {
		return nil // let go by a reviewer before it
	}
	visible := m.Client.AgentVisible(m.Ref(r))
	was := r.Light
	r.Classify(visible, now)
	if r.Light != was && r.Light == StBlocked {
		m.Tell(LightMsg{A: m.Ref(r), Visible: visible, Now: now})
		m.Tell(PanesMsg{}) // the panel says it asks something
	}
	if r.Light == StExited && was != StExited {
		why := ""
		if _, err, _ := r.Pane.Exited(); err != nil {
			why = " (" + err.Error() + ")"
		}
		if r.Model != "" {
			// A model it does not have stops it as it starts.
			why += ", started with model " + r.Model + ": if " + string(r.Agent) + " has none of that name, pick another with its model button in a review pane"
		}
		return m.DropReviewer(t, r, string(r.Agent)+" stopped before it sent a review"+why, true)
	}
	if was.AtWork() && (r.Light == StIdle || r.Light == StDone) && t.Review.Phase == Reviewing && s.Res == nil && r.IdleUnasked() {
		// It ended its turn with no review sent: asking the user
		// something in words, it lost the thread, or what it ran to send
		// it never reached nerdr.
		if m.CutOff(r) != "" {
			m.Tell(ReviewNewsMsg{T: m.Ref(t), Text: "waits: " + string(r.Agent) + " could not send its review to nerdr"})
			m.TellCutOff(r)
		} else {
			m.Tell(ReviewNewsMsg{T: m.Ref(t), Text: "waits: " + string(r.Agent) + " stopped without sending one: see its pane"})
		}
		m.Tell(PanesMsg{})
	}
	m.CheckSent(r, now)
	return m.DeliverQueue(r, now)
}

// findingsDone takes the brief of a review that has ended off the panel
// once t's agent has ended its turn on the findings: it would go on
// counting findings that are fixed.
func (m *Core) FindingsDone(t *Tab) {
	if !t.FindingsSent || t.Review != nil || t.FindingsQueued() {
		return
	}
	t.FindingsSent, t.ReviewBrief, t.ReviewApproved = false, "", false
	m.SaveSession()
	m.Tell(PanesMsg{})
}

// ReviewPhase is where a review stands.
type ReviewPhase int

const (
	// Reviewing: the reviewer is looking.
	Reviewing ReviewPhase = iota
	// Fixing: its findings are with the tab's agent, or waiting in F3 to
	// be sent to it.
	Fixing
)

// ReviewRun is a review going on in a tab.
type ReviewRun struct {
	Seats  []*Seat // the reviewers still at it
	Phase  ReviewPhase
	Round  int
	Rounds int
	// Id is kept with each reviewer's record, to tell this review's apart.
	Id   string
	Base string
	// Focus is what the agent asked to have looked at most, if anything.
	Focus string
	// Tree is the worktree as the reviewers were asked to look at it.
	Tree string
	// FixMsg is the findings in the tab's messages, until they are sent.
	FixMsg state.Message
	// Brief is how the last round came out, all reviewers together;
	// approved, the reviewers that approved in it or before and were let
	// go.
	Brief    string
	Approved []string
	// Asked is set on a review the agent asked for with nerdr
	// request-review: its findings always go to the agent, and the agent
	// asks for another round when it wants one.
	Asked bool
}

// Seat is a reviewer in a review.
type Seat struct {
	R *Tab
	// Res is its review this round, nil until it is sent.
	Res *codereview.Result
}

// SeatOf is r's seat in t's review, or nil.
func (t *Tab) SeatOf(r *Tab) *Seat {
	if t == nil || t.Review == nil {
		return nil
	}
	for _, s := range t.Review.Seats {
		if s.R == r {
			return s
		}
	}
	return nil
}

// Lit is the light t shows in the bars: of its agent and the others in its
// panes, each raised by its reviewers', the one inTabRank puts first. One
// agent asking something is red over all; past that, one still working
// is yellow over the others' green, as with a reviewer: the tab's work is
// not over while any of its agents is at it, as when agents done with
// their part hand it back to one that carries on.
func (t *Tab) Lit() Status {
	s := t.AgentLit()
	for _, a := range t.ExtraAgents() {
		if x := a.AgentLit(); inTabRank(x) > inTabRank(s) {
			s = x
		}
	}
	return s
}

// inTabRank ranks the lights of the agents in one tab for Lit: as
// StatusRank does, but with work going on over work done, which
// StatusRank puts first only between tabs, where a done one is another
// piece of work waiting for the user.
func inTabRank(s Status) int {
	switch s {
	case StBlocked:
		return StatusRank(StBlocked)
	case StWorking:
		return StatusRank(StBlocked) - 1
	case StBackground:
		return StatusRank(StBlocked) - 2
	case StDone:
		return StatusRank(StBlocked) - 3
	}
	return StatusRank(s) - 3
}

// AgentLit is agent t's light, unless a reviewer of its work wants more
// of a look. A reviewer asking something is red over anything, one
// working yellow over a finished agent's green, one with only a process
// left running cyan over it: the review is not over. A review done whose
// findings wait for the user to say whether they go to the agent is red
// too: it is asking something of them.
func (t *Tab) AgentLit() Status {
	s := t.Light
	if s == StBlocked || t.FindingsToDecide() {
		return StBlocked
	}
	if t.Review == nil {
		return s
	}
	for _, seat := range t.Review.Seats {
		switch seat.R.Light {
		case StBlocked:
			return StBlocked
		case StWorking:
			s = StWorking
		case StBackground:
			if s != StWorking {
				s = StBackground
			}
		}
	}
	return s
}

// ReviewHolds reports whether t's messages wait for its reviewer.
func (t *Tab) ReviewHolds() bool {
	return t.Review != nil && t.Review.Phase == Reviewing
}

// Reviewer is who is to review: an agent of kind, started with model, or
// the model the config gives it when that is "".
type Reviewer struct {
	Kind  agents.Kind
	Model string
}

// askAgain is the command that asks for another review by the same
// reviewers, with the same models.
func askAgain(seats []*Seat) string {
	cmd := `"${NERDR_BIN:-nerdr}" request-review`
	for _, s := range seats {
		cmd += " --agent " + string(s.R.Agent)
		if s.R.Model != "" {
			cmd += " --model " + s.R.Model
		}
	}
	return cmd
}

// ReadyAsk is an agent's word that its work is ready for a review: what
// it wants looked at most, and who it asked to review it, if it said:
// one agent or more, to review together.
type ReadyAsk struct {
	Focus string
	By    []Reviewer
}

// ReviewToTell is the review done in cur, or one of its agents, that is
// still worth telling of — by askReviewDone, a peek while the tabs are
// walked and a mark on cur's entry — and the tab it is of; nil if there
// is none.
// One whose findings have gone does not hide another agent's that wait.
func (cur *Tab) ReviewToTell() (*Tab, *state.ReviewDone) {
	for _, a := range append([]*Tab{cur}, cur.ExtraAgents()...) {
		if a.ReviewDone != nil && !a.ReviewDoneStale() {
			return a, a.ReviewDone
		}
	}
	return nil, nil
}

// ReviewDoneStale reports whether the findings t.ReviewDone asks about are
// no longer t's to send: sent, dropped or set to go from F3 already.
func (t *Tab) ReviewDoneStale() bool {
	msg := t.ReviewDone.Msg
	if msg.Text == "" {
		return false
	}
	i := t.FindMessage(msg)
	return i < 0 || t.Messages[i].Auto
}

// FindingsToDecide reports whether a review done in t has findings, or
// remarks, that wait for the user to say whether they go to t's agent.
func (t *Tab) FindingsToDecide() bool {
	return t.ReviewDone != nil && t.ReviewDone.Msg.Text != "" && !t.ReviewDoneStale()
}

// FindingsWait reports whether a review's findings wait in t's messages
// for the user to send.
func (t *Tab) FindingsWait() bool {
	for _, msg := range t.Messages {
		if !msg.Auto && codereview.IsFixPrompt(msg.Text) {
			return true
		}
	}
	return false
}

// FindingsQueued reports whether t has a review's findings set to go to
// its agent by themselves.
func (t *Tab) FindingsQueued() bool {
	for _, msg := range t.Messages {
		if msg.Auto && codereview.IsFixPrompt(msg.Text) {
			return true
		}
	}
	return false
}

// IdleUnasked reports whether t has nothing more on its way to it: no
// message waiting to be typed in, none just sent.
func (t *Tab) IdleUnasked() bool {
	return t.NextAuto() < 0 && !t.AwaitWork
}

// OnTree goes on from a look at a tab's worktree.
func (m *Core) OnTree(msg TreeMsg) Cmd {
	t := msg.T
	if !m.Live(t) {
		return nil
	}
	if msg.Err != nil {
		if msg.Why != TreeAuto {
			m.Toast(t.Name + ": could not look at the worktree for a review: " + msg.Err.Error())
		}
		return nil
	}
	switch msg.Why {
	case TreeAuto, TreeHand:
		if t.Review != nil {
			if msg.Why == TreeHand && msg.By.Kind != "" {
				// Picked while the look for another reviewer was on its
				// way: it joins that one.
				return m.AddReviewer(t, msg.By)
			}
			return nil
		}
		if msg.Why == TreeAuto && (t.Light.AtWork() || t.NextAuto() >= 0) {
			// The agent was given more to do while the worktree was
			// looked at: the end of that turn is the time.
			return nil
		}
		var focus string
		var by []Reviewer
		// Asked for by the agent, rather than started by hand.
		asked := t.ReviewAsk != nil && msg.Why == TreeAuto
		if msg.By.Kind != "" {
			by = []Reviewer{msg.By}
		}
		if a := t.ReviewAsk; a != nil {
			focus = a.Focus
			if len(by) == 0 {
				by = a.By
			}
		}
		t.ReviewAsk = nil
		if !msg.Changed {
			if msg.Why == TreeHand {
				m.Toast(t.Name + ": nothing to review — the worktree is as its base")
			}
			return nil
		}
		if msg.Why == TreeAuto && msg.Tree == t.Reviewed {
			return nil // reviewed as it is
		}
		if len(by) == 0 {
			by = []Reviewer{{Kind: m.ReviewKind(t)}}
		}
		return m.startReview(t, by, msg.Base, msg.Tree, focus, asked)
	case TreeFixed:
		run := t.Review
		if run == nil || run.Phase != Fixing {
			return nil
		}
		if msg.Tree == run.Tree {
			m.EndReview(t, run.Brief+" · "+string(t.Agent)+" changed nothing", true)
			return nil
		}
		run.Round++
		run.Tree = msg.Tree
		run.Phase = Reviewing
		for _, s := range run.Seats {
			s.Res = nil
			m.AddMessage(s.R, codereview.Again(string(t.Agent), run.Round, run.Rounds), true)
		}
		m.saveReview()
		m.Tell(PanesMsg{})
		m.Toast(t.Name + ": " + m.ReviewerNames(t) + " review again (round " + strconv.Itoa(run.Round) + ")")
		return nil
	}
	return nil
}

// StartReview starts by reviewing t's work, looking most at focus.
func (m *Core) StartReview(t *Tab, by Reviewer, base, tree, focus string) Cmd {
	return m.startReview(t, []Reviewer{by}, base, tree, focus, false)
}

// startReview starts by reviewing t's work together, in one round. One
// the agent asked for (asked) has a round only: its findings go to the
// agent, which asks for another when it wants one.
func (m *Core) startReview(t *Tab, by []Reviewer, base, tree, focus string, asked bool) Cmd {
	rounds := max(m.Cfg.Review.Rounds, 1)
	if asked {
		rounds = 1
	}
	t.Review = &ReviewRun{Round: 1, Rounds: rounds, Base: base, Tree: tree, Focus: focus, Asked: asked,
		Id: time.Now().Format(time.RFC3339Nano)}
	var cmd Cmd
	for _, b := range by {
		cmd = Batch(cmd, m.AddReviewer(t, b))
	}
	if len(t.Review.Seats) == 0 {
		t.Review = nil
	}
	return cmd
}

// AddReviewer starts by reviewing t's work, beside the reviewers at it
// already, in the round they are in.
func (m *Core) AddReviewer(t *Tab, by Reviewer) Cmd {
	k, model := by.Kind, by.Model
	if model == "" {
		model = m.Cfg.Review.Models[k]
	}
	if !agents.TakesModel(k) {
		model = ""
	}
	// The review prompt contains the author's text and the reviewer reads
	// its worktree. Keep that work inside a sandbox too, including when
	// the config changed while the worktree was being inspected.
	if m.Confined(hooks.Report{}, t) && !m.SandboxedKind(k) {
		m.Toast(t.Name + ": a sandboxed agent's work needs a sandboxed reviewer")
		return nil
	}
	run := t.Review
	if run.Phase != Reviewing {
		m.Toast(t.Name + ": " + string(t.Agent) + " is on the findings · another reviewer can join when the work is reviewed again")
		return nil
	}
	for _, s := range run.Seats {
		if s.R.Agent == k {
			m.Toast(t.Name + ": " + string(k) + " is reviewing already")
			return nil
		}
	}
	r := &Tab{AgentState: Agent{Tab: state.Tab{
		ID: t.ID + "-review-" + string(k), Name: t.Owner().Name + " · review", Dir: t.Dir, Agent: k, Worktrees: t.Worktrees,
		Model: model,
	}, Light: StStarting}, ReviewOf: t}
	req := codereview.Request(string(t.Agent), string(k), run.Base, run.Focus, run.Round, run.Rounds)
	args := slices.Clone(m.Cfg.Review.Args[k])
	first := agents.StartPrompt(k, req)
	if first == nil {
		r.Messages = []state.Message{{Text: req, Auto: true}}
	}
	add := func(l agents.Launch) agents.Launch {
		l.Argv = slices.Concat(l.Argv, args, first)
		return l
	}
	p := m.ReviewPaneFor(t, r)
	cmd := m.SpawnNew(r, agents.WithModel(m.Cfg.Agents, k, model), add)
	if r.Pane == nil && r.Light == StExited {
		m.Toast(t.Name + ": " + r.Err)
		p.A = nil
		return cmd
	}
	// Started, or to start once its CLI has answered (see SpawnNew and
	// Spawn).
	run.Seats = append(run.Seats, &Seat{R: r})
	m.saveReview()
	m.Tell(TermsMsg{T: m.Ref(t.Owner())})
	m.Tell(PanesMsg{}) // the panes say so
	with := string(k)
	if model != "" {
		with += " (" + model + ")"
	}
	Debugf("review %s: %s started", m.AgentName(t), with)
	m.Toast(m.AgentName(t) + ": " + with + " reviews " + string(t.Agent) + "'s work, in a pane beside it")
	return cmd
}

// ReviewPaneFor is the pane reviewer r of of's work is shown in: the
// pane it was picked in, or, for a review that started by itself, a new
// one beside of's that leaves the keyboard where it is.
func (m *Core) ReviewPaneFor(of, r *Tab) *PaneTab {
	o := of.Owner()
	for _, p := range o.Panes() {
		if p.Kind == PaneReview && p.Of == of && p.A == nil {
			p.A = r
			return p
		}
	}
	p := &PaneTab{Kind: PaneReview, A: r, Of: of}
	ps := o.Panes()
	at := len(ps) - 1
	if op := of.PaneOf(); op != nil {
		at = slices.Index(ps, op)
	}
	o.PaneList = slices.Insert(ps, at+1, p)
	return p
}

// ReviewerNotStarted takes reviewer r, whose agent could not be started
// after its seat was taken (see OnSpawnReady), out of its review.
func (m *Core) ReviewerNotStarted(r *Tab) {
	t := r.ReviewOf
	m.Toast(t.Name + ": " + r.Err)
	for _, p := range t.Owner().Panes() {
		if p.A == r {
			p.A = nil
		}
	}
	if run := t.Review; run != nil {
		run.Seats = slices.DeleteFunc(run.Seats, func(s *Seat) bool { return s.R == r })
		if len(run.Seats) == 0 {
			t.Review = nil
		}
	}
	m.saveReview()
	m.Tell(PanesMsg{})
}
