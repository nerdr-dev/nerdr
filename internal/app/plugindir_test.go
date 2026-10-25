package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"nerdr/internal/hooks"
	"nerdr/internal/plugin"
	"nerdr/internal/workspace"
)

// Test binaries need the same internal child entry point as cmd/nerdr.
func init() {
	if len(os.Args) > 1 && os.Args[1] == PluginExecArg {
		os.Exit(ExecPlugin(os.Args[2:]))
	}
}

func TestPluginRunDirectory(t *testing.T) {
	root, outside, worktree := t.TempDir(), t.TempDir(), t.TempDir()
	inside := filepath.Join(root, "inside")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"escape": outside, "absolute": inside, "relative": "inside"} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	regular := filepath.Join(root, "file")
	if err := os.WriteFile(regular, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	tb := &tab{}
	tb.Dir = root
	tb.Worktrees = []workspace.Worktree{{Path: worktree}}
	for _, tc := range []struct{ name, dir, want string }{
		{"default", "", root}, {"root", root, root}, {"subdirectory", inside, inside},
		{"absolute symlink", filepath.Join(root, "absolute"), inside},
		{"relative symlink", filepath.Join(root, "relative"), inside},
		{"worktree", worktree, worktree},
		{"outside", outside, ""}, {"escape symlink", filepath.Join(root, "escape"), ""},
		{"relative path", "inside", ""}, {"missing", filepath.Join(root, "missing"), ""},
		{"file", regular, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := runDir(tb, tc.dir)
			if tc.want == "" {
				if err == nil {
					f.Close()
					t.Fatal("invalid directory accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			actual, err := f.Stat()
			if err != nil {
				t.Fatal(err)
			}
			expected, err := os.Stat(tc.want)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(actual, expected) {
				t.Fatal("wrong directory opened")
			}
		})
	}
	// macOS's /var -> /private/var is one real example of an aliased root.
	alias := filepath.Join(t.TempDir(), "root")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	tb.Dir = alias
	f, err := runDir(tb, inside)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func TestPluginDirectoryPinnedThroughStart(t *testing.T) {
	for _, swap := range []string{"symlink", "directory"} {
		t.Run(swap, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			inside := filepath.Join(root, "inside")
			if err := os.Mkdir(inside, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(inside, "marker"), []byte("inside"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(outside, "marker"), []byte("outside"), 0o600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, "link")
			if err := os.Symlink(inside, link); err != nil {
				t.Fatal(err)
			}
			tb := &tab{}
			tb.Dir = root
			f, err := runDir(tb, link)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			expected := inside
			if swap == "symlink" {
				if err := os.Remove(link); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, link); err != nil {
					t.Fatal(err)
				}
			} else {
				expected = filepath.Join(root, "moved")
				if err := os.Rename(inside, expected); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, inside); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("sh", "-c", `cat marker; printf '\n%s\n' "$PWD"`)
			if err := pluginCommandIn(cmd, f, "", ""); err != nil {
				t.Fatal(err)
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			expected, err = filepath.EvalSymlinks(expected)
			if err != nil {
				t.Fatal(err)
			}
			if string(out) != "inside\n"+expected+"\n" {
				t.Fatalf("directory changed after check: %q", out)
			}
		})
	}
}

func TestPluginRunRejectsOutsideDirectory(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	m := testModel(t)
	tb := m.cur()
	tb.Dir = t.TempDir()
	defer m.Shutdown()
	allowedPlugin(t, cfg, "dircheck", "description = \"Test.\"\nrun = [\"true\"]\nopen = false\n")
	outside := t.TempDir()
	link := filepath.Join(tb.Dir, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	reply := make(chan hooks.Answer, 1)
	m.Update(hooks.Report{Method: hooks.Plugin, What: hooks.PluginRun, Pane: tb.hookKey(), Name: "dircheck", Dir: link, Reply: reply})
	if a := <-reply; !strings.Contains(a.Err, "within this tab") {
		t.Fatalf("outside directory not refused: %+v", a)
	}
	if len(m.plugins.byID) != 0 {
		t.Fatal("a plugin was started")
	}
}

// Even a symlink changing during resolution/open must never select the
// outside directory. Errors while it changes are safe refusals.
func TestPluginDirectoryConcurrentSwap(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	inside := filepath.Join(root, "inside")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(inside, link); err != nil {
		t.Fatal(err)
	}
	stop, done := make(chan struct{}), make(chan error, 1)
	go func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
			target := inside
			if i%2 == 0 {
				target = outside
			}
			if err := os.Symlink(target, link+".new"); err != nil {
				done <- err
				return
			}
			if err := os.Rename(link+".new", link); err != nil {
				done <- err
				return
			}
		}
	}()
	defer func() {
		close(stop)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	tb := &tab{}
	tb.Dir = root
	expected, err := os.Stat(inside)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		f, err := runDir(tb, link)
		if err != nil {
			continue
		}
		got, err := f.Stat()
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(got, expected) {
			t.Fatal("symlink race opened a directory outside the tab")
		}
	}
}

func TestPluginDirectoryHeldWhileConfirming(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	m := testModel(t)
	tb := m.cur()
	tb.Dir = t.TempDir()
	defer m.Shutdown()
	allowedPlugin(t, cfg, "dircheck", "description = \"Test.\"\nrun = [\"sh\", \"-c\", \"cat marker\"]\nconfirm = true\nopen = false\n")
	inside := filepath.Join(tb.Dir, "inside")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inside, "marker"), []byte("checked directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	reply := make(chan hooks.Answer, 1)
	m.Update(hooks.Report{Method: hooks.Plugin, What: hooks.PluginRun, Pane: tb.hookKey(), Name: "dircheck", Dir: inside, Reply: reply})
	if m.plugins.asking == nil {
		t.Fatal("run did not ask for confirmation")
	}
	if err := os.Rename(inside, filepath.Join(tb.Dir, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), inside); err != nil {
		t.Fatal(err)
	}
	m.Update(keyMsg("y"))
	run := started(t, m, reply)
	out, exit := readRun(t, m, tb, run)
	if out != "checked directory" || exit != 0 {
		t.Fatalf("confirmation changed the directory: %q, exit %d", out, exit)
	}
}

// The folder is hashed again in the child, just before the program runs:
// a change between the user's yes and the start is refused.
func TestPluginChangedBeforeExecIsRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, plugin.File),
		[]byte("description = \"d\"\nrun = [\"./run.sh\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntrue\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p, key, err := plugin.Fresh(plugin.Load(dir))
	if err != nil || p.Err != "" {
		t.Fatal(err, p.Err)
	}
	f, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	marker := filepath.Join(t.TempDir(), "ran")
	cmd := exec.Command("sh", "-c", "touch "+marker)
	if err := pluginCommandIn(cmd, f, dir, key); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Run(); err == nil {
		t.Fatal("the run went on after its folder changed")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the changed program ran")
	}
	if err := checkPluginKey(dir, key); err == nil {
		t.Fatal("the changed folder still matches its key")
	}
}

// A check with only one of the folder and the key is a caller's mistake,
// not a check that passes; both empty is the internal "no check".
func TestCheckPluginKeyIncomplete(t *testing.T) {
	if err := checkPluginKey("", ""); err != nil {
		t.Fatalf("no check: %v", err)
	}
	if err := checkPluginKey(t.TempDir(), ""); err == nil {
		t.Fatal("a missing key passed")
	}
	if err := checkPluginKey("", "key"); err == nil {
		t.Fatal("a missing folder passed")
	}
}
