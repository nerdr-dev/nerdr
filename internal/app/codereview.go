package app

import (
	"strconv"
	"strings"
	"time"

	"nerdr/internal/agents"
	"nerdr/internal/codereview"
	"nerdr/internal/config"
	"nerdr/internal/git"
	"nerdr/internal/hooks"
	"nerdr/internal/state"

	tea "github.com/charmbracelet/bubbletea"
)

// A tab's work can be reviewed by a second agent (internal/codereview).
// The reviewer runs in the tab's worktree, in a pane of the tab's beside
// the agent (see reviewerpanel.go): what matters is its conclusion, which comes back through
// `nerdr post-review` and is shown against the code in the review panel and,
// in short, in the panel above the agent. Its findings go to the tab's
// agent as a message — kept in F3 for the user to send, or sent at once —
// and when the agent has worked on them, the reviewer looks again, until
// it approves or the rounds run out.
//
// Several agents can review the same work side by side, one of each kind:
// a round ends when all of them have sent their review, and their findings
// go to the agent together, in one message. One that approves is let go;
// the others look again at the agent's fixes.
//
// A reviewer is a tab of its own as far as running it goes (it is started,
// read off the screen and sent its messages as a tab's agent is), but it
// is not in the tab row: it has a pane in the tab of the agent it reviews
// (see reviewerpanel.go), and goes with it.
//
// While the reviewer looks, the tab's own messages wait, so the code does
// not change under it. A review starts by itself, when the config says so,
// once the agent finishes a turn that left the worktree other than the
// last review saw it: any such turn, or with when = "ready", one in which
// the agent said with `nerdr ready` that the work is ready for a review —
// it knows better than a changed file does whether it is halfway, and can
// say what it wants looked at most.

// reviewPhase is where a review stands.
type reviewPhase int

const (
	// reviewing: the reviewer is looking.
	reviewing reviewPhase = iota
	// fixing: its findings are with the tab's agent, or waiting in F3 to
	// be sent to it.
	fixing
)

// reviewRun is a review going on in a tab.
type reviewRun struct {
	seats  []*seat // the reviewers still at it
	phase  reviewPhase
	round  int
	rounds int
	// id is kept with each reviewer's record, to tell this review's apart.
	id   string
	base string
	// focus is what the agent asked to have looked at most, if anything.
	focus string
	// tree is the worktree as the reviewers were asked to look at it.
	tree string
	// fixMsg is the findings in the tab's messages, until they are sent.
	fixMsg state.Message
	// brief is how the last round came out, all reviewers together;
	// approved, the reviewers that approved in it or before and were let
	// go.
	brief    string
	approved []string
}

// seat is a reviewer in a review.
type seat struct {
	r *tab
	// res is its review this round, nil until it is sent.
	res *codereview.Result
}

// seatOf is r's seat in t's review, or nil.
func (t *tab) seatOf(r *tab) *seat {
	if t == nil || t.review == nil {
		return nil
	}
	for _, s := range t.review.seats {
		if s.r == r {
			return s
		}
	}
	return nil
}

// lit is the light t shows in the bars: of its agent and the others in its
// panes, the one that most wants a look, each raised by its reviewers'.
func (t *tab) lit() status {
	s := t.agentLit()
	for _, a := range t.extraAgents() {
		if x := a.agentLit(); statusRank(x) > statusRank(s) {
			s = x
		}
	}
	return s
}

// agentLit is agent t's light, unless a reviewer of its work wants more
// of a look. A reviewer asking something is red over anything, one
// working yellow over a finished agent's green: the review is not over.
func (t *tab) agentLit() status {
	s := t.status
	if t.review == nil || s == stBlocked {
		return s
	}
	for _, seat := range t.review.seats {
		switch seat.r.status {
		case stBlocked:
			return stBlocked
		case stWorking:
			s = stWorking
		}
	}
	return s
}

// reviewHolds reports whether t's messages wait for its reviewer.
func (t *tab) reviewHolds() bool {
	return t.review != nil && t.review.phase == reviewing
}

// live reports whether t is still an agent nerdr runs: a tab open in the
// tab row, an agent in one of its panes, or the reviewer of one of those.
func (m *Model) live(t *tab) bool {
	switch {
	case t.reviewOf != nil:
		return t.reviewOf.seatOf(t) != nil && m.live(t.reviewOf)
	case t.partOf != nil:
		return t.paneOf() != nil && m.tabIndex(t.partOf) >= 0
	}
	return m.tabIndex(t) >= 0
}

