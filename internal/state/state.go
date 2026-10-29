// Package state saves which tabs are open, so nerdr can be started again
// after a restart and carry on where it was: the same tabs, in the same
// worktrees, each agent resuming its conversation.
package state

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/denied"
	"github.com/nerdr-dev/nerdr/internal/spinoff"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// Tab is what is remembered about one tab.
type Tab struct {
	ID    string      `json:"id"`
	Name  string      `json:"name"`
	Dir   string      `json:"dir"`
	Agent agents.Kind `json:"agent"`
	// Model is the model the agent was started with in place of the
	// config's, by the agent that started it (`nerdr agent --model`).
	Model     string `json:"model,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	// ResumeArgv is the command the agent's herdr integration said
	// reopens its conversation; see agents.ResumeReported.
	ResumeArgv []string `json:"resume_argv,omitempty"`
	// Sessions is the conversation each other agent the tab has run was
	// in, so switching back to one continues where it left off.
	// An empty value records a previous visit by an agent such as Codex
	// that resumes the directory's last conversation without an explicit id.
	Sessions  map[agents.Kind]string `json:"sessions,omitempty"`
	Worktrees []workspace.Worktree   `json:"worktrees"`
	// Group is the name given to the group of a tab spanning several
	// repositories; empty for the default, multi.
	Group string `json:"group,omitempty"`
	// Messages are what the user has written for the agent, in order. An
	// auto one is typed in the next time the agent is idle; the others
	// wait until the user puts them in.
	Messages []Message `json:"messages,omitempty"`
	// Inactive are messages for an agent of the tab whose pane has since
	// been closed, each marked with its number (Message.Gone). They are
	// never typed in: they wait for the user to give them to another
	// agent, or delete them.
	Inactive []Message `json:"inactive,omitempty"`
	// Queue is how sessions from before Messages kept them, all auto. It
	// is only read: TakeQueue moves it into Messages.
	Queue []string `json:"queue,omitempty"`
	// Goal is the first task the tab's agent was given, Last the last
	// thing the user sent it, and Note what the user wrote to themselves
	// about the tab; shown above the agent.
	Goal string `json:"goal,omitempty"`
	Last string `json:"last,omitempty"`
	Note string `json:"note,omitempty"`
	// Prompts are the latest things the user sent the agent, the oldest
	// first, Last among them: for the panel, when Last is only "yes", and
	// the list of them (a h).
	Prompts []Prompt `json:"prompts,omitempty"`
	// Recap is Claude's last summary of where the tab's work stands, from
	// its conversation log, and RecapAt when it wrote it; shown above the
	// agent in Last's place while nothing has been sent since.
	Recap   string    `json:"recap,omitempty"`
	RecapAt time.Time `json:"recap_at,omitempty"`
	// Status is where the tab's work stands, in the agent's own words
	// (`nerdr status`), StatusBy the number of the agent of the tab that
	// set it and StatusAt when; shown above the agent.
	Status   string    `json:"status,omitempty"`
	StatusBy int       `json:"status_by,omitempty"`
	StatusAt time.Time `json:"status_at,omitempty"`
	// LastActive is when the agent last worked or the user last looked at
	// the tab, for marking tabs gone cold.
	LastActive time.Time `json:"last_active,omitempty"`
	// Spinoff is the parts of its work the agent has asked to have split
	// off into new tabs, while the user has not been through them all.
	Spinoff *spinoff.Pending `json:"spinoff,omitempty"`
	// SpinoffReport is what became of those parts once the user has been
	// through them all, kept until the agents of the tabs made have taken
	// their prompts up, when the agent that sent them is told.
	SpinoffReport []spinoff.Outcome `json:"spinoff_report,omitempty"`
	// Reviewed is the worktree as its last code review saw it, so the same
	// work is not reviewed again; ReviewBrief how that review ended, for
	// the panel above the agent.
	Reviewed    string `json:"reviewed,omitempty"`
	ReviewBrief string `json:"review_brief,omitempty"`
	// FindingsSent is set while the agent works on the findings of a
	// review that has ended; the brief goes when its turn on them does.
	FindingsSent bool `json:"findings_sent,omitempty"`
	// Agents are the agents working beside the tab's own in its worktree,
	// each in a pane of its own, to be resumed with it.
	Agents []Tab `json:"agents,omitempty"`
	// Num is the agent's number among the tab's agents, which names it in
	// the row of panes and to the others: `nerdr send 2` reaches number 2.
	Num int `json:"num,omitempty"`
	// Sandboxed is set once any agent in the tab's worktree has run in a
	// sandbox, so git commit and push know not to run hooks on its files.
	Sandboxed bool `json:"sandboxed,omitempty"`
}

// Prompt is one thing the user sent a tab's agent, and when.
type Prompt struct {
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// Message is one message for a tab's agent.
type Message struct {
	Text string `json:"text"`
	// Auto is set when it is to be typed in by itself, the next time the
	// agent is idle.
	Auto bool `json:"auto,omitempty"`
	// From is the number of the agent of the tab that sent it, 0 for one
	// that came from elsewhere: `nerdr send --wait` takes the answer it
	// waits for out of the list by it.
	From int `json:"from,omitempty"`
	// Sandboxed is set on a message an agent in a sandbox wrote. An agent
	// started outside one does not have it typed in: it waits for the user.
	Sandboxed bool `json:"sandboxed,omitempty"`
	// Gone is the number of the agent it was for, on one of a tab's
	// Inactive: that agent's pane was closed before it was sent.
	Gone int `json:"gone,omitempty"`
}

// TakeQueue moves a queue kept the old way into Messages, as auto ones
// after any already there.
func (t *Tab) TakeQueue() {
	for _, q := range t.Queue {
		t.Messages = append(t.Messages, Message{Text: q, Auto: true})
	}
	t.Queue = nil
}

// Session is everything saved for one start folder.
type Session struct {
	Root   string `json:"root"`
	Active int    `json:"active"`
	Tabs   []Tab  `json:"tabs"`
	// Split is the id of the tab shown beside the active one, and
	// SplitLeft whether it is on the left.
	Split     string `json:"split,omitempty"`
	SplitLeft bool   `json:"split_left,omitempty"`
	// PRs is what nerdr last knew of your pull requests, so what happened
	// to them while it was not running is told at the next start.
	PRs *PRWatch `json:"prs,omitempty"`
	// Issues is the same for the issues assigned to you.
	Issues *IssueWatch `json:"issues,omitempty"`
	// GroupNames is the group name given to each folder of worktrees in
	// several repositories, by folder: a tab opened on it again goes in
	// the same group.
	GroupNames map[string]string `json:"group_names,omitempty"`
	// SandboxedDirs are the worktree folders where an agent has run in a
	// sandbox, kept when a tab is closed so reopening the worktree still
	// skips git hooks on its files.
	SandboxedDirs map[string]bool `json:"sandboxed_dirs,omitempty"`
	// SideWidth is the columns the sidebar was dragged to, its frames
	// included; 0 leaves it as wide as its names need.
	SideWidth int `json:"side_width,omitempty"`
}

// PRWatch is how your pull requests stood at the last look, and the news
// of them not seen yet.
type PRWatch struct {
	Known map[string]PRSeen `json:"known,omitempty"` // by repository path and number
	// Repos are the repositories that have answered once: only after that
	// is a change news.
	Repos []string `json:"repos,omitempty"`
	News  []PRNews `json:"news,omitempty"`
}

// PRSeen is how one pull request stood.
type PRSeen struct {
	Repo string `json:"repo"`
	// Failing is the checks failing, as "<head commit>/<name>": the same
	// check failing again after a push is news.
	Failing map[string]bool `json:"failing,omitempty"`
	// Reviews is each reviewer's latest review, by reviewer.
	Reviews map[string]ReviewSeen `json:"reviews,omitempty"`
}

// ReviewSeen is a review as far as telling of it goes.
type ReviewSeen struct {
	State string    `json:"state"`
	At    time.Time `json:"at"`
}

// PRNews is one thing that happened to a pull request.
type PRNews struct {
	Key    string `json:"key"`  // as in PRWatch.Known
	Repo   string `json:"repo"` // the repository's name
	Number int    `json:"number"`
	What   string `json:"what"`
	// Tab is the name of the tab working on its branch, if one was.
	Tab string `json:"tab,omitempty"`
	// Checks is the failing checks it tells of, as in PRSeen.Failing: once
	// none of them fails, it is no longer news.
	Checks []string `json:"checks,omitempty"`
}

// IssueWatch is the open issues assigned to you at the last look, and
// those newly assigned not seen yet.
type IssueWatch struct {
	// Known is every issue assigned to you, as "owner/name#number": one
	// not among them at the next look is news.
	Known []string    `json:"known,omitempty"`
	News  []IssueNews `json:"news,omitempty"`
}

// IssueNews is an issue newly assigned to you.
type IssueNews struct {
	Key    string `json:"key"`  // as in IssueWatch.Known
	Repo   string `json:"repo"` // the repository's name
	Number int    `json:"number"`
	Title  string `json:"title"`
}

// Dir is where session files are kept.
func Dir() string {
	d := os.Getenv("XDG_STATE_HOME")
	if d == "" {
		home, _ := os.UserHomeDir()
		d = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(d, "nerdr", "sessions")
}

// Path is the session file for a start folder. Each folder has its own, so
// nerdr started in two places keeps two sets of tabs.
func Path(root string) string {
	sum := sha1.Sum([]byte(root))
	name := filepath.Base(root) + "-" + hex.EncodeToString(sum[:4]) + ".json"
	return filepath.Join(Dir(), name)
}

// Load reads the session for root. A missing file gives an empty session.
func Load(root string) (Session, error) {
	s := Session{Root: root}
	data, err := os.ReadFile(Path(root))
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		// The next save would write over the file and lose every tab and
		// conversation in it: keep it beside, for the user to look at.
		kept, kerr := preserve(Path(root), data)
		if kerr != nil {
			return Session{Root: root}, fmt.Errorf("%w (and could not keep a copy: %v)", err, kerr)
		}
		return Session{Root: root}, fmt.Errorf("%w; the file is kept as %s", err, kept)
	}
	return s, nil
}

// preserve writes data, a session file that could not be read, beside it
// under a name of its own.
func preserve(p string, data []byte) (string, error) {
	kept := strings.TrimSuffix(p, ".json") + ".broken-" + time.Now().Format("20060102-150405") + ".json"
	if err := denied.Err(os.WriteFile(kept, data, 0o600)); err != nil {
		return "", err
	}
	return kept, nil
}

// Snapshots of each session are kept as they were before a save, at most
// one every snapshotEvery and the newest keepSnapshots of them, so tabs
// closed by mistake — or a session file broken some other way — can be got
// back by copying one over the session file while nerdr is not running.
const (
	snapshotEvery = 15 * time.Minute
	keepSnapshots = 20
)

// SnapshotDir is where the snapshots are kept.
func SnapshotDir() string { return filepath.Join(Dir(), "snapshots") }

// snapshot copies the session file at p, as it is before being replaced,
// if the newest snapshot of it is old enough.
func snapshot(p string) error {
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	dir := SnapshotDir()
	base := strings.TrimSuffix(filepath.Base(p), ".json")
	old, _ := filepath.Glob(filepath.Join(dir, base+"@*.json"))
	sort.Strings(old) // the names sort by time
	if n := len(old); n > 0 {
		if st, err := os.Stat(old[n-1]); err == nil && time.Since(st.ModTime()) < snapshotEvery {
			return nil
		}
		if last, err := os.ReadFile(old[n-1]); err == nil && string(last) == string(data) {
			return nil
		}
	}
	if err := denied.Err(os.MkdirAll(dir, 0o700)); err != nil {
		return err
	}
	name := filepath.Join(dir, base+"@"+time.Now().Format("20060102-150405")+".json")
	if err := denied.Err(os.WriteFile(name, data, 0o600)); err != nil {
		return err
	}
	old = append(old, name)
	for len(old) > keepSnapshots {
		_ = os.Remove(old[0])
		old = old[1:]
	}
	return nil
}

// Save writes the session atomically, so a crash mid-write cannot leave a
// file that loses every tab.
func Save(s Session) error {
	if err := denied.Err(os.MkdirAll(Dir(), 0o755)); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	p := Path(s.Root)
	// A snapshot that cannot be made must not stop the session being
	// saved: that is what matters now.
	_ = snapshot(p)
	// The session holds the agents' session ids and what was said to
	// them: for this user only, as the snapshots are. A tmp left by a
	// crash is removed first, since WriteFile keeps an existing file's mode.
	tmp := p + ".tmp"
	_ = os.Remove(tmp)
	// Not synced to disk first: that costs ten times the save, on the
	// screen's time, at every tab switched to. A crash that loses it has
	// the snapshots to go back to.
	if err := denied.Err(os.WriteFile(tmp, data, 0o600)); err != nil {
		return err
	}
	return denied.Err(os.Rename(tmp, p))
}

// ErrLocked is returned by Lock when another nerdr has the folder's session.
var ErrLocked = errors.New("locked")

// Lock claims root's session for this process until release is called or
// the process ends. Two nerdrs in one folder would resume the same
// conversations in two places and overwrite each other's session file.
func Lock(root string) (release func(), err error) {
	if err := denied.Err(os.MkdirAll(Dir(), 0o755)); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(Path(root)+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if denied.Err(err) != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("nerdr is already running in %s: %w", root, ErrLocked)
		}
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}
