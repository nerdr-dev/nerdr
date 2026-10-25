package awake

import (
	"os"
	"os/exec"
	"strconv"

	"github.com/nerdr-dev/nerdr/internal/proc"
)

// holder holds, on macOS, a caffeinate: -i keeps the machine from idle
// sleep, -d the display on, and -w ends it with nerdr.
type holder struct {
	cmd           *exec.Cmd
	sleep, screen bool
}

func (h *holder) hold(sleep, screen bool) error {
	if h.cmd != nil && sleep == h.sleep && screen == h.screen {
		return nil
	}
	if h.cmd != nil {
		_ = h.cmd.Process.Kill()
		_ = h.cmd.Wait()
		h.cmd = nil
	}
	h.sleep, h.screen = sleep, screen
	if !sleep {
		return nil
	}
	args := []string{"-i", "-w", strconv.Itoa(os.Getpid())}
	if screen {
		args = append(args, "-d")
	}
	cmd := proc.Detach(exec.Command("caffeinate", args...))
	if err := cmd.Start(); err != nil {
		return err
	}
	h.cmd = cmd
	return nil
}
