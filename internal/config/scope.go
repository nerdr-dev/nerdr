package config

import (
	"encoding/json"
	"reflect"
	"strings"
)

// The scopes of the settings, as Config's scope tags have them.
const (
	// ScopeClient is a setting of the client's: how nerdr looks and tells
	// you things, read from the config file where the client runs.
	ScopeClient = "client"
	// ScopeCore is a setting of the core's: the agents and how they are
	// started, where repositories and worktrees are, what is installed for
	// the agents. Read from the config file where the core runs, and never
	// taken from a client, whose file may be on another machine.
	ScopeCore = "core"
)

// Merge is core with the client's settings taken from client: what a core
// goes by, given the config a client sent it. Nothing of core's own scope
// is taken from client.
func Merge(core, client Config) Config {
	out := core
	o, c := reflect.ValueOf(&out).Elem(), reflect.ValueOf(client)
	t := o.Type()
	for i := range t.NumField() {
		if t.Field(i).Tag.Get("scope") == ScopeClient {
			o.Field(i).Set(c.Field(i))
		}
	}
	return out
}

// ScopeOf is the scope of key in table, as SetKey names them: ScopeCore
// for a key nerdr does not know, so it is never taken as a client's.
func ScopeOf(table, key string) string {
	name := key
	if table != "" {
		name, _, _ = strings.Cut(table, ".")
	}
	t := reflect.TypeFor[Config]()
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Tag.Get("toml") == name {
			if f.Tag.Get("scope") == ScopeClient {
				return ScopeClient
			}
			return ScopeCore
		}
	}
	return ScopeCore
}

// wireConfig is a Config as JSON: its settings, and what the file sets of
// those it says so of.
type wireConfig struct {
	plainConfig
	DataDirSet      bool `json:"$dataDirSet,omitempty"`
	WorktreesDirSet bool `json:"$worktreesDirSet,omitempty"`
}

type plainConfig Config

// MarshalJSON writes c with what its file sets, for a core to send a
// client its own settings.
func (c Config) MarshalJSON() ([]byte, error) {
	return json.Marshal(wireConfig{plainConfig(c), c.dataDirSet, c.worktreesDirSet})
}

// UnmarshalJSON reads what MarshalJSON wrote.
func (c *Config) UnmarshalJSON(b []byte) error {
	var w wireConfig
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	*c = Config(w.plainConfig)
	c.dataDirSet, c.worktreesDirSet = w.DataDirSet, w.WorktreesDirSet
	return nil
}
