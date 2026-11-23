//go:build e2e

package e2e

import (
	"bufio"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// apiClient is `nerdr api` for root, its lines read one at a time.
type apiClient struct {
	t   *testing.T
	in  io.WriteCloser
	out *bufio.Scanner
	cmd *exec.Cmd
}

func startAPI(t *testing.T, bin, root string, env []string) *apiClient {
	t.Helper()
	return startAPIArgs(t, bin, env, "api", root)
}

// startAPIArgs is startAPI of nerdr with args.
func startAPIArgs(t *testing.T, bin string, env []string, args ...string) *apiClient {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = in.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if s := stderr.String(); s != "" {
			t.Logf("nerdr api: %s", s)
		}
	})
	sc := bufio.NewScanner(out)
	sc.Buffer(nil, 4<<20)
	return &apiClient{t: t, in: in, out: sc, cmd: cmd}
}

// next is the next line, decoded, within a while.
func (c *apiClient) next() map[string]any {
	c.t.Helper()
	got := make(chan map[string]any, 1)
	go func() {
		if !c.out.Scan() {
			got <- nil
			return
		}
		var v map[string]any
		_ = json.Unmarshal(c.out.Bytes(), &v)
		got <- v
	}()
	select {
	case v := <-got:
		if v == nil {
			c.t.Fatal("nerdr api ended")
		}
		return v
	case <-time.After(10 * time.Second):
		c.t.Fatal("no line from nerdr api")
	}
	return nil
}

// call sends a request and is its answer, events before it skipped.
func (c *apiClient) call(req map[string]any) map[string]any {
	c.t.Helper()
	b, _ := json.Marshal(req)
	if _, err := c.in.Write(append(b, '\n')); err != nil {
		c.t.Fatal(err)
	}
	for {
		a := c.next()
		if a["event"] == nil {
			if a["id"] != req["id"] {
				c.t.Fatalf("answer %v to %v", a, req)
			}
			return a
		}
	}
}

// readUntil reads agent id's screen until it shows what.
func (c *apiClient) readUntil(id any, what string) {
	c.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for n := 100; ; n++ {
		a := c.call(map[string]any{"id": float64(n), "op": "read", "target": id})
		res, _ := a["result"].(map[string]any)
		if text, _ := res["text"].(string); strings.Contains(text, what) {
			return
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("no %q on the agent's screen: %v", what, a)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestTheAPI: with the TUI closed, a program reads the tabs and the
// agent's screen by `nerdr api`, answers what the agent asks, and is
// told when its state changes.
func TestTheAPI(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	env := holderEnv(t, bin, scratch)

	h := start(t, bin, root, env)
	newCrashTab(h)
	endClient(t, h, "c")

	c := startAPI(t, bin, root, h.env)
	if w := c.next(); w["api"] != float64(1) || w["root"] != root {
		t.Fatalf("welcome %v", w)
	}
	a := c.call(map[string]any{"id": float64(1), "op": "state"})
	res := a["result"].(map[string]any)
	tabs := res["tabs"].([]any)
	if len(tabs) != 1 {
		t.Fatalf("state %v", a)
	}
	tab := tabs[0].(map[string]any)
	if tab["name"] != "crash" {
		t.Fatalf("tab %v", tab)
	}
	agent := tab["agents"].([]any)[0].(map[string]any)["id"]

	if a := c.call(map[string]any{"id": float64(2), "op": "subscribe"}); a["error"] != nil {
		t.Fatalf("subscribe %v", a)
	}
	if a := c.call(map[string]any{"id": float64(3), "op": "type", "target": agent, "text": "ask", "keys": []string{"enter"}}); a["error"] != nil {
		t.Fatalf("type %v", a)
	}
	c.readUntil(agent, "Do you want to proceed?")
	// The agent asking is told, as a state of its own.
	for deadline := time.Now().Add(10 * time.Second); ; {
		ev := c.next()
		if ag, _ := ev["agent"].(map[string]any); ev["event"] == "agent" && ag["state"] == "blocked" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not told the agent asks")
		}
	}
	if a := c.call(map[string]any{"id": float64(4), "op": "type", "target": agent, "keys": []string{"1", "enter"}}); a["error"] != nil {
		t.Fatalf("type %v", a)
	}
	c.readUntil(agent, "answered 1")

	if a := c.call(map[string]any{"id": float64(5), "op": "repos"}); !strings.Contains(strings.Join(names(a), ","), "repoA") {
		t.Fatalf("repos %v", a)
	}

	// Quit, from a client: the API's connection ends with the core.
	h = start(t, bin, root, env)
	h.waitTabBar("1:crash", 10*time.Second)
	endClient(t, h, "q")
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	_ = c.in.Close()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("nerdr api still running after nerdr quit")
	}
}

// names are the names in a repos answer.
func names(a map[string]any) []string {
	var out []string
	list, _ := a["result"].([]any)
	for _, r := range list {
		if m, ok := r.(map[string]any); ok {
			out = append(out, m["name"].(string))
		}
	}
	return out
}
