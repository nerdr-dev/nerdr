package core

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/forge"
	"github.com/nerdr-dev/nerdr/internal/forges"
	"github.com/nerdr-dev/nerdr/internal/prask"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// The [auto] settings have nerdr ask a tab's agent by itself when
// something happens to the pull request of the tab's branch: its checks
// come to fail, a review that is not an approval comes in, it comes to
// conflict with its base. What is asked is the message F2's f and a
// write, or one to rebase. With "ask" it is kept in the tab's F3 for the
// user to read and send, and they are told; with "auto" it is sent when
// the agent is idle, through the same queue.
//
// The watch of your pull requests (prwatch.go) is what sees it happen, so
// only your own pull requests are acted on, and only what changed while
// nerdr watched: a repository seen for the first time, or a check that
// was already failing, is not.
//
// Two of the messages quote what others wrote, and "auto" sends them
// unread. So "auto" is lowered to "ask" where the text comes from someone
// with no say in the repository (a review comment by a non-member, the CI
// log of a branch in a fork), where a repository's own .nerdr.toml asks
// for it and the user has not allowed that file, and after max_tries in
// a row without the trouble being cleared up between, so that an agent
// that cannot fix it is not sent the same again and again. A tab asleep
// or cold is not woken for it unless wake is set.

// autoJob is an [auto] event to act on for one pull request.
type autoJob struct {
	Event string
	Key   string // PrKey
	Entry PREntry
	Tab   *Tab
	Mode  string // config.AutoAsk or AutoSend
	// Why is why Mode is ask though auto was set, "" when it is not.
	Why string
	// PrevDone is the event's Done before this job, put back if the pull
	// request cannot be read, for the job to be tried again.
	PrevDone string
	// ID is the job's reading of the pull request, as AutoTry.Job has it.
	ID int64
}

// autoRetries is how many looks in a row a job whose pull request could
// not be read is tried again at.
const autoRetries = 3

// What is what the message asks, for F3, the panel and the bar.
func (j autoJob) What() string {
	n := j.Entry.PR.Ref()
	switch j.Event {
	case config.EventCIFailed:
		return "fix " + n + "'s failing CI"
	case config.EventReviewComments:
		return "draft replies to " + n + "'s reviews"
	case config.EventConflicts:
		return "rebase " + n + " onto " + j.Entry.PR.Base
	}
	return j.Event + " on " + n
}

// autoFetched is the pull request of an autoJob read afresh, with the
// failing jobs' logs for one of failing CI.
type autoFetched struct {
	Job  autoJob
	PR   forge.PR
	Logs string
	Err  string
}

// AutoMsg is an [auto] message kept for the user or sent to an agent, to
// tell in the bar, in the history (F5) and, while nerdr's window does not
// have focus, on the desktop.
type AutoMsg struct {
	T    TabRef
	Text string
}

// AutoMode is what is set for event on the pull requests of repo: its
// .nerdr.toml's setting where it has one, else the config file's. A
// repository's "auto" holds only once the user has allowed its file as
// it is; until then it is ask, and why says so.
func (m *Core) AutoMode(repo, event string) (mode, why string) {
	mode = m.Cfg.Auto.Mode(event)
	c, err := workspace.LoadRepoConfig(repo)
	if err != nil {
		return config.AutoMode(mode), ""
	}
	r := c.Auto.Mode(event)
	if r == "" {
		return config.AutoMode(mode), ""
	}
	r = config.AutoMode(r)
	if r == config.AutoSend && !workspace.Allowed(SetupAllowedFile(), repo, c) {
		return config.AutoAsk, "the repository's " + workspace.RepoConfigFile + " asks for auto, and you have not allowed it as it is"
	}
	return r, ""
}

// AutoOn reports whether any [auto] event is on, in the config file or
// in the .nerdr.toml of a repository a tab works in, so that the pull
// requests are watched for it. The files are read again at most as often
// as the pull requests are asked for.
func (m *Core) AutoOn(now time.Time) bool {
	for _, ev := range config.AutoEvents {
		if config.AutoMode(m.Cfg.Auto.Mode(ev)) != config.AutoOff {
			return true
		}
	}
	w := &m.PRWatch
	if !w.AutoLookedAt.IsZero() && now.Sub(w.AutoLookedAt) < m.GithubEvery(PrWatchEvery) {
		return w.AutoInRepos
	}
	w.AutoLookedAt, w.AutoInRepos = now, false
	seen := map[string]bool{}
	for _, t := range m.Tabs {
		for _, wt := range t.Worktrees {
			if seen[wt.Repo] {
				continue
			}
			seen[wt.Repo] = true
			c, err := workspace.LoadRepoConfig(wt.Repo)
			if err != nil {
				continue
			}
			for _, ev := range config.AutoEvents {
				if r := c.Auto.Mode(ev); r != "" && config.AutoMode(r) != config.AutoOff {
					w.AutoInRepos = true
					return true
				}
			}
		}
	}
	return false
}

