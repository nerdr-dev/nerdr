package agents

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Searching the agents' conversations: the logs claude and codex keep of
// every conversation, those had in the folders nerdr works in. What the
// user and the agent said is read once and kept, and read again only from
// a log that has changed; the tools' output, most of a log, is left out.

// Conversation is one agent conversation, as its log has it.
type Conversation struct {
	Kind     Kind
	ID       string
	Dir      string // the folder it was had in
	Path     string // its log
	Modified time.Time
	Said     []Said

	size  int64
	lower []string // Said's texts in lower case, to search
}

// Said is one thing said in a conversation: Who is "user" or the agent's
// kind.
type Said struct {
	Who, Text string
}

// Index is the conversations read, by their logs' paths.
type Index struct {
	mu    sync.Mutex
	convs map[string]*Conversation
	// heads is, by log path, the folder each log seen was had in, as far
	// as its first lines say, for the logs not read in full: those had
	// elsewhere, which are most of codex's, one folder for every project.
	// A log unchanged is not opened again to tell.
	heads map[string]logHead
}

// logHead is where a log was had, read off its first lines when it was
// the size and time it is.
type logHead struct {
	dir      string
	size     int64
	modified time.Time
}

// Len is how many conversations x has.
func (x *Index) Len() int {
	x.mu.Lock()
	defer x.mu.Unlock()
	return len(x.convs)
}

