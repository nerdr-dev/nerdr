package agents

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"nerdr/internal/proc"
)

// The models an agent can be started with, as the agent itself lists
// them — nerdr keeps no list of its own, which would be out of date with
// the next release of any of them. Each lists them its own way: codex in
// the cache its /model picker reads, cursor, antigravity and opencode
// with a command, copilot in its help on its config; claude names only
// its aliases, in its help. A kind with none of these has no list, and
// its model is typed in.

// AgentModel is a model an agent can be started with: ID is what it is
// given on the command line, Name what the agent calls it, if it says.
type AgentModel struct {
	ID, Name string
}

// modelsTimeout is how long an agent has to list its models.
const modelsTimeout = 15 * time.Second

// Models lists the models k can be started with, as the config runs it;
// none, with no error, for a kind that does not list them.
func Models(specs map[Kind]Spec, k Kind) ([]AgentModel, error) {
	if !TakesModel(k) {
		return nil, nil
	}
	switch k {
	case Codex:
		data, err := os.ReadFile(filepath.Join(codexHome(), "models_cache.json"))
		if err != nil {
			return nil, err
		}
		return codexModels(data)
	case Cursor:
		out, err := runListing(specs, k, "models")
		return cursorModels(out), err
	case Antigravity:
		out, err := runListing(specs, k, "models")
		return tabbedModels(out), err
	case OpenCode:
		out, err := runListing(specs, k, "models")
		return tabbedModels(out), err
	case Copilot:
		out, err := runListing(specs, k, "help", "config")
		if ms := copilotModels(out); len(ms) > 0 {
			return ms, nil
		}
		// A copilot that lists them in its help on --model instead.
		help, herr := runListing(specs, k, "--help")
		if ms := copilotChoices(help); len(ms) > 0 {
			return ms, nil
		}
		if err == nil {
			err = herr
		}
		return nil, err
	case Claude:
		out, err := runListing(specs, k, "--help")
		return claudeAliases(out), err
	}
	return nil, nil
}

// runListing runs k, as the config runs it, with args, for what it
// writes.
func runListing(specs map[Kind]Spec, k Kind, args ...string) (string, error) {
	argv, env := Command(specs, k)
	if len(argv) == 0 {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), modelsTimeout)
	defer cancel()
	cmd := proc.Detach(exec.CommandContext(ctx, argv[0], append(append([]string{}, argv[1:]...), args...)...))
	cmd.Env = append(os.Environ(), env...)
	out, err := proc.Output(cmd)
	return string(out), err
}

// codexModels reads codex's cache of its models: those its picker lists,
// in its order.
func codexModels(data []byte) ([]AgentModel, error) {
	var cache struct {
		Models []struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
			Visibility  string `json:"visibility"`
			Priority    int    `json:"priority"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}
	sort.SliceStable(cache.Models, func(i, j int) bool { return cache.Models[i].Priority < cache.Models[j].Priority })
	var out []AgentModel
	for _, m := range cache.Models {
		if m.Slug != "" && m.Visibility != "hide" {
			out = append(out, AgentModel{ID: m.Slug, Name: m.DisplayName})
		}
	}
	return out, nil
}

// cursorModels reads `cursor-agent models`: "id - Name" a line, under a
// heading.
func cursorModels(out string) []AgentModel {
	var models []AgentModel
	for _, l := range strings.Split(out, "\n") {
		id, name, ok := strings.Cut(strings.TrimSpace(l), " - ")
		if ok && modelID.MatchString(id) {
			models = append(models, AgentModel{ID: id, Name: strings.TrimSuffix(name, " (current, default)")})
		}
	}
	return models
}

// tabbedModels reads a listing of a model a line, its id first and what
// it is called after a tab, if anything: `agy models`, `opencode models`.
func tabbedModels(out string) []AgentModel {
	var models []AgentModel
	for _, l := range strings.Split(out, "\n") {
		id, name, _ := strings.Cut(strings.TrimSpace(l), "\t")
		if modelID.MatchString(id) {
			models = append(models, AgentModel{ID: id, Name: strings.TrimSpace(name)})
		}
	}
	return models
}

// copilotModels reads the models listed under `model` in `copilot help
// config`, a quoted name a line.
func copilotModels(out string) []AgentModel {
	var models []AgentModel
	in := false
	for _, l := range strings.Split(out, "\n") {
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(t, "`model`"):
			in = true
		case !in:
		case strings.HasPrefix(t, "- "):
			if id := strings.Trim(strings.TrimPrefix(t, "- "), `"'`+"`"); modelID.MatchString(id) {
				models = append(models, AgentModel{ID: id})
			}
		case t != "":
			if len(models) > 0 {
				return models
			}
		}
	}
	return models
}

// copilotChoices reads the models quoted in the description of --model
// in `copilot --help`, as some versions list them: (choices: "a", "b").
func copilotChoices(out string) []AgentModel {
	var desc []string
	in := false
	for _, l := range strings.Split(out, "\n") {
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(t, "--model "):
			in = true
		case in && strings.HasPrefix(t, "-"):
			in = false
		}
		if in {
			desc = append(desc, t)
		}
	}
	var models []AgentModel
	for _, q := range doubleQuoted.FindAllStringSubmatch(strings.Join(desc, " "), -1) {
		if modelID.MatchString(q[1]) {
			models = append(models, AgentModel{ID: q[1]})
		}
	}
	return models
}

// claudeAliases are the aliases claude's help gives for --model, quoted
// in its description ("an alias for the latest model (e.g. 'fable',
// 'opus', or 'sonnet')").
func claudeAliases(out string) []AgentModel {
	var desc []string
	in := false
	for _, l := range strings.Split(out, "\n") {
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(t, "--model "):
			in = true
		case in && strings.HasPrefix(t, "-"):
			in = false
		}
		if in {
			desc = append(desc, t)
		}
	}
	var models []AgentModel
	for _, q := range quoted.FindAllStringSubmatch(strings.Join(desc, " "), -1) {
		if modelID.MatchString(q[1]) {
			models = append(models, AgentModel{ID: q[1]})
		}
	}
	return models
}

var (
	// modelID is what a model's id looks like, to tell it from headings
	// and other text in a listing.
	modelID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/\[\]=-]*$`)
	quoted  = regexp.MustCompile(`'([^'\s]+)'`)
	// doubleQuoted is a word in double quotes.
	doubleQuoted = regexp.MustCompile(`"([^"\s]+)"`)
)
