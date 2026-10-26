// Package hooks lets herdr's agent integrations report to nerdr.
//
// herdr's official integrations (`herdr integration install claude`,
// opencode, copilot, …) are hooks and plugins inside the agents. They say
// exactly which conversation the agent is in, and some (opencode among
// them) also say whether it is working, blocked or idle — better than
// reading it off the screen. They talk to whatever socket HERDR_SOCKET_PATH
// names, in herdr's protocol: one JSON request per line, answered by one
// JSON line.
//
// nerdr listens on a socket of its own and gives its agents
// HERDR_SOCKET_PATH pointing at it, with HERDR_PANE_ID a secret standing
// for the tab (see app's hookKey). The
// reports come to nerdr, not to herdr (whose pane is nerdr's as a whole),
// and are passed on as Reports. The integrations that report through the
// herdr CLI instead find nerdr as HERDR_BIN_PATH: see Command.
package hooks

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/clean"
	"github.com/nerdr-dev/nerdr/internal/codereview"
	"github.com/nerdr-dev/nerdr/internal/denied"
	"github.com/nerdr-dev/nerdr/internal/handoff"
	"github.com/nerdr-dev/nerdr/internal/spinoff"
	"github.com/nerdr-dev/nerdr/internal/usage"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// Report is one report from an integration.
type Report struct {
	Method string // pane.report_agent, pane.report_agent_session, pane.release_agent
	Pane   string // HERDR_PANE_ID, which nerdr sets to the tab's secret
	Source string // e.g. herdr:opencode
	Agent  string // e.g. opencode
	// State is idle, working, blocked or unknown; empty when the report is
	// only about the session.
	State       string
	Message     string
	SessionID   string
	SessionPath string
	// SessionSource is why the session started, on a report made as it
	// did: startup, resume, clear, compact, … (herdr's
	// session_start_source).
	SessionSource string
	// ResumeArgv is the command the agent said reopens its conversation,
	// herdr's resume_argv: `my-agent --resume ID`.
	ResumeArgv []string
	Seq        int64
	// Background says a working agent's turn is over, but it waits on
	// subagents it started in the background; see subagents.go.
	Background bool
	// Spinoff is the parts an agent hands to new tabs; see Spinoff.
	Spinoff []spinoff.Part
	// Handoff is what an agent wrote for the one taking over its tab; see
	// Handoff.
	Handoff string
	// Review is a reviewer's conclusion; see Review.
	Review *codereview.Result
	// Focus is what an agent asking for a review of its work wants looked
	// at most; see RequestReview.
	Focus string
	// To and Text are a message for another agent of the tab, by its
	// number (0 asks only which there are); see Send. Reply takes nerdr's
	// answer for the agent that sent it.
	To    int
	Text  string
	Reply chan Answer
	// Kind is the agent a NewAgent report asks for, "" for the sender's
	// own kind; Text is its task.
	Kind string
	// Model is the model a NewAgent report asks for, "" for the config's.
	Model string
	// Limits is what a Usage report says.
	Limits *usage.Report
	// What is what a Query asks for; Name a terminal's ("terminal 2"),
	// Lines how many lines of a pane to read.
	What  string
	Name  string
	Lines int
	// Restart is set on a Terminal report for the terminal Name, not a
	// new one; Text is the command to run in it, "" for the one before.
	Restart bool
	// Clear is set when `nerdr status --clear` clears the tab's status.
	Clear bool
	// Branch and Repo are a NewTab's branch and repository ("" for the
	// asking tab's).
	Branch string
	Repo   string
	// Run, Values, Dir and Offset are of an agent's run of a plugin: which
	// run, the parameters it is given, the folder it runs in and how much
	// of its output has been read; see PluginRun.
	Run    string
	Values map[string]any
	Dir    string
	Offset int
	// Peer is the process the report came from. nerdr sets Unvouched
	// when it is not the agent of the tab the report names, nor runs
	// under it (see app's vouch).
	Peer      Peer
	Unvouched bool
}

// Answer is nerdr's answer to a report that waits for one: Err when it
// was refused, else Text to tell the agent.
type Answer struct {
	Err, Text string
}

