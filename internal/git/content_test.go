package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestChangedNewLines(t *testing.T) {
	diff := `diff --git a/f.ts b/f.ts
index 0000000..1111111 100644
--- a/f.ts
+++ b/f.ts
@@ -1,3 +1,4 @@
 one
-two
+TWO
+extra
 three
`
	got := changedNewLines(diff)
	want := map[int]bool{2: true, 3: true}
	if len(got) != len(want) || !got[2] || !got[3] {
		t.Fatalf("changedNewLines = %v, want %v", got, want)
	}
}

func TestNewFileContentOfABinaryFileSaysSo(t *testing.T) {
	dir := t.TempDir()
	// A vim swap file: NULs, and control codes that move the cursor.
	if err := os.WriteFile(filepath.Join(dir, ".f.swp"), []byte("b0VIM 9.1\x00\x00\r\x1b[2J\x0c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := NewFileContent(dir, ".f.swp")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Binary files /dev/null and b/.f.swp differ") || strings.ContainsAny(out, "\x00\r\x1b") {
		t.Fatalf("expected git's binary message and none of the bytes, got:\n%q", out)
	}
}

func TestNewFileContentFormatsAsAllAdded(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "new.ts"), []byte("export const a = 1;\nexport const b = 2;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := NewFileContent(dir, "new.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "+export const a = 1;") || !strings.Contains(out, "+export const b = 2;") {
		t.Fatalf("expected both lines rendered as additions, got:\n%s", out)
	}
	if strings.Contains(out, "+\n\n") {
		t.Fatalf("expected no spurious trailing blank added line, got:\n%s", out)
	}
}

func initRepoWithCommit(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", "-A")
	run("commit", "-q", "-m", "init")
	return dir
}

func TestOldFileContentReadsHeadVersion(t *testing.T) {
	dir := initRepoWithCommit(t, map[string]string{"f.ts": "line one\nline two\n"})
	if err := os.WriteFile(filepath.Join(dir, "f.ts"), []byte("line one\nline TWO\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := OldFileContent(dir, "HEAD", "f.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, " line two") {
		t.Fatalf("expected old content 'line two' present, got:\n%s", out)
	}
	if strings.Contains(out, "TWO") {
		t.Fatalf("old content should not contain the new edit, got:\n%s", out)
	}
}

func TestFullNewFileWithMarkersKeepsHighlightAcrossWholeFile(t *testing.T) {
	dir := initRepoWithCommit(t, map[string]string{"f.ts": "keep one\nkeep two\nold value\nkeep three\n"})
	if err := os.WriteFile(filepath.Join(dir, "f.ts"), []byte("keep one\nkeep two\nnew value\nkeep three\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := Diff(dir, "f.ts")
	if err != nil {
		t.Fatal(err)
	}
	out, err := FullNewFileWithMarkers(dir, "f.ts", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "+new value") {
		t.Fatalf("expected changed line marked as added, got:\n%s", out)
	}
	if !strings.Contains(out, " keep one") || !strings.Contains(out, " keep three") {
		t.Fatalf("expected unchanged lines kept as plain context, got:\n%s", out)
	}
}

// TestParseDiffTakesHunkLinesByTheirFirstCharacter covers code that itself
// starts with "++" or "--", and the marker git adds for a missing final
// newline: none of them may throw the line numbers off.
func TestParseDiffTakesHunkLinesByTheirFirstCharacter(t *testing.T) {
	diff := `diff --git a/f.c b/f.c
--- a/f.c
+++ b/f.c
@@ -1,3 +1,3 @@
 a
+++i;
--- old comment
 b
\ No newline at end of file
+c`
	got := ParseDiff(diff)
	want := []DiffLine{{'h', 0}, {'h', 0}, {'h', 0}, {'@', 0},
		{' ', 1}, {'+', 2}, {'-', 0}, {' ', 3}, {'h', 0}, {'+', 4}}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestOldFileContentNumbersFromOne covers the old-code view: its lines are
// numbered, highlighted and opened from 1, not shifted one line down.
func TestOldFileContentNumbersFromOne(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte("package x\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "i"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	out, err := OldFileContent(dir, "HEAD", "x.go")
	if err != nil {
		t.Fatal(err)
	}
	var first int
	for _, d := range ParseDiff(out) {
		if d.Kind == ' ' {
			first = d.NewLine
			break
		}
	}
	if first != 1 {
		t.Fatalf("first line numbered %d, want 1:\n%s", first, out)
	}
}
