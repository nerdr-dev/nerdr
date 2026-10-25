package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"nerdr/internal/plugin"
)

// runDir opens the requested working directory beneath a trusted tab
// root. Resolving aliases permits absolute symlinks within the tab;
// Root.OpenFile then prevents a changed path from escaping that root.
// The caller owns the returned descriptor, including while asking for
// confirmation: startup uses that directory, not another path lookup.
func runDir(t *tab, dir string) (*os.File, error) {
	if dir == "" {
		dir = t.Dir
	}
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("plugin working directory must be absolute")
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, fmt.Errorf("plugin working directory: %w", err)
	}
	roots := []string{t.Dir}
	for _, w := range t.Worktrees {
		roots = append(roots, w.Path)
	}
	var lastErr error
	for _, path := range roots {
		rootPath, err := filepath.EvalSymlinks(path)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(rootPath, resolved)
		if err != nil || !filepath.IsLocal(rel) {
			continue
		}
		root, err := os.OpenRoot(rootPath)
		if err != nil {
			lastErr = err
			continue
		}
		// O_DIRECTORY rejects files and FIFOs without blocking on them.
		f, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_DIRECTORY, 0)
		root.Close()
		if err != nil {
			lastErr = err
			continue
		}
		return f, nil
	}
	if lastErr != nil {
		return nil, fmt.Errorf("plugin working directory: %w", lastErr)
	}
	return nil, fmt.Errorf("plugin working directory must be within this tab's folders")
}

// PluginExecArg is an internal entry point, handled before normal CLI
// setup. Re-exec lets the child fchdir on Linux and macOS without ever
// changing the working directory of nerdr's other goroutines.
const PluginExecArg = "--nerdr-plugin-exec"

func pluginCommandIn(cmd *exec.Cmd, dir *os.File, checkDir, key string) error {
	if cmd.Err != nil {
		return cmd.Err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd.Args = append([]string{exe, PluginExecArg, cmd.Path, checkDir, key}, cmd.Args...)
	cmd.Path, cmd.Dir = exe, ""
	cmd.ExtraFiles = []*os.File{dir} // descriptor 3 in the child
	return nil
}

// checkPluginKey is the last look at a plugin before its program starts:
// the folder is hashed in the child, as near to exec as this can be, so a
// change between the question and the start is refused rather than run.
func checkPluginKey(dir, key string) error {
	if dir == "" && key == "" {
		return nil // the internal helper is called without a check, as in tests
	}
	if dir == "" || key == "" {
		return fmt.Errorf("plugin key check is incomplete")
	}
	got, err := (plugin.Plugin{Dir: dir}).Key()
	if err != nil {
		return err
	}
	if got != key {
		return fmt.Errorf("%s changed while it was started: it is not what the user allowed; run it again after they allow it", dir)
	}
	return nil
}

// ExecPlugin is the child side of pluginCommandIn. It inherits the
// caller's credentials and sandbox; calling it directly grants no rights.
// The directory descriptor is closed before the plugin is executed.
func ExecPlugin(args []string) int {
	fail := func(err error) int {
		fmt.Fprintf(os.Stderr, "nerdr plugin: %v\n", err)
		return 125
	}
	if len(args) < 4 {
		return fail(fmt.Errorf("missing plugin command"))
	}
	program, checkDir, key := args[0], args[1], args[2]
	if err := checkPluginKey(checkDir, key); err != nil {
		return fail(err)
	}
	dir := os.NewFile(3, "plugin working directory")
	if dir == nil {
		return fail(fmt.Errorf("missing plugin working directory"))
	}
	err := dir.Chdir()
	dir.Close()
	if err != nil {
		return fail(err)
	}
	// Do not carry nerdr's PWD (or an old symlink alias) into the plugin.
	os.Unsetenv("PWD")
	pwd, err := os.Getwd()
	if err != nil {
		return fail(err)
	}
	os.Setenv("PWD", pwd)
	return fail(syscall.Exec(program, args[3:], os.Environ()))
}
