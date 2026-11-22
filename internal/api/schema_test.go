package api

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/core"
)

// The schema is made here, from the types the API sends and takes, and
// written to schema.json (embedded as Schema) and to the docs, which
// serve it at https://nerdr.app/api/schema.json. Make it anew with
//
//	go test ./internal/api -run TestSchemaIsCurrent -update-schema
var updateSchema = flag.Bool("update-schema", false, "write the API's schema anew")

// schemaFiles are where the schema is kept, from this package's folder.
var schemaFiles = []string{"schema.json", "../../docs/public/api/schema.json"}

// opSpec is an op as the schema has it: the fields of Request it takes,
// those of them it needs, and what its result is. All that is not made
// from the types is here, and the tests below check it against the code.
type opSpec struct {
	op, desc string
	fields   []string
	required []string
	result   reflect.Type // nil: an empty object
}

var schemaOps = []opSpec{
	{op: "state", desc: "Every tab with its agents, terminals and branches, the agents that need the user, nerdr's open questions, and the agents that can be started.",
		result: reflect.TypeFor[core.APIState]()},
	{op: "subscribe", desc: "Have the core send events from now on."},
	{op: "read", desc: "The last lines (80 if not given) of what an agent or a terminal shows, as plain text. target is an agent's or a terminal's ID; agent works too, for an agent.",
		fields: []string{"target", "agent", "lines"},
		result: reflect.TypeFor[ReadResult]()},
	{op: "send", desc: "Queue text as a message for agent, typed in once it is at its prompt.",
		fields: []string{"agent", "text"}, required: []string{"agent", "text"}},
	{op: "type", desc: "Type into an agent or a terminal now: text as a paste, then keys. Give text, keys or both.",
		fields: []string{"target", "agent", "text", "keys"}},
	{op: "answer", desc: "Answer one of nerdr's questions by its ID; yes left out is no.",
		fields: []string{"question", "yes"}, required: []string{"question"}},
	{op: "seen", desc: "Count as the user looking at the agent, as visiting its tab does.",
		fields: []string{"agent"}, required: []string{"agent"}},
	{op: "wake", desc: "Wake the agents of a tab put to sleep while it was not looked at.",
		fields: []string{"tab"}, required: []string{"tab"}},
	{op: "repos", desc: "The repositories a tab can be made in.",
		result: reflect.TypeFor[[]core.APIRepo]()},
	{op: "new_tab", desc: "Make a tab on a new branch in the repositories named, its agent (the first of state's agents when not given) started with task. The tab comes with a tab_opened event once its worktrees are made, or a notice says why not.",
		fields: []string{"repos", "branch", "agent", "model", "task"}, required: []string{"repos", "branch"},
		result: reflect.TypeFor[NewTabResult]()},
	{op: "new_terminal", desc: "Open a terminal in a tab's worktree, in a new pane, last; nerdr's window keeps the keyboard where it is. The tab gets a tab event with the terminal.",
		fields: []string{"tab"}, required: []string{"tab"},
		result: reflect.TypeFor[OpenedResult]()},
	{op: "new_agent", desc: "Start an agent of the kind named (one of state's agents) beside a tab's own, in a new pane, with model if given and task as its first message if given; nerdr's window keeps the keyboard where it is. It comes with an agent event.",
		fields: []string{"tab", "agent", "model", "task"}, required: []string{"tab", "agent"},
		result: reflect.TypeFor[OpenedResult]()},
	{op: "review", desc: "Start a code review of an agent's work by an agent of the kind by (the one the config gives when not given), with model if given; or add that reviewer to the review going on. The reviewer comes with an agent event once the worktree is looked at, or a notice says why not, such as there being nothing to review.",
		fields: []string{"agent", "by", "model"}, required: []string{"agent"}},
	{op: "close_tab", desc: "Close a tab, stopping everything in it, and answer once it is closed. With remove its worktrees are deleted too, never by force: it is refused, the tab left open, if that would lose changes not committed or commits not pushed. The branches are kept, unless each one's pull request is merged with the head it has.",
		fields: []string{"tab", "remove"}, required: []string{"tab"},
		result: reflect.TypeFor[ClosedResult]()},
	{op: "quit", desc: "Stop nerdr for its folder, as quitting in its window does: every agent, terminal and plugin stops, and the tabs are kept. The connection then closes, maybe before the answer comes."},
}

// eventSpec is an event as the schema has it: the fields of Event it
// carries, and those of them always there.
type eventSpec struct {
	event, desc string
	fields      []string
	required    []string
}

