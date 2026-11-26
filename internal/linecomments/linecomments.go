// Package linecomments holds the comments the user writes on lines of a
// change in F1, as on a pull request before it is merged: left one by one
// while reading the diff, and sent to the tab's agent together, in one
// message (Prompt), once the reading is done.
//
// A comment is pending until it is sent. It names the line it is on, and
// quotes its code: the code is what finds the line again once the agent
// has changed the file (Locate), and what tells the comment is outdated
// when the code is gone. Comments are kept in one file in nerdr's state
// folder (Open), on the machine the panel runs on: they are the user's,
// not the repository's, and survive a restart of nerdr. Several nerdr
// clients may share the folder, so each change to the file is made under
// its lock, from what the file holds then.
package linecomments

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nerdr-dev/nerdr/internal/clean"
	"github.com/nerdr-dev/nerdr/internal/term"
)

// FileName is the file comments are kept in, in nerdr's state folder.
const FileName = "review-comments.json"

// Limits on a comment: it is drawn in the panel and typed into an agent.
const (
	// MaxCode is the most lines of code a comment quotes; a longer range
	// is quoted from its start.
	MaxCode = 40
	// MaxText is the longest a comment's own text may be.
	MaxText = 8 << 10
)

// Comment is one pending comment on the code.
type Comment struct {
	ID int `json:"id"`
	// Machine is the machine the worktree is on, as nerdr's client names
	// it: "" for this one. Two machines can have a worktree at one path.
	Machine string `json:"machine,omitempty"`
	// Worktree is the worktree the change is in, as the panel names it.
	Worktree string `json:"worktree"`
	// File is relative to the worktree.
	File string `json:"file"`
	// Line is the first line commented on, 1-based, in the file as it is
	// now; 0 for removed lines, which the file no longer has.
	Line int `json:"line,omitempty"`
	// Removed is set when the lines commented on are lines the change
	// removes; OldLine is then where the first is in the file before it.
	Removed bool `json:"removed,omitempty"`
	OldLine int  `json:"old_line,omitempty"`
	// Code is the lines commented on, as they read, without the diff's
	// sign in front.
	Code []string `json:"code"`
	// More is how many lines of the range are left out of Code; see
	// MaxCode. Hash is then of the whole range, for Locate to tell it is
	// all still there.
	More int    `json:"more,omitempty"`
	Hash string `json:"hash,omitempty"`
	Text string `json:"text"`
	Time string `json:"ts,omitempty"`
	// Outdated is set once the code commented on is no longer found in
	// the file: the agent has rewritten it.
	Outdated bool `json:"outdated,omitempty"`
}

// Lines is how many lines c is on.
func (c Comment) Lines() int { return len(c.Code) + c.More }

// Where is c's place, as the message names it: file:line, file:from-to,
// or the file and that the lines are removed ones. It is safe to draw.
func (c Comment) Where() string {
	c.File = clean.Line(c.File)
	switch {
	case c.Removed && c.OldLine > 0:
		if c.Lines() > 1 {
			return fmt.Sprintf("%s, removed lines %d-%d of the old file", c.File, c.OldLine, c.OldLine+c.Lines()-1)
		}
		return fmt.Sprintf("%s, removed line %d of the old file", c.File, c.OldLine)
	case c.Removed:
		if c.Lines() > 1 {
			return c.File + ", removed lines"
		}
		return c.File + ", a removed line"
	case c.Line <= 0:
		return c.File
	case c.Lines() > 1:
		return fmt.Sprintf("%s:%d-%d", c.File, c.Line, c.Line+c.Lines()-1)
	}
	return fmt.Sprintf("%s:%d", c.File, c.Line)
}

// of reports whether c is on worktree of machine.
func (c Comment) of(machine, worktree string) bool {
	return c.Machine == machine && c.Worktree == worktree
}

// sanitize makes c safe to draw and keep: the file is written by nerdr,
// but read back from the disk.
func sanitize(c Comment) (Comment, bool) {
	c.Machine = clean.Line(c.Machine)
	c.Worktree = filepath.Clean(c.Worktree)
	// The path as git has it, to find the file by: made safe where it is
	// shown (Where), not here.
	c.File = filepath.ToSlash(strings.TrimPrefix(c.File, "./"))
	c.Text = strings.TrimSpace(clean.Text(c.Text))
	c.Time = clean.Line(c.Time)
	c.Code = append([]string(nil), c.Code...)
	if len(c.Code) > MaxCode {
		if c.Hash == "" && c.More == 0 {
			c.Hash = hashLines(c.Code)
		}
		c.More += len(c.Code) - MaxCode
		c.Code = c.Code[:MaxCode]
	}
	for i, l := range c.Code {
		// Its indentation kept, tabs and all: it is quoted as code.
		c.Code[i] = strings.ReplaceAll(clean.Text(l), "\n", " ")
	}
	if len(c.Text) > MaxText {
		c.Text = c.Text[:MaxText]
	}
	ok := c.ID > 0 && c.File != "" && c.Text != "" && len(c.Code) > 0 && c.Worktree != "."
	return c, ok
}