// Methods nerdr takes. Anything else is answered with an error, so an agent
// trying to drive herdr through nerdr's socket learns it cannot.
const (
	ReportAgent        = "pane.report_agent"
	ReportAgentSession = "pane.report_agent_session"
	ReleaseAgent       = "pane.release_agent"
	ReportMetadata     = "pane.report_metadata"
	// Spinoff is nerdr's own: `nerdr spinoff` sends the parts of its
	// agent's work the user asked to have split off into new tabs.
	Spinoff = "nerdr.spinoff"
	// Handoff is nerdr's own too: `nerdr handoff` sends what an agent
	// wrote for the agent its tab is switched to.
	Handoff = "nerdr.handoff"
	// Review is nerdr's own as well: `nerdr post-review` sends a reviewer
	// agent's conclusion.
	Review = "nerdr.review"
	// RequestReview is nerdr's own: `nerdr request-review` says the
	// agent's work is ready for a review, with what to look at most, if
	// anything.
	RequestReview = "nerdr.request_review"
	// Message is nerdr's own: `nerdr send` sends a message to another
	// agent in the same tab, and waits for nerdr's answer.
	Message = "nerdr.send"
	// NewAgent is nerdr's own: `nerdr agent` starts another agent in the
	// same tab with a task, and waits for nerdr's answer.
	NewAgent = "nerdr.agent"
	// Usage is nerdr's own: `nerdr hook statusline` reports what claude
	// tells its status line of the account's rate limits and the agent's
	// context window.
	Usage = "nerdr.usage"
	// Query is nerdr's own: `nerdr agents`, `read`, `wait`, `tabs` and
	// `search` ask what nerdr knows — the tab's agents, what a pane shows,
	// every tab, where conversations are searched — and wait for it.
	Query = "nerdr.query"
	// Terminal is nerdr's own: `nerdr terminal` runs a command in a new
	// terminal pane of the tab, where the user sees it, or with --restart
	// stops what runs in one and runs it, or another command, again.
	Terminal = "nerdr.terminal"
	// Close is nerdr's own: `nerdr close` closes another agent's pane or a
	// terminal of the tab, when the user asked for it.
	Close = "nerdr.close"
	// NewTab is nerdr's own: `nerdr tab new` makes a tab on a new branch,
	// with an agent given a task.
	NewTab = "nerdr.tab"
	// Status is nerdr's own: `nerdr status` sets the line saying where
	// the tab's work stands, shown above the agent and in nerdr tabs, or
	// clears it, or asks what it says.
	Status = "nerdr.status"
	// Plugin is nerdr's own: `nerdr plugin list`, `run` and `stop` run
	// the user's plugins through nerdr, which runs them as the user,
	// outside every sandbox: see the Plugin* below.
	Plugin = "nerdr.plugin"
)

// What a Plugin report asks.
const (
	// PluginCheck asks whether the user allowed the plugin whose key is
	// Text, in folder Name.
	PluginCheck = "check"
	// PluginRun asks nerdr to run the plugin called Name with Values, in
	// Dir: it answers "started RUN" once it has, or why it has not — "not
	// allowed", "declined", "busy" or "unanswered" — perhaps after asking
	// the user.
	PluginRun = "run"
	// PluginRead asks for what run Run printed from byte Offset on, and
	// whether it has ended, waiting a little for more: the answer is a
	// RunOutput as JSON.
	PluginRead = "read"
	// PluginStop asks nerdr to stop the agent's run Run, or its runs of the
	// plugin called Name; it answers what it stopped.
	PluginStop = "stop"
)

// RunOutput is nerdr's answer to a PluginRead: what the run printed from
// the offset asked for to Offset, and its exit status once it has Ended.
// Lost is set when some of what was asked for is no longer kept.
type RunOutput struct {
	Text   string `json:"text"`
	Offset int    `json:"offset"`
	Ended  bool   `json:"ended"`
	Exit   int    `json:"exit"`
	Lost   bool   `json:"lost,omitempty"`
}

// PluginAskWait is how long a PluginRun waits for nerdr's answer: the
// user may be asked to say yes to the run first. It is short of the two
// minutes an agent's command may take.
const PluginAskWait = 100 * time.Second

// MaxPluginOutput is the most of a run's output one PluginRead answers
// with, in bytes.
const MaxPluginOutput = 32 << 10

