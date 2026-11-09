// Package browser opens a URL — a pull request, a comment, a CI run — in
// the user's browser.
package browser

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/launch"
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

// PagesDir is the folder OpenPage writes pages in: the user's own, mode
// 0700. Tests set it.
var PagesDir = func() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "nerdr", "pages"), nil
}

// OpenPage writes page, a page of its own made where nerdr's core runs,
// maybe another machine, to a file of name in PagesDir and opens it, as
// OpenFile does. The name is the caller's, made a file name here: a page
// never says where it is written.
func OpenPage(name string, page []byte) error {
	dir, err := PagesDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	file := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, name)
	if file == "" {
		file = "page"
	}
	path := filepath.Join(dir, file+".html")
	tmp, err := os.CreateTemp(dir, ".page-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.Write(page)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr, os.Chmod(tmp.Name(), 0o600)); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	return OpenFile(path)
}
