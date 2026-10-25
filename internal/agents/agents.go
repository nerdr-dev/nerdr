// Package agents knows the coding-agent CLIs nerdr can run in a tab:
// which are installed, how to start one, and how to pick its conversation
// up again after the machine has been restarted.
package agents

import (
	"bufio"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/nerdr-dev/nerdr/internal/proc"
)

// Kind names an agent CLI. It doubles as the herdr detection manifest id
// and the agent name herdr's integrations report.
type Kind string

const (
	Claude      Kind = "claude"
	OpenCode    Kind = "opencode"
	Copilot     Kind = "copilot"
	Codex       Kind = "codex"
	Cursor      Kind = "cursor"
	Gemini      Kind = "gemini"
	Antigravity Kind = "agy"
)

// All is every kind nerdr knows, in the order the picker offers them. Any
// other agent can be added in the config as a kind of its own; see Spec.
var All = []Kind{Claude, OpenCode, Copilot, Codex, Cursor, Gemini, Antigravity}

// Spec is how one kind is launched. Command is the program and any fixed
// leading arguments (a wrapper such as `my-sandbox -- copilot`); Args are appended
// to every launch, new or resumed; an agent's model flag goes there too.
// Env is set in the agent's environment. ConfigModel is opencode's model,
// which it takes only through its config (see configModelEnv). Sandboxed
// says the command runs the agent in a sandbox, so nerdr does nothing
// outside it for the agent: see app's confined.
//
// A kind nerdr does not know (`[agents.amp]` in the config) is run from its
// Command as well, with ResumeArgs added when its tab is resumed, and its
// screen read with herdr's rules for Detect, or for its own name.
type Spec struct {
	Command     []string          `toml:"command"`
	Args        []string          `toml:"args"`
	ResumeArgs  []string          `toml:"resume_args"`
	Detect      string            `toml:"detect"`
	Env         map[string]string `toml:"env"`
	ConfigModel string            `toml:"config_model"`
	Sandboxed   bool              `toml:"sandboxed"`
}

// openCodeConfigEnv is the variable opencode reads inline config from.
const openCodeConfigEnv = "OPENCODE_CONFIG_CONTENT"

// configModelEnv puts opencode's ConfigModel in its environment. opencode
// 2 dropped --model and takes the model from its config, which
// OPENCODE_CONFIG_CONTENT overrides as inline JSON; only its own server
// reads that, which startArgs sees to (--standalone). The model is merged
// over the value already set, in s.Env or inherited.
func configModelEnv(k Kind, s Spec) []string {
	if k != OpenCode || s.ConfigModel == "" {
		return nil
	}
	base := s.Env[openCodeConfigEnv]
	if base == "" {
		base = os.Getenv(openCodeConfigEnv)
	}
	var m map[string]any
	if base != "" {
		_ = json.Unmarshal([]byte(base), &m)
	}
	if m == nil {
		m = map[string]any{}
	}
	m["model"] = s.ConfigModel
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return []string{openCodeConfigEnv + "=" + string(b)}
}

// startArgs is the command line every launch of k begins with: the
// command and Args.
func startArgs(s Spec, k Kind) []string {
	argv := append(append([]string{}, s.Command...), s.Args...)
	if k == OpenCode && (s.ConfigModel != "" || helpHas(s, []string{"--help"}, "--standalone")) {
		// opencode runs its own server instead of the shared background
		// service: only its own reads the model in the environment, and
		// only its own runs nerdr's plugin with the tab's environment
		// (see hooks.OpenCodePlugin); the service has none of it.
		argv = append(argv, "--standalone")
	}
	return argv
}

// envList is k's environment as NAME=value, sorted: s's Env, and for
// opencode the variable that carries its ConfigModel.
func envList(k Kind, s Spec) []string {
	var out []string
	for name, v := range s.Env {
		if name == openCodeConfigEnv && k == OpenCode && s.ConfigModel != "" {
			continue // merged into the model's entry below
		}
		out = append(out, name+"="+v)
	}
	out = append(out, configModelEnv(k, s)...)
	sort.Strings(out)
	return out
}

// DefaultSpec is what a kind runs when the config says nothing about it;
// nothing for a kind nerdr does not know.
func DefaultSpec(k Kind) Spec {
	switch k {
	case Cursor:
		// Its installer puts it on the PATH as both `agent` and
		// `cursor-agent`; the first is too generic a name to look for.
		return Spec{Command: []string{"cursor-agent"}}
	}
	if !Known(k) {
		return Spec{}
	}
	return Spec{Command: []string{string(k)}}
}

