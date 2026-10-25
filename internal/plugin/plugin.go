// Package plugin reads the plugins that add to nerdr: small programs,
// each in a folder of its own with a plugin.toml saying what it is for,
// what it takes and how to run it.
//
// A plugin has a run command, which agents run (`nerdr plugin run`), and a
// ui command, a program the user uses in F5; either or both. Its
// description and parameters tell an agent what it does and what to give
// it, and the user what it is, in F5:
//
//	title       = "Docker dev"
//	description = "Rebuilds the services chosen and restarts the stack."
//	run         = ["./run.sh"]
//	ui          = ["./ui.sh"]
//	show        = ["values", "output"] # what F5 shows of an agent's run
//	open        = true             # F5 opens on it when an agent runs it
//	confirm     = false            # the user says yes to each agent's run
//	watch       = false            # run goes on, what it prints sent to the agent
//
//	[[params]]
//	name        = "repos"
//	type        = "multi"          # string (the default), bool, choice, multi
//	description = "The repositories to build."
//	options     = ["api", "web"]
//	required    = true
//
// They are the user's own, in their config folder
// (~/.config/nerdr/plugins/NAME), and the same in every tab. The folder's
// name is the plugin's.
package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// File is the file in a plugin's folder that says what the plugin is.
const File = "plugin.toml"

// The most a plugin.toml may be, and the most of its text: the file is
// shown and drawn, and an agent may have written it.
const (
	maxToml        = 1 << 20
	maxText        = 8 << 10 // a description
	maxTitle       = 200
	maxCommandArg  = 4 << 10
	maxCommandArgs = 64
	maxParams      = 64
	maxParamText   = 1 << 10
	maxOptions     = 64
	maxOptionText  = 200
	maxValue       = 64 << 10
	maxList        = 256
	maxListItem    = 4 << 10
	maxErr         = 4 << 10
)

// The types a parameter can have.
const (
	String = "string"
	Bool   = "bool"
	Choice = "choice" // one of options
	Multi  = "multi"  // any of options, or any strings without them
)

// Param is one parameter a plugin takes.
type Param struct {
	Name        string   `toml:"name" json:"name"`
	Type        string   `toml:"type" json:"type"`
	Description string   `toml:"description" json:"description,omitempty"`
	Options     []string `toml:"options" json:"options,omitempty"`
	Required    bool     `toml:"required" json:"required,omitempty"`
	Default     any      `toml:"default" json:"default,omitempty"`
}

// What F5 can show of an agent running a plugin, beyond that it runs,
// who runs it and how it ended: the plugin says which in show.
const (
	ShowValues = "values" // the parameters the agent gave
	ShowOutput = "output" // what it prints, which is what the agent gets back
	ShowUI     = "ui"     // its screen, started for the run when it does not run
)

// Plugin is one plugin, as its plugin.toml says, and where it is.
type Plugin struct {
	Name        string   `json:"name"`
	Title       string   `toml:"title" json:"title,omitempty"`
	Description string   `toml:"description" json:"description"`
	Run         []string `toml:"run" json:"run,omitempty"`
	UI          []string `toml:"ui" json:"ui,omitempty"`
	Params      []Param  `toml:"params" json:"params,omitempty"`
	Show        []string `toml:"show" json:"show,omitempty"`
	// Open is whether F5 opens on the plugin when an agent runs it; unset,
	// it does.
	Open *bool `toml:"open" json:"open,omitempty"`
	// Confirm has the user say yes to each run of an agent's, as
	// confirm_plugin_runs in nerdr's config does for every plugin.
	Confirm bool `toml:"confirm" json:"confirm,omitempty"`
	// Watch is set for a run that goes on until it is stopped, watching
	// something: `nerdr plugin run` leaves it running and returns, and
	// what it prints is sent to the agent that started it, a message at a
	// time, when the agent is idle.
	Watch bool `toml:"watch" json:"watch,omitempty"`
	// Dir is the plugin's folder.
	Dir string `json:"dir"`
	// Err is why the plugin cannot be used: its plugin.toml is wrong.
	Err string `json:"error,omitempty"`
}

// UserDir is where the user's own plugins are: plugins beside nerdr's
// config file.
func UserDir(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "plugins")
}

