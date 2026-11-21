package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/nerdr-dev/nerdr/internal/api"
	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/state"
)

const listUsage = `usage: nerdr list [--all] [--json]

Lists the nerdrs running on this machine, one for each folder nerdr was
started in: the folder, its tabs and agents, and how many agents wait for
you. A nerdr runs on with its window closed, until you quit it; start
nerdr in the folder to open its window again.

--all (-a) lists too the folders with saved tabs where no nerdr runs:
start nerdr there to get the tabs back.

--json prints them as a JSON array, each with root, running, version,
api, tabs, agents, waiting and questions (what nerdr itself asks you);
a folder not running has root, running (false), tabs and saved, when
its tabs were last saved. root is the FOLDER to give nerdr api.

Programs nerdr runs, its agents among them, are refused: in a tab, use
nerdr tabs.
`

// listTimeout is how long nerdr list waits for one core to answer.
const listTimeout = 5 * time.Second

// running is a nerdr that runs, as nerdr list tells of it.
// Running is false for a folder with saved tabs where no nerdr runs,
// which has only Root, Tabs and Saved.
type running struct {
	Root      string     `json:"root"`
	Running   bool       `json:"running"`
	Version   string     `json:"version,omitempty"`
	API       int        `json:"api,omitempty"`
	Tabs      int        `json:"tabs"`
	Agents    int        `json:"agents"`
	Waiting   int        `json:"waiting"`
	Questions int        `json:"questions"`
	Saved     *time.Time `json:"saved,omitempty"`
}

// runList is `nerdr list`.
func runList(args []string) int {
	fl := flag.NewFlagSet("list", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	asJSON := fl.Bool("json", false, "")
	withSaved := fl.Bool("all", false, "")
	fl.BoolVar(withSaved, "a", false, "")
	if fl.Parse(args) != nil || fl.NArg() > 0 {
		fmt.Fprint(os.Stderr, listUsage)
		return 2
	}
	dir, err := attach.Dir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr list:", err)
		return 1
	}
	all, alive, failed := listRunning(dir)
	for _, why := range failed {
		fmt.Fprintln(os.Stderr, "nerdr list:", why)
	}
	// The saved tabs are held back from a program nerdr runs, as the
	// running ones are by their cores: not to guard them, which is the
	// sandbox's to do (the files are the caller's to read or not), but so
	// that such a program is not told half of what there is.
	var saved []running
	underNerdr := inNerdr()
	if !underNerdr {
		saved = listSaved(state.Dir(), func(root string) bool { return coreRuns(root, alive) })
	} else if *withSaved {
		fmt.Fprintln(os.Stderr, "nerdr list: the folders with saved tabs are not listed for a program nerdr runs")
	}
	if *withSaved {
		all = append(all, saved...)
		sort.SliceStable(all, func(i, j int) bool { return all[i].Root < all[j].Root })
	}
	switch {
	case *asJSON:
		b, _ := json.MarshalIndent(all, "", "  ")
		fmt.Println(string(b))
	case len(all) == 0 && len(failed) == 0 && *withSaved:
		fmt.Fprintln(os.Stderr, "nerdr list: no nerdr is running, and no folder has saved tabs")
	case len(all) == 0 && len(failed) == 0 && len(saved) > 0:
		fmt.Fprintf(os.Stderr, "nerdr list: no nerdr is running; %s saved tabs: nerdr list --all\n",
			plural(len(saved), "folder has", "folders have"))
	case len(all) == 0 && len(failed) == 0:
		fmt.Fprintln(os.Stderr, "nerdr list: no nerdr is running")
	case len(all) > 0 && *withSaved:
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "FOLDER\tSTATE\tTABS\tAGENTS\tWAITING\tQUESTIONS")
		for _, r := range all {
			if r.Running {
				fmt.Fprintf(w, "%s\trunning\t%d\t%d\t%d\t%d\n", r.Root, r.Tabs, r.Agents, r.Waiting, r.Questions)
			} else {
				fmt.Fprintf(w, "%s\tstopped\t%d\t-\t-\t-\n", r.Root, r.Tabs)
			}
		}
		_ = w.Flush()
	case len(all) > 0:
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "FOLDER\tTABS\tAGENTS\tWAITING\tQUESTIONS")
		for _, r := range all {
			fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\n", r.Root, r.Tabs, r.Agents, r.Waiting, r.Questions)
		}
		_ = w.Flush()
	}
	if len(all) == 0 && len(failed) > 0 {
		return 1
	}
	return 0
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// coreRuns reports whether a core runs for root: its API socket is in
// alive, or something listens at its client socket, for a core whose API
// socket could not be made. It is asked, not the session's lock, since
// taking that even for a moment could stop a nerdr starting there. Not
// knowing is running: a folder is only said stopped when it surely is.
func coreRuns(root string, alive map[string]bool) bool {
	if sock, _, err := attach.APIPaths(root); err != nil || alive[sock] {
		return true
	}
	sock, _, err := attach.Paths(root)
	if err != nil {
		return true
	}
	c, err := net.DialTimeout("unix", sock, listTimeout)
	if err == nil {
		// Said nothing to, the core refuses it, for want of a hello.
		_ = c.Close()
		return true
	}
	return !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ECONNREFUSED)
}

