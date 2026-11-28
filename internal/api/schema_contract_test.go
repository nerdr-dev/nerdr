package api

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/attach"
	"github.com/nerdr-dev/nerdr/internal/core"
)

// The tests here check what the API sends, encoded as it is sent, against
// the schema: strictly, so a field the schema does not have where it is
// sent fails as well as one it needs and does not get.

// schemaDoc is the schema, decoded.
func schemaDoc(t *testing.T) map[string]any {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(Schema, &root); err != nil {
		t.Fatal(err)
	}
	return root
}

// validate is why JSON value v (decoded) is not what schema s says, ""
// when it is. An object with properties may have only those.
func validate(root, s map[string]any, v any, at string) string {
	if r, ok := s["$ref"].(string); ok {
		def, ok := root["$defs"].(map[string]any)[strings.TrimPrefix(r, "#/$defs/")].(map[string]any)
		if !ok {
			return at + ": $ref " + r + " is to nothing"
		}
		return validate(root, def, v, at)
	}
	if all, ok := s["allOf"].([]any); ok {
		for _, x := range all {
			if why := validate(root, x.(map[string]any), v, at); why != "" {
				return why
			}
		}
	}
	if one, ok := s["oneOf"].([]any); ok {
		var whys []string
		n := 0
		for _, x := range one {
			if why := validate(root, x.(map[string]any), v, at); why == "" {
				n++
			} else {
				whys = append(whys, why)
			}
		}
		if n != 1 {
			return fmt.Sprintf("%s: %d of oneOf match: %s", at, n, strings.Join(whys, "; "))
		}
	}
	if any_, ok := s["anyOf"].([]any); ok {
		var whys []string
		for _, x := range any_ {
			why := validate(root, x.(map[string]any), v, at)
			if why == "" {
				whys = nil
				break
			}
			whys = append(whys, why)
		}
		if whys != nil {
			return fmt.Sprintf("%s: none of anyOf match: %s", at, strings.Join(whys, "; "))
		}
	}
	if c, ok := s["const"]; ok && c != v {
		return fmt.Sprintf("%s: %v is not %v", at, v, c)
	}
	switch s["type"] {
	case nil:
	case "null":
		if v != nil {
			return at + ": not null"
		}
	case "string":
		if _, ok := v.(string); !ok {
			return at + ": not a string"
		}
		if s["format"] == "date-time" {
			if _, err := time.Parse(time.RFC3339, v.(string)); err != nil {
				return at + ": not a date-time"
			}
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return at + ": not a boolean"
		}
	case "number":
		if _, ok := v.(float64); !ok {
			return at + ": not a number"
		}
	case "integer":
		if f, ok := v.(float64); !ok || f != math.Trunc(f) {
			return at + ": not an integer"
		}
	case "array":
		a, ok := v.([]any)
		if !ok {
			return at + ": not an array"
		}
		for i, x := range a {
			if why := validate(root, s["items"].(map[string]any), x, fmt.Sprintf("%s[%d]", at, i)); why != "" {
				return why
			}
		}
	case "object":
		o, ok := v.(map[string]any)
		if !ok {
			return at + ": not an object"
		}
		req, _ := s["required"].([]any)
		for _, r := range req {
			if _, ok := o[r.(string)]; !ok {
				return fmt.Sprintf("%s: no %q", at, r)
			}
		}
		props, _ := s["properties"].(map[string]any)
		extra, _ := s["additionalProperties"].(map[string]any)
		keys := make([]string, 0, len(o))
		for k := range o {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			p, ok := props[k].(map[string]any)
			switch {
			case ok:
			case extra != nil:
				p = extra
			case props != nil:
				return fmt.Sprintf("%s: %q is not in the schema here", at, k)
			default:
				continue
			}
			if why := validate(root, p, o[k], at+"."+k); why != "" {
				return why
			}
		}
	default:
		return fmt.Sprintf("%s: the validator knows no type %v", at, s["type"])
	}
	return ""
}

