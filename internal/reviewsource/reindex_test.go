package reviewsource

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/analysis"
)

// countIndex counts the projects LoadProject reads into an index.
func countIndex(t *testing.T) *int {
	t.Helper()
	n := 0
	was := indexProject
	indexProject = func(p *analysis.Project) { n++; was(p) }
	t.Cleanup(func() { indexProject = was })
	return &n
}

// TestUnchangedProjectIsNotIndexedAgain: loading a worktree whose code is
// as it was keeps the project read last, without reading every file into
// an index again only to let it go.
func TestUnchangedProjectIsNotIndexedAgain(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lib.ts"), []byte("export const a = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	n := countIndex(t)
	var s Local
	v := s.LoadProject(dir)
	for range 3 {
		if s.LoadProject(dir) != v {
			t.Fatal("the version moved on with nothing changed")
		}
	}
	if *n != 1 {
		t.Fatalf("indexed %d times, not once", *n)
	}
	if err := os.WriteFile(filepath.Join(dir, "more.ts"), []byte("export const b = 2;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s.LoadProject(dir) == v || *n != 2 {
		t.Fatalf("a change: indexed %d times in all", *n)
	}
}

// TestRelationsOfOtherFilesLeaveTheProject: the relations of a file the
// project does not read (a large JSON or Go file), or of one deleted, say
// nothing is missing from the analysis, and the next load keeps the same
// project, so the diff is not read again for it, over and over.
func TestRelationsOfOtherFilesLeaveTheProject(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("lib.ts", "export const a = 1;\n")
	big := strings.Repeat("x", 600<<10)
	write("big.json", big)
	write("big.go", "package big\n// "+big+"\n")
	var s Local
	v := s.LoadProject(dir)
	for _, file := range []string{"big.json", "big.go", "gone.ts", "gone.go"} {
		syms, _, incomplete := s.Relations(dir, file)
		if len(syms) != 0 || incomplete != "" {
			t.Fatalf("%s: %v, incomplete %q", file, syms, incomplete)
		}
		if s.LoadProject(dir) != v {
			t.Fatalf("after the relations of %s, the version moved on", file)
		}
	}
}
