package core

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/term"
)

func TestHookSequenceSourcesAreBounded(t *testing.T) {
	a := &Tab{}
	a.Pane = &term.Pane{}
	a.HookID = "test"
	m := &Core{}
	m.Tabs = []*Tab{a}
	report := func(source string, seq int64) *Tab {
		return m.HookSession(hooks.Report{Pane: a.HookID, Source: source, Seq: seq})
	}
	if report(strings.Repeat("x", hooks.MaxSource+1), 1) != nil {
		t.Fatal("oversized source accepted")
	}
	for i := 0; i < maxHookSources; i++ {
		if report(fmt.Sprintf("source-%d", i), 1) == nil {
			t.Fatal("valid source refused")
		}
	}
	if report("overflow", 1) != nil {
		t.Fatal("unbounded sequence map")
	}
	if len(a.Hook.Seq) != maxHookSources {
		t.Fatalf("sources: %d", len(a.Hook.Seq))
	}
	if report("source-0", 2) == nil {
		t.Fatal("existing integration cannot advance")
	}
	if report("source-0", 2) != nil {
		t.Fatal("duplicate sequence accepted")
	}
}