// reviewKind is the agent that reviews t's work: the config's, or with
// "other", another kind than t's that is installed — codex for claude and
// claude for the rest, where they are — or t's own when there is no other.
func (m *Model) reviewKind(t *tab) agents.Kind {
	avail := agents.Available(m.cfg.Agents)
	confined := m.confined(hooks.Report{}, t)
	if k := agents.Kind(m.cfg.Review.Agent); k != config.ReviewOther && k != "" {
		if !confined || m.sandboxedKind(k) {
			return k
		}
	}
	prefer := []agents.Kind{agents.Claude, agents.Codex}
	if t.Agent == agents.Claude {
		prefer = []agents.Kind{agents.Codex}
	}
	for _, k := range prefer {
		if k != t.Agent && contains(avail, k) && (!confined || m.sandboxedKind(k)) {
			return k
		}
	}
	for _, k := range avail {
		if k != t.Agent && (!confined || m.sandboxedKind(k)) {
			return k
		}
	}
	return t.Agent
}

func contains(ks []agents.Kind, k agents.Kind) bool {
	for _, x := range ks {
		if x == k {
			return true
		}
	}
	return false
}

// treeMsg is the worktree as it is now, looked at for a review.
type treeMsg struct {
	t    *tab
	tree string
	// changed is whether the work differs from the branch's base at all.
	changed bool
	base    string
	err     error
	// why is what the look was for.
	why treeWhy
	// by is the reviewer asked for, for a review started by hand.
	by reviewer
}

// reviewer is who is to review: an agent of kind, started with model, or
// the model the config gives it when that is "".
type reviewer struct {
	kind  agents.Kind
	model string
}

// readyAsk is an agent's word that its work is ready for a review: what
// it wants looked at most, and who it asked to review it, if it said.
type readyAsk struct {
	focus string
	by    reviewer
}

type treeWhy int

const (
	treeAuto  treeWhy = iota // a turn ended: start a review if it is due
	treeHand                 // the user asked for a review
	treeFixed                // the agent worked on the findings: look again
)

// lookAtTree finds out, in the background, how t's worktree stands.
func (m *Model) lookAtTree(t *tab, why treeWhy, by reviewer) tea.Cmd {
	var paths []string
	for _, w := range t.Worktrees {
		paths = append(paths, w.Path)
	}
	if len(paths) == 0 {
		paths = []string{t.Dir}
	}
	name := t.Name
	return func() tea.Msg {
		start := time.Now()
		defer func() { debugf("review %s: worktree looked at in %s", name, time.Since(start).Round(time.Millisecond)) }()
		msg := treeMsg{t: t, why: why, by: by}
		var trees []string
		for _, p := range paths {
			tree, err := git.WorkTree(p)
			if err != nil {
				msg.err = err
				return msg
			}
			trees = append(trees, tree)
			base := git.DetectBase(p)
			if len(paths) == 1 {
				msg.base = base
			}
			from := "HEAD"
			if base != "" {
				from = git.MergeBase(p, base)
			}
			if git.TreeOf(p, from) != tree {
				msg.changed = true
			}
		}
		msg.tree = strings.Join(trees, ",")
		return msg
	}
}

// afterTurn is called when t's agent has finished a turn: a review is
// started if one is due, or one waiting on the agent's fixes goes on.
func (m *Model) afterTurn(t *tab) tea.Cmd {
	if t.reviewOf != nil || t.handoff != nil || t.Spinoff != nil {
		return nil
	}
	if run := t.review; run != nil {
		if run.phase == fixing && t.findMessage(run.fixMsg) < 0 {
			return m.lookAtTree(t, treeFixed, reviewer{})
		}
		return nil
	}
	if t.nextAuto() >= 0 {
		return nil // more is coming: the review waits for the end of it
	}
	if t.reviewAsk == nil && (!m.cfg.Review.Auto || m.waitsForReady(t)) {
		return nil
	}
	return m.lookAtTree(t, treeAuto, reviewer{})
}

// waitsForReady reports whether a review of t's work starts by itself only
// once its agent has said the work is ready for one. An agent without the
// skill that tells it how is not waited for: its turns are reviewed as
// with when = "turn".
func (m *Model) waitsForReady(t *tab) bool {
	return m.cfg.Review.When == config.WhenReady && m.skillOn(t, "nerdr-request-review")
}

