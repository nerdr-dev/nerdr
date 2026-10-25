package main

import (
	"flag"
	"strings"
	"testing"
)

func TestWaitFor(t *testing.T) {
	all := []waitAgent{{Num: 1, State: "working"}, {Num: 2, State: "idle"}, {Num: 3, State: "idle", Waiting: 1}, {Num: 4, State: "blocked"}}
	if _, done, _ := waitFor(all, []int{2, 4}); !done {
		t.Fatal("idle and blocked are done")
	}
	if _, done, _ := waitFor(all, []int{2, 1}); done {
		t.Fatal("one working is not")
	}
	if _, done, _ := waitFor(all, []int{3}); done {
		t.Fatal("an idle one with a task waiting is not")
	}
	if _, _, missing := waitFor(all, []int{9}); missing != 9 {
		t.Fatal("a number not there")
	}
}

// TestParseAnywhere: a flag counts after the arguments too, as before them,
// and nothing after "--" is a flag.
func TestParseAnywhere(t *testing.T) {
	for _, c := range []struct {
		args  []string
		lines int
		rest  string
	}{
		{[]string{"terminal", "--lines", "10"}, 10, "terminal"},
		{[]string{"--lines", "10", "terminal", "2"}, 10, "terminal 2"},
		{[]string{"terminal", "-lines=5", "2"}, 5, "terminal 2"},
		{[]string{"2"}, 0, "2"},
		{[]string{"--", "-x", "--lines", "3"}, 0, "-x --lines 3"},
	} {
		fs := flag.NewFlagSet("read", flag.ContinueOnError)
		lines := fs.Int("lines", 0, "")
		if err := parseAnywhere(fs, c.args); err != nil {
			t.Fatalf("%q: %v", c.args, err)
		}
		if *lines != c.lines || strings.Join(fs.Args(), " ") != c.rest {
			t.Errorf("%q: lines %d, arguments %q", c.args, *lines, fs.Args())
		}
	}
}

// TestTerminalArg: a terminal's name is taken off the front of what
// nerdr terminal --restart and nerdr close are given, the rest a command.
func TestTerminalArg(t *testing.T) {
	for _, c := range []struct {
		args       []string
		name, rest string
	}{
		{nil, "terminal", ""},
		{[]string{"terminal"}, "terminal", ""},
		{[]string{"terminal", "2"}, "terminal 2", ""},
		{[]string{"2", "npm", "start"}, "terminal 2", "npm start"},
		{[]string{"terminal", "npm", "start"}, "terminal", "npm start"},
		{[]string{"npm", "start"}, "terminal", "npm start"},
	} {
		name, rest := terminalArg(c.args)
		if name != c.name || strings.Join(rest, " ") != c.rest {
			t.Errorf("%q: %q, %q", c.args, name, rest)
		}
	}
}

// TestTakeFlags: a command's own flags count wherever they are; a word
// that only looks like a flag stays in the text, as does all after "--".
func TestTakeFlags(t *testing.T) {
	for _, c := range []struct {
		args []string
		wait bool
		rest string
	}{
		{[]string{"2", "hello", "--wait"}, true, "2 hello"},
		{[]string{"--wait", "2", "hello"}, true, "2 hello"},
		{[]string{"2", "run", "it", "with", "--verbose"}, false, "2 run it with --verbose"},
		{[]string{"2", "--timeout", "5m", "hi"}, false, "2 hi"},
		{[]string{"2", "--", "--wait", "is", "a", "flag"}, false, "2 --wait is a flag"},
		{[]string{"2", "-", "-wait=false"}, false, "2 -"},
	} {
		fs := flag.NewFlagSet("send", flag.ContinueOnError)
		wait := fs.Bool("wait", false, "")
		fs.Duration("timeout", 0, "")
		rest, err := takeFlags(fs, c.args)
		if err != nil || *wait != c.wait || strings.Join(rest, " ") != c.rest {
			t.Errorf("%q: wait %v, rest %q, %v", c.args, *wait, rest, err)
		}
	}
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.Duration("timeout", 0, "")
	if _, err := takeFlags(fs, []string{"2", "hi", "--timeout"}); err == nil {
		t.Error("a flag missing its value")
	}
}

// TestShellCommand: one argument is a command line as it is; several are
// one command's words, quoted where the shell would split or expand them.
func TestShellCommand(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"make && make test"}, "make && make test"},
		{[]string{"npm", "run", "dev"}, "npm run dev"},
		{[]string{"bash", "-c", "make && x"}, "bash -c 'make && x'"},
		{[]string{"echo", "it's"}, `echo 'it'\''s'`},
		{[]string{"go", "test", "./...", "-run=TestX"}, "go test ./... -run=TestX"},
		{[]string{"printf", ""}, "printf ''"},
	} {
		if got := shellCommand(c.args); got != c.want {
			t.Errorf("%q: %s, want %s", c.args, got, c.want)
		}
	}
}
