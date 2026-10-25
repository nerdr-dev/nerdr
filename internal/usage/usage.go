// Package usage reads how much of their accounts' rate limits the agents
// have used, and how full each agent's context window is, from what the
// agents themselves say: codex writes its limits into its conversation
// logs, and claude gives them to its status line, which nerdr sets to
// `nerdr hook statusline` (see StatusLine). nerdr never asks the agents'
// services itself, and never reads their sign-ins.
package usage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/nerdr-dev/nerdr/internal/denied"
)

// Window is one rate-limit window of an account: how much of it is used,
// in percent, and when it starts afresh.
type Window struct {
	Used     float64 `json:"used"`
	ResetsAt int64   `json:"resets_at"` // Unix seconds; 0 when not known
	Minutes  int     `json:"minutes,omitempty"`
}

// Resets is when w starts afresh, the zero time when not known.
func (w *Window) Resets() time.Time {
	if w == nil || w.ResetsAt <= 0 {
		return time.Time{}
	}
	return time.Unix(w.ResetsAt, 0)
}

// Report is what one agent says of its account and its context, as far as
// nerdr keeps it.
type Report struct {
	// Session is the short window (five hours), Weekly the long one.
	Session *Window `json:"session,omitempty"`
	Weekly  *Window `json:"weekly,omitempty"`
	// Context is how much of the agent's context window its conversation
	// fills, in percent; nil when not known.
	Context *float64 `json:"context,omitempty"`
	Model   string   `json:"model,omitempty"`
	Plan    string   `json:"plan,omitempty"`
}

// Checked is r with every percentage in 0–1000 and the text cut short:
// a report comes from whatever an agent ran.
func (r *Report) Checked() *Report {
	if r == nil {
		return nil
	}
	c := *r
	pct := func(f float64) float64 {
		if math.IsNaN(f) || f < 0 {
			return 0
		}
		return min(f, 1000)
	}
	win := func(w *Window) *Window {
		if w == nil {
			return nil
		}
		v := Window{Used: pct(w.Used), ResetsAt: max(w.ResetsAt, 0), Minutes: max(min(w.Minutes, 1<<20), 0)}
		return &v
	}
	c.Session, c.Weekly = win(r.Session), win(r.Weekly)
	if r.Context != nil {
		v := pct(*r.Context)
		c.Context = &v
	}
	c.Model, c.Plan = cut(r.Model, 60), cut(r.Plan, 30)
	return &c
}

func cut(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// statusInput is what claude gives its status line on stdin, as far as
// nerdr reads it.
type statusInput struct {
	Cwd       string `json:"cwd"`
	Workspace struct {
		CurrentDir string `json:"current_dir"`
		ProjectDir string `json:"project_dir"`
	} `json:"workspace"`
	Model struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	ContextWindow struct {
		UsedPercentage *float64 `json:"used_percentage"`
	} `json:"context_window"`
	RateLimits struct {
		FiveHour *claudeWindow `json:"five_hour"`
		SevenDay *claudeWindow `json:"seven_day"`
	} `json:"rate_limits"`
}

type claudeWindow struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       int64    `json:"resets_at"`
}

func (w *claudeWindow) window(minutes int) *Window {
	if w == nil || w.UsedPercentage == nil {
		return nil
	}
	return &Window{Used: *w.UsedPercentage, ResetsAt: w.ResetsAt, Minutes: minutes}
}

// FromStatusLine is the report in what claude gives its status line, and
// the folder it works in.
func FromStatusLine(b []byte) (*Report, string, error) {
	var in statusInput
	if err := json.Unmarshal(b, &in); err != nil {
		return nil, "", err
	}
	dir := in.Workspace.ProjectDir
	if dir == "" {
		dir = in.Cwd
	}
	r := &Report{
		Session: in.RateLimits.FiveHour.window(300),
		Weekly:  in.RateLimits.SevenDay.window(7 * 24 * 60),
		Context: in.ContextWindow.UsedPercentage,
		Model:   in.Model.DisplayName,
	}
	return r.Checked(), dir, nil
}

