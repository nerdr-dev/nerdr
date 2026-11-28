package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/agents"
	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/hooks"
	"github.com/nerdr-dev/nerdr/internal/rundir"
	"github.com/nerdr-dev/nerdr/skills"
)

const doctorUsage = `usage: nerdr doctor [--json] [FOLDER]

Looks at how nerdr is set up here, and says on a line each what is wrong
and what to do about it: the config file, the agents and git and gh on
PATH, the folder of nerdr's sockets, the nerdr running for FOLDER (this
folder when not given) and whether it is this version, and the skills
nerdr gives the agents. In a nerdr tab, whether the agent reaches nerdr.

It only looks: it changes nothing and runs no program. Exits 1 when
something is wrong (✗); a warning (!) is something that may be.
--json prints the checks as a JSON array of name, status (ok, warn,
fail) and text.
`

// check is one of nerdr doctor's checks: what it looked at, how it is,
// and what to do.
type check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Text   string `json:"text"`
}

const (
	checkOK   = "ok"
	checkWarn = "warn"
	checkFail = "fail"
)

// runDoctor is `nerdr doctor`. It runs in the caller's process, in an
// agent's sandbox when an agent runs it: it reads the user's config and
// nerdr's own folders alone, never a worktree's, runs no program and
// changes nothing.
func runDoctor(args []string) int {
	fl := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	asJSON := fl.Bool("json", false, "")
	if parseAnywhere(fl, args) != nil || fl.NArg() > 1 {
		fmt.Fprint(os.Stderr, doctorUsage)
		return 2
	}
	folder := "."
	if fl.NArg() == 1 {
		folder = fl.Arg(0)
	}
	checks := doctorChecks(folder)
	failed := false
	for _, c := range checks {
		failed = failed || c.Status == checkFail
	}
	if *asJSON {
		b, _ := json.MarshalIndent(checks, "", "  ")
		fmt.Println(string(b))
	} else {
		for _, c := range checks {
			mark := "✓"
			switch c.Status {
			case checkWarn:
				mark = "!"
			case checkFail:
				mark = "✗"
			}
			fmt.Printf("%s %-9s %s\n", mark, c.Name, c.Text)
		}
	}
	if failed {
		return 1
	}
	return 0
}

// doctorChecks are nerdr doctor's checks, for the nerdr of folder.
func doctorChecks(folder string) []check {
	tab := inNerdr()
	out := []check{{"version", checkOK, "nerdr " + version()}}
	add := func(name, status, format string, a ...any) {
		out = append(out, check{name, status, fmt.Sprintf(format, a...)})
	}

	cfg, err := config.Load()
	switch {
	case err != nil && tab && errors.Is(err, fs.ErrPermission):
		add("config", checkWarn, "%s cannot be read from here, as a sandbox may have it; nerdr itself reads it", config.Path())
	case err != nil:
		add("config", checkFail, "%v: fix what it names, or move the file away to start from the defaults", err)
	default:
		if _, statErr := os.Stat(config.Path()); statErr != nil {
			add("config", checkOK, "no config file at %s: the defaults", config.Path())
		} else if _, perr := cfg.PrefixByte(); perr != nil {
			add("config", checkFail, "%s: %v", config.Path(), perr)
		} else {
			add("config", checkOK, "%s", config.Path())
		}
	}

	if found := agents.Available(cfg.Agents); len(found) == 0 {
		var all []string
		for _, k := range agents.All {
			all = append(all, string(k))
		}
		add("agents", checkFail, "none on PATH: install one of %s, or say how it is started in [agents] in the config", strings.Join(all, ", "))
	} else {
		var names []string
		for _, k := range found {
			names = append(names, string(k))
		}
		add("agents", checkOK, "%s", strings.Join(names, ", "))
	}

	if p, err := exec.LookPath("git"); err != nil {
		add("git", checkFail, "not on PATH: nerdr makes each tab's worktree with git; install it")
	} else {
		add("git", checkOK, "%s", p)
	}
	_, glabErr := exec.LookPath("glab")
	if p, err := exec.LookPath("gh"); err != nil && glabErr == nil {
		add("gh", checkWarn, "not on PATH: only GitLab's merge requests and CI are shown (with glab); for GitHub's, install gh and run gh auth login")
	} else if err != nil {
		add("gh", checkWarn, "not on PATH: without it nerdr shows no pull requests or CI; install GitHub's gh and run gh auth login (glab, glab auth login, for GitLab)")
	} else {
		add("gh", checkOK, "%s", p)
	}
	if p, err := exec.LookPath("glab"); err == nil {
		add("glab", checkOK, "%s", p)
	}

	out = append(out, socketsCheck(tab))
	if !tab {
		out = append(out, pathCheck())
	}
	out = append(out, runningCheck(folder, tab))
	if tab {
		out = append(out, tabCheck())
	}
	out = append(out, skillsCheck(cfg, err == nil))
	return out
}

