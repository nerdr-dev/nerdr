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

// diskPkgs are the packages that read or write the disk, or the PATH.
var diskPkgs = map[string]bool{
	"os":        true,
	"io/ioutil": true,
	"io/fs":     true,
	"os/user":   true,
	"os/exec":   true,
	"syscall":   true,
	"github.com/nerdr-dev/nerdr/internal/agents":  true,
	"github.com/nerdr-dev/nerdr/skills":           true,
	"github.com/nerdr-dev/nerdr/internal/plugin":  true,
	"github.com/nerdr-dev/nerdr/internal/usage":   true,
	"github.com/nerdr-dev/nerdr/internal/config":  true,
	"github.com/nerdr-dev/nerdr/internal/hooks":   true,
	"github.com/nerdr-dev/nerdr/internal/store":   true,
	"github.com/nerdr-dev/nerdr/internal/prompts": true,
	"github.com/nerdr-dev/nerdr/internal/state":   true,
}

// diskFuncs are the functions of other packages that read the disk.
var diskFuncs = map[string]bool{}

// diskPure are the functions and methods of diskPkgs that neither read
// nor write the disk nor look on the PATH, which the TUI may use.
var diskPure = map[string]bool{}

func init() {
	for _, s := range strings.Fields(`
		filepath.Abs filepath.EvalSymlinks filepath.Glob filepath.Walk
		filepath.WalkDir
		core.ReadPlugins core.ReadPluginFacts core.AllowPlugin
		core.DisallowPluginNow core.ReadPreview core.HomeDir
		core.CodexConfig core.Tracked
	`) {
		diskFuncs[s] = true
	}
	for _, s := range strings.Fields(`
		agents.ContextDocumented agents.DefaultSpec agents.TakesModel
		agents.Words agents.Hit.Snippet
		plugin.Param.About plugin.Plugin.Command plugin.Plugin.Opens
		plugin.Plugin.Shows plugin.Plugin.Usage
		usage.*Window.Resets
		config.Config.DataDirSet config.Config.NotifyOn
		config.Config.PrefixByte config.Config.PrefixKey
		config.Config.SkillEnabled config.Config.WorktreesDirSet
		skills.All skills.Printable skills.Skill.Body
		skills.Skill.Description skills.Skill.Without
		fs.FileInfo.ModTime
	`) {
		diskPure[s] = true
	}
}

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

// diskCalls are the uses of diskPkgs' functions and methods, and of
// diskFuncs, in the package's own files, as a count a line by file.
func diskCalls() (string, error) {
	fset, _, info, _, err := typeCheckPackage()
	if err != nil {
		return "", err
	}
	count := map[string]int{}
	for id, obj := range info.Uses {
		fn, ok := obj.(*types.Func)
		if !ok || fn.Pkg() == nil {
			continue
		}
		name := fn.Pkg().Name() + "." + fn.Name()
		if recv := fn.Type().(*types.Signature).Recv(); recv != nil {
			name = fn.Pkg().Name() + "." + types.TypeString(recv.Type(), func(*types.Package) string { return "" }) + "." + fn.Name()
		}
		if !diskPkgs[fn.Pkg().Path()] && !diskFuncs[name] || diskPure[name] {
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
