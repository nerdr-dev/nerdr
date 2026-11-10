package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestServers: servers are added to [[servers]] last, read back in order
// with their defaults, and one is taken out with its table alone.
func TestServers(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), []byte("prefix = \"ctrl+a\"\n\n[review]\nrounds = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, s := range []Server{{Name: "box"}, {Name: "gpu", SSH: "gpu.lan", Folder: "~/src"}, {Name: "old"}} {
		if err := AddServer(s); err != nil {
			t.Fatal(err)
		}
	}
	if err := AddServer(Server{Name: "box"}); err == nil {
		t.Error("a second box added")
	}
	if err := AddServer(Server{Name: "a:b"}); err == nil {
		t.Error("a name with a colon added")
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Servers) != 3 || c.Servers[0].Host() != "box" || c.Servers[0].Dir() != "~" ||
		c.Servers[1].Host() != "gpu.lan" || c.Servers[1].Dir() != "~/src" || c.Prefix != "ctrl+a" {
		t.Fatalf("read %+v", c)
	}
	if err := RemoveServer("gpu"); err != nil {
		t.Fatal(err)
	}
	c, _ = Load()
	if len(c.Servers) != 2 || c.Servers[0].Name != "box" || c.Servers[1].Name != "old" || c.Review.Rounds != 2 {
		t.Fatalf("after removing gpu: %+v", c.Servers)
	}
	b, _ := os.ReadFile(Path())
	if strings.Contains(string(b), "gpu") {
		t.Fatalf("gpu still there:\n%s", b)
	}
	if ScopeOf("", "servers") != ScopeClient {
		t.Error("servers is not the client's")
	}
}
