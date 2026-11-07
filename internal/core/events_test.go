package core

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"reflect"
	"strings"
	"testing"
)

// TestEveryEventRegistered: every type core tells a client (by Tell) goes
// as JSON, and comes back the same.
func TestEveryEventRegistered(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	told := map[string]bool{}
	for _, f := range pkgs["core"].Files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Tell" {
				return true
			}
			switch a := call.Args[0].(type) {
			case *ast.CompositeLit:
				if id, ok := a.Type.(*ast.Ident); ok {
					told[id.Name] = true
				}
			case *ast.CallExpr: // a conversion: ToastMsg(s)
				if id, ok := a.Fun.(*ast.Ident); ok && strings.HasSuffix(id.Name, "Msg") {
					told[id.Name] = true
				}
			}
			return true
		})
	}
	if len(told) < 30 {
		t.Fatalf("only %d types told found", len(told))
	}
	for n := range told {
		if eventTypes[n] == nil {
			t.Errorf("%s is told but not registered", n)
		}
	}
	for n, ty := range eventTypes {
		ev := reflect.New(ty).Elem().Interface()
		b, err := EncodeEvent(ev)
		if err != nil {
			t.Errorf("%s: %v", n, err)
			continue
		}
		back, err := DecodeEvent(b)
		if err != nil || !reflect.DeepEqual(back, ev) {
			t.Errorf("%s: back as %#v, %v", n, back, err)
		}
	}
	b, err := EncodeEvent(PanicMsg{Value: "boom", Stack: []byte("stack")})
	if err != nil {
		t.Fatal(err)
	}
	if back, err := DecodeEvent(b); err != nil || back.(WirePanic).Value != "boom" {
		t.Fatalf("panic back as %#v, %v", back, err)
	}
}
