package core

import (
	"path/filepath"

	"github.com/nerdr-dev/nerdr/internal/state"
)

// setupAllowedFile is where the .nerdr.toml files the user allowed are
// kept, beside the sessions.
func SetupAllowedFile() string {
	return filepath.Join(filepath.Dir(state.Dir()), "setup-allowed.json")
}
