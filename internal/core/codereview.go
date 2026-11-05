package core

import (
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
	avail := agents.Available(m.Cfg.Agents)
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
	return m.Cfg.Review.When == config.WhenReady && m.Client.SkillOn(t, "nerdr-request-review")
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
	if k := agents.Kind(rep.Kind); k != "" {
		switch {
		case !Contains(agents.Available(m.Cfg.Agents), k):
			m.Toast(t.Name + ": there is no agent " + string(k) + " here to review with; the configured reviewer reviews")
		case rep.Model != "" && !agents.TakesModel(k):
			m.Toast(t.Name + ": nerdr cannot start " + string(k) + " with a model of its choosing; it reviews with its own")
			ask.By = Reviewer{Kind: k}
		default:
			ask.By = Reviewer{Kind: k, Model: rep.Model}
		}
	}
	t.ReviewAsk = ask
	Debugf("review %s: ready, agent %s", m.Client.AgentName(t), t.Light.Label())
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
	m.Client.SaveSession()
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
	Debugf("review %s: %s sent its review", m.Client.AgentName(t), r.Agent)
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
		m.Client.ResizePanes()
		return m.Client.PinReview()
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
	auto := m.Cfg.Review.SendFindings == config.FindingsAuto && (!sandboxed || t.InSandbox)
	switch {
	case len(stay) == 0:
		// Approval ends the review, but does not discard its remarks.
		// Queue them before endReview saves the session. No reviewer stays
		// around waiting for optional remarks to be addressed.
		var remarks state.Message
		if len(changes) > 0 {
			remarks = state.Message{Text: codereview.FixPrompt(changes, run.Round, run.Rounds),
				Auto: auto, Sandboxed: sandboxed}
			t.Messages = append([]state.Message{remarks}, t.Messages...)
		}
		m.EndReview(t, brief, true)
		if !auto {
			// Asked about the remarks; with none, told of the approval,
			// which is otherwise only a line in the panel.
			t.ReviewDone = &state.ReviewDone{Brief: brief, Msg: remarks, Approved: true, Says: says}
		}
	case run.Round >= run.Rounds:
		// The user decides what is left: the findings wait in F3. Where
		// they are is not in the brief, which outlives them there.
		fix := state.Message{Text: codereview.FixPrompt(changes, run.Round, run.Rounds), Sandboxed: sandboxed}
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
				m.Client.DropReviewPane(s.R)
			}
		}
		run.Seats = stay
		run.FixMsg = state.Message{Text: codereview.FixPrompt(changes, run.Round, run.Rounds), Auto: auto, Sandboxed: sandboxed}
		// Ahead of what waited for the review: the fixes are for the
		// work as it was reviewed.
		t.Messages = append([]state.Message{run.FixMsg}, t.Messages...)
		run.Phase = Fixing
		t.ReviewBrief = brief
		m.Client.SaveSession()
		if auto {
			m.Toast(t.Name + ": " + brief + " — sent to " + string(t.Agent))
		} else {
			m.Toast(t.Name + ": " + brief + " — in " + m.Client.ScreenKey(3) + " for you to send to " + string(t.Agent))
			t.ReviewDone = &state.ReviewDone{Brief: brief, Msg: run.FixMsg, Says: says}
		}
		m.Client.NotifyReview(t, brief)
	}
	m.Client.SaveSession() // with what is still to be told of
	m.Client.ResizePanes()
	return m.Client.PinReview() // the panel shows the findings
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
	m.Client.SaveSession()
	m.Client.ResizePanes()
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
		m.Client.DropReviewPane(s.R)
	}
	t.Review = nil
	t.ReviewBrief, t.FindingsSent = brief, false
	m.Client.SaveSession()
	m.Client.ResizePanes()
	m.Toast(t.Name + ": review " + brief)
	if notify {
		m.Client.NotifyReview(t, brief)
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
	if p := r.Pane; p != nil {
		go p.Close()
	}
	m.Client.DropReviewPane(r)
	m.Toast(t.Name + ": " + why + " · the review goes on with " + m.ReviewerNames(t))
	if run.Phase == Reviewing && m.RoundDone(t) {
		return m.EndRound(t)
	}
	m.Client.ResizePanes()
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
	visible := m.Client.AgentVisible(r)
	was := r.Light
	r.Classify(visible, now)
	if r.Light != was && r.Light == StBlocked {
		m.Client.NotifyChange(r, visible, now)
		m.Client.ResizePanes() // the panel says it asks something
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
			m.Client.NotifyReview(t, "waits: "+string(r.Agent)+" could not send its review to nerdr")
			m.TellCutOff(r)
		} else {
			m.Client.NotifyReview(t, "waits: "+string(r.Agent)+" stopped without sending one: see its pane")
		}
		m.Client.ResizePanes()
	}
	m.CheckSent(r, now)
	return m.DeliverQueue(r, now)
}

