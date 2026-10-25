package usage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFromStatusLine(t *testing.T) {
	r, dir, err := FromStatusLine([]byte(`{"cwd":"/w/a","workspace":{"project_dir":"/w/p"},"model":{"display_name":"Opus"},
		"context_window":{"used_percentage":12.5},
		"rate_limits":{"five_hour":{"used_percentage":23.5,"resets_at":1738425600},"seven_day":{"used_percentage":-4}}}`))
	if err != nil || dir != "/w/p" || r.Model != "Opus" || *r.Context != 12.5 ||
		r.Session.Used != 23.5 || r.Session.ResetsAt != 1738425600 || r.Session.Minutes != 300 || r.Weekly.Used != 0 {
		t.Fatalf("%+v %q %v", r, dir, err)
	}
	r, _, _ = FromStatusLine([]byte(`{"cwd":"/w/a"}`))
	if r.Session != nil || r.Weekly != nil || r.Context != nil {
		t.Fatalf("none known: %+v", r)
	}
}

func TestCodexLimits(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "sessions", "2026", "10", "17")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	log := `{"type":"session_meta","payload":{"cwd":"/w"}}
{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":50000},"model_context_window":200000},"rate_limits":{"primary":{"used_percent":20.0,"window_minutes":300,"resets_at":1792271751},"secondary":{"used_percent":3.0,"window_minutes":10080,"resets_at":1792858551},"plan_type":"plus"}}}
{"type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":null}}
`
	if err := os.WriteFile(filepath.Join(dir, "rollout-x.jsonl"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	r, _, ok := Codex(home)
	if !ok || r.Session.Used != 20 || r.Weekly.Minutes != 10080 || r.Plan != "plus" || *r.Context != 25 {
		t.Fatalf("%+v %v", r, ok)
	}
	if _, _, ok := Codex(t.TempDir()); ok {
		t.Fatal("no logs, no limits")
	}
}
