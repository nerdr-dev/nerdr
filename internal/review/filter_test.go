package review

import (
	"testing"

	"nerdr/internal/git"
)

func TestFilterFiles(t *testing.T) {
	files := []git.FileStatus{
		{Path: "src/lib/billing.ts", Code: " M"},
		{Path: "src/lib/stripe.ts", Code: " M"},
		{Path: "components/BillingPanel.tsx", Code: " M"},
	}
	got := filterFiles(files, "billing")
	if len(got) != 2 {
		t.Fatalf("filterFiles billing = %v, want 2", got)
	}
}

func TestIgnoredPath(t *testing.T) {
	cases := map[string]bool{
		"package-lock.json":      true,
		"src/lib/billing.ts":     false,
		"dist/bundle.min.js":     true,
		"src/x.min.js.map":       true,
		"node_modules/foo/index": true,
	}
	for path, want := range cases {
		if got := ignoredPath(path); got != want {
			t.Errorf("ignoredPath(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestHideIgnoredFiles(t *testing.T) {
	files := []git.FileStatus{
		{Path: "src/a.ts", Code: " M"},
		{Path: "package-lock.json", Code: " M"},
		{Path: "src/b.min.js", Code: " M"},
	}
	got := hideIgnoredFiles(files)
	if len(got) != 1 || got[0].Path != "src/a.ts" {
		t.Fatalf("hideIgnoredFiles = %v", got)
	}
}
