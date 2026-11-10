package state

import "testing"

// TestClient: what a client keeps is read back for the same key, and not
// for another.
func TestClient(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if c := LoadClient("/w"); c.Key != "/w" || len(c.Off) != 0 {
		t.Fatalf("nothing yet: %+v", c)
	}
	want := Client{Key: "/w", Off: []string{"gpu"}, Active: MachineTab{Machine: "box", Tab: "1"}, SplitLeft: true}
	if err := SaveClient(want); err != nil {
		t.Fatal(err)
	}
	got := LoadClient("/w")
	if got.Key != "/w" || len(got.Off) != 1 || got.Off[0] != "gpu" || got.Active != want.Active || !got.SplitLeft {
		t.Fatalf("read %+v", got)
	}
	if c := LoadClient("box:/w"); len(c.Off) != 0 {
		t.Fatalf("another key's: %+v", c)
	}
}
