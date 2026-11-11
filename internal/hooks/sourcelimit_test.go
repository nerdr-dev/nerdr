package hooks

import (
	"strings"
	"testing"
)

func TestReportSourceLengthBound(t *testing.T) {
	s, got := listen(t)
	for _, method := range []string{ReportAgent, ReportAgentSession, ReleaseAgent} {
		err := Send(s.Path(), method, map[string]any{"pane_id": "test", "state": "idle", "source": strings.Repeat("x", MaxSource+1), "seq": 1})
		if err == nil || !strings.Contains(err.Error(), "source is too long") {
			t.Fatalf("%s: %v", method, err)
		}
	}
	select {
	case <-got:
		t.Fatal("oversized source reached core")
	default:
	}
	source := strings.Repeat("x", MaxSource)
	if err := Send(s.Path(), ReportAgentSession, map[string]any{"pane_id": "test", "source": source, "seq": 1}); err != nil {
		t.Fatal(err)
	}
	if r := next(t, got); r.Source != source {
		t.Fatal("valid source changed")
	}
}