// onReady takes an agent's word that its work is ready for a review: one
// starts when its turn ends, with what it asked to have looked at. With
// auto off too: the skill has the agent say so then only when the user
// asked for a review, or said yes to one.
func (m *Model) onReady(rep hooks.Report) tea.Cmd {
	t := m.agentByHook(rep.Pane)
	if t == nil || t.reviewOf != nil {
		return nil
	}
	if t.review != nil {
		return nil // the review going on looks again when the agent's turn ends
	}
	ask := &readyAsk{focus: rep.Focus}
	if k := agents.Kind(rep.Kind); k != "" {
		switch {
		case !contains(agents.Available(m.cfg.Agents), k):
			m.toast(t.Name + ": there is no agent " + string(k) + " here to review with; the configured reviewer reviews")
		case rep.Model != "" && !agents.TakesModel(k):
			m.toast(t.Name + ": nerdr cannot start " + string(k) + " with a model of its choosing; it reviews with its own")
			ask.by = reviewer{kind: k}
		default:
			ask.by = reviewer{kind: k, model: rep.Model}
		}
	}
	t.reviewAsk = ask
	debugf("review %s: ready, agent %s", m.agentName(t), t.status.label())
	if t.status == stIdle || t.status == stDone {
		return m.afterTurn(t) // said between turns: no turn end to wait for
	}
	return nil
}

// onTree goes on from a look at a tab's worktree.
func (m *Model) onTree(msg treeMsg) tea.Cmd {
	t := msg.t
	if !m.live(t) {
		return nil
	}
	if msg.err != nil {
		if msg.why != treeAuto {
			m.toast(t.Name + ": could not look at the worktree for a review: " + msg.err.Error())
		}
		return nil
	}
	switch msg.why {
	case treeAuto, treeHand:
		if t.review != nil {
			if msg.why == treeHand && msg.by.kind != "" {
				// Picked while the look for another reviewer was on its
				// way: it joins that one.
				return m.addReviewer(t, msg.by)
			}
			return nil
		}
		if msg.why == treeAuto && (t.status == stWorking || t.nextAuto() >= 0) {
			// The agent was given more to do while the worktree was
			// looked at: the end of that turn is the time.
			return nil
		}
		focus, by := "", msg.by
		if a := t.reviewAsk; a != nil {
			focus = a.focus
			if by.kind == "" {
				by = a.by
			}
		}
		t.reviewAsk = nil
		if !msg.changed {
			if msg.why == treeHand {
				m.toast(t.Name + ": nothing to review — the worktree is as its base")
			}
			return nil
		}
		if msg.why == treeAuto && msg.tree == t.Reviewed {
			return nil // reviewed as it is
		}
		if by.kind == "" {
			by = reviewer{kind: m.reviewKind(t)}
		}
		return m.startReview(t, by, msg.base, msg.tree, focus)
	case treeFixed:
		run := t.review
		if run == nil || run.phase != fixing {
			return nil
		}
		if msg.tree == run.tree {
			m.endReview(t, run.brief+" · "+string(t.Agent)+" changed nothing", true)
			return nil
		}
		run.round++
		run.tree = msg.tree
		run.phase = reviewing
		for _, s := range run.seats {
			s.res = nil
			m.addMessage(s.r, codereview.Again(string(t.Agent), run.round, run.rounds), true)
		}
		m.resizePanes()
		m.toast(t.Name + ": " + m.reviewerNames(t) + " review again (round " + strconv.Itoa(run.round) + ")")
		return nil
	}
	return nil
}

// startReview starts by reviewing t's work, looking most at focus.
func (m *Model) startReview(t *tab, by reviewer, base, tree, focus string) tea.Cmd {
	t.review = &reviewRun{round: 1, rounds: max(m.cfg.Review.Rounds, 1), base: base, tree: tree, focus: focus,
		id: time.Now().Format(time.RFC3339Nano)}
	cmd := m.addReviewer(t, by)
	if len(t.review.seats) == 0 {
		t.review = nil
	}
	return cmd
}

// setSession is the conversation t's agent is in. A reviewer's is recorded
// as one, so the tab it reviews never carries it on as its own: it was had
// in the same worktree.
func (m *Model) setSession(t *tab, id string) {
	t.SessionID = id
	m.saveSession()
	if t.reviewOf != nil && id != "" {
		_ = codereview.SaveSession(t.Dir, string(t.Agent), id)
	}
}

