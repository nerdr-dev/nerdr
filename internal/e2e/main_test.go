//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nerdr/internal/agents"
)

// TestMain puts do-nothing notify-send and osascript first on PATH: the
// nerdr under test inherits the desktop session, and an agent it sees
// finish would otherwise pop up a real notification on the desktop of
// whoever runs the tests. It also leaves out what a nerdr or herdr the
// tests are run in (by an agent in a tab, say) gives its programs: the
// nerdr under test would take itself for one of their agents.
func TestMain(m *testing.M) {
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "NERDR" || strings.HasPrefix(k, "NERDR_") && !strings.HasPrefix(k, "NERDR_E2E_") || strings.HasPrefix(k, "HERDR_") {
			os.Unsetenv(k)
		}
	}
	dir, err := os.MkdirTemp("", "nerdr-e2e-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Every agent nerdr knows is "installed", as a program that does
	// nothing, ahead of any real one: the new-tab dialog then offers the
	// same agents wherever the tests run (with one alone it asks for none),
	// and no test starts a real agent by accident. The tests run the fake
	// agent by naming it in the config; TestRealClaude, the real claude.
	stubs := []string{"notify-send", "osascript"}
	for _, k := range agents.All {
		if k == agents.Claude && os.Getenv("NERDR_E2E_CLAUDE") != "" {
			continue
		}
		stubs = append(stubs, agents.DefaultSpec(k).Command[0])
	}
	for _, name := range stubs {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
