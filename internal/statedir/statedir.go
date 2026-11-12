// Package statedir is nerdr's state folder, $XDG_STATE_HOME/nerdr or
// ~/.local/state/nerdr, and the private temporary folder in it that the
// host side of nerdr makes its temporary Git folders in.
//
// Not os.TempDir: on macOS that is $TMPDIR, and sandboxes such as cplt let
// their agent read and write all of it, so an agent could change what is
// in a folder nerdr made there between two of nerdr's steps. A sandbox
// must deny writes to the state folder, and nerdr's sandbox check tries
// that.
package statedir

import (
	"os"
	"path/filepath"

	"github.com/nerdr-dev/nerdr/internal/rundir"
)

// Dir is nerdr's state folder: nerdr in $XDG_STATE_HOME, or
// ~/.local/state/nerdr without it. Tests move it by setting
// XDG_STATE_HOME.
func Dir() string {
	d := os.Getenv("XDG_STATE_HOME")
	if d == "" {
		home, _ := os.UserHomeDir()
		d = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(d, "nerdr")
}

// TempDir is the private temporary folder in the state folder. It may not
// be there yet: MkdirTemp makes it.
func TempDir() string {
	return filepath.Join(Dir(), "tmp")
}

// MkdirTemp is os.MkdirTemp in TempDir, which it first makes, of mode
// 0700, or checks is a folder of this user's that nobody else can enter
// (and not a link put in its place). The folder it makes is the caller's
// to remove.
func MkdirTemp(prefix string) (string, error) {
	root, err := EnsureTempDir()
	if err != nil {
		return "", err
	}
	return os.MkdirTemp(root, prefix)
}

// EnsureTempDir is TempDir, made, or checked, as MkdirTemp does.
func EnsureTempDir() (string, error) {
	root := TempDir()
	if err := rundir.Ensure(root); err != nil {
		return "", err
	}
	return root, nil
}