// Update reads the conversations had in roots, or in folders under them,
// again from the logs changed since last read, and forgets those whose
// logs are gone.
func (x *Index) Update(roots []string) {
	var clean []string
	for _, r := range roots {
		if r == "" {
			continue
		}
		if abs, err := filepath.Abs(r); err == nil {
			r = abs
		}
		clean = append(clean, filepath.Clean(r))
	}
	under := func(dir string) bool {
		if dir == "" {
			return false
		}
		dir = filepath.Clean(dir)
		for _, r := range clean {
			if dir == r || strings.HasPrefix(dir, r+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}

	type log struct {
		kind Kind
		path string
	}
	var logs []log
	if home := claudeHome(); home != "" {
		// A log's folder is named after where it was had, spelt as claude
		// spells it: only those spelt as under a root can be.
		var prefixes []string
		for _, r := range clean {
			prefixes = append(prefixes, filepath.Base(claudeProject(r)))
		}
		dirs, _ := os.ReadDir(filepath.Join(home, "projects"))
		for _, d := range dirs {
			if !d.IsDir() || !hasAnyPrefix(d.Name(), prefixes) {
				continue
			}
			files, _ := filepath.Glob(filepath.Join(home, "projects", d.Name(), "*.jsonl"))
			for _, f := range files {
				logs = append(logs, log{Claude, f})
			}
		}
	}
	files, _ := filepath.Glob(filepath.Join(codexHome(), "sessions", "*", "*", "*", "rollout-*.jsonl"))
	for _, f := range files {
		logs = append(logs, log{Codex, f})
	}

	x.mu.Lock()
	old, oldHeads := x.convs, x.heads
	x.mu.Unlock()

	next, heads := map[string]*Conversation{}, map[string]logHead{}
	for _, l := range logs {
		st, err := os.Stat(l.path)
		if err != nil {
			continue
		}
		if c := old[l.path]; c != nil && c.size == st.Size() && c.Modified.Equal(st.ModTime()) {
			next[l.path] = c
			continue
		}
		// Where it was had first, from its first lines, or from what they
		// said last time: a log had elsewhere is not read in full.
		h, ok := oldHeads[l.path]
		if !ok || h.size != st.Size() || !h.modified.Equal(st.ModTime()) {
			h = logHead{dir: logDir(l.kind, l.path), size: st.Size(), modified: st.ModTime()}
		}
		heads[l.path] = h
		if !under(h.dir) {
			continue
		}
		c := readConversation(l.kind, l.path)
		if c == nil || !under(c.Dir) || len(c.Said) == 0 {
			continue
		}
		c.size, c.Modified = st.Size(), st.ModTime()
		next[l.path] = c
	}
	x.mu.Lock()
	x.convs, x.heads = next, heads
	x.mu.Unlock()
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if p != "" && strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// logDir is the folder the log at path of kind k was had in, from its
// first lines, or "".
func logDir(k Kind, path string) string {
	switch k {
	case Claude:
		return claudeCwd(path)
	case Codex:
		dir := ""
		_ = eachFirstLine(path, func(b []byte) {
			var e struct {
				Payload struct {
					Cwd string `json:"cwd"`
				} `json:"payload"`
			}
			if json.Unmarshal(b, &e) == nil {
				dir = e.Payload.Cwd
			}
		})
		return dir
	}
	return ""
}

// readConversation reads the log at path of kind k, or nil when it has
// no folder it was had in.
func readConversation(k Kind, path string) *Conversation {
	c := &Conversation{Kind: k, Path: path, Dir: logDir(k, path)}
	var turns []turn
	switch k {
	case Claude:
		c.ID = strings.TrimSuffix(filepath.Base(path), ".jsonl")
		turns, _ = claudeFileTurns(path)
	case Codex:
		m := codexRollout.FindStringSubmatch(path)
		if m == nil {
			return nil
		}
		c.ID = m[1]
		turns, _ = codexFileTurns(path)
	}
	if c.Dir == "" {
		return nil
	}
	for _, t := range turns {
		if t.who == "tool" || t.who == "command" {
			continue
		}
		text := clip(t.text, maxSaid)
		c.Said = append(c.Said, Said{t.who, text})
		c.lower = append(c.lower, strings.ToLower(text))
	}
	return c
}

// claudeCwd is the folder the claude log at path was had in, as the
// first entry that says it has it.
func claudeCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for range promptScanLines {
		line, err := r.ReadBytes('\n')
		var e struct {
			Cwd string `json:"cwd"`
		}
		if json.Unmarshal(line, &e) == nil && e.Cwd != "" {
			return e.Cwd
		}
		if err != nil {
			return ""
		}
	}
	return ""
}

// Hit is a conversation that has what was searched for: the number of
// things said in it that do, and the last of them.
type Hit struct {
	Conv  *Conversation
	Count int
	At    int // the index in Conv.Said of the last thing said that has it
}

// Snippet is the part of what was said at h that has the first of words
// in it, on one line, about w long.
func (h Hit) Snippet(words []string, w int) string {
	text := oneLine(h.Conv.Said[h.At].Text)
	lower := strings.ToLower(text)
	at := 0
	if len(words) > 0 {
		at = max(strings.Index(lower, words[0]), 0)
	}
	start := max(at-w/3, 0)
	for start > 0 && !utf8Start(text[start]) {
		start--
	}
	end := min(start+w, len(text))
	for end < len(text) && !utf8Start(text[end]) {
		end++
	}
	out := text[start:end]
	if start > 0 {
		out = "…" + out
	}
	if end < len(text) {
		out += "…"
	}
	return out
}

// Words are the words of a search, in lower case.
func Words(q string) []string { return strings.Fields(strings.ToLower(q)) }

// Search is the conversations with something said in them that has every
// one of words, the latest first, at most limit.
func (x *Index) Search(words []string, limit int) []Hit {
	if len(words) == 0 {
		return nil
	}
	x.mu.Lock()
	convs := make([]*Conversation, 0, len(x.convs))
	for _, c := range x.convs {
		convs = append(convs, c)
	}
	x.mu.Unlock()
	sort.Slice(convs, func(i, j int) bool { return convs[i].Modified.After(convs[j].Modified) })
	var hits []Hit
	// A conversation carried on can be logged again in full in a new log:
	// the same thing said in the same folder is found once, the newest.
	seen := map[[2]string]bool{}
	for _, c := range convs {
		h := Hit{Conv: c, At: -1}
	said:
		for i, s := range c.lower {
			for _, w := range words {
				if !strings.Contains(s, w) {
					continue said
				}
			}
			h.Count++
			h.At = i
		}
		if h.Count > 0 {
			key := [2]string{c.Dir, c.Said[h.At].Text}
			if seen[key] {
				continue
			}
			seen[key] = true
			hits = append(hits, h)
			if len(hits) >= limit {
				break
			}
		}
	}
	return hits
}

// Conversation is the conversation with id, or nil.
func (x *Index) Conversation(id string) *Conversation {
	x.mu.Lock()
	defer x.mu.Unlock()
	var best *Conversation
	for _, c := range x.convs {
		if c.ID == id && (best == nil || c.Modified.After(best.Modified)) {
			best = c
		}
	}
	return best
}
