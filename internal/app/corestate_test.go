package app

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

// The TUI is to run in a process of its own, with a copy of core's state
// (core.Mirror): what it reads of core it may read, but what it changes of
// core it must ask core to (core.Request, by m.do). TestTUIChangesCoreByRequest
// holds it to that: no call of a method of core's that changes core, no
// change to what is core's, but what testdata/corestate.txt still lists,
// which only gets shorter.

var updateCoreState = flag.Bool("update-corestate", false, "write testdata/corestate.txt anew")

const corePkg = "github.com/nerdr-dev/nerdr/internal/core"

// readsCore are the methods of core's types that only read, which the TUI
// may call on its copy. Program's are left to the screens (see
// core.Program): a copy's send what the user does as requests. The
// ScreenClient's are the TUI's own copies of the screens, and CoreConn's
// its connection to a core in another process, which brings the copy up
// to date with core's own (Take, Run). SkillStates, and SkillOn and
// AgentNote by it, read what the agent finds of its skills, kept a moment
// in SkillLook: in a copy, the copy's own.
var readsCore = map[string]bool{}

func init() {
	for _, s := range strings.Fields(`
		*Core.TabIndex *Core.GroupTabs *Core.Groups *Core.Ref *Core.ByRef
		*Core.AgentName *Core.AgentLabel *Core.Live *Core.PaneName
		*Core.GithubEvery *Core.TabOnBranch *Core.SandboxedKind
		*Core.PluginUIOf *Core.GroupStatus *Core.Cold *Core.Waiting
		*Core.ReposOff *Core.TabLimitReached *Core.ShortDir *Core.ReposHold
		*Core.PaneRef *Core.PaneByRef *Core.Neediest *Core.CallRows
		*Core.TabOfDir *Core.ReviewKind *Core.PluginRunOf *Core.PaneMinW
		*Core.OtherRepos *Core.ColdTabs *Core.AllAgents *Core.WaitsForReady
		*Core.UsedModels *Core.Toast *Core.TabFor *Core.SpinoffKinds
		*Core.SearchRoots *Core.ReviewModel *Core.ReviewerNames
		*Core.KnownGroupNames *Core.GroupCount *Core.CutOff
		*Core.CountStatus *Core.Program *Core.InactiveToast *Core.MsgRefOf
		*Core.PRNewsByKey *Core.IssueNewsByKey *Core.Runs *Tab.HasProgram

		*Core.SkillStates *Core.SkillOn *Core.AgentNote
		*ScreenClient.Program *ScreenClient.Shown *ScreenClient.Drop
		*ScreenClient.Tell
		*CoreConn.Take *CoreConn.Started *CoreConn.Run *CoreConn.Send *CoreConn.Close *CoreConn.Reconnect
		Request.Do

		*Agent.AutoCount
		*Tab.AgentLit *Tab.AgentRunning *Tab.AgentsIn *Tab.Attention
		*Tab.ExtraAgents *Tab.FindMessage *Tab.FindingsQueued
		*Tab.FindingsWait *Tab.GoalText *Tab.GroupName *Tab.HasSandboxed
		*Tab.IdleUnasked *Tab.NextNum *Tab.OwnPane *Tab.Owner *Tab.PaneOf
		*Tab.Panes *Tab.ReadyHandoff *Tab.ReviewDoneStale *Tab.ReviewToTell
		*Tab.ReviewedBy *Tab.TaskAgents *Tab.TaskRefs *Tab.BeforeBrief
		*Tab.Lit
		*PaneTab.Agent *PaneTab.Status *PaneTab.Term
		*PluginRun.Lines *PluginRun.Running *PluginUI.Running
		CallRow.Total Overlap.Branches PREntry.Branch PanicMsg.String
		SkillUse.Totals Status.AtWork Status.Label TaskRef.List
		TabInfo.CanCommit TabInfo.CanOpenPR TabInfo.CanPush TabInfo.Ci
		TabInfo.Known TabInfo.Merged TabInfo.Prs TabInfo.Sums
	`) {
		readsCore[s] = true
	}
}

