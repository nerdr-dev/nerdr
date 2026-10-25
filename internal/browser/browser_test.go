package browser

import (
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
