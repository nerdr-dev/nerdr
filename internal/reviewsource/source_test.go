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
	syms, refs := s.Relations(dir, "lib.ts")
	if len(syms) != 1 || syms[0].Name != "helper" || len(refs) != 1 || len(refs[0]) == 0 || refs[0][0].File != "use.ts" {
		t.Fatalf("relations: %v %v", syms, refs)
	}

	write("more.ts", "export const y = 2;\n")
	if s.LoadProject(dir) == v {
		t.Fatal("the version stayed the same with the code changed")
	}
}