// addReviewer starts by reviewing t's work, beside the reviewers at it
// already, in the round they are in.
func (m *Model) addReviewer(t *tab, by reviewer) tea.Cmd {
	k, model := by.kind, by.model
	if model == "" {
		model = m.cfg.Review.Models[k]
	}
	if !agents.TakesModel(k) {
		model = ""
	}
	// The review prompt contains the author's text and the reviewer reads
	// its worktree. Keep that work inside a sandbox too, including when
	// the config changed while the worktree was being inspected.
	if m.confined(hooks.Report{}, t) && !m.sandboxedKind(k) {
		m.toast(t.Name + ": a sandboxed agent's work needs a sandboxed reviewer")
		return nil
	}
	run := t.review
	if run.phase != reviewing {
		m.toast(t.Name + ": " + string(t.Agent) + " is on the findings · another reviewer can join when the work is reviewed again")
		return nil
	}
	for _, s := range run.seats {
		if s.r.Agent == k {
			m.toast(t.Name + ": " + string(k) + " is reviewing already")
			return nil
		}
	}
	r := &tab{Tab: state.Tab{
		ID: t.ID + "-review-" + string(k), Name: t.owner().Name + " · review", Dir: t.Dir, Agent: k, Worktrees: t.Worktrees,
		Model: model,
	}, status: stStarting, reviewOf: t}
	req := codereview.Request(string(t.Agent), string(k), run.base, run.focus, run.round, run.rounds)
	l := agents.NewSession(agents.WithModel(m.cfg.Agents, k, model), k)
	l.Argv = append(l.Argv, m.cfg.Review.Args[k]...)
	if first := agents.StartPrompt(k, req); first != nil {
		l.Argv = append(l.Argv, first...)
	} else {
		r.Messages = []state.Message{{Text: req, Auto: true}}
	}
	p := m.reviewPaneFor(t, r)
	cmd := m.spawn(r, l, false)
	if r.pane == nil {
		m.toast(t.Name + ": " + r.err)
		p.a = nil
		return cmd
	}
	run.seats = append(run.seats, &seat{r: r})
	m.focusTerms(t.owner())
	m.resizePanes() // the panes say so
	with := string(k)
	if model != "" {
		with += " (" + model + ")"
	}
	debugf("review %s: %s started", m.agentName(t), with)
	m.toast(m.agentName(t) + ": " + with + " reviews " + string(t.Agent) + "'s work, in a pane beside it")
	return cmd
}

// reviewerNames are the agents reviewing t's work, as words.
func (m *Model) reviewerNames(t *tab) string {
	var names []string
	for _, s := range t.review.seats {
		names = append(names, string(s.r.Agent))
	}
	return strings.Join(names, " and ")
}

// onReview takes a reviewer's conclusion.
func (m *Model) onReview(rep hooks.Report) tea.Cmd {
	r := m.agentByHook(rep.Pane)
	if r == nil || r.reviewOf == nil || rep.Review == nil {
		return nil
	}
	t := r.reviewOf
	run := t.review
	if run.phase != reviewing {
		return nil // sent again after the round ended: the agent has the findings
	}
	res := *rep.Review
	t.seatOf(r).res = &res
	debugf("review %s: %s sent its review", m.agentName(t), r.Agent)
	branch := ""
	worktree := t.Dir
	if len(t.Worktrees) > 0 {
		worktree, branch = t.Worktrees[0].Path, t.Worktrees[0].Branch
	}
	rec := codereview.Record{Result: res, Worktree: worktree, Branch: branch, Agent: string(r.Agent),
		Author: string(t.Agent), Round: run.round, Time: time.Now().Format(time.RFC3339), Run: run.id}
	if err := codereview.Save(rec); err != nil {
		m.toast(t.Name + ": could not keep the review: " + err.Error())
	}
	if !m.roundDone(t) {
		m.toast(t.Name + ": " + string(r.Agent) + ": " + res.Brief() + " · waiting for the other reviewers")
		m.resizePanes()
		return m.pinReview()
	}
	return m.endRound(t)
}

// roundDone reports whether all of t's reviewers have sent their review.
func (m *Model) roundDone(t *tab) bool {
	for _, s := range t.review.seats {
		if s.res == nil {
			return false
		}
	}
	return len(t.review.seats) > 0
}

