package analysis

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// "export default" followed by a declaration exports what the declaration
// names, or nothing named, never the keyword.
func TestExportDefaultDeclarationsAreNotNamedByTheirKeyword(t *testing.T) {
	for line, want := range map[string]string{
		"export default function () {}":      "",
		"export default async () => {}":      "",
		"export default class {}":            "",
		"export default abstract class A {}": "A",
		"export abstract class B {}":         "B",
		"export default function main() {}":  "main",
		"export default App":                 "App",
		"export default const":               "",
	} {
		name, _, ok := ParseSymbolDecl(line)
		if name != want || ok != (want != "") {
			t.Errorf("%q: %q, %v", line, name, ok)
		}
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.ts"), []byte("export default function () {}\nexport default class {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if syms := LoadProject(root).Symbols("a.ts"); len(syms) != 0 {
		t.Errorf("symbols: %v", syms)
	}
}

// The aliases of tsconfig's paths are tried longest first, whatever order
// the JSON object is read in.
func TestAliasesLongestFirst(t *testing.T) {
	root := t.TempDir()
	cfg := `{"compilerOptions":{"paths":{"@/*":["src/*"],"@/ui/*":["lib/ui/*"],"#/*":["x/*"],"@/u/*":["u/*"]}}}`
	if err := os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 20 {
		var got []string
		for _, a := range LoadProject(root).Aliases {
			got = append(got, a.Prefix)
		}
		if want := []string{"@/ui/", "@/u/", "#/", "@/"}; !slices.Equal(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
