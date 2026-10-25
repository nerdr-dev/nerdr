// Package herdr connects nerdr to herdr when it runs in a herdr pane.
//
// herdr lists one agent per pane, and all of nerdr's agents share its
// one pane. So nerdr reports itself as that pane's agent, with the state
// of the tab that most needs attention (blocked, then finished, then
// working) and that tab's name as the message. When herdr then gives the
// pane focus — the user clicked it in herdr's agent list — nerdr opens
// that same tab.
package herdr

import (
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nerdr-dev/nerdr/internal/proc"
)

const source = "nerdr"

// Client reports to the herdr session nerdr runs in.
type Client struct {
	bin  string
	pane string

	mu        sync.Mutex
	last      string // the last report sent, so an unchanged one is not resent
	lastState string // the state in it
	seq       int64
}

// Detect returns a client when nerdr is running inside herdr, else nil.
func Detect() *Client {
	if os.Getenv("HERDR_ENV") != "1" || os.Getenv("HERDR_PANE_ID") == "" {
		return nil
	}
	bin := os.Getenv("HERDR_BIN_PATH")
	if bin == "" {
		var err error
		if bin, err = exec.LookPath("herdr"); err != nil {
			return nil
		}
	}
	return &Client{bin: bin, pane: os.Getenv("HERDR_PANE_ID"), seq: time.Now().UnixMilli()}
}

// NewClient is a client reporting through the herdr binary bin as pane.
func NewClient(bin, pane string) *Client {
	return &Client{bin: bin, pane: pane, seq: time.Now().UnixMilli()}
}

// Report tells herdr the pane's state: "idle", "working", "blocked" or
// "unknown". herdr turns an idle after unseen work into "done" itself.
// tokens are shown where herdr's sidebar rows name them ($summary, …).
// It runs in the background and drops a report identical to the last.
func (c *Client) Report(state, message, title string, tokens map[string]string) {
	if c == nil {
		return
	}
	names := make([]string, 0, len(tokens))
	for k := range tokens {
		names = append(names, k)
	}
	sort.Strings(names)
	key := state + "\x00" + message + "\x00" + title
	for _, k := range names {
		key += "\x00" + k + "=" + tokens[k]
	}
	c.mu.Lock()
	if key == c.last {
		c.mu.Unlock()
		return
	}
	c.last = key
	c.lastState = state
	c.seq++
	seq := c.seq
	c.mu.Unlock()

	go func() {
		args := []string{"pane", "report-agent", c.pane, "--source", source, "--agent", source,
			"--state", state, "--seq", strconv.FormatInt(seq, 10)}
		if message != "" {
			args = append(args, "--message", message)
		}
		_ = run(c.bin, args...)
		if title != "" || len(names) > 0 {
			args := []string{"pane", "report-metadata", c.pane, "--source", source,
				"--agent", source, "--seq", strconv.FormatInt(seq, 10)}
			if title != "" {
				args = append(args, "--title", title)
			}
			for _, k := range names {
				if tokens[k] == "" {
					args = append(args, "--clear-token", k)
				} else {
					args = append(args, "--token", k+"="+tokens[k])
				}
			}
			_ = run(c.bin, args...)
		}
	}()
}

// State is the state last reported.
func (c *Client) State() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastState
}

// Notify shows a notification through herdr, which delivers it the way the
// user has set up (a toast, the desktop, a sound). sound is "none", "done"
// or "request".
func (c *Client) Notify(title, body, sound string) {
	if c == nil {
		return
	}
	go func() {
		_ = run(c.bin, "notification", "show", title, "--body", body, "--sound", sound)
	}()
}

// Release tells herdr nerdr is gone from the pane.
func (c *Client) Release() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.seq++
	seq := c.seq
	c.mu.Unlock()
	_ = run(c.bin, "pane", "release-agent", c.pane, "--source", source, "--agent", source, "--seq", strconv.FormatInt(seq, 10))
}

// RenameTab sets the herdr tab's label.
func (c *Client) RenameTab(label string) {
	if c == nil || label == "" {
		return
	}
	go func() {
		tab := os.Getenv("HERDR_TAB_ID")
		if tab == "" {
			return
		}
		_ = run(c.bin, "tab", "rename", tab, label)
	}()
}

// ChildEnv strips herdr's variables from the environment the agents get.
// An agent with herdr's hooks installed would otherwise report its state
// as nerdr's pane's, and every tab's agent would overwrite the others'.
func ChildEnv(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if strings.HasPrefix(kv, "HERDR_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func run(bin string, args ...string) error {
	cmd := proc.Detach(exec.Command(bin, args...))
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		return <-done
	}
}
