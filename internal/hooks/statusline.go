package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"nerdr/internal/proc"
	"nerdr/internal/usage"
)

// claude gives its account's rate limits to its status line alone. So
// nerdr starts claude with a status line of its own, `nerdr hook
// statusline` (see ClaudeSettings): it reports the limits and the
// context window to the tab, then shows the status line the user has set
// in claude's settings, given the same input, or a short one of nerdr's
// when there is none.

// statusLineCommand is the status line nerdr gives claude.
const statusLineCommand = `"$NERDR_BIN" hook statusline 2>/dev/null`

// statusLineWait is the longest the user's own status line may take.
const statusLineWait = 5 * time.Second

// StatusLineCommand is `nerdr hook statusline`: reads what claude gives
// its status line on stdin, reports it to the tab, and writes the status
// line to show on stdout.
func StatusLineCommand(stdin io.Reader, stdout, stderr io.Writer) int {
	in, err := io.ReadAll(io.LimitReader(stdin, 4<<20))
	if err != nil {
		fmt.Fprintln(stderr, "nerdr hook statusline:", err)
		return 1
	}
	r, dir, err := usage.FromStatusLine(in)
	if err != nil {
		fmt.Fprintln(stderr, "nerdr hook statusline: no status on stdin:", err)
		return 1
	}
	if sock, pane := os.Getenv("NERDR_SOCKET"), os.Getenv("NERDR_TAB"); sock != "" && pane != "" {
		_ = Send(sock, Usage, map[string]any{"pane_id": pane, "limits": r})
	}
	if cmd := userStatusLine(dir); cmd != "" {
		if out, ok := runStatusLine(cmd, dir, in); ok {
			_, _ = stdout.Write(out)
			return 0
		}
	}
	fmt.Fprintln(stdout, ownStatusLine(r))
	return 0
}

// userStatusLine is the status line command set in claude's settings for
// dir — the project's local settings, its shared ones, then the user's —
// or "" for none. One that is nerdr's own is none: it would run itself.
func userStatusLine(dir string) string {
	var files []string
	if dir != "" {
		files = append(files,
			filepath.Join(dir, ".claude", "settings.local.json"),
			filepath.Join(dir, ".claude", "settings.json"))
	}
	home := os.Getenv("CLAUDE_CONFIG_DIR")
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(h, ".claude")
		}
	}
	if home != "" {
		files = append(files, filepath.Join(home, "settings.json"))
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var s struct {
			StatusLine *struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"statusLine"`
		}
		if json.Unmarshal(data, &s) != nil || s.StatusLine == nil {
			continue
		}
		cmd := strings.TrimSpace(s.StatusLine.Command)
		if strings.Contains(cmd, "hook statusline") {
			return ""
		}
		return cmd
	}
	return ""
}

// runStatusLine runs the user's status line cmd in dir, given in, as
// claude would, and returns what it wrote.
func runStatusLine(cmd, dir string, in []byte) ([]byte, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), statusLineWait)
	defer cancel()
	c := proc.Detach(exec.CommandContext(ctx, "sh", "-c", cmd))
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		c.Dir = dir
	}
	c.Stdin = bytes.NewReader(in)
	out, err := proc.Output(c)
	if err != nil && len(out) == 0 {
		return nil, false
	}
	return out, true
}

// ownStatusLine is nerdr's status line for one who has set none: the
// model, the context window and the account's limits, those known.
func ownStatusLine(r *usage.Report) string {
	var parts []string
	if r.Model != "" {
		parts = append(parts, r.Model)
	}
	if r.Context != nil {
		parts = append(parts, fmt.Sprintf("context %d%%", int(math.Round(*r.Context))))
	}
	if r.Session != nil {
		parts = append(parts, fmt.Sprintf("5h %d%%", int(math.Round(r.Session.Used))))
	}
	if r.Weekly != nil {
		parts = append(parts, fmt.Sprintf("week %d%%", int(math.Round(r.Weekly.Used))))
	}
	return strings.Join(parts, " · ")
}
