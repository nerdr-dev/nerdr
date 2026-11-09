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

// TestRemoteCommands covers a file or worktree on another machine opened
// in a window editor here that edits over ssh, and what is refused: an
// editor that cannot, which is to run there, and a path from the other
// machine that could be read as an option.
func TestRemoteCommands(t *testing.T) {
	t.Cleanup(func() { File, Worktree = "", "" })
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	File = "code"
	if got, ok := RemoteFileCommand("box", "/srv/wt", "/srv/wt/a.go", 7); !ok ||
		!reflect.DeepEqual(got.Argv, []string{"code", "--remote", "ssh-remote+box", "/srv/wt", "--goto", "/srv/wt/a.go:7"}) {
		t.Errorf("code: %v %v", got, ok)
	}
	File = "zed"
	if got, ok := RemoteFileCommand("me@box", "/srv/wt", "/srv/wt/a.go", 7); !ok ||
		!reflect.DeepEqual(got.Argv, []string{"zed", "ssh://me@box/srv/wt/a.go:7"}) {
		t.Errorf("zed: %v %v", got, ok)
	}
	for _, c := range []struct{ setting, host, path string }{
		{"nvim", "box", "/srv/wt/a.go"},       // runs on box instead
		{"subl", "box", "/srv/wt/a.go"},       // no ssh of its own
		{"code", "box", "-n"},                 // not a path
		{"code", "box", "a.go"},               // not absolute
		{"code", "-oProxyCommand=x", "/a.go"}, // not a host
		{"code", "box", "/a\n.go"},
	} {
		File = c.setting
		if got, ok := RemoteFileCommand(c.host, "/srv/wt", c.path, 0); ok {
			t.Errorf("%s on %s of %q: %v", c.setting, c.host, c.path, got)
		}
	}
	if got, ok := RemoteDirCommand("box", "/srv/wt"); !ok ||
		!reflect.DeepEqual(got.Argv, []string{"code", "--remote", "ssh-remote+box", "/srv/wt"}) {
		t.Errorf("the worktree: %v %v", got, ok)
	}
	Worktree = "nvim"
	if _, ok := RemoteDirCommand("box", "/srv/wt"); ok {
		t.Error("nvim opened the worktree from here")
	}
}

// TestFileCommandInGoesByTheEnvironmentGiven: where the file is on another
// machine, its $EDITOR is the one there.
func TestFileCommandInGoesByTheEnvironmentGiven(t *testing.T) {
	env := map[string]string{"EDITOR": "hx"}
	got := FileCommandIn("", func(k string) string { return env[k] }, "/wt", "/wt/a.go", 3)
	if !reflect.DeepEqual(got.Argv, []string{"hx", "+3", "/wt/a.go"}) {
		t.Errorf("got %v", got)
	}
}