// socketsCheck is whether the folder of nerdr's sockets is one only this
// user can enter, without changing it.
func socketsCheck(tab bool) check {
	dir, err := rundir.Dir()
	if err != nil {
		return check{"sockets", checkFail, err.Error()}
	}
	err = rundir.Check(dir, false)
	switch {
	case err == nil:
		return check{"sockets", checkOK, dir + " is private"}
	case errors.Is(err, fs.ErrNotExist):
		return check{"sockets", checkOK, dir + " is not there yet: nerdr makes it when it starts"}
	case tab && errors.Is(err, fs.ErrPermission):
		return check{"sockets", checkOK, dir + " is out of reach from here, as a sandbox around an agent should have it"}
	case strings.Contains(err.Error(), "open to other users"):
		return check{"sockets", checkFail, err.Error() + ": nerdr makes it private again when it starts, or run chmod 700 " + dir}
	case strings.Contains(err.Error(), "another user"):
		return check{"sockets", checkFail, err.Error() + ": with no nerdr running, remove it, or set XDG_RUNTIME_DIR to a folder of your own"}
	}
	return check{"sockets", checkFail, err.Error()}
}

// pathCheck is whether nerdr on PATH is this one: nerdr connect from
// another machine runs the nerdr on PATH of a login shell here.
func pathCheck() check {
	onPath, err := exec.LookPath("nerdr")
	if err != nil {
		return check{"path", checkWarn, "no nerdr on PATH: nerdr connect from another machine needs it there, in a login shell"}
	}
	self, err := os.Executable()
	if err != nil {
		return check{"path", checkOK, onPath}
	}
	if same(onPath, self) {
		return check{"path", checkOK, onPath + " is this nerdr"}
	}
	return check{"path", checkWarn, fmt.Sprintf("nerdr on PATH is %s, not this one (%s): which runs depends on how it is started", onPath, self)}
}

// same is whether paths a and b are one file.
func same(a, b string) bool {
	ai, aerr := os.Stat(a)
	bi, berr := os.Stat(b)
	return aerr == nil && berr == nil && os.SameFile(ai, bi)
}

// runningCheck is whether a nerdr runs for folder, and of which version;
// asked of its API, which refuses a program nerdr runs, so not from a tab.
func runningCheck(folder string, tab bool) check {
	if tab {
		return check{"running", checkOK, "not looked at: nerdr refuses its own programs at its API; run nerdr doctor in your own terminal for this check"}
	}
	root, err := bridgeRoot(folder)
	if err != nil {
		return check{"running", checkFail, err.Error()}
	}
	refusedUnderNerdr := check{"running", checkOK, "not looked at: nerdr refuses programs a nerdr runs at its API; run nerdr doctor in your own terminal for this check"}
	c, err := dialAPI(root)
	if err != nil {
		if strings.Contains(err.Error(), "takes no client from a program") {
			// The core refused before it read the hello.
			return refusedUnderNerdr
		}
		if strings.HasPrefix(err.Error(), "no nerdr runs") {
			return check{"running", checkOK, "no nerdr runs for " + root}
		}
		return check{"running", checkFail, fmt.Sprintf("cannot reach the nerdr of %s: %v", root, err)}
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(listTimeout))
	w, err := readWelcome(bufio.NewReader(c))
	if err != nil && strings.Contains(err.Error(), "takes no client from a program") {
		// Run under a nerdr, without what it gives its programs.
		return refusedUnderNerdr
	}
	if err != nil {
		return check{"running", checkFail, fmt.Sprintf("the nerdr of %s did not answer: %v", root, err)}
	}
	if w.Version != version() {
		return check{"running", checkWarn, fmt.Sprintf("the nerdr running for %s is %s, and this one %s: to run this one, quit it (nerdr stop) and start nerdr again", root, w.Version, version())}
	}
	return check{"running", checkOK, fmt.Sprintf("nerdr runs for %s, this version", root)}
}