// autoTries is the [auto] state of the pull request key, made if need be.
func (m *Core) autoTries(key string) map[string]state.AutoTry {
	w := &m.PRWatch
	if w.Auto == nil {
		w.Auto = map[string]map[string]state.AutoTry{}
	}
	if w.Auto[key] == nil {
		w.Auto[key] = map[string]state.AutoTry{}
	}
	return w.Auto[key]
}

// AutoJobs is what the [auto] settings act on for e, the pull request
// key, between two looks at it, before and now; and it keeps how far each
// event has got. It is called only for a repository that answered before,
// as news is told only of one.
func (m *Core) AutoJobs(key string, e PREntry, before, now state.PRSeen) []autoJob {
	tries := m.autoTries(key)
	var out []autoJob
	for _, ev := range config.AutoEvents {
		tr := tries[ev]
		prev := tr.Done
		var fire bool
		switch ev {
		case config.EventCIFailed:
			fire = autoCI(&tr, e.PR, before, now)
		case config.EventReviewComments:
			fire = autoReview(&tr, e.PR, before, now)
		case config.EventConflicts:
			fire = autoConflict(&tr, e.PR, before, now)
		}
		if tr == (state.AutoTry{}) {
			delete(tries, ev)
		} else {
			tries[ev] = tr
		}
		if !fire {
			continue
		}
		mode, why := m.AutoMode(e.Repo.Path, ev)
		if mode == config.AutoOff {
			continue
		}
		t := m.TabOnBranch(e.Repo.Path, e.Branch())
		if t == nil {
			continue // the news of it is told; there is no agent to ask
		}
		out = append(out, autoJob{Event: ev, Key: key, Entry: e, Tab: t, Mode: mode, Why: why, PrevDone: prev})
	}
	if len(tries) == 0 {
		delete(m.PRWatch.Auto, key)
	}
	return out
}

// autoCI is whether pr's failing checks are to be acted on now. Checks
// that come to fail on its head have it wait for that head; once none of
// its checks is still running, and some still fail, it is acted on, once
// for that head. All of them passing clears the tries up; none at all, as
// a head just pushed may have before CI has started, does not.
func autoCI(tr *state.AutoTry, pr forge.PR, before, now state.PRSeen) bool {
	fail, pending := prask.Failing(pr.Checks)
	if len(pr.Checks) > 0 && fail == 0 && pending == 0 {
		tr.Want, tr.Tries = "", 0
		return false
	}
	for k := range now.Failing {
		if !before.Failing[k] {
			tr.Want = pr.Head
		}
	}
	if tr.Want != pr.Head {
		tr.Want = "" // pushed past, or nothing waits
		return false
	}
	if pending > 0 {
		return false // the rest of the run first: one message for all of it
	}
	tr.Want = ""
	if len(now.Failing) == 0 || tr.Done == pr.Head {
		return false // run again and passed, or acted on already
	}
	tr.Done = pr.Head
	return true
}

// autoReview is whether a review has come in that asks for something: one
// not by the author that is not an approval; or one whose job could not
// read the pull request, kept in Want, to try again. An approval that
// comes in clears the tries up; the PR staying approved does not.
func autoReview(tr *state.AutoTry, pr forge.PR, before, now state.PRSeen) bool {
	var latest time.Time
	for author, rv := range now.Reviews {
		if old, ok := before.Reviews[author]; ok && !rv.At.After(old.At) {
			continue
		}
		if rv.State == "APPROVED" {
			tr.Tries = 0
			continue
		}
		if rv.At.After(latest) {
			latest = rv.At
		}
	}
	at := tr.Want
	if !latest.IsZero() {
		at = latest.UTC().Format(time.RFC3339Nano)
	}
	tr.Want = ""
	if at == "" || tr.Done == at {
		return false
	}
	tr.Done = at
	return true
}