// Command is how k is run, and the variables it is run with, from the
// config: for running it for something else than a tab, such as asking
// it about its settings.
func Command(specs map[Kind]Spec, k Kind) (argv, env []string) {
	s := specFor(specs, k)
	return s.Command, envList(k, s)
}

// HasFlag reports whether k, as the config runs it, knows flag: one an
// older version does not know would stop it starting.
func HasFlag(specs map[Kind]Spec, k Kind, flag string) bool {
	return helpHas(specFor(specs, k), []string{"--help"}, flag)
}

// Known is whether k is one of the kinds nerdr has support of its own for.
func Known(k Kind) bool {
	return slices.Contains(All, k)
}

// Available reports which kinds have their command on PATH: the ones in
// All, in that order, then those only the config names, by name.
func Available(specs map[Kind]Spec) []Kind {
	kinds := append([]Kind{}, All...)
	var custom []Kind
	for k := range specs {
		if !Known(k) {
			custom = append(custom, k)
		}
	}
	slices.Sort(custom)
	var out []Kind
	for _, k := range append(kinds, custom...) {
		s := specFor(specs, k)
		if len(s.Command) == 0 {
			continue
		}
		if _, err := exec.LookPath(s.Command[0]); err == nil {
			out = append(out, k)
		}
	}
	return out
}

// DetectID is the herdr manifest k's screen is read with, and the agent
// name its herdr integration reports: the config's Detect, or k itself.
func DetectID(specs map[Kind]Spec, k Kind) string {
	if s, ok := specs[k]; ok && s.Detect != "" {
		return s.Detect
	}
	return string(k)
}

// specFor is how k is started: its section in the config, with the
// default command where the section only adds arguments or variables.
func specFor(specs map[Kind]Spec, k Kind) Spec {
	s, ok := specs[k]
	if !ok {
		return DefaultSpec(k)
	}
	if len(s.Command) == 0 {
		s.Command = DefaultSpec(k).Command
	}
	return s
}

// Launch is a resolved command line for one tab.
type Launch struct {
	Argv []string
	// SessionID is what the tab should remember so the conversation can be
	// resumed later; empty when the agent assigns its own and it is looked
	// up at resume time instead.
	SessionID string
	// Env is added to the agent's environment, as NAME=value.
	Env []string
	// New is set when Resume was asked for but starts a fresh
	// conversation instead (see Resume).
	New bool
	// Unsure is set when SessionID is one nerdr gave an agent that may not
	// have taken it: looked for once the agent has said something (see
	// HasSession).
	Unsure bool
}

// StartPrompt is the arguments that start k with prompt as its first
// message, or nil when k is to be typed it like any other. nerdr types a
// message in once the agent's screen looks idle, but copilot's has no
// screen herdr's rules know as idle: any still screen is taken for one,
// and the message was typed into it while copilot was starting, or asking
// whether to trust the folder, and was lost. Its -i runs the prompt once
// it is ready. Codex's screen looks idle while it is still starting, or
// updating itself, and a message typed in then was left unsent in its
// prompt; it takes the prompt as its last argument, after "--" so a task
// such as "review" is not taken for a command of its. opencode's rules
// have no idle screen either, and the reviewer's request typed into it
// while it started was lost: the review never began. Its --prompt sends
// the prompt once it is up.
//
// A prompt longer than maxArg is typed in after all: Linux takes no single
// argument of 128 KB or more, and the agent would not start.
func StartPrompt(k Kind, prompt string) []string {
	if len(prompt) >= maxArg {
		return nil
	}
	switch k {
	case Copilot:
		return []string{"-i", prompt}
	case Codex:
		return []string{"--", prompt}
	case OpenCode:
		return []string{"--prompt", prompt}
	}
	return nil
}

// maxArg is the longest prompt StartPrompt puts on the command line.
const maxArg = 120 << 10

// NewSession is the command that starts a fresh conversation.
func NewSession(specs map[Kind]Spec, k Kind) Launch {
	l := newSession(specs, k)
	l.Env = envList(k, specFor(specs, k))
	return l
}

