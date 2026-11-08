package app

import (
	"flag"
	"fmt"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// The TUI may run on another machine than the repositories it shows, so
// git and gh are core's to run, not the TUI's: it asks core (core.Request)
// and shows what comes back. TestTUILeavesGitToCore holds it to that: no
// use of a function or method of the packages that run them, nor of
// os/exec, but what testdata/gitcalls.txt still lists, which only gets
// shorter. Their types the TUI may use, as core's requests do.

var updateGitCalls = flag.Bool("update-gitcalls", false, "write testdata/gitcalls.txt anew")

// gitPkgs are the packages that run git and gh, or other programs.
var gitPkgs = map[string]bool{
	"github.com/nerdr-dev/nerdr/internal/workspace": true,
	"github.com/nerdr-dev/nerdr/internal/git":       true,
	"github.com/nerdr-dev/nerdr/internal/github":    true,
	"github.com/nerdr-dev/nerdr/internal/gitsafe":   true,
	"os/exec": true,
}

// gitPure are the functions and methods of gitPkgs that neither run a
// program nor look at the disk, which the TUI may use.
var gitPure = map[string]bool{}

func init() {
	for _, s := range strings.Fields(`
		github.Issue.BranchName github.Issue.Task github.RequestKey
		workspace.Plan.CanStartAnew workspace.Plan.Describe
		workspace.Plan.ExistingItems workspace.Plan.FromBase
		workspace.Plan.SetupIn workspace.PlanItem.Merged
		workspace.PlanItem.CanStartAnew
	`) {
		gitPure[s] = true
	}
}

func TestTUILeavesGitToCore(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks the package")
	}
	got, err := gitCalls()
	if err != nil {
		t.Skip("cannot type-check the package here:", err)
	}
	const file = "testdata/gitcalls.txt"
	if *updateGitCalls {
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
	t.Log("git and gh are core's to run, by m.do(core.Request); when one listed is gone, run go test -run TestTUILeavesGitToCore -update-gitcalls")
}

// gitCalls are the uses of gitPkgs' functions and methods in the
// package's own files, as a count a line by file.
func gitCalls() (string, error) {
	fset, _, info, _, err := typeCheckPackage()
	if err != nil {
		return "", err
	}
	count := map[string]int{}
	for id, obj := range info.Uses {
		fn, ok := obj.(*types.Func)
		if !ok || fn.Pkg() == nil || !gitPkgs[fn.Pkg().Path()] {
			continue
		}
		name := fn.Pkg().Name() + "." + fn.Name()
		if recv := fn.Type().(*types.Signature).Recv(); recv != nil {
			name = fn.Pkg().Name() + "." + types.TypeString(recv.Type(), func(*types.Package) string { return "" }) + "." + fn.Name()
		}
		if gitPure[name] {
			continue
		}
		count[filepath.Base(fset.Position(id.Pos()).Filename)+": "+name]++
	}
	var ls []string
	for k, n := range count {
		ls = append(ls, fmt.Sprintf("%s %d", k, n))
	}
	sort.Strings(ls)
	return strings.Join(ls, "\n") + "\n", nil
}