var schemaEvents = []eventSpec{
	{event: "tab_opened", desc: "A tab is there, made by anyone. Its agents each get an agent event too.",
		fields: []string{"tab"}, required: []string{"tab"}},
	{event: "tab_closed", desc: "A tab is gone.",
		fields: []string{"tab_id", "name"}, required: []string{"tab_id", "name"}},
	{event: "tab", desc: "A tab's name, task, status, branches or terminals changed.",
		fields: []string{"tab"}, required: []string{"tab"}},
	{event: "agent", desc: "An agent's state changed: was is the state before, left out for an agent just come; agent.state is \"gone\" for one gone. name is its tab's.",
		fields: []string{"tab_id", "name", "agent", "was"}, required: []string{"tab_id", "name", "agent"}},
	{event: "waiting", desc: "The agents that need the user changed: their IDs, neediest first.",
		fields: []string{"waiting"}, required: []string{"waiting"}},
	{event: "question", desc: "nerdr asks the user something.",
		fields: []string{"question"}, required: []string{"question"}},
	{event: "answered", desc: "A question is answered, here or in another client, or its time ran out.",
		fields: []string{"question_id"}, required: []string{"question_id"}},
	{event: "notice", desc: "What nerdr tells the user in its status bar; important for what it keeps on screen until seen.",
		fields: []string{"text", "important"}, required: []string{"text"}},
	{event: "lost", desc: "The client read too slowly and events were dropped: get the state afresh."},
}

func obj(props map[string]any, required ...string) map[string]any {
	o := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		o["required"] = required
	}
	return o
}

// schemaGen makes the schema's parts from Go types.
type schemaGen struct {
	defs  map[string]any
	names map[string]reflect.Type
	docs  map[string]string
}

// schemaName is what a type is called in the schema: its Go name, less
// the API and JSON that tell it apart in Go.
func schemaName(t reflect.Type) string {
	return strings.TrimSuffix(strings.TrimPrefix(t.Name(), "API"), "JSON")
}

func (g *schemaGen) of(t reflect.Type) map[string]any {
	switch t {
	case reflect.TypeFor[json.RawMessage]():
		return map[string]any{}
	case reflect.TypeFor[time.Time]():
		return map[string]any{"type": "string", "format": "date-time"}
	}
	switch t.Kind() {
	case reflect.Pointer:
		return g.of(t.Elem())
	case reflect.Interface:
		return map[string]any{}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": g.of(t.Elem())}
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			panic("schema: a map's keys must be strings: " + t.String())
		}
		return map[string]any{"type": "object", "additionalProperties": g.of(t.Elem())}
	case reflect.Struct:
		name := schemaName(t)
		if was, ok := g.names[name]; ok && was != t {
			panic(fmt.Sprintf("schema: %s and %s are both %s", was, t, name))
		}
		if _, ok := g.names[name]; !ok {
			g.names[name] = t
			g.defs[name] = g.object(t, nil, nil)
		}
		return map[string]any{"$ref": "#/$defs/" + name}
	}
	panic("schema: no schema for " + t.String())
}

// jsonField is a field of a struct as encoding/json sees it.
type jsonField struct {
	goName, name string
	optional     bool
	t            reflect.Type
}

func jsonFields(t reflect.Type) []jsonField {
	var out []jsonField
	for f := range t.Fields() {
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if name == "" {
			name = f.Name
		}
		o := strings.Split(opts, ",")
		out = append(out, jsonField{goName: f.Name, name: name, t: f.Type,
			optional: slices.Contains(o, "omitempty") || slices.Contains(o, "omitzero")})
	}
	return out
}

// object is struct t's schema, with only the fields named in only when it
// is not nil, and required in place of those that are not omitted when
// empty when it is not nil.
func (g *schemaGen) object(t reflect.Type, only, required []string) map[string]any {
	props := map[string]any{}
	var req []string
	for _, f := range jsonFields(t) {
		if only != nil && !slices.Contains(only, f.name) {
			continue
		}
		s := g.of(f.t)
		if f.t.Kind() == reflect.Pointer && !f.optional {
			s = map[string]any{"anyOf": []any{s, map[string]any{"type": "null"}}}
		}
		if only == nil {
			if d := g.doc(t.PkgPath(), t.Name()+"."+f.goName, f.goName, f.name); d != "" {
				s = withDesc(s, d)
			}
		}
		props[f.name] = s
		if required == nil && !f.optional {
			req = append(req, f.name)
		}
	}
	for _, n := range only {
		if _, ok := props[n]; !ok {
			panic(fmt.Sprintf("schema: %s has no field %q", t, n))
		}
	}
	if required != nil {
		req = required
	}
	o := obj(props, req...)
	if d := g.doc(t.PkgPath(), t.Name(), t.Name(), schemaName(t)); d != "" && only == nil {
		o["description"] = d
	}
	return o
}

