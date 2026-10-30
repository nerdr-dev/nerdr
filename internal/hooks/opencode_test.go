package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// openCodeEvents are the events nerdr's opencode plugin passes on.
var openCodeEvents = []string{
	"session.created",
	"session.execution.started",
	"session.execution.succeeded",
	"session.execution.failed",
	"session.execution.interrupted",
	"permission.asked",
	"permission.replied",
}

// TestOpenCodeStateFollowsTheTurn covers what each of opencode's events
// means: a turn's execution is work until it succeeds, fails or is
// interrupted, and a permission waits for the user.
func TestOpenCodeStateFollowsTheTurn(t *testing.T) {
	for _, c := range []struct {
		ev   OpenCodeEvent
		want string
	}{
		{OpenCodeEvent{Type: "session.execution.started"}, "working"},
		{OpenCodeEvent{Type: "session.execution.succeeded"}, "idle"},
		{OpenCodeEvent{Type: "session.execution.failed"}, "idle"},
		{OpenCodeEvent{Type: "session.execution.interrupted"}, "idle"},
		{OpenCodeEvent{Type: "permission.asked"}, "blocked"},
		{OpenCodeEvent{Type: "permission.replied"}, "working"},
		{OpenCodeEvent{Type: "session.created"}, ""},
		{OpenCodeEvent{Type: "session.step.started"}, ""},
		// A subagent's turn starts and ends inside the agent's own.
		{OpenCodeEvent{Type: "session.execution.started", ParentID: "ses_p"}, ""},
		{OpenCodeEvent{Type: "session.execution.succeeded", ParentID: "ses_p"}, ""},
		// But its question waits for the user all the same.
		{OpenCodeEvent{Type: "permission.asked", ParentID: "ses_p"}, "blocked"},
	} {
		if got := OpenCodeState(c.ev); got != c.want {
			t.Errorf("%+v: %q, want %q", c.ev, got, c.want)
		}
	}
}

// TestOpenCodeReportsItsSessionNotASubagents covers which conversation the
// reports say the agent is in: a new one of its own, never a subagent's.
func TestOpenCodeReportsItsSessionNotASubagents(t *testing.T) {
	method, p := openCodeReport(OpenCodeEvent{Type: "session.created", SessionID: "ses_1", Seq: 5}, "tab1")
	if method != ReportAgentSession || p["agent_session_id"] != "ses_1" || p["state"] != nil || p["seq"] != int64(5) || p["source"] != OpenCodeSource {
		t.Fatalf("%s %v", method, p)
	}
	if method, p := openCodeReport(OpenCodeEvent{Type: "session.created", SessionID: "ses_2", ParentID: "ses_1"}, "tab1"); method != "" {
		t.Fatalf("a subagent's session reported: %s %v", method, p)
	}
	method, p = openCodeReport(OpenCodeEvent{Type: "permission.asked", SessionID: "ses_2", ParentID: "ses_1"}, "tab1")
	if method != ReportAgent || p["state"] != "blocked" || p["agent_session_id"] != nil {
		t.Fatalf("%s %v", method, p)
	}
	if method, _ := openCodeReport(OpenCodeEvent{Type: "session.step.started", SessionID: "ses_1"}, "tab1"); method != "" {
		t.Fatalf("an event that says nothing reported: %s", method)
	}
}