func newSession(specs map[Kind]Spec, k Kind) Launch {
	s := specFor(specs, k)
	argv := startArgs(s, k)
	switch k {
	case Claude:
		// Claude takes the id from us, so resuming never has to guess
		// which of the worktree's conversations was this tab's.
		id := newUUID()
		return Launch{Argv: append(argv, "--session-id", id), SessionID: id}
	case Gemini:
		// Gemini takes the id from us as well, so its log is found by it.
		id := newUUID()
		return Launch{Argv: append(argv, "--session-id", id), SessionID: id}
	case Copilot:
		// Copilot takes the id from us too, where it knows --session-id.
		// Without it a reviewer's conversation, had in the tab's worktree,
		// was never known as one, and the tab carried it on as its own.
		if helpHas(s, []string{"--help"}, "--session-id") {
			id := newUUID()
			return Launch{Argv: append(argv, "--session-id", id), SessionID: id}
		}
	case Codex:
		return Launch{Argv: append(argv, codexNoDaemon(s)...)}
	case OpenCode:
		// opencode makes the session it is given when there is none by
		// that id, in the form of its own; whether it did is looked at
		// once something has been said in it.
		if helpHas(s, []string{"--help"}, "or to create if it does not exist") {
			id := openCodeID()
			return Launch{Argv: append(argv, "--session", id), SessionID: id, Unsure: true}
		}
	}
	return Launch{Argv: argv}
}

// NewSessionIn is NewSession for a conversation in dir, with an id nerdr
// knows for each agent that can be given one: cursor-agent makes its chat
// first (create-chat), which runs it, so this is not for the screen's
// goroutine.
func NewSessionIn(specs map[Kind]Spec, k Kind, dir string) Launch {
	l := NewSession(specs, k)
	if k == Cursor {
		if id := cursorCreateChat(specFor(specs, k), dir); id != "" {
			l.Argv = append(l.Argv, "--resume", id)
			l.SessionID = id
		}
	}
	return l
}

// cursorCreateChat makes a new, empty chat in dir and returns its id.
func cursorCreateChat(s Spec, dir string) string {
	if len(s.Command) == 0 {
		return ""
	}
	cmd := proc.Detach(exec.Command(s.Command[0], append(s.Command[1:], "create-chat")...))
	cmd.Dir = dir
	out, err := runFor(cmd, 15*time.Second)
	if err != nil {
		return ""
	}
	return checked(strings.TrimSpace(string(out)))
}

// openCodeID is a new session id in opencode's form: ses_, the time
// counting down in 12 hex digits, and 14 random letters and digits.
func openCodeID() string {
	const letters = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	n := ^(uint64(time.Now().UnixMilli()) * 0x1000) & (1<<48 - 1)
	var b [14]byte
	_, _ = rand.Read(b[:])
	tail := make([]byte, len(b))
	for i, c := range b {
		tail[i] = letters[int(c)%len(letters)]
	}
	return fmt.Sprintf("ses_%012x%s", n, tail)
}

// codexNoDaemon is codex's --no-daemon, when the codex run has it.
//
// codex runs its sessions in one app-server shared by every codex of the
// user, started by the first; the commands its agent runs get that
// server's environment, not the tab's. So a codex in a tab ran its
// commands with the variables of whatever started the server — another
// tab's NERDR_TAB, or none, and "nerdr post-review" found no nerdr to send to.
// --no-daemon keeps the server in the tab's own codex. An older codex
// knows no such flag and would not start with it.
func codexNoDaemon(s Spec) []string {
	if !helpHas(s, []string{"resume", "--help"}, "--no-daemon") {
		return nil
	}
	return []string{"--no-daemon"}
}

// helpHas reports whether the help s's command prints for args mentions
// flag, asked once per command: a flag an older version does not know
// would stop it starting.
func helpHas(s Spec, args []string, flag string) bool {
	if len(s.Command) == 0 {
		return false
	}
	key := strings.Join(append(append(append([]string{}, s.Command...), args...), flag), "\x00")
	helpMu.Lock()
	defer helpMu.Unlock()
	has, ok := helpSeen[key]
	if !ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cmd := proc.Detach(exec.CommandContext(ctx, s.Command[0], append(append([]string{}, s.Command[1:]...), args...)...))
		out, _ := proc.Output(cmd)
		cancel()
		has = strings.Contains(string(out), flag)
		helpSeen[key] = has
	}
	return has
}

var (
	helpMu   sync.Mutex
	helpSeen = map[string]bool{} // by command, arguments and flag; see helpHas
)

