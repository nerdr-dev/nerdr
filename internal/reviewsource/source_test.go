package reviewsource

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLocalReadsTheProject: LoadProject reads a worktree's code and gives
// a version that changes only once the code has; Imports and Relations
// answer from what it read.
func TestLocalReadsTheProject(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("lib.ts", "export function helper() { return 1; }\n")
	write("use.ts", "import { helper } from './lib';\nexport const x = helper();\n")
	var s Local
	if got := s.Imports(dir, []string{"lib.ts", "use.ts"}); got != nil {
		t.Fatalf("imports before the code was read: %v", got)
	}

	v := s.LoadProject(dir)
	if v == 0 || s.LoadProject(dir) != v {
		t.Fatalf("version %d, and again %d, with nothing changed", v, s.LoadProject(dir))
	}
	if got := s.Imports(dir, []string{"lib.ts", "use.ts"}); len(got["use.ts"]) != 1 || got["use.ts"][0] != "lib.ts" {
		t.Fatalf("imports: %v", got)
	}
	syms, refs, incomplete := s.Relations(dir, "lib.ts")
	if incomplete != "" {
		t.Fatal(incomplete)
	}
	if len(syms) != 1 || syms[0].Name != "helper" || len(refs) != 1 || len(refs[0]) == 0 || refs[0][0].File != "use.ts" {
		t.Fatalf("relations: %v %v", syms, refs)
	}

	write("more.ts", "export const y = 2;\n")
	if s.LoadProject(dir) == v {
		t.Fatal("the version stayed the same with the code changed")
	}
}

func TestLocalRelationsReportsIncompleteIndex(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lib.ts"), []byte("export const helper = 1;"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "caller.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(1 << 20); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	s := Local{}
	s.LoadProject(dir)
	syms, refs, incomplete := s.Relations(dir, "lib.ts")
	if len(syms) != 1 || len(refs) != 1 || incomplete == "" {
		t.Fatalf("missing incomplete result: %v %v %q", syms, refs, incomplete)
	}
}

func TestConcurrentProjectReloadAndIncompleteRelations(t *testing.T) {
	dir := t.TempDir()
	s := Local{}
	s.LoadProject(dir)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 100 {
			// A missing source updates the cached project's incomplete status.
			s.Relations(dir, "missing.ts")
		}
	}()
	for range 100 {
		s.LoadProject(dir)
	}
	<-done
}
