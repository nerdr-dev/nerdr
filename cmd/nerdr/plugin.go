package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/clean"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/plugin"
	"github.com/nerdr-dev/nerdr/internal/state"
)

const pluginUsage = `usage: nerdr plugin list [--json]
       nerdr plugin run NAME [--PARAM VALUE …] [--json PARAMS|-]
       nerdr plugin stop NAME|RUN

Plugins are programs the user added to nerdr, each with a description
saying what it does and when to run it, and the parameters it takes.

list prints every plugin there is, the user's own, with its description,
its parameters and how to run it; --json the same as JSON.

run runs plugin NAME and prints what it prints, with its exit status.
Give each parameter as --NAME VALUE: a list as A,B or the flag again, a
bool as the flag alone (--clean) or --clean=false. Or give them all as a
JSON object with --json, - to read it from stdin. A required parameter
missing is an error: ask the user for it rather than guess. A plugin
without run is for the user only, in nerdr's F5.

In a nerdr tab, nerdr runs it, as the user and outside any sandbox you
are in, in the folder you run this in, and the user sees it run in F5.
That is what the user allowed it for: do not try to do what a plugin is
for some other way when you cannot, and do not give it parameters to do
more than the user asked.

A relative program with a folder in its name must be in the plugin's
folder: write ./name there, or bin/name when it is there, and use a name
on PATH for a program. A relative program outside the folder is refused.
A script run by a known interpreter or runner must also be in the folder;
a data file may be elsewhere. An absolute path to a tool is used as it
is, and is shown in the question.

This is a best-effort guard. A tool such as make, npm or docker, a
wrapper such as timeout or xargs, a package or module lookup such as
npx's or python -m's, or a script name built in a command reads files and
targets from the folder you run the command in; that folder is not part
of what the user allowed. Put a script in the plugin's folder and write
./name.

A plugin runs only once the user has allowed it as it is now, in nerdr's
F5 (a): one made or changed, by you or another agent, waits for them. Tell
the user; do not copy or change it to get round that. The user may also
be asked to say yes to each run, seeing its parameters: run then waits
for them, up to about a minute and a half, and a no is final for that
run: do not run it again unasked.

A plugin that watches (list says so) goes on: run starts it and returns
at once, and what it prints comes to you as messages from nerdr, when
you are idle, with nothing to wait for or poll. stop stops it, by its
name or the run's id; nerdr stops it too when you are gone, or the user
stops it in F5.
`

// runPlugin is `nerdr plugin`.
func runPlugin(args []string) int {
	if len(args) == 0 || helpAsked(args) {
		fmt.Fprint(os.Stderr, pluginUsage)
		return 2
	}
	switch args[0] {
	case "list", "ls":
		return pluginList(args[1:])
	case "run":
		return pluginRun(args[1:])
	case "stop":
		return pluginStop(args[1:])
	}
	fmt.Fprintf(os.Stderr, "nerdr plugin: no command %q\n\n%s", args[0], pluginUsage)
	return 2
}

func findPlugins() []plugin.Plugin {
	return plugin.Find(plugin.UserDir(config.Path()))
}

// pluginAllowed reports whether the user allowed the plugin p whose key
// is key. An agent asks the nerdr running its tab, which knows: a sandbox
// may keep the agent from the file it is kept in. The user, in a shell,
// reads that file.
func pluginAllowed(name string, p plugin.Plugin, key string) bool {
	if _, _, inTab := agentTab(); !inTab {
		return plugin.AllowedKey(plugin.AllowedFile(state.Dir()), key)
	}
	answer, ok := ask(name, hooks.Plugin, map[string]any{"what": hooks.PluginCheck, "name": p.Dir, "text": key})
	return ok && answer == "allowed"
}

// listed is a plugin as nerdr plugin list gives it.
type listed struct {
	plugin.Plugin
	Allowed bool `json:"allowed"`
}

// listing is every plugin, each with whether the user allowed it as it is.
func listing() []listed {
	var out []listed
	for _, p := range findPlugins() {
		l := listed{Plugin: p}
		if p.Err == "" {
			if q, key, err := plugin.Fresh(p); err == nil {
				l.Plugin = q
				l.Allowed = pluginAllowed("nerdr plugin list", q, key)
			}
		}
		out = append(out, l)
	}
	return out
}

