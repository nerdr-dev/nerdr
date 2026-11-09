package review

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/browser"
	"github.com/nerdr-dev/nerdr/internal/git"
)

// pageSource draws one page for any walkthrough.
type pageSource struct{ Local }

func (pageSource) WalkthroughPage(wt, branch string) ([]byte, bool, error) {
	return []byte("<h1>" + branch + "</h1>"), true, nil
}

// TestWalkthroughPageIsWrittenWhereTheBrowserIs: the page core draws is
// written by the client, in its own folder, and opened from there: the
// worktree may be on the machine of a core elsewhere.
func TestWalkthroughPageIsWrittenWhereTheBrowserIs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pages")
	old := browser.PagesDir
	browser.PagesDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { browser.PagesDir, browser.Setting = old, "" })
	browser.Setting = "true" // no browser for the test

	msg := showWalkthrough(pageSource{}, git.Worktree{Path: "/srv/wt/app", Branch: "feat/x"}, "")()
	if got := msg.(toastMsg).text; got != "opened the walkthrough in the browser" {
		t.Fatalf("said %q", got)
	}
	b, err := os.ReadFile(filepath.Join(dir, "walkthrough-app-feat_x.html"))
	if err != nil || string(b) != "<h1>feat/x</h1>" {
		t.Fatalf("the page: %q %v", b, err)
	}
}
