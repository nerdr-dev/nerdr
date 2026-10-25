package walkthrough

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, dir, file, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, file)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// gitRepo makes a repository on main with a change on branch work: a.go
// edited (oldCall removed, newCall added), b.go untouched, c.go new.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "pkg/a.go", "package pkg\n\nfunc A() {\n\toldCall()\n}\n")
	write(t, dir, "pkg/b.go", "package pkg\n\nfunc caller() {\n\tA()\n}\n")
	run(t, dir, "init", "-q", "-b", "main")
	run(t, dir, "add", ".")
	run(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "init")
	run(t, dir, "switch", "-q", "-c", "work")
	write(t, dir, "pkg/a.go", "package pkg\n\nfunc A() {\n\tnewCall()\n}\n")
	write(t, dir, "pkg/c.go", "package pkg\n\nfunc newCall() {}\n")
	return dir
}

func sample() Walkthrough {
	return Walkthrough{
		Title:   "swap the call",
		Summary: "A calls newCall now.",
		Steps: []Step{
			{ID: "new", Title: "the new function", Text: "added", Code: []Ref{{File: "pkg/c.go"}}},
			{ID: "swap", Title: "A uses it", Text: "swapped", Code: []Ref{{File: "pkg/a.go", Anchor: "newCall()", Note: "here"}}},
			{ID: "callers", Title: "callers", Text: "unchanged", Side: true, From: []string{"swap"},
				Code: []Ref{{File: "pkg/b.go", Anchor: "func caller()"}}},
		},
		Risks: []Risk{{Level: "low", Text: "nothing much"}, {Level: "High", Text: "names", File: "pkg/a.go", Anchor: "oldCall()"}},
	}
}

func TestAddAndLoad(t *testing.T) {
	dir := gitRepo(t)
	if _, err := Add(filepath.Join(dir, "pkg"), sample()); err != nil {
		t.Fatal(err)
	}
	w, ok := Load(dir, "work")
	if !ok || w.Title != "swap the call" || len(w.Steps) != 3 || w.Branch != "work" {
		t.Fatalf("loaded %+v ok=%v", w, ok)
	}
	if w.Steps[1].Code[0].File != "pkg/a.go" || w.Risks[1].Level != "high" {
		t.Fatalf("files are relative to the root and levels lower case: %+v %+v", w.Steps[1].Code, w.Risks[1])
	}
	if _, ok := Load(dir, "other"); ok {
		t.Fatal("another branch's walkthrough must not be shown")
	}

	again := sample()
	again.Title = "second"
	if _, err := Add(dir, again); err != nil {
		t.Fatal(err)
	}
	if w, _ := Load(dir, "work"); w.Title != "second" {
		t.Fatalf("the last one written is in force, got %q", w.Title)
	}
}

func TestAddRefusesWhatWouldNotShow(t *testing.T) {
	dir := gitRepo(t)
	for name, edit := range map[string]func(*Walkthrough){
		"no title":         func(w *Walkthrough) { w.Title = "" },
		"no steps":         func(w *Walkthrough) { w.Steps = nil },
		"bad id":           func(w *Walkthrough) { w.Steps[0].ID = "Has Space" },
		"same id":          func(w *Walkthrough) { w.Steps[1].ID = "new" },
		"from later":       func(w *Walkthrough) { w.Steps[0].From = []string{"swap"} },
		"no such file":     func(w *Walkthrough) { w.Steps[0].Code[0].File = "pkg/z.go" },
		"anchor nowhere":   func(w *Walkthrough) { w.Steps[1].Code[0].Anchor = "nothing like this" },
		"anchor twice":     func(w *Walkthrough) { w.Steps[2].Code[0].Anchor = "()" },
		"bad level":        func(w *Walkthrough) { w.Risks[0].Level = "huge" },
		"risk anchor only": func(w *Walkthrough) { w.Risks[0].Anchor = "A()" },
	} {
		w := sample()
		edit(&w)
		if _, err := Add(dir, w); err == nil {
			t.Errorf("%s: expected a refusal", name)
		}
	}
	if _, ok := Load(dir, "work"); ok {
		t.Fatal("a refused walkthrough must not be written")
	}
}