// Find is every plugin in the user's folder, by name.
func Find(userDir string) []Plugin {
	var out []Plugin
	entries, _ := os.ReadDir(userDir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Lstat(filepath.Join(userDir, e.Name(), File)); err != nil {
			continue
		}
		out = append(out, Load(filepath.Join(userDir, e.Name())))
	}
	return out
}

// Load reads the plugin in dir; one whose plugin.toml is wrong has Err
// set.
func Load(dir string) Plugin {
	p := Plugin{Name: filepath.Base(dir), Dir: dir}
	data, err := readToml(dir)
	if err == nil {
		var md toml.MetaData
		md, err = toml.Decode(string(data), &p)
		if err == nil {
			if un := md.Undecoded(); len(un) > 0 {
				err = fmt.Errorf("%s has nothing called %s", File, un[0])
			}
		}
	}
	if err == nil {
		err = p.check()
	}
	if err != nil {
		p.Err = err.Error()
		if len(p.Err) > maxErr {
			p.Err = p.Err[:maxErr] + "…"
		}
	}
	// The name is the folder's, whatever plugin.toml says.
	p.Name, p.Dir = filepath.Base(dir), dir
	return p
}

// readToml reads the plugin.toml in dir. It is read only when it is a
// regular file of a reasonable size: a link, a FIFO or a device is not
// something to read, and opening one can wait or read for ever.
func readToml(dir string) ([]byte, error) {
	path := filepath.Join(dir, File)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular file in the plugin's folder, not a link or a special file", File)
	}
	if info.Size() > maxToml {
		return nil, fmt.Errorf("%s is %d bytes; a plugin's is at most %d", File, info.Size(), maxToml)
	}
	return os.ReadFile(path)
}

var (
	validName  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	validParam = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
)

// check is what is wrong with p, if anything.
func (p *Plugin) check() error {
	switch {
	case !validName.MatchString(p.Name):
		return fmt.Errorf("the folder's name, %q, is the plugin's: use lower case letters, digits, '.', '_' and '-'", p.Name)
	case strings.TrimSpace(p.Description) == "":
		return errors.New("description is missing: say what the plugin does, and when an agent should run it")
	case len(p.Description) > maxText:
		return fmt.Errorf("description is %d bytes; keep it under %d", len(p.Description), maxText)
	case len(p.Title) > maxTitle:
		return fmt.Errorf("title is %d bytes; keep it under %d", len(p.Title), maxTitle)
	case len(p.Run) == 0 && len(p.UI) == 0:
		return errors.New("run and ui are both missing: a plugin has one or both")
	case len(p.Run) > 0 && p.Run[0] == "", len(p.UI) > 0 && p.UI[0] == "":
		return errors.New("run and ui start with the program to run")
	case len(p.Run) > maxCommandArgs, len(p.UI) > maxCommandArgs:
		return fmt.Errorf("run and ui take at most %d words", maxCommandArgs)
	case p.Watch && len(p.Run) == 0:
		return errors.New("watch is set, but there is no run to watch with")
	}
	for _, argv := range [][]string{p.Run, p.UI} {
		for _, a := range argv {
			if len(a) > maxCommandArg {
				return fmt.Errorf("a word of a command is %d bytes; at most %d", len(a), maxCommandArg)
			}
		}
		if err := p.checkCommand(argv); err != nil {
			return err
		}
	}
	for _, s := range p.Show {
		switch {
		case !slices.Contains([]string{ShowValues, ShowOutput, ShowUI}, s):
			return fmt.Errorf("show: %q is not values, output or ui", s)
		case s == ShowUI && len(p.UI) == 0:
			return errors.New("show has ui, but there is no ui to show")
		}
	}
	if len(p.Params) > maxParams {
		return fmt.Errorf("there are %d parameters; a plugin has at most %d", len(p.Params), maxParams)
	}
	seen := map[string]bool{}
	env := map[string]bool{}
	for i := range p.Params {
		q := &p.Params[i]
		if q.Type == "" {
			q.Type = String
		}
		name := strings.ToUpper(strings.ReplaceAll(q.Name, "-", "_"))
		switch {
		case !validParam.MatchString(q.Name):
			return fmt.Errorf("parameter %q: a name is lower case letters, digits, '_' and '-'", q.Name)
		case seen[q.Name] || q.Name == "json":
			return fmt.Errorf("parameter %q is there twice, or is taken", q.Name)
		case env[name]:
			return fmt.Errorf("parameter %q has the same environment name as another, %s", q.Name, "NERDR_PARAM_"+name)
		case !slices.Contains([]string{String, Bool, Choice, Multi}, q.Type):
			return fmt.Errorf("parameter %q: type is string, bool, choice or multi, not %q", q.Name, q.Type)
		case q.Type == Choice && len(q.Options) == 0:
			return fmt.Errorf("parameter %q: a choice needs options", q.Name)
		case q.Required && q.Default != nil:
			return fmt.Errorf("parameter %q is required and has a default: give it one or the other", q.Name)
		case len(q.Description) > maxParamText:
			return fmt.Errorf("parameter %q: description is %d bytes; keep it under %d", q.Name, len(q.Description), maxParamText)
		case len(q.Options) > maxOptions:
			return fmt.Errorf("parameter %q: %d options; at most %d", q.Name, len(q.Options), maxOptions)
		}
		for _, o := range q.Options {
			if len(o) > maxOptionText {
				return fmt.Errorf("parameter %q: option %q is %d bytes; at most %d", q.Name, o, len(o), maxOptionText)
			}
		}
		seen[q.Name] = true
		env[name] = true
		if q.Default != nil {
			v, err := q.value(q.Default)
			if err != nil {
				return fmt.Errorf("parameter %q: default: %v", q.Name, err)
			}
			q.Default = v
		}
	}
	return nil
}

