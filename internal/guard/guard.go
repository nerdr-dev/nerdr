// Package guard is for tests that hold a package's own code to a rule
// read off its types: what it may call, which only a list kept in its
// testdata may break, and that list only gets shorter.
package guard

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// TypeCheck type-checks the package in the working directory, its own
// files and not its tests, as a test of it runs there.
func TypeCheck() (*token.FileSet, []*ast.File, *types.Info, *types.Package, error) {
	out, err := exec.Command("go", "list", "-export", "-deps", "-json", ".").Output()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	exports := map[string]string{}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for {
		var p struct{ ImportPath, Export string }
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return nil, nil, nil, nil, err
		}
		exports[p.ImportPath] = p.Export
	}
	fset := token.NewFileSet()
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		return os.Open(exports[path])
	})
	names, _ := filepath.Glob("*.go")
	var files []*ast.File
	for _, n := range names {
		if strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, n, nil, 0)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		files = append(files, f)
	}
	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
		Uses:       map[*ast.Ident]types.Object{},
		Defs:       map[*ast.Ident]types.Object{},
	}
	pkg, err := (&types.Config{Importer: imp}).Check(files[0].Name.Name, fset, files, info)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return fset, files, info, pkg, nil
}

// DiskPkgs are the packages that read or write the disk, or the PATH.
var DiskPkgs = map[string]bool{
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

// DiskFuncs are the functions of other packages that read the disk.
var DiskFuncs = map[string]bool{}

// DiskPure are the functions and methods of DiskPkgs that neither read
// nor write the disk nor look on the PATH.
var DiskPure = map[string]bool{}

func init() {
	for _, s := range strings.Fields(`
		filepath.Abs filepath.EvalSymlinks filepath.Glob filepath.Walk
		filepath.WalkDir
		core.ReadPlugins core.ReadPluginFacts core.AllowPlugin
		core.DisallowPluginNow core.ReadPreview core.HomeDir
		core.CodexConfig core.Tracked
	`) {
		DiskFuncs[s] = true
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
		config.Merge config.ScopeOf config.Quote config.List config.Table
		config.RawText
		skills.All skills.Printable skills.Skill.Body
		skills.Skill.Description skills.Skill.Without
		fs.FileInfo.ModTime
	`) {
		DiskPure[s] = true
	}
}

// DiskCalls are the uses of DiskPkgs' functions and methods, and of
// DiskFuncs, in the files of the package in the working directory that
// in keeps, as a count a line by file.
func DiskCalls(in func(file string) bool) (string, error) {
	fset, _, info, _, err := TypeCheck()
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
		if !DiskPkgs[fn.Pkg().Path()] && !DiskFuncs[name] || DiskPure[name] {
			continue
		}
		file := filepath.Base(fset.Position(id.Pos()).Filename)
		if !in(file) {
			continue
		}
		count[file+": "+name]++
	}
	var ls []string
	for k, n := range count {
		ls = append(ls, fmt.Sprintf("%s %d", k, n))
	}
	sort.Strings(ls)
	return strings.Join(ls, "\n") + "\n", nil
}

// Compare is the lines of got that want lacks, as new, and those of want
// that got lacks, as gone (or fewer): what a guard's test reports when its
// list in testdata is no longer what the code has.
func Compare(got, want string) (added, gone []string) {
	gl, wl := lines(got), lines(want)
	for _, l := range gl {
		if !contains(wl, l) {
			added = append(added, l)
		}
	}
	for _, l := range wl {
		if !contains(gl, l) {
			gone = append(gone, l)
		}
	}
	return added, gone
}

func lines(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == '\n' })
}

func contains(ls []string, l string) bool {
	for _, x := range ls {
		if x == l {
			return true
		}
	}
	return false
}