// TestRenderShowsTheChange covers the page drawing the code from git: the
// hunk an anchor is in, marked, an untracked file whole, code the change
// did not touch as context, and a removed line a risk points at.
func TestRenderShowsTheChange(t *testing.T) {
	dir := gitRepo(t)
	w, err := Add(dir, sample())
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render(dir, w)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	i := strings.Index(s, `<script id="data" type="application/json">`)
	j := strings.Index(s[i:], "</script>")
	var p page
	if err := json.Unmarshal([]byte(s[i+len(`<script id="data" type="application/json">`):i+j]), &p); err != nil {
		t.Fatal(err)
	}
	if p.Stats.Files != 2 || p.Base != "main" {
		t.Fatalf("stats %+v base %q", p.Stats, p.Base)
	}
	if got := p.Steps[1].From; len(got) != 1 || got[0] != "new" {
		t.Fatalf("a main step follows the one before, got %v", got)
	}
	if lines := p.Steps[0].Code[0].Hunks[0].Lines; len(lines) != 3 || lines[0].Kind != "+" {
		t.Fatalf("the new file, every line added: %+v", lines)
	}
	marked := 0
	for _, l := range p.Steps[1].Code[0].Hunks[0].Lines {
		if l.Mark {
			marked++
			if l.Kind != "+" || l.Text != "\tnewCall()" {
				t.Fatalf("marked %+v", l)
			}
		}
	}
	if marked != 1 {
		t.Fatalf("%d lines marked", marked)
	}
	if c := p.Steps[2].Code[0]; !c.Context || len(c.Hunks) != 1 {
		t.Fatalf("untouched code is shown as context: %+v", c)
	}
	if p.Risks[0].Level != "high" || p.Risks[0].Code == nil || len(p.Risks[0].Code.Hunks) != 1 {
		t.Fatalf("risks, the most serious first, with the hunk of the removed line: %+v", p.Risks)
	}
	if len(p.Uncovered) != 0 {
		t.Fatalf("every changed file is in a step: %+v", p.Uncovered)
	}
	if !strings.Contains(s, "<title>swap the call · walkthrough</title>") {
		t.Fatal("the title is in the page")
	}
}

func TestPruneKeepsTheLastOfEachBranch(t *testing.T) {
	dir := gitRepo(t)
	for _, title := range []string{"one", "two"} {
		w := sample()
		w.Title = title
		if _, err := Add(dir, w); err != nil {
			t.Fatal(err)
		}
	}
	gone := sample()
	gone.Branch = "deleted"
	if _, err := Add(dir, gone); err != nil {
		t.Fatal(err)
	}
	n, err := Prune(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("dropped %d, want the first of work and the deleted branch's", n)
	}
	if w, ok := Load(dir, "work"); !ok || w.Title != "two" {
		t.Fatalf("kept %+v", w)
	}
}

// TestTrimKeepsTheCodeAroundTheAnchor covers a new file, which is one
// hunk: a step about a line in it shows that line and what is near it.
func TestTrimKeepsTheCodeAroundTheAnchor(t *testing.T) {
	var h hunk
	for i := 1; i <= 100; i++ {
		h.Lines = append(h.Lines, line{Kind: "+", New: i, Mark: i == 50})
	}
	got := trimToMarks(h).Lines
	if len(got) != 2*aroundMark+1+2 || got[0].Kind != "~" || got[len(got)-1].Kind != "~" {
		t.Fatalf("%d lines, first %+v last %+v", len(got), got[0], got[len(got)-1])
	}
	if got[0].Text != "37 lines" || got[1].New != 50-aroundMark {
		t.Fatalf("left out %q, starting at %d", got[0].Text, got[1].New)
	}
}

// A title with the page's placeholders in it is the title, written as it
// is: the data still goes where it belongs.
func TestRenderTakesTheTitleAsText(t *testing.T) {
	dir := gitRepo(t)
	w, err := Add(dir, sample())
	if err != nil {
		t.Fatal(err)
	}
	w.Title = "hello {{DATA}} {{TITLE}}"
	out, err := Render(dir, w)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "<title>hello {{DATA}} {{TITLE}} · walkthrough</title>") {
		t.Error("the title is not as written")
	}
	if strings.Contains(s, `<script id="data" type="application/json">{{DATA}}`) {
		t.Error("the data did not go in the page")
	}
}

// The page's policy lets its own script run, by hash, and nothing else: a
// script an agent got in would not run, nor could anything be fetched.
func TestRenderPolicyAllowsOnlyThePageScript(t *testing.T) {
	dir := gitRepo(t)
	w, err := Add(dir, sample())
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render(dir, w)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	// The hash is of the one script that runs, from just after its tag to
	// just before its end, as the browser takes it; another script would
	// go unhashed and not run.
	if n := strings.Count(s, "<script"); n != 2 || strings.Count(s, `<script id="data" type="application/json">`) != 1 {
		t.Fatalf("expected the data and one script, found %d script tags", n)
	}
	m := regexp.MustCompile(`(?s)<script>(.*)</script>`).FindStringSubmatch(s)
	if m == nil {
		t.Fatal("no script in the page")
	}
	if !strings.HasPrefix(m[1], "\n(function () {\n") || !strings.HasSuffix(m[1], "\n})();\n") {
		t.Fatalf("the script is not taken whole: starts %q, ends %q", m[1][:20], m[1][len(m[1])-20:])
	}
	sum := sha256.Sum256([]byte(m[1]))
	want := "script-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	if !strings.Contains(s, want) || !strings.Contains(s, "default-src 'none'") {
		t.Errorf("the policy does not hold the script's hash: want %s", want)
	}
	if strings.Contains(s, "{{SCRIPT_HASH}}") {
		t.Error("the hash was not put in")
	}
}