// listSaved is the folders with a session saved in sessions, the folder
// of the session files, where no nerdr runs (as runs tells): a session
// with a tab at least, for a folder that is there still. A file not
// named as nerdr names the folder's, a copy kept or another file, is
// passed by; and so is all of it when the folder cannot be read, as in
// a sandbox kept out of it.
func listSaved(sessions string, runs func(root string) bool) []running {
	out := []running{}
	files, _ := filepath.Glob(filepath.Join(sessions, "*.json"))
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var s struct {
			Root string            `json:"root"`
			Tabs []json.RawMessage `json:"tabs"`
		}
		if json.Unmarshal(data, &s) != nil || s.Root == "" || len(s.Tabs) == 0 ||
			filepath.Join(sessions, filepath.Base(state.Path(s.Root))) != path {
			continue
		}
		if fi, err := os.Stat(s.Root); err != nil || !fi.IsDir() || runs(s.Root) {
			continue
		}
		r := running{Root: s.Root, Tabs: len(s.Tabs)}
		if fi, err := os.Stat(path); err == nil {
			t := fi.ModTime()
			r.Saved = &t
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Root < out[j].Root })
	return out
}

// listRunning asks each core with an API socket in dir what it runs, the
// folders in order. A socket no core listens at, left by one that died,
// is passed by; alive has the others, those that did not answer too, and
// failed says why each of them did not, once for each reason.
func listRunning(dir string) (all []running, alive map[string]bool, failed []string) {
	all = []running{}
	alive = map[string]bool{}
	socks, _ := filepath.Glob(filepath.Join(dir, "api-*.sock"))
	seen := map[string]bool{}
	for _, sock := range socks {
		r, err := askRunning(sock, strings.TrimSuffix(sock, ".sock")+".key")
		switch {
		case err == nil:
			all = append(all, r)
			alive[sock] = true
		case errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED):
		case !seen[err.Error()]:
			seen[err.Error()] = true
			failed = append(failed, err.Error())
			alive[sock] = true
		default:
			alive[sock] = true
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Root < all[j].Root })
	return all, alive, failed
}

// askRunning asks the core listening at API socket sock, with the key in
// keyFile, which folder it runs for and what its state is.
func askRunning(sock, keyFile string) (running, error) {
	c, err := dialAPIAt(sock, keyFile)
	if err != nil {
		return running{}, err
	}
	defer c.Close()
	welcome, st, err := askState(c)
	if err != nil {
		return running{}, err
	}
	r := running{Root: welcome.Root, Running: true, Version: welcome.Version, API: welcome.API,
		Tabs: len(st.Tabs), Waiting: len(st.Waiting), Questions: len(st.Questions)}
	for _, t := range st.Tabs {
		r.Agents += len(t.Agents)
	}
	return r, nil
}

// askState reads the welcome of the core c has said hello to, or why it
// refused, and asks it for its state.
func askState(c net.Conn) (api.Welcome, core.APIState, error) {
	_ = c.SetDeadline(time.Now().Add(listTimeout))
	in := bufio.NewReaderSize(c, 64<<10)
	welcome, err := readWelcome(in)
	if err != nil {
		return api.Welcome{}, core.APIState{}, err
	}
	if _, err := c.Write([]byte(`{"op":"state"}` + "\n")); err != nil {
		return welcome, core.APIState{}, err
	}
	var answer struct {
		Result core.APIState `json:"result"`
		Error  string        `json:"error"`
	}
	if err := readLine(in, &answer); err != nil {
		return welcome, core.APIState{}, err
	}
	if answer.Error != "" {
		return welcome, core.APIState{}, errors.New(answer.Error)
	}
	return welcome, answer.Result, nil
}

// readWelcome reads a core's welcome, the first line it sends, or why it
// refused.
func readWelcome(in *bufio.Reader) (api.Welcome, error) {
	var welcome struct {
		api.Welcome
		Error string `json:"error"`
	}
	if err := readLine(in, &welcome); err != nil {
		return api.Welcome{}, err
	}
	if welcome.Error != "" {
		return api.Welcome{}, errors.New(welcome.Error)
	}
	return welcome.Welcome, nil
}

// readLine reads the next line a core sends into v.
func readLine(in *bufio.Reader, v any) error {
	line, err := in.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("a nerdr closed the connection: %w", err)
	}
	return json.Unmarshal(line, v)
}