// endRound goes on from a round all of t's reviewers have sent their
// review in: the findings go to the agent, or the review ends.
func (m *Model) endRound(t *tab) tea.Cmd {
	run := t.review
	briefs := append([]string{}, run.approved...)
	var changes []codereview.Review
	var stay []*seat
	for _, s := range run.seats {
		briefs = append(briefs, string(s.r.Agent)+": "+s.res.Brief())
		if s.res.Verdict != codereview.Approve || len(s.res.Findings) > 0 {
			changes = append(changes, codereview.Review{Agent: string(s.r.Agent), Result: *s.res})
		}
		if s.res.Verdict == codereview.Approve {
			run.approved = append(run.approved, string(s.r.Agent)+": approved")
			continue
		}
		stay = append(stay, s)
	}
	brief := strings.Join(briefs, " · ")
	run.brief = brief
	says := reviewSays(run.seats)
	t.Reviewed = run.tree
	switch {
	case len(stay) == 0:
		// Approval ends the review, but does not discard its remarks.
		// Queue them before endReview saves the session. No reviewer stays
		// around waiting for optional remarks to be addressed.
		var remarks state.Message
		if len(changes) > 0 {
			remarks = state.Message{Text: codereview.FixPrompt(changes, run.round, run.rounds),
				Auto: m.cfg.Review.SendFindings == config.FindingsAuto}
			t.Messages = append([]state.Message{remarks}, t.Messages...)
		}
		m.endReview(t, brief, true)
		if m.cfg.Review.SendFindings != config.FindingsAuto {
			// Asked about the remarks; with none, told of the approval,
			// which is otherwise only a line in the panel.
			t.reviewDone = &reviewDone{brief: brief, msg: remarks, approved: true, says: says}
		}
	case run.round >= run.rounds:
		// The user decides what is left: the findings wait in F3. Where
		// they are is not in the brief, which outlives them there.
		fix := state.Message{Text: codereview.FixPrompt(changes, run.round, run.rounds)}
		t.Messages = append(t.Messages, fix)
		brief += " after " + plural(run.round, "round")
		m.endReview(t, brief, true)
		t.reviewDone = &reviewDone{brief: brief, msg: fix, says: says}
	default:
		// The ones that approved have had their say.
		for _, s := range run.seats {
			if s.res.Verdict == codereview.Approve {
				if s.r.pane != nil {
					go s.r.pane.Close()
				}
				m.dropReviewPane(s.r)
			}
		}
		run.seats = stay
		auto := m.cfg.Review.SendFindings == config.FindingsAuto
		run.fixMsg = state.Message{Text: codereview.FixPrompt(changes, run.round, run.rounds), Auto: auto}
		// Ahead of what waited for the review: the fixes are for the
		// work as it was reviewed.
		t.Messages = append([]state.Message{run.fixMsg}, t.Messages...)
		run.phase = fixing
		t.ReviewBrief = brief
		m.saveSession()
		if auto {
			m.toast(t.Name + ": " + brief + " — sent to " + string(t.Agent))
		} else {
			m.toast(t.Name + ": " + brief + " — in " + m.screenKey(3) + " for you to send to " + string(t.Agent))
			t.reviewDone = &reviewDone{brief: brief, msg: run.fixMsg, says: says}
		}
		m.notifyReview(t, brief)
	}
	m.resizePanes()
	return m.pinReview() // the panel shows the findings
}

// reviewDone is a review that has ended a round with its findings in the
// tab's messages, for the user to send, or approved with none: msg is
// then empty.
type reviewDone struct {
	brief string
	msg   state.Message
	// approved is set when the review ended in approval; says is what
	// each reviewer said of the work, for the dialog.
	approved bool
	says     []reviewSaid
}

// reviewSaid is a reviewer's conclusion, as the dialog shows it.
type reviewSaid struct {
	agent, brief, summary string
	approved              bool
}

// reviewSays is what the reviewers at seats said this round.
func reviewSays(seats []*seat) []reviewSaid {
	var out []reviewSaid
	for _, s := range seats {
		if s.res != nil {
			out = append(out, reviewSaid{agent: string(s.r.Agent), brief: s.res.Brief(), summary: s.res.Summary,
				approved: s.res.Verdict == codereview.Approve})
		}
	}
	return out
}

