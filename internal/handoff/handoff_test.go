package handoff

import (
	"strings"
	"testing"
)

func TestPrompt(t *testing.T) {
	for name, c := range map[string]struct {
		in        Info
		want, not []string
	}{
		"handoff": {
			Info{From: "claude", To: "opencode", Summary: "THE SUMMARY", Transcript: "/n/handoff.md"},
			[]string{"from claude to you (opencode)", "none of claude's conversation", "THE SUMMARY", "/n/handoff.md", "wait for them"},
			[]string{"did not write"},
		},
		"no handoff": {
			Info{From: "claude", To: "codex", Transcript: "/n/handoff.md", TranscriptLen: 5000},
			[]string{"claude did not write a handoff", "/n/handoff.md (5 KB)", "Its end is where the work stands"},
			[]string{"wrote this handoff"},
		},
		"nothing to read": {
			Info{From: "copilot", To: "claude", TranscriptErr: "found no log", Resumed: true},
			[]string{"back in your own earlier conversation", "could not read its conversation either (found no log)", "git status"},
			nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := Prompt(c.in)
			if !strings.HasPrefix(got, "nerdr: ") {
				t.Errorf("not marked as nerdr's: %q", got)
			}
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("lacks %q:\n%s", w, got)
				}
			}
			for _, n := range c.not {
				if strings.Contains(got, n) {
					t.Errorf("has %q:\n%s", n, got)
				}
			}
		})
	}
}

func TestRequestSaysHowToSend(t *testing.T) {
	r := Request("claude", "codex")
	for _, w := range []string{"from you (claude) to codex", "nerdr-handoff", `"${NERDR_BIN:-nerdr}" handoff -`, "nerdr: the user is switching this tab's agent"} {
		if !strings.Contains(r, w) {
			t.Errorf("lacks %q:\n%s", w, r)
		}
	}
}
