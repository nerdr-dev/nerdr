package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
)

// With install = "home", opencode's plugin goes in the config folder of
// the environment opencode starts with — BaseEnv, the last client's or
// nerdr's own, with the config's env for opencode over it — so that one each sets differently does not leave
// it where opencode does not look.
func TestOpenCodePluginHomeIsInTheAgentsEnvironment(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	agent := filepath.Join(bin, "agent")
	if err := os.WriteFile(agent, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		// nerdr's environment, and the config's env for opencode, of
		// folders under out; "" is unset, "-" set empty.
		nerdr, env map[string]string
		want       string
	}{
		{"OPENCODE_CONFIG_DIR of each", map[string]string{"OPENCODE_CONFIG_DIR": "a"}, map[string]string{"OPENCODE_CONFIG_DIR": "b"}, "b"},
		{"OPENCODE_CONFIG_DIR emptied", map[string]string{"OPENCODE_CONFIG_DIR": "a", "XDG_CONFIG_HOME": "x"}, map[string]string{"OPENCODE_CONFIG_DIR": "-"}, "x/opencode"},
		{"XDG_CONFIG_HOME of each", map[string]string{"XDG_CONFIG_HOME": "x"}, map[string]string{"XDG_CONFIG_HOME": "y"}, "y/opencode"},
		{"nerdr's OPENCODE_CONFIG_DIR over the env's XDG_CONFIG_HOME", map[string]string{"OPENCODE_CONFIG_DIR": "a"}, map[string]string{"XDG_CONFIG_HOME": "y"}, "a"},
		{"nerdr's alone", map[string]string{"XDG_CONFIG_HOME": "x"}, nil, "x/opencode"},
		// The agent's environment builds on the last client's, not the
		// core's own.
		{"from the client", map[string]string{"XDG_CONFIG_HOME": "x"}, nil, "c/opencode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, out := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			abs := func(v string) string {
				if v == "-" {
					return ""
				}
				return filepath.Join(out, v)
			}
			t.Setenv("OPENCODE_CONFIG_DIR", "")
			t.Setenv("XDG_CONFIG_HOME", "")
			for k, v := range tc.nerdr {
				t.Setenv(k, abs(v))
			}
			env := map[string]string{}
			for k, v := range tc.env {
				env[k] = abs(v)
			}
			c := &Core{}
			if tc.name == "from the client" {
				c.ClientEnv = []string{"XDG_CONFIG_HOME=" + abs("c")}
			}
			c.Cfg.Install = config.InstallHome
			c.Cfg.Agents = map[agents.Kind]agents.Spec{agents.OpenCode: {Command: []string{agent}, Env: env}}
			want := filepath.Join(out, tc.want, "plugins", "nerdr.js")
			if got := c.OpenCodePluginHome(home); got != want {
				t.Fatalf("plugin at %s, want %s", got, want)
			}
			c.applySkills(func(bool) (bool, error) { return false, nil })
			if _, err := os.Stat(want); err != nil {
				t.Fatalf("not installed there: %v", err)
			}
		})
	}
}