// Store is the pending comments of every worktree, kept in a file. One is
// shared by all the panels of a nerdr, so a comment written in one is
// seen in another. Its methods may be called from any goroutine.
type Store struct {
	mu   sync.Mutex
	path string // "" keeps them in memory only
	all  []Comment
	next int
	ver  int
	err  error
	// seen is the file as last read or written, for Refresh to tell
	// another client has changed it.
	seen []byte
}

// Open reads the comments kept in path; "" for a Store that keeps them in
// memory only. A file that is missing or cannot be read is an empty store:
// the comments are a convenience, not a record nerdr depends on.
func Open(path string) *Store {
	s := &Store{path: path, next: 1}
	if path != "" {
		data, err := readFile(path)
		if err == nil {
			err = s.load(data)
		}
		s.err = err
	}
	return s
}

// readFile is the file's content: none when it is not there yet, and an
// error when it is there but cannot be read, which is not taken for none.
func readFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

// load takes the comments in data, the file's content, in place of those
// held; s.mu is held or s not yet shared. A file that is not what nerdr
// writes leaves them as they were, and is an error: written over, the
// comments in it would be lost.
func (s *Store) load(data []byte) error {
	var f fileJSON
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &f); err != nil {
			return fmt.Errorf("%s: %w", s.path, err)
		}
	}
	s.seen = data
	s.all, s.next = nil, max(1, f.Next)
	for _, c := range f.Comments {
		if c, ok := sanitize(c); ok {
			s.all = append(s.all, c)
			s.next = max(s.next, c.ID+1)
		}
	}
	return nil
}

type fileJSON struct {
	// Next is the ID the next comment gets: one is not given twice, even
	// once those before it are gone.
	Next     int       `json:"next,omitempty"`
	Comments []Comment `json:"comments"`
}

// Version changes each time the comments do, for a panel to tell its
// rows are to be drawn again.
func (s *Store) Version() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ver
}

// Err is the last failure to write the file, if any.
func (s *Store) Err() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Refresh reads the file again when another nerdr has changed it. A
// file that cannot be read leaves the comments as they were.
func (s *Store) Refresh() {
	if s == nil || s.path == "" {
		return
	}
	// Read with s.mu held, so what change writes meanwhile is not taken
	// over by an older read.
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := readFile(s.path)
	if err == nil && !bytes.Equal(data, s.seen) {
		if err = s.load(data); err == nil {
			s.ver++
		}
	}
	if err != nil {
		s.err = err
	}
}

// For is the comments on worktree of machine, by file and line.
func (s *Store) For(machine, worktree string) []Comment {
	if s == nil {
		return nil
	}
	worktree = filepath.Clean(worktree)
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Comment
	for _, c := range s.all {
		if c.of(machine, worktree) {
			c.Code = append([]string(nil), c.Code...)
			out = append(out, c)
		}
	}
	Sort(out)
	return out
}

// Sort puts cs in the order of the files and their lines; removed lines,
// which have none, go before the file's others.
func Sort(cs []Comment) {
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].File != cs[j].File {
			return cs[i].File < cs[j].File
		}
		if cs[i].Line != cs[j].Line {
			return cs[i].Line < cs[j].Line
		}
		return cs[i].OldLine < cs[j].OldLine
	})
}

// Add keeps c as a new comment, and returns it as kept.
func (s *Store) Add(c Comment) (Comment, error) {
	if s == nil {
		return c, errors.New("no place for comments")
	}
	if c.Time == "" {
		c.Time = time.Now().UTC().Format(time.RFC3339)
	}
	c.ID = 1 // for sanitize; the real one is given under the lock
	if _, ok := sanitize(c); !ok {
		return c, errors.New("a comment needs a file, code and text")
	}
	var kept Comment
	err := s.change(func(all []Comment, next *int) ([]Comment, bool) {
		c.ID = *next
		*next++
		kept, _ = sanitize(c)
		return append(all, kept), true
	})
	return kept, err
}

// SetText changes the text of comment id.
func (s *Store) SetText(id int, text string) error {
	text = strings.TrimSpace(clean.Text(text))
	return s.each(func(c *Comment) bool {
		if c.ID != id {
			return false
		}
		c.Text = text
		return true
	})
}

