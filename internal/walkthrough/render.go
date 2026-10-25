package walkthrough

import (
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"nerdr/internal/git"
	"nerdr/internal/order"
	"nerdr/internal/store"
	"nerdr/internal/why"
)

//go:embed page.html
var pageHTML string

// pageTemplate is the page with its policy filled in: the one script it
// runs, by hash, so a script anywhere else on the page would not run.
var pageTemplate = withScriptHash(pageHTML)

// scriptOpen starts the page's script; it ends at the last </script>.
const scriptOpen = "<script>\n"

func withScriptHash(page string) string {
	start := strings.Index(page, scriptOpen)
	end := strings.LastIndex(page, "</script>")
	if start < 0 || end < start {
		panic("walkthrough: page.html has no script to hash")
	}
	sum := sha256.Sum256([]byte(page[start+len(scriptOpen)-1 : end]))
	return strings.Replace(page, "{{SCRIPT_HASH}}", "sha256-"+base64.StdEncoding.EncodeToString(sum[:]), 1)
}

// hunk is one hunk of a file's diff, numbered on both sides.
type hunk struct {
	Header string `json:"header"`
	Lines  []line `json:"lines"`
}

type line struct {
	// Kind is "+", "-" or " ", or "~" for lines left out, Text saying how
	// many.
	Kind string `json:"k"`
	Old  int    `json:"o,omitempty"`
	New  int    `json:"n,omitempty"`
	Text string `json:"t"`
	// Why is the note a why.Note anchored to this line says, if any.
	Why string `json:"why,omitempty"`
	// Mark is the line a reference's anchor is on.
	Mark bool `json:"mark,omitempty"`
}

var hunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// parseHunks reads a unified diff of one file into its hunks.
func parseHunks(diff string) []hunk {
	var out []hunk
	var cur *hunk
	oldN, newN := 0, 0
	for _, l := range strings.Split(diff, "\n") {
		if m := hunkRe.FindStringSubmatch(l); m != nil {
			oldN, _ = strconv.Atoi(m[1])
			newN, _ = strconv.Atoi(m[2])
			out = append(out, hunk{Header: l})
			cur = &out[len(out)-1]
			continue
		}
		if cur == nil {
			continue
		}
		switch {
		case strings.HasPrefix(l, "diff "):
			cur = nil
		case strings.HasPrefix(l, "\\"):
		case strings.HasPrefix(l, "+"):
			cur.Lines = append(cur.Lines, line{Kind: "+", New: newN, Text: l[1:]})
			newN++
		case strings.HasPrefix(l, "-"):
			cur.Lines = append(cur.Lines, line{Kind: "-", Old: oldN, Text: l[1:]})
			oldN++
		case strings.HasPrefix(l, " "):
			cur.Lines = append(cur.Lines, line{Kind: " ", Old: oldN, New: newN, Text: l[1:]})
			oldN++
			newN++
		}
	}
	return out
}

// snippet is code shown for a reference: the hunks it points at, or the
// code around its anchor when the change did not touch it.
type snippet struct {
	File string `json:"file"`
	Note string `json:"note,omitempty"`
	// Context is set when the code shown is not part of the change.
	Context bool   `json:"context,omitempty"`
	Hunks   []hunk `json:"hunks"`
	// More counts the hunks of the file left out.
	More int `json:"more,omitempty"`
	// Missing says why nothing could be shown.
	Missing string `json:"missing,omitempty"`
}

// maxHunks is the most hunks a reference to a whole file shows: past it,
// the file is read in F1.
const maxHunks = 8

// contextLines is how much code is shown around an anchor in code the
// change did not touch.
const contextLines = 6

// aroundMark is how much of a hunk is kept on either side of an anchor's
// line: a new file is one hunk, and the step is about the line, not all
// four hundred.
const aroundMark = 12

// maxHunkLines is the most lines of a hunk shown when no anchor says
// which part matters.
const maxHunkLines = 80

// gap stands for n lines left out.
func gap(n int) line { return line{Kind: "~", Text: fmt.Sprintf("%d lines", n)} }

