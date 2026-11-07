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

// TestEveryRequestRegistered: every type with core's Do is a request that
// can go as JSON, and comes back the same.
func TestEveryRequestRegistered(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range pkgs["core"].Files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || fd.Name.Name != "Do" || len(fd.Recv.List) != 1 {
				continue
			}
			id, ok := fd.Recv.List[0].Type.(*ast.Ident)
			if !ok {
				t.Errorf("Do on a pointer at %s: a request goes by value", fset.Position(fd.Pos()))
				continue
			}
			names = append(names, id.Name)
		}
	}
	if len(names) != len(requestTypes) {
		t.Errorf("%d types with Do, %d registered", len(names), len(requestTypes))
	}
	for _, n := range names {
		ty := requestTypes[n]
		if ty == nil {
			t.Errorf("%s is not registered", n)
			continue
		}
		r := reflect.New(ty).Elem().Interface().(Request)
		b, err := EncodeRequest(r)
		if err != nil {
			t.Errorf("%s: %v", n, err)
			continue
		}
		back, err := DecodeRequest(b)
		if err != nil || !reflect.DeepEqual(back, r) {
			t.Errorf("%s: back as %#v, %v", n, back, err)
		}
	}
	if _, err := DecodeRequest([]byte(`{"op":"Nope","req":{}}`)); err == nil {
		t.Error("an unknown request was taken")
	}
}
