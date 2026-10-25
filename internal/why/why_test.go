package why

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"nerdr/internal/store"
)

func writeNotes(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git", "nerdr"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "nerdr", FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadAndMatchByContent(t *testing.T) {
	dir := writeNotes(t,
		`{"file":"src/a.ts","anchor":"z.string().trim().catch('')","short":"empty context is valid","detail":"more"}`,
	)
	ix := Load(dir)
	if ix.Len() != 1 {
		t.Fatalf("expected 1 note, got %d", ix.Len())
	}

	// The anchor is a fragment of the line, and indentation is irrelevant.
	n, ok := ix.Match("src/a.ts", "  context: z.string().trim().catch(''),")
	if !ok || n.Short != "empty context is valid" {
		t.Fatalf("expected the note to match its anchor, got ok=%v %+v", ok, n)
	}

	// Same text in a different file must not match.
	if _, ok := ix.Match("src/b.ts", "  context: z.string().trim().catch(''),"); ok {
		t.Fatal("expected no match when the file differs")
	}

	// Once the code has moved on, the anchor is gone and so is the note —
	// better than showing it against the wrong hunk.
	if _, ok := ix.Match("src/a.ts", "  context: z.number(),"); ok {
		t.Fatal("expected no match once the anchored code changed")
	}
}

func TestMatchPrefersTheMostRecentNote(t *testing.T) {
	dir := writeNotes(t,
		`{"file":"a.ts","anchor":"doTheThing()","short":"first reason"}`,
		`{"file":"a.ts","anchor":"doTheThing()","short":"revised reason"}`,
	)
	n, ok := Load(dir).Match("a.ts", "  return doTheThing();")
	if !ok || n.Short != "revised reason" {
		t.Fatalf("expected the later note to supersede the earlier one, got %+v", n)
	}
}

func TestLoadSkipsUnusableLinesWithoutLosingTheRest(t *testing.T) {
	dir := writeNotes(t,
		`{"file":"a.ts","anchor":"one()","short":"kept"}`,
		`not json at all`,
		``,
		`{"file":"a.ts","anchor":"two()"}`, // no short text
		`{"anchor":"three()","short":"missing file"}`, // no file
		`{"file":"a.ts","anchor":"four()","short":"also kept"}`,
	)
	ix := Load(dir)
	if ix.Len() != 2 {
		t.Fatalf("expected the 2 usable notes to survive, got %d", ix.Len())
	}
	if _, ok := ix.Match("a.ts", "one()"); !ok {
		t.Fatal("expected the note before the malformed line to survive")
	}
	if _, ok := ix.Match("a.ts", "four()"); !ok {
		t.Fatal("expected the note after the malformed line to survive")
	}
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	if ix := Load(t.TempDir()); ix.Len() != 0 {
		t.Fatal("expected an empty index for a worktree with no notes")
	}
	if _, ok := Load(t.TempDir()).Match("a.ts", "anything"); ok {
		t.Fatal("expected no match from an empty index")
	}
}

func TestLoadNormalisesFilePaths(t *testing.T) {
	dir := writeNotes(t, `{"file":"./src/a.ts","anchor":"thing()","short":"why"}`)
	if _, ok := Load(dir).Match("src/a.ts", "thing()"); !ok {
		t.Fatal("expected a leading ./ in the note's path to be normalised away")
	}
}

// TestLoadDropsPathsOutsideTheWorktree covers a note's file being joined to
// the worktree to read and open it: an agent writing the file by hand must
// not be able to point that outside the worktree.
func TestLoadDropsPathsOutsideTheWorktree(t *testing.T) {
	dir := writeNotes(t,
		`{"file":"src/../../secret.ts","anchor":"x","short":"out"}`,
		`{"file":"/etc/passwd","anchor":"x","short":"absolute"}`,
		`{"file":"src/../a.ts","anchor":"x","short":"in"}`,
	)
	ix := Load(dir)
	if ix.Len() != 1 {
		t.Fatalf("expected only the note inside the worktree, got %d", ix.Len())
	}
	if _, ok := ix.Match("a.ts", "x"); !ok {
		t.Fatal("expected src/../a.ts to be cleaned to a.ts")
	}
}