// encoded is v as the API sends it, decoded again.
func encoded(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// mustValidate fails t when v is not what the definition def says.
func mustValidate(t *testing.T, root map[string]any, def map[string]any, v any, what string) {
	t.Helper()
	if why := validate(root, def, v, what); why != "" {
		t.Errorf("%s does not match the schema: %s", what, why)
	}
}

func ref(name string) map[string]any { return map[string]any{"$ref": "#/$defs/" + name} }

// TestAnswersMatchTheSchema: the welcome, and each answer a core gives a
// client, is what x-nerdr-ops says the op's result is.
func TestAnswersMatchTheSchema(t *testing.T) {
	root := schemaDoc(t)
	ops := root["x-nerdr-ops"].(map[string]any)
	m := testCore()
	_, sock, keyFile := serve(t, m, nil)
	c := dial(t, sock)
	key, err := attach.ReadKey(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	c.send(Hello{Key: key, API: 1})
	mustValidate(t, root, ref("Welcome"), c.line(), "the welcome")

	reqs := []map[string]any{
		{"id": 1, "op": "state"},
		{"id": 2, "op": "subscribe"},
		{"id": 3, "op": "send", "agent": "t1", "text": "hi"},
		{"id": 4, "op": "seen", "agent": "t1"},
		{"id": 5, "op": "repos"},
		{"id": 6, "op": "read", "agent": "t1"},
		{"id": 7, "op": "dance"},
	}
	for _, r := range reqs {
		if r["op"] != "dance" { // no op: the schema rightly has none such
			mustValidate(t, root, ref("Request"), encoded(t, r), fmt.Sprint("request ", r["op"]))
		}
		c.send(r)
		var a map[string]any
		for a == nil || a["id"] == nil { // events come between, once subscribed
			a = c.line()
		}
		mustValidate(t, root, ref("Answer"), a, fmt.Sprint("the answer to ", r["op"]))
		if a["error"] != nil {
			continue
		}
		op := ops[r["op"].(string)].(map[string]any)
		mustValidate(t, root, op["result"].(map[string]any), a["result"], fmt.Sprint("the result of ", r["op"]))
	}
}

// TestResultTypesAreTheOnesDoReturns: each op whose result is a type of
// this package returns that type in do, so the schema's is the one sent
// even for the ops the test above cannot run.
func TestResultTypesAreTheOnesDoReturns(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "api.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The type names each case of do's switch mentions, by op.
	used := map[string][]string{}
	ast.Inspect(f, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok || fd.Recv == nil || fd.Name.Name != "do" {
			return true
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			cc, ok := n.(*ast.CaseClause)
			if !ok {
				return true
			}
			var names []string
			for _, st := range cc.Body {
				ast.Inspect(st, func(n ast.Node) bool {
					if cl, ok := n.(*ast.CompositeLit); ok {
						if id, ok := cl.Type.(*ast.Ident); ok {
							names = append(names, id.Name)
						}
						if _, ok := cl.Type.(*ast.MapType); ok {
							names = append(names, "map")
						}
					}
					return true
				})
			}
			for _, e := range cc.List {
				if lit, ok := e.(*ast.BasicLit); ok {
					op, _ := strconv.Unquote(lit.Value)
					used[op] = names
				}
			}
			return false
		})
		return false
	})
	for _, o := range schemaOps {
		if slices.Contains(used[o.op], "map") {
			t.Errorf("op %s answers with a map: give its result a type of its own, and name it in schemaOps", o.op)
		}
		if o.result != nil && o.result.PkgPath() == "github.com/nerdr-dev/nerdr/internal/api" && !slices.Contains(used[o.op], o.result.Name()) {
			t.Errorf("op %s: schemaOps says it answers with %s, and do does not make one", o.op, o.result.Name())
		}
	}
}

