package main

import (
	"os"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/holder"
)

// held is the holder that started this nerdr as its core (nerdr core),
// nil when none did.
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
