package statedir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMkdirTempIsInTheStateFolder(t *testing.T) {
	state, tmp := t.TempDir(), t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("TMPDIR", tmp)
	dir, err := MkdirTemp("nerdr-objects-")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(dir) != filepath.Join(state, "nerdr", "tmp") || !strings.HasPrefix(filepath.Base(dir), "nerdr-objects-") {
		t.Fatalf("made %s", dir)
	}
	if strings.HasPrefix(dir, tmp) || strings.HasPrefix(dir, os.TempDir()) {
		t.Fatalf("%s is in the temporary folder", dir)
	}
	for _, d := range []string{TempDir(), dir} {
		fi, err := os.Stat(d)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o700 {
			t.Errorf("%s has mode %o", d, fi.Mode().Perm())
		}
	}
}

func TestMkdirTempTightensAnOpenRoot(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	if err := os.MkdirAll(TempDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(TempDir(), 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := MkdirTemp("nerdr-index-"); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(TempDir()); fi.Mode().Perm() != 0o700 {
		t.Errorf("root left with mode %o", fi.Mode().Perm())
	}
}

func TestMkdirTempRefusesALinkedRoot(t *testing.T) {
	state, elsewhere := t.TempDir(), t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, TempDir()); err != nil {
		t.Fatal(err)
	}
	if dir, err := MkdirTemp("nerdr-objects-"); err == nil {
		t.Fatalf("made %s through a link", dir)
	}
}