// makesNew are the methods of core's that give a slice or map made anew,
// not one of core's own.
var makesNew = map[string]bool{}

func init() {
	for _, s := range strings.Fields(`
		*Core.CallRows *Core.Groups *Core.GroupTabs *Core.Waiting
		*Core.ColdTabs *Core.AllAgents *Core.KnownGroupNames
		*Core.SearchRoots *Core.UsedModels *Core.SpinoffKinds
		*Core.OtherRepos *Tab.TaskRefs *Tab.TaskAgents *Tab.ExtraAgents
		*Tab.AgentsIn
	`) {
		makesNew[s] = true
	}
}

func TestTUIChangesCoreByRequest(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks the package")
	}
	got, err := coreChanges()
	if err != nil {
		t.Skip("cannot type-check the package here:", err)
	}
	const file = "testdata/corestate.txt"
	if *updateCoreState {
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
	t.Log("changes to core go by m.do(core.Request); when one listed is gone, run go test -run TestTUIChangesCoreByRequest -update-corestate")
}

func lines(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == '\n' })
}

// coreChanges are the calls of methods of core's that change core, and
// the changes to core's state, in the package's own files, as a count a
// line by file.
func coreChanges() (string, error) {
	fset, files, info, pkg, err := typeCheckPackage()
	if err != nil {
		return "", err
	}
	c := checker{info: info, pkg: pkg, count: map[string]int{}, alias: map[types.Object]bool{}, fresh: map[types.Object]bool{}}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool { c.aliases(n); return true })
	}
	for _, f := range files {
		file := filepath.Base(fset.Position(f.Pos()).Filename)
		ast.Inspect(f, func(n ast.Node) bool { c.node(file, n); return true })
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Body != nil {
				c.configTold(file, fd)
			}
		}
	}
	var ls []string
	for k, n := range c.count {
		ls = append(ls, fmt.Sprintf("%s %d", k, n))
	}
	sort.Strings(ls)
	return strings.Join(ls, "\n") + "\n", nil
}

type checker struct {
	info  *types.Info
	pkg   *types.Package
	count map[string]int
	// alias are the variables of the functions' own that point into
	// core's state: a slice, a map or a pointer taken from it.
	alias map[types.Object]bool
	// fresh are those that hold a value of core's type made there, from a
	// composite literal: the function's own until it hands it on.
	fresh map[types.Object]bool
}

// aliases marks the variables n sets to point into core's state.
func (c *checker) aliases(n ast.Node) {
	mark := func(lhs, rhs ast.Expr) {
		id, ok := lhs.(*ast.Ident)
		if !ok || rhs == nil {
			return
		}
		obj := c.info.Defs[id]
		if obj == nil {
			obj = c.info.Uses[id]
		}
		if obj == nil {
			return
		}
		if u, ok := rhs.(*ast.UnaryExpr); ok && u.Op == token.AND {
			if _, lit := u.X.(*ast.CompositeLit); lit || !c.throughCore(u.X) {
				c.fresh[obj] = true // made here, or the TUI's own
				return
			}
		}
		if _, ok := rhs.(*ast.CompositeLit); ok {
			c.fresh[obj] = true
			return
		}
		if c.pointsIntoCore(rhs) {
			c.alias[obj] = true
		}
	}
	switch x := n.(type) {
	case *ast.AssignStmt:
		if len(x.Lhs) == len(x.Rhs) {
			for i := range x.Lhs {
				mark(x.Lhs[i], x.Rhs[i])
			}
		}
	case *ast.ValueSpec:
		if len(x.Names) == len(x.Values) {
			for i := range x.Names {
				mark(x.Names[i], x.Values[i])
			}
		}
	}
}

