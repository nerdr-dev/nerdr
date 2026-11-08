package holder

import (
	"os"

	"github.com/nerdr-dev/nerdr/internal/rundir"
)

// stateFileIn is a state file in dir, a folder of this user's that nobody
// else can enter, mode 0600, its name removed as soon as it is open: it
// has none but for the moment between the two, and then only in dir. It
// holds the agents' secrets and screens.
func stateFileIn(dir string) (*os.File, error) {
	if err := rundir.Ensure(dir); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(dir, ".holder-state-*")
	if err != nil {
		return nil, err
	}
	if err := os.Remove(f.Name()); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