// MaxStatus is the longest status line an agent can set.
const MaxStatus = 300

// What a Query asks for.
const (
	QueryAgents = "agents" // the tab's agents and their state, as JSON
	QueryRead   = "read"   // what agent To, or terminal Name, shows
	QueryTabs   = "tabs"   // every tab, as JSON
	QueryRoots  = "roots"  // the folders whose conversations are searched, one a line
	// QueryReply takes the message agent To sent the one asking, if one
	// waits, out of its list: `nerdr send --wait` asks it until one is
	// there.
	QueryReply = "reply"
)

// MaxText is the longest message one agent sends another, in bytes.
const MaxText = 32 << 10

// answerWait is how long a report that waits for nerdr's answer waits.
// nerdr answers at once when it is not busy; what an agent asked is done
// when nerdr gets to it, whether or not the answer was waited for.
const answerWait = 10 * time.Second

type request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type params struct {
	PaneID           string             `json:"pane_id"`
	Source           string             `json:"source"`
	Agent            string             `json:"agent"`
	State            string             `json:"state"`
	Message          string             `json:"message"`
	AgentSessionID   string             `json:"agent_session_id"`
	AgentSessionPath string             `json:"agent_session_path"`
	SessionSource    string             `json:"session_start_source"`
	ResumeArgv       []string           `json:"resume_argv"`
	Seq              *int64             `json:"seq"`
	Background       bool               `json:"background"`
	Parts            []spinoff.Part     `json:"parts"`
	Summary          string             `json:"summary"`
	Review           *codereview.Result `json:"review"`
	Focus            string             `json:"focus"`
	To               int                `json:"to"`
	Text             string             `json:"text"`
	Kind             string             `json:"kind"`
	Model            string             `json:"model"`
	What             string             `json:"what"`
	Name             string             `json:"name"`
	Lines            int                `json:"lines"`
	Restart          bool               `json:"restart"`
	Clear            bool               `json:"clear"`
	Branch           string             `json:"branch"`
	Repo             string             `json:"repo"`
	Limits           *usage.Report      `json:"limits"`
	Run              string             `json:"run"`
	Values           map[string]any     `json:"values"`
	Dir              string             `json:"dir"`
	Offset           int                `json:"offset"`
}

// Server is nerdr's end of the socket.
type Server struct {
	ln   net.Listener
	lock *os.File // held while the socket is this nerdr's
	path string
	send func(Report)

	wg   sync.WaitGroup
	mu   sync.Mutex
	conn map[net.Conn]bool
}

// SocketPath is the socket nerdr listens on: nerdr.sock in
// $XDG_STATE_HOME/nerdr, or ~/.local/state/nerdr without it. It is the
// same whatever folder nerdr starts in and whatever the temporary folder
// is, so a sandbox around an agent can be told once to allow connecting
// to it. One nerdr has it at a time: see Listen.
func SocketPath() string {
	d := os.Getenv("XDG_STATE_HOME")
	if d == "" {
		home, _ := os.UserHomeDir()
		d = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(d, "nerdr", "nerdr.sock")
}

// ForRoot is socket path made the folder root's own, for a nerdr that
// finds path taken by another started elsewhere: beside it, with a name
// of its own.
func ForRoot(path, root string) string {
	sum := sha1.Sum([]byte(root))
	return filepath.Join(filepath.Dir(path), "nerdr-"+hex.EncodeToString(sum[:4])+".sock")
}

// ErrTaken is returned by Listen when another nerdr has the socket.
var ErrTaken = errors.New("another nerdr has the socket")

// Listen opens the socket at path and passes every report to send, from
// the server's own goroutines. The socket is claimed with a lock beside
// it, held until Close, so a second nerdr gets ErrTaken, naming the folder
// the first started in, rather than taking the socket from it. A stale
// socket left by a nerdr that died is removed first; the lock makes sure
// no live one owns it.
func Listen(path string, send func(Report)) (*Server, error) {
	if err := denied.Err(os.MkdirAll(filepath.Dir(path), 0o700)); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if denied.Err(err) != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		owner, _ := io.ReadAll(lock)
		_ = lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			if o := strings.TrimSpace(string(owner)); o != "" {
				return nil, fmt.Errorf("%w (%s, started in %s)", ErrTaken, path, o)
			}
			return nil, fmt.Errorf("%w (%s)", ErrTaken, path)
		}
		return nil, err
	}
	if cwd, err := os.Getwd(); err == nil {
		_ = lock.Truncate(0)
		_, _ = lock.WriteAt([]byte(cwd+"\n"), 0)
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if denied.Err(err) != nil {
		_ = lock.Close()
		return nil, err
	}
	// Only the user may connect: a report can change which conversation a
	// tab resumes.
	if err := denied.Err(os.Chmod(path, 0o600)); err != nil {
		_ = ln.Close()
		_ = lock.Close()
		return nil, err
	}
	s := &Server{ln: ln, lock: lock, path: path, send: send, conn: map[net.Conn]bool{}}
	s.wg.Add(1)
	go s.accept()
	return s, nil
}