// reviewLine is what the panel above t's agent says of its review.
func (m *Core) ReviewLine(t *Tab) string {
	run := t.Review
	if run == nil {
		// Briefs kept before said where the findings were, and went on
		// saying it after they were sent.
		brief, _, _ := strings.Cut(t.ReviewBrief, " · findings in ")
		switch {
		case brief == "":
		case t.FindingsWait():
			brief += " · findings in " + m.Client.ScreenKey(3)
		case t.FindingsSent || t.FindingsQueued():
			brief += " · sent to " + string(t.Agent)
		}
		return brief
	}
	round := "round " + strconv.Itoa(run.Round) + "/" + strconv.Itoa(run.Rounds)
	switch {
	case run.Phase == Fixing && t.FindMessage(run.FixMsg) >= 0 && !run.FixMsg.Auto:
		return run.Brief + " · " + round + " · in " + m.Client.ScreenKey(3) + " for you to send to " + string(t.Agent)
	case run.Phase == Fixing:
		return run.Brief + " · " + round + " · " + string(t.Agent) + " is on it"
	}
	var each []string
	waits := false // on the user
	for _, s := range run.Seats {
		r := s.R
		switch {
		case s.Res != nil:
			each = append(each, string(r.Agent)+": "+s.Res.Brief())
		case r.Light == StBlocked:
			each = append(each, string(r.Agent)+" needs input to review")
			waits = true
		case r.Light == StExited || r.Pane == nil:
			each = append(each, string(r.Agent)+" is not running")
		case (r.Light == StIdle || r.Light == StDone) && r.IdleUnasked() && r.Worked && m.CutOff(r) != "":
			each = append(each, string(r.Agent)+" could not send its review: its sandbox keeps it from nerdr")
			waits = true
		case (r.Light == StIdle || r.Light == StDone) && r.IdleUnasked() && r.Worked:
			each = append(each, string(r.Agent)+" stopped without sending a review")
			waits = true
		default:
			each = append(each, string(r.Agent)+" reviewing")
		}
	}
	line := strings.Join(append(run.Approved, each...), " · ")
	if !waits {
		line += " · " + round
		// Said only when there are some: they go once the review is done.
		if len(t.Messages) > 0 {
			line += " · messages in " + m.Client.ScreenKey(3) + " wait for the review"
		}
	}
	return line
}

// findingsDone takes the brief of a review that has ended off the panel
// once t's agent has ended its turn on the findings: it would go on
// counting findings that are fixed.
func (m *Core) FindingsDone(t *Tab) {
	if !t.FindingsSent || t.Review != nil || t.FindingsQueued() {
		return
	}
	t.FindingsSent, t.ReviewBrief = false, ""
	m.Client.SaveSession()
	m.Client.ResizePanes()
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
// panes, the one that most wants a look, each raised by its reviewers'.
func (t *Tab) Lit() Status {
	s := t.AgentLit()
	for _, a := range t.ExtraAgents() {
		if x := a.AgentLit(); StatusRank(x) > StatusRank(s) {
			s = x
		}
	}
	return s
}

// AgentLit is agent t's light, unless a reviewer of its work wants more
// of a look. A reviewer asking something is red over anything, one
// working yellow over a finished agent's green, one with only a process
// left running cyan over it: the review is not over.
func (t *Tab) AgentLit() Status {
	s := t.Light
	if t.Review == nil || s == StBlocked {
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

// ReadyAsk is an agent's word that its work is ready for a review: what
// it wants looked at most, and who it asked to review it, if it said.
type ReadyAsk struct {
	Focus string
	By    Reviewer
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
