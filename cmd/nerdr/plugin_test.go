package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/plugin"
	"github.com/nerdr-dev/nerdr/internal/state"
)

func TestPluginValues(t *testing.T) {
	p := plugin.Plugin{Name: "docker", Params: []plugin.Param{
		{Name: "repos", Type: plugin.Multi},
		{Name: "clean", Type: plugin.Bool},
		{Name: "note", Type: plugin.String},
	}}
	got, err := pluginValues(p, []string{"--repos", "api,web", "--clean", "--note=hi there", "--repos", "db"})
	if err != nil {
		t.Fatal(err)
	}
	want := plugin.Values{"repos": []string{"api", "web", "db"}, "clean": true, "note": "hi there"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	got, err = pluginValues(p, []string{"--json", `{"repos":["api"],"clean":false}`})
	if err != nil || !reflect.DeepEqual(got, plugin.Values{"repos": []string{"api"}, "clean": false}) {
		t.Fatalf("--json: %v, %v", got, err)
	}
	for _, bad := range [][]string{{"--nope", "x"}, {"api"}, {"--note"}, {"--clean=maybe"}} {
		if _, err := pluginValues(p, bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}

// A plugin runs only once the user allowed it as it is.
func TestPluginRunNeedsTheUser(t *testing.T) {
	cfg, marker := t.TempDir(), filepath.Join(t.TempDir(), "ran")
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("NERDR", "")
	dir := filepath.Join(cfg, "nerdr", "plugins", "touch")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, plugin.File), []byte("description = \"d\"\nrun = [\"./run.sh\"]\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755)

	if code := pluginRun([]string{"touch"}); code == 0 {
		t.Fatal("a plugin not allowed ran")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a plugin not allowed ran its program")
	}
	p, key, err := plugin.Fresh(plugin.Load(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := plugin.Allow(plugin.AllowedFile(state.Dir()), p, key); err != nil {
		t.Fatal(err)
	}
	if code := pluginRun([]string{"touch"}); code != 0 {
		t.Fatalf("an allowed plugin exited %d", code)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("an allowed plugin did not run its program")
	}
}

// In a nerdr tab, the run is nerdr's: the command asks it to run the
// plugin, with the values and the folder, and prints what nerdr says it
// printed until it ends, with its exit status.
func TestPluginRunThroughNerdr(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := filepath.Join(cfg, "nerdr", "plugins", "say")
	_ = os.MkdirAll(dir, 0o755)
	marker := filepath.Join(t.TempDir(), "ran")
	_ = os.WriteFile(filepath.Join(dir, plugin.File), []byte("description = \"d\"\nrun = [\"./run.sh\"]\n"+
		"[[params]]\nname = \"word\"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755)

	sockDir, err := os.MkdirTemp("", "nerdr")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockDir)
	var asked hooks.Report
	srv, err := hooks.Listen(filepath.Join(sockDir, "s"), func(r hooks.Report) {
		switch r.What {
		case hooks.PluginRun:
			asked = r
			r.Reply <- hooks.Answer{Text: "started 0123456789abcdef"}
		case hooks.PluginRead:
			out := hooks.RunOutput{Text: "said hi\n", Offset: 8}
			if r.Offset == 8 {
				out = hooks.RunOutput{Offset: 8, Ended: true, Exit: 4}
			}
			b, _ := json.Marshal(out)
			r.Reply <- hooks.Answer{Text: string(b)}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	t.Setenv("NERDR", "1")
	t.Setenv("NERDR_SHELL", "")
	t.Setenv("NERDR_TAB", "secret")
	t.Setenv("NERDR_SOCKET", srv.Path())

	stdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	code := pluginRun([]string{"say", "--word", "hi"})
	os.Stdout = stdout
	w.Close()
	printed, _ := io.ReadAll(r)
	if code != 4 || string(printed) != "said hi\n" {
		t.Fatalf("exit %d, printed %q: want nerdr's 4 and what it read", code, printed)
	}
	cwd, _ := os.Getwd()
	if asked.Name != "say" || asked.Values["word"] != "hi" || asked.Dir != cwd {
		t.Fatalf("nerdr was asked to run %q with %v in %q", asked.Name, asked.Values, asked.Dir)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the command ran the plugin itself, not nerdr")
	}
}

// The text of `nerdr plugin list` is written by a plugin its author may
// be an agent: an escape sequence in it must not reach the terminal.
func TestPluginListCleansMetadata(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("NERDR", "")
	dir := filepath.Join(cfg, "nerdr", "plugins", "escape")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const seq = `\u001b]52;c;ZXZpbA==\u0007`
	toml := "description = \"d " + seq + "\"\ntitle = \"t " + seq + "\"\nrun = [\"true\"]\n" +
		"[[params]]\nname = \"mode\"\ntype = \"choice\"\noptions = [\"a" + seq + "\"]\ndefault = \"a" + seq + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, plugin.File), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	code := pluginList(nil)
	os.Stdout = stdout
	w.Close()
	printed, _ := io.ReadAll(r)
	if code != 0 {
		t.Fatalf("plugin list exited %d", code)
	}
	if strings.Contains(string(printed), "\x1b]52;") {
		t.Fatalf("plugin list printed the escape sequence: %q", printed)
	}
}

// A plugin whose name is sixteen hex digits is stopped by its name; only
// a word that is not a plugin's name is a run's id.
func TestPluginStopTarget(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	const hexName = "0123456789abcdef"
	dir := filepath.Join(cfg, "nerdr", "plugins", hexName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, plugin.File),
		[]byte("description = \"d\"\nrun = [\"true\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if field, _ := stopTarget(hexName); field != "name" {
		t.Fatalf("a plugin named %s was taken as a %s", hexName, field)
	}
	if field, _ := stopTarget("fedcba9876543210"); field != "run" {
		t.Fatalf("a run id was taken as a %s", field)
	}
	if field, _ := stopTarget("deploy"); field != "name" {
		t.Fatalf("a plugin name was taken as a %s", field)
	}
}