// Path is the socket's path, for NERDR_SOCKET and HERDR_SOCKET_PATH.
func (s *Server) Path() string { return s.path }

// Close stops listening, drops open connections and removes the socket.
func (s *Server) Close() {
	_ = s.ln.Close()
	s.mu.Lock()
	for c := range s.conn {
		_ = c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
	_ = os.Remove(s.path)
	_ = s.lock.Close()
}

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conn[c] = true
		s.mu.Unlock()
		s.wg.Add(1)
		go s.serve(c)
	}
}

func (s *Server) serve(c net.Conn) {
	defer s.wg.Done()
	defer func() {
		s.mu.Lock()
		delete(s.conn, c)
		s.mu.Unlock()
		_ = c.Close()
	}()
	cl := &client{s: s, peer: peerOf(c)}
	sc := bufio.NewScanner(c)
	// A spinoff carries the prompts for the new agents, which may be long.
	sc.Buffer(make([]byte, 64*1024), maxRequest)
	for {
		// A hook sends its request at once; one that holds a connection
		// open doing nothing is dropped.
		_ = c.SetReadDeadline(time.Now().Add(time.Minute))
		if !sc.Scan() {
			return
		}
		resp := s.handle(cl, sc.Bytes())
		_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := c.Write(append(resp, '\n')); err != nil {
			return
		}
	}
}

// maxRequest is the longest request line taken.
const maxRequest = 8 << 20

// maxMessage is the longest message of a report kept, in bytes: it is
// shown as what the agent last said.
const maxMessage = 4 << 10

// handle answers one request line from cl.
func (s *Server) handle(cl *client, line []byte) []byte {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		return errorResponse("", "invalid_request", "invalid JSON")
	}
	if req.Method == "ping" {
		return result(req.ID, map[string]any{"type": "pong", "server": "nerdr"})
	}
	h, ok := handlers[req.Method]
	if !ok {
		return errorResponse(req.ID, "method_not_found",
			"nerdr takes agent reports only; "+req.Method+" is herdr's")
	}
	var p params
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return errorResponse(req.ID, "invalid_params", err.Error())
		}
	}
	if p.PaneID == "" {
		return errorResponse(req.ID, "invalid_params", "pane_id is required")
	}
	// What the handler is given comes from the agent, or from whatever it
	// ran, so each checks it as `nerdr spinoff` and `nerdr handoff` check
	// it: an agent can write to this socket without them.
	return h(cl, req.ID, p)
}

// client is one connection's end of the server: what it reports goes to
// nerdr with the process it came from.
type client struct {
	s    *Server
	peer Peer
}

func (c *client) send(r Report) {
	r.Peer = c.peer
	c.s.send(r)
}

// handler checks a request's params and passes the report they make on,
// returning the response.
type handler func(c *client, id string, p params) []byte

// handlers are the methods nerdr takes, each with its handler: one method
// is taken exactly when it has one. Anything else is herdr's.
var handlers map[string]handler

func init() {
	handlers = map[string]handler{
		ReportAgent:        handleReport(ReportAgent),
		ReportAgentSession: handleReport(ReportAgentSession),
		ReleaseAgent:       handleReport(ReleaseAgent),
		ReportMetadata:     handleReport(ReportMetadata),
		Spinoff:            handleSpinoff,
		Handoff:            handleHandoff,
		Review:             handleReview,
		RequestReview:      handleRequestReview,
		Message:            handleMessage,
		NewAgent:           handleNewAgent,
		Query:              handleQuery,
		Terminal:           handleTerminal,
		Close:              handleClose,
		NewTab:             handleNewTab,
		Status:             handleStatus,
		Plugin:             handlePlugin,
		Usage:              handleUsage,
	}
}