// trimToMarks keeps the lines of h within aroundMark of a marked line, and
// says how many it left out between them.
func trimToMarks(h hunk) hunk {
	keep := make([]bool, len(h.Lines))
	for i, l := range h.Lines {
		if l.Mark {
			for j := max(i-aroundMark, 0); j <= min(i+aroundMark, len(h.Lines)-1); j++ {
				keep[j] = true
			}
		}
	}
	var out []line
	left := 0
	for i, l := range h.Lines {
		if !keep[i] {
			left++
			continue
		}
		if left > 0 {
			out = append(out, gap(left))
			left = 0
		}
		out = append(out, l)
	}
	if left > 0 {
		out = append(out, gap(left))
	}
	h.Lines = out
	return h
}

// trimLong cuts a hunk shown whole to its first maxHunkLines.
func trimLong(h hunk) hunk {
	if len(h.Lines) > maxHunkLines+10 {
		left := len(h.Lines) - maxHunkLines
		h.Lines = append(append([]line(nil), h.Lines[:maxHunkLines]...), gap(left))
	}
	return h
}

// page is what the page draws, as JSON.
type page struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Branch  string `json:"branch"`
	Base    string `json:"base"`
	Agent   string `json:"agent,omitempty"`
	Time    string `json:"time,omitempty"`
	Stats   struct {
		Files int `json:"files"`
		Add   int `json:"add"`
		Del   int `json:"del"`
	} `json:"stats"`
	Steps []pageStep `json:"steps"`
	Risks []pageRisk `json:"risks"`
	// Uncovered are changed files no step or risk points at.
	Uncovered []fileLine `json:"uncovered"`
	// Order is the one line the reading order has for each changed file.
	Order map[string]fileLine `json:"order"`
}

type pageStep struct {
	ID    string    `json:"id"`
	Title string    `json:"title"`
	Text  string    `json:"text"`
	From  []string  `json:"from"`
	Side  bool      `json:"side"`
	Code  []snippet `json:"code"`
}

type pageRisk struct {
	Level string   `json:"level"`
	Text  string   `json:"text"`
	Code  *snippet `json:"code,omitempty"`
}

type fileLine struct {
	File  string `json:"file"`
	Role  string `json:"role,omitempty"`
	Short string `json:"short,omitempty"`
	Add   int    `json:"add"`
	Del   int    `json:"del"`
}

// PagePath is where the page for branch of the worktree at path is
// written, or "" when path is not inside a git repository.
func PagePath(worktreePath, branch string) string {
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, branch)
	if name == "" {
		name = "detached"
	}
	return store.Path(worktreePath, filepath.Join("walkthrough", name+".html"))
}

// Write draws the page for w, the walkthrough of the change in the
// worktree at root, from the change as it is now, and returns where it
// was written.
func Write(root string, w Walkthrough) (string, error) {
	path := PagePath(root, w.Branch)
	if path == "" {
		return "", fmt.Errorf("no data folder for %s", root)
	}
	b, err := Render(root, w)
	if err != nil {
		return "", err
	}
	// In the data folder, where agents write: never through a link.
	return path, store.WriteFile(path, b, 0o644)
}

