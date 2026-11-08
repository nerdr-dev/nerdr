// mksession writes, with a nerdr release's own state package, the session
// files the tests of later versions read: one as nerdr makes them, and one
// with every field set.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/nerdr-dev/nerdr/internal/state"
	"github.com/nerdr-dev/nerdr/internal/workspace"
)

func main() {
	out := os.Args[1]
	at := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	wt := func(name string) []workspace.Worktree {
		return []workspace.Worktree{{Repo: "/ROOT/repoA", Path: "/ROOT/worktrees/" + name, Branch: name}}
	}
	tabs := state.Session{
		Root: "/ROOT", Active: 1, Split: "feat-x", SideWidth: 30,
		GroupNames: map[string]string{},
		Tabs: []state.Tab{
			{
				ID: "fix-parser", Name: "fix-parser", Dir: "/ROOT/worktrees/fix-parser", Agent: "claude",
				SessionID: "11111111-2222-3333-4444-555555555555", Worktrees: wt("fix-parser"),
				Messages: []state.Message{{Text: "then run the tests", Auto: true}},
				Queue:    []string{"and push it"},
				Goal:     "Fix the parser", Last: "Fixed the off-by-one", Status: "Fixing the parser", StatusAt: at,
				LastActive: at, Num: 1,
				Agents: []state.Tab{{
					ID: "fix-parser-2", Name: "fix-parser", Dir: "/ROOT/worktrees/fix-parser", Agent: "claude",
					SessionID: "66666666-7777-8888-9999-000000000000", Worktrees: wt("fix-parser"), Num: 2,
					Messages: []state.Message{{Text: "look at the lexer", Auto: true, From: 1}},
				}},
			},
			{
				ID: "feat-x", Name: "feat-x", Dir: "/ROOT/worktrees/feat-x", Agent: "claude",
				SessionID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Worktrees: wt("feat-x"), LastActive: at,
			},
		},
	}
	var full state.Session
	fill(reflect.ValueOf(&full).Elem(), 0)
	full.Root = "/ROOT"
	for name, s := range map[string]state.Session{"tabs": tabs, "full": full} {
		dir, _ := os.MkdirTemp("", "mksession")
		os.Setenv("XDG_STATE_HOME", dir)
		if err := state.Save(s); err != nil {
			panic(err)
		}
		b, err := os.ReadFile(state.Path("/ROOT"))
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(filepath.Join(out, name+".json"), b, 0o644); err != nil {
			panic(err)
		}
		os.RemoveAll(dir)
	}
	fmt.Println("written")
}

// fill sets every field of v to a value that is not its zero.
func fill(v reflect.Value, depth int) {
	switch v.Kind() {
	case reflect.String:
		v.SetString("s-" + v.Type().Name())
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int64, reflect.Int32:
		if v.Type() == reflect.TypeOf(time.Duration(0)) {
			v.SetInt(int64(time.Minute))
		} else {
			v.SetInt(7)
		}
	case reflect.Float64:
		v.SetFloat(1.5)
	case reflect.Struct:
		if v.Type() == reflect.TypeOf(time.Time{}) {
			v.Set(reflect.ValueOf(time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)))
			return
		}
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				fill(v.Field(i), depth)
			}
		}
	case reflect.Pointer:
		p := reflect.New(v.Type().Elem())
		fill(p.Elem(), depth)
		v.Set(p)
	case reflect.Slice:
		if depth > 1 {
			return
		}
		s := reflect.MakeSlice(v.Type(), 1, 1)
		fill(s.Index(0), depth+1)
		v.Set(s)
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		k, e := reflect.New(v.Type().Key()).Elem(), reflect.New(v.Type().Elem()).Elem()
		fill(k, depth)
		if depth <= 1 {
			fill(e, depth+1)
		}
		m.SetMapIndex(k, e)
		v.Set(m)
	case reflect.Interface:
		v.Set(reflect.ValueOf("s-any"))
	}
}
