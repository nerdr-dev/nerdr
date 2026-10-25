package hooks

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
