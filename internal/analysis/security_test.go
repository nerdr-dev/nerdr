package analysis

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestAnalysisRejectsLinksSpecialFilesAndOversizedSources(t *testing.T) {
	for _, kind := range []string{"symlink", "fifo", "large"} {
		t.Run(kind, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			secret := filepath.Join(outside, "secret")
			os.WriteFile(secret, []byte("export const PRIVATE_OUTSIDE_NAME = 1;"), 0600)
			for _, name := range []string{"tsconfig.json", "source.ts"} {
				path := filepath.Join(root, name)
				var err error
				switch kind {
				case "symlink":
					err = os.Symlink(secret, path)
				case "fifo":
					err = syscall.Mkfifo(path, 0600)
				case "large":
					var f *os.File
					f, err = os.Create(path)
					if err == nil {
						err = f.Truncate(8 << 20)
						f.Close()
					}
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			p := LoadProject(root)
			p.Index()
			if syms := p.Symbols("source.ts"); len(syms) != 0 {
				t.Fatalf("read unsafe source: %+v", syms)
			}
			if imports := p.ImportsOf("source.ts"); len(imports) != 0 {
				t.Fatalf("unsafe imports: %v", imports)
			}
			if len(p.wordIndex) != 0 {
				t.Fatal("indexed unsafe file")
			}
		})
	}
}

func TestAnalysisRejectsSourceReplacedAfterWalk(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	path := filepath.Join(root, "file.ts")
	os.WriteFile(path, []byte("export const original = 1;"), 0600)
	p := LoadProject(root)
	secret := filepath.Join(outside, "secret")
	os.WriteFile(secret, []byte("export const PRIVATE_OUTSIDE_NAME = 1;"), 0600)
	os.Remove(path)
	if err := os.Symlink(secret, path); err != nil {
		t.Fatal(err)
	}
	p.Index()
	if len(p.wordIndex) != 0 || len(p.Symbols("file.ts")) != 0 {
		t.Fatal("followed a replacement symlink")
	}
	if _, err := p.readSource("../secret"); err == nil {
		t.Fatal("read traversal path")
	}
}

func TestAnalysisIndexHasAggregateBudget(t *testing.T) {
	root := t.TempDir()
	// Individually permitted files together exceed the reference budget.
	for i := range 14 {
		name := fmt.Sprintf("%02d.ts", i)
		if err := os.WriteFile(filepath.Join(root, name), []byte(strings.Repeat("x ", 150000)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	p := LoadProject(root)
	p.Index()
	if p.Incomplete() == "" {
		t.Fatal("reference cap silently truncated the index")
	}
	if n := len(p.wordIndex["x"]); n != maxIndexRefs {
		t.Fatalf("unbounded references: %d", n)
	}
}
