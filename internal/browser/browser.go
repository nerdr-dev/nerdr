// Package browser opens a URL — a pull request, a comment, a CI run — in
// the user's browser.
package browser

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"nerdr/internal/launch"
)

// Setting is the browser setting from the config: the command to open a
// URL with, the URL added as its last argument ("firefox",
// "google-chrome --new-window", "open -a Safari"). Empty for $BROWSER, and
// failing that the system's default browser.
var Setting string

// Command is the command line that opens url.
func Command(url string) []string {
	for _, s := range []string{Setting, os.Getenv("BROWSER")} {
		if f := strings.Fields(s); len(f) > 0 {
			return append(f, url)
		}
	}
	if runtime.GOOS == "darwin" {
		return []string{"open", url}
	}
	return []string{"xdg-open", url}
}

// Open opens url, and returns once the browser has been started. Only a
// web address is opened: the URLs come from GitHub, a check's from
// whoever posted it, and xdg-open would as readily run a file: or a
// custom scheme's handler, or a browser take one starting with "-" for
// an option.
func Open(link string) error {
	if u, err := url.Parse(link); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("not a web address: %q", link)
	}
	return launch.Detached(Command(link))
}

// OpenFile opens a page nerdr wrote itself, at path, in the browser. Only
// an absolute path to a file that is there is opened, as a file: URL.
func OpenFile(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("not an absolute path: %q", path)
	}
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		return fmt.Errorf("no page at %s", path)
	}
	return launch.Detached(Command((&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()))
}