// TestOpenCodeCommandReportsToTheTab covers `nerdr hook opencode` end to
// end: the plugin's JSON on stdin reaches nerdr as the tab's report.
func TestOpenCodeCommandReportsToTheTab(t *testing.T) {
	s, got := listen(t)
	t.Setenv("NERDR_SOCKET", s.Path())
	t.Setenv("NERDR_TAB", "tab1")
	var stderr bytes.Buffer
	in := `{"type":"session.execution.succeeded","sessionID":"ses_1","parentID":"","created":1792344870924,"seq":1792344870924001}`
	if code := OpenCodeCommand(strings.NewReader(in), &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	r := next(t, got)
	if r.Method != ReportAgent || r.Pane != "tab1" || r.Source != OpenCodeSource || r.Agent != "opencode" ||
		r.State != "idle" || r.SessionID != "ses_1" || r.Seq != 1792344870924001 {
		t.Fatalf("report = %+v", r)
	}
	if code := OpenCodeCommand(strings.NewReader("not json"), &stderr); code == 0 {
		t.Fatal("no event, but exit 0")
	}
}

// TestOpenCodePluginHearsEveryEventNerdrReads covers the plugin: in the
// shape opencode 2 loads, marked as nerdr's, and subscribed to each event
// OpenCodeState and openCodeReport read.
func TestOpenCodePluginHearsEveryEventNerdrReads(t *testing.T) {
	js := string(OpenCodePlugin)
	for _, want := range []string{OpenCodeMarker, "export default {", `id: "nerdr"`, "setup:", `["hook", "opencode"]`, "NERDR_OPENCODE_HOOK"} {
		if !strings.Contains(js, want) {
			t.Errorf("no %s in the plugin", want)
		}
	}
	for _, ev := range openCodeEvents {
		if !strings.Contains(js, `"`+ev+`"`) {
			t.Errorf("the plugin does not pass on %s", ev)
		}
	}
}

// TestOpenCodePluginKeepsTheClaimAcrossReloads covers opencode loading a
// location's plugins again in the same process: it evicts an idle
// location, and boots it — plugin included — at the next message, by
// which time the variable marking the tab's opencode is long gone. The
// plugin must still report, and take the variable out so that a nested
// opencode does not; one that never had it must not report, nor claim the
// process.
func TestOpenCodePluginKeepsTheClaimAcrossReloads(t *testing.T) {
	const src = `const plugin = (await import(process.argv[2])).default
const setups = Number(process.argv[3])
let subscriptions = 0
const ctx = { event: { subscribe: () => { subscriptions++; return (async function* () {})() } } }
for (let i = 0; i < setups; i++) await plugin.setup(ctx)
console.log(JSON.stringify({
  subscriptions,
  hook: process.env.NERDR_OPENCODE_HOOK ?? "",
  claimed: globalThis.__nerdrOpencodeHook === true,
}))
`
	for _, c := range []struct {
		name    string
		hooked  bool
		setups  int
		subs    int
		claimed bool
	}{
		{"the tab's opencode", true, 1, len(openCodeEvents), true},
		{"a reload in the same process", true, 2, 2 * len(openCodeEvents), true},
		{"not the tab's", false, 1, 0, false},
		{"not the tab's, reloaded", false, 2, 0, false},
	} {
		var env []string
		if c.hooked {
			env = append(env, "NERDR_OPENCODE_HOOK=1")
		}
		var got struct {
			Subscriptions int
			Hook          string
			Claimed       bool
		}
		out := runOpenCodePlugin(t, src, []string{strconv.Itoa(c.setups)}, env...)
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("%s: node said %q", c.name, out)
		}
		if got.Subscriptions != c.subs || got.Claimed != c.claimed {
			t.Errorf("%s: %d subscriptions, claimed %v; want %d, %v", c.name, got.Subscriptions, got.Claimed, c.subs, c.claimed)
		}
		if got.Hook != "" {
			t.Errorf("%s: NERDR_OPENCODE_HOOK survived the setup: %q", c.name, got.Hook)
		}
	}
}

// TestOpenCodePluginOnlyTheNewestSetupReports covers streams a setup left
// behind when opencode evicted its location outliving the reboot: both
// still yield, and only the newest setup may report. Otherwise each event
// is reported twice, and nerdr drops them as out of order in whatever
// order they arrive.
func TestOpenCodePluginOnlyTheNewestSetupReports(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "record")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n{ cat; echo; } >> \"$NERDR_TEST_OUT\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "reports")
	const src = `import fs from "node:fs"

const plugin = (await import(process.argv[2])).default
const out = process.argv[3]

function channel() {
  const queue = []
  const waiting = []
  return {
    push(value) {
      const wake = waiting.shift()
      if (wake) wake(value)
      else queue.push(value)
    },
    [Symbol.asyncIterator]() {
      return {
        next: () => queue.length
          ? Promise.resolve({ value: queue.shift(), done: false })
          : new Promise((resolve) => waiting.push((value) => resolve({ value, done: false }))),
      }
    },
  }
}

const streams = []
const ctx = { event: { subscribe: (type) => {
  const stream = channel()
  streams.push({ type, stream })
  return stream
} } }

await plugin.setup(ctx)
const first = streams.length
await plugin.setup(ctx)
const second = streams.length - first

// The streams of both setups still yield: those of the first must not report.
for (const { type, stream } of streams) {
  stream.push({ type, data: { sessionID: "ses_1" }, created: Date.now() })
}
await new Promise((resolve) => setTimeout(resolve, 1500))
const reports = fs.existsSync(out)
  ? fs.readFileSync(out, "utf8").trim().split("\n").filter(Boolean).length
  : 0
console.log(JSON.stringify({ first, second, reports }))
`
	var got struct{ First, Second, Reports int }
	if err := json.Unmarshal([]byte(runOpenCodePlugin(t, src, []string{out},
		"NERDR_OPENCODE_HOOK=1", "NERDR_BIN="+bin, "NERDR_TEST_OUT="+out)), &got); err != nil {
		t.Fatal(err)
	}
	if got.First != len(openCodeEvents) || got.Second != len(openCodeEvents) {
		t.Fatalf("setups subscribed %d and %d, want %d each", got.First, got.Second, len(openCodeEvents))
	}
	if got.Reports != len(openCodeEvents) {
		t.Errorf("the two setups reported %d events together, want %d: the old streams must stop", got.Reports, len(openCodeEvents))
	}
}