// TestNoteOutlivesItsChange is the property the whole anchoring scheme
// exists for: once the work is committed there are no hunks left, but the
// line is still in the file, and the note still belongs to it.
func TestNoteOutlivesItsChange(t *testing.T) {
	ix := Load(writeNotes(t, `{"file":"a.go","anchor":"cache.Invalidate()","short":"the write above can race"}`))

	// While the change is uncommitted the line arrives as an addition...
	if _, ok := ix.Match("a.go", "+\tcache.Invalidate()"); !ok {
		t.Fatal("note did not match its own added line")
	}
	// ...and afterwards as ordinary context. Same note either way.
	if _, ok := ix.Match("a.go", "\tcache.Invalidate()"); !ok {
		t.Fatal("note stopped matching once the line was no longer an addition")
	}
}

// TestLocateFindsTheLineAndReportsARewrite covers the notes index: a note
// points at a line number in the file as it is now, or says the code it
// described is gone.
func TestLocateFindsTheLineAndReportsARewrite(t *testing.T) {
	dir := writeNotes(t,
		`{"file":"a.go","anchor":"cache.Invalidate()","short":"the write above can race"}`,
		`{"file":"a.go","anchor":"cache.Purge()","short":"written about code that has since been rewritten"}`)
	if err := os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("package main\n\nfunc f() {\n\tcache.Invalidate()\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	notes := Load(dir).All()
	if len(notes) != 2 {
		t.Fatalf("expected both notes, got %d", len(notes))
	}

	byAnchor := map[string]Note{}
	for _, n := range notes {
		byAnchor[n.Anchor] = n
	}
	if line, ok := byAnchor["cache.Invalidate()"].Locate(dir); !ok || line != 4 {
		t.Errorf("live note located at line %d (ok=%v), want 4", line, ok)
	}
	if _, ok := byAnchor["cache.Purge()"].Locate(dir); ok {
		t.Error("a note whose code was rewritten must not resolve to a line")
	}
}

// TestLaterNoteSupersedesTheEarlierOne covers what an agent is told to do
// when it changes annotated code: append rather than edit, and the newer
// note is the one that counts.
func TestLaterNoteSupersedesTheEarlierOne(t *testing.T) {
	ix := Load(writeNotes(t,
		`{"file":"a.go","anchor":"retry(3)","short":"first reason"}`,
		`{"file":"a.go","anchor":"retry(3)","short":"the reason it is really there"}`))

	n, ok := ix.Match("a.go", "\tretry(3)")
	if !ok || n.Short != "the reason it is really there" {
		t.Fatalf("expected the later note to win, got %q (ok=%v)", n.Short, ok)
	}
	if all := ix.All(); len(all) != 1 {
		t.Fatalf("the index should list one note per anchor, got %d", len(all))
	}
}

// TestReplacesRetiresTheNoteForRewrittenCode covers the case supersession
// by anchor cannot: once code is rewritten its anchor is gone, so the new
// note has a new anchor, and without naming the old one the old note would
// be listed as stale for ever.
func TestReplacesRetiresTheNoteForRewrittenCode(t *testing.T) {
	ix := Load(writeNotes(t,
		`{"file":"a.go","anchor":"cache.Purge()","short":"old reason"}`,
		`{"file":"b.go","anchor":"cache.Purge()","short":"same code, other file"}`,
		`{"file":"a.go","anchor":"cache.Invalidate()","short":"new reason","replaces":"cache.Purge()"}`))

	if _, ok := ix.Match("a.go", "cache.Purge()"); ok {
		t.Fatal("a replaced note must not be shown")
	}
	if _, ok := ix.Match("b.go", "cache.Purge()"); !ok {
		t.Fatal("replaces is scoped to its own file")
	}
	if got := len(ix.All()); got != 2 {
		t.Fatalf("expected 2 notes in force, got %d", got)
	}
	if ix.Read() != 3 {
		t.Fatalf("Read must count every note written, got %d", ix.Read())
	}
}

// gitWorktrees makes a repository with a second, linked worktree — the
// layout agents work in, where .git is a file rather than a directory.
func gitWorktrees(t *testing.T) (main, linked string) {
	t.Helper()
	base := t.TempDir()
	main, linked = filepath.Join(base, "main"), filepath.Join(base, "linked")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(filepath.Join(main, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "package pkg\n\nfunc f() {\n\tretry(3)\n\treturn\n\treturn\n}\n"
	if err := os.WriteFile(filepath.Join(main, "pkg", "a.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	run(main, "init", "-q", "-b", "main")
	run(main, "add", ".")
	run(main, "commit", "-q", "-m", "init")
	run(main, "worktree", "add", "-q", "-b", "feature", linked)
	return main, linked
}

func TestAddFromALinkedWorktreeIsSeenByEveryWorktree(t *testing.T) {
	main, linked := gitWorktrees(t)

	// Standing in a subdirectory, naming the file relative to it.
	n, line, err := Add(filepath.Join(linked, "pkg"), Note{
		File: "a.go", Anchor: "retry(3)", Short: `retries, not a loop: "flaky" <upstream>`, Agent: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if n.File != "pkg/a.go" || line != 4 || n.Branch != "feature" || n.Time == "" {
		t.Fatalf("note not resolved: %+v line %d", n, line)
	}

	for _, wt := range []string{linked, main} {
		got, ok := Load(wt).Match("pkg/a.go", "\tretry(3)")
		if !ok || got.Short != n.Short {
			t.Fatalf("%s: note not found (ok=%v): %+v", wt, ok, got)
		}
	}
	if out, _ := exec.Command("git", "-C", linked, "status", "--porcelain").Output(); len(out) != 0 {
		t.Fatalf("nothing may be written into the worktree itself: %s", out)
	}
}

func TestAddRefusesWhatWouldFailSilentlyLater(t *testing.T) {
	_, linked := gitWorktrees(t)
	for name, n := range map[string]Note{
		"missing anchor":   {File: "pkg/a.go", Anchor: "retry(4)", Short: "x"},
		"ambiguous anchor": {File: "pkg/a.go", Anchor: "return", Short: "x"},
		"multi-line":       {File: "pkg/a.go", Anchor: "retry(3)\n\treturn", Short: "x"},
		"no such file":     {File: "pkg/b.go", Anchor: "retry(3)", Short: "x"},
		"no reason":        {File: "pkg/a.go", Anchor: "retry(3)"},
		"outside":          {File: "/etc/hostname", Anchor: "a", Short: "x"},
	} {
		if _, _, err := Add(linked, n); err == nil {
			t.Errorf("%s: expected a refusal", name)
		}
	}
	if Load(linked).Read() != 0 {
		t.Fatal("a refused note must not be written")
	}
}

func TestRelativeDataDirIsInsideEachWorktree(t *testing.T) {
	main, linked := gitWorktrees(t)
	store.SetDataDir(".nerdr")
	t.Cleanup(func() { store.SetDataDir("") })

	if _, _, err := Add(linked, Note{File: "pkg/a.go", Anchor: "retry(3)", Short: "kept in the worktree"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(linked, ".nerdr", FileName)); err != nil {
		t.Fatalf("the note should be in the worktree: %v", err)
	}
	if out, _ := exec.Command("git", "-C", linked, "status", "--porcelain").Output(); len(out) != 0 {
		t.Fatalf("the notes folder must be kept out of git: %s", out)
	}
	if n, ok := Load(linked).Match("pkg/a.go", "retry(3)"); !ok || n.Short != "kept in the worktree" {
		t.Fatalf("not read back: %+v", n)
	}
	if Load(main).Read() != 0 {
		t.Fatal("another worktree has notes of its own")
	}
}

// A note written straight into the file, past `nerdr why`, is drawn on the
// terminal all the same: its escape sequences must not reach it.
func TestReadNotesDropsControlCharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "why.jsonl")
	line := `{"file":"a.go","anchor":"x := 1","short":"why \u001b]52;c;aGk=\u0007","detail":"more\u001b[2J\nlines"}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	notes := readNotes(path)
	if len(notes) != 1 {
		t.Fatalf("notes = %+v", notes)
	}
	if n := notes[0]; strings.ContainsAny(n.Short+n.Detail, "\x1b\x07") || n.Detail != "more[2J\nlines" {
		t.Fatalf("note = %+v", n)
	}
}