// checkCommand checks the words of a run or ui: a program with a folder
// in its name, and a script an argument names, are the plugin's own, in
// its folder, and not a link out of it. A program without a folder is
// looked up on PATH. Other arguments, such as data files, may name the
// folder the agent runs in.
func (p *Plugin) checkCommand(argv []string) error {
	if len(argv) == 0 {
		return nil
	}
	program := argv[0]
	resolved := p.Command(argv)[0]
	switch {
	case strings.HasPrefix(program, "./") || strings.HasPrefix(program, "../"):
		if filepath.IsAbs(resolved) && !p.within(resolved) {
			return fmt.Errorf("%s is outside the plugin's folder: use a name on PATH, or a file in the folder", program)
		}
	case filepath.IsAbs(program):
		// Absolute: the question shows it as it is. A tool such as
		// /bin/sh is for the user to trust; a script outside the folder
		// is refused like any other script.
		if isScript(program) && !p.within(program) {
			return fmt.Errorf("%s is a script outside the plugin's folder: keep what it runs in its folder", program)
		}
	case strings.ContainsRune(program, filepath.Separator):
		if !filepath.IsAbs(resolved) {
			return fmt.Errorf("%q is not in the plugin's folder: write ./%s for a program there, or a name on PATH", program, program)
		}
	}
	if err := p.checkRegular(resolved); err != nil {
		return err
	}
	if err := p.checkReal(resolved); err != nil {
		return err
	}
	script, spec, isInterp := interpreterScript(argv)
	codeMode := isInterp && script < 0
	for i, a := range p.Command(argv) {
		if i == 0 || a == "" || strings.HasPrefix(argv[i], "-") {
			continue
		}
		if !isInterp {
			// Another program's arguments are data or targets it reads
			// from the folder it runs in; no file argument is ours to
			// check. The program itself was checked above.
			continue
		}
		if codeMode {
			// The interpreter runs code; what follows is its words, not
			// a script file.
			continue
		}
		code := spec.code[argv[i-1]]
		skipArg := code || i > 1 && spec.value[argv[i-1]]
		afterScript := script >= 0 && i > script
		if isScript(a) || i == script {
			if !skipArg && !afterScript {
				if !filepath.IsAbs(a) {
					// Command makes a script in the folder absolute; what
					// is left relative is one only the agent's folder
					// could give.
					return fmt.Errorf("%q would come from the folder the agent runs in: put it in the plugin's folder and write ./%s", argv[i], argv[i])
				}
				if !p.within(a) {
					return fmt.Errorf("%s is a script outside the plugin's folder: keep what it runs in its folder", a)
				}
			}
			if filepath.IsAbs(a) && p.within(a) {
				if err := p.checkRegular(a); err != nil {
					return err
				}
			}
		}
		if skipArg || afterScript {
			continue
		}
		if err := p.checkReal(a); err != nil {
			return err
		}
	}
	return nil
}

