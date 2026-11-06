package holder

import "os"

// stateFile is where an upgrade leaves the holder's state. macOS has no
// file in memory only, so it is a file in the user's own temporary
// folder (mode 0700 there), removed as soon as it is open: it has no name
// but for the moment between the two.
func stateFile() (*os.File, error) {
	f, err := os.CreateTemp("", "nerdr-holder-*")
	if err != nil {
		return nil, err
	}
	if err := os.Remove(f.Name()); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