// withDesc is s with a description; a $ref is wrapped, as its siblings
// are passed by in older drafts.
func withDesc(s map[string]any, d string) map[string]any {
	if _, ok := s["$ref"]; ok {
		return map[string]any{"allOf": []any{s}, "description": d}
	}
	c := map[string]any{"description": d}
	for k, v := range s {
		c[k] = v
	}
	return c
}

// doc is the doc comment of key in package pkg, in one line, with the
// Go name it begins with put as the schema calls it.
func (g *schemaGen) doc(pkg, key, goName, name string) string {
	d := g.docs[filepath.Base(pkg)+"."+key]
	if rest, ok := strings.CutPrefix(d, goName+" "); ok {
		d = name + " " + rest
	}
	return d
}

// readDocs reads the doc comments of the types and their fields in the
// packages in dirs.
func readDocs(t *testing.T, dirs ...string) map[string]string {
	t.Helper()
	docs := map[string]string{}
	fset := token.NewFileSet()
	for _, dir := range dirs {
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			pkg := f.Name.Name
			for _, d := range f.Decls {
				gd, ok := d.(*ast.GenDecl)
				if !ok || gd.Tok != token.TYPE {
					continue
				}
				for _, sp := range gd.Specs {
					ts := sp.(*ast.TypeSpec)
					doc := ts.Doc
					if doc == nil && len(gd.Specs) == 1 {
						doc = gd.Doc
					}
					docs[pkg+"."+ts.Name.Name] = oneLine(doc)
					st, ok := ts.Type.(*ast.StructType)
					if !ok {
						continue
					}
					for _, fl := range st.Fields.List {
						c := fl.Doc
						if c == nil {
							c = fl.Comment
						}
						for _, n := range fl.Names {
							docs[pkg+"."+ts.Name.Name+"."+n.Name] = oneLine(c)
						}
					}
				}
			}
		}
	}
	return docs
}

func oneLine(c *ast.CommentGroup) string {
	if c == nil {
		return ""
	}
	return strings.Join(strings.Fields(c.Text()), " ")
}

// camel is s, as in new_tab, in the camel case of a type: NewTab.
func camel(s string) string {
	var b strings.Builder
	for _, w := range strings.Split(s, "_") {
		if w != "" {
			b.WriteString(strings.ToUpper(w[:1]) + w[1:])
		}
	}
	return b.String()
}