// checkRegular refuses a program or script in the plugin's folder that is
// not a regular file: a FIFO or a device would wait or read for ever.
func (p *Plugin) checkRegular(path string) error {
	if !filepath.IsAbs(path) || !p.within(path) {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil // missing: the program will say so, if it is run
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	return nil
}

// checkReal refuses a path in the plugin's folder that is, or goes
// through, a link out of it: what runs then is not what the folder's hash
// covers.
func (p *Plugin) checkReal(path string) error {
	if !filepath.IsAbs(path) || !p.within(path) {
		return nil
	}
	realDir, err := filepath.EvalSymlinks(p.Dir)
	if err != nil {
		return nil
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil // missing: the program will say so, if it is run
	}
	realDir, err = filepath.Abs(realDir)
	if err != nil {
		return nil
	}
	real, err = filepath.Abs(real)
	if err != nil {
		return nil
	}
	rel, err := filepath.Rel(realDir, real)
	if err != nil || !filepath.IsLocal(rel) {
		return fmt.Errorf("%s links outside the plugin's folder: keep what it runs in its folder", path)
	}
	return nil
}

// within reports whether path is p's folder or beneath it, by its name:
// links are followed by checkReal.
func (p *Plugin) within(path string) bool {
	dir, err := filepath.Abs(p.Dir)
	if err != nil {
		return false
	}
	path, err = filepath.Abs(filepath.Clean(path))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && filepath.IsLocal(rel)
}

// Shows reports whether F5 shows what of an agent's run of p.
func (p Plugin) Shows(what string) bool { return slices.Contains(p.Show, what) }

// Opens reports whether F5 opens on p when an agent runs it.
func (p Plugin) Opens() bool { return p.Open == nil || *p.Open }

// Command is argv, a plugin's run or ui, to run: a program named with a
// path of its own, ./run.sh, is in the plugin's folder, and so is the
// script an interpreter or runner runs. Its other arguments are left as
// written: a data path such as ./... or -f ./docker-compose.yml must mean
// what it always did, in the folder the plugin runs in.
func (p Plugin) Command(argv []string) []string {
	out := slices.Clone(argv)
	script, _, isInterp := interpreterScript(argv)
	for i, a := range out {
		if a == "" || filepath.IsAbs(a) {
			continue
		}
		if i == 0 {
			// The program: ./run.sh is the plugin's, and so is bin/run.sh
			// when that file is there. A bare name is on PATH.
			if strings.HasPrefix(a, "./") || strings.HasPrefix(a, "../") {
				out[i] = filepath.Join(p.Dir, a)
				continue
			}
			if strings.ContainsRune(a, filepath.Separator) {
				if _, err := os.Stat(filepath.Join(p.Dir, a)); err == nil {
					out[i] = filepath.Join(p.Dir, a)
				}
			}
			continue
		}
		if isInterp {
			// Only the script an interpreter runs is the plugin's. Its
			// other words are code, options and data, which may name the
			// folder the agent runs in; none are rewritten.
			if script >= 0 && i == script {
				if path, ok := p.scriptPath(a); ok {
					out[i] = path
				}
			}
			continue
		}
		// Another program's arguments are left as written: a file beside
		// the plugin.toml with the same name as a target, such as make's
		// build, is not what the user meant.
	}
	return out
}

// scriptPath is a in the plugin's folder, if it is there; links are
// followed later by checkReal.
func (p Plugin) scriptPath(a string) (string, bool) {
	path := filepath.Join(p.Dir, a)
	if !p.within(path) {
		return "", false
	}
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	return path, true
}

// scriptExts are the extensions of files an interpreter runs. A word of
// a command with one is a script, not a data file: it must be in the
// plugin's folder, so that it is what the user read.
var scriptExts = map[string]bool{
	".sh": true, ".bash": true, ".zsh": true, ".fish": true, ".py": true, ".pl": true,
	".rb": true, ".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".php": true,
	".lua": true, ".ps1": true, ".awk": true, ".sed": true,
}

// isScript reports whether a word names a script, rather than data or a
// shell word with spaces in it.
func isScript(a string) bool {
	if strings.ContainsAny(a, " \t\r\n;|&<>()$`\"'") {
		return false
	}
	return scriptExts[strings.ToLower(filepath.Ext(a))]
}

// looksLikeFile reports whether a word of a command may name a file the
// plugin's own: a path with a folder in it, or a script's extension.
func looksLikeFile(a string) bool {
	return strings.ContainsRune(a, filepath.Separator) || isScript(a)
}

// interpreter is a program that runs a script named in its first word
// that is neither a flag nor the value of one. code and value name the
// flags whose next word is code, and a value, rather than a script.
type interpreter struct {
	code, value map[string]bool
}

func words(ss ...string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}

// interpreters are the common ones, so that `sh script` uses the
// script beside the plugin.toml even when it has no extension, and one
// only an agent's folder could give is refused.
var interpreters = map[string]interpreter{
	"sh":      {code: words("-c", "-s"), value: words("-o", "+o", "-O", "+O", "--rcfile", "--init-file")},
	"bash":    {code: words("-c", "-s"), value: words("-o", "+o", "-O", "+O", "--rcfile", "--init-file")},
	"dash":    {code: words("-c", "-s"), value: words("-o", "+o", "-O", "+O")},
	"zsh":     {code: words("-c", "-s"), value: words("-o", "+o", "-O", "+O")},
	"ksh":     {code: words("-c", "-s"), value: words("-o", "+o", "-O", "+O")},
	"ash":     {code: words("-c", "-s"), value: words("-o", "+o", "-O", "+O")},
	"fish":    {code: words("-c"), value: words("-O")},
	"python":  {code: words("-c", "-m"), value: words("-W", "-X")},
	"python2": {code: words("-c", "-m"), value: words("-W", "-X")},
	"python3": {code: words("-c", "-m"), value: words("-W", "-X")},
	"perl":    {code: words("-e", "-E"), value: words("-I", "-M", "-m")},
	"ruby":    {code: words("-e"), value: words("-I", "-r")},
	"node":    {code: words("-e", "-p", "--eval", "--print"), value: words("-r", "--require", "--loader", "--experimental-loader", "--import", "--run")},
	"nodejs":  {code: words("-e", "-p", "--eval", "--print"), value: words("-r", "--require", "--loader", "--experimental-loader", "--import", "--run")},
	"lua":     {code: words("-e"), value: words("-l")},
	"php":     {code: words("-r"), value: words("-c", "-d")},
}

// interpreterFor is the specification of the interpreter called name,
// including the versions of it: python3.12, node20, ruby3.2.
func interpreterFor(name string) (interpreter, bool) {
	name = strings.ToLower(name)
	if spec, ok := interpreters[name]; ok {
		return spec, true
	}
	for _, base := range []string{"python", "node", "nodejs", "perl", "ruby", "php", "lua"} {
		rest, ok := strings.CutPrefix(name, base)
		if !ok || rest == "" {
			continue
		}
		version := true
		for _, r := range rest {
			if (r < '0' || r > '9') && r != '.' {
				version = false
				break
			}
		}
		if version {
			return interpreters[base], true
		}
	}
	return interpreter{}, false
}

// isRunner reports whether a program runs a package or command it is
// given: npx ./tool, bunx ./tool.
func isRunner(name string) bool {
	switch strings.ToLower(name) {
	case "npx", "pnpx", "bunx":
		return true
	}
	return false
}

// envProgram is the index of the program env runs: its options and
// NAME=value words come first.
func envProgram(argv []string, from int) (int, bool) {
	for i := from; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "--":
			return i + 1, i+1 < len(argv)
		case a == "-u" || a == "--unset" || a == "-S" || a == "--split-string":
			i++
			continue
		case strings.HasPrefix(a, "--unset=") || strings.HasPrefix(a, "--split-string="):
			continue
		case strings.HasPrefix(a, "-"):
			continue
		case strings.Contains(a, "="):
			continue // NAME=value
		default:
			return i, true
		}
	}
	return 0, false
}