func okResponse(id string) []byte { return result(id, map[string]any{"type": "ok"}) }

func invalid(id, msg string) []byte { return errorResponse(id, "invalid_params", msg) }

func handleSpinoff(c *client, id string, p params) []byte {
	if err := spinoff.Check(p.Parts, workspace.ValidBranch); err != nil {
		return invalid(id, err.Error())
	}
	for _, pt := range p.Parts {
		for _, c := range pt.Commits {
			if !spinoff.FullCommit(c) {
				return invalid(id, c+" is not a full commit id")
			}
		}
	}
	c.send(Report{Method: Spinoff, Pane: p.PaneID, Spinoff: p.Parts})
	return okResponse(id)
}

func handleHandoff(c *client, id string, p params) []byte {
	text := strings.TrimSpace(clean.Text(p.Summary))
	switch {
	case text == "":
		return invalid(id, "summary is empty")
	case len(text) > handoff.MaxSummary:
		return invalid(id, "summary is too long")
	}
	c.send(Report{Method: Handoff, Pane: p.PaneID, Handoff: text})
	return okResponse(id)
}

func handleReview(c *client, id string, p params) []byte {
	if p.Review == nil {
		return invalid(id, "review is missing")
	}
	if err := codereview.Check(p.Review); err != nil {
		return invalid(id, err.Error())
	}
	c.send(Report{Method: Review, Pane: p.PaneID, Review: p.Review})
	return okResponse(id)
}

func handleRequestReview(c *client, id string, p params) []byte {
	focus := strings.TrimSpace(clean.Text(p.Focus))
	kind, model := strings.ToLower(clean.Line(p.Kind)), clean.Line(p.Model)
	switch {
	case len(focus) > codereview.MaxFocus:
		return invalid(id, "focus is too long")
	case len(kind) > 40:
		return invalid(id, "kind is not an agent")
	case model != "" && kind == "":
		return invalid(id, "a model is the model of an agent: give the agent too")
	case badModel(model) != "":
		return invalid(id, badModel(model))
	}
	c.send(Report{Method: RequestReview, Pane: p.PaneID, Focus: focus, Kind: kind, Model: model})
	return okResponse(id)
}

func handleMessage(c *client, id string, p params) []byte {
	text := strings.TrimSpace(clean.Text(p.Text))
	switch {
	case p.To < 0:
		return invalid(id, "to is an agent's number, from 1")
	case p.To > 0 && text == "":
		return invalid(id, "the message is empty")
	case len(text) > MaxText:
		return invalid(id, "the message is too long")
	}
	return c.answered(id, Report{Method: Message, Pane: p.PaneID, To: p.To, Text: text})
}

// badModel is why model, which goes on an agent's command line, is not a
// model's name, or "".
func badModel(model string) string {
	if len(model) > 100 || strings.HasPrefix(model, "-") || strings.ContainsAny(model, " \t") {
		return "model is not a model's name"
	}
	return ""
}

func handleNewAgent(c *client, id string, p params) []byte {
	text, kind, model := strings.TrimSpace(clean.Text(p.Text)), clean.Line(p.Kind), clean.Line(p.Model)
	switch {
	case text == "":
		return invalid(id, "the task is empty")
	case len(text) > MaxText:
		return invalid(id, "the task is too long")
	case len(kind) > 40:
		return invalid(id, "kind is not an agent")
	case badModel(model) != "":
		return invalid(id, badModel(model))
	}
	return c.answered(id, Report{Method: NewAgent, Pane: p.PaneID, Kind: kind, Model: model, Text: text})
}