// sessionIDOK is what a conversation id looks like: the uuids and
// prefixed ids (ses_…) the agents use.
var sessionIDOK = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,199}$`)

// ValidSessionID reports whether id can be put on an agent's command line
// as a conversation to resume. The tab's id comes from the agent's own
// integration, so the agent can name anything: "--dangerously-skip-
// permissions" after --resume would be read as that flag, and the tab
// would be restarted with the agent's permissions off.
func ValidSessionID(id string) bool { return sessionIDOK.MatchString(id) }

// checked is id, or "" when it is not a valid one: the ids found in the
// agents' own files are as much the agent's to write as those it reports.
func checked(id string) string {
	if !ValidSessionID(id) {
		return ""
	}
	return id
}

// Resume is the command that continues the tab's conversation in dir. When
// the conversation cannot be found it falls back to the agent's own
// "continue the latest here", and failing that the caller can start fresh
// with NewSession.
//
// The conversations in skip are not the tab's, though they were had in dir:
// those of the agents that reviewed its work. Looking for the latest passes
// them over, and when one of them may be all there is, the agent's own
// "continue the latest" is not risked: a fresh conversation is started.
func Resume(specs map[Kind]Spec, k Kind, sessionID, dir string, skip map[string]bool) Launch {
	l := resume(specs, k, sessionID, dir, skip)
	if l.SessionID == "" && len(skip) > 0 && k != Codex && slices.Contains(All, k) {
		// codex's --last is left to it: it reports no id to look for, so
		// a tab with codex would never carry on.
		l = NewSession(specs, k)
		l.New = true
		return l
	}
	l.Env = envList(k, specFor(specs, k))
	return l
}

func resume(specs map[Kind]Spec, k Kind, sessionID, dir string, skip map[string]bool) Launch {
	if !ValidSessionID(sessionID) {
		sessionID = ""
	}
	s := specFor(specs, k)
	argv := startArgs(s, k)
	switch k {
	case Claude:
		// A conversation claude never saved, as when the tab was closed
		// before anything was said, cannot be resumed: the one before it
		// in the worktree is carried on instead.
		if sessionID == "" || len(claudeLogs(Claude, sessionID)) == 0 {
			if id := checked(latestClaudeSession(dir, skip)); id != "" {
				sessionID = id
			}
		}
		if sessionID != "" {
			return Launch{Argv: append(argv, "--resume", sessionID), SessionID: sessionID}
		}
		return Launch{Argv: append(argv, "--continue")}
	case OpenCode:
		if sessionID == "" {
			sessionID = checked(latestOpenCodeSession(s, dir, skip))
		}
		if sessionID != "" {
			return Launch{Argv: append(argv, "--session", sessionID), SessionID: sessionID}
		}
		return Launch{Argv: append(argv, "--continue")}
	case Copilot:
		// Copilot keeps a session it was started on even when nothing was
		// said in it: the newest with something said is carried on instead.
		if sessionID == "" || !copilotSaid(sessionID) {
			if id := checked(latestCopilotSession(dir, skip)); id != "" {
				sessionID = id
			}
		}
		if sessionID != "" {
			return Launch{Argv: append(argv, "--resume="+sessionID), SessionID: sessionID}
		}
		// Copilot's --continue takes the newest session of any folder: as
		// nerdr starts every tab at once, another tab's, just begun.
		l := newSession(specs, k)
		l.New = true
		return l
	case Codex:
		// codex's --last only looks at sessions started in the directory
		// it runs in, so it needs no help to stay out of sibling worktrees.
		argv = append(append(argv, "resume"), codexNoDaemon(s)...)
		if sessionID != "" {
			return Launch{Argv: append(argv, sessionID), SessionID: sessionID}
		}
		return Launch{Argv: append(argv, "--last")}
	case Cursor:
		// cursor-agent keeps its chats in a folder per working
		// directory, so its --continue stays in this worktree too; the
		// newest is looked up anyway, for the id the tab remembers.
		if sessionID == "" {
			sessionID = checked(latestCursorChat(dir, skip))
		}
		if sessionID != "" {
			return Launch{Argv: append(argv, "--resume", sessionID), SessionID: sessionID}
		}
		return Launch{Argv: append(argv, "--continue")}
	case Gemini:
		if sessionID == "" {
			id, _ := latestGeminiSession(dir, skip)
			sessionID = checked(id)
		}
		if sessionID != "" {
			return Launch{Argv: append(argv, "--resume", sessionID), SessionID: sessionID}
		}
		return Launch{Argv: append(argv, "--resume", "latest")}
	case Antigravity:
		if sessionID == "" {
			sessionID = checked(latestAntigravityConversation(dir, skip))
		}
		if sessionID != "" {
			return Launch{Argv: append(argv, "--conversation", sessionID), SessionID: sessionID}
		}
		return Launch{Argv: append(argv, "--continue")}
	}
	return Launch{Argv: append(argv, s.ResumeArgs...)}
}

// ResumeReported is how k carries on the conversation its herdr
// integration said reopens with reported (herdr's resume_argv, such as
// `my-agent --resume ID --model m`), for an agent nerdr does not know:
// nerdr has its own ways for the others. Only the arguments are taken,
// after k's own command and arguments, so a wrapper or sandbox in the
// command stays around the agent; and only when the reported command is
// k's program, one of the words of its command. ok is false otherwise,
// and ResumeArgs are used.
func ResumeReported(specs map[Kind]Spec, k Kind, reported []string) (l Launch, ok bool) {
	if len(reported) == 0 || slices.Contains(All, k) || ValidResumeArgv(reported) != nil {
		return Launch{}, false
	}
	s := specFor(specs, k)
	if !slices.ContainsFunc(s.Command, func(w string) bool { return filepath.Base(w) == reported[0] }) {
		return Launch{}, false
	}
	return Launch{Argv: append(startArgs(s, k), reported[1:]...), Env: envList(k, s)}, true
}

// ValidResumeArgv says what is wrong with a resume command an agent
// reported, as herdr checks it: one to a few dozen words of no more than
// 8 KiB, without control characters, starting with a plain command name.
func ValidResumeArgv(argv []string) error {
	if len(argv) == 0 {
		return errors.New("resume_argv must not be empty")
	}
	if len(argv) > 64 {
		return errors.New("resume_argv allows at most 64 arguments")
	}
	n := 0
	for _, a := range argv {
		n += len(a)
		if strings.ContainsFunc(a, unicode.IsControl) {
			return errors.New("resume_argv must not contain control characters")
		}
	}
	if n > 8<<10 {
		return errors.New("resume_argv allows at most 8192 bytes")
	}
	if !plainCommand.MatchString(argv[0]) {
		return errors.New("resume_argv must start with a plain command name, not a path")
	}
	return nil
}

var plainCommand = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_.-]*$`)

