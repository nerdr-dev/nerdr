package hooks

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The hook protocol only grows: an agent that runs on through an update
// of nerdr keeps the hooks and the environment the nerdr before gave it,
// and what they send must still be taken, and mean what it meant. The
// requests released versions sent are kept in testdata/compat, one file
// a version, recorded from that version's binary with TestRecordCompat;
// each is checked against what this nerdr sends for the same.

// compatCase is one thing an agent's hooks, a herdr integration or a
// skill had nerdr do: run with Args, Stdin on its standard input.
type compatCase struct {
	Name  string          `json:"name"`
	Args  []string        `json:"args"`
	Stdin json.RawMessage `json:"stdin,omitempty"`
}

// compatLine is one request a case made on the hook socket.
type compatLine struct {
	Case    string          `json:"case"`
	Request json.RawMessage `json:"request"`
}

const compatTab = "compat-tab"

func compatCases(t *testing.T) []compatCase {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "compat", "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []compatCase
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

// recordCompat runs bin for each case, in order, in a tab of its own as an
// agent runs it, and returns the requests it made on the hook socket,
// each answered as a nerdr answers one it takes.
func recordCompat(t *testing.T, bin string, cases []compatCase) []compatLine {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "nerdr.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var (
		mu    sync.Mutex
		lines []compatLine
		now   string
	)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				sc := bufio.NewScanner(c)
				sc.Buffer(nil, maxRequest)
				for sc.Scan() {
					var req request
					_ = json.Unmarshal(sc.Bytes(), &req)
					mu.Lock()
					lines = append(lines, compatLine{Case: now, Request: append(json.RawMessage{}, sc.Bytes()...)})
					mu.Unlock()
					_, _ = c.Write(append(result(req.ID, map[string]any{"type": "ok", "text": ""}), '\n'))
				}
			}()
		}
	}()
	for _, sub := range []string{"home", "state", "config", "tmp"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// agy's and grok's hooks report only for the agent nerdr started: the
	// one given is this test, its parent standing for nerdr.
	agent, nerdr := strconv.Itoa(os.Getpid()), strconv.Itoa(os.Getppid())
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + filepath.Join(dir, "home"),
		"XDG_STATE_HOME=" + filepath.Join(dir, "state"),
		"XDG_CONFIG_HOME=" + filepath.Join(dir, "config"),
		"TMPDIR=" + filepath.Join(dir, "tmp"),
		"NERDR=1",
		"NERDR_BIN=" + bin,
		"NERDR_TAB=" + compatTab,
		"NERDR_SOCKET=" + sock,
		"HERDR_PANE_ID=" + compatTab,
		"HERDR_SOCKET_PATH=" + sock,
		"HERDR_BIN_PATH=" + bin,
		"NERDR_AGY_HOOK=" + nerdr,
		"NERDR_GROK_HOOK=" + nerdr,
	}
	for _, c := range cases {
		mu.Lock()
		now = c.Name
		mu.Unlock()
		args := make([]string, len(c.Args))
		for i, a := range c.Args {
			args[i] = strings.NewReplacer("$AGENT_PID", agent, "$HERDR_PANE_ID", compatTab).Replace(a)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env, cmd.Dir = env, dir
		cmd.Stdin = bytes.NewReader(c.Stdin)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Logf("%s: %v: %s", c.Name, err, out)
		}
		cancel()
	}
	mu.Lock()
	defer mu.Unlock()
	return append([]compatLine{}, lines...)
}

// compatReports is what a nerdr takes from lines, by case: the reports it
// passes on, with what can differ from one run to the next left out, or
// the error it answers.
func compatReports(t *testing.T, lines []compatLine) map[string][]string {
	t.Helper()
	var got []Report
	s := &Server{send: func(r Report) {
		if r.Reply != nil {
			r.Reply <- Answer{}
		}
		got = append(got, r)
	}}
	out := map[string][]string{}
	for _, l := range lines {
		got = nil
		resp := s.handle(&client{s: s}, l.Request)
		var r struct {
			Error *struct{ Code, Message string } `json:"error"`
		}
		_ = json.Unmarshal(resp, &r)
		if r.Error != nil {
			out[l.Case] = append(out[l.Case], "refused: "+r.Error.Message)
			continue
		}
		for _, rep := range got {
			rep.Reply, rep.Peer = nil, Peer{}
			if rep.Seq != 0 {
				rep.Seq = 1 // the time it was sent, for most
			}
			// What is pointed to, not where.
			limits, _ := json.Marshal(rep.Limits)
			review, _ := json.Marshal(rep.Review)
			rep.Limits, rep.Review = nil, nil
			out[l.Case] = append(out[l.Case], fmt.Sprintf("%+v limits %s review %s", rep, limits, review))
		}
	}
	return out
}

// buildNerdr builds this nerdr.
func buildNerdr(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "nerdr")
	cmd := exec.Command("go", "build", "-o", bin, "../../cmd/nerdr")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// TestOldHooksAreTaken: what the hooks of each released version sent is
// taken by this nerdr, and means what this nerdr's own hooks mean by the
// same. One that sent nothing for a case is not held to it: the protocol
// grows.
func TestOldHooksAreTaken(t *testing.T) {
	if testing.Short() {
		t.Skip("builds nerdr")
	}
	cases := compatCases(t)
	now := compatReports(t, recordCompat(t, buildNerdr(t), cases))
	files, _ := filepath.Glob(filepath.Join("testdata", "compat", "v*.jsonl"))
	if len(files) == 0 {
		t.Fatal("no recorded versions")
	}
	for _, f := range files {
		version := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		lines := readCompat(t, f)
		if len(lines) == 0 {
			t.Fatalf("%s: nothing recorded", version)
		}
		old := compatReports(t, lines)
		for _, c := range cases {
			o, ok := old[c.Name]
			if !ok {
				continue
			}
			for _, r := range o {
				if strings.HasPrefix(r, "refused: ") {
					t.Errorf("%s, %s: %s", version, c.Name, r)
				}
			}
			if !reflect.DeepEqual(o, now[c.Name]) {
				t.Errorf("%s, %s: taken as\n  %q\nwhile this nerdr's means\n  %q", version, c.Name, o, now[c.Name])
			}
		}
	}
}

func readCompat(t *testing.T, path string) []compatLine {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lines []compatLine
	for _, l := range bytes.Split(bytes.TrimSpace(b), []byte("\n")) {
		var cl compatLine
		if err := json.Unmarshal(l, &cl); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		lines = append(lines, cl)
	}
	return lines
}

// TestRecordCompat records what a released nerdr's hooks send, for
// TestOldHooksAreTaken: run it with NERDR_COMPAT_BIN the binary of the
// release and NERDR_COMPAT_VERSION its version (v0.3.0). The binary can
// be built from the tag: git archive v0.3.0 | tar -x -C DIR, then go
// build ./cmd/nerdr in DIR.
func TestRecordCompat(t *testing.T) {
	bin, version := os.Getenv("NERDR_COMPAT_BIN"), os.Getenv("NERDR_COMPAT_VERSION")
	if bin == "" || version == "" {
		t.Skip("records a release's hooks: set NERDR_COMPAT_BIN and NERDR_COMPAT_VERSION")
	}
	var buf bytes.Buffer
	for _, l := range recordCompat(t, bin, compatCases(t)) {
		b, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(append(b, '\n'))
	}
	if err := os.WriteFile(filepath.Join("testdata", "compat", version+".jsonl"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