// scriptAfter is the index of the script spec runs from from, or -1 when
// it runs code or has none.
func scriptAfter(spec interpreter, argv []string, from int) int {
	for i := from; i < len(argv); i++ {
		a := argv[i]
		if spec.code[a] {
			return -1 // it runs code, and what follows is its words
		}
		if spec.value[a] {
			i++
			continue
		}
		if strings.HasPrefix(a, "-") || strings.HasPrefix(a, "+") {
			continue
		}
		return i
	}
	return -1
}

// runnerScript is the index of the file a runner such as npx runs, if
// the first word it is given is a path or a script; -1 for a package
// name.
func runnerScript(argv []string, from int) int {
	for i := from; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "-p" || a == "--package":
			i++
			continue
		case strings.HasPrefix(a, "--package="):
			continue
		case a == "-c" || a == "--call":
			return -1 // it runs code
		case strings.HasPrefix(a, "-"):
			continue
		case strings.Contains(a, "="):
			continue
		case looksLikeFile(a):
			return i
		default:
			return -1 // a package name, not a file
		}
	}
	return -1
}

// interpreterScript is the index of the script argv's program runs, if
// that program, or the program env runs, is an interpreter and its first
// word that is not a flag is a script; -1 when it runs code or has no
// script. The common versioned names and runners are looked through too.
func interpreterScript(argv []string) (script int, spec interpreter, found bool) {
	start := 0
	for start < len(argv) {
		name := strings.ToLower(filepath.Base(argv[start]))
		if name != "env" {
			break
		}
		next, ok := envProgram(argv, start+1)
		if !ok {
			return -1, interpreter{}, false
		}
		start = next
	}
	if start >= len(argv) {
		return -1, interpreter{}, false
	}
	name := strings.ToLower(filepath.Base(argv[start]))
	if s, ok := interpreterFor(name); ok {
		return scriptAfter(s, argv, start+1), s, true
	}
	if isRunner(name) {
		return runnerScript(argv, start+1), interpreter{}, true
	}
	if start > 0 && (isScript(argv[start]) || strings.HasPrefix(argv[start], "./") || strings.HasPrefix(argv[start], "../")) {
		// env ./script: the word after env is a script.
		return start, interpreter{}, true
	}
	return -1, interpreter{}, false
}