// latestOpenCodeSession asks opencode for its sessions and picks the newest
// one started in dir. opencode groups sessions by project, and every
// worktree of a repository is the same project to it, so its own
// --continue could pick up a conversation from a sibling worktree.
func latestOpenCodeSession(s Spec, dir string, skip map[string]bool) string {
	best, bestAt := "", int64(-1)
	for _, ses := range openCodeSessions(s, dir) {
		if ses.Updated > bestAt && !skip[ses.ID] {
			best, bestAt = ses.ID, ses.Updated
		}
	}
	return best
}

// openCodeSession is one of opencode's sessions, as it lists them.
type openCodeSession struct {
	ID        string `json:"id"`
	Updated   int64  `json:"updated"`
	Directory string `json:"directory"`
}

// openCodeSessions asks opencode for its sessions started in dir.
func openCodeSessions(s Spec, dir string) []openCodeSession {
	if len(s.Command) == 0 {
		return nil
	}
	cmd := proc.Detach(exec.Command(s.Command[0], append(s.Command[1:], "session", "list", "--format", "json", "-n", "50")...))
	cmd.Dir = dir
	done := make(chan struct{})
	var out []byte
	go func() {
		out, _ = proc.Output(cmd)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return nil
	}
	var sessions []openCodeSession
	if json.Unmarshal(out, &sessions) != nil {
		return nil
	}
	var here []openCodeSession
	for _, ses := range sessions {
		if samePath(ses.Directory, dir) {
			here = append(here, ses)
		}
	}
	return here
}

// HasConversation reports whether k has had a conversation in dir that a
// tab opened on it can carry on: one an agent had in a worktree before
// nerdr opened it, or in a tab since closed.
func HasConversation(specs map[Kind]Spec, k Kind, dir string, skip map[string]bool) bool {
	switch k {
	case Claude:
		return latestClaudeSession(dir, skip) != ""
	case OpenCode:
		return latestOpenCodeSession(specFor(specs, k), dir, skip) != ""
	case Copilot:
		return latestCopilotSession(dir, skip) != ""
	case Codex:
		_, err := codexLog("", dir)
		return err == nil
	case Cursor:
		return latestCursorChat(dir, skip) != ""
	case Gemini:
		id, _ := latestGeminiSession(dir, skip)
		return id != ""
	case Antigravity:
		return latestAntigravityConversation(dir, skip) != ""
	}
	return false
}