// pointsIntoCore reports whether e is a slice, a map or a pointer into
// core's state: reached through it, or given by a method of core's.
func (c *checker) pointsIntoCore(e ast.Expr) bool {
	tv, ok := c.info.Types[e]
	if !ok || tv.Type == nil {
		return false
	}
	switch tv.Type.Underlying().(type) {
	case *types.Slice, *types.Map, *types.Pointer:
	default:
		return false
	}
	switch x := e.(type) {
	case *ast.UnaryExpr:
		return x.Op == token.AND && c.throughCore(x.X)
	case *ast.CallExpr:
		if se, ok := x.Fun.(*ast.SelectorExpr); ok {
			if sel := c.info.Selections[se]; sel != nil && sel.Kind() == types.MethodVal {
				fn := sel.Obj().(*types.Func)
				recv := fn.Type().(*types.Signature).Recv()
				if recv == nil || !isCore(recv.Type()) || isCore(tv.Type) {
					return false
				}
				name := types.TypeString(recv.Type(), func(*types.Package) string { return "" }) + "." + fn.Name()
				return !makesNew[name]
			}
		}
		return false
	}
	return c.throughCore(e) && !isCore(tv.Type)
}

func (c *checker) node(file string, n ast.Node) {
	switch x := n.(type) {
	case *ast.CallExpr:
		se, ok := x.Fun.(*ast.SelectorExpr)
		if !ok {
			return
		}
		sel := c.info.Selections[se]
		if sel == nil || sel.Kind() != types.MethodVal {
			return
		}
		fn := sel.Obj().(*types.Func)
		recv := fn.Type().(*types.Signature).Recv()
		if recv == nil || !isCore(recv.Type()) {
			return
		}
		name := types.TypeString(recv.Type(), func(*types.Package) string { return "" }) + "." + fn.Name()
		if strings.HasPrefix(name, "Program.") || readsCore[name] {
			return
		}
		c.count[file+": call "+name]++
	case *ast.AssignStmt:
		for _, l := range x.Lhs {
			c.write(file, l)
		}
	case *ast.IncDecStmt:
		c.write(file, x.X)
	case *ast.UnaryExpr:
		if x.Op == token.AND {
			c.write(file, x.X)
		}
	}
}

// clientFields are core.Core's fields that are the client's own in its
// copy: Gen counts its changes too, it reads the config itself (and has
// core read it again when it changes it), and it is the copy's client.
var clientFields = map[string]bool{"Gen": true, "Cfg": true, "Client": true}

// write counts e, written or taken the address of, when it is core's
// state: reached through core's types, and not a copy of its own.
func (c *checker) write(file string, e ast.Expr) {
	if c.clientsOwn(e) || c.fresh[c.root(e)] {
		return
	}
	if c.throughCore(e) && c.shared(e) {
		c.count[file+": write "+exprString(e)]++
	}
}

// configTold counts function fd when it changes the config but does not
// tell core so, by core.SetConfig.
func (c *checker) configTold(file string, fd *ast.FuncDecl) {
	changes, tells := false, false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			for _, l := range x.Lhs {
				changes = changes || c.inConfig(l)
			}
		case *ast.IncDecStmt:
			changes = changes || c.inConfig(x.X)
		case *ast.CompositeLit:
			if n, ok := types.Unalias(c.info.Types[x].Type).(*types.Named); ok &&
				n.Obj().Name() == "SetConfig" && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == corePkg {
				tells = true
			}
		}
		return true
	})
	if changes && !tells {
		c.count[file+": config changed, core not told: "+fd.Name.Name]++
	}
}

// inConfig reports whether e is the config of core.Core, or within it.
func (c *checker) inConfig(e ast.Expr) bool {
	for {
		switch x := e.(type) {
		case *ast.SelectorExpr:
			if sel := c.info.Selections[x]; sel != nil && sel.Kind() == types.FieldVal &&
				x.Sel.Name == "Cfg" && sel.Obj().Pkg() != nil && sel.Obj().Pkg().Path() == corePkg {
				return true
			}
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		default:
			return false
		}
	}
}

// root is the variable e is reached from, nil when none.
func (c *checker) root(e ast.Expr) types.Object {
	for {
		switch x := e.(type) {
		case *ast.SelectorExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		case *ast.Ident:
			return c.info.Uses[x]
		default:
			return nil
		}
	}
}