func pluginList(args []string) int {
	asJSON := len(args) == 1 && args[0] == "--json"
	if len(args) > 0 && !asJSON {
		fmt.Fprint(os.Stderr, pluginUsage)
		return 2
	}
	all := listing()
	if asJSON {
		if all == nil {
			all = []listed{}
		}
		b, _ := json.MarshalIndent(all, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	if len(all) == 0 {
		fmt.Printf("No plugins. The user's are in %s.\n", clean.Line(plugin.UserDir(config.Path())))
		return 0
	}
	for i, p := range all {
		if i > 0 {
			fmt.Println()
		}
		head := clean.Line(p.Name)
		if p.Title != "" {
			head += " — " + clean.Line(p.Title)
		}
		fmt.Printf("%s  (%s)\n", head, clean.Line(p.Dir))
		if p.Err != "" {
			fmt.Println("  broken:", clean.Line(p.Err))
			continue
		}
		for _, l := range strings.Split(strings.TrimSpace(clean.Text(p.Description)), "\n") {
			fmt.Println("  " + l)
		}
		if len(p.Run) == 0 {
			fmt.Println("  for the user only, in nerdr's F5: agents cannot run it")
			continue
		}
		if !p.Allowed {
			fmt.Println("  not allowed as it is: it runs once the user allows it in nerdr's F5")
		}
		fmt.Println("  " + clean.Line(p.Usage()))
		if p.Watch {
			fmt.Println("  watches: run starts it in the background, what it prints comes to you as messages, " +
				"and nerdr plugin stop " + clean.Line(p.Name) + " stops it")
		}
		for _, q := range p.Params {
			fmt.Printf("    --%s  %s\n", q.Name, clean.Line(q.About()))
		}
	}
	return 0
}

func pluginRun(args []string) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprint(os.Stderr, pluginUsage)
		return 2
	}
	name := args[0]
	var p *plugin.Plugin
	all := findPlugins()
	for i := range all {
		if all[i].Name == name {
			p = &all[i]
		}
	}
	switch {
	case p == nil:
		var names []string
		for _, q := range all {
			names = append(names, clean.Line(q.Name))
		}
		fmt.Fprintf(os.Stderr, "nerdr plugin run: there is no plugin %q; those there are: %s\n", clean.Line(name), orNone(names))
		return 1
	case p.Err != "":
		fmt.Fprintf(os.Stderr, "nerdr plugin run: %s is broken: %s\n", clean.Line(name), clean.Line(p.Err))
		return 1
	case len(p.Run) == 0:
		fmt.Fprintf(os.Stderr, "nerdr plugin run: %s has no run: it is for the user, in nerdr's F5\n", clean.Line(name))
		return 1
	}
	// What is checked and run is the plugin as it is now, and as the user
	// allowed it.
	q, key, err := plugin.Fresh(*p)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nerdr plugin run: %s: %v\n", clean.Line(name), clean.Line(err.Error()))
		return 1
	}
	p = &q
	// The values come first: the user asked to say yes to the run sees
	// them.
	vals, err := pluginValues(*p, args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "nerdr plugin run: %s\n\n%s\n", clean.Line(err.Error()), clean.Line(p.Usage()))
		return 2
	}
	if missing := p.Complete(vals); len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "nerdr plugin run: %s needs more: ask the user for it, then run it again.\n", clean.Line(name))
		for _, q := range missing {
			fmt.Fprintf(os.Stderr, "  --%s  %s\n", q.Name, clean.Line(q.About()))
		}
		return 2
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "nerdr plugin run: %s: the folder you run in: %s\n", clean.Line(name), clean.Line(err.Error()))
		return 1
	}
	if _, _, inTab := agentTab(); inTab {
		// nerdr runs it: see pluginrun.go.
		return remoteRun(*p, vals, cwd)
	}
	// The user, in a shell or outside nerdr, runs it as themselves, here.
	if !plugin.AllowedKey(plugin.AllowedFile(state.Dir()), key) {
		fmt.Fprintf(os.Stderr, "nerdr plugin run: %s did not run: %s\n", clean.Line(name), whyNot[notAllowed])
		return 1
	}
	if now, err := p.Key(); err != nil || now != key {
		fmt.Fprintf(os.Stderr, "nerdr plugin run: %s changed while it was started: run it again\n", clean.Line(name))
		return 1
	}
	// This is the user's own shell, not the app's pinned start: the key
	// is checked as near to the run as this path can, but without the
	// child's second check. A change in between is not seen here.
	cmd := p.RunCmd(vals, cwd)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "nerdr plugin run: %s: %v\n", clean.Line(name), clean.Line(err.Error()))
		return 1
	}
	return 0
}

// pluginValues are the parameters args give p: --NAME VALUE, --NAME=VALUE,
// --FLAG for a bool, or --json with them all.
func pluginValues(p plugin.Plugin, args []string) (plugin.Values, error) {
	vals := plugin.Values{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, ok := strings.CutPrefix(a, "--")
		if !ok {
			if name, ok = strings.CutPrefix(a, "-"); !ok || name == "" {
				return nil, fmt.Errorf("%q is not a parameter: give them as --NAME VALUE", a)
			}
		}
		name, value, hasValue := strings.Cut(name, "=")
		if name == "json" {
			if !hasValue {
				if i+1 >= len(args) {
					return nil, errors.New("--json needs the parameters as a JSON object, or -")
				}
				i++
				value = args[i]
			}
			if value == "-" {
				b, err := io.ReadAll(os.Stdin)
				if err != nil {
					return nil, err
				}
				value = string(b)
			}
			var obj map[string]any
			if err := json.Unmarshal([]byte(value), &obj); err != nil {
				return nil, fmt.Errorf("--json: %v", err)
			}
			for k, v := range obj {
				if err := p.Set(vals, k, v); err != nil {
					return nil, err
				}
			}
			continue
		}
		q, ok := p.Param(name)
		if !ok {
			return nil, fmt.Errorf("%s takes no parameter --%s", p.Name, name)
		}
		if !hasValue {
			if q.Type == plugin.Bool {
				value = "true"
			} else {
				if i+1 >= len(args) {
					return nil, fmt.Errorf("--%s needs a value", name)
				}
				i++
				value = args[i]
			}
		}
		if err := p.Set(vals, name, value); err != nil {
			return nil, err
		}
	}
	return vals, nil
}

func orNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
