package agents

import (
	"os"
	"path/filepath"
	"testing"
)

func ids(ms []AgentModel) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}

func sameIDs(t *testing.T, what string, got []AgentModel, want ...string) {
	t.Helper()
	g := ids(got)
	if len(g) != len(want) {
		t.Fatalf("%s: got %q, want %q", what, g, want)
	}
	for i := range g {
		if g[i] != want[i] {
			t.Fatalf("%s: got %q, want %q", what, g, want)
		}
	}
}

// TestCodexModels covers codex's cache: the models its picker lists, in
// its order, those it hides left out.
func TestCodexModels(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	cache := `{"fetched_at":"x","models":[
		{"slug":"gpt-b","display_name":"GPT-B","visibility":"list","priority":2},
		{"slug":"hidden","display_name":"Hidden","visibility":"hide","priority":3},
		{"slug":"gpt-a","display_name":"GPT-A","visibility":"list","priority":1}]}`
	if err := os.WriteFile(filepath.Join(home, "models_cache.json"), []byte(cache), 0o600); err != nil {
		t.Fatal(err)
	}
	ms, err := Models(nil, Codex)
	if err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "codex", ms, "gpt-a", "gpt-b")
	if ms[0].Name != "GPT-A" {
		t.Fatalf("name %q", ms[0].Name)
	}
}

func TestListingsAreRead(t *testing.T) {
	sameIDs(t, "cursor", cursorModels("Available models\n\nauto - Auto (current, default)\ngpt-5.3-codex - Codex 5.3\n\nTip: use --model\n"),
		"auto", "gpt-5.3-codex")
	sameIDs(t, "agy", tabbedModels("Fetching available models...\ngemini-3.8-flash-high\tGemini 3.8 Flash (High)\nclaude-sonnet-4-6\tClaude Sonnet 4.6\n"),
		"gemini-3.8-flash-high", "claude-sonnet-4-6")
	sameIDs(t, "opencode", tabbedModels("deepseek/deepseek-flash\nopenai/gpt-4.1\n"),
		"deepseek/deepseek-flash", "openai/gpt-4.1")
	sameIDs(t, "copilot", copilotModels("  `logLevel`: log level.\n\n  `model`: AI model to use; can be changed with /model.\n"+
		"    - \"claude-sonnet-5.5\"\n    - \"gpt-5.4\"\n\n  `mouse`: whether to use the mouse.\n    - \"on\"\n"),
		"claude-sonnet-5.5", "gpt-5.4")
	sameIDs(t, "copilot, unquoted", copilotModels("  `model`: AI model to use.\n    - claude-sonnet-5.5\n    - gpt-5.4\n\n  `mouse`: x\n"),
		"claude-sonnet-5.5", "gpt-5.4")
	sameIDs(t, "copilot --help", copilotChoices("  --log-level <level>  (choices: \"none\", \"all\")\n"+
		"  --model <model>      Set the AI model to use (choices:\n"+
		"                       \"claude-sonnet-4.5\", \"gpt-5\")\n"+
		"  --no-color           x \"y\"\n"),
		"claude-sonnet-4.5", "gpt-5")
	sameIDs(t, "claude", claudeAliases("  --fallback-model <model>   Enable fallback to 'x y'\n"+
		"  --model <model>                       Model for the current session. Provide\n"+
		"                                        an alias for the latest model (e.g.\n"+
		"                                        'fable', 'opus', or 'sonnet') or a\n"+
		"                                        model's full name.\n"+
		"  --name <name>   Name the 'session'\n"),
		"fable", "opus", "sonnet")
}

// TestOwnModel covers the model an agent starts with when nerdr names
// none: on its command line in the config, opencode's config_model, or
// in the agent's own settings, JSONC as opencode has them.
func TestOwnModel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	write := func(path, text string) {
		t.Helper()
		path = filepath.Join(home, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := OwnModel(nil, Gemini); got != "" {
		t.Fatalf("gemini with no settings: %q", got)
	}
	write(".codex/config.toml", "model = \"gpt-a\"\nmodel_reasoning_effort = \"medium\"\n")
	write(".gemini/settings.json", `{"model": {"name": "gemini-x"}}`)
	write(".claude/settings.json", `{"model": "opus"}`)
	write(".config/opencode/opencode.jsonc", "{\n  // the default\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"model\": \"anthropic/claude-y\",\n}\n")
	for k, want := range map[Kind]string{Codex: "gpt-a", Gemini: "gemini-x", Claude: "opus", OpenCode: "anthropic/claude-y", Cursor: ""} {
		if got := OwnModel(nil, k); got != want {
			t.Errorf("%s: %q, want %q", k, got, want)
		}
	}
	specs := map[Kind]Spec{
		Codex:    {Args: []string{"--yolo", "-m", "gpt-b"}},
		Claude:   {Args: []string{"--model=sonnet"}},
		OpenCode: {ConfigModel: "openai/z"},
	}
	for k, want := range map[Kind]string{Codex: "gpt-b", Claude: "sonnet", OpenCode: "openai/z"} {
		if got := OwnModel(specs, k); got != want {
			t.Errorf("%s from the config: %q, want %q", k, got, want)
		}
	}
}