// autoConflict is whether pr conflicts with its base and is to be acted
// on: once for each head, the first time only once it has come to
// conflict while watched, not for one that already did. A head pushed
// since it was acted on that conflicts still is acted on again. Its being
// mergeable again clears the tries up, and lets the same head's coming
// to conflict again (its base moved) be acted on.
func autoConflict(tr *state.AutoTry, pr forge.PR, before, now state.PRSeen) bool {
	if pr.Mergeable == "MERGEABLE" {
		tr.Tries, tr.Done = 0, ""
	}
	if !now.Conflicting || tr.Done == pr.Head || before.Conflicting && tr.Done == "" {
		return false
	}
	tr.Done = pr.Head
	return true
}

// StartAuto starts on jobs: the message of each is written, for the
// failing CI and the reviews after the pull request is read afresh, away
// from the loop.
func (m *Core) StartAuto(jobs []autoJob) Cmd {
	var cmds []Cmd
	for _, j := range jobs {
		switch j.Event {
		case config.EventConflicts:
			m.AutoDeliver(j, prask.RebasePrompt(j.Entry.PR))
		default:
			// The latest reading is the one that counts: one started at an
			// earlier look whose answer comes after it is dropped.
			id := time.Now().UnixNano()
			if id <= m.PRWatch.AutoJobAt {
				id = m.PRWatch.AutoJobAt + 1
			}
			m.PRWatch.AutoJobAt, j.ID = id, id
			tries := m.autoTries(j.Key)
			tr := tries[j.Event]
			tr.Job = id
			tries[j.Event] = tr
			cmds = append(cmds, fetchForAuto(j))
		}
	}
	if len(cmds) > 0 {
		m.SaveSession()
	}
	return Batch(cmds...)
}

func fetchForAuto(j autoJob) Cmd {
	return func() Event {
		out := autoFetched{Job: j}
		dir := j.Entry.Repo.Path
		pr, err := forges.FetchPR(dir, j.Entry.PR.Number)
		if err == nil && j.Event == config.EventCIFailed {
			out.Logs, err = forges.FailedRunLogs(dir, pr.Checks, prask.LogLimit)
		}
		out.PR = pr
		if err != nil {
			out.Err = err.Error()
		}
		return out
	}
}

// OnAutoFetched writes the message of a job whose pull request was read.
func (m *Core) OnAutoFetched(f autoFetched) {
	j := f.Job
	tries := m.autoTries(j.Key)
	tr := tries[j.Event]
	if tr.Job != j.ID {
		return // a later reading was started since: its answer counts
	}
	tr.Job = 0
	tries[j.Event] = tr
	if f.Err != "" {
		if tr.Retries < autoRetries {
			// Tried again at the next look: GitHub may answer then.
			tr.Retries++
			if j.Event == config.EventCIFailed {
				tr.Want = j.Entry.PR.Head
			} else {
				tr.Want = tr.Done
			}
			tr.Done = j.PrevDone
			tries[j.Event] = tr
			m.SaveSession()
			m.Tell(HistoryMsg{Cat: HistoryWarning, Text: j.Tab.Name + ": could not " + j.What() + " by itself, trying again at the next look: " + f.Err})
			return
		}
		tr.Retries = 0
		tries[j.Event] = tr
		m.SaveSession()
		m.Toast(j.Tab.Name + ": could not " + j.What() + " by itself: " + f.Err)
		return
	}
	if tr.Retries != 0 {
		tr.Retries = 0
		tries[j.Event] = tr
	}
	// What the watch has seen since the reading began counts over it: the
	// pull request closed, pushed past, or passing again.
	known, open := m.PRWatch.Known[j.Key]
	if !open || f.PR.State != "" && f.PR.State != "OPEN" {
		return
	}
	var text string
	switch j.Event {
	case config.EventCIFailed:
		if f.PR.Head != "" && f.PR.Head != j.Entry.PR.Head || known.Head != j.Entry.PR.Head || !failingOn(known, j.Entry.PR.Head) {
			return // pushed since, or passing: the new head's checks are what count
		}
		t, err := prask.FixMessage(f.PR, f.Logs)
		if err != nil {
			return // passing again
		}
		text = t
	case config.EventReviewComments:
		if text = prask.ReplyMessage(f.PR); text == "" {
			return // a review with no words, nor comments, to answer
		}
		if who := prask.Strangers(f.PR); len(who) > 0 && j.Mode == config.AutoSend {
			j.Mode, j.Why = config.AutoAsk, "it quotes "+joinNames(who)+", who "+isAre(len(who))+" not a member of the repository"
		}
	}
	m.AutoDeliver(j, text)
}