// Render draws the page for w: a page of its own, which needs nothing
// from the network, so it opens as a file.
func Render(root string, w Walkthrough) ([]byte, error) {
	base := git.DetectBase(root)
	cmp := git.Comparison{Worktree: root, Base: base}
	files, err := cmp.Files()
	if err != nil {
		return nil, err
	}
	untracked := map[string]bool{}
	for _, f := range files {
		if f.Code == "??" {
			untracked[f.Path] = true
		}
	}
	stats := cmp.NumStat()
	notes := why.Load(root)
	ord := order.Load(root, w.Branch)

	p := page{Title: w.Title, Summary: w.Summary, Branch: w.Branch, Base: base,
		Agent: w.Agent, Time: w.Time, Order: map[string]fileLine{}}
	if base == "" {
		p.Base = "HEAD"
	}
	for _, f := range files {
		c := stats[f.Path]
		p.Stats.Files++
		p.Stats.Add += c.Add
		p.Stats.Del += c.Del
		fl := fileLine{File: f.Path, Add: c.Add, Del: c.Del}
		if e, ok := ord.For(f.Path); ok {
			fl.Role, fl.Short = order.Label(e.Role), e.Short
		}
		p.Order[f.Path] = fl
	}

	diffs := map[string][]hunk{}
	hunksOf := func(file string) []hunk {
		if h, ok := diffs[file]; ok {
			return h
		}
		d, _ := cmp.Diff(file, untracked[file])
		h := parseHunks(d)
		for i := range h {
			for j := range h[i].Lines {
				l := &h[i].Lines[j]
				if l.Kind != "-" {
					if n, ok := notes.Match(file, l.Text); ok {
						l.Why = n.Short
					}
				}
			}
		}
		diffs[file] = h
		return h
	}
	covered := map[string]bool{}
	show := func(r Ref) snippet {
		covered[r.File] = true
		return codeFor(root, r, hunksOf(r.File))
	}

	prevMain := ""
	for _, s := range w.Steps {
		ps := pageStep{ID: s.ID, Title: s.Title, Text: s.Text, From: s.From, Side: s.Side}
		if len(ps.From) == 0 && prevMain != "" {
			ps.From = []string{prevMain}
		}
		if ps.From == nil {
			ps.From = []string{}
		}
		if !s.Side {
			prevMain = s.ID
		}
		for _, r := range s.Code {
			if r.File != "" {
				ps.Code = append(ps.Code, show(r))
			}
		}
		if ps.Code == nil {
			ps.Code = []snippet{}
		}
		p.Steps = append(p.Steps, ps)
	}
	risks := append([]Risk(nil), w.Risks...)
	sort.SliceStable(risks, func(i, j int) bool { return levelRank(risks[i].Level) < levelRank(risks[j].Level) })
	p.Risks = []pageRisk{}
	for _, r := range risks {
		pr := pageRisk{Level: r.Level, Text: r.Text}
		if r.File != "" {
			sn := show(Ref{File: r.File, Anchor: r.Anchor})
			pr.Code = &sn
		}
		p.Risks = append(p.Risks, pr)
	}
	p.Uncovered = []fileLine{}
	for _, f := range files {
		if !covered[f.Path] {
			p.Uncovered = append(p.Uncovered, p.Order[f.Path])
		}
	}

	data, err := json.Marshal(p) // escapes <, > and &: safe inside <script>
	if err != nil {
		return nil, err
	}
	// In one pass over the page: a title an agent wrote with {{DATA}} in
	// it is not where the data goes.
	out := strings.NewReplacer("{{TITLE}}", html.EscapeString(w.Title), "{{DATA}}", string(data)).Replace(pageTemplate)
	return []byte(out), nil
}

func levelRank(l string) int {
	for i, v := range Levels {
		if v == l {
			return i
		}
	}
	return len(Levels)
}

// codeFor is the code shown for r: the hunks holding its anchor, every
// hunk of the file when it has none, and the code around the anchor when
// it is on a line the change did not touch.
func codeFor(root string, r Ref, hunks []hunk) snippet {
	sn := snippet{File: r.File, Note: r.Note}
	if r.Anchor == "" {
		if len(hunks) == 0 {
			sn.Missing = "no change in this file now"
		}
		if len(hunks) > maxHunks {
			hunks, sn.More = hunks[:maxHunks], len(hunks)-maxHunks
		}
		for _, h := range hunks {
			sn.Hunks = append(sn.Hunks, trimLong(h))
		}
		return sn
	}
	for _, h := range hunks {
		hit := false
		h.Lines = append([]line(nil), h.Lines...)
		for i := range h.Lines {
			if strings.Contains(h.Lines[i].Text, r.Anchor) {
				h.Lines[i].Mark, hit = true, true
			}
		}
		if hit {
			sn.Hunks = append(sn.Hunks, trimToMarks(h))
		}
	}
	if len(sn.Hunks) > 0 {
		return sn
	}
	// Not through a link out of the worktree: what is shown goes in the
	// page, in the data folder, where agents can read it.
	src, err := git.ReadWorktreeFile(root, r.File)
	if err != nil {
		sn.Missing = "the file is gone, and the change no longer removes the line"
		return sn
	}
	all := strings.Split(strings.TrimSuffix(string(src), "\n"), "\n")
	at := matching(string(src), r.Anchor)
	if len(at) == 0 {
		sn.Missing = fmt.Sprintf("%q is no longer in the file — the code has changed since the walkthrough was written", r.Anchor)
		return sn
	}
	from, to := max(at[0]-contextLines, 1), min(at[0]+contextLines, len(all))
	h := hunk{Header: fmt.Sprintf("@@ %d-%d, not changed @@", from, to)}
	for n := from; n <= to; n++ {
		h.Lines = append(h.Lines, line{Kind: " ", Old: n, New: n, Text: all[n-1], Mark: n == at[0]})
	}
	sn.Context, sn.Hunks = true, []hunk{h}
	return sn
}
