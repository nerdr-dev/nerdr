package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/gitsafe"
)

// An origin-pinned checkout does not authenticate the ignored local files
// copied into it. Keep copying available, but do not run host setup on
// those files after an agent could have changed them in a sandbox.
func TestPinnedSetupWithCopiedSandboxFiles(t *testing.T) {
	for _, tc := range []struct {
		name      string
		sandboxed bool
		copy      bool
	}{
		{"sandboxed with copy", true, true},
		{"sandboxed without copy", true, false},
		{"trusted with copy", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _, marks, allowed := pinRepo(t)
			config, err := LoadRepoConfig(a.Path)
			if err != nil {
				t.Fatal(err)
			}
			if tc.copy {
				config.Copy = []string{".env"}
				f, err := os.OpenFile(filepath.Join(a.Path, RepoConfigFile), os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.WriteString("copy = ['.env']\n")
				closeErr := f.Close()
				if err != nil || closeErr != nil {
					t.Fatalf("write config: %v, close: %v", err, closeErr)
				}
			}
			if err := Allow(allowed, a.Path, config); err != nil {
				t.Fatal(err)
			}
			// Pin and create the real checkout before sandbox history, since
			// subsequent host transport is denied. Prepare must still account
			// for sandbox changes to copied files after that verified checkout.
			plan, err := MakePlan(filepath.Join(filepath.Dir(a.Path), "worktrees"), "feat", []Repo{a})
			if err != nil {
				t.Fatal(err)
			}
			plan.Sandboxed = tc.sandboxed
			if _, err := plan.Sync(); err != nil {
				t.Fatal(err)
			}
			wts, err := plan.Execute()
			if err != nil {
				t.Fatal(err)
			}
			if tc.sandboxed {
				gitsafe.Sandboxed(a.Path)
			}
			// Changed after approval: approval covers the config, not this file.
			if err := os.WriteFile(filepath.Join(a.Path, ".env"), []byte("LOCAL=changed\n"), 0600); err != nil {
				t.Fatal(err)
			}
			warnings := Prepare(wts, plan.SetupIn(), allowed, func(string) {})
			if len(wts) != 1 || plan.Items[0].Pinned == "" || !plan.SetupIn()[wts[0].Path] {
				t.Fatal("test requires a verified origin-pinned base eligible for setup")
			}
			blocked := tc.sandboxed && tc.copy
			if marked(marks, "ran") == blocked {
				t.Fatalf("setup ran = %v, want %v", marked(marks, "ran"), !blocked)
			}
			if blocked {
				if len(warnings) != 1 || !strings.Contains(warnings[0], "setup not run — copied files") {
					t.Fatalf("missing copied-file explanation: %v", warnings)
				}
			} else if len(warnings) != 0 {
				t.Fatalf("unexpected warnings: %v", warnings)
			}
			if tc.copy {
				b, err := os.ReadFile(filepath.Join(wts[0].Path, ".env"))
				if err != nil || string(b) != "LOCAL=changed\n" {
					t.Fatalf("copy should still work: %q, %v", b, err)
				}
			}
		})
	}
}