// Values are the parameters given to a plugin, by name: a string, a bool
// or a list of strings, as each one's type says.
type Values map[string]any

// value is v as the value of q, or why it is not one.
func (q Param) value(v any) (any, error) {
	switch q.Type {
	case Bool:
		switch b := v.(type) {
		case bool:
			return b, nil
		case string:
			if x, err := strconv.ParseBool(b); err == nil {
				return x, nil
			}
		}
		return nil, fmt.Errorf("%v is not true or false", v)
	case Multi:
		var list []string
		switch x := v.(type) {
		case string:
			for _, s := range strings.Split(x, ",") {
				if s = strings.TrimSpace(s); s != "" {
					list = append(list, s)
				}
			}
		case []string:
			list = x
		case []any:
			for _, s := range x {
				str, ok := s.(string)
				if !ok {
					return nil, fmt.Errorf("%v is not a list of words", v)
				}
				list = append(list, str)
			}
		default:
			return nil, fmt.Errorf("%v is not a list of words", v)
		}
		if len(list) > maxList {
			return nil, fmt.Errorf("%d values; at most %d", len(list), maxList)
		}
		for _, s := range list {
			if len(s) > maxListItem {
				return nil, fmt.Errorf("a value is %d bytes; at most %d", len(s), maxListItem)
			}
			if len(q.Options) > 0 && !slices.Contains(q.Options, s) {
				return nil, fmt.Errorf("%q is not one of %s", s, strings.Join(q.Options, ", "))
			}
		}
		return list, nil
	default:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%v is not text", v)
		}
		if len(s) > maxValue {
			return nil, fmt.Errorf("%d bytes; at most %d", len(s), maxValue)
		}
		if q.Type == Choice && !slices.Contains(q.Options, s) {
			return nil, fmt.Errorf("%q is not one of %s", s, strings.Join(q.Options, ", "))
		}
		return s, nil
	}
}

