// Package editor opens a file, or a whole worktree, in the user's editor.
// A terminal editor (vim, nano, …) runs inside nerdr and has the screen
// until it exits; a window editor (VS Code, Zed, …) is started on its own
// and nerdr carries on.
package editor

import (
	"fmt"
	"os"
	"path/filepath"
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

// fileEditor is the editor setting, then $VISUAL, then $EDITOR, then vim.
func fileEditor() []string {
	for _, s := range []string{File, os.Getenv("VISUAL"), os.Getenv("EDITOR")} {
		if f := strings.Fields(s); len(f) > 0 {
			return f
		}
	}
	return []string{"vim"}
}

// FileCommand opens path, a file in the worktree dir, on line (none when
// 0).
func FileCommand(dir, path string, line int) Command {
	argv := fileEditor()
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
func DirCommand(dir string) Command {
	argv := strings.Fields(Worktree)
	if len(argv) == 0 {
		argv = []string{"code"}
	}
	_, window := styleOf(argv)
	return Command{Argv: append(argv, dir), Window: window}
}
