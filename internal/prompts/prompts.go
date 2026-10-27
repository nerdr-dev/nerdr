// Package prompts keeps the user's saved prompts: text they type into an
// agent again and again ("commit, push and open a pull request"), picked
// from a list with the prefix + l or in F3 rather than written out each
// time. They are kept in prompts.toml beside the config file, the same
// for every repository, and may be edited there by hand:
//
//	[[prompt]]
//	text = "commit, push and open a pull request"
//	enter = true
package prompts

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/config"
	"github.com/nerdr-dev/nerdr/internal/denied"

	"github.com/BurntSushi/toml"
)

// Prompt is one saved prompt.
type Prompt struct {
	Text string `toml:"text"`
	// Enter is set when enter is pressed after the text is typed in, to
	// send it; off, it is left in the agent's prompt to change or send.
	Enter bool `toml:"enter"`
}

type file struct {
	Prompt []Prompt `toml:"prompt"`
}

// Path is where the prompts are kept: prompts.toml beside config.toml.
func Path() string {
	return filepath.Join(filepath.Dir(config.Path()), "prompts.toml")
}

// Load reads the saved prompts, in order. A missing file is none.
func Load() ([]Prompt, error) {
	data, err := os.ReadFile(Path())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f file
	if _, err := toml.Decode(string(data), &f); err != nil {
		return nil, fmt.Errorf("%s: %w", Path(), err)
	}
	var out []Prompt
	for _, p := range f.Prompt {
		if p.Text = strings.TrimSpace(p.Text); p.Text != "" {
			out = append(out, p)
		}
	}
	return out, nil
}

// Save writes ps over the file, through a file beside it so a crash
// halfway leaves the old one.
func Save(ps []Prompt) error {
	var buf bytes.Buffer
	buf.WriteString("# nerdr's saved prompts: the prefix + l, or F3 then →.\n\n")
	if err := toml.NewEncoder(&buf).Encode(file{Prompt: ps}); err != nil {
		return err
	}
	dir := filepath.Dir(Path())
	if err := denied.Err(os.MkdirAll(dir, 0755)); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".nerdr-prompts-*")
	if denied.Err(err) != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(buf.Bytes())
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return denied.Err(os.Rename(f.Name(), Path()))
}
