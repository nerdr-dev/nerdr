package main

import (
	"os"
	"syscall"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/holder"
	"github.com/nerdr-dev/nerdr/internal/spawn"
)

// overHolder, with NERDR_HOLDER=1 and no holder yet, has nerdr run over
// one: this process becomes `nerdr holder -- nerdr ARGS`, which starts
// nerdr again as its core, and starts it again if it crashes, the
// programs of the tabs living on in the holder. It returns only when it
// cannot.
func overHolder() error {
	if os.Getenv(core.EnvHolder) != "1" || os.Getenv(holder.EnvFD) != "" {
		return nil
	}
	bin, err := spawn.Bin()
	if err != nil {
		return err
	}
	argv := append([]string{bin, "holder", "--", bin}, os.Args[1:]...)
	return syscall.Exec(bin, argv, os.Environ())
}

// held is the holder that started this nerdr as its core, nil when none
// did.
func held() (*core.Held, error) {
	if os.Getenv(holder.EnvFD) == "" {
		return nil, nil
	}
	cl, err := holder.FromEnv()
	if err != nil {
		return nil, err
	}
	return core.NewHeld(cl)
}
