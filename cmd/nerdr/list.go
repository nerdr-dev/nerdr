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
)

const listUsage = `usage: nerdr list [--json]

Lists the nerdrs running on this machine, one for each folder nerdr was
started in: the folder, its tabs and agents, and how many agents wait for
you. A nerdr runs on with its window closed, until you quit it; start
nerdr in the folder to open its window again.

--json prints them as a JSON array, each with root, version, api, tabs,
agents, waiting and questions (what nerdr itself asks you). root is the
FOLDER to give nerdr api.

Programs nerdr runs, its agents among them, are refused: in a tab, use
nerdr tabs.
`

// listTimeout is how long nerdr list waits for one core to answer.
const listTimeout = 5 * time.Second

// running is a nerdr that runs, as nerdr list tells of it.
type running struct {
	Root      string `json:"root"`
	Version   string `json:"version"`
	API       int    `json:"api"`
	Tabs      int    `json:"tabs"`
	Agents    int    `json:"agents"`
	Waiting   int    `json:"waiting"`
	Questions int    `json:"questions"`
}

// runList is `nerdr list`.
func runList(args []string) int {
	fl := flag.NewFlagSet("list", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	asJSON := fl.Bool("json", false, "")
	if fl.Parse(args) != nil || fl.NArg() > 0 {
		fmt.Fprint(os.Stderr, listUsage)
		return 2
	}
	dir, err := attach.Dir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "nerdr list:", err)
		return 1
	}
	all, failed := listRunning(dir)
	for _, why := range failed {
		fmt.Fprintln(os.Stderr, "nerdr list:", why)
	}
	if *asJSON {
		b, _ := json.MarshalIndent(all, "", "  ")
		fmt.Println(string(b))
	} else if len(all) == 0 {
		if len(failed) == 0 {
			fmt.Fprintln(os.Stderr, "nerdr list: no nerdr is running")
		}
	} else {
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

// listRunning asks each core with an API socket in dir what it runs, the
// folders in order. A socket no core listens at, left by one that died,
// is passed by; failed says why each of the others did not answer, once
// for each reason.
func listRunning(dir string) (all []running, failed []string) {
	all = []running{}
	socks, _ := filepath.Glob(filepath.Join(dir, "api-*.sock"))
	seen := map[string]bool{}
	for _, sock := range socks {
		r, err := askRunning(sock, strings.TrimSuffix(sock, ".sock")+".key")
		switch {
		case err == nil:
			all = append(all, r)
		case errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED):
		case !seen[err.Error()]:
			seen[err.Error()] = true
			failed = append(failed, err.Error())
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Root < all[j].Root })
	return all, failed
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
	r := running{Root: welcome.Root, Version: welcome.Version, API: welcome.API,
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
