package plugin

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func write(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o755); err != nil {
		t.Fatal(err)
	}
}

const docker = `title = "Docker dev"
description = "Rebuilds and restarts the stack."
run = ["./run.sh"]

[[params]]
name = "repos"
type = "multi"
options = ["api", "web"]
required = true

[[params]]
name = "clean"
type = "bool"

[[params]]
name = "mode"
type = "choice"
options = ["dev", "prod"]
default = "dev"
`

func TestFind(t *testing.T) {
	user := t.TempDir()
	write(t, filepath.Join(user, "docker"), File, docker)
	write(t, filepath.Join(user, "seed"), File, `description = "Seeds the database."`+"\nui = [\"./ui.sh\"]\n")
	write(t, filepath.Join(user, "notaplugin"), "README", "")

	all := Find(user)
	if len(all) != 2 {
		t.Fatalf("found %d plugins, want docker and seed: %+v", len(all), all)
	}
	if all[0].Name != "docker" || all[0].Title != "Docker dev" {
		t.Fatalf("docker: %+v", all[0])
	}
	if all[1].Name != "seed" || all[1].Err != "" {
		t.Fatalf("seed: %+v", all[1])
	}
}

func TestLoadSaysWhatIsWrong(t *testing.T) {
	for _, c := range []struct{ toml, want string }{
		{`run = ["x"]`, "description is missing"},
		{`description = "d"`, "run and ui are both missing"},
		{"description = \"d\"\nrun = [\"x\"]\nsurprise = 1", "nothing called surprise"},
		{"description = \"d\"\nrun = [\"x\"]\n[[params]]\nname = \"a\"\ntype = \"number\"", "type is string"},
		{"description = \"d\"\nrun = [\"x\"]\n[[params]]\nname = \"a\"\ntype = \"choice\"", "needs options"},
		{"description = \"d\"\nrun = [\"x\"]\n[[params]]\nname = \"Big\"", "lower case"},
	} {
		dir := filepath.Join(t.TempDir(), "p")
		write(t, dir, File, c.toml)
		if p := Load(dir); !strings.Contains(p.Err, c.want) {
			t.Errorf("%q: error %q, want %q", c.toml, p.Err, c.want)
		}
	}
	dir := filepath.Join(t.TempDir(), "Bad Name")
	write(t, dir, File, "description = \"d\"\nrun = [\"x\"]")
	if p := Load(dir); !strings.Contains(p.Err, "folder's name") {
		t.Errorf("a bad folder name: error %q", p.Err)
	}
}

func TestValues(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docker")
	write(t, dir, File, docker)
	p := Load(dir)
	if p.Err != "" {
		t.Fatal(p.Err)
	}
	vals := Values{}
	if err := p.Set(vals, "repos", "api"); err != nil {
		t.Fatal(err)
	}
	if err := p.Set(vals, "repos", "web"); err != nil {
		t.Fatal(err)
	}
	if err := p.Set(vals, "repos", "db"); err == nil {
		t.Fatal("db is not one of the options")
	}
	if err := p.Set(vals, "mode", "test"); err == nil {
		t.Fatal("test is not a mode")
	}
	if err := p.Set(vals, "nope", "x"); err == nil {
		t.Fatal("there is no parameter nope")
	}
	if missing := p.Complete(vals); len(missing) != 0 {
		t.Fatalf("missing %v", missing)
	}
	env := p.Env(vals)
	for _, want := range []string{"NERDR_PARAM_REPOS=api,web", "NERDR_PARAM_CLEAN=false", "NERDR_PARAM_MODE=dev", "NERDR_PLUGIN=docker"} {
		if !slices.Contains(env, want) {
			t.Errorf("env %v lacks %s", env, want)
		}
	}
	if missing := p.Complete(Values{}); len(missing) != 1 || missing[0].Name != "repos" {
		t.Fatalf("missing %v, want repos", missing)
	}
	if u := p.Usage(); u != "nerdr plugin run docker --repos A,B [--clean] [--mode dev|prod]" {
		t.Fatalf("usage %q", u)
	}
}

func TestRunGetsItsValues(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "echo")
	write(t, dir, File, "description = \"d\"\nrun = [\"./run.sh\"]\n[[params]]\nname = \"who\"\n")
	write(t, dir, "run.sh", "#!/bin/sh\necho \"$NERDR_PARAM_WHO\"; cat; pwd\n")
	p := Load(dir)
	here := t.TempDir()
	out, err := p.RunCmd(Values{"who": "you"}, here).Output()
	if err != nil {
		t.Fatal(err)
	}
	want := "you\n{\"who\":\"you\"}\n" + here + "\n"
	if got, _ := filepath.EvalSymlinks(here); got != here {
		want = "you\n{\"who\":\"you\"}\n" + got + "\n"
	}
	if string(out) != want {
		t.Fatalf("run printed %q, want %q", out, want)
	}
}