// askReviewDone asks, when the tab shown has a review's findings waiting
// in F3, whether to send them to its agent now or keep them there, or
// tells of a review that approved with none. It is
// asked once, the first time the tab is shown with nothing else open: at
// once when the review ends in it, or when the user comes back to it. Not
// while the prefix is on: walking the tabs with the arrows, the tab is
// only passed, and the enter that ends the walk would answer it unseen.
// The review may be of any of the tab's agents.
func (m *Model) askReviewDone() {
	cur := m.cur()
	if cur == nil || m.prefix || m.dialog != nil || m.editor != nil {
		return
	}
	var t *tab
	for _, a := range append([]*tab{cur}, cur.extraAgents()...) {
		if a.reviewDone != nil {
			t = a
			break
		}
	}
	if t == nil {
		return
	}
	d := t.reviewDone
	t.reviewDone = nil
	if d.msg.Text != "" {
		if i := t.findMessage(d.msg); i < 0 || t.Messages[i].Auto {
			return // sent, dropped or set to go from F3 already
		}
	}
	m.dialog = &reviewDoneDialog{t: t, d: d}
}

// sendFindings sets the findings msg in t's messages to go to its agent by
// themselves, as space does in F3, the next time it is idle.
func (m *Model) sendFindings(t *tab, msg state.Message) {
	i := t.findMessage(msg)
	if !m.live(t) || i < 0 {
		return
	}
	t.Messages[i].Auto = true
	if run := t.review; run != nil && run.fixMsg == msg {
		run.fixMsg = t.Messages[i] // still the findings the review waits on
	}
	m.saveSession()
	m.resizePanes()
	m.toast(t.Name + ": the findings go to " + string(t.Agent))
}

// endReview stops t's reviewers, with brief as how the review ended.
func (m *Model) endReview(t *tab, brief string, notify bool) {
	run := t.review
	if run == nil {
		return
	}
	if run.phase == fixing {
		if i := t.findMessage(run.fixMsg); i >= 0 && run.fixMsg.Auto {
			// Sent by itself to an agent the review no longer waits on:
			// left for the user instead.
			t.Messages[i].Auto = false
		}
	}
	for _, s := range run.seats {
		if p := s.r.pane; p != nil {
			go p.Close()
		}
		m.dropReviewPane(s.r)
	}
	t.review = nil
	t.ReviewBrief, t.FindingsSent = brief, false
	m.saveSession()
	m.resizePanes()
	m.toast(t.Name + ": review " + brief)
	if notify {
		m.notifyReview(t, brief)
	}
}

// dropReviewer stops r, one of t's reviewers, for why; the review goes on
// with the others, or ends with it when it was the last.
func (m *Model) dropReviewer(t, r *tab, why string, notify bool) tea.Cmd {
	run := t.review
	if len(run.seats) <= 1 {
		m.endReview(t, why, notify)
		return nil
	}
	for i, s := range run.seats {
		if s.r == r {
			run.seats = append(run.seats[:i:i], run.seats[i+1:]...)
			break
		}
	}
	if p := r.pane; p != nil {
		go p.Close()
	}
	m.dropReviewPane(r)
	m.toast(t.Name + ": " + why + " · the review goes on with " + m.reviewerNames(t))
	if run.phase == reviewing && m.roundDone(t) {
		return m.endRound(t)
	}
	m.resizePanes()
	return nil
}

// notifyReview tells of a review's conclusion as a finished turn is told
// of.
func (m *Model) notifyReview(t *tab, brief string) {
	if !m.cfg.NotifyOn("done") || (!m.blurred && t == m.cur()) {
		return
	}
	if !m.blurred {
		m.inApp = append(m.inApp, t.Name+": review "+brief)
		m.inAppTabs = append(m.inAppTabs, t)
		return
	}
	m.notes = append(m.notes, note{t: t, title: "nerdr · " + t.Name, body: "review " + brief})
}

// detectReviewer is detect for t's reviewers.
func (m *Model) detectReviewer(t *tab, now time.Time) tea.Cmd {
	if t.review == nil {
		return nil
	}
	var cmds []tea.Cmd
	for _, s := range append([]*seat{}, t.review.seats...) {
		cmds = append(cmds, m.detectSeat(t, s, now))
		if t.review == nil {
			break
		}
	}
	return tea.Batch(cmds...)
}