// Delete takes comment id away.
func (s *Store) Delete(id int) error { return s.Remove([]int{id}) }

// Remove takes the comments ids away.
func (s *Store) Remove(ids []int) error {
	drop := map[int]bool{}
	for _, id := range ids {
		drop[id] = true
	}
	return s.drop(func(c Comment) bool { return drop[c.ID] })
}

// RemoveSent takes away the comments sent to the agent, by ID, each with
// the text it was sent with: one changed since, in this nerdr or another,
// was not sent as it is, and is kept.
func (s *Store) RemoveSent(sent map[int]string) error {
	return s.drop(func(c Comment) bool {
		text, ok := sent[c.ID]
		return ok && text == c.Text
	})
}

func (s *Store) drop(gone func(Comment) bool) error {
	if s == nil {
		return nil
	}
	return s.change(func(all []Comment, _ *int) ([]Comment, bool) {
		keep := all[:0:0]
		for _, c := range all {
			if !gone(c) {
				keep = append(keep, c)
			}
		}
		return keep, len(keep) != len(all)
	})
}

// Seen is what is known of a file as it is now, for Follow: Src, its
// content, when SrcOK; Old, the lines its diff removes by their numbers in
// the file before the change, when OldOK.
type Seen struct {
	Src   string
	SrcOK bool
	Old   map[int]string
	OldOK bool
}

// Follow finds the comments on worktree of machine's file again in seen:
// each moves to where its code is, or is marked outdated when its code is
// gone. Those on what seen does not know of, the file not read or its
// diff, are left as they are: not read is not gone. It reports whether
// any comment changed.
func (s *Store) Follow(machine, worktree, file string, seen Seen) bool {
	if s == nil {
		return false
	}
	worktree = filepath.Clean(worktree)
	lines := splitLines(seen.Src)
	follow := func(c *Comment) bool {
		if !c.of(machine, worktree) || c.File != file || c.Removed && !seen.OldOK || !c.Removed && !seen.SrcOK {
			return false
		}
		var at int
		var ok bool
		if c.Removed {
			at, ok = LocateOld(*c, seen.Old)
		} else {
			at, ok = Locate(*c, lines)
		}
		was := *c
		c.Outdated = !ok
		switch {
		case ok && c.Removed:
			c.OldLine = at
		case ok:
			c.Line = at
		}
		return was.Outdated != c.Outdated || was.Line != c.Line || was.OldLine != c.OldLine
	}
	// Tried on a copy first: the diff is read far more often than a
	// comment moves, and the file is then neither locked nor written.
	s.mu.Lock()
	moves := false
	for _, c := range s.all {
		moves = follow(&c) || moves
	}
	s.mu.Unlock()
	if !moves {
		return false
	}
	return s.each(follow) == nil
}

// each changes the comments f reports it changed, and writes them out.
func (s *Store) each(f func(*Comment) bool) error {
	return s.change(func(all []Comment, _ *int) ([]Comment, bool) {
		hit := false
		for i := range all {
			if f(&all[i]) {
				hit = true
			}
		}
		return all, hit
	})
}

// change applies f to the comments, and writes them out when it reports
// it changed them. With a file, that is done under the file's lock, to
// what the file holds then: another nerdr may have written it since it
// was read here.
func (s *Store) change(f func(all []Comment, next *int) ([]Comment, bool)) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		all, ok := f(s.all, &s.next)
		if ok {
			s.all = all
			s.ver++
		}
		return nil
	}
	unlock, err := lockFile(s.path + ".lock")
	if err != nil {
		s.err = err
		return err
	}
	defer unlock()
	data, err := readFile(s.path)
	if err == nil && !bytes.Equal(data, s.seen) {
		if err = s.load(data); err == nil {
			s.ver++
		}
	}
	if err != nil {
		// Written over, the comments in it would be lost.
		s.err = err
		return err
	}
	all, ok := f(append([]Comment(nil), s.all...), &s.next)
	if !ok {
		return nil
	}
	data, err = s.write(all)
	s.err = err
	if err != nil {
		return err
	}
	s.all, s.seen = all, data
	s.ver++
	return nil
}

// write puts all in the file whole, through a file beside it renamed over
// it, so a crash leaves the old or the new and never half; it returns
// what it wrote.
func (s *Store) write(all []Comment) ([]byte, error) {
	data, err := json.MarshalIndent(fileJSON{Next: s.next, Comments: all}, "", " ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	dir := filepath.Dir(s.path)
	f, err := os.CreateTemp(dir, ".review-comments-*")
	if err != nil {
		return nil, err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, s.path)
	}
	if err != nil {
		os.Remove(tmp)
		return nil, err
	}
	return data, nil
}