// claudeHome is where claude keeps its conversations.
func claudeHome() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// claudeProject is the folder claude keeps dir's conversations in, named
// after the path with every character but letters and digits made a dash.
func claudeProject(dir string) string {
	home := claudeHome()
	if home == "" || dir == "" {
		return ""
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	name := strings.Map(func(r rune) rune {
		if r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, dir)
	return filepath.Join(home, "projects", name)
}

// latestClaudeSession is the id of the conversation last written to in
// dir that has something the user said in it.
func latestClaudeSession(dir string, skip map[string]bool) string {
	root := claudeProject(dir)
	if root == "" {
		return ""
	}
	files, _ := filepath.Glob(filepath.Join(root, "*.jsonl"))
	sort.Slice(files, func(i, j int) bool { return modTime(files[i]).After(modTime(files[j])) })
	for _, f := range files {
		if id := strings.TrimSuffix(filepath.Base(f), ".jsonl"); !skip[id] && fileHas(f, `"type":"user"`) {
			return id
		}
	}
	return ""
}

func modTime(path string) time.Time {
	st, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

// fileHas reports whether the file at path has s in it.
func fileHas(path, s string) bool {
	data, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(data), s)
}

// copilotSaid reports whether anything was said in Copilot CLI's session
// id: it keeps one for every start, used or not.
func copilotSaid(id string) bool {
	home, err := os.UserHomeDir()
	if err != nil || strings.ContainsAny(id, `/\`) {
		return false
	}
	return fileHas(filepath.Join(home, ".copilot", "session-state", id, "events.jsonl"), `"user.message"`)
}

// latestCopilotSession finds the newest Copilot CLI session whose working
// directory is dir and that has something said in it, from the
// session-state folders the CLI keeps.
func latestCopilotSession(dir string, skip map[string]bool) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	root := filepath.Join(home, ".copilot", "session-state")
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	type cand struct {
		id string
		at time.Time
	}
	var cands []cand
	for _, e := range entries {
		if !e.IsDir() || skip[e.Name()] {
			continue
		}
		ws := filepath.Join(root, e.Name(), "workspace.yaml")
		data, err := os.ReadFile(ws)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			v, ok := strings.CutPrefix(strings.TrimSpace(line), "cwd:")
			if !ok {
				continue
			}
			v = strings.Trim(strings.TrimSpace(v), `"'`)
			if st, err := os.Stat(ws); err == nil && samePath(v, dir) && copilotSaid(e.Name()) {
				cands = append(cands, cand{e.Name(), st.ModTime()})
			}
			break
		}
	}
	if len(cands) == 0 {
		return ""
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].at.After(cands[j].at) })
	return cands[0].id
}

// cursorChats is where cursor-agent keeps the chats started in dir: a
// folder per chat, named by its id, under the MD5 of the directory's path.
func cursorChats(dir string) string {
	root := os.Getenv("CURSOR_CONFIG_DIR")
	if root == "" {
		if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
			root = filepath.Join(x, "cursor")
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return ""
			}
			root = filepath.Join(home, ".cursor")
		}
	}
	// The path as the agent's process.cwd() has it: absolute, with no
	// symbolic links.
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	sum := md5.Sum([]byte(abs))
	return filepath.Join(root, "chats", hex.EncodeToString(sum[:]))
}

// latestCursorChat is the id of the chat last written to in dir.
func latestCursorChat(dir string, skip map[string]bool) string {
	root := cursorChats(dir)
	if root == "" {
		return ""
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	best, bestAt := "", time.Time{}
	for _, e := range entries {
		st, err := os.Stat(filepath.Join(root, e.Name(), "store.db"))
		if err == nil && e.IsDir() && !skip[e.Name()] && st.ModTime().After(bestAt) {
			best, bestAt = e.Name(), st.ModTime()
		}
	}
	return best
}

// geminiHome is Gemini CLI's folder, where it keeps its conversations.
func geminiHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".gemini")
}

