package config

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/agents"
)

// TestEverySettingHasAScope keeps a new setting from going unclassified:
// one with no scope would be taken from neither file by Merge's rules, or
// from the wrong one.
func TestEverySettingHasAScope(t *testing.T) {
	typ := reflect.TypeFor[Config]()
	for i := range typ.NumField() {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		switch f.Tag.Get("scope") {
		case ScopeClient, ScopeCore:
		default:
			t.Errorf("Config.%s has no scope tag: client or core", f.Name)
		}
	}
}

// TestMergeTakesOnlyTheClientsSettings: a client's config never changes
// how the core starts its agents, or where its worktrees go.
func TestMergeTakesOnlyTheClientsSettings(t *testing.T) {
	core := Config{
		Prefix:       "ctrl+b",
		WorktreesDir: "/srv/wt",
		Agents:       map[agents.Kind]agents.Spec{"claude": {Command: []string{"sandbox", "claude"}}},
		Review:       Review{Rounds: 2},
		dataDirSet:   true,
	}
	client := Config{
		Prefix:       "ctrl+a",
		Mouse:        new(bool),
		WorktreesDir: "/home/me/wt",
		Agents:       map[agents.Kind]agents.Spec{"claude": {Command: []string{"claude"}}},
		Review:       Review{Rounds: 9},
	}
	got := Merge(core, client)
	if got.Prefix != "ctrl+a" || got.Mouse == nil {
		t.Errorf("the client's settings not taken: prefix %q, mouse %v", got.Prefix, got.Mouse)
	}
	if got.WorktreesDir != "/srv/wt" || got.Review.Rounds != 2 || !got.DataDirSet() {
		t.Errorf("the core's settings changed: %+v", got)
	}
	if cmd := got.Agents["claude"].Command; !reflect.DeepEqual(cmd, []string{"sandbox", "claude"}) {
		t.Errorf("agent command %q, want the core's own", cmd)
	}
}

func TestScopeOf(t *testing.T) {
	for _, c := range []struct{ table, key, want string }{
		{"", "prefix", ScopeClient},
		{"", "editor", ScopeClient},
		{"", "worktrees_dir", ScopeCore},
		{"", "install", ScopeCore},
		{"review", "rounds", ScopeCore},
		{"review.models", "codex", ScopeCore},
		{"agents.claude", "command", ScopeCore},
		{"", "no_such_key", ScopeCore},
	} {
		if got := ScopeOf(c.table, c.key); got != c.want {
			t.Errorf("ScopeOf(%q, %q) = %s, want %s", c.table, c.key, got, c.want)
		}
	}
}

// TestConfigAsJSON: what a core sends a client of its settings keeps what
// its file sets, which the first-start setup asks by.
func TestConfigAsJSON(t *testing.T) {
	c := Config{WorktreesDir: "/srv/wt", Review: Review{Rounds: 3}, dataDirSet: true}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var got Config
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, c) {
		t.Fatalf("got %+v, want %+v (%s)", got, c, b)
	}
}
