package app

import (
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/plugin"

	"github.com/charmbracelet/x/ansi"
)

// TestPluginFlowLines: the plugins pane draws what went from the agent to
// the plugin and back, the ways lit for a while after something went.
func TestPluginFlowLines(t *testing.T) {
	m := &Model{}
	p := plugin.Plugin{Name: "save-text"}
	now := time.Now()
	run := &core.PluginRun{Agent: "1:claude", Values: map[string]any{"text": "hi"}, Started: now}

	lines := m.flowLines(p, run, 100)
	if len(lines) != 2 {
		t.Fatalf("want two lines, got %q", lines)
	}
	in, back := ansi.Strip(lines[0]), ansi.Strip(lines[1])
	if !strings.Contains(in, "1:claude ━━▶ save-text") || !strings.Contains(in, "got 1 value") {
		t.Errorf("in: %q", in)
	}
	if !strings.Contains(back, "◀┄┄") || !strings.Contains(back, "nothing back yet") {
		t.Errorf("back before output: %q", back)
	}
	if !flowIn(run, now) || flowOut(run, now) {
		t.Error("only the way in should be lit as it starts")
	}

	run.Add("Wrote 3 bytes\n")
	run.Pumped, run.OutAt = 14, now
	if !flowOut(run, now) {
		t.Error("the way back should be lit when it prints")
	}
	run.Ended, run.Exit = now, 2
	back = ansi.Strip(m.flowLines(p, run, 100)[1])
	if !strings.Contains(back, "gave back 1 line, 14 bytes") || !strings.Contains(back, "exit 2") {
		t.Errorf("back once ended: %q", back)
	}
	later := now.Add(flowPulse + time.Second)
	if flowIn(run, later) || flowOut(run, later) {
		t.Error("the ways should go dark after the pulse")
	}
}
