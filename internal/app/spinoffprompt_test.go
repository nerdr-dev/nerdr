package app

import (
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/fleet"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/spinoff"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// askCore sends report r to c, its method's handler, with what c answers.
func askCore(c *core.Core, r hooks.Report) hooks.Answer {
	reply := make(chan hooks.Answer, 1)
	r.Reply = reply
	switch r.Method {
	case hooks.Spinoff:
		c.OnSpinoff(r)
	case hooks.SpinoffPrompt:
		c.OnSpinoffPrompt(r)
	}
	select {
	case a := <-reply:
		return a
	case <-time.After(time.Second):
		return hooks.Answer{Err: "no answer"}
	}
}

func planOf(tb *core.Tab, branches ...string) hooks.Report {
	var parts []spinoff.Part
	for _, b := range branches {
		parts = append(parts, spinoff.Part{Branch: b, Summary: b + "."})
	}
	return hooks.Report{Method: hooks.Spinoff, Pane: tb.HookKey(), Spinoff: parts}
}

// promptOf is the prompt for the part waiting in tb as branch, by its id.
func promptOf(tb *core.Tab, branch string) hooks.Report {
	id := ""
	if tb.Spinoff != nil {
		for _, pt := range tb.Spinoff.Next {
			if pt.Branch == branch {
				id = pt.ID
			}
		}
	}
	return hooks.Report{Method: hooks.SpinoffPrompt, Pane: tb.HookKey(), Part: id, Text: "Do " + branch + "."}
}

// A prompt from an agent in a sandbox is not taken for a part sent from
// outside one: it would go to an agent outside one.
func TestSpinoffPromptKeepsTheSandbox(t *testing.T) {
	m := testModel(t)
	c, tb := &m.home().Core, m.Cur()
	m.dialog = &closeTab{t: tb} // no dialog opens
	if r := askCore(c, planOf(tb, "fix-a")); r.Err != "" {
		t.Fatal(r.Err)
	}
	tb.InSandbox = true // switched to an agent in a sandbox meanwhile
	if r := askCore(c, promptOf(tb, "fix-a")); !strings.Contains(r.Err, "sandbox") || tb.Spinoff.Next[0].Prompt != "" {
		t.Fatalf("taken: %+v", r)
	}
	tb.InSandbox = false
	if r := askCore(c, promptOf(tb, "fix-a")); r.Err != "" || tb.Spinoff.Next[0].Prompt != "Do fix-a." {
		t.Fatalf("not taken from outside one: %+v", r)
	}
}

// A prompt is sent once: its tab may be being made with it, so another
// for the same part is refused, and the first kept.
func TestSpinoffPromptIsNotReplaced(t *testing.T) {
	m := testModel(t)
	c, tb := &m.home().Core, m.Cur()
	m.dialog = &closeTab{t: tb}
	askCore(c, planOf(tb, "fix-a"))
	askCore(c, promptOf(tb, "fix-a"))
	again := promptOf(tb, "fix-a")
	again.Text = "Do something else."
	if r := askCore(c, again); !strings.Contains(r.Err, "already has the prompt") || tb.Spinoff.Next[0].Prompt != "Do fix-a." {
		t.Fatalf("replaced: %+v, %q", r, tb.Spinoff.Next[0].Prompt)
	}
}

// The dialog waiting for a prompt makes the tab on the machine the user
// said, and not on another should that one go meanwhile.
func TestSpinoffWaitKeepsTheMachine(t *testing.T) {
	m := testModel(t)
	fakeAgent(m, agents.Claude)
	tb := m.Cur()
	dir := testRepo(t)
	tb.Worktrees = []workspace.Worktree{{Repo: dir, Path: dir, Branch: "main"}}
	m.dialog = &closeTab{t: tb}
	askCore(&m.home().Core, planOf(tb, "fix-a"))
	m.dialog = nil
	m.openSpinoff(tb)
	d := m.dialog.(*spinoffDialog)
	d.key(m, keyMsg("enter"))
	d.waitOn = &fleet.Machine{Name: "far"} // said with enter, gone since
	askCore(&m.home().Core, promptOf(tb, "fix-a"))
	m.spinoffPrompted(tb)
	if d.waiting || !strings.Contains(d.err, "cannot be reached") || tb.SpinoffBusy != "" {
		t.Fatalf("made elsewhere: waiting %v, %q, %q", d.waiting, d.err, tb.SpinoffBusy)
	}
}

// A prompt comes by the id nerdr gave its part: late, after the user went
// through that part, it is refused, and taken for no other part sent
// since under the same branch; the agent is told the id of each.
func TestSpinoffPromptByItsPart(t *testing.T) {
	m := testModel(t)
	c, tb := &m.home().Core, m.Cur()
	m.dialog = &closeTab{t: tb}
	r := askCore(c, planOf(tb, "fix-a", "fix-b"))
	first := tb.Spinoff.Next[0].ID
	if first == "" || r.Text != first+"\tfix-a\n"+tb.Spinoff.Next[1].ID+"\tfix-b" {
		t.Fatalf("answer %q", r.Text)
	}
	late := promptOf(tb, "fix-a")
	c.SpinoffNext(tb, spinoff.Outcome{Part: tb.Spinoff.Next[0], Skipped: true})
	if r := askCore(c, planOf(tb, "fix-a")); r.Err != "" || len(tb.Spinoff.Next) != 2 {
		t.Fatalf("the branch again: %+v", r)
	}
	if r := askCore(c, late); !strings.Contains(r.Err, "no part of yours with the id "+first) || tb.Spinoff.Next[1].Prompt != "" {
		t.Fatalf("the late prompt was taken: %+v", r)
	}
	if r := askCore(c, promptOf(tb, "fix-a")); r.Err != "" || tb.Spinoff.Next[1].Prompt != "Do fix-a." {
		t.Fatalf("the new part's prompt: %+v", r)
	}
}

// A request to make a part's tab that comes after the user went on to
// another part of the same branch is refused: made, it would have the
// new part's prompt with the old part's repositories.
func TestSpinoffCreateOfAPartGone(t *testing.T) {
	m := testModel(t)
	c, tb := &m.home().Core, m.Cur()
	m.dialog = &closeTab{t: tb}
	askCore(c, planOf(tb, "fix-a"))
	old := tb.Spinoff.Next[0]
	askCore(c, planOf(tb, "fix-a"))
	c.SpinoffNext(tb, spinoff.Outcome{Part: old, Skipped: true})
	askCore(c, promptOf(tb, "fix-a"))
	old.Prompt = ""
	core.SpinoffCreate{T: c.Ref(tb), Part: old, Kind: "claude", Name: "fix-a"}.Do(c)
	if tb.SpinoffBusy != "" {
		t.Fatal("made with the part gone")
	}
}

// A plan core takes up after the agent stopped waiting for its answer is
// left: the agent has not the parts' ids to send their prompts by.
func TestSpinoffPlanTooLate(t *testing.T) {
	m := testModel(t)
	c, tb := &m.home().Core, m.Cur()
	m.dialog = &closeTab{t: tb}
	r := planOf(tb, "fix-a")
	r.Deadline = time.Now().Add(-time.Second)
	c.OnSpinoff(r)
	if tb.Spinoff != nil {
		t.Fatalf("taken: %+v", tb.Spinoff)
	}
}
