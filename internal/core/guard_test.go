package core

import (
	"os/exec"
	"strings"
	"testing"
)

// TestCoreHasNoUserInterface: core must work with no client at all, so
// nothing it imports, however indirectly, may draw or read keys.
func TestCoreHasNoUserInterface(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		for _, ui := range []string{"bubbletea", "lipgloss", "bubbles"} {
			if strings.Contains(dep, ui) {
				t.Errorf("core depends on %s", dep)
			}
		}
		// The TUI and its review panel; reviewsource, what the panel
		// reads, is core's to read.
		for _, ui := range []string{"internal/app", "internal/review"} {
			if dep == "github.com/nerdr-dev/nerdr/"+ui || strings.HasPrefix(dep, "github.com/nerdr-dev/nerdr/"+ui+"/") {
				t.Errorf("core depends on %s", dep)
			}
		}
	}
}

func TestBatchLeavesOutNil(t *testing.T) {
	if Batch(nil, nil) != nil {
		t.Error("nothing to do is not nil")
	}
	one := func() Event { return 1 }
	if got := Batch(nil, one)(); got != 1 {
		t.Errorf("one left: %v", got)
	}
	if got, ok := Batch(one, nil, one)().(BatchEvent); !ok || len(got) != 2 {
		t.Errorf("two left: %v", got)
	}
}
