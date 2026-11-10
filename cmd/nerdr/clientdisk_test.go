package main

import (
	"flag"
	"os"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/guard"
)

// The client may run on another machine than core, so what it shows is
// core's to read, sent over the connection: the session, the layout, the
// repositories (see app's TestTUILeavesTheDiskToCore). TestClientLeavesTheDiskToCore
// holds client.go, which starts the client, to that: no use of a function
// that reads or writes the disk or looks on the PATH but what
// testdata/clientdisk.txt still lists, which only gets shorter: the
// client's own config and terminal, its own file of what it shows across
// the machines (state.Client), and starting a core on this machine when
// none runs.

var updateClientDisk = flag.Bool("update-clientdisk", false, "write testdata/clientdisk.txt anew")

func TestClientLeavesTheDiskToCore(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks the package")
	}
	got, err := guard.DiskCalls(func(file string) bool { return file == "client.go" })
	if err != nil {
		t.Skip("cannot type-check the package here:", err)
	}
	const file = "testdata/clientdisk.txt"
	if *updateClientDisk {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	b, _ := os.ReadFile(file)
	added, gone := guard.Compare(got, string(b))
	for _, l := range added {
		t.Errorf("new: %s", l)
	}
	for _, l := range gone {
		t.Errorf("gone, or fewer: %s", l)
	}
	if len(added)+len(gone) > 0 {
		t.Log("what the client shows is core's to read; when one listed is gone, run go test -run TestClientLeavesTheDiskToCore -update-clientdisk")
	}
}