// lockTimeout is how long lockFile waits for another nerdr to let go.
var lockTimeout = 3 * time.Second

// lockFile takes the exclusive lock of the file at path, made if it is
// not there, and returns what lets go of it.
func lockFile(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockTimeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) || time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

func splitLines(src string) []string {
	if src == "" {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// Locate finds c's code in lines, the whole of it, line after line, and
// returns the 1-based line it starts on: where it is found more than
// once, the one nearest where c was.
// Lines are compared without the space around them: an agent that
// reindents the code has not rewritten it. ok is false when the code is
// nowhere in lines whole: a range whose first line is still there but not
// the rest has been rewritten, and is outdated.
func Locate(c Comment, lines []string) (line int, ok bool) {
	if len(c.Code) == 0 {
		return 0, false
	}
	near := c.Line
	best, bestDist := 0, -1
	for i := 0; i+len(c.Code) <= len(lines); i++ {
		whole := true
		for k, code := range c.Code {
			if !Same(lines[i+k], code) {
				whole = false
				break
			}
		}
		if !whole || c.Hash != "" && (i+c.Lines() > len(lines) || hashLines(lines[i:i+c.Lines()]) != c.Hash) {
			continue
		}
		if dist := abs(i + 1 - near); bestDist < 0 || dist < bestDist {
			best, bestDist = i+1, dist
		}
	}
	return best, bestDist >= 0
}

// LocateOld is Locate for a comment on removed lines, in old: the lines
// the diff removes, by their numbers in the file before the change. A
// line it does not have is none of the code's.
func LocateOld(c Comment, old map[int]string) (line int, ok bool) {
	if len(c.Code) == 0 {
		return 0, false
	}
	best, bestDist := 0, -1
	for n, l := range old {
		if !Same(l, c.Code[0]) {
			continue
		}
		whole := true
		for k := 1; k < len(c.Code) && whole; k++ {
			next, there := old[n+k]
			whole = there && Same(next, c.Code[k])
		}
		if whole && c.Hash != "" {
			rng := make([]string, 0, c.Lines())
			for k := range c.Lines() {
				l, there := old[n+k]
				if !there {
					whole = false
					break
				}
				rng = append(rng, l)
			}
			whole = whole && hashLines(rng) == c.Hash
		}
		if !whole {
			continue
		}
		// Ties go to the earlier line, so a map's order changes nothing.
		if dist := abs(n - c.OldLine); bestDist < 0 || dist < bestDist || dist == bestDist && n < best {
			best, bestDist = n, dist
		}
	}
	return best, bestDist >= 0
}

// Same reports whether two lines are the same code, as Locate compares
// them: without the space around them, and as the diff pane shows them, so
// a line as the file has it is the same as the line as a comment quotes
// it from the pane (see term.Printable).
func Same(a, b string) bool { return norm(a) == norm(b) }

func norm(l string) string { return strings.TrimSpace(clean.Line(term.Printable(l))) }

// hashLines is a hash of lines as Locate compares them; see Same.
func hashLines(lines []string) string {
	h := sha256.New()
	for _, l := range lines {
		h.Write([]byte(norm(l)))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// Prompt is the message for the agent with the comments cs, numbered,
// each with where it is, the code it is on, and what it says. against says
// what the change is compared with, as git would be asked for it.
func Prompt(cs []Comment, against string) string {
	var b strings.Builder
	n := "a comment"
	if len(cs) > 1 {
		n = fmt.Sprintf("%d comments", len(cs))
	}
	fmt.Fprintf(&b, "I have read through the change%s and left %s on it, as in a review before merging. ", against, n)
	b.WriteString("Go through all of them, then change the code as they ask. Where you disagree with one, or it is unclear, say so in your answer instead of changing the code.")
	for i, c := range cs {
		fmt.Fprintf(&b, "\n\n%d. %s", i+1, c.Where())
		if c.Outdated {
			b.WriteString(" (outdated: this code is no longer in the file as it was when I commented)")
		}
		fence := "```"
		for _, l := range c.Code {
			if strings.Contains(l, "```") {
				fence = "~~~"
			}
		}
		b.WriteString("\n" + fence + "\n")
		for _, l := range c.Code {
			b.WriteString(l + "\n")
		}
		if c.More > 0 {
			fmt.Fprintf(&b, "… (%d more lines)\n", c.More)
		}
		b.WriteString(fence + "\n")
		b.WriteString(c.Text)
	}
	return b.String()
}