func handleQuery(c *client, id string, p params) []byte {
	switch p.What {
	case QueryAgents, QueryTabs, QueryRoots:
	case QueryReply:
		if p.To < 1 {
			return invalid(id, "reply takes an agent's number")
		}
	case QueryRead:
		if p.To < 0 || p.Lines < 0 || len(p.Name) > 40 {
			return invalid(id, "read takes an agent's number or a terminal's name")
		}
	default:
		return invalid(id, "what is agents, read, reply, tabs or roots")
	}
	return c.answered(id, Report{Method: Query, Pane: p.PaneID, What: p.What, To: p.To,
		Name: clean.Line(p.Name), Lines: min(p.Lines, 5000)})
}

func handleTerminal(c *client, id string, p params) []byte {
	cmd := strings.TrimSpace(clean.Text(p.Text))
	switch {
	case cmd == "" && !p.Restart:
		return invalid(id, "the command is empty")
	case len(cmd) > 4<<10:
		return invalid(id, "the command is too long")
	}
	return c.answered(id, Report{Method: Terminal, Pane: p.PaneID, Text: cmd, Name: cut(clean.Line(p.Name), 40), Restart: p.Restart})
}

func handleClose(c *client, id string, p params) []byte {
	if p.To < 0 || (p.To == 0) == (p.Name == "") || len(p.Name) > 40 {
		return invalid(id, "close takes an agent's number or a terminal's name")
	}
	return c.answered(id, Report{Method: Close, Pane: p.PaneID, To: p.To, Name: clean.Line(p.Name)})
}

func handleNewTab(c *client, id string, p params) []byte {
	text := strings.TrimSpace(clean.Text(p.Text))
	branch, kind, model, repo := clean.Line(p.Branch), clean.Line(p.Kind), clean.Line(p.Model), clean.Line(p.Repo)
	switch {
	case text == "":
		return invalid(id, "the task is empty")
	case len(text) > MaxText:
		return invalid(id, "the task is too long")
	case branch == "" || len(branch) > 200:
		return invalid(id, "a branch is needed")
	case len(kind) > 40 || len(repo) > 200:
		return invalid(id, "kind or repo is too long")
	case badModel(model) != "":
		return invalid(id, badModel(model))
	}
	return c.answered(id, Report{Method: NewTab, Pane: p.PaneID, Text: text, Branch: branch,
		Kind: kind, Model: model, Repo: repo})
}

func handleStatus(c *client, id string, p params) []byte {
	text := strings.Join(strings.Fields(clean.Text(p.Text)), " ")
	switch {
	case len(text) > MaxStatus:
		return invalid(id, fmt.Sprintf("the status is %d bytes; keep it under %d, a line saying where the work stands", len(text), MaxStatus))
	case p.Clear && text != "":
		return invalid(id, "clear takes no text")
	}
	return c.answered(id, Report{Method: Status, Pane: p.PaneID, Text: text, Clear: p.Clear})
}

func handlePlugin(c *client, id string, p params) []byte {
	r := Report{Method: Plugin, Pane: p.PaneID, What: p.What}
	switch p.What {
	case PluginCheck:
		switch {
		case len(p.Text) != 64 || strings.Trim(p.Text, "0123456789abcdef") != "":
			return invalid(id, "text is the plugin's key")
		case p.Name == "" || len(p.Name) > 4<<10:
			return invalid(id, "name is the plugin's folder")
		}
		r.Name, r.Text = clean.Line(p.Name), p.Text
	case PluginRun:
		if p.Name == "" || len(p.Name) > 200 || len(p.Dir) > 4<<10 {
			return invalid(id, "name is the plugin's, dir the folder to run it in")
		}
		r.Name, r.Values, r.Dir = p.Name, p.Values, p.Dir
		return c.answeredWithin(id, r, PluginAskWait)
	case PluginRead:
		if len(p.Run) != 16 || strings.Trim(p.Run, "0123456789abcdef") != "" || p.Offset < 0 {
			return invalid(id, "run is the run's id, offset how much of it was read")
		}
		r.Run, r.Offset = p.Run, p.Offset
	case PluginStop:
		if p.Run == "" && p.Name == "" || len(p.Run) > 64 || len(p.Name) > 200 {
			return invalid(id, "run is the run's id, or name the plugin's")
		}
		r.Run, r.Name = p.Run, clean.Line(p.Name)
	default:
		return invalid(id, "what is check, run, read or stop")
	}
	return c.answered(id, r)
}

