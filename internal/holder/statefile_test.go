package holder

import (
	"os"
	"path/filepath"
	"testing"
)

// The state an upgrade leaves, with the agents' secrets in it, is only
// ever named in a folder of the user's that a sandbox must deny, and not
// for longer than it takes to open it.
func TestTheStateFileIsNamedOnlyInTheGuardedFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	f, err := stateFileIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("folder: %v %v", fi, err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("left named: %v", ents)
	}
	if fi, err := f.Stat(); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("file: %v %v", fi, err)
	}
	if _, err := f.WriteString("state"); err != nil {
		t.Error(err)
	}

	// Not through a link put in the folder's place.
	open := filepath.Join(t.TempDir(), "open")
	if err := os.Mkdir(open, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(open, link); err != nil {
		t.Fatal(err)
	}
	if _, err := stateFileIn(link); err == nil {
		t.Error("a state file made through a link")
	}
}