// makeSchema is the schema, as it is written.
func makeSchema(t *testing.T) []byte {
	t.Helper()
	g := &schemaGen{defs: map[string]any{}, names: map[string]reflect.Type{}, docs: readDocs(t, ".", "../core")}
	ref := func(name string) map[string]any { return map[string]any{"$ref": "#/$defs/" + name} }

	g.of(reflect.TypeFor[Hello]())
	g.of(reflect.TypeFor[Welcome]())
	answer := g.object(reflect.TypeFor[Answer](), nil, nil)
	answer["description"] = "Answer is the core's answer to a request, with the request's id: result, or error, a sentence for people. A line with only an error, before the welcome, is why the core refused the client. x-nerdr-ops gives each op's result."
	g.defs["Answer"] = answer

	req := reflect.TypeFor[Request]()
	var reqs []any
	ops := map[string]any{}
	for _, o := range schemaOps {
		s := g.object(req, append([]string{"id", "op"}, o.fields...), append([]string{"op"}, o.required...))
		s["properties"].(map[string]any)["op"] = map[string]any{"const": o.op}
		s["description"] = o.desc
		name := camel(o.op) + "Request"
		g.defs[name] = s
		reqs = append(reqs, ref(name))
		result := map[string]any{"type": "object"}
		if o.result != nil {
			result = g.of(o.result)
		}
		ops[o.op] = map[string]any{"request": ref(name), "result": result}
	}
	g.defs["Request"] = map[string]any{"description": "A line a client sends after hello: op, what to do, and the parameters it takes. id is any JSON value, sent back with the answer.",
		"oneOf": reqs}

	ev := reflect.TypeFor[Event]()
	var evs []any
	for _, e := range schemaEvents {
		s := g.object(ev, append([]string{"event"}, e.fields...), append([]string{"event"}, e.required...))
		s["properties"].(map[string]any)["event"] = map[string]any{"const": e.event}
		s["description"] = e.desc
		name := camel(e.event) + "Event"
		g.defs[name] = s
		evs = append(evs, ref(name))
	}
	g.defs["Event"] = map[string]any{"description": "What a subscribed client is told as it happens: a line with an event and no id.",
		"oneOf": evs}

	g.defs["ClientLine"] = map[string]any{"description": "A line a client sends: hello first, then requests.",
		"anyOf": []any{ref("Hello"), ref("Request")}}
	g.defs["CoreLine"] = map[string]any{"description": "A line the core sends: the welcome first, then answers and, once subscribed, events.",
		"anyOf": []any{ref("Welcome"), ref("Answer"), ref("Event")}}

	root := map[string]any{
		"$schema":     "https://json-schema.org/draft/2020-12/schema",
		"$id":         "https://nerdr.app/api/schema.json",
		"title":       "nerdr API",
		"description": fmt.Sprintf("nerdr's API, version %d: JSON, one object a line, over nerdr api FOLDER or the API socket. What is added to a version (fields, ops, events) does not raise it, so a client must pass by what it does not know.", core.APIVersion),
		"x-nerdr-api": core.APIVersion,
		"anyOf":       []any{ref("ClientLine"), ref("CoreLine")},
		"$defs":       g.defs,
		"x-nerdr-ops": ops,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(root); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestSchemaIsCurrent fails when a type of the API changed and the schema
// was not made anew.
func TestSchemaIsCurrent(t *testing.T) {
	want := makeSchema(t)
	for _, path := range schemaFiles {
		if *updateSchema {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, want, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s is not the API's schema as the types have it now: make it anew with\n"+
				"  go test ./internal/api -run TestSchemaIsCurrent -update-schema", path)
		}
	}
	if !*updateSchema && !bytes.Equal(Schema, want) {
		t.Error("the Schema embedded is not schema.json's; build again")
	}
}

// TestSchemaIsValidJSON checks the schema is JSON, and that every $ref in
// it is to one of its $defs.
func TestSchemaIsValidJSON(t *testing.T) {
	var root map[string]any
	if err := json.Unmarshal(Schema, &root); err != nil {
		t.Fatal(err)
	}
	defs := root["$defs"].(map[string]any)
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if r, ok := v["$ref"].(string); ok {
				if _, ok := defs[strings.TrimPrefix(r, "#/$defs/")]; !ok {
					t.Errorf("$ref %s is to nothing", r)
				}
			}
			for _, x := range v {
				walk(x)
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(root)
}

// stringsIn are the string literals in this package's code (not its
// tests) that pick picks.
func stringsIn(t *testing.T, pick func(n ast.Node) []ast.Expr) []string {
	t.Helper()
	fset := token.NewFileSet()
	files, _ := filepath.Glob("*.go")
	seen := map[string]bool{}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			for _, e := range pick(n) {
				if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if s, err := strconv.Unquote(lit.Value); err == nil && s != "" {
						seen[s] = true
					}
				}
			}
			return true
		})
	}
	var out []string
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// TestSchemaHasEveryOp checks schemaOps has each op the core answers, and
// nothing more; and that each field of Request is taken by some op.
func TestSchemaHasEveryOp(t *testing.T) {
	inDo := false
	code := stringsIn(t, func(n ast.Node) []ast.Expr {
		if fd, ok := n.(*ast.FuncDecl); ok {
			inDo = fd.Recv != nil && fd.Name.Name == "do"
		}
		if cc, ok := n.(*ast.CaseClause); ok && inDo {
			return cc.List
		}
		return nil
	})
	var spec []string
	used := map[string]bool{"id": true, "op": true}
	for _, o := range schemaOps {
		spec = append(spec, o.op)
		for _, f := range o.fields {
			used[f] = true
		}
	}
	sort.Strings(spec)
	if !slices.Equal(code, spec) {
		t.Errorf("the ops the core answers are %v, and schemaOps has %v: put the op in schemaOps", code, spec)
	}
	for _, f := range jsonFields(reflect.TypeFor[Request]()) {
		if !used[f.name] {
			t.Errorf("no op in schemaOps takes Request's %q: name it in the op's fields", f.name)
		}
	}
}

// TestSchemaHasEveryEvent checks schemaEvents has each event the core
// sends, and nothing more; and that each field of Event is in some event.
func TestSchemaHasEveryEvent(t *testing.T) {
	code := stringsIn(t, func(n ast.Node) []ast.Expr {
		if kv, ok := n.(*ast.KeyValueExpr); ok {
			if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "Event" {
				return []ast.Expr{kv.Value}
			}
		}
		return nil
	})
	var spec []string
	used := map[string]bool{"event": true}
	for _, e := range schemaEvents {
		spec = append(spec, e.event)
		for _, f := range e.fields {
			used[f] = true
		}
	}
	sort.Strings(spec)
	if !slices.Equal(code, spec) {
		t.Errorf("the events the core sends are %v, and schemaEvents has %v: put the event in schemaEvents", code, spec)
	}
	for _, f := range jsonFields(reflect.TypeFor[Event]()) {
		if !used[f.name] {
			t.Errorf("no event in schemaEvents has Event's %q: name it in the event's fields", f.name)
		}
	}
}