// AutoDeliver puts text, the message of j, in its tab's messages: sent by
// itself with auto, unless something lowers it to ask; and tells the user.
// A message of the same kind still waiting there is replaced, not added
// to.
func (m *Core) AutoDeliver(j autoJob, text string) {
	t := j.Tab
	if !m.Live(t) || text == "" {
		return
	}
	mode, why := j.Mode, j.Why
	tr := m.autoTries(j.Key)[j.Event]
	if mode == config.AutoSend {
		switch {
		case j.Event == config.EventCIFailed && j.Entry.PR.Fork != "":
			mode, why = config.AutoAsk, "its branch is in a fork, whose CI log is not sent unread"
		case tr.Tries >= m.Cfg.Auto.MaxTries:
			mode, why = config.AutoAsk, "it was sent by itself "+Plural(tr.Tries, "time")+" in a row already"
		case !m.Cfg.Auto.Wake && t.Light == StAsleep:
			mode, why = config.AutoAsk, "the tab is asleep, and wake is off"
		case !m.Cfg.Auto.Wake && m.Cold(t, time.Now()):
			mode, why = config.AutoAsk, "the tab is cold, and wake is off"
		}
	}
	origin := j.What()
	for i := len(t.Messages) - 1; i >= 0; i-- {
		if msg := t.Messages[i]; msg.AutoKey == j.Key && msg.AutoEvent == j.Event {
			t.DropMessage(i)
		}
	}
	if where := autoWhere(t, j.Entry); where != "" {
		text = where + "\n\n" + text
	}
	t.Messages = append(t.Messages, state.Message{Text: text, Auto: mode == config.AutoSend, Origin: origin,
		AutoKey: j.Key, AutoEvent: j.Event, AutoHead: j.Entry.PR.Head})
	ref := m.Ref(t)
	if mode == config.AutoSend {
		t.AutoNote, t.AutoSentAt = "nerdr will "+origin+" when the agent is idle", time.Time{}
		m.Tell(HistoryMsg{Cat: HistoryInfo, Text: t.Name + ": " + t.AutoNote + " (auto " + j.Event + ")"})
	} else {
		text := t.Name + ": a message to " + origin + " waits in F3 for you to read and send"
		if why != "" {
			text += " — not sent by itself, as " + why
		}
		t.AutoNote, t.AutoSentAt = "", time.Time{}
		m.Tell(AutoMsg{T: ref, Text: text})
	}
	m.SaveSession()
	m.Tell(PanesMsg{})
}

// AutoHold is why msg, an [auto] message about to be sent by itself, is
// not to be after all, "" when it is: what was set when it was written may
// have changed since (the config, the repository's .nerdr.toml or its
// being allowed), and max_tries counts what was sent. A message the user
// ticked in F3 is theirs (it has no AutoKey), and not asked about.
func (m *Core) AutoHold(t *Tab, msg state.Message) string {
	if msg.AutoKey == "" {
		return ""
	}
	if !m.Cfg.Auto.Wake {
		switch {
		case t.Light == StAsleep:
			return "the tab is asleep, and wake is off"
		case m.Cold(t, time.Now()):
			return "the tab is cold, and wake is off"
		}
	}
	repo := msg.AutoKey
	if i := strings.LastIndex(repo, "#"); i >= 0 {
		repo = repo[:i]
	}
	if mode, why := m.AutoMode(repo, msg.AutoEvent); mode != config.AutoSend {
		if why == "" {
			why = msg.AutoEvent + " is no longer auto"
		}
		return why
	}
	if tr := m.PRWatch.Auto[msg.AutoKey][msg.AutoEvent]; tr.Tries >= m.Cfg.Auto.MaxTries {
		return "it was sent by itself " + Plural(tr.Tries, "time") + " in a row already"
	}
	return ""
}

// HoldAutoAsleep keeps for the user the [auto] messages waiting in t, a
// tab asleep, when wake is off: else they would wake it.
func (m *Core) HoldAutoAsleep(t *Tab) {
	if m.Cfg.Auto.Wake || t.Light != StAsleep {
		return
	}
	for i, msg := range t.Messages {
		if msg.Auto && msg.AutoKey != "" {
			m.autoHeld(t, i, "the tab is asleep, and wake is off")
		}
	}
}

// autoHeld keeps message i of t's, an [auto] message, for the user, as
// AutoHold says why.
func (m *Core) autoHeld(t *Tab, i int, why string) {
	msg := &t.Messages[i]
	msg.Auto = false
	t.AutoNote, t.AutoSentAt = "", time.Time{}
	m.Tell(AutoMsg{T: m.Ref(t), Text: t.Name + ": a message to " + msg.Origin + " waits in F3 for you to read and send — not sent by itself, as " + why})
	m.SaveSession()
	m.Tell(PanesMsg{})
}

