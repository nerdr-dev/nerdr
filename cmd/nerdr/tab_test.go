package main

import (
	"fmt"
	"strings"
	"syscall"
	"testing"
)

// TestExplainSandboxAsksToRetryOutside: when a sandbox keeps the command
// from the socket, the agent is told to run it again outside its own
// tools' sandbox before it gives up, and still that another nerdr will
// not help.
func TestExplainSandboxAsksToRetryOutside(t *testing.T) {
	err := explain("/run/nerdr/hooks.sock", fmt.Errorf("dial unix: connect: %w", syscall.EPERM))
	msg := err.Error()
	for _, want := range []string{"run the same command again", "outside that sandbox", "allow connecting to /run/nerdr/hooks.sock", "Another nerdr program"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
}