// detectSeat is detect for s, one of t's reviewers.
func (m *Model) detectSeat(t *tab, s *seat, now time.Time) tea.Cmd {
	r := s.r
	if t.seatOf(r) == nil {
		return nil // let go by a reviewer before it
	}
	visible := m.agentVisible(r)
	was := r.status
	r.classify(visible, now)
	if r.status != was && r.status == stBlocked {
		m.notifyChange(r, visible, now)
		m.resizePanes() // the panel says it asks something
	}
	if r.status == stExited && was != stExited {
		why := ""
		if _, err, _ := r.pane.Exited(); err != nil {
			why = " (" + err.Error() + ")"
		}
		return m.dropReviewer(t, r, string(r.Agent)+" stopped before it sent a review"+why, true)
	}
	if was == stWorking && (r.status == stIdle || r.status == stDone) && t.review.phase == reviewing && s.res == nil && r.idleUnasked() {
		// It ended its turn with no review sent: asking the user
		// something in words, it lost the thread, or what it ran to send
		// it never reached nerdr.
		if m.cutOff(r) != "" {
			m.notifyReview(t, "waits: "+string(r.Agent)+" could not send its review to nerdr")
			m.tellCutOff(r)
		} else {
			m.notifyReview(t, "waits: "+string(r.Agent)+" stopped without sending one: see its pane")
		}
		m.resizePanes()
	}
	m.checkSent(r, now)
	return m.deliverQueue(r, now)
}

// reviewLine is what the panel above t's agent says of its review.
func (m *Model) reviewLine(t *tab) string {
	run := t.review
	if run == nil {
		// Briefs kept before said where the findings were, and went on
		// saying it after they were sent.
		brief, _, _ := strings.Cut(t.ReviewBrief, " · findings in ")
		switch {
		case brief == "":
		case t.findingsWait():
			brief += " · findings in " + m.screenKey(3)
		case t.FindingsSent || t.findingsQueued():
			brief += " · sent to " + string(t.Agent)
		}
		return brief
	}
	round := "round " + strconv.Itoa(run.round) + "/" + strconv.Itoa(run.rounds)
	switch {
	case run.phase == fixing && t.findMessage(run.fixMsg) >= 0 && !run.fixMsg.Auto:
		return run.brief + " · " + round + " · in " + m.screenKey(3) + " for you to send to " + string(t.Agent)
	case run.phase == fixing:
		return run.brief + " · " + round + " · " + string(t.Agent) + " is on it"
	}
	var each []string
	waits := false // on the user
	for _, s := range run.seats {
		r := s.r
		switch {
		case s.res != nil:
			each = append(each, string(r.Agent)+": "+s.res.Brief())
		case r.status == stBlocked:
			each = append(each, string(r.Agent)+" needs input to review")
			waits = true
		case r.status == stExited || r.pane == nil:
			each = append(each, string(r.Agent)+" is not running")
		case (r.status == stIdle || r.status == stDone) && r.idleUnasked() && r.worked && m.cutOff(r) != "":
			each = append(each, string(r.Agent)+" could not send its review: its sandbox keeps it from nerdr")
			waits = true
		case (r.status == stIdle || r.status == stDone) && r.idleUnasked() && r.worked:
			each = append(each, string(r.Agent)+" stopped without sending a review")
			waits = true
		default:
			each = append(each, string(r.Agent)+" reviewing")
		}
	}
	line := strings.Join(append(run.approved, each...), " · ")
	if !waits {
		line += " · " + round + " · your messages wait"
	}
	return line
}

// findingsWait reports whether a review's findings wait in t's messages
// for the user to send.
func (t *tab) findingsWait() bool {
	for _, msg := range t.Messages {
		if !msg.Auto && codereview.IsFixPrompt(msg.Text) {
			return true
		}
	}
	return false
}

// findingsQueued reports whether t has a review's findings set to go to
// its agent by themselves.
func (t *tab) findingsQueued() bool {
	for _, msg := range t.Messages {
		if msg.Auto && codereview.IsFixPrompt(msg.Text) {
			return true
		}
	}
	return false
}

// findingsDone takes the brief of a review that has ended off the panel
// once t's agent has ended its turn on the findings: it would go on
// counting findings that are fixed.
func (m *Model) findingsDone(t *tab) {
	if !t.FindingsSent || t.review != nil || t.findingsQueued() {
		return
	}
	t.FindingsSent, t.ReviewBrief = false, ""
	m.saveSession()
	m.resizePanes()
}

// idleUnasked reports whether t has nothing more on its way to it: no
// message waiting to be typed in, none just sent.
func (t *tab) idleUnasked() bool {
	return t.nextAuto() < 0 && !t.awaitWork
}
