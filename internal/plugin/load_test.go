package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// loadPlugin makes a plugin whose run is argv, beside the files the
// allowed commands name, and loads it.
func loadPlugin(t *testing.T, base string, argv []string) Plugin {
	t.Helper()
	dir := filepath.Join(base, "p")
	_ = os.RemoveAll(dir)
	run, _ := json.Marshal(argv)
	write(t, dir, File, "description = \"d\"\nrun = "+string(run)+"\n")
	for _, f := range []string{"ok.js", "preload.js", "ok.sh", "rc.sh", "ok.rb", "lib.rb", "ok.pl",
		"ok.ts", "prog.awk", "prog.sed", "ok.lua", "ok.php", "ok.py"} {
		write(t, dir, f, "x\n")
	}
	if err := os.MkdirAll(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	return Load(dir)
}

// A flag of an interpreter that loads more code — node --require, bash
// --rcfile, ruby -r, awk -f — loads it from the plugin's folder, as its
// script is, whether its value is the next word, after = or joined to
// the flag: a relative one is the folder the agent runs in, which the
// plugin's hash does not cover. env cannot make a command line of a word
// either, nor set a variable that loads code.
func TestCodeLoadingFlagsAreThePlugins(t *testing.T) {
	base := t.TempDir()
	for _, argv := range [][]string{
		{"node", "--require", "./evil.js", "./ok.js"},
		{"node", "--require=./evil.js", "./ok.js"},
		{"node", "-r", "./evil.js", "./ok.js"},
		{"node", "--import", "./evil.mjs", "./ok.js"},
		{"node", "--import=evil", "./ok.js"},
		{"node", "--require", "express", "./ok.js"},
		{"node", "--loader", "./evil.mjs", "./ok.js"},
		{"node", "--experimental-loader=./evil.mjs", "./ok.js"},
		{"node", "-r", "./evil.js", "-e", "1"},
		{"node20", "--require", "/tmp/evil.js", "./ok.js"},
		{"bash", "--rcfile", "./evil.sh", "./ok.sh"},
		{"bash", "--rcfile=./evil.sh", "./ok.sh"},
		{"bash", "--init-file", "./evil.sh", "-i", "./ok.sh"},
		{"sh", "--rcfile", "./evil.sh", "-c", "echo"},
		{"ruby", "-r", "./evil.rb", "./ok.rb"},
		{"ruby", "-r./evil", "./ok.rb"},
		{"ruby", "-wr./evil", "./ok.rb"},
		{"ruby", "-I", "./evil", "./ok.rb"},
		{"ruby", "-I./evil", "-r", "x", "./ok.rb"},
		// The flag's value is the plugin's, but then the script is not.
		{"ruby", "-wr", "./lib.rb", "./evil.rb"},
		{"perl", "-I./evil", "./ok.pl"},
		{"perl", "-M./evil", "./ok.pl"},
		{"php", "-c", "./evil.ini", "./ok.php"},
		{"lua", "-l", "evil", "./ok.lua"},
		{"lua", "-e", "x=1", "./evil.lua"},
		{"env", "-S", "sh ./evil.sh"},
		{"env", "--split-string=sh ./evil.sh"},
		{"env", "--split-string", "sh ./evil.sh"},
		{"env", "-iS", "sh ./evil.sh"},
		{"env", "NODE_OPTIONS=--require=./evil.js", "node", "./ok.js"},
		{"env", "PATH=./bin:/usr/bin", "mytool"},
		{"env", "PATH=.", "sh", "./ok.sh"},
		{"env", "BASH_ENV=./evil.sh", "bash", "./ok.sh"},
		{"env", "LD_PRELOAD=./evil.so", "./ok.sh"},
		{"env", "RUBYOPT=-r./evil", "ruby", "./ok.rb"},
		{"env", "A=b", "env", "PYTHONPATH=.", "python3", "./ok.py"},
		{"deno", "run", "./evil.ts"},
		{"deno", "run", "-A", "--preload", "./evil.ts", "./ok.ts"},
		{"deno", "run", "--config=./deno.json", "./ok.ts"},
		{"deno", "--import-map", "./map.json", "./ok.ts"},
		{"bun", "./evil.ts"},
		{"bun", "run", "--preload", "./evil.ts", "./ok.ts"},
		{"bun", "-r", "./evil.ts", "./ok.ts"},
		{"awk", "-f", "./evil.awk", "data.txt"},
		{"awk", "--file=./evil.awk", "data.txt"},
		{"gawk", "-f", "evil.awk", "data.txt"},
		{"gawk", "-i", "evil", "{print}"},
		{"awk", "-v", "x=1", "-f./evil.awk"},
		{"sed", "-f", "./evil.sed", "data.txt"},
		{"sed", "-n", "--file=./evil.sed", "data.txt"},
	} {
		p := loadPlugin(t, base, argv)
		if p.Err == "" {
			t.Errorf("%q: allowed", argv)
		}
	}
}

// What such a flag loads from the plugin's folder is allowed, and so is a
// module the interpreter finds on its own load path; awk's and sed's
// program as a word is code, not a file.
func TestCodeLoadingFlagsFromThePluginAreAllowed(t *testing.T) {
	base := t.TempDir()
	for _, argv := range [][]string{
		{"node", "--require", "./preload.js", "./ok.js"},
		{"node", "--require=./preload.js", "./ok.js"},
		{"node", "-r", "preload.js", "ok.js"},
		{"bash", "--rcfile", "./rc.sh", "./ok.sh"},
		{"bash", "-O", "extglob", "./ok.sh"},
		{"ruby", "-r", "json", "./ok.rb"},
		{"ruby", "-rjson", "-r./lib.rb", "./ok.rb"},
		{"ruby", "-I", "./lib", "./ok.rb"},
		{"perl", "-MPOSIX", "-Mstrict", "./ok.pl"},
		{"perl", "-I./lib", "./ok.pl"},
		{"deno", "run", "-A", "./ok.ts"},
		{"deno", "task", "dev"},
		{"deno", "eval", "console.log(1)"},
		{"bun", "run", "build"},
		{"bun", "--preload", "./preload.js", "./ok.ts"},
		{"awk", "-f", "./prog.awk", "data.txt"},
		{"awk", "-F:", "{print $1}", "./data.txt"},
		{"sed", "-e", "s/a/b/", "data.txt"},
		{"sed", "-f", "prog.sed", "data.txt"},
		{"env", "FOO=bar", "./ok.sh"},
		{"env", "-u", "HOME", "sh", "./ok.sh"},
		{"lua", "-e", "x=1", "./ok.lua"},
	} {
		if p := loadPlugin(t, base, argv); p.Err != "" {
			t.Errorf("%q: %s", argv, p.Err)
		}
	}
}

// What a flag loads code from, made a link out of the plugin's folder, is
// refused as a script would be.
func TestCodeLoadingFlagThroughALinkOut(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "evil.js")
	if err := os.WriteFile(outside, []byte("evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "p")
	write(t, dir, File, "description = \"d\"\nrun = [\"node\", \"--require=./pre.js\", \"./ok.js\"]\n")
	write(t, dir, "ok.js", "x\n")
	if err := os.Symlink(outside, filepath.Join(dir, "pre.js")); err != nil {
		t.Fatal(err)
	}
	if p := Load(dir); !strings.Contains(p.Err, "links outside") {
		t.Fatalf("error %q", p.Err)
	}
	if err := os.Remove(filepath.Join(dir, "pre.js")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "pre.js"), 0o600); err != nil {
		t.Skip("mkfifo:", err)
	}
	if p := Load(dir); !strings.Contains(p.Err, "not a regular file") {
		t.Fatalf("a FIFO it loads: error %q", p.Err)
	}
}

// within fails t when f takes longer than a few seconds.
func within(t *testing.T, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); f() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s hung", what)
	}
}

