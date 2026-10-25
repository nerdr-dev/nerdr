package analysis

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectImportsAndSymbols(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("tsconfig.json", `{"compilerOptions":{"paths":{"@/*":["./src/*"]}}}`)
	write("src/index.ts", `import { helper } from "./lib";
import { util } from "@/lib2";

export function main() { return helper() + util(); }
`)
	write("src/lib.ts", `export function helper() { return 1; }
`)
	write("src/lib2.ts", `export function util() { return 2; }
`)

	p := LoadProject(root)

	// Two imports resolved: a relative one and a tsconfig alias ("@/").
	imports := p.ImportsOf("src/index.ts")
	if len(imports) != 2 {
		t.Fatalf("ImportsOf(src/index.ts) = %v", imports)
	}

	syms := p.Symbols("src/index.ts")
	if len(syms) != 1 || syms[0].Name != "main" || syms[0].Kind != "function" {
		t.Fatalf("Symbols = %v", syms)
	}

	refs := p.References("helper", "src/lib.ts")
	if len(refs) != 2 {
		t.Fatalf("References = %v", refs)
	}
	// Expect the import site (line 1) and the call site (line 4).
	if refs[0].File != "src/index.ts" || refs[0].Line != 1 {
		t.Fatalf("first ref = %v", refs[0])
	}
	if refs[1].Line != 4 {
		t.Fatalf("second ref = %v", refs[1])
	}
}

func TestGroupChangedOrdersBottomUpAndIsolatesUnrelated(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/index.ts", `import { helper } from "./lib";
export function main() { return helper(); }
`)
	write("src/lib.ts", `export function helper() { return 1; }
`)
	write("src/unrelated.ts", `export const x = 1;
`)

	p := LoadProject(root)
	groups := p.GroupChanged([]string{"src/index.ts", "src/lib.ts", "src/unrelated.ts"})

	if len(groups) != 1 {
		t.Fatalf("expected exactly one group (unrelated.ts has no edges), got %d: %v", len(groups), groups)
	}
	g := groups[0]
	if len(g.Files) != 2 {
		t.Fatalf("expected 2 files in the group, got %v", g.Files)
	}
	if g.Files[0] != "src/lib.ts" {
		t.Fatalf("expected lib.ts (the dependency) first, got %v", g.Files)
	}
	if g.Files[1] != "src/index.ts" {
		t.Fatalf("expected index.ts (the dependent) second, got %v", g.Files)
	}
	if g.Base != "src/lib.ts" {
		t.Fatalf("expected Base = src/lib.ts, got %q", g.Base)
	}
}

func TestGroupChangedHandlesCycles(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.ts", `import "./b";
export const a = 1;
`)
	write("b.ts", `import "./a";
export const b = 1;
`)

	p := LoadProject(root)
	groups := p.GroupChanged([]string{"a.ts", "b.ts"})
	if len(groups) != 1 || len(groups[0].Files) != 2 {
		t.Fatalf("expected one 2-file group despite the import cycle, got %v", groups)
	}
}
