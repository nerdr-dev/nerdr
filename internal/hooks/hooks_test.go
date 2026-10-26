package hooks

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func listen(t *testing.T) (*Server, chan Report) {
	t.Helper()
	got := make(chan Report, 8)
	// Not in t.TempDir(): with the test's name in it, the path is longer
	// than a socket's may be on macOS (104 bytes).
	dir, err := os.MkdirTemp("", "nh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	s, err := Listen(filepath.Join(dir, "n.sock"), func(r Report) { got <- r })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, got
}

func next(t *testing.T, got chan Report) Report {
	t.Helper()
	select {
	case r := <-got:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("no report")
	}
	return Report{}
}

func TestReportsReachNerdr(t *testing.T) {
	s, got := listen(t)
	// What herdr's opencode plugin sends.
	err := Send(s.Path(), ReportAgent, map[string]any{
		"pane_id": "ab12", "source": "herdr:opencode", "agent": "opencode",
		"state": "working", "agent_session_id": "ses_1", "seq": 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	r := next(t, got)
	if r.Method != ReportAgent || r.Pane != "ab12" || r.State != "working" || r.SessionID != "ses_1" || r.Seq != 7 {
		t.Fatalf("report = %+v", r)
	}
}

func TestOtherMethodsAreRefused(t *testing.T) {
	s, got := listen(t)
	err := Send(s.Path(), "pane.split", map[string]any{"pane_id": "x"})
	if err == nil || !strings.Contains(err.Error(), "method_not_found") {
		t.Fatalf("err = %v", err)
	}
	if err := Send(s.Path(), ReportAgent, map[string]any{"pane_id": "x", "state": "sleeping"}); err == nil {
		t.Fatal("a bad state was taken")
	}
	if err := Send(s.Path(), ReportMetadata, map[string]any{"pane_id": "x", "title": "t"}); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-got:
		t.Fatalf("passed on %+v", r)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestCommandLikeHerdrs(t *testing.T) {
	s, got := listen(t)
	t.Setenv("HERDR_SOCKET_PATH", s.Path())
	var stderr bytes.Buffer
	code := Command([]string{"report-agent-session", "ab12", "--source", "herdr:qwen", "--agent=qwen",
		"--agent-session-id", "q-1", "--seq", "3", "--token", "a=b"}, &stderr)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	r := next(t, got)
	if r.Method != ReportAgentSession || r.Pane != "ab12" || r.Agent != "qwen" || r.SessionID != "q-1" || r.Seq != 3 {
		t.Fatalf("report = %+v", r)
	}
	if code := Command([]string{"split", "x"}, &stderr); code == 0 {
		t.Fatal("pane split accepted")
	}
}

// The words after "--" are the command that resumes the agent's
// conversation, as in herdr's guide for agent authors; one herdr would
// refuse is refused.
func TestCommandCarriesTheResumeCommand(t *testing.T) {
	s, got := listen(t)
	t.Setenv("HERDR_SOCKET_PATH", s.Path())
	var stderr bytes.Buffer
	code := Command([]string{"report-agent", "ab12", "--source", "my-agent", "--agent", "my-agent",
		"--state", "idle", "--agent-session-id", "s-1", "--", "my-agent", "--resume", "s-1", "--model", "m"}, &stderr)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	r := next(t, got)
	if want := []string{"my-agent", "--resume", "s-1", "--model", "m"}; !slices.Equal(r.ResumeArgv, want) ||
		r.SessionID != "s-1" || r.State != "idle" {
		t.Fatalf("report = %+v", r)
	}
	for _, argv := range [][]string{{"/bin/sh", "-c", "x"}, {"my-agent", "a\nb"}, {}} {
		err := Send(s.Path(), ReportAgent, map[string]any{"pane_id": "ab12", "state": "idle", "resume_argv": argv})
		if err == nil {
			t.Errorf("resume_argv %q taken", argv)
		}
	}
}

// A status is one line, and not too long for one.
func TestStatusIsChecked(t *testing.T) {
	s, got := listen(t)
	go func() {
		for r := range got {
			if r.Reply != nil {
				r.Reply <- Answer{Text: r.Text}
			}
		}
	}()
	answer, err := Request(s.Path(), Status, map[string]any{"pane_id": "x", "text": "tests  pass;\nready for review"})
	if err != nil || answer != "tests pass; ready for review" {
		t.Fatalf("answer %q, %v", answer, err)
	}
	if _, err := Request(s.Path(), Status, map[string]any{"pane_id": "x", "text": strings.Repeat("x", MaxStatus+1)}); err == nil {
		t.Error("a status too long was taken")
	}
	if _, err := Request(s.Path(), Status, map[string]any{"pane_id": "x", "text": "x", "clear": true}); err == nil {
		t.Error("clear with a text was taken")
	}
}

func TestSpinoffCarriesItsParts(t *testing.T) {
	s, got := listen(t)
	long := strings.Repeat("x", 100<<10) // a prompt longer than a hook's report
	commit := strings.Repeat("ab", 20)
	err := Send(s.Path(), Spinoff, map[string]any{"pane_id": "ab12", "parts": []map[string]any{
		{"branch": "fix/a", "summary": "A", "prompt": long, "commits": []string{commit}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	r := next(t, got)
	if r.Method != Spinoff || r.Pane != "ab12" || len(r.Spinoff) != 1 ||
		r.Spinoff[0].Branch != "fix/a" || len(r.Spinoff[0].Prompt) != len(long) || r.Spinoff[0].Commits[0] != commit {
		t.Fatalf("report = %+v", r.Spinoff)
	}
	if err := Send(s.Path(), Spinoff, map[string]any{"pane_id": "ab12"}); err == nil {
		t.Fatal("a spinoff with no parts was taken")
	}
}

func TestHandoffCarriesItsText(t *testing.T) {
	s, got := listen(t)
	if err := Send(s.Path(), Handoff, map[string]any{"pane_id": "ab12", "summary": "the work so far"}); err != nil {
		t.Fatal(err)
	}
	if r := next(t, got); r.Method != Handoff || r.Pane != "ab12" || r.Handoff != "the work so far" {
		t.Fatalf("report = %+v", r)
	}
	if err := Send(s.Path(), Handoff, map[string]any{"pane_id": "ab12", "summary": "  "}); err == nil {
		t.Fatal("an empty handoff was taken")
	}
}

// An agent can write to the socket itself, past what `nerdr spinoff` and
// `nerdr handoff` check, so the socket checks it again.
func TestWhatAgentsSendIsChecked(t *testing.T) {
	s, got := listen(t)
	for _, tc := range []struct {
		name   string
		method string
		params map[string]any
	}{
		{"a session id that is a flag", ReportAgentSession,
			map[string]any{"pane_id": "ab12", "agent_session_id": "--dangerously-skip-permissions"}},
		{"a session id with a space", ReportAgent,
			map[string]any{"pane_id": "ab12", "state": "idle", "agent_session_id": "x --yolo"}},
		{"a commit that is an option", Spinoff, map[string]any{"pane_id": "ab12", "parts": []map[string]any{
			{"branch": "fix/a", "summary": "A", "prompt": "p", "commits": []string{"--output=/tmp/x"}}}}},
		{"a short commit", Spinoff, map[string]any{"pane_id": "ab12", "parts": []map[string]any{
			{"branch": "fix/a", "summary": "A", "prompt": "p", "commits": []string{"HEAD"}}}}},
		{"a bad branch", Spinoff, map[string]any{"pane_id": "ab12", "parts": []map[string]any{
			{"branch": "-x\x1b[2J", "summary": "A", "prompt": "p"}}}},
		{"a long handoff", Handoff, map[string]any{"pane_id": "ab12", "summary": strings.Repeat("x", 65<<10)}},
	} {
		if err := Send(s.Path(), tc.method, tc.params); err == nil {
			t.Errorf("%s was taken", tc.name)
		}
	}
	select {
	case r := <-got:
		t.Fatalf("passed on %+v", r)
	case <-time.After(100 * time.Millisecond):
	}

	esc := "done \x1b]52;c;aGk=\x07\x1b[31m"
	if err := Send(s.Path(), ReportAgent, map[string]any{"pane_id": "ab12", "state": "idle", "message": esc}); err != nil {
		t.Fatal(err)
	}
	if r := next(t, got); strings.ContainsRune(r.Message, 0x1b) || strings.ContainsRune(r.Message, 0x07) {
		t.Fatalf("message = %q", r.Message)
	}
	if err := Send(s.Path(), Handoff, map[string]any{"pane_id": "ab12", "summary": esc}); err != nil {
		t.Fatal(err)
	}
	if r := next(t, got); strings.ContainsRune(r.Handoff, 0x1b) {
		t.Fatalf("handoff = %q", r.Handoff)
	}
}

func TestReviewIsChecked(t *testing.T) {
	s, got := listen(t)
	rev := map[string]any{"verdict": "changes", "summary": "one \x1b[31mthing",
		"findings": []map[string]any{{"file": "./a.go", "anchor": "x := 1", "severity": "High", "text": "wrong"}}}
	if err := Send(s.Path(), Review, map[string]any{"pane_id": "ab12", "review": rev}); err != nil {
		t.Fatal(err)
	}
	r := next(t, got)
	if r.Method != Review || r.Pane != "ab12" || r.Review == nil || len(r.Review.Findings) != 1 {
		t.Fatalf("report = %+v", r)
	}
	if f := r.Review.Findings[0]; f.File != "a.go" || f.Severity != "high" || strings.ContainsRune(r.Review.Summary, 0x1b) {
		t.Fatalf("review not cleaned: %+v", r.Review)
	}
	for _, bad := range []map[string]any{
		nil,
		{"verdict": "maybe", "summary": "x"},
		{"verdict": "changes", "summary": "x"},
		{"verdict": "approve", "summary": "x", "findings": []map[string]any{{"file": "/etc/passwd", "severity": "low", "text": "x"}}},
	} {
		p := map[string]any{"pane_id": "ab12"}
		if bad != nil {
			p["review"] = bad
		}
		if err := Send(s.Path(), Review, p); err == nil {
			t.Fatalf("taken: %v", bad)
		}
	}
}

func TestReadyCarriesItsFocus(t *testing.T) {
	s, got := listen(t)
	if err := Send(s.Path(), RequestReview, map[string]any{"pane_id": "ab12", "focus": " the retry\x07 loop "}); err != nil {
		t.Fatal(err)
	}
	if r := next(t, got); r.Method != RequestReview || r.Pane != "ab12" || r.Focus != "the retry loop" {
		t.Fatalf("report = %+v", r)
	}
	if err := Send(s.Path(), RequestReview, map[string]any{"pane_id": "ab12"}); err != nil {
		t.Fatalf("ready without a focus: %v", err)
	}
	if r := next(t, got); r.Method != RequestReview || r.Focus != "" {
		t.Fatalf("report = %+v", r)
	}
	if err := Send(s.Path(), RequestReview, map[string]any{"pane_id": "ab12", "focus": strings.Repeat("x", 3000)}); err == nil {
		t.Fatal("a focus over the limit was taken")
	}
}

// TestReadyNamesItsReviewer covers nerdr request-review --agent --model:
// both reach nerdr, and a model with no agent to be the model of is refused.
func TestReadyNamesItsReviewer(t *testing.T) {
	s, got := listen(t)
	if err := Send(s.Path(), RequestReview, map[string]any{"pane_id": "ab12", "kind": "Codex", "model": "gpt-6-astra"}); err != nil {
		t.Fatal(err)
	}
	if r := next(t, got); r.Kind != "codex" || r.Model != "gpt-6-astra" {
		t.Fatalf("report = %+v", r)
	}
	if err := Send(s.Path(), RequestReview, map[string]any{"pane_id": "ab12", "model": "gpt-6-astra"}); err == nil {
		t.Fatal("a model without its agent was taken")
	}
}

// A second nerdr does not take the socket from the first, which keeps
// answering; once the first is gone, the second can have it.
func TestListenTaken(t *testing.T) {
	s, got := listen(t)
	if _, err := Listen(s.Path(), func(Report) {}); !errors.Is(err, ErrTaken) {
		t.Fatalf("second Listen: %v, want ErrTaken", err)
	}
	if err := Send(s.Path(), ReportAgent, map[string]any{"pane_id": "p", "agent": "claude", "state": "idle"}); err != nil {
		t.Fatalf("first nerdr no longer answers: %v", err)
	}
	next(t, got)
	s.Close()
	s2, err := Listen(s.Path(), func(Report) {})
	if err != nil {
		t.Fatalf("Listen after Close: %v", err)
	}
	s2.Close()
}

// TestEveryMethodReachesNerdr sends each of nerdr's own methods over the
// socket, as its CLI does, and checks it is taken: a method with a
// handler but missing from what the socket takes (nerdr close was) is
// refused as herdr's before it is looked at.
func TestEveryMethodReachesNerdr(t *testing.T) {
	s, got := listen(t)
	go func() {
		for r := range got {
			if r.Reply != nil {
				r.Reply <- Answer{Text: "ok"}
			}
		}
	}()
	for _, m := range []string{Spinoff, Handoff, Review, RequestReview, Message, NewAgent, Usage, Query, Terminal, Close, NewTab, Status,
		ReportAgent, ReportAgentSession, ReleaseAgent, ReportMetadata} {
		_, err := Request(s.Path(), m, map[string]any{"pane_id": "x"})
		if err != nil && strings.Contains(err.Error(), "method_not_found") {
			t.Errorf("%s: %v", m, err)
		}
	}
	if _, err := Request(s.Path(), Close, map[string]any{"pane_id": "x", "to": 2}); err != nil {
		t.Errorf("nerdr close: %v", err)
	}
	if _, err := Request(s.Path(), "pane.split", map[string]any{"pane_id": "x"}); err == nil || !strings.Contains(err.Error(), "method_not_found") {
		t.Errorf("herdr's own method taken: %v", err)
	}
}

// TestSecondNerdrGetsASocketOfItsOwn: the socket taken, a nerdr started
// in another folder listens beside it, on one named for its folder.
func TestSecondNerdrGetsASocketOfItsOwn(t *testing.T) {
	first, _ := listen(t)
	if _, err := Listen(first.Path(), func(Report) {}); !errors.Is(err, ErrTaken) {
		t.Fatalf("second listen: %v", err)
	}
	own := ForRoot(first.Path(), "/somewhere/else")
	if own == first.Path() || filepath.Dir(own) != filepath.Dir(first.Path()) || own != ForRoot(first.Path(), "/somewhere/else") {
		t.Fatalf("own socket %s beside %s", own, first.Path())
	}
	s, err := Listen(own, func(Report) {})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := Request(own, "ping", map[string]any{}); err != nil {
		t.Fatal(err)
	}
}

// TestReportsSayWhichProcessSentThem: the kernel says which process is at
// the other end, and nerdr finds those it runs under.
func TestReportsSayWhichProcessSentThem(t *testing.T) {
	if !peerSupported {
		t.Skip("nerdr cannot ask the kernel here")
	}
	s, got := listen(t)
	if err := Send(s.Path(), ReportAgent, map[string]any{"pane_id": "p", "agent": "claude", "state": "idle"}); err != nil {
		t.Fatal(err)
	}
	r := next(t, got)
	if !r.Peer.Known || len(r.Peer.Chain) < 2 || r.Peer.Chain[0] != os.Getpid() || r.Peer.Chain[1] != os.Getppid() {
		t.Fatalf("peer %+v, want this process (%d) under its parent (%d)", r.Peer, os.Getpid(), os.Getppid())
	}
}