// Param is p's parameter called name, if it has one.
func (p Plugin) Param(name string) (Param, bool) {
	for _, q := range p.Params {
		if q.Name == name {
			return q, true
		}
	}
	return Param{}, false
}

// Set checks v as the value of p's parameter name, and puts it in vals.
func (p Plugin) Set(vals Values, name string, v any) error {
	q, ok := p.Param(name)
	if !ok {
		return fmt.Errorf("%s takes no parameter %q", p.Name, name)
	}
	x, err := q.value(v)
	if err != nil {
		return fmt.Errorf("--%s: %v", name, err)
	}
	if prev, ok := vals[name].([]string); ok && q.Type == Multi {
		x = append(prev, x.([]string)...) // --repos a --repos b
		if len(x.([]string)) > maxList {
			return fmt.Errorf("--%s: %d values; at most %d", name, len(x.([]string)), maxList)
		}
	}
	vals[name] = x
	return nil
}

// Complete fills in the defaults of the parameters not given, and returns
// the required ones still missing.
func (p Plugin) Complete(vals Values) []Param {
	var missing []Param
	for _, q := range p.Params {
		if _, ok := vals[q.Name]; ok {
			continue
		}
		switch {
		case q.Default != nil:
			vals[q.Name] = q.Default
		case q.Required:
			missing = append(missing, q)
		case q.Type == Bool:
			vals[q.Name] = false
		}
	}
	return missing
}

// Env is the variables a plugin's program is given: which plugin it is,
// its folder, and each parameter as NERDR_PARAM_NAME (a list joined with
// commas, a bool true or false).
func (p Plugin) Env(vals Values) []string {
	env := []string{"NERDR_PLUGIN=" + p.Name, "NERDR_PLUGIN_DIR=" + p.Dir}
	names := make([]string, 0, len(vals))
	for k := range vals {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		var s string
		switch v := vals[k].(type) {
		case []string:
			s = strings.Join(v, ",")
		default:
			s = fmt.Sprint(v)
		}
		env = append(env, "NERDR_PARAM_"+strings.ToUpper(strings.ReplaceAll(k, "-", "_"))+"="+s)
	}
	return env
}

// RunCmd is the command that runs p with vals, in dir: the values also as
// JSON on its standard input.
func (p Plugin) RunCmd(vals Values, dir string) *exec.Cmd {
	argv := p.Command(p.Run)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), p.Env(vals)...)
	b, _ := json.Marshal(vals)
	cmd.Stdin = strings.NewReader(string(b) + "\n")
	return cmd
}