// geminiChats is the folder Gemini CLI keeps dir's conversations in:
// ~/.gemini/tmp/<id>/chats, the id from the projects.json that maps each
// folder to one.
func geminiChats(dir string) string {
	g := geminiHome()
	if g == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(g, "projects.json"))
	if err != nil {
		return ""
	}
	var reg struct {
		Projects map[string]string `json:"projects"`
	}
	if json.Unmarshal(data, &reg) != nil {
		return ""
	}
	for path, id := range reg.Projects {
		if samePath(path, dir) && id != "" && !strings.ContainsAny(id, `/\`) {
			return filepath.Join(g, "tmp", id, "chats")
		}
	}
	return ""
}

// latestGeminiSession is the id and log of the conversation last written
// to in dir.
func latestGeminiSession(dir string, skip map[string]bool) (id, path string) {
	root := geminiChats(dir)
	if root == "" {
		return "", ""
	}
	files, _ := filepath.Glob(filepath.Join(root, "session-*.json*"))
	var bestAt time.Time
	for _, f := range files {
		st, err := os.Stat(f)
		if err != nil || !st.ModTime().After(bestAt) {
			continue
		}
		if sid := geminiSessionID(f); sid != "" && !skip[sid] {
			id, path, bestAt = sid, f, st.ModTime()
		}
	}
	return id, path
}

// geminiSessionID reads the conversation's id from the start of its log:
// the first line of a .jsonl one, the object of an older .json one.
func geminiSessionID(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var head struct {
		SessionID string `json:"sessionId"`
	}
	if strings.HasSuffix(path, ".jsonl") {
		line, _ := r.ReadBytes('\n')
		_ = json.Unmarshal(line, &head)
	} else {
		_ = json.NewDecoder(r).Decode(&head)
	}
	return head.SessionID
}

// latestAntigravityConversation is the conversation last started in dir,
// from the history of prompts the Antigravity CLI keeps, each with its
// workspace and conversation.
func latestAntigravityConversation(dir string, skip map[string]bool) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	best, bestAt := "", int64(-1)
	_ = eachLine(filepath.Join(home, ".gemini", "antigravity-cli", "history.jsonl"), func(b []byte) {
		var e struct {
			Timestamp      int64  `json:"timestamp"`
			Workspace      string `json:"workspace"`
			ConversationID string `json:"conversationId"`
		}
		if json.Unmarshal(b, &e) == nil && e.ConversationID != "" && !skip[e.ConversationID] && e.Timestamp >= bestAt && samePath(e.Workspace, dir) {
			best, bestAt = e.ConversationID, e.Timestamp
		}
	})
	return best
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ca, errA := filepath.EvalSymlinks(a)
	cb, errB := filepath.EvalSymlinks(b)
	if errA == nil && errB == nil {
		return ca == cb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Claim is the id of the conversation an agent of kind k started in dir
// since since, for one nerdr could not give an id and that has not
// reported it, or "" while that is not certain. It is looked for each
// time the agent finishes a turn, until it is found.
//
// The conversations there since since that are not in skip, the other
// agents', are the candidates. One alone is the agent's. Of several, the
// one in which the user said one of the things in said — what nerdr typed
// in to this agent — is, when only one has; none is taken while more than
// one could be, as when two agents of the kind started at once and were
// told the same: a later turn tells them apart.
func Claim(specs map[Kind]Spec, k Kind, dir string, skip map[string]bool, since time.Time, said []string) string {
	var cands []string
	switch k {
	case Codex:
		cands = codexSessionsSince(dir, skip, since)
	case Antigravity:
		cands = antigravitySince(dir, skip, since)
	default:
		// nerdr gives these an id at start; this is for when it could not
		// make sure of it, and the newest is all there is to go by.
		var id string
		switch k {
		case Claude:
			id = latestClaudeSession(dir, skip)
		case Copilot:
			id = latestCopilotSession(dir, skip)
		case Gemini:
			id, _ = latestGeminiSession(dir, skip)
		case OpenCode:
			id = latestOpenCodeSession(specFor(specs, k), dir, skip)
		case Cursor:
			id = latestCursorChat(dir, skip)
		}
		if id != "" {
			cands = []string{id}
		}
	}
	if len(cands) == 1 && len(said) == 0 {
		return checked(cands[0])
	}
	var match []string
	for _, id := range cands {
		if saidIn(userSaid(specs, k, id, dir), said) {
			match = append(match, id)
		}
	}
	switch {
	case len(match) == 1:
		return checked(match[0])
	case len(match) == 0 && len(cands) == 1:
		// What it was told is not in it as nerdr typed it (an agent may
		// tidy a pasted message): the only one there is still its own.
		return checked(cands[0])
	}
	return ""
}

// HasSession reports whether conversation id of kind k was had in dir:
// for an id nerdr gave an agent that may not have taken it.
func HasSession(specs map[Kind]Spec, k Kind, id, dir string) bool {
	switch k {
	case OpenCode:
		return slices.ContainsFunc(openCodeSessions(specFor(specs, k), dir), func(s openCodeSession) bool { return s.ID == id })
	case Cursor:
		root := cursorChats(dir)
		if root == "" || strings.ContainsAny(id, `/\`) {
			return false
		}
		_, err := os.Stat(filepath.Join(root, id))
		return err == nil
	}
	return true
}

// userSaid is what the user said in conversation id of kind k, as its
// log has it.
func userSaid(specs map[Kind]Spec, k Kind, id, dir string) []string {
	if k == Antigravity {
		return antigravitySaid(id)
	}
	turns, err := turnsOf(specs, k, id, dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, t := range turns {
		if t.who == "user" {
			out = append(out, t.text)
		}
	}
	return out
}

// saidIn reports whether any of said is among what the log has the user
// say, compared by their words, a long message by its start.
func saidIn(logged, said []string) bool {
	norm := func(s string) string {
		s = strings.Join(strings.Fields(s), " ")
		if r := []rune(s); len(r) > 200 {
			s = string(r[:200])
		}
		return s
	}
	for _, s := range said {
		want := norm(s)
		if want == "" {
			continue
		}
		for _, l := range logged {
			if got := norm(l); got != "" && (strings.Contains(got, want) || strings.Contains(want, got)) {
				return true
			}
		}
	}
	return false
}

// codexRollout is the id at the end of a codex log's name,
// rollout-<when>-<id>.jsonl.
var codexRollout = regexp.MustCompile(`-([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\.jsonl$`)

// codexSessionsSince are the ids of the codex conversations started in
// dir and written to since since, not in skip.
func codexSessionsSince(dir string, skip map[string]bool, since time.Time) []string {
	files, _ := filepath.Glob(filepath.Join(codexHome(), "sessions", "*", "*", "*", "rollout-*.jsonl"))
	var out []string
	for _, f := range files {
		m := codexRollout.FindStringSubmatch(f)
		if m == nil || skip[m[1]] || modTime(f).Before(since) {
			continue
		}
		var cwd string
		_ = eachFirstLine(f, func(b []byte) {
			var e struct {
				Payload struct {
					Cwd string `json:"cwd"`
				} `json:"payload"`
			}
			if json.Unmarshal(b, &e) == nil {
				cwd = e.Payload.Cwd
			}
		})
		if samePath(cwd, dir) {
			out = append(out, m[1])
		}
	}
	return out
}

// antigravityHistory is the Antigravity CLI's history of prompts, each
// with its workspace and conversation.
func antigravityHistory(each func(display, workspace, conversation, kind string, at int64)) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	_ = eachLine(filepath.Join(home, ".gemini", "antigravity-cli", "history.jsonl"), func(b []byte) {
		var e struct {
			Display        string `json:"display"`
			Timestamp      int64  `json:"timestamp"`
			Workspace      string `json:"workspace"`
			ConversationID string `json:"conversationId"`
			Type           string `json:"type"`
		}
		if json.Unmarshal(b, &e) == nil && e.ConversationID != "" {
			each(e.Display, e.Workspace, e.ConversationID, e.Type, e.Timestamp)
		}
	})
}

// antigravitySince are the conversations prompted in dir since since, not
// in skip.
func antigravitySince(dir string, skip map[string]bool, since time.Time) []string {
	var out []string
	antigravityHistory(func(_, ws, id, _ string, at int64) {
		if !skip[id] && at >= since.UnixMilli() && samePath(ws, dir) && !slices.Contains(out, id) {
			out = append(out, id)
		}
	})
	return out
}

// antigravitySaid is what was typed in Antigravity's conversation id,
// its commands left out.
func antigravitySaid(id string) []string {
	var out []string
	antigravityHistory(func(display, _, conv, kind string, _ int64) {
		if conv == id && kind != "slash_command" {
			out = append(out, display)
		}
	})
	return out
}

// modelFlags are the flags a kind's model is given with on its command
// line; the first is the one nerdr gives.
var modelFlags = map[Kind][]string{
	Claude: {"--model"}, Codex: {"--model", "-m"}, Copilot: {"--model"},
	Cursor: {"--model"}, Gemini: {"--model", "-m"}, Antigravity: {"--model"},
}

// TakesModel reports whether nerdr can start k with a model of its
// choosing: a kind it knows the model flag of, or opencode.
func TakesModel(k Kind) bool {
	_, ok := modelFlags[k]
	return ok || k == OpenCode
}

// WithModel is specs with k started with model in place of the model
// the config gives it, if any; specs itself when model is "" or nerdr
// does not know how k is given one.
func WithModel(specs map[Kind]Spec, k Kind, model string) map[Kind]Spec {
	if model == "" || !TakesModel(k) {
		return specs
	}
	out := make(map[Kind]Spec, len(specs)+1)
	for kind, s := range specs {
		out[kind] = s
	}
	s := specFor(specs, k)
	if k == OpenCode {
		s.ConfigModel = model
		out[k] = s
		return out
	}
	flags := modelFlags[k]
	var args []string
	for i := 0; i < len(s.Args); i++ {
		a := s.Args[i]
		switch {
		case slices.Contains(flags, a):
			i++ // and its value
		case slices.ContainsFunc(flags, func(f string) bool { return strings.HasPrefix(a, f+"=") }):
		default:
			args = append(args, a)
		}
	}
	s.Args = append(args, flags[0], model)
	out[k] = s
	return out
}
