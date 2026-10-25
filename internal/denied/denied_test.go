package denied

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func reset() {
	mu.Lock()
	kept = map[[2]string]*Denial{}
	mu.Unlock()
}

// A file nerdr may not write is kept by its path, once however often.
func TestErr(t *testing.T) {
	reset()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	file := filepath.Join(dir, "x")
	before := Noted()
	for range 2 {
		if Err(os.WriteFile(file, nil, 0o600)) == nil {
			t.Skip("can write to a read-only folder (root?)")
		}
	}
	ds := List()
	if len(ds) != 1 || ds[0].N != 2 || !strings.Contains(ds[0].What, file) || ds[0].Why != "permission denied" {
		t.Fatalf("kept %+v", ds)
	}
	if Noted() != before+1 {
		t.Errorf("Noted went from %d to %d for one new denial", before, Noted())
	}
	if Err(os.ErrNotExist) == nil || len(List()) != 1 {
		t.Errorf("a missing file was kept as a denial")
	}
}

func TestLine(t *testing.T) {
	for in, want := range map[string]string{
		"fatal: x\nBLOCKED by sandbox: git push --force\n": "BLOCKED by sandbox: git push --force",
		"git@github.com: Permission denied (publickey).":   "git@github.com: Permission denied (publickey).",
		`Post "https://api.github.com/graphql": Forbidden`: `Post "https://api.github.com/graphql": Forbidden`,
		"fatal: not a git repository":                      "",
	} {
		got, ok := Line(in)
		if got != want || ok != (want != "") {
			t.Errorf("Line(%q) = %q, %v", in, got, ok)
		}
	}
}

// Past most, the denial seen longest ago goes.
func TestMost(t *testing.T) {
	reset()
	for i := range most + 1 {
		Note("try", strings.Repeat("x", i+1))
	}
	ds := List()
	if len(ds) != most || ds[len(ds)-1].Why != "xx" {
		t.Fatalf("kept %d, the oldest %q", len(ds), ds[len(ds)-1].Why)
	}
}
