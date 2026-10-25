package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSkillSelectionPreservesConfigAndCanBeEmpty(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(Path()), 0755); err != nil {
		t.Fatal(err)
	}
	original := "skills = \"off\"\nenabled_skills = [\n \"nerdr-explain\",\n]\n# keep this comment\nprefix = \"ctrl+t\"\n[agents.claude]\ncommand = [\"custom-agent\"]\n"
	if err := os.WriteFile(Path(), []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	for _, names := range [][]string{{"nerdr-plugins"}, {}, {"nerdr-explain", "nerdr-request-review"}} {
		if err := SaveSetup(InstallWorktree, nil, names); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(cfg.EnabledSkills, names) || cfg.Prefix != "ctrl+t" {
			t.Fatalf("config: %+v", cfg)
		}
		if cfg.SkillEnabled("nerdr-plugins") != (len(names) == 1) {
			t.Fatal("selection not effective")
		}
		b, _ := os.ReadFile(Path())
		if !strings.Contains(string(b), "# keep this comment") || !strings.Contains(string(b), "custom-agent") {
			t.Fatal(string(b))
		}
	}
}

// install had the name skills once: the old name is still read, install
// winning where both are set.
func TestInstallReadsTheOldName(t *testing.T) {
	for _, c := range []struct {
		name, file, want string
	}{
		{"only skills", "skills = \"worktree\"\n", InstallWorktree},
		{"only install", "install = \"home\"\n", InstallHome},
		{"both", "skills = \"off\"\ninstall = \"worktree\"\n", InstallWorktree},
		{"install empty", "install = \"\"\nskills = \"home\"\n", ""},
		{"not a setting", "install = \"everywhere\"\n", ""},
		{"old name not a setting", "skills = \"everywhere\"\n", ""},
		{"neither", "prefix = \"ctrl+t\"\n", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			if err := os.MkdirAll(filepath.Dir(Path()), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(Path(), []byte(c.file), 0644); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load()
			if err != nil || cfg.Install != c.want {
				t.Fatalf("install %q, want %q (%v)", cfg.Install, c.want, err)
			}
		})
	}
}

// Writing install takes out a skills line, from the first start and the
// settings dialog alike, and the settings dialog sees an old skills line
// as install.
func TestWritingInstallTakesOutTheOldName(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(Path()), 0755); err != nil {
		t.Fatal(err)
	}
	original := "# my config\nprefix = \"ctrl+t\"\nskills = \"off\"\nenabled_skills = [\"nerdr-send\"]\n[agents.claude]\ncommand = [\"custom-agent\"]\n"
	if err := os.WriteFile(Path(), []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	if raw, err := Raw(); err != nil || raw["install"] != "off" || raw["skills"] != nil {
		t.Fatalf("raw: %v %v", raw, err)
	}
	for _, write := range []func() error{
		func() error { return SaveSetup(InstallWorktree, nil) },
		func() error { return SetKey("", "install", Quote(InstallHome)) },
	} {
		if err := write(); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(Path())
		text := string(b)
		if strings.Contains("\n"+text, "\nskills =") || strings.Count(text, "install =") != 1 ||
			!strings.Contains(text, "# my config") || !strings.Contains(text, "enabled_skills = [\"nerdr-send\"]") || !strings.Contains(text, "custom-agent") {
			t.Fatalf("config file:\n%s", text)
		}
		if err := os.WriteFile(Path(), []byte(original), 0644); err != nil {
			t.Fatal(err)
		}
	}
}
