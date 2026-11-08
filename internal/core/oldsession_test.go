package core

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/holder"
	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/state/statetest"
)

// TestACoreStartsFromOldSessions: a core over a holder, started in a
// folder whose session a released version saved, takes up its tabs as
// they were, the agents beside them and the messages waiting for each,
// and starts every agent carrying on its conversation.
func TestACoreStartsFromOldSessions(t *testing.T) {
	for _, f := range statetest.Old(t, "tabs") {
		t.Run(filepath.Base(f), func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			t.Setenv("HOME", t.TempDir()) // no conversations of the user's
			root := t.TempDir()
			statetest.Put(t, f, root)
			for _, wt := range []string{"fix-parser", "feat-x"} {
				if err := os.MkdirAll(filepath.Join(root, "worktrees", wt), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			sess, err := state.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			cl, err := holder.New().Connect()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cl.Shutdown() }) // and the agents with it
			held, err := NewHeld(cl)
			if err != nil {
				t.Fatal(err)
			}
			args := filepath.Join(root, "args")
			script := agentScript(t, "echo \"$@\" >> '"+args+"'; exec cat\n")
			c := &Core{Client: NoClient{}, Root: root, Wake: make(chan struct{}, 1), Held: held,
				Cfg: config.Config{Agents: map[agents.Kind]agents.Spec{agents.Claude: {Command: []string{script}}}}}
			askedHelp(c.Cfg.Agents)
			c.Mu.Lock()
			c.Restore(sess, time.Now())
			c.TakeUpHeld()
			c.Mu.Unlock()

			if len(c.Tabs) != 2 || c.Tabs[0].Name != "fix-parser" || c.Tabs[1].Name != "feat-x" {
				t.Fatalf("tabs %+v", c.Tabs)
			}
			fix := c.Tabs[0]
			var texts []string
			for _, m := range fix.Messages {
				texts = append(texts, m.Text)
			}
			if !slices.Equal(texts, []string{"then run the tests", "and push it"}) {
				t.Errorf("messages %q, the queue of old among them", texts)
			}
			beside := fix.ExtraAgents()
			if len(beside) != 1 || beside[0].Num != 2 || len(beside[0].Messages) != 1 || beside[0].Messages[0].Text != "look at the lexer" {
				t.Fatalf("the agent beside: %+v", beside)
			}
			if wt := fix.Worktrees; len(wt) != 1 || wt[0].Path != filepath.Join(root, "worktrees", "fix-parser") || wt[0].Branch != "fix-parser" {
				t.Errorf("worktrees %+v", wt)
			}

			l := NewLoop(c, nil)
			l.Start()
			defer l.Stop()
			l.Post(RequestEv{R: StartRestored{Front: c.Ref(c.Tabs[sess.Active])}})
			all := []*Tab{fix, beside[0], c.Tabs[1]}
			waitLocked(t, c, "every agent started", func() bool {
				for _, a := range all {
					if a.Pane == nil {
						return false
					}
				}
				return true
			})
			for _, id := range []string{"11111111-2222-3333-4444-555555555555", "66666666-7777-8888-9999-000000000000", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"} {
				waitFor(t, "carried on "+id, func() bool {
					b, _ := os.ReadFile(args)
					return strings.Contains(string(b), "--resume "+id)
				})
			}
		})
	}
}
