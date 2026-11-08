package core

import (
	"os"
	"testing"
)

// The holder is what nerdr runs over, unless NERDR_HOLDER is 0 or a
// program in a tab started it.
func TestUseHolder(t *testing.T) {
	for _, v := range []string{"NERDR", "NERDR_TAB", "NERDR_SHELL", EnvByAgent} {
		t.Setenv(v, "")
	}
	for v, want := range map[string]bool{"": true, "1": true, "yes": true, "0": false} {
		t.Setenv(EnvHolder, v)
		if v == "" {
			os.Unsetenv(EnvHolder)
		}
		if got := UseHolder(); got != want {
			t.Errorf("NERDR_HOLDER=%q: %v, want %v", v, got, want)
		}
	}
}

func TestInATab(t *testing.T) {
	for _, c := range []struct {
		name                          string
		nerdr, tab, shell, use, agent string
		in                            bool
	}{
		{"outside nerdr", "", "", "", "", "", false},
		{"an agent", "1", "k", "", "", "", true},
		{"an agent whose sandbox drops NERDR", "", "k", "", "", "", true},
		{"an agent whose sandbox drops NERDR_TAB", "1", "", "", "", "", true},
		{"a tab's terminal", "1", "k", "1", "", "", false},
		{"a terminal or plugin run an agent started", "1", "k", "1", "", "1", true},
		{"an agent given NERDR_HOLDER=1", "1", "k", "", "1", "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("NERDR", c.nerdr)
			t.Setenv("NERDR_TAB", c.tab)
			t.Setenv("NERDR_SHELL", c.shell)
			t.Setenv(EnvHolder, c.use)
			t.Setenv(EnvByAgent, c.agent)
			if got := InATab(); got != c.in {
				t.Errorf("InATab = %v, want %v", got, c.in)
			}
			if got := UseHolder(); got != !c.in {
				t.Errorf("UseHolder = %v, want %v", got, !c.in)
			}
		})
	}
}
