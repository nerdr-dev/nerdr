package holder

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/plugin"
	"github.com/nerdr-dev/nerdr/internal/spawn"
)

// The test binary is the nerdr a plugin's run starts through, as
// cmd/nerdr is.
func init() {
	if len(os.Args) > 1 && os.Args[1] == core.PluginExecArg {
		os.Exit(core.ExecPlugin(os.Args[2:]))
	}
}

// A plugin's run started in the holder gets the folder the core opened as
// descriptor 3, and runs there even when the path has since gone
// elsewhere.
func TestPluginRunGetsTheFolderTheCoreOpened(t *testing.T) {
	h, cl := serve(t)
	shutdown(t, h)
	root, outside := t.TempDir(), t.TempDir()
	inside := filepath.Join(root, "inside")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(inside, "marker"), []byte("inside"), 0o600)
	os.WriteFile(filepath.Join(outside, "marker"), []byte("outside"), 0o600)
	f, err := os.Open(inside)
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(root, "moved")
	if err := os.Rename(inside, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, inside); err != nil {
		t.Fatal(err)
	}
	s, err := core.PluginSpec(exec.Command("sh", "-c", `cat marker; printf '\n%s\n' "$PWD"`), f, "", "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := cl.Spawn(s)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	out, _ := io.ReadAll(p)
	if err := p.Wait(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	want, _ := filepath.EvalSymlinks(moved)
	if string(out) != "inside\n"+want+"\n" {
		t.Errorf("ran with %q", out)
	}
}

// The check of the plugin's key in the child still refuses a plugin that
// changed after the user allowed it, when the holder starts it.
func TestPluginChangedBeforeTheHolderStartsItIsRefused(t *testing.T) {
	h, cl := serve(t)
	shutdown(t, h)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, plugin.File), []byte("description = \"d\"\nrun = [\"./run.sh\"]\n"), 0o644)
	script := filepath.Join(dir, "run.sh")
	os.WriteFile(script, []byte("#!/bin/sh\ntrue\n"), 0o755)
	pl, key, err := plugin.Fresh(plugin.Load(dir))
	if err != nil || pl.Err != "" {
		t.Fatal(err, pl.Err)
	}
	marker := filepath.Join(t.TempDir(), "ran")
	run := func() (string, error) {
		f, err := os.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		s, err := core.PluginSpec(exec.Command("sh", "-c", "touch "+marker), f, dir, key)
		if err != nil {
			t.Fatal(err)
		}
		p, err := cl.Spawn(s)
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		out, _ := io.ReadAll(p)
		return string(out), p.Wait()
	}
	if out, err := run(); err != nil {
		t.Fatalf("unchanged plugin: %v: %s", err, out)
	}
	os.Remove(marker)
	os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755)
	out, err := run()
	var ee *spawn.ExitError
	if !errors.As(err, &ee) || ee.Code != 125 {
		t.Fatalf("changed plugin: %v: %s", err, out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the changed program ran")
	}
}