// TestEventsMatchTheSchema: every event the core sends is one of the
// schema's, with only the fields that event has there.
func TestEventsMatchTheSchema(t *testing.T) {
	root := schemaDoc(t)
	ctx := 41.0
	until := time.Date(2026, 11, 14, 21, 3, 0, 0, time.UTC)
	agent := core.APIAgent{ID: "a1", Num: 1, Kind: "claude", Model: "opus", State: "working", Reviewer: true, Messages: 1, Context: &ctx}
	tab := core.APITab{ID: "a1", Name: "fix", Group: "g", Dir: "/w/fix", Task: "fix it", Status: "testing",
		Agents:    []core.APIAgent{agent},
		Terminals: []core.APITerminal{{ID: "p1", Name: "terminal", Command: "make", Running: true}},
		Branches: []core.BranchJSON{{Repo: "web", Branch: "fix", Ahead: 1, Pushed: true, PR: 3, PRState: "OPEN",
			CI: "pass", URL: "https://example.com/pr/3", After: 1}}}
	q := core.APIQuestion{ID: 4, Kind: "plugin_run", Text: "may it?", Repos: []string{"web"}, Agent: "a1",
		Plugin: "deploy", Values: map[string]any{"env": "staging", "n": 2.0}, Dir: "/w/fix", Until: &until}
	empty := core.APIState{Root: "/w", Tabs: []core.APITab{}, Waiting: []string{}, Questions: []core.APIQuestion{}, Agents: []string{"claude"}}
	full := empty
	full.Tabs = []core.APITab{tab}
	full.Waiting = []string{"a1"}
	full.Questions = []core.APIQuestion{q}
	changed := full
	tab2 := tab
	tab2.Name = "fix2"
	agent2 := agent
	agent2.State = "blocked"
	tab2.Agents = []core.APIAgent{agent2}
	tab2.Spinoff = &core.APISpinoff{Done: 1, Busy: "making fix-a…", Failed: &core.APISpinoffFailed{Branch: "fix-a", Err: "taken"},
		Parts: []core.APISpinoffPart{{ID: "p1", Branch: "fix-a", Summary: "a", Commits: []string{"abc"}, Repos: []string{"web"},
			InRepos: []string{"web"}, Sandboxed: true, PromptReady: true, Kinds: []string{"claude"}, Kind: "claude"}}}
	changed.Tabs = []core.APITab{tab2}
	spunOff := changed
	tab3 := tab2
	tab3.Spinoff = nil
	spunOff.Tabs = []core.APITab{tab3}

	var evs []Event
	evs = append(evs, Diff(empty, full)...)
	evs = append(evs, Diff(full, changed)...)
	evs = append(evs, Diff(changed, spunOff)...)
	evs = append(evs, Diff(changed, empty)...)
	// What Observe and sendEvents make, not Diff.
	evs = append(evs, Event{Event: "notice", Text: "made fix", Important: true}, Event{Event: "lost"})
	mustValidate(t, root, ref("State"), encoded(t, full), "the state")
	mustValidate(t, root, ref("State"), encoded(t, changed), "the state with a spinoff")

	seen := map[string]bool{}
	for _, ev := range evs {
		seen[ev.Event] = true
		mustValidate(t, root, ref("Event"), encoded(t, ev), "event "+ev.Event)
	}
	for _, e := range schemaEvents {
		if !seen[e.event] {
			t.Errorf("no %s event was made to check: make one here", e.event)
		}
	}

	// And the validator does fail what does not match.
	bad := map[string]any{"event": "tab_closed", "tab_id": "a1", "name": "fix", "text": "stray"}
	if validate(root, ref("Event"), bad, "bad") == "" {
		t.Error("the validator takes a field an event does not have")
	}
	if validate(root, ref("ReadResult"), map[string]any{"output": "x"}, "bad") == "" {
		t.Error("the validator takes a result with the wrong field")
	}
}
