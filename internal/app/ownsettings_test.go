package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestTUIWritesOnlyItsOwnSettings: core's own settings are written by
// core, in the config file where it runs (core.EditConfig), which may be
// another machine than the TUI's. The TUI writes its own settings alone,
// in saveSetting, which asks config.ScopeOf first.
func TestTUIWritesOnlyItsOwnSettings(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	writes := regexp.MustCompile(`config\.(Save\w+|SetKeys?)\(`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range writes.FindAllString(string(src), -1) {
			if f == "settings.go" && w == "config.SetKey(" {
				continue
			}
			t.Errorf("%s: %s…): ask core to write its settings (core.EditConfig), or write the TUI's own in saveSetting", f, w)
		}
	}
	src, _ := os.ReadFile("settings.go")
	if strings.Count(string(src), "config.SetKey(") != 1 {
		t.Error("settings.go writes the config file in more than saveSetting")
	}
}