// tabCheck is whether the agent of a tab reaches the nerdr running it.
func tabCheck() check {
	tab, sock := os.Getenv("NERDR_TAB"), os.Getenv("NERDR_SOCKET")
	switch {
	case os.Getenv("NERDR_SHELL") == "1":
		return check{"tab", checkOK, "a tab's shell: only its agents report to nerdr"}
	case sock == "" && os.Getenv("NERDR_NO_SOCKET") != "":
		return check{"tab", checkFail, "the nerdr running this tab has no socket: " + os.Getenv("NERDR_NO_SOCKET")}
	case tab == "" || sock == "":
		return check{"tab", checkFail, "NERDR=1 is set but NERDR_TAB or NERDR_SOCKET is not: a sandbox around the agent most likely drops them; let the NERDR_* variables through"}
	}
	if _, err := request(sock, hooks.Query, map[string]any{"what": hooks.QueryAgents, "pane_id": tab}); err != nil {
		return check{"tab", checkFail, fmt.Sprintf("cannot reach nerdr at %s: %v. A sandbox must let the agent connect to it", sock, err)}
	}
	return check{"tab", checkOK, "the agent reaches nerdr"}
}

// skillsCheck is whether the skills nerdr gives the agents in the home
// folder are there, and as this nerdr has them.
func skillsCheck(cfg config.Config, read bool) check {
	if !read {
		return check{"skills", checkOK, "not looked at, the config not read"}
	}
	switch cfg.Install {
	case "":
		return check{"skills", checkOK, "where to put them not chosen yet: nerdr asks at its first start"}
	case config.InstallOff:
		return check{"skills", checkWarn, "install = \"off\": nerdr puts no skills or hooks in place, so agents do not know its commands, and their state is read from the screen"}
	case config.InstallWorktree:
		return check{"skills", checkOK, "put in each tab's worktree as its agent starts (install = \"worktree\"); not looked at here"}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return check{"skills", checkFail, err.Error()}
	}
	// The agents, each with how many skills it lacks, or has from
	// another nerdr.
	var missing, outdated []string
	for _, k := range agents.Available(cfg.Agents) {
		dirs := skills.Dirs(home, "", k)[1:]
		lack, old := 0, 0
		for _, s := range skills.All() {
			if !cfg.SkillEnabled(s.Name) {
				continue
			}
			switch _, st := s.Find(dirs); st {
			case skills.Missing:
				lack++
			case skills.Outdated:
				old++
			}
		}
		if lack > 0 {
			missing = append(missing, fmt.Sprintf("%s (%d)", k, lack))
		}
		if old > 0 {
			outdated = append(outdated, fmt.Sprintf("%s (%d)", k, old))
		}
	}
	switch {
	case len(missing) > 0:
		return check{"skills", checkWarn, "missing for " + strings.Join(missing, ", ") + ": nerdr puts them in place when it starts; restart it if it runs"}
	case len(outdated) > 0:
		return check{"skills", checkWarn, "from another version of nerdr for " + strings.Join(outdated, ", ") + ": the next start brings them up to date"}
	}
	return check{"skills", checkOK, "in place for the agents on PATH"}
}