func handleUsage(c *client, id string, p params) []byte {
	if p.Limits == nil {
		return invalid(id, "limits are missing")
	}
	c.send(Report{Method: Usage, Pane: p.PaneID, Limits: p.Limits.Checked()})
	return okResponse(id)
}

// handleReport is for herdr's own reports: the agent's state, its
// conversation, its release, and metadata, which is taken but not passed
// on (presentation only; nerdr draws its own).
func handleReport(method string) handler {
	return func(c *client, id string, p params) []byte {
		if method == ReportAgent {
			switch p.State {
			case "idle", "working", "blocked", "unknown":
			default:
				return invalid(id, "state must be idle, working, blocked or unknown")
			}
		}
		if p.AgentSessionID != "" && !agents.ValidSessionID(p.AgentSessionID) {
			// It goes on the agent's command line when the tab is restarted.
			return invalid(id, "agent_session_id is not a conversation id")
		}
		if p.ResumeArgv != nil {
			// The words go on the agent's command line when the tab is
			// resumed, after its own command: see agents.ResumeReported.
			if err := agents.ValidResumeArgv(p.ResumeArgv); err != nil {
				return errorResponse(id, "invalid_resume_argv", err.Error())
			}
		}
		if method == ReportMetadata {
			return okResponse(id)
		}
		if len(p.Message) > maxMessage {
			p.Message = p.Message[:maxMessage]
		}
		r := Report{
			Method: method, Pane: p.PaneID, Source: clean.Line(p.Source), Agent: clean.Line(p.Agent),
			State: p.State, Message: clean.Text(p.Message),
			SessionID: p.AgentSessionID, SessionPath: p.AgentSessionPath,
			SessionSource: p.SessionSource, ResumeArgv: p.ResumeArgv,
			Background: p.Background && p.State == "working",
		}
		if p.Seq != nil {
			r.Seq = *p.Seq
		}
		c.send(r)
		return okResponse(id)
	}
}

func cut(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// answered sends r to nerdr and waits for its answer, which is the
// response to request id.
func (c *client) answered(id string, r Report) []byte {
	return c.answeredWithin(id, r, answerWait)
}

// answeredWithin is answered, waiting as long as wait.
func (c *client) answeredWithin(id string, r Report, wait time.Duration) []byte {
	reply := make(chan Answer, 1)
	r.Reply = reply
	c.send(r)
	select {
	case a := <-reply:
		if a.Err != "" {
			return errorResponse(id, "refused", a.Err)
		}
		return result(id, map[string]any{"type": "ok", "text": a.Text})
	case <-time.After(wait):
		return errorResponse(id, "no_answer", "nerdr did not answer in time, being busy: what you asked may still be done. "+
			"Check with nerdr agents or nerdr tabs before asking again")
	}
}

func result(id string, v any) []byte {
	b, _ := json.Marshal(map[string]any{"id": id, "result": v})
	return b
}

func errorResponse(id, code, msg string) []byte {
	b, _ := json.Marshal(map[string]any{"id": id, "error": map[string]string{"code": code, "message": msg}})
	return b
}

// Send makes one request on the socket at path and returns the error the
// other end answered with, if any.
func Send(path, method string, p map[string]any) error {
	_, err := Request(path, method, p)
	return err
}

// Request is Send, also returning the text the answer had for the agent,
// if any.
func Request(path, method string, p map[string]any) (string, error) {
	return RequestWithin(path, method, p, answerWait)
}

// RequestWithin is Request for an answer nerdr may take as long as wait
// to give.
func RequestWithin(path, method string, p map[string]any, wait time.Duration) (string, error) {
	c, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return "", err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(wait + 5*time.Second))
	line, _ := json.Marshal(map[string]any{
		"id": "nerdr:" + strconv.FormatInt(time.Now().UnixNano(), 10), "method": method, "params": p,
	})
	if _, err := c.Write(append(line, '\n')); err != nil {
		return "", err
	}
	resp, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		return "", err
	}
	var r struct {
		Result struct {
			Text string `json:"text"`
		} `json:"result"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp, &r); err != nil {
		return "", errors.New("could not read the answer")
	}
	if r.Error != nil {
		return "", fmt.Errorf("%s: %s", r.Error.Code, r.Error.Message)
	}
	return r.Result.Text, nil
}
