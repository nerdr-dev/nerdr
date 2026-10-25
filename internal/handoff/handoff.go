// Package handoff is what a tab's new agent is told of the old one's work
// when the user switches the agent (ctrl+q a s).
//
// Agents do not share conversations, so on its own the new agent would
// start knowing nothing of the work in the tab. nerdr first asks the old
// agent to write a handoff (Request), which it sends back with `nerdr
// handoff`; the new agent is given it as its first message (Prompt). An
// agent that cannot write one — it has run out of tokens, has hung, or is
// not running — is not waited for: the new agent is pointed to the old
// one's conversation instead, read from its CLI's log and cut down to what
// was said (agents.Transcript), and works the rest out from the worktree.
package handoff

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/store"
)

// MaxSummary is the longest handoff taken, in bytes: it is typed into the
// new agent as a message.
const MaxSummary = 64 << 10

// Request is the message that asks the agent from to write a handoff for
// the agent to. It holds all the agent needs to know, so it works whether
// or not the nerdr-handoff skill is installed.
func Request(from, to agents.Kind) string {
	return fmt.Sprintf(`nerdr: the user is switching this tab's agent from you (%s) to %s, which has none of your conversation. Write a handoff for it, as the nerdr-handoff skill says: everything it needs to carry on the work in this worktree — the goal and why the user wants it; what has been decided and why, including options rejected; what you found out (files, causes, constraints, pitfalls); what is done and what is left, and how to check it; anything the user asked you to keep in mind. Write it in the language the user works with you in. Send it with

"${NERDR_BIN:-nerdr}" handoff - <<'EOF'
…the handoff…
EOF

Do not commit, push or change anything for it, and do nothing more once it is sent: nerdr then stops you and starts %s.`, from, to, to)
}

// Info is what the new agent is told about.
type Info struct {
	From, To agents.Kind
	// Summary is the handoff the old agent wrote; empty when it wrote
	// none.
	Summary string
	// Transcript is the file the old agent's conversation is in, cut
	// down; empty when it could not be read, and TranscriptErr why.
	Transcript    string
	TranscriptErr string
	TranscriptLen int
	// Resumed is set when the new agent continues a conversation of its
	// own from earlier in the tab.
	Resumed bool
}

// Prompt is the first message for the agent taking over.
func Prompt(in Info) string {
	var b strings.Builder
	fmt.Fprintf(&b, "nerdr: the user switched this tab's agent from %s to you (%s).", in.From, in.To)
	if in.Resumed {
		fmt.Fprintf(&b, " You are back in your own earlier conversation in this tab; since you were stopped, %s has worked here, and what it did is below.", in.From)
	} else {
		fmt.Fprintf(&b, " You have none of %s's conversation, so what it did is below.", in.From)
	}
	if in.Summary != "" {
		fmt.Fprintf(&b, "\n\n%s wrote this handoff for you:\n\n---\n%s\n---", in.From, strings.TrimSpace(in.Summary))
		if in.Transcript != "" {
			fmt.Fprintf(&b, "\n\nIf you need more, its whole conversation, cut down to what was said and the tools it ran, is in %s.", in.Transcript)
		}
	} else {
		fmt.Fprintf(&b, "\n\n%s did not write a handoff: it may have run out of tokens, not answered, or not been running.", in.From)
		if in.Transcript != "" {
			fmt.Fprintf(&b, " Read its conversation instead, cut down by nerdr to what the user and it said and the tools it ran (their output is left out): %s (%s). Its end is where the work stands.", in.Transcript, size(in.TranscriptLen))
		} else {
			reason := in.TranscriptErr
			if reason == "" {
				reason = "it had none"
			}
			fmt.Fprintf(&b, " nerdr could not read its conversation either (%s), so work out where things stand from the worktree.", reason)
		}
	}
	b.WriteString("\n\nLook at the worktree as well: git status, the branch's commits and its diff from the branch it started from, and any why notes and reading order recorded for it.")
	b.WriteString(" Then tell the user in a few lines what you understand the work to be and where it stands, and wait for them before going on.")
	return b.String()
}

func size(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d bytes", n)
	}
	return fmt.Sprintf("%d KB", (n+1023)/1024)
}

// Path is the file the conversation of agent k in the tab with id tab is
// written to, for the agent taking over to read: beside the notes for the
// worktree, which an agent can reach (it writes them), or with none, in
// the temporary directory.
func Path(worktree, tab string, k agents.Kind) string {
	name := "handoff-" + tab + "-" + string(k) + ".md"
	if dir := store.Dir(worktree); dir != "" {
		return filepath.Join(dir, "handoff", name)
	}
	return filepath.Join(os.TempDir(), "nerdr-"+name)
}

// Write writes the conversation of agent k (sessionID, or with none the
// newest in dir) to its Path, and returns the path and how long it is.
func Write(specs map[agents.Kind]agents.Spec, k agents.Kind, sessionID, dir, worktree, tab string) (string, int, error) {
	text, err := agents.Transcript(specs, k, sessionID, dir)
	if err != nil {
		return "", 0, err
	}
	path := Path(worktree, tab, k)
	// Beside the notes, where agents write: never through a link one left.
	if err := store.WriteFile(path, []byte(text), 0o600); err != nil {
		return "", 0, err
	}
	return path, len(text), nil
}
