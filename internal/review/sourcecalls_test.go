package review

import (
	"encoding/json"
	"flag"
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
	"slices"
	"sort"
	"strings"
	"testing"
)

// The panel may run on another machine than the repositories it shows, so
// it reads them through its Source (source.go) and nothing else.
// TestReviewReadsThroughSource holds it to that: outside source.go, no use
// of a function or method of the packages that run git and gh or read the
// disk, but the pure ones in readPure and what testdata/sourcecalls.txt
// still lists, which only gets shorter. Their types the panel may use.

var updateSourceCalls = flag.Bool("update-sourcecalls", false, "write testdata/sourcecalls.txt anew")

// readPkgs are the packages that run git, gh or other programs, or read
// the disk.
var readPkgs = map[string]bool{
	"github.com/nerdr-dev/nerdr/internal/analysis":    true,
	"github.com/nerdr-dev/nerdr/internal/codereview":  true,
	"github.com/nerdr-dev/nerdr/internal/git":         true,
	"github.com/nerdr-dev/nerdr/internal/github":      true,
	"github.com/nerdr-dev/nerdr/internal/gitsafe":     true,
	"github.com/nerdr-dev/nerdr/internal/order":       true,
	"github.com/nerdr-dev/nerdr/internal/store":       true,
	"github.com/nerdr-dev/nerdr/internal/walkthrough": true,
	"github.com/nerdr-dev/nerdr/internal/watch":       true,
	"github.com/nerdr-dev/nerdr/internal/why":         true,
	"github.com/nerdr-dev/nerdr/internal/workspace":   true,
	"os":      true,
	"os/exec": true,
}

// readPure are the functions and methods of readPkgs that neither run a
// program nor look at the disk, which the panel may use.
var readPure = map[string]bool{}

// readStays are the uses that stay with the panel wherever it runs:
// $EDITOR runs on the client's machine.
var readStays = map[string]bool{
	"model.go: exec.Command": true,
}

func init() {
	for _, s := range strings.Fields(`
		analysis.ParseSymbolDecl analysis.*Project.Same
		git.ParseDiff github.Failed
		order.Label order.Roles
		order.Index.Files order.Index.For order.Index.Len
		order.Index.Read order.Index.Summary
		why.Index.All why.Index.Len why.Index.Match why.Index.Read
		why.Index.With
	`) {
		readPure[s] = true
	}
}

func TestReviewReadsThroughSource(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks the package")
	}
	got, err := sourceCalls()
	if err != nil {
		t.Skip("cannot type-check the package here:", err)
	}
	const file = "testdata/sourcecalls.txt"
	if *updateSourceCalls {
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
	gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
	for _, l := range gl {
		if l != "" && !slices.Contains(wl, l) {
			t.Errorf("new: %s", l)
		}
	}
	for _, l := range wl {
		if l != "" && !slices.Contains(gl, l) {
			t.Errorf("gone, or fewer: %s", l)
		}
	}
	t.Log("the panel reads the repositories through m.src (source.go); when one listed is gone, run go test -run TestReviewReadsThroughSource -update-sourcecalls")
}

// sourceCalls are the uses of readPkgs' functions and methods in the
// package's own files but source.go, as a count a line by file.
func sourceCalls() (string, error) {
	out, err := exec.Command("go", "list", "-export", "-deps", "-json", ".").Output()
	if err != nil {
		return "", err
	}
	exports := map[string]string{}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for {
		var p struct{ ImportPath, Export string }
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return "", err
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
			return "", err
		}
		files = append(files, f)
	}
	info := &types.Info{Uses: map[*ast.Ident]types.Object{}}
	if _, err := (&types.Config{Importer: imp}).Check("review", fset, files, info); err != nil {
		return "", err
	}
	count := map[string]int{}
	for id, obj := range info.Uses {
		fn, ok := obj.(*types.Func)
		if !ok || fn.Pkg() == nil || !readPkgs[fn.Pkg().Path()] {
			continue
		}
		name := fn.Pkg().Name() + "." + fn.Name()
		if recv := fn.Type().(*types.Signature).Recv(); recv != nil {
			name = fn.Pkg().Name() + "." + types.TypeString(recv.Type(), func(*types.Package) string { return "" }) + "." + fn.Name()
		}
		base := filepath.Base(fset.Position(id.Pos()).Filename)
		if readPure[name] || base == "source.go" || readStays[base+": "+name] {
			continue
		}
		count[base+": "+name]++
	}
	var ls []string
	for k, n := range count {
		ls = append(ls, fmt.Sprintf("%s %d", k, n))
	}
	sort.Strings(ls)
	return strings.Join(ls, "\n") + "\n", nil
}