// RearmAutoJobs has the readings that were under way when nerdr stopped
// acted on again at the next look: they never answered, and what they
// were for is kept as done.
func (m *Core) RearmAutoJobs() {
	for _, tries := range m.PRWatch.Auto {
		for ev, tr := range tries {
			if tr.Job == 0 {
				continue
			}
			tr.Job, tr.Want, tr.Done = 0, tr.Done, ""
			tries[ev] = tr
		}
	}
}

// autoWhere says which of t's worktrees the pull request of e is in, for
// a tab with more than one: the message names only its number.
func autoWhere(t *Tab, e PREntry) string {
	if len(t.Worktrees) < 2 {
		return ""
	}
	for _, w := range t.Worktrees {
		if filepath.Clean(w.Repo) == filepath.Clean(e.Repo.Path) {
			return fmt.Sprintf("This is about the worktree %s (repository %s, branch %s).", w.Path, e.Repo.Name, e.Branch())
		}
	}
	return ""
}

// autoCount counts msg, an [auto] message being sent by itself, towards
// max_tries.
func (m *Core) autoCount(msg state.Message) {
	tries := m.autoTries(msg.AutoKey)
	tr := tries[msg.AutoEvent]
	tr.Tries++
	tries[msg.AutoEvent] = tr
}

// WithdrawStaleAuto takes back nerdr's [auto] messages waiting in the tabs
// whose trouble a look has found gone: the pull request no longer open,
// its checks no longer failing on the head the message was for, or no
// longer conflicting there. known is the look's pull requests by key;
// kept says which repositories did not answer, whose are left alone. A
// message the user ticked or edited is theirs, and stays.
func (m *Core) WithdrawStaleAuto(known map[string]state.PRSeen, kept func(repo string) bool) {
	for _, t := range m.Tabs {
		changed := false
		for i := len(t.Messages) - 1; i >= 0; i-- {
			msg := t.Messages[i]
			if msg.AutoKey == "" {
				continue
			}
			s, open := known[msg.AutoKey]
			repo := msg.AutoKey
			if k := strings.LastIndex(repo, "#"); k >= 0 {
				repo = repo[:k]
			}
			if kept(repo) {
				continue
			}
			why := ""
			switch {
			case !open:
				why = "the pull request is no longer open"
			case msg.AutoEvent == config.EventCIFailed && !failingOn(s, msg.AutoHead):
				why = "its checks no longer fail there"
			case msg.AutoEvent == config.EventConflicts && (s.Mergeable || s.Head != msg.AutoHead):
				why = "it no longer conflicts there"
			}
			if why == "" {
				continue
			}
			t.DropMessage(i)
			changed = true
			m.Tell(HistoryMsg{Cat: HistoryInfo, Text: t.Name + ": took back the message to " + msg.Origin + ", as " + why})
		}
		if changed {
			if t.AutoCount() == 0 && t.AutoSentAt.IsZero() {
				t.AutoNote = ""
			}
			m.SaveSession()
			m.Tell(PanesMsg{})
		}
	}
}

// failingOn reports whether a check of s fails on head.
func failingOn(s state.PRSeen, head string) bool {
	for k := range s.Failing {
		if strings.HasPrefix(k, head+"/") {
			return true
		}
	}
	return false
}

// autoSent tells that msg, an [auto] message, has just been sent to t's
// agent; the tab's panel says so until the agent is sent something else.
func (m *Core) autoSent(t *Tab, msg state.Message) {
	t.AutoNote = "nerdr sent the agent, by itself, a message to " + msg.Origin + " at " + time.Now().Format("15:04")
	t.AutoSentAt = t.LastAt
	m.Tell(AutoMsg{T: m.Ref(t), Text: t.Name + ": nerdr sent the agent a message to " + msg.Origin})
}

// AutoLine is what t's panel says of an [auto] message: one waiting to
// be sent by itself, or the last thing the agent was sent; "" otherwise.
func (t *Tab) AutoLine() string {
	if t.AutoNote == "" {
		return ""
	}
	if !t.AutoSentAt.IsZero() && !t.LastAt.Equal(t.AutoSentAt) {
		return "" // sent something since
	}
	if t.AutoSentAt.IsZero() && t.AutoCount() == 0 {
		return "" // the message waiting has gone
	}
	return t.AutoNote
}

func joinNames(who []string) string {
	switch len(who) {
	case 1:
		return who[0]
	case 2:
		return who[0] + " and " + who[1]
	}
	return fmt.Sprintf("%s and %d others", who[0], len(who)-1)
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}
