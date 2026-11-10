package app

import (
	"flag"
	"os"
	"slices"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/guard"
)

// The TUI may run on another machine than core, and the files it shows,
// so the disk and the PATH are core's to read, not the TUI's: it asks
// core (core.Request) and shows what comes back, in its copy of core's
// state or as an answer. TestTUILeavesTheDiskToCore holds it to that: no
// use of a function or method of the packages that read or write the
// disk or look on the PATH, nor of the few such functions of others, but
// what testdata/diskcalls.txt still lists, which only gets shorter: the
// client's own config and terminal, kept on the client until core has
// them too. Their types the TUI may use.

var updateDiskCalls = flag.Bool("update-diskcalls", false, "write testdata/diskcalls.txt anew")

func TestTUILeavesTheDiskToCore(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks the package")
	}
	got, err := diskCalls()
	if err != nil {
		t.Skip("cannot type-check the package here:", err)
	}
	const file = "testdata/diskcalls.txt"
	if *updateDiskCalls {
		if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	b, _ := os.ReadFile(file)
	want := string(b)
	if got == want {
		return
	}
	gl, wl := lines(got), lines(want)
	for _, l := range gl {
		if !slices.Contains(wl, l) {
			t.Errorf("new: %s", l)
		}
	}
	for _, l := range wl {
		if !slices.Contains(gl, l) {
			t.Errorf("gone, or fewer: %s", l)
		}
	}
	t.Log("the disk and the PATH are core's to read, by m.do(core.Request) or m.tellCore; when one listed is gone, run go test -run TestTUILeavesTheDiskToCore -update-diskcalls")
}

// diskCalls are the uses of the functions and methods that read or write
// the disk or look on the PATH (guard.DiskPkgs, guard.DiskFuncs) in the
// package's own files, as a count a line by file.
func diskCalls() (string, error) { return guard.DiskCalls(func(string) bool { return true }) }