// plugin.toml swapped for a pipe, a link (to /dev/zero, out of the
// folder) or a huge file is refused at once.
func TestReadTomlRefusesWhatIsNotASmallFile(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside.toml")
	if err := os.WriteFile(outside, []byte("description = \"d\"\nrun = [\"./x\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"fifo", "zero", "outside", "huge"} {
		dir := filepath.Join(base, kind)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, File)
		var err error
		switch kind {
		case "fifo":
			err = syscall.Mkfifo(path, 0o600)
		case "zero":
			err = os.Symlink("/dev/zero", path)
		case "outside":
			err = os.Symlink(outside, path)
		case "huge":
			err = os.WriteFile(path, make([]byte, maxToml+1), 0o644)
		}
		if err != nil {
			t.Fatal(err)
		}
		within(t, kind, func() {
			if _, err := readToml(dir); err == nil {
				t.Errorf("%s: read", kind)
			}
			if p := Load(dir); p.Err == "" {
				t.Errorf("%s: loaded", kind)
			}
		})
	}
}

// Key walks the plugin's folder without following links or opening what
// is not a regular file: a pipe or a link to /dev/zero there is named in
// the hash, so it changes the key, and a folder over maxHashed is
// refused, all at once.
func TestKeyDoesNotReadWhatIsNotAFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p")
	write(t, dir, File, "description = \"d\"\nrun = [\"./run.sh\"]\n")
	write(t, dir, "run.sh", "#!/bin/sh\n")
	p := Plugin{Dir: dir}
	before, err := p.Key()
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o600); err != nil {
		t.Skip("mkfifo:", err)
	}
	var withPipe string
	within(t, "a pipe", func() {
		if withPipe, err = p.Key(); err != nil {
			t.Fatal(err)
		}
	})
	if withPipe == before {
		t.Error("a pipe put in the folder did not change the key")
	}
	if err := os.Symlink("/dev/zero", filepath.Join(dir, "zero")); err != nil {
		t.Fatal(err)
	}
	within(t, "a link to /dev/zero", func() {
		if _, err := p.Key(); err != nil {
			t.Fatal(err)
		}
	})
	f, err := os.Create(filepath.Join(dir, "big"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxHashed + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	within(t, "a big file", func() {
		if _, err := p.Key(); err == nil || !strings.Contains(err.Error(), "more than") {
			t.Errorf("a folder over the limit: %v", err)
		}
	})
}