// clientsOwn reports whether e is within one of clientFields.
func (c *checker) clientsOwn(e ast.Expr) bool {
	for {
		switch x := e.(type) {
		case *ast.SelectorExpr:
			if sel := c.info.Selections[x]; sel != nil && sel.Kind() == types.FieldVal {
				if p := sel.Obj().Pkg(); p != nil && p.Path() == corePkg && clientFields[x.Sel.Name] {
					if _, ok := sel.Recv().Underlying().(*types.Pointer); ok || isCoreCore(sel.Recv()) {
						return true
					}
				}
			}
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		default:
			return false
		}
	}
}

// embedded is the type the field sel selects is promoted from, the first
// embedded field on its way.
func (c *checker) embedded(sel *types.Selection) types.Type {
	t := sel.Recv()
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t = p.Elem()
	}
	st, ok := t.Underlying().(*types.Struct)
	if !ok {
		return nil
	}
	return st.Field(sel.Index()[0]).Type()
}

func isCoreCore(t types.Type) bool {
	if t == nil {
		return false
	}
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Name() == "Core" && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == corePkg
}

func (c *checker) throughCore(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.Ident:
		obj := c.info.Uses[x]
		return obj != nil && c.alias[obj]
	case *ast.SelectorExpr:
		sel := c.info.Selections[x]
		if sel == nil || sel.Kind() != types.FieldVal {
			return false
		}
		if p := sel.Obj().Pkg(); p != nil && p.Path() == corePkg && len(sel.Index()) > 1 && isCoreCore(c.embedded(sel)) {
			return true // a field of core.Core's, in Model
		}
		return isCore(c.info.Types[x.X].Type) || c.throughCore(x.X)
	case *ast.IndexExpr:
		return c.throughCore(x.X)
	case *ast.StarExpr:
		return c.throughCore(x.X)
	case *ast.ParenExpr:
		return c.throughCore(x.X)
	}
	return false
}

// shared reports whether writing e changes something beyond a variable
// of the function's own: through a pointer, a slice or a map, or a
// variable of the package's.
func (c *checker) shared(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.SelectorExpr:
		if _, ok := c.info.Types[x.X].Type.Underlying().(*types.Pointer); ok {
			return true
		}
		return c.shared(x.X)
	case *ast.IndexExpr:
		switch c.info.Types[x.X].Type.Underlying().(type) {
		case *types.Slice, *types.Map, *types.Pointer:
			return true
		}
		return c.shared(x.X)
	case *ast.StarExpr:
		return true
	case *ast.ParenExpr:
		return c.shared(x.X)
	case *ast.Ident:
		obj := c.info.Uses[x]
		if obj == nil {
			obj = c.info.Defs[x]
		}
		v, ok := obj.(*types.Var)
		return !ok || v.Parent() == c.pkg.Scope()
	}
	return true
}

func isCore(t types.Type) bool {
	for {
		switch u := types.Unalias(t).(type) {
		case *types.Pointer:
			t = u.Elem()
			continue
		case *types.Named:
			return u.Obj().Pkg() != nil && u.Obj().Pkg().Path() == corePkg
		}
		return false
	}
}

func exprString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.SelectorExpr:
		return exprString(x.X) + "." + x.Sel.Name
	case *ast.IndexExpr:
		return exprString(x.X) + "[]"
	case *ast.StarExpr:
		return "*" + exprString(x.X)
	case *ast.ParenExpr:
		return exprString(x.X)
	case *ast.Ident:
		return x.Name
	case *ast.CallExpr:
		return exprString(x.Fun) + "()"
	}
	return "?"
}

// typeCheckPackage type-checks the package's own files, not its tests.
func typeCheckPackage() (*token.FileSet, []*ast.File, *types.Info, *types.Package, error) {
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
	pkg, err := (&types.Config{Importer: imp}).Check("app", fset, files, info)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return fset, files, info, pkg, nil
}
