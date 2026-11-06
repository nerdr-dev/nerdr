package spawn

import (
	"os"
	"path/filepath"
	"testing"
)

// The path nerdr was started by is kept, a link to it among them, as long
// as it is the same file; on PATH, it is found there.
func TestStartedAs(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "Cellar", "1.0", "nerdr")
	other := filepath.Join(dir, "other")
	bin := filepath.Join(dir, "bin")
	for _, d := range []string{filepath.Dir(exe), bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{exe, other} {
		if err := os.WriteFile(f, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(bin, "nerdr")
	if err := os.Symlink(exe, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	for _, c := range []struct{ arg0, want string }{
		{link, link},     // by its path
		{"nerdr", link},  // found on PATH
		{exe, exe},       // the file itself
		{other, exe},     // another file: not this nerdr
		{"missing", exe}, // not on PATH
		{filepath.Join(bin, "gone"), exe},
	} {
		if got := startedAs(c.arg0, exe); got != c.want {
			t.Errorf("startedAs(%q) = %q, want %q", c.arg0, got, c.want)
		}
	}
}