// Usage is how an agent runs p: nerdr plugin run NAME and its parameters.
func (p Plugin) Usage() string {
	parts := []string{"nerdr plugin run " + p.Name}
	for _, q := range p.Params {
		var s string
		switch q.Type {
		case Bool:
			s = "--" + q.Name
		case Multi:
			s = "--" + q.Name + " A,B"
		case Choice:
			s = "--" + q.Name + " " + strings.Join(q.Options, "|")
		default:
			s = "--" + q.Name + " TEXT"
		}
		if !q.Required {
			s = "[" + s + "]"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " ")
}

// About is a parameter's line for the user and the agent: its type, what
// it is, and what it can be.
func (q Param) About() string {
	s := q.Type
	if q.Required {
		s += ", required"
	}
	if q.Description != "" {
		s += ": " + q.Description
	}
	if len(q.Options) > 0 {
		s += " (" + strings.Join(q.Options, ", ") + ")"
	}
	if q.Default != nil {
		d := fmt.Sprint(q.Default)
		if l, ok := q.Default.([]string); ok {
			d = strings.Join(l, ",")
		}
		s += " · default " + d
	}
	return s
}

// What the user allows of a plugin.
//
// A plugin is code an agent may have written, in the user's folder, when
// a sandbox lets it write there or it has none. Its ui
// is started by nerdr, outside every sandbox, and its run is run by any
// agent whose task its description seems to fit, one outside a sandbox
// among them. So no plugin is started or run until the user has allowed
// it as it is now, in F5, and a change to anything in its folder asks
// again, as .nerdr.toml's setup does: an agent that makes a plugin cannot
// have it run before the user has seen it.
//
// What is allowed is a Key, whatever folder it is in.

// maxHashed is the most of a plugin's folder its key is made of; one
// bigger cannot be allowed.
const maxHashed = 16 << 20

// Key is a hash of everything in p's folder: the names of its files,
// links and empty folders, and what is in them. A file outside the folder
// it uses is not.
func (p Plugin) Key() (string, error) {
	h := sha256.New()
	total := 0
	err := filepath.WalkDir(p.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(p.Dir, path)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			to, err := os.Readlink(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(h, "link %q %q\n", rel, to)
		case d.Type().IsDir() && rel != ".":
			// An empty folder is a marker a script can test for: it is
			// part of what the user allowed too.
			entries, err := os.ReadDir(path)
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				fmt.Fprintf(h, "empty %q\n", rel)
			}
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			if total += int(info.Size()); total > maxHashed {
				return fmt.Errorf("the plugin's folder holds more than %d MB", maxHashed>>20)
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			fmt.Fprintf(h, "file %q %d %o\n", rel, info.Size(), info.Mode().Perm()&0o111)
			if _, err := io.Copy(h, f); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// AllowedFile is where the plugins the user allowed are kept: beside
// the sessions, in stateDir's parent.
func AllowedFile(stateDir string) string {
	return filepath.Join(filepath.Dir(stateDir), "plugins-allowed.json")
}

// Allowed reports whether the user allowed p as it is now, as kept in
// file.
func Allowed(file string, p Plugin) bool {
	key, err := p.Key()
	return err == nil && AllowedKey(file, key)
}

// AllowedKey reports whether the user allowed the plugin whose Key is key.
func AllowedKey(file, key string) bool {
	_, ok := loadAllowed(file)[key]
	return ok && key != ""
}

// Fresh is p read again, with the key of what was read: the folder is
// hashed before and after plugin.toml is read, so the plugin and the key
// are of the same files. An error says the folder changed meanwhile, or
// cannot be hashed.
func Fresh(p Plugin) (Plugin, string, error) {
	before, err := p.Key()
	if err != nil {
		return p, "", err
	}
	q := Load(p.Dir)
	after, err := q.Key()
	if err != nil {
		return p, "", err
	}
	if before != after {
		return p, "", errors.New("its folder changed while it was read")
	}
	return q, after, nil
}

// Allow keeps in file that the user allows p as key, a Key of it, says:
// the key of what the user was shown, not of what is there by the time
// they answer.
func Allow(file string, p Plugin, key string) error {
	all := loadAllowed(file)
	all[key] = filepath.Clean(p.Dir) // where it was, for whoever reads the file
	return saveAllowed(file, all)
}

// AllowedDir reports whether the user allowed any version of the plugin
// in dir.
func AllowedDir(file, dir string) bool {
	dir = filepath.Clean(dir)
	for _, where := range loadAllowed(file) {
		if filepath.Clean(where) == dir {
			return true
		}
	}
	return false
}

// Disallow takes the user's allowance of key away: agents and screens
// wait for a new one. An allowance already gone is not an error.
func Disallow(file, key string) error {
	all := loadAllowed(file)
	if _, ok := all[key]; !ok {
		return nil
	}
	delete(all, key)
	return saveAllowed(file, all)
}

// DisallowDir takes away every allowance of the plugin in dir — every
// version of it the user allowed — and says how many there were.
func DisallowDir(file, dir string) (int, error) {
	all := loadAllowed(file)
	dir = filepath.Clean(dir)
	n := 0
	for key, where := range all {
		if filepath.Clean(where) == dir {
			delete(all, key)
			n++
		}
	}
	if n == 0 {
		return 0, nil
	}
	return n, saveAllowed(file, all)
}

func saveAllowed(file string, all map[string]string) error {
	b, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".plugins-allowed-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

func loadAllowed(file string) map[string]string {
	all := map[string]string{}
	if b, err := os.ReadFile(file); err == nil {
		_ = json.Unmarshal(b, &all)
	}
	return all
}
