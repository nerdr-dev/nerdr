//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestAPIServers covers nerdr api --servers: this machine's core and
// box's, reached over (a fake) ssh as nerdr api there, through one
// standard input and output. Each answer and event says whose it is; box's
// connection lost, the client is told, and told box's state again when it
// is back; quit goes to the machine named, and only with one named.
func TestAPIServers(t *testing.T) {
	bin := build(t)
	root, scratch := setup(t)
	home := filepath.Join(scratch, "home")
	_ = os.MkdirAll(home, 0o755)
	ssh := filepath.Join(scratch, "fakessh")
	if err := os.WriteFile(ssh, []byte(fakeSSH), 0o755); err != nil {
		t.Fatal(err)
	}
	env := append(holderEnv(t, bin, scratch),
		"HOME="+home, "SHELL=/bin/sh",
		"PATH="+filepath.Dir(bin)+":"+os.Getenv("PATH"),
		"NERDR_SSH="+ssh, "FAKE_SSH_LOG="+filepath.Join(scratch, "ssh.log"))
	boxDir := filepath.Join(root, "repoB")
	cfg := filepath.Join(scratch, "config", "nerdr", "config.toml")
	f, err := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("\n[[servers]]\nname = \"box\"\nfolder = \"" + boxDir + "\"\n")
	f.Close()

	// The TUI starts box's core, through its bridge, and makes a tab here;
	// closed, both cores run on.
	h := start(t, bin, root, env)
	newCrashTab(h)
	endClient(t, h, "c")

	c := startAPIArgs(t, bin, h.env, "api", "--servers", root)
	w := c.next()
	machines, _ := w["machines"].([]any)
	if len(machines) != 2 {
		t.Fatalf("welcome %v", w)
	}
	if m := machines[0].(map[string]any); m["machine"] != "local" || m["status"] != "connected" || m["root"] != root {
		t.Fatalf("welcome %v", w)
	}
	if a := c.call(map[string]any{"id": "q", "op": "quit"}); a["error"] == nil {
		t.Fatalf("quit with no machine: %v", a)
	}
	if a := c.call(map[string]any{"id": "s", "op": "subscribe"}); a["error"] != nil {
		t.Fatalf("subscribe %v", a)
	}
	// Each machine's state, as it is connected to.
	connected := func(name, wantRoot string) {
		t.Helper()
		for deadline := time.Now().Add(20 * time.Second); ; {
			ev := c.next()
			st, _ := ev["state"].(map[string]any)
			if ev["event"] == "machine" && ev["machine"] == name && ev["status"] == "connected" && st != nil {
				if st["root"] != wantRoot {
					t.Fatalf("%s's state %v", name, st)
				}
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s never connected", name)
			}
		}
	}
	seen := map[string]bool{}
	for deadline := time.Now().Add(20 * time.Second); len(seen) < 2; {
		ev := c.next()
		name, _ := ev["machine"].(string)
		if st, _ := ev["state"].(map[string]any); ev["event"] == "machine" && ev["status"] == "connected" && st != nil {
			want := map[string]string{"local": root, "box": boxDir}[name]
			if st["root"] != want {
				t.Fatalf("%s's state %v", name, st)
			}
			seen[name] = true
		}
		if time.Now().After(deadline) {
			t.Fatalf("connected: %v", seen)
		}
	}
	a := c.call(map[string]any{"id": "b", "machine": "box", "op": "state"})
	if res, _ := a["result"].(map[string]any); a["machine"] != "box" || res["root"] != boxDir {
		t.Fatalf("box's state %v", a)
	}
	a = c.call(map[string]any{"id": float64(2), "machine": "local", "op": "state"})
	if res, _ := a["result"].(map[string]any); a["machine"] != "local" || res["root"] != root || len(res["tabs"].([]any)) != 1 {
		t.Fatalf("local state %v", a)
	}

	// box's connection lost: the client is told, and box's state again
	// once it is back.
	apis := pgrep("-f", "^nerdr api "+boxDir+"$")
	if len(apis) != 1 {
		t.Fatalf("nerdr api on box: %v", apis)
	}
	_ = syscall.Kill(apis[0], syscall.SIGKILL)
	for deadline := time.Now().Add(10 * time.Second); ; {
		ev := c.next()
		if ev["event"] == "machine" && ev["machine"] == "box" && ev["status"] == "down" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not told box is down")
		}
	}
	connected("box", boxDir)

	// Quit on box: box's core ends, this machine's runs on.
	if a := c.call(map[string]any{"id": "qb", "machine": "box", "op": "quit"}); a["error"] != nil || a["machine"] != "box" {
		t.Fatalf("quit box %v", a)
	}
	for deadline := time.Now().Add(15 * time.Second); ; {
		ev := c.next()
		if ev["event"] == "machine" && ev["machine"] == "box" && ev["status"] == "down" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("box's core did not end")
		}
	}
	if a := c.call(map[string]any{"id": float64(3), "machine": "local", "op": "state"}); a["error"] != nil {
		t.Fatalf("local after box quit: %v", a)
	}
}
