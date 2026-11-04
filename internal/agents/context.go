package agents

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// What an agent reads when it starts, beside its skills, as its own
// documentation says, for F4: its instruction files, rules, memory and the
// MCP servers it is set up with. This is where it would look, narrowed to
// what is there; nerdr cannot see what a running agent really loaded.

// Groups of ContextItem, in the order they are shown.
const (
	ContextInstructions = "Instructions"
	ContextRules        = "Rules"
	ContextMemory       = "Memory"
	ContextMCP          = "MCP servers"
)

// maxContextRules is how many rule files are listed from one folder.
const maxContextRules = 200

// ContextItem is something an agent reads at start: a file, or an MCP
// server, named in the config file Path, whose contents are not for showing
// as such files can hold credentials.
type ContextItem struct {
	Group string
	Name  string // the server's; "" for a file
	Path  string
	Size  int64
}

// ContextDocumented is whether agent k's documentation says what it reads.
// For the others, Context lists the usual places.
func ContextDocumented(k Kind) bool {
	return k != Cursor && k != Antigravity
}

// Context is what an agent of kind k started in dir reads, by group. repo
// is the folder of the repository's main checkout, which claude keeps its
// memory by; "" when it is not known.
func Context(k Kind, dir, repo string) []ContextItem {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	var c contextList
	switch k {
	case Claude:
		managed := "/etc/claude-code/CLAUDE.md"
		if runtime.GOOS == "darwin" {
			managed = "/Library/Application Support/ClaudeCode/CLAUDE.md"
		}
		claude := claudeHome()
		c.files(ContextInstructions, managed, filepath.Join(claude, "CLAUDE.md"))
		c.upward(dir, "", "CLAUDE.md", "CLAUDE.local.md")
		c.files(ContextInstructions, filepath.Join(dir, ".claude", "CLAUDE.md"), filepath.Join(dir, "AGENTS.md"))
		c.rules(filepath.Join(claude, "rules"))
		c.rules(filepath.Join(dir, ".claude", "rules"))
		if repo == "" {
			repo = dir
		}
		if project := claudeProject(repo); project != "" {
			c.files(ContextMemory, filepath.Join(project, "memory", "MEMORY.md"))
		}
		c.jsonServers(filepath.Join(dir, ".mcp.json"), "mcpServers")
		c.claudeServers(filepath.Join(home, ".claude.json"), dir)
	case Codex:
		c.files(ContextInstructions, filepath.Join(home, ".codex", "AGENTS.md"))
		c.upward(dir, worktreeRoot(dir), "AGENTS.override.md", "AGENTS.md")
		c.tomlServers(filepath.Join(home, ".codex", "config.toml"))
		c.tomlServers(filepath.Join(dir, ".codex", "config.toml"))
	case Gemini:
		c.files(ContextInstructions, filepath.Join(home, ".gemini", "GEMINI.md"))
		c.upward(dir, "", "GEMINI.md")
		c.jsonServers(filepath.Join(home, ".gemini", "settings.json"), "mcpServers")
		c.jsonServers(filepath.Join(dir, ".gemini", "settings.json"), "mcpServers")
	case Copilot:
		copilot := filepath.Join(home, ".copilot")
		c.files(ContextInstructions, filepath.Join(copilot, "copilot-instructions.md"),
			filepath.Join(dir, ".github", "copilot-instructions.md"), filepath.Join(dir, "AGENTS.md"),
			filepath.Join(dir, "CLAUDE.md"), filepath.Join(dir, ".claude", "CLAUDE.md"), filepath.Join(dir, "GEMINI.md"))
		c.rules(filepath.Join(copilot, "instructions"))
		c.rules(filepath.Join(dir, ".github", "instructions"))
		c.jsonServers(filepath.Join(copilot, "mcp-config.json"), "mcpServers")
	case OpenCode:
		// The first of each pair it finds, not both.
		c.firstOf(filepath.Join(home, ".config", "opencode", "AGENTS.md"), filepath.Join(home, ".claude", "CLAUDE.md"))
		for _, d := range ancestors(dir, "") {
			if c.firstOf(filepath.Join(d, "AGENTS.md"), filepath.Join(d, "CLAUDE.md")) {
				break
			}
		}
		c.jsonServers(filepath.Join(home, ".config", "opencode", "opencode.json"), "mcp")
		c.jsonServers(filepath.Join(dir, "opencode.json"), "mcp")
	case Cursor:
		c.files(ContextInstructions, filepath.Join(dir, "AGENTS.md"), filepath.Join(dir, "CLAUDE.md"))
		c.rules(filepath.Join(dir, ".cursor", "rules"))
	case Antigravity:
		c.files(ContextInstructions, filepath.Join(home, ".gemini", "GEMINI.md"), filepath.Join(dir, "GEMINI.md"), filepath.Join(dir, "AGENTS.md"))
	}
	groups := map[string]int{ContextInstructions: 0, ContextRules: 1, ContextMemory: 2, ContextMCP: 3}
	sort.SliceStable(c.items, func(i, j int) bool { return groups[c.items[i].Group] < groups[c.items[j].Group] })
	return c.items
}