// TestOpenCodePluginAddsTheNote covers nerdr's note on the tab: added to
// the system prompt of each request, after what opencode put there, and
// kept across a reload as the claim is, by the hook taken last, but taken
// out of the environment so that a nested opencode is not told it; none
// without one, nor with an opencode whose plugins have no session hooks.
func TestOpenCodePluginAddsTheNote(t *testing.T) {
	const src = `const plugin = (await import(process.argv[2])).default
const setups = Number(process.argv[3])
const hooks = []
const ctx = {
  event: { subscribe: () => (async function* () {})() },
  session: { hook: async (name, fn) => { hooks.push({ name, fn }) } },
}
const refused = {
  event: ctx.event,
  session: { hook: async () => { throw new Error("refused") } },
}
for (let i = 0; i < setups; i++) {
  const mode = process.argv[4]
  await plugin.setup(mode === "old" ? { event: ctx.event } : mode === "refused" && i > 0 ? refused : ctx)
}
const event = { system: [{ type: "text", text: "opencode's own" }] }
for (const h of hooks) if (h.name === "context") h.fn(event)
console.log(JSON.stringify({
  hooks: hooks.map((h) => h.name),
  system: event.system.map((p) => p.text),
  env: process.env.NERDR_OPENCODE_NOTE ?? "",
}))
`
	for _, c := range []struct {
		name   string
		env    []string
		args   []string
		hooks  int
		system []string
	}{
		{"told", []string{"NERDR_OPENCODE_NOTE=the note"}, []string{"1", ""}, 1, []string{"opencode's own", "the note"}},
		{"reloaded", []string{"NERDR_OPENCODE_NOTE=the note"}, []string{"2", ""}, 2, []string{"opencode's own", "the note"}},
		{"a reload's hook refused", []string{"NERDR_OPENCODE_NOTE=the note"}, []string{"2", "refused"}, 1, []string{"opencode's own", "the note"}},
		{"no note", nil, []string{"1", ""}, 0, []string{"opencode's own"}},
		{"no session hooks", []string{"NERDR_OPENCODE_NOTE=the note"}, []string{"1", "old"}, 0, []string{"opencode's own"}},
	} {
		var got struct {
			Hooks  []string
			System []string
			Env    string
		}
		out := runOpenCodePlugin(t, src, c.args, c.env...)
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("%s: node said %q", c.name, out)
		}
		if len(got.Hooks) != c.hooks || !slices.Equal(got.System, c.system) {
			t.Errorf("%s: hooks %q, system %q; want %d hooks, %q", c.name, got.Hooks, got.System, c.hooks, c.system)
		}
		if got.Env != "" {
			t.Errorf("%s: NERDR_OPENCODE_NOTE survived the setup: %q", c.name, got.Env)
		}
	}
}

// runOpenCodePlugin writes nerdr's plugin and the harness src to a temp
// folder, and runs node on the harness with the plugin's path and the
// variables its setup reads; env overrides them. It returns what node
// printed.
func runOpenCodePlugin(t *testing.T, src string, args []string, env ...string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node:", err)
	}
	dir := t.TempDir()
	plugin := filepath.Join(dir, "nerdr.js")
	if err := os.WriteFile(plugin, OpenCodePlugin, 0o644); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(dir, "harness.mjs")
	if err := os.WriteFile(harness, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	envs := map[string]string{
		"PATH":         os.Getenv("PATH"),
		"HOME":         dir,
		"NERDR_BIN":    filepath.Join(dir, "nerdr"),
		"NERDR_TAB":    "tab1",
		"NERDR_SOCKET": filepath.Join(dir, "nerdr.sock"),
	}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		envs[k] = v
	}
	cmd := exec.Command(node, append([]string{harness, plugin}, args...)...)
	for k, v := range envs {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v: %s", err, out)
	}
	return string(out)
}

// TestOpenCodePluginInstallUpdatesOnlyNerdrs covers installing the plugin:
// written when missing, an outdated copy of nerdr's brought up to date,
// and one the user took over left alone.
func TestOpenCodePluginInstallUpdatesOnlyNerdrs(t *testing.T) {
	home := t.TempDir()
	file := OpenCodePluginHome(home)
	if file != filepath.Join(home, ".config", "opencode", "plugins", "nerdr.js") {
		t.Fatalf("home: %s", file)
	}
	if p := OpenCodePluginProject("/w"); p != filepath.Join("/w", ".opencode", "plugins", "nerdr.js") || !strings.HasSuffix(p, filepath.FromSlash(OpenCodePluginExclude)) {
		t.Fatalf("project: %s", p)
	}
	if wrote, err := InstallOpenCodePlugin(file); !wrote || err != nil {
		t.Fatalf("first install: %v %v", wrote, err)
	}
	if wrote, _ := InstallOpenCodePlugin(file); wrote {
		t.Fatal("an up-to-date install should write nothing")
	}
	_ = os.WriteFile(file, []byte(OpenCodeMarker+" old\n"), 0o644)
	if wrote, _ := InstallOpenCodePlugin(file); !wrote {
		t.Fatal("nerdr's outdated copy was not updated")
	}
	if b, _ := os.ReadFile(file); !bytes.Equal(b, OpenCodePlugin) {
		t.Fatal("the update did not write the plugin")
	}
	_ = os.WriteFile(file, []byte("// mine\n"), 0o644)
	if wrote, _ := InstallOpenCodePlugin(file); wrote {
		t.Fatal("the user's own copy was replaced")
	}
}
