// Package editor opens a file, or a whole worktree, in the user's editor.
// A terminal editor (vim, nano, …) runs inside nerdr and has the screen
// until it exits; a window editor (VS Code, Zed, …) is started on its own
// and nerdr carries on.
package editor

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// File and Worktree are the editor and worktree_editor settings from the
// config: the command a file is opened in from the review panel, and the
// one a tab's worktree is opened in. Empty for the defaults; see
// FileCommand and DirCommand.
var File, Worktree string

// Command is a command line that opens something in an editor. Window is
// set for an editor with a window of its own, which is started apart from
// nerdr instead of in its terminal.
type Command struct {
	Argv   []string
	Window bool
}

// style is how an editor is told the line to open a file on.
type style int

const (
	plusLine style = iota // +12 file: vi, vim, neovim, nano, emacs, micro
	vscode                // dir --goto file:12, the folder opened with it
	colon                 // file:12
)

// windowEditors are the editors with a window of their own that nerdr
// knows, by program name, and how each takes a line.
var windowEditors = map[string]style{
	"code":          vscode,
	"code-insiders": vscode,
	"code-oss":      vscode,
	"codium":        vscode,
	"vscodium":      vscode,
	"cursor":        vscode,
	"windsurf":      vscode,
	"positron":      vscode,
	"zed":           colon,
	"zeditor":       colon,
	"subl":          colon,
}

func styleOf(argv []string) (style, bool) {
	s, ok := windowEditors[filepath.Base(argv[0])]
	return s, ok
}

// fileEditor is setting, then $VISUAL, then $EDITOR by getenv, then vim.
func fileEditor(setting string, getenv func(string) string) []string {
	for _, s := range []string{setting, getenv("VISUAL"), getenv("EDITOR")} {
		if f := strings.Fields(s); len(f) > 0 {
			return f
		}
	}
	return []string{"vim"}
}

// FileCommand opens path, a file in the worktree dir, on line (none when
// 0).
func FileCommand(dir, path string, line int) Command {
	return FileCommandIn(File, os.Getenv, dir, path, line)
}

// FileCommandIn is FileCommand with setting for the editor setting, and
// getenv for $VISUAL and $EDITOR: those where the file is, which may be
// another machine than nerdr's own.
func FileCommandIn(setting string, getenv func(string) string, dir, path string, line int) Command {
	argv := fileEditor(setting, getenv)
	s, window := styleOf(argv)
	switch {
	case s == vscode:
		// The worktree with it, so the file opens in the worktree's window
		// rather than whichever one was last used.
		target := path
		if line > 0 {
			target = fmt.Sprintf("%s:%d", path, line)
		}
		argv = append(argv, dir, "--goto", target)
	case s == colon && line > 0:
		argv = append(argv, fmt.Sprintf("%s:%d", path, line))
	case s == colon:
		argv = append(argv, path)
	default:
		// The path last, so a file name that looks like an option is not
		// read as one.
		if line > 0 {
			argv = append(argv, fmt.Sprintf("+%d", line))
		}
		argv = append(argv, path)
	}
	return Command{Argv: argv, Window: window}
}

// DirCommand opens the worktree dir: in the worktree_editor setting, else
// VS Code.
func DirCommand(dir string) Command { return DirCommandIn(Worktree, dir) }

// DirCommandIn is DirCommand with setting for the worktree_editor setting.
func DirCommandIn(setting, dir string) Command {
	argv := strings.Fields(setting)
	if len(argv) == 0 {
		argv = []string{"code"}
	}
	_, window := styleOf(argv)
	return Command{Argv: append(argv, dir), Window: window}
}

// RemoteFileCommand is FileCommand for a file on host, a machine reached
// over ssh, in a window editor started here that edits over ssh itself:
// VS Code and its kin by their ssh remote, Zed by an ssh:// address. It
// is false for an editor that cannot, a terminal one above all, which is
// to run on host instead; and for a path that is not absolute and plain:
// what names a file on another machine is not to be read as an option.
func RemoteFileCommand(host, dir, path string, line int) (Command, bool) {
	return remote(fileEditor(File, os.Getenv), host, dir, path, line)
}

// RemoteDirCommand is DirCommand for a worktree on host; see
// RemoteFileCommand.
func RemoteDirCommand(host, dir string) (Command, bool) {
	argv := strings.Fields(Worktree)
	if len(argv) == 0 {
		argv = []string{"code"}
	}
	return remote(argv, host, dir, "", 0)
}

func remote(argv []string, host, dir, path string, line int) (Command, bool) {
	if !plainHost(host) || !plainPath(dir) || path != "" && !plainPath(path) {
		return Command{}, false
	}
	s, window := styleOf(argv)
	if !window {
		return Command{}, false
	}
	at := func(p string) string {
		if line > 0 {
			return fmt.Sprintf("%s:%d", p, line)
		}
		return p
	}
	switch {
	case s == vscode:
		argv = append(argv, "--remote", "ssh-remote+"+host, dir)
		if path != "" {
			argv = append(argv, "--goto", at(path))
		}
	case s == colon && slices.Contains([]string{"zed", "zeditor"}, filepath.Base(argv[0])):
		target := dir
		if path != "" {
			target = at(path)
		}
		argv = append(argv, "ssh://"+host+target)
	default:
		return Command{}, false
	}
	return Command{Argv: argv, Window: true}, true
}

// plainPath reports whether p is an absolute path with no control
// characters.
func plainPath(p string) bool {
	return strings.HasPrefix(p, "/") && !strings.ContainsFunc(p, func(r rune) bool { return r < ' ' || r == 0x7f })
}

// plainHost reports whether h can name a host for ssh, and nothing else.
func plainHost(h string) bool {
	if h == "" || strings.HasPrefix(h, "-") {
		return false
	}
	for _, r := range h {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-@:[]", r)) {
			return false
		}
	}
	return true
}
