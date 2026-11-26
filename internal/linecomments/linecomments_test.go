package linecomments

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocateFindsTheWholeRangeNearestWhereItWas(t *testing.T) {
	c := Comment{Line: 3, Code: []string{"\tx := 1", "\treturn x"}}
	cases := []struct {
		name  string
		lines []string
		want  int
		ok    bool
	}{
		{"unmoved", []string{"a", "b", "\tx := 1", "\treturn x"}, 3, true},
		{"moved down", []string{"new", "new", "a", "b", "\tx := 1", "\treturn x"}, 5, true},
		{"reindented", []string{"x := 1", "return x"}, 1, true},
		{"only the whole range is found",
			[]string{"x", "x", "\tx := 1", "\tother", "", "", "", "\tx := 1", "\treturn x"}, 8, true},
		{"of two, the nearer", []string{"\tx := 1", "\treturn x", "\tx := 1", "\treturn x"}, 3, true},
		{"its first line left, the rest rewritten", []string{"a", "b", "\tx := 1", "\treturn y"}, 0, false},
		{"its last line cut off", []string{"a", "b", "\tx := 1"}, 0, false},
		{"gone", []string{"a", "b", "\ty := 2"}, 0, false},
	}
	for _, tc := range cases {
		got, ok := Locate(c, tc.lines)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: Locate = %d, %v; want %d, %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
	if at, ok := Locate(Comment{Line: 5, Code: []string{""}}, []string{"", "a", "", "b", "c", ""}); !ok || at != 6 {
		t.Errorf("a blank line stays at the nearest blank line: %d, %v", at, ok)
	}
}

func TestLocateOldGoesByTheOldFilesNumbers(t *testing.T) {
	old := map[int]string{3: "return nil", 9: "return nil", 10: "}", 1000000: "end"}
	second := Comment{Removed: true, OldLine: 9, Code: []string{"return nil"}}
	if at, ok := LocateOld(second, old); !ok || at != 9 {
		t.Errorf("the second of two removed lines alike: %d, %v", at, ok)
	}
	second.OldLine = 6 // as far from both: the earlier
	if at, ok := LocateOld(second, old); !ok || at != 3 {
		t.Errorf("a tie: %d, %v", at, ok)
	}
	pair := Comment{Removed: true, OldLine: 3, Code: []string{"return nil", "}"}}
	if at, ok := LocateOld(pair, old); !ok || at != 9 {
		t.Errorf("only the whole range: %d, %v", at, ok)
	}
	if _, ok := LocateOld(Comment{Removed: true, Code: []string{"return nil", "x"}}, old); ok {
		t.Error("lines the diff does not have are none of the code's")
	}
}

func TestStoreKeepsCommentsAcrossARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", FileName)
	s := Open(path)
	a, err := s.Add(Comment{Worktree: "/wt/", File: "./a.go", Line: 2, Code: []string{"x := 1"}, Text: " use a constant "})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == 0 || a.File != "a.go" || a.Text != "use a constant" || a.Time == "" {
		t.Fatalf("kept as %#v", a)
	}
	b, _ := s.Add(Comment{Worktree: "/wt", File: "a.go", Line: 1, Code: []string{"package a"}, Text: "name"})
	other, _ := s.Add(Comment{Worktree: "/other", File: "b.go", Line: 1, Code: []string{"x"}, Text: "y"})
	if _, err := s.Add(Comment{Worktree: "/wt", File: "a.go", Code: []string{"x"}}); err == nil {
		t.Error("a comment with no text should not be kept")
	}
	if err := s.SetText(a.ID, "a constant, please"); err != nil {
		t.Fatal(err)
	}

	s = Open(path)
	got := s.For("", "/wt")
	if len(got) != 2 || got[0].ID != b.ID || got[1].ID != a.ID || got[1].Text != "a constant, please" {
		t.Fatalf("after reopening, by line: %#v", got)
	}
	if err := s.Delete(b.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove([]int{a.ID, other.ID}); err != nil {
		t.Fatal(err)
	}
	s = Open(path)
	if n := len(s.For("", "/wt")) + len(s.For("", "/other")); n != 0 {
		t.Fatalf("%d comments left", n)
	}
	c, _ := s.Add(Comment{Worktree: "/wt", File: "a.go", Code: []string{"x"}, Text: "t"})
	if c.ID <= other.ID {
		t.Errorf("ids are not given again: %d after %d", c.ID, other.ID)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".review-comments-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

// TestTwoClientsOnOneFileLoseNothing covers two nerdrs sharing the state
// folder: what one writes is not written over by the other, and ids are
// not given twice.
func TestTwoClientsOnOneFileLoseNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	one, two := Open(path), Open(path)
	a, _ := one.Add(Comment{Worktree: "/a", File: "a.go", Line: 1, Code: []string{"x"}, Text: "from one"})
	b, _ := two.Add(Comment{Worktree: "/b", File: "b.go", Line: 1, Code: []string{"y"}, Text: "from two"})
	if a.ID == b.ID {
		t.Fatalf("both got id %d", a.ID)
	}
	if err := one.SetText(a.ID, "from one, changed"); err != nil {
		t.Fatal(err)
	}
	s := Open(path)
	if got := s.For("", "/a"); len(got) != 1 || got[0].Text != "from one, changed" {
		t.Errorf("one's: %#v", got)
	}
	if got := s.For("", "/b"); len(got) != 1 || got[0].Text != "from two" {
		t.Errorf("two's: %#v", got)
	}
	// two sees one's once it refreshes, or changes the file itself.
	v := two.Version()
	two.Refresh()
	if two.Version() == v || len(two.For("", "/a")) != 1 {
		t.Error("Refresh should read what the other wrote")
	}
	v = two.Version()
	two.Refresh()
	if two.Version() != v {
		t.Error("nothing new: the version stays")
	}
}

// TestOnlyWhatWasSentIsTakenAway covers a comment changed after the
// message with it was made, in this nerdr or another: it is kept.
func TestOnlyWhatWasSentIsTakenAway(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	a, b := Open(path), Open(path)
	one, _ := a.Add(Comment{Worktree: "/wt", File: "a.go", Line: 1, Code: []string{"x"}, Text: "one"})
	two, _ := a.Add(Comment{Worktree: "/wt", File: "a.go", Line: 2, Code: []string{"y"}, Text: "two"})
	sent := map[int]string{one.ID: one.Text, two.ID: two.Text}
	if err := b.SetText(two.ID, "two, changed"); err != nil {
		t.Fatal(err)
	}
	if err := a.RemoveSent(sent); err != nil {
		t.Fatal(err)
	}
	if got := Open(path).For("", "/wt"); len(got) != 1 || got[0].Text != "two, changed" {
		t.Fatalf("left: %#v", got)
	}
}

// TestAFileThatCannotBeReadIsNotWrittenOver covers the comments file
// there but unreadable: a change fails rather than putting an empty list
// with one comment in its place, and Refresh keeps what it had.
func TestAFileThatCannotBeReadIsNotWrittenOver(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any file")
	}
	path := filepath.Join(t.TempDir(), FileName)
	s := Open(path)
	if _, err := s.Add(Comment{Worktree: "/wt", File: "a.go", Line: 1, Code: []string{"x"}, Text: "kept"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(path, 0o600)
	s.Refresh()
	if got := s.For("", "/wt"); len(got) != 1 {
		t.Fatalf("Refresh should keep what it had: %#v", got)
	}
	if _, err := s.Add(Comment{Worktree: "/wt", File: "a.go", Line: 2, Code: []string{"y"}, Text: "new"}); err == nil {
		t.Fatal("Add should fail on a file it cannot read")
	}
	os.Chmod(path, 0o600)
	if got := Open(path).For("", "/wt"); len(got) != 1 || got[0].Text != "kept" {
		t.Fatalf("the file should be as it was: %#v", got)
	}
}

// TestARefreshKeepsWhatItHadOverABadFile covers the file spoilt under a
// store that had read it: it keeps its comments, and changes nothing.
func TestARefreshKeepsWhatItHadOverABadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	s := Open(path)
	s.Add(Comment{Worktree: "/wt", File: "a.go", Line: 1, Code: []string{"x"}, Text: "kept"})
	os.WriteFile(path, []byte("{spoilt"), 0o600)
	s.Refresh()
	if got := s.For("", "/wt"); len(got) != 1 || s.Err() == nil {
		t.Fatalf("after a refresh: %#v, %v", got, s.Err())
	}
	if err := s.SetText(1, "changed"); err == nil {
		t.Fatal("a change should not write over the spoilt file")
	}
}

// TestALongRangeIsCheckedWhole covers a comment on more lines than it
// quotes: a change to a line past the quote still makes it outdated.
func TestALongRangeIsCheckedWhole(t *testing.T) {
	var code []string
	for i := range MaxCode + 1 {
		code = append(code, "line"+strings.Repeat("x", i))
	}
	s := Open("")
	c, _ := s.Add(Comment{Worktree: "/wt", File: "a.go", Line: 1, Code: code, Text: "all of it"})
	if len(c.Code) != MaxCode || c.More != 1 || c.Hash == "" {
		t.Fatalf("kept as %d lines, %d more, hash %q", len(c.Code), c.More, c.Hash)
	}
	src := strings.Join(code, "\n") + "\n"
	s.Follow("", "/wt", "a.go", Seen{Src: src, SrcOK: true})
	if s.For("", "/wt")[0].Outdated {
		t.Fatal("the whole range is there")
	}
	changed := append(append([]string(nil), code[:MaxCode]...), "rewritten")
	s.Follow("", "/wt", "a.go", Seen{Src: strings.Join(changed, "\n") + "\n", SrcOK: true})
	if !s.For("", "/wt")[0].Outdated {
		t.Fatal("its last line, past the quote, was rewritten")
	}
	old := map[int]string{}
	for i, l := range code {
		old[10+i] = l
	}
	r := Comment{Removed: true, OldLine: 10, Code: code[:MaxCode], More: 1, Hash: c.Hash}
	if at, ok := LocateOld(r, old); !ok || at != 10 {
		t.Errorf("removed, whole: %d, %v", at, ok)
	}
	old[10+MaxCode] = "rewritten"
	if _, ok := LocateOld(r, old); ok {
		t.Error("removed, its last line not the same")
	}
}

// TestFollowLeavesWhatWasNotRead covers a file that could not be read: not
// known to be gone, its comments are not outdated.
func TestFollowLeavesWhatWasNotRead(t *testing.T) {
	s := Open("")
	s.Add(Comment{Worktree: "/wt", File: "a.go", Line: 1, Code: []string{"x"}, Text: "t"})
	s.Add(Comment{Worktree: "/wt", File: "a.go", Removed: true, OldLine: 1, Code: []string{"y"}, Text: "t"})
	if s.Follow("", "/wt", "a.go", Seen{}) {
		t.Fatal("nothing read, nothing changes")
	}
	for _, c := range s.For("", "/wt") {
		if c.Outdated {
			t.Errorf("outdated without being read: %#v", c)
		}
	}
}

// TestCommentsAreByMachine covers two machines with a worktree at the
// same path: each has its own comments.
func TestCommentsAreByMachine(t *testing.T) {
	s := Open("")
	s.Add(Comment{Worktree: "/wt", File: "a.go", Line: 1, Code: []string{"x"}, Text: "here"})
	s.Add(Comment{Machine: "box", Worktree: "/wt", File: "a.go", Line: 1, Code: []string{"x"}, Text: "on box"})
	if got := s.For("", "/wt"); len(got) != 1 || got[0].Text != "here" {
		t.Errorf("this machine's: %#v", got)
	}
	if got := s.For("box", "/wt"); len(got) != 1 || got[0].Text != "on box" {
		t.Errorf("box's: %#v", got)
	}
	s.Follow("box", "/wt", "a.go", Seen{Src: "gone\n", SrcOK: true, OldOK: true})
	if s.For("", "/wt")[0].Outdated || !s.For("box", "/wt")[0].Outdated {
		t.Error("Follow is of one machine's file")
	}
}

func TestOpenSurvivesABadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := Open(path)
	if len(s.For("", "/wt")) != 0 || s.Err() == nil {
		t.Fatal("a bad file should be an empty store, with its error")
	}
	// A change does not put one comment in place of what the file held.
	if _, err := s.Add(Comment{Worktree: "/wt", File: "a.go", Line: 1, Code: []string{"x"}, Text: "t"}); err == nil {
		t.Fatal("Add should fail on a file it cannot make out")
	}
	if data, _ := os.ReadFile(path); string(data) != "{not json" {
		t.Fatalf("the file should be left as it was: %q", data)
	}
	data := `{"comments":[{"id":3,"worktree":"/wt","file":"a.go","code":["x\u001b[2J"],"text":"hi\u001b]0;t\u0007"},{"id":4,"worktree":"/wt","file":"","code":["x"],"text":"no file"}]}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	got := Open(path).For("", "/wt")
	if len(got) != 1 || strings.ContainsRune(got[0].Code[0], 0x1b) || strings.ContainsRune(got[0].Text, 0x1b) {
		t.Fatalf("read as %#v", got)
	}
}

func TestFollowMovesCommentsAndMarksTheGoneOutdated(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	s := Open(path)
	moved, _ := s.Add(Comment{Worktree: "/wt", File: "a.go", Line: 2, Code: []string{"keep()"}, Text: "t"})
	gone, _ := s.Add(Comment{Worktree: "/wt", File: "a.go", Line: 3, Code: []string{"drop()"}, Text: "t"})
	removed, _ := s.Add(Comment{Worktree: "/wt", File: "a.go", Removed: true, OldLine: 4, Code: []string{"old()"}, Text: "t"})
	elsewhere, _ := s.Add(Comment{Worktree: "/wt", File: "b.go", Line: 1, Code: []string{"keep()"}, Text: "t"})
	v := s.Version()

	if !s.Follow("", "/wt", "a.go", Seen{Src: "new()\nnew()\nnew()\nkeep()\n", SrcOK: true, Old: map[int]string{2: "old()"}, OldOK: true}) {
		t.Fatal("Follow should report the comments it changed")
	}
	if s.Version() == v {
		t.Error("the version should change with them")
	}
	byID := func() map[int]Comment {
		out := map[int]Comment{}
		for _, c := range Open(path).For("", "/wt") {
			out[c.ID] = c
		}
		return out
	}
	got := byID()
	if c := got[moved.ID]; c.Line != 4 || c.Outdated {
		t.Errorf("moved: %#v", c)
	}
	if c := got[gone.ID]; !c.Outdated || c.Line != 3 {
		t.Errorf("gone: %#v", c)
	}
	if c := got[removed.ID]; c.Outdated || c.OldLine != 2 {
		t.Errorf("a removed line still in the diff, moved on the old side: %#v", c)
	}
	if c := got[elsewhere.ID]; c.Line != 1 || c.Outdated {
		t.Errorf("another file's: %#v", c)
	}
	if s.Follow("", "/wt", "a.go", Seen{Src: "new()\nnew()\nnew()\nkeep()\n", SrcOK: true, Old: map[int]string{2: "old()"}, OldOK: true}) {
		t.Error("nothing changed the second time")
	}
	// Without the diff's removed lines, as the whole file shows it, the
	// comment on one is left as it is.
	s.Follow("", "/wt", "a.go", Seen{Src: "drop()\n", SrcOK: true})
	got = byID()
	if c := got[gone.ID]; c.Outdated || c.Line != 1 {
		t.Errorf("found again: %#v", c)
	}
	if c := got[removed.ID]; c.Outdated {
		t.Errorf("not looked for, not outdated: %#v", c)
	}
	s.Follow("", "/wt", "a.go", Seen{Src: "drop()\n", SrcOK: true, OldOK: true})
	if c := byID()[removed.ID]; !c.Outdated {
		t.Errorf("a removed line no longer in the diff: %#v", c)
	}
}

func TestPromptQuotesEachCommentWithItsPlace(t *testing.T) {
	cs := []Comment{
		{File: "a.go", Line: 12, Code: []string{"x := clamp(v, 0, 9)"}, Text: "why 9?"},
		{File: "a.go", Line: 20, Code: []string{"a", "b"}, More: 1, Text: "fold these"},
		{File: "b.go", Removed: true, OldLine: 7, Code: []string{"keep()"}, Text: "this is still needed"},
		{File: "c.go", Line: 3, Outdated: true, Code: []string{"s := \"```\""}, Text: "gone"},
	}
	got := Prompt(cs, " (git diff --merge-base main)")
	for _, want := range []string{
		"read through the change (git diff --merge-base main) and left 4 comments",
		"1. a.go:12\n```\nx := clamp(v, 0, 9)\n```\nwhy 9?",
		"2. a.go:20-22\n```\na\nb\n… (1 more lines)\n```\nfold these",
		"3. b.go, removed line 7 of the old file\n```\nkeep()\n```\nthis is still needed",
		"4. c.go:3 (outdated",
		"~~~\ns := \"```\"\n~~~\ngone",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt lacks %q:\n%s", want, got)
		}
	}
	if one := Prompt(cs[:1], ""); !strings.Contains(one, "left a comment on it") {
		t.Errorf("one comment: %s", one)
	}
}
