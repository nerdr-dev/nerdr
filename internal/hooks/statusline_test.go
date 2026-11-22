package hooks

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const statusIn = `{"workspace":{"project_dir":"%s"},"model":{"display_name":"Opus"},"context_window":{"used_percentage":40},
	"rate_limits":{"five_hour":{"used_percentage":20,"resets_at":1}}}`

// TestStatusLineReportsAndShowsTheUsers: nerdr's status line reports the
// limits to the tab, and shows the user's own status line, given the same
// input, or nerdr's own without one.
func TestStatusLineReportsAndShowsTheUsers(t *testing.T) {
	srv, got := listen(t)
	t.Setenv("NERDR_SOCKET", srv.Path())
	t.Setenv("NERDR_TAB", "tab-1")
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	dir := t.TempDir()
	in := strings.ReplaceAll(statusIn, "%s", dir)

	var out bytes.Buffer
	if StatusLineCommand(strings.NewReader(in), &out, os.Stderr) != 0 {
		t.Fatal("exit status")
	}
	r := next(t, got)
	if r.Method != Usage || r.Pane != "tab-1" || r.Limits.Session.Used != 20 || *r.Limits.Context != 40 {
		t.Fatalf("report %+v", r)
	}
	if s := out.String(); !strings.Contains(s, "Opus · context 40% · 5h 20%") {
		t.Fatalf("nerdr's own line: %q", s)
	}

	settings := `{"statusLine":{"type":"command","command":"jq -r .model.display_name 2>/dev/null || sed -n 's/.*display_name\":\"\\([^\"]*\\).*/mine \\1/p'"}}`
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	StatusLineCommand(strings.NewReader(in), &out, os.Stderr)
	next(t, got)
	if s := strings.TrimSpace(out.String()); !strings.Contains(s, "Opus") || strings.Contains(s, "context") {
		t.Fatalf("the user's own line: %q", s)
	}
}

// TestStatusLineIgnoresTheProjectsSettings covers a repository whose
// .claude/settings.json or settings.local.json names a status line
// command: it comes with the repository, and must never run.
func TestStatusLineIgnoresTheProjectsSettings(t *testing.T) {
	t.Setenv("NERDR_SOCKET", "")
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	for _, name := range []string{"settings.json", "settings.local.json"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			mark := filepath.Join(t.TempDir(), "ran")
			if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
				t.Fatal(err)
			}
			settings := `{"statusLine":{"type":"command","command":"touch '` + mark + `'; echo pwned"}}`
			if err := os.WriteFile(filepath.Join(dir, ".claude", name), []byte(settings), 0o644); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			StatusLineCommand(strings.NewReader(strings.ReplaceAll(statusIn, "%s", dir)), &out, os.Stderr)
			if _, err := os.Stat(mark); err == nil || strings.Contains(out.String(), "pwned") {
				t.Fatalf("the project's %s status line ran: %q", name, out.String())
			}
			if !strings.Contains(out.String(), "Opus · context 40%") {
				t.Fatalf("not nerdr's own line: %q", out.String())
			}
		})
	}
}

// TestStatusLineRunsOnlyCommands covers a status line of another type than
// "command", or none: its command is not run.
func TestStatusLineRunsOnlyCommands(t *testing.T) {
	t.Setenv("NERDR_SOCKET", "")
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	mark := filepath.Join(t.TempDir(), "ran")
	for _, typ := range []string{`"static"`, `""`, `null`} {
		settings := `{"statusLine":{"type":` + typ + `,"command":"touch '` + mark + `'; echo ran"}}`
		if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(settings), 0o644); err != nil {
			t.Fatal(err)
		}
		if cmd := userStatusLine(); cmd != "" {
			t.Errorf("type %s: command %q", typ, cmd)
		}
		var out bytes.Buffer
		StatusLineCommand(strings.NewReader(strings.ReplaceAll(statusIn, "%s", t.TempDir())), &out, os.Stderr)
		if _, err := os.Stat(mark); err == nil {
			t.Fatalf("type %s: the command ran", typ)
		}
	}
}

// TestStatusLineSettingsAreReadSafely covers the user's settings file as
// a named pipe or a huge file (nerdr must neither hang nor read it all),
// and as a link the user made, which is followed.
func TestStatusLineSettingsAreReadSafely(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	file := filepath.Join(home, "settings.json")

	if err := syscall.Mkfifo(file, 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan string, 1)
	go func() { done <- userStatusLine() }()
	select {
	case cmd := <-done:
		if cmd != "" {
			t.Errorf("a named pipe gave %q", cmd)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("blocked on a named pipe")
	}

	_ = os.Remove(file)
	big := `{"statusLine":{"type":"command","command":"echo big"},"x":"` + strings.Repeat("a", maxSettingsBytes) + `"}`
	if err := os.WriteFile(file, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	if cmd := userStatusLine(); cmd != "" {
		t.Errorf("a file over the limit gave %q", cmd)
	}

	_ = os.Remove(file)
	real := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(real, []byte(`{"statusLine":{"type":"command","command":"echo mine"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, file); err != nil {
		t.Fatal(err)
	}
	if cmd := userStatusLine(); cmd != "echo mine" {
		t.Errorf("a linked settings file gave %q", cmd)
	}
}