// contextList gathers the items, none twice.
type contextList struct {
	items []ContextItem
	seen  map[string]bool
}

func (c *contextList) add(it ContextItem) {
	key := it.Group + "\x00" + it.Name + "\x00" + it.Path
	if c.seen == nil {
		c.seen = map[string]bool{}
	}
	if !c.seen[key] {
		c.seen[key] = true
		c.items = append(c.items, it)
	}
}

// file adds the regular file path, and reports whether it is there.
func (c *contextList) file(group, path string) bool {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	c.add(ContextItem{Group: group, Path: path, Size: st.Size()})
	return true
}

func (c *contextList) files(group string, paths ...string) {
	for _, p := range paths {
		c.file(group, p)
	}
}

// firstOf adds the first of paths that is there, and reports whether one was.
func (c *contextList) firstOf(paths ...string) bool {
	for _, p := range paths {
		if c.file(ContextInstructions, p) {
			return true
		}
	}
	return false
}

// upward adds names from each folder from the top down to dir, the top
// being stop, or the root of the disk with stop "".
func (c *contextList) upward(dir, stop string, names ...string) {
	folders := ancestors(dir, stop)
	for i := len(folders) - 1; i >= 0; i-- {
		for _, n := range names {
			c.file(ContextInstructions, filepath.Join(folders[i], n))
		}
	}
}

// rules adds the markdown files under root, not following links.
func (c *contextList) rules(root string) {
	n := 0
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if n >= maxContextRules {
			return filepath.SkipAll
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".md") && c.file(ContextRules, path) {
			n++
		}
		return nil
	})
}

// jsonServers adds the servers under key in the JSON file.
func (c *contextList) jsonServers(file, key string) {
	var v map[string]json.RawMessage
	if readJSON(file, &v) {
		var names map[string]json.RawMessage
		if json.Unmarshal(v[key], &names) == nil {
			c.sortedServers(file, names)
		}
	}
}

// claudeServers adds the servers in claude's own file: its user-wide ones,
// and the ones of the project dir.
func (c *contextList) claudeServers(file, dir string) {
	var v struct {
		Servers  map[string]json.RawMessage `json:"mcpServers"`
		Projects map[string]struct {
			Servers map[string]json.RawMessage `json:"mcpServers"`
		} `json:"projects"`
	}
	if readJSON(file, &v) {
		c.sortedServers(file, v.Servers)
		c.sortedServers(file, v.Projects[dir].Servers)
	}
}

// tomlServers adds the servers in codex's config file.
func (c *contextList) tomlServers(file string) {
	var v struct {
		Servers map[string]toml.Primitive `toml:"mcp_servers"`
	}
	if _, err := toml.DecodeFile(file, &v); err != nil {
		return
	}
	names := map[string]json.RawMessage{}
	for name := range v.Servers {
		names[name] = nil
	}
	c.sortedServers(file, names)
}

// sortedServers adds names in a steady order: a map has none.
func (c *contextList) sortedServers(file string, names map[string]json.RawMessage) {
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	for _, n := range sorted {
		c.add(ContextItem{Group: ContextMCP, Name: n, Path: file})
	}
}

func readJSON(file string, v any) bool {
	data, err := os.ReadFile(file)
	return err == nil && json.Unmarshal(data, v) == nil
}

// ancestors is dir and the folders above it, up to and with stop, or to the
// root of the disk with stop "".
func ancestors(dir, stop string) []string {
	var out []string
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		out = append(out, d)
		if d == stop || filepath.Dir(d) == d {
			return out
		}
	}
}

// worktreeRoot is the folder at or above dir with a .git in it, or dir.
func worktreeRoot(dir string) string {
	for _, d := range ancestors(dir, "") {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
	}
	return filepath.Clean(dir)
}
