package hooks

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Each agent's hooks, while nerdr's core is down: the socket gone, or
// left by a core that died, with nobody listening. A hook must never hold
// its agent up, so each returns at once, and the command line the agent
// runs it by ignores how it ended; the status line still shows a line.
func TestHooksGoOnWhileTheCoreIsDown(t *testing.T) {
	dir, err := os.MkdirTemp("", "nh") // short, for the socket's path
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	dead := filepath.Join(dir, "dead.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: dead, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ln.SetUnlinkOnClose(false)
	ln.Close()
	if _, err := os.Stat(dead); err != nil {
		t.Fatal("the dead core's socket is not there:", err)
	}

	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("NERDR_TAB", "tab1")
	// This process stands for the agent, its parent for the nerdr that
	// started it.
	pid, parent := strconv.Itoa(os.Getpid()), strconv.Itoa(os.Getppid())
	t.Setenv("NERDR_AGY_HOOK", parent)
	t.Setenv("NERDR_GROK_HOOK", parent)
	hooks := []struct {
		name string
		run  func(stderr *bytes.Buffer) int
	}{
		{"claude", func(e *bytes.Buffer) int {
			return ClaudeCommand(strings.NewReader(`{"hook_event_name":"Stop","session_id":"s1","stop_hook_active":false}`), e)
		}},
		{"codex", func(e *bytes.Buffer) int {
			return CodexCommand(strings.NewReader(`{"session_id":"01a1","hook_event_name":"Stop","stop_hook_active":false}`), e)
		}},
		{"copilot", func(e *bytes.Buffer) int {
			return CopilotCommand([]string{"Stop"}, strings.NewReader(`{"hook_event_name":"Stop","session_id":"11c8","stop_hook_active":false}`), e)
		}},
		{"opencode", func(e *bytes.Buffer) int {
			return OpenCodeCommand(strings.NewReader(`{"type":"session.execution.succeeded","sessionID":"ses_1","seq":1}`), e)
		}},
		{"agy", func(e *bytes.Buffer) int {
			return AgyCommand([]string{"SessionStart", pid}, strings.NewReader(`{"conversationId":"c1"}`), e)
		}},
		{"grok", func(e *bytes.Buffer) int {
			return GrokCommand([]string{"SessionStart", pid}, strings.NewReader(`{"sessionId":"s1"}`), e)
		}},
		{"herdr's", func(e *bytes.Buffer) int {
			return Command([]string{"report-agent", "tab1", "--source", "herdr:qwen", "--agent", "qwen", "--state", "idle"}, e)
		}},
	}
	for _, sock := range []string{filepath.Join(dir, "none.sock"), dead} {
		t.Setenv("NERDR_SOCKET", sock)
		t.Setenv("HERDR_SOCKET_PATH", sock)
		for _, h := range hooks {
			var stderr bytes.Buffer
			start := time.Now()
			code := h.run(&stderr)
			if took := time.Since(start); took > time.Second {
				t.Errorf("%s, %s: took %v", h.name, filepath.Base(sock), took)
			}
			// It tried, and could not: else this tells nothing.
			if code == 0 || !strings.Contains(stderr.String(), filepath.Base(sock)) {
				t.Errorf("%s, %s: exit %d, %q: did not try the socket", h.name, filepath.Base(sock), code, stderr.String())
			}
		}
		var out bytes.Buffer
		start := time.Now()
		in := strings.ReplaceAll(statusIn, "%s", dir)
		if code := StatusLineCommand(strings.NewReader(in), &out, os.Stderr); code != 0 || !strings.Contains(out.String(), "Opus") {
			t.Errorf("status line, %s: exit %d, %q", filepath.Base(sock), code, out.String())
		}
		if took := time.Since(start); took > time.Second {
			t.Errorf("status line, %s: took %v", filepath.Base(sock), took)
		}
	}

	// How each agent runs them: whatever the hook's exit, the agent is
	// told it went well.
	lines := map[string]string{"claude": ClaudeSettings(false), "codex": codexHookCommand}
	for name, s := range lines {
		if n := strings.Count(s, " hook "+name); n == 0 || strings.Count(s, " hook "+name+` >/dev/null 2>&1 || true`)+
			strings.Count(s, " hook "+name+` \u003e/dev/null 2\u003e\u00261 || true`) != n {
			t.Errorf("%s runs its hook as %s", name, s)
		}
	}
}
