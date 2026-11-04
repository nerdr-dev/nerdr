package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeContext(t *testing.T, file, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Only what is there is listed, grouped, and of an MCP server only its name.
func TestContextClaude(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	dir := filepath.Join(t.TempDir(), "w")
	writeContext(t, filepath.Join(home, ".claude", "CLAUDE.md"), "mine")
	writeContext(t, filepath.Join(dir, "CLAUDE.md"), "project")
	writeContext(t, filepath.Join(dir, ".claude", "rules", "go", "style.md"), "rule")
	writeContext(t, filepath.Join(dir, ".claude", "rules", "skip.txt"), "no")
	writeContext(t, filepath.Join(claudeProject(dir), "memory", "MEMORY.md"), "memo")
	writeContext(t, filepath.Join(dir, ".mcp.json"), `{"mcpServers":{"shared":{"env":{"TOKEN":"secret"}}}}`)
	writeContext(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"mine":{}},"projects":{"`+dir+`":{"mcpServers":{"local":{}}}}}`)

	var got []string
	for _, it := range Context(Claude, dir, "") {
		name := it.Name
		if name == "" {
			name = filepath.Base(it.Path)
		}
		got = append(got, it.Group+"/"+name)
	}
	want := "Instructions/CLAUDE.md Instructions/CLAUDE.md Rules/style.md Memory/MEMORY.md MCP servers/shared MCP servers/mine MCP servers/local"
	if s := strings.Join(got, " "); s != want {
		t.Fatalf("got  %s\nwant %s", s, want)
	}
}

// codex looks no higher than the worktree; a server is found in its TOML.
func TestContextCodex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	outer := t.TempDir()
	dir := filepath.Join(outer, "repo", "sub")
	writeContext(t, filepath.Join(outer, "AGENTS.md"), "above the repository")
	writeContext(t, filepath.Join(outer, "repo", ".git"), "gitdir: x")
	writeContext(t, filepath.Join(outer, "repo", "AGENTS.md"), "root")
	writeContext(t, filepath.Join(dir, "AGENTS.md"), "sub")
	writeContext(t, filepath.Join(home, ".codex", "config.toml"), "[mcp_servers.docs]\ncommand = \"x\"\n")

	items := Context(Codex, dir, "")
	if len(items) != 3 || items[0].Path != filepath.Join(outer, "repo", "AGENTS.md") || items[1].Path != filepath.Join(dir, "AGENTS.md") || items[2].Name != "docs" {
		t.Fatalf("%+v", items)
	}
}
