package analysis

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAnalysisFile(t *testing.T, root, name, source string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSizeableProjectReferencesReachLastFile(t *testing.T) {
	root := t.TempDir()
	// 400,000 identifiers spread over ordinary-sized source files. The old
	// 250,000 occurrence budget silently lost files at the end of the alphabet.
	for i := range 100 {
		writeAnalysisFile(t, root, fmt.Sprintf("file%03d.ts", i), strings.Repeat("const value = helper(input);\n", 1000))
	}
	writeAnalysisFile(t, root, "z.ts", "export const last = helper(input);\n")
	p := LoadProject(root)
	refs := p.References("helper", "")
	if p.Incomplete() != "" {
		t.Fatal(p.Incomplete())
	}
	if len(refs) != 100001 || refs[len(refs)-1].File != "z.ts" {
		t.Fatalf("lost late references: count=%d", len(refs))
	}
}

func TestIndexReportsEachBudget(t *testing.T) {
	for _, tc := range []struct {
		name               string
		bytes, refs, words int
		reason             string
	}{
		{"bytes", 1, 100, 100, "index byte"},
		{"references", 100, 1, 100, "reference limit"},
		{"identifiers", 100, 100, 1, "identifier limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeAnalysisFile(t, root, "a.ts", "first second third")
			p := LoadProject(root)
			p.buildWordIndexLimited(tc.bytes, tc.refs, tc.words)
			if !strings.Contains(p.Incomplete(), tc.reason) {
				t.Fatalf("missing reason: %q", p.Incomplete())
			}
			p.Index()
			if !strings.Contains(p.Incomplete(), tc.reason) {
				t.Fatal("lost cached status")
			}
		})
	}
}

func TestTraversalReportsEachBudget(t *testing.T) {
	for _, tc := range []struct {
		name           string
		files, entries int
		reason         string
	}{
		{"files", 1, 100, "source file limit"},
		{"entries", 100, 1, "directory entry limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeAnalysisFile(t, root, "a.ts", "a")
			writeAnalysisFile(t, root, "b.ts", "b")
			p := loadProject(root, tc.files, tc.entries)
			p.Index()
			if !strings.Contains(p.Incomplete(), tc.reason) {
				t.Fatalf("missing reason: %q", p.Incomplete())
			}
			complete := LoadProject(root)
			complete.Index()
			if p.Same(complete) {
				t.Fatal("incomplete project reused as complete")
			}
		})
	}
}

func TestOversizedSourceMarksIndexIncomplete(t *testing.T) {
	root := t.TempDir()
	writeAnalysisFile(t, root, "a.ts", strings.Repeat(" ", maxSourceBytes+1))
	p := LoadProject(root)
	p.Index()
	if p.Incomplete() == "" {
		t.Fatal("oversized source silently skipped")
	}
}
