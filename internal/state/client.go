package state

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/nerdr-dev/nerdr/internal/denied"
)

// Client is what a client keeps of its own, where it runs: what it shows
// across the machines, which no one core knows. Each core keeps the
// layout of its own tabs in its session.
type Client struct {
	// Key is what the client was started for: the folder of the core
	// here, or HOST:FOLDER of one reached over ssh.
	Key string `json:"key"`
	// Off are the servers the user disconnected from: not connected to
	// as the client starts, until connected to again.
	Off []string `json:"off,omitempty"`
	// Active is the tab in front, and Split the one beside it, when they
	// are a server's: by its name and the tab's id.
	Active    MachineTab `json:"active,omitzero"`
	Split     MachineTab `json:"split,omitzero"`
	SplitLeft bool       `json:"split_left,omitempty"`
}

// MachineTab names a tab of a machine's: the machine's name among the
// client's, and the tab's id.
type MachineTab struct {
	Machine string `json:"machine,omitempty"`
	Tab     string `json:"tab,omitempty"`
}

// ClientPath is the file of the client started for key.
func ClientPath(key string) string {
	sum := sha1.Sum([]byte(key))
	return filepath.Join(filepath.Dir(Dir()), "client-"+hex.EncodeToString(sum[:6])+".json")
}

// LoadClient reads what the client started for key keeps; nothing yet,
// or a file that cannot be read, is an empty one.
func LoadClient(key string) Client {
	c := Client{Key: key}
	data, err := os.ReadFile(ClientPath(key))
	if err != nil || json.Unmarshal(data, &c) != nil || c.Key != key {
		return Client{Key: key}
	}
	return c
}

// SaveClient writes c, for the next start of the client for c.Key.
func SaveClient(c Client) error {
	p := ClientPath(c.Key)
	if err := denied.Err(os.MkdirAll(filepath.Dir(p), 0o755)); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := denied.Err(os.WriteFile(tmp, data, 0o600)); err != nil {
		return err
	}
	return denied.Err(os.Rename(tmp, p))
}
