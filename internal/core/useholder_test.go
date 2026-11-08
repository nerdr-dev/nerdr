package core

import (
	"os"
	"testing"
)

// The holder is what nerdr runs over, unless NERDR_HOLDER is 0.
func TestUseHolder(t *testing.T) {
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
