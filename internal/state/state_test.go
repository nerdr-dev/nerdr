package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLockIsExclusive(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	release, err := Lock(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(root); !errors.Is(err, ErrLocked) {
		t.Fatalf("second lock: err = %v, want ErrLocked", err)
	}
	release()
	again, err := Lock(root)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	again()
}

func TestBrokenSessionIsKept(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := "/x/develop"
	_ = os.MkdirAll(Dir(), 0o755)
	if err := os.WriteFile(Path(root), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(root)
	if err == nil || len(s.Tabs) != 0 {
		t.Fatalf("load: %+v, %v", s, err)
	}
	kept, _ := filepath.Glob(filepath.Join(Dir(), "develop-*.broken-*.json"))
	if len(kept) != 1 {
		t.Fatalf("kept = %v", kept)
	}
	if data, _ := os.ReadFile(kept[0]); string(data) != "{not json" {
		t.Fatalf("kept %q", data)
	}
}

func TestSnapshotsBeforeSave(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := "/x/develop"
	save := func(name string) {
		t.Helper()
		if err := Save(Session{Root: root, Tabs: []Tab{{ID: name, Name: name}}}); err != nil {
			t.Fatal(err)
		}
	}
	save("one") // nothing before it to keep
	snaps := func() []string {
		s, _ := filepath.Glob(filepath.Join(SnapshotDir(), "*.json"))
		return s
	}
	if n := len(snaps()); n != 0 {
		t.Fatalf("%d snapshots of nothing", n)
	}
	save("two")
	if n := len(snaps()); n != 1 {
		t.Fatalf("%d snapshots after the second save", n)
	}
	save("three") // too soon after the last snapshot
	if n := len(snaps()); n != 1 {
		t.Fatalf("%d snapshots after the third save", n)
	}
	data, _ := os.ReadFile(snaps()[0])
	if !strings.Contains(string(data), `"one"`) {
		t.Fatalf("snapshot holds %s", data)
	}
}

// TestSaveIsForTheUserOnly covers the session holding agents' session ids
// and what was said to them.
func TestSaveIsForTheUserOnly(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s := Session{Root: "/x/develop"}
	// A tmp a crash left behind, readable by all, must not lend its mode.
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(s.Root)+".tmp", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Save(s); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(Path(s.Root))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("session saved as %o", mode)
	}
}
