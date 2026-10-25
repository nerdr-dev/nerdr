package editor

import (
	"reflect"
	"testing"
)

// TestFileCommandPassesTheLineTheWayEachEditorTakesIt covers which editor
// a file opens in (the setting, else $VISUAL, $EDITOR, vim) and how it is
// told the line: VS Code wants --goto and the worktree, so the file lands
// in the worktree's window; a window editor is started beside nerdr.
func TestFileCommandPassesTheLineTheWayEachEditorTakesIt(t *testing.T) {
	t.Cleanup(func() { File = "" })
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	cases := []struct {
		setting, visual string
		line            int
		want            Command
	}{
		{"", "", 7, Command{Argv: []string{"vim", "+7", "/wt/a.go"}}},
		{"", "nvim -p", 0, Command{Argv: []string{"nvim", "-p", "/wt/a.go"}}},
		{"code", "nvim", 7, Command{Argv: []string{"code", "/wt", "--goto", "/wt/a.go:7"}, Window: true}},
		{"/usr/bin/cursor", "", 0, Command{Argv: []string{"/usr/bin/cursor", "/wt", "--goto", "/wt/a.go"}, Window: true}},
		{"zed", "", 7, Command{Argv: []string{"zed", "/wt/a.go:7"}, Window: true}},
	}
	for _, c := range cases {
		File = c.setting
		t.Setenv("VISUAL", c.visual)
		if got := FileCommand("/wt", "/wt/a.go", c.line); !reflect.DeepEqual(got, c.want) {
			t.Errorf("editor %q, $VISUAL %q: got %+v, want %+v", c.setting, c.visual, got, c.want)
		}
	}
}

// TestDirCommandDefaultsToVSCode covers the worktree opening in VS Code
// unless worktree_editor names another.
func TestDirCommandDefaultsToVSCode(t *testing.T) {
	t.Cleanup(func() { Worktree = "" })
	if got := DirCommand("/wt"); !reflect.DeepEqual(got, Command{Argv: []string{"code", "/wt"}, Window: true}) {
		t.Errorf("default: %+v", got)
	}
	Worktree = "nvim"
	if got := DirCommand("/wt"); !reflect.DeepEqual(got, Command{Argv: []string{"nvim", "/wt"}}) {
		t.Errorf("terminal editor: %+v", got)
	}
}
