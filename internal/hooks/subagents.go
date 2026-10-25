package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
)

// A subagent started in the background goes on after the turn that
// started it: claude and copilot end the agent's turn with a Stop while it
// is still at it, and once it is done prompt the agent with the news, which
// takes up its answer in a turn of its own. Until then the agent is not
// done. Both have a hook for a subagent starting and one for it stopping,
// with its agent_id, so the hooks keep count, in a file between them.
//
// A subagent's start or stop says nothing of the agent by itself: claude
// runs subagents of its own, for a recap, after the agent's turn is over
// (herdr's integration ignores SubagentStop for that reason). Only the
// last one stopping that the agent's Stop was waiting on is news.

// subagents is what the hooks of the agent in a tab keep: the subagents it
// has going, by agent_id, and whether its turn ended with some going.
type subagents struct {
	Going   map[string]bool `json:"going"`
	Waiting bool            `json:"waiting"`
}

// SubagentWait is what the agent's subagents are after a hook's event.
type SubagentWait struct {
	Going int  // how many are going
	Ends  bool // the event is the last stopping that the agent waited on
}

// see takes a hook's event, from the subagent agent (or the agent itself,
// for ""). A new conversation has none going.
func (s *subagents) see(event, agent string, newSession bool) SubagentWait {
	if s.Going == nil {
		s.Going = map[string]bool{}
	}
	ends := false
	switch {
	case newSession:
		*s = subagents{Going: map[string]bool{}}
	case event == "SubagentStart" && agent != "":
		s.Going[agent] = true
	case event == "SubagentStop":
		delete(s.Going, agent)
		if len(s.Going) == 0 && s.Waiting {
			s.Waiting, ends = false, true
		}
	case event == "Stop" && agent == "":
		s.Waiting = len(s.Going) > 0
	}
	return SubagentWait{Going: len(s.Going), Ends: ends}
}

// subagentsFile is where the hooks of the agent kind in tab pane keep
// its subagents.
func subagentsFile(kind, pane string) string {
	return filepath.Join(os.TempDir(), "nerdr-"+kind+"-subagents-"+filepath.Base(pane)+".json")
}

// seeSubagents takes the event in the subagents kept in file; see see.
func seeSubagents(file, event, agent string, newSession bool) SubagentWait {
	// Hooks run in separate processes. Lock a stable sidecar, not the
	// state file whose inode is replaced by the atomic rename below.
	lock, err := os.OpenFile(file+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return SubagentWait{}
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return SubagentWait{}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	var s subagents
	if b, err := os.ReadFile(file); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	before, _ := json.Marshal(s)
	w := s.see(event, agent, newSession)
	if b, _ := json.Marshal(s); string(b) != string(before) {
		// Whole or not at all, for another hook reading it meanwhile.
		tmp, err := os.CreateTemp(filepath.Dir(file), filepath.Base(file)+".*")
		if err == nil {
			defer os.Remove(tmp.Name())
			_, writeErr := tmp.Write(b)
			closeErr := tmp.Close()
			if writeErr == nil && closeErr == nil {
				_ = os.Rename(tmp.Name(), file)
			}
		}
	}
	return w
}

// stopState is what the agent's own Stop means with w: done, or still at
// work in the background. Such a report says background, so that nerdr
// does not take the still screen of an agent waiting on them for one
// interrupted.
func stopState(w SubagentWait) string {
	if w.Going > 0 {
		return "working"
	}
	return "idle"
}

// subagentState is what a subagent starting or stopping means: nothing,
// unless it is the last the agent was waiting on. Then the agent is at
// work as before, no longer in the background: it takes up the answer,
// or, when it does not, the screen says it is done.
func subagentState(w SubagentWait) string {
	if w.Ends {
		return "working"
	}
	return ""
}
