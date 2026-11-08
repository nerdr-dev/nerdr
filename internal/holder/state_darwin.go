package holder

import (
	"os"

	"github.com/nerdr-dev/nerdr/internal/rundir"
)

// stateFile is where an upgrade leaves the holder's state. macOS has no
// file in memory only, so it is a file in the client sockets' folder,
// which a sandbox must deny (see rundir), never the temporary folder,
// which sandboxes tend to allow; removed as soon as it is open.
func stateFile() (*os.File, error) {
	dir, err := rundir.Dir()
	if err != nil {
		return nil, err
	}
	return stateFileIn(dir)
}