// Codex is the newest of what codex wrote of its account's limits in its
// logs under home (CODEX_HOME, ~/.codex), and when; ok is false when no
// log has them.
func Codex(home string) (r Report, at time.Time, ok bool) {
	files, _ := filepath.Glob(filepath.Join(home, "sessions", "*", "*", "*", "rollout-*.jsonl"))
	type file struct {
		path string
		mod  time.Time
	}
	var fs []file
	for _, f := range files {
		if st, err := os.Stat(f); err == nil {
			fs = append(fs, file{f, st.ModTime()})
		}
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].mod.After(fs[j].mod) })
	// The newest log that has them: one just started has none yet.
	for _, f := range fs[:min(len(fs), 8)] {
		if r, ok := CodexLog(f.path); ok && (r.Session != nil || r.Weekly != nil) {
			return r, f.mod, true
		}
	}
	return Report{}, time.Time{}, false
}

// codexEvent is a token_count event in a codex log.
type codexEvent struct {
	Type    string `json:"type"`
	Payload struct {
		Type string `json:"type"`
		Info *struct {
			Last struct {
				Input int64 `json:"input_tokens"`
			} `json:"last_token_usage"`
			Window int64 `json:"model_context_window"`
		} `json:"info"`
		RateLimits *struct {
			Primary   *codexWindow `json:"primary"`
			Secondary *codexWindow `json:"secondary"`
			Plan      string       `json:"plan_type"`
		} `json:"rate_limits"`
	} `json:"payload"`
}

type codexWindow struct {
	UsedPercent float64 `json:"used_percent"`
	Minutes     int     `json:"window_minutes"`
	ResetsAt    int64   `json:"resets_at"`
}

func (w *codexWindow) window() *Window {
	if w == nil {
		return nil
	}
	return &Window{Used: w.UsedPercent, ResetsAt: w.ResetsAt, Minutes: w.Minutes}
}

// CodexLog is the last of what the codex log at path says of the
// account's limits and of its conversation's context.
func CodexLog(path string) (Report, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Report{}, false
	}
	defer f.Close()
	var r Report
	found := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	for sc.Scan() {
		b := sc.Bytes()
		if !bytes.Contains(b, []byte(`"token_count"`)) {
			continue
		}
		var e codexEvent
		if json.Unmarshal(b, &e) != nil || e.Payload.Type != "token_count" {
			continue
		}
		if i := e.Payload.Info; i != nil && i.Window > 0 {
			c := 100 * float64(i.Last.Input) / float64(i.Window)
			r.Context, found = &c, true
		}
		if l := e.Payload.RateLimits; l != nil && (l.Primary != nil || l.Secondary != nil) {
			r.Session, r.Weekly, r.Plan, found = l.Primary.window(), l.Secondary.window(), l.Plan, true
		}
	}
	return *r.Checked(), found
}

// Saved is claude's limits as kept between runs, and when they were
// told: an agent tells them only once it has had an answer from its
// account, so after a restart there are none until one is used.
type Saved struct {
	Report
	At time.Time `json:"at"`
}

// claudeFile is where Saved is kept, in nerdr's state folder dir.
func claudeFile(dir string) string { return filepath.Join(dir, "claude-limits.json") }

// SaveClaude keeps s in dir.
func SaveClaude(dir string, s Saved) {
	b, err := json.Marshal(s)
	if err != nil {
		return
	}
	_ = denied.Err(os.MkdirAll(dir, 0o700))
	_ = denied.Err(os.WriteFile(claudeFile(dir), b, 0o600))
}

// LoadClaude is what was kept in dir, ok false for nothing.
func LoadClaude(dir string) (Saved, bool) {
	var s Saved
	b, err := os.ReadFile(claudeFile(dir))
	if err != nil || json.Unmarshal(b, &s) != nil {
		return s, false
	}
	s.Report = *s.Report.Checked()
	return s, s.Session != nil || s.Weekly != nil
}
