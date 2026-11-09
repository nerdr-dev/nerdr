package browser

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

// TestCommandPrefersTheSetting covers which browser a URL opens in: the
// config's, else $BROWSER, else the system default.
func TestCommandPrefersTheSetting(t *testing.T) {
	t.Cleanup(func() { Setting = "" })
	t.Setenv("BROWSER", "")
	def := "xdg-open"
	if runtime.GOOS == "darwin" {
		def = "open"
	}
	if got := Command("u"); !reflect.DeepEqual(got, []string{def, "u"}) {
		t.Errorf("default: %v", got)
	}
	t.Setenv("BROWSER", "firefox")
	if got := Command("u"); !reflect.DeepEqual(got, []string{"firefox", "u"}) {
		t.Errorf("$BROWSER: %v", got)
	}
	Setting = "google-chrome --new-window"
	if got := Command("u"); !reflect.DeepEqual(got, []string{"google-chrome", "--new-window", "u"}) {
		t.Errorf("setting: %v", got)
	}
}

// TestOpenTakesOnlyWebAddresses covers what reaches xdg-open: a check's
// URL is whatever its poster set, and file: or a custom scheme would run
// something other than a browser.
func TestOpenTakesOnlyWebAddresses(t *testing.T) {
	for _, u := range []string{"", "file:///etc/passwd", "javascript:alert(1)", "vscode://x", "-profile", "https://"} {
		if err := Open(u); err == nil {
			t.Errorf("opened %q", u)
		}
	}
}

// TestOpenPageWritesWhereTheClientSays: a page made where core runs is
// written in the user's own folder, under a name made a plain file name,
// whatever it holds, readable by the user alone.
func TestOpenPageWritesWhereTheClientSays(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pages")
	old := PagesDir
	PagesDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { PagesDir, Setting = old, "" })
	Setting = "true" // no browser for the test
	if err := OpenPage("walkthrough-../../evil/x", []byte("<p>hi</p>")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("pages: %v %v", entries, err)
	}
	name := entries[0].Name()
	if name != "walkthrough-______evil_x.html" {
		t.Fatalf("written as %q", name)
	}
	st, _ := os.Stat(filepath.Join(dir, name))
	dst, _ := os.Stat(dir)
	if st.Mode().Perm() != 0o600 || dst.Mode().Perm() != 0o700 {
		t.Fatalf("modes %o and %o", st.Mode().Perm(), dst.Mode().Perm())
	}
	if b, _ := os.ReadFile(filepath.Join(dir, name)); string(b) != "<p>hi</p>" {
		t.Fatalf("the page is %q", b)
	}
}