func TestAllowedUntilChanged(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "seed")
	write(t, dir, File, "description = \"d\"\nui = [\"./ui.sh\"]\n")
	write(t, dir, "ui.sh", "echo hi\n")
	file := filepath.Join(t.TempDir(), "allowed.json")
	p := Load(dir)
	if Allowed(file, p) {
		t.Fatal("a plugin is allowed before the user said so")
	}
	p, key, err := Fresh(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := Allow(file, p, key); err != nil {
		t.Fatal(err)
	}
	if !Allowed(file, p) {
		t.Fatal("not allowed after Allow")
	}
	write(t, dir, "ui.sh", "echo bye\n")
	if Allowed(file, p) {
		t.Fatal("still allowed after its program changed")
	}
	// The same plugin elsewhere is allowed with it.
	p, key, _ = Fresh(p)
	_ = Allow(file, p, key)
	other := filepath.Join(t.TempDir(), "seed")
	write(t, other, File, "description = \"d\"\nui = [\"./ui.sh\"]\n")
	write(t, other, "ui.sh", "echo bye\n")
	if !Allowed(file, Load(other)) {
		t.Fatal("the same plugin in another folder is not allowed")
	}
}

// plugin.toml is read only when it is a regular file of a reasonable
// size: a link or a FIFO is refused rather than followed or waited on.
func TestLoadRefusesLinksAndSpecialFiles(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside.toml")
	if err := os.WriteFile(outside, []byte("description = \"d\"\nrun = [\"true\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	linked := filepath.Join(base, "linked")
	if err := os.MkdirAll(linked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(linked, File)); err != nil {
		t.Fatal(err)
	}
	if p := Load(linked); !strings.Contains(p.Err, "regular file") {
		t.Fatalf("a linked plugin.toml: error %q", p.Err)
	}

	fifo := filepath.Join(base, "fifo")
	if err := os.MkdirAll(fifo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(fifo, File), 0o600); err != nil {
		t.Skip("mkfifo:", err)
	}
	done := make(chan Plugin, 1)
	go func() { done <- Load(fifo) }()
	select {
	case p := <-done:
		if !strings.Contains(p.Err, "regular file") {
			t.Fatalf("a FIFO plugin.toml: error %q", p.Err)
		}
	case <-time.After(time.Second):
		t.Fatal("Load waited on a FIFO")
	}

	big := filepath.Join(base, "big")
	if err := os.MkdirAll(big, 0o755); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, maxToml+1)
	copy(data, "description = \"d\"\nrun = [\"true\"]\n")
	if err := os.WriteFile(filepath.Join(big, File), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if p := Load(big); !strings.Contains(p.Err, "at most") {
		t.Fatalf("a huge plugin.toml: error %q", p.Err)
	}
}

// What is drawn and asked about from plugin.toml is bounded: an agent can
// write it, and the panel draws it every frame.
func TestLoadCapsMetadata(t *testing.T) {
	for _, c := range []struct{ toml, want string }{
		{"description = \"" + strings.Repeat("d", maxText+1) + "\"\nrun = [\"true\"]", "description is"},
		{"description = \"d\"\ntitle = \"" + strings.Repeat("t", maxTitle+1) + "\"\nrun = [\"true\"]", "title is"},
		{"description = \"d\"\nrun = [\"true\", \"" + strings.Repeat("a", maxCommandArg+1) + "\"]", "word of a command"},
		{"description = \"d\"\nrun = [\"true\"]\n[[params]]\nname = \"a-b\"\n[[params]]\nname = \"a_b\"", "same environment name"},
	} {
		dir := filepath.Join(t.TempDir(), "p")
		write(t, dir, File, c.toml)
		if p := Load(dir); !strings.Contains(p.Err, c.want) {
			t.Errorf("error %q, want %q", p.Err, c.want)
		}
	}
}

// The files a command names in the plugin's folder are used from there,
// not from the folder an agent happens to run it in.
func TestCommandUsesThePluginsFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p")
	write(t, dir, File, "description = \"d\"\nrun = [\"sh\", \"run.sh\"]\n")
	write(t, dir, "run.sh", "#!/bin/sh\necho plugin\n")
	write(t, dir, "script", "#!/bin/sh\necho noext\n")
	write(t, dir, "build", "#!/bin/sh\necho build\n")
	write(t, dir, "docker-compose.yml", "services: {}\n")
	write(t, filepath.Join(dir, "bin"), "deploy.sh", "#!/bin/sh\necho deploy\n")
	if err := os.Mkdir(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := Load(dir)
	if p.Err != "" {
		t.Fatal(p.Err)
	}
	inside := func(name string) string { return filepath.Join(dir, name) }
	for _, tc := range []struct {
		argv, want []string
	}{
		{[]string{"sh", "run.sh"}, []string{"sh", inside("run.sh")}},
		{[]string{"sh", "./run.sh"}, []string{"sh", inside("./run.sh")}},
		{[]string{"sh", "script"}, []string{"sh", inside("script")}},
		{[]string{"sh", "bin/deploy.sh"}, []string{"sh", inside("bin/deploy.sh")}},
		{[]string{"bin/deploy.sh"}, []string{inside("bin/deploy.sh")}},
		{[]string{"sh", "internal/app/x.go"}, []string{"sh", "internal/app/x.go"}},
		{[]string{"tool", "."}, []string{"tool", "."}},
		{[]string{"tool", "data"}, []string{"tool", "data"}},
		{[]string{"node", "--require", "./run.sh", "app.js"}, []string{"node", "--require", "./run.sh", "app.js"}},
		{[]string{"node", "--require", "express", "app.js"}, []string{"node", "--require", "express", "app.js"}},
		{[]string{"go", "test", "./..."}, []string{"go", "test", "./..."}},
		{[]string{"docker", "compose", "-f", "./docker-compose.yml", "up"}, []string{"docker", "compose", "-f", "./docker-compose.yml", "up"}},
		{[]string{"make", "build"}, []string{"make", "build"}},
		{[]string{"make", "-C", "../api"}, []string{"make", "-C", "../api"}},
		{[]string{"bash", "-O", "extglob", "./run.sh"}, []string{"bash", "-O", "extglob", inside("run.sh")}},
		{[]string{"bash", "+O", "extglob", "./run.sh"}, []string{"bash", "+O", "extglob", inside("run.sh")}},
		{[]string{"sh", "-s", "./run.sh"}, []string{"sh", "-s", "./run.sh"}},
		{[]string{"bash", "-c", "./run.sh && ./other"}, []string{"bash", "-c", "./run.sh && ./other"}},
		{[]string{"sh", "-c", "echo", "evil.sh"}, []string{"sh", "-c", "echo", "evil.sh"}},
		{[]string{"python3", "-W", "./x", "run.py"}, []string{"python3", "-W", "./x", "run.py"}},
		{[]string{"env", "bash", "script"}, []string{"env", "bash", inside("script")}},
		{[]string{"python3.12", "script"}, []string{"python3.12", inside("script")}},
		{[]string{"npx", "./run.sh"}, []string{"npx", inside("run.sh")}},
	} {
		if got := p.Command(tc.argv); !slices.Equal(got, tc.want) {
			t.Errorf("Command(%q) = %q, want %q", tc.argv, got, tc.want)
		}
	}
}

// A program in a folder is written ./bin/x; a link out of the folder is
// refused, because its contents are not what the user allowed.
func TestCheckRefusesLinksOut(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside.sh")
	if err := os.WriteFile(outside, []byte("#!/bin/sh\necho evil\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "p")
	write(t, dir, File, "description = \"d\"\nrun = [\"./run.sh\"]\n")
	if err := os.Symlink(outside, filepath.Join(dir, "run.sh")); err != nil {
		t.Fatal(err)
	}
	if p := Load(dir); !strings.Contains(p.Err, "links outside") {
		t.Fatalf("a link out of the folder: error %q", p.Err)
	}

	inside := filepath.Join(base, "inside")
	write(t, inside, File, "description = \"d\"\nrun = [\"./run.sh\"]\n")
	write(t, inside, "real.sh", "#!/bin/sh\necho ok\n")
	if err := os.Symlink("real.sh", filepath.Join(inside, "run.sh")); err != nil {
		t.Fatal(err)
	}
	if p := Load(inside); p.Err != "" {
		t.Fatalf("a link inside the folder: error %q", p.Err)
	}

	realDir := filepath.Join(base, "real", "p")
	write(t, realDir, File, "description = \"d\"\nrun = [\"./run.sh\"]\n")
	write(t, realDir, "run.sh", "#!/bin/sh\necho ok\n")
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(realDir, alias); err != nil {
		t.Fatal(err)
	}
	if p := Load(alias); p.Err != "" {
		t.Fatalf("a plugin under a linked folder: error %q", p.Err)
	}

	noext := filepath.Join(base, "noext")
	write(t, noext, File, "description = \"d\"\nrun = [\"sh\", \"script\"]\n")
	write(t, noext, "script", "#!/bin/sh\necho ok\n")
	if p := Load(noext); p.Err != "" {
		t.Fatalf("a script without an extension: error %q", p.Err)
	}

	missing := filepath.Join(base, "missing")
	write(t, missing, File, "description = \"d\"\nrun = [\"bin/run.sh\"]\n")
	if p := Load(missing); !strings.Contains(p.Err, "not in the plugin's folder") {
		t.Fatalf("a relative program not in the folder: error %q", p.Err)
	}

	fifo := filepath.Join(base, "fifo-command")
	write(t, fifo, File, "description = \"d\"\nrun = [\"./run.sh\"]\n")
	if err := syscall.Mkfifo(filepath.Join(fifo, "run.sh"), 0o600); err != nil {
		t.Skip("mkfifo:", err)
	}
	if p := Load(fifo); !strings.Contains(p.Err, "not a regular file") {
		t.Fatalf("a FIFO program: error %q", p.Err)
	}

	dangling := filepath.Join(base, "dangling")
	write(t, dangling, File, "description = \"d\"\nrun = [\"bin/run.sh\"]\n")
	if err := os.MkdirAll(filepath.Join(dangling, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "missing-target"), filepath.Join(dangling, "bin", "run.sh")); err != nil {
		t.Fatal(err)
	}
	if p := Load(dangling); !strings.Contains(p.Err, "not in the plugin's folder") {
		t.Fatalf("a dangling program: error %q", p.Err)
	}
	for _, c := range []struct{ toml, want string }{
		{"description = \"d\"\nrun = [\"sh\", \"deploy.sh\"]\n", "would come from the folder"},
		{"description = \"d\"\nrun = [\"sh\", \"script\"]\n", "would come from the folder"},
		{"description = \"d\"\nrun = [\"./../outside.sh\"]\n", "outside the plugin's folder"},
		{"description = \"d\"\nrun = [\"sh\", \"/tmp/x.sh\"]\n", "script outside the plugin's folder"},
		{"description = \"d\"\nrun = [\"/tmp/x.sh\"]\n", "script outside the plugin's folder"},
		{"description = \"d\"\nrun = [\"node\", \"--require\", \"./../outside.js\", \"app.js\"]\n", "would come from the folder"},
	} {
		dir := filepath.Join(base, "check")
		write(t, dir, File, c.toml)
		if p := Load(dir); !strings.Contains(p.Err, c.want) {
			t.Errorf("%q: error %q, want %q", c.toml, p.Err, c.want)
		}
	}
}

// A parameter's value is bounded: it is put in F5, in the question and in
// the plugin's environment, and an agent gives it.
func TestValuesAreBounded(t *testing.T) {
	p := Plugin{Name: "p", Params: []Param{
		{Name: "note", Type: String},
		{Name: "list", Type: Multi},
	}}
	vals := Values{}
	if err := p.Set(vals, "note", strings.Repeat("x", maxValue+1)); err == nil {
		t.Fatal("a huge note was accepted")
	}
	if err := p.Set(vals, "list", strings.Repeat("x", maxListItem+1)); err == nil {
		t.Fatal("a huge list value was accepted")
	}
	for i := 0; i < maxList; i++ {
		if err := p.Set(vals, "list", "x"); err != nil {
			t.Fatalf("value %d: %v", i, err)
		}
	}
	if err := p.Set(vals, "list", "x"); err == nil {
		t.Fatal("too many values were accepted")
	}
}

// The script of a known interpreter, also when env or a versioned name
// runs it, is the plugin's; a script only the agent's folder could give
// is still refused. Code a -c flag runs is not a script file.
func TestCommandsThroughInterpreters(t *testing.T) {
	base := t.TempDir()
	for _, toml := range []string{
		"description = \"d\"\nrun = [\"env\", \"bash\", \"script\"]\n",
		"description = \"d\"\nrun = [\"python3.12\", \"script\"]\n",
		"description = \"d\"\nrun = [\"npx\", \"./script\"]\n",
		"description = \"d\"\nrun = [\"sh\", \"-c\", \"echo\", \"evil.sh\"]\n",
		"description = \"d\"\nrun = [\"bash\", \"-O\", \"extglob\", \"./script\"]\n",
	} {
		dir := filepath.Join(base, "ok")
		write(t, dir, File, toml)
		write(t, dir, "script", "#!/bin/sh\necho ok\n")
		if p := Load(dir); p.Err != "" {
			t.Errorf("%q: error %q", toml, p.Err)
		}
	}

	// env runs a script in the folder even when it is not executable.
	dir := filepath.Join(base, "noexec")
	write(t, dir, File, "description = \"d\"\nrun = [\"env\", \"bash\", \"deploy\"]\n")
	if err := os.WriteFile(filepath.Join(dir, "deploy"), []byte("#!/bin/sh\necho ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := Load(dir); p.Err != "" {
		t.Fatalf("env bash deploy without x: error %q", p.Err)
	}

	for _, toml := range []string{
		"description = \"d\"\nrun = [\"env\", \"bash\", \"evil\"]\n",
		"description = \"d\"\nrun = [\"/usr/bin/env\", \"sh\", \"evil\"]\n",
		"description = \"d\"\nrun = [\"python3.12\", \"evil\"]\n",
		"description = \"d\"\nrun = [\"npx\", \"./evil\"]\n",
	} {
		dir := filepath.Join(base, "bad")
		write(t, dir, File, toml)
		if p := Load(dir); !strings.Contains(p.Err, "would come from the folder") {
			t.Errorf("%q: error %q", toml, p.Err)
		}
	}
}

// A required parameter may not also have a default: one says an agent
// must give it, the other says it need not.
func TestRequiredWithDefault(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p")
	write(t, dir, File, "description = \"d\"\nrun = [\"true\"]\n[[params]]\nname = \"x\"\nrequired = true\ndefault = \"y\"\n")
	if p := Load(dir); !strings.Contains(p.Err, "required and has a default") {
		t.Fatalf("error %q", p.Err)
	}
}

// An allowance can be taken away again; what is gone is not an error.
func TestDisallow(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p")
	write(t, dir, File, "description = \"d\"\nrun = [\"true\"]\n")
	file := filepath.Join(t.TempDir(), "allowed.json")
	p, key, err := Fresh(Load(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := Allow(file, p, key); err != nil {
		t.Fatal(err)
	}
	if !Allowed(file, p) {
		t.Fatal("not allowed after Allow")
	}
	if err := Disallow(file, key); err != nil {
		t.Fatal(err)
	}
	if Allowed(file, p) {
		t.Fatal("still allowed after Disallow")
	}
	if err := Disallow(file, key); err != nil {
		t.Fatalf("Disallow again: %v", err)
	}
}

// An empty folder is part of the key: a script can test for it, so
// adding one waits for the user again.
func TestEmptyDirectoryChangesTheKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p")
	write(t, dir, File, "description = \"d\"\nrun = [\"true\"]\n")
	p := Load(dir)
	before, err := p.Key()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "deploy-prod"), 0o755); err != nil {
		t.Fatal(err)
	}
	after, err := p.Key()
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("an empty folder did not change the key")
	}
}

// A file in the folder with the name of a target or a data path does not
// change what a program that is not an interpreter is told.
func TestFlagsAndTargetsAreNotRewritten(t *testing.T) {
	base := t.TempDir()
	for _, toml := range []string{
		"description = \"d\"\nrun = [\"make\", \"build\"]\n",
		"description = \"d\"\nrun = [\"docker\", \"compose\", \"-f\", \"./docker-compose.yml\", \"up\"]\n",
	} {
		dir := filepath.Join(base, "p")
		write(t, dir, File, toml)
		write(t, dir, "build", "#!/bin/sh\ntrue\n")
		write(t, dir, "docker-compose.yml", "services: {}\n")
		if p := Load(dir); p.Err != "" {
			t.Errorf("%q: error %q", toml, p.Err)
		}
	}
}

// DisallowDir takes away old versions too: a user who stops allowing a
// plugin means the plugin, not just the contents they happen to see.
func TestDisallowDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p")
	write(t, dir, File, "description = \"a\"\nrun = [\"true\"]\n")
	file := filepath.Join(t.TempDir(), "allowed.json")
	a, key, err := Fresh(Load(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := Allow(file, a, key); err != nil {
		t.Fatal(err)
	}
	write(t, dir, File, "description = \"b\"\nrun = [\"true\"]\n")
	b := Load(dir)
	if Allowed(file, b) {
		t.Fatal("the new version is allowed before the user said so")
	}
	if !AllowedDir(file, dir) {
		t.Fatal("the old version is not known for the folder")
	}
	n, err := DisallowDir(file, dir)
	if err != nil || n != 1 {
		t.Fatalf("DisallowDir: %d, %v", n, err)
	}
	if AllowedDir(file, dir) || Allowed(file, a) {
		t.Fatal("an old version is still allowed")
	}
	if n, err := DisallowDir(file, dir); err != nil || n != 0 {
		t.Fatalf("DisallowDir again: %d, %v", n, err)
	}
}
