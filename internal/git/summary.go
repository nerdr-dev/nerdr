package git

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/proc"
)

var shortStatRe = regexp.MustCompile(`(\d+) files? changed(?:, (\d+) insertions?\(\+\))?(?:, (\d+) deletions?\(-\))?`)

// ShortStat returns a summary of changes in the worktree compared to HEAD
// (staged + unstaged + new files not yet added): number of files,
// insertions and deletions.
func ShortStat(worktreePath string) (files, add, del int) {
	return shortStat(worktreePath, "HEAD")
}

// ShortStatBase is ShortStat against the merge base with base rather than
// HEAD, so a branch's committed work is counted too.
func ShortStatBase(worktreePath, base string) (files, add, del int) {
	return shortStat(worktreePath, "--merge-base", base)
}

func shortStat(worktreePath string, against ...string) (files, add, del int) {
	// git diff leaves out files nobody has added yet, which is most of what
	// an agent creates; they are counted here, every line an insertion.
	files, add = untrackedStat(worktreePath)
	args := append([]string{"diff"}, against...)
	out, err := proc.Output(gitsafe.Command(worktreePath, append(args, "--shortstat")...))
	if err != nil || len(out) == 0 {
		return files, add, 0
	}
	m := shortStatRe.FindStringSubmatch(string(out))
	if m == nil {
		return files, add, 0
	}
	n, _ := strconv.Atoi(m[1])
	a, _ := strconv.Atoi(m[2])
	del, _ = strconv.Atoi(m[3])
	return files + n, add + a, del
}

// LineCount is the lines added to and removed from one file. Binary is set
// for a file git counts no lines in.
type LineCount struct {
	Add, Del int
	Binary   bool
}

// NumStat counts the lines added and removed in each file the worktree
// changes against the given revision arguments, keyed by path relative to
// the worktree; a file not yet added counts every line as added.
func NumStat(worktreePath string, against ...string) map[string]LineCount {
	out := map[string]LineCount{}
	args := append([]string{"diff"}, against...)
	if b, err := proc.Output(gitsafe.Command(worktreePath, append(args, "--numstat", "-z")...)); err == nil {
		// "ADD\tDEL\tPATH\0", or for a rename "ADD\tDEL\t\0FROM\0TO\0";
		// binary files have "-" for both counts.
		fields := strings.Split(string(b), "\x00")
		for i := 0; i < len(fields); i++ {
			parts := strings.SplitN(fields[i], "\t", 3)
			if len(parts) != 3 {
				continue
			}
			p := parts[2]
			if p == "" && i+2 < len(fields) {
				p = fields[i+2]
				i += 2
			}
			add, errA := strconv.Atoi(parts[0])
			del, errD := strconv.Atoi(parts[1])
			out[p] = LineCount{Add: add, Del: del, Binary: errA != nil || errD != nil}
		}
	}
	for _, p := range untrackedFiles(worktreePath) {
		n, binary := fileLines(worktreePath, p)
		out[p] = LineCount{Add: n, Binary: binary}
	}
	return out
}

// untrackedFiles lists the untracked, not ignored files in the worktree.
func untrackedFiles(worktreePath string) []string {
	b, err := proc.Output(gitsafe.Command(worktreePath, "ls-files", "--others", "--exclude-standard", "-z"))
	if err != nil {
		return nil
	}
	var out []string
	for _, p := range strings.Split(string(b), "\x00") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// isBinary is whether data is a binary file by git's own test: a NUL byte
// in its first 8000 bytes.
func isBinary(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0
}

// fileLines counts the lines of file in the worktree, as git diff would
// show them added: a link's one, where it points; a binary file, or one
// that cannot be read, has none.
func fileLines(worktreePath, file string) (lines int, binary bool) {
	text, _, err := readWorktree(worktreePath, file)
	if err != nil {
		return 0, false
	}
	data := []byte(text)
	if isBinary(data) {
		return 0, true
	}
	lines = bytes.Count(data, []byte("\n"))
	if len(data) > 0 && data[len(data)-1] != '\n' {
		lines++
	}
	return lines, false
}

// untrackedStat counts the untracked, not ignored files in the worktree
// and their lines. A binary file counts as a file with no lines, as git
// diff would show it.
func untrackedStat(worktreePath string) (files, lines int) {
	for _, p := range untrackedFiles(worktreePath) {
		files++
		n, _ := fileLines(worktreePath, p)
		lines += n
	}
	return files, lines
}
