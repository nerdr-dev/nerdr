package review

import (
	"strings"
	"testing"

	"nerdr/internal/analysis"
	"nerdr/internal/git"
)

func TestStatusMark(t *testing.T) {
	cases := map[string]string{
		" M": "m", "M ": "m", "MM": "m",
		"??": "n", "A ": "n",
		"D ": "d", " D": "d",
		"":   " ", // the peeked file, which isn't part of the change set
		"  ": " ",
	}
	for code, want := range cases {
		if got := statusMark(code); got != want {
			t.Fatalf("statusMark(%q) = %q, want %q", code, got, want)
		}
	}
}

// TestBuildTreeKeepsKindsTogether covers the flat list: new and modified
// files that belong to the same change sit in the same group rather than
// being split into separate sections by kind.
func TestBuildTreeKeepsKindsTogether(t *testing.T) {
	files := []git.FileStatus{
		{Path: "lib.ts", Code: " M"},
		{Path: "caller.ts", Code: "??"},
		{Path: "unrelated.ts", Code: " M"},
	}
	groups := []analysis.Group{{Files: []string{"lib.ts", "caller.ts"}, Base: "lib.ts"}}

	tree := buildTree(files, groups)
	if len(tree) != 2 {
		t.Fatalf("expected one group plus the leftovers, got %d roots", len(tree))
	}

	group := tree[0]
	if len(group.children) != 2 {
		t.Fatalf("expected both files in the group, got %d", len(group.children))
	}
	marks := map[string]string{}
	for _, c := range group.children {
		marks[c.path] = statusMark(c.status)
	}
	if marks["lib.ts"] != "m" || marks["caller.ts"] != "n" {
		t.Fatalf("expected the kinds to survive as marks, got %v", marks)
	}
}

func TestBuildTreeWithoutGroupsHasNoWrapper(t *testing.T) {
	files := []git.FileStatus{{Path: "a.ts", Code: " M"}, {Path: "b.ts", Code: "??"}}
	tree := buildTree(files, nil)
	for _, n := range tree {
		if n.path == "#"+sectionFiles {
			t.Fatal("with nothing grouped there is nothing to distinguish, so no wrapper level")
		}
	}
	if len(tree) != 2 {
		t.Fatalf("expected both files at the top level, got %d roots", len(tree))
	}
}

// TestGroupMembersSortByFolderKeepingDependencyOrder covers both halves:
// same-folder files read together, and the bottom-up order from the import
// analysis survives within a folder.
func TestGroupMembersSortByFolderKeepingDependencyOrder(t *testing.T) {
	files := []git.FileStatus{
		{Path: "src/lib/a.ts", Code: " M"},
		{Path: "src/app/x.ts", Code: " M"},
		{Path: "src/lib/b.ts", Code: " M"},
		{Path: "src/app/y.ts", Code: " M"},
	}
	// Dependency order as the analysis produced it, interleaving folders.
	groups := []analysis.Group{{
		Files: []string{"src/lib/a.ts", "src/app/x.ts", "src/lib/b.ts", "src/app/y.ts"},
		Base:  "src/lib/a.ts",
	}}

	tree := buildTree(files, groups)
	var got []string
	for _, c := range tree[0].children {
		got = append(got, c.path)
	}
	want := []string{
		"src/app/x.ts", "src/app/y.ts", // folder first…
		"src/lib/a.ts", "src/lib/b.ts", // …and dependency order kept inside it
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestDirOf(t *testing.T) {
	for in, want := range map[string]string{
		"src/lib/a.ts": "src/lib",
		"a.ts":         "",
		"src/a.ts":     "src",
	} {
		if got := dirOf(in); got != want {
			t.Fatalf("dirOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestElideLeftKeepsTheDistinguishingEnd(t *testing.T) {
	// Two files of the same name are told apart by the nearest parent, so
	// the end of the path is what has to survive.
	a := elideLeft("src/app/api/appointments", 18)
	b := elideLeft("src/app/api/appointments/prep", 18)
	if a == b {
		t.Fatalf("paths that differ must still differ once shortened: %q vs %q", a, b)
	}
	if !strings.HasSuffix(b, "prep") {
		t.Fatalf("expected the end to be kept, got %q", b)
	}
	if len([]rune(a)) > 18 || len([]rune(b)) > 18 {
		t.Fatalf("over budget: %q (%d), %q (%d)", a, len([]rune(a)), b, len([]rune(b)))
	}
	if got := elideLeft("src/lib", 18); got != "src/lib" {
		t.Fatalf("a path that fits must be left alone, got %q", got)
	}
}

func TestBuildTreeHasNoCollapsedFolders(t *testing.T) {
	// Everything is listed flat, so no changed file can hide behind a
	// collapsed folder — which also kept n/p from reaching them, since
	// those only walk visible rows.
	files := []git.FileStatus{
		{Path: "deep/nested/path/a.ts", Code: " M"},
		{Path: "b.ts", Code: "??"},
	}
	m := Model{tree: buildTree(files, nil)}
	var seen []string
	for _, r := range m.flatRows() {
		if r.node.isDir {
			t.Fatalf("expected no directory rows, got %q", r.node.path)
		}
		seen = append(seen, r.node.path)
	}
	if len(seen) != 2 {
		t.Fatalf("expected both files visible without expanding anything, got %v", seen)
	}
}
