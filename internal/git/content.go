package git

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"nerdr/internal/gitsafe"
	"nerdr/internal/proc"
)

// NewFileContent returns the worktree content of an untracked file,
// formatted as a synthetic unified diff (every line added). Plain `git
// diff` never shows untracked files, so this is the only way to display
// their content through the same diff-rendering pipeline.
func NewFileContent(worktreePath, file string) (string, error) {
	data, link, err := readWorktree(worktreePath, file)
	if err != nil {
		return fmt.Sprintf("(could not find %s)\n", file), nil
	}
	if link {
		// As git shows a link: where it points, not what is there.
		var b strings.Builder
		fmt.Fprintf(&b, "diff --git a/%s b/%s\n", file, file)
		b.WriteString("new file mode 120000\n")
		b.WriteString("--- /dev/null\n")
		fmt.Fprintf(&b, "+++ b/%s\n", file)
		b.WriteString("@@ -0,0 +1 @@\n")
		b.WriteString("+" + data + "\n")
		b.WriteString("\\ No newline at end of file\n")
		return b.String(), nil
	}
	if isBinary([]byte(data)) {
		// What git diff says of a binary file, rather than its bytes: they
		// are not text, and the control codes among them move the
		// terminal's cursor and scramble the screen around the pane.
		return fmt.Sprintf("diff --git a/%s b/%s\nnew file mode 100644\nBinary files /dev/null and b/%s differ\n", file, file, file), nil
	}
	lines := splitLines(data)
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\n", file, file)
	b.WriteString("new file mode 100644\n")
	b.WriteString("--- /dev/null\n")
	fmt.Fprintf(&b, "+++ b/%s\n", file)
	fmt.Fprintf(&b, "@@ -0,0 +1,%d @@\n", len(lines))
	for _, l := range lines {
		b.WriteString("+" + l + "\n")
	}
	return b.String(), nil
}

// ReadRefFile returns a file's raw content as it existed at ref.
func ReadRefFile(worktreePath, ref, file string) (string, error) {
	out, err := proc.Output(gitsafe.Command(worktreePath, "show", ref+":"+file))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// ReadWorktreeFile returns a file's raw current content from disk; for a
// link, where it points, as git keeps it.
func ReadWorktreeFile(worktreePath, file string) (string, error) {
	data, _, err := readWorktree(worktreePath, file)
	return data, err
}

// readWorktree is ReadWorktreeFile, and whether file is a link. The
// worktree is an agent's, and nerdr reads it outside any sandbox the agent
// is in, and shows what it reads, in walkthrough pages too: a link is not
// followed, nor a folder that leads out of the worktree, and what is not a
// regular file (a FIFO would wait for ever) is not read.
func readWorktree(worktreePath, file string) (data string, link bool, err error) {
	root, err := os.OpenRoot(worktreePath)
	if err != nil {
		return "", false, err
	}
	defer root.Close()
	rel := filepath.FromSlash(file)
	info, err := root.Lstat(rel)
	switch {
	case err != nil:
		return "", false, err
	case info.Mode()&fs.ModeSymlink != 0:
		to, err := root.Readlink(rel)
		return to, true, err
	case !info.Mode().IsRegular():
		return "", false, fmt.Errorf("%s is not a regular file", file)
	}
	f, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !os.SameFile(st, info) {
		return "", false, fmt.Errorf("%s changed while it was read", file)
	}
	b, err := io.ReadAll(f)
	return string(b), false, err
}

// OldFileContent returns the file's content as it existed at ref, formatted
// as plain (unmarked) context lines through the diff-rendering pipeline, so
// a reviewer can read the pre-change version in isolation.
func OldFileContent(worktreePath, ref, file string) (string, error) {
	raw, err := ReadRefFile(worktreePath, ref, file)
	if err != nil {
		return fmt.Sprintf("(%s does not exist in %s)\n", file, ref), nil
	}
	if isBinary([]byte(raw)) {
		return fmt.Sprintf("(%s is a binary file)\n", file), nil
	}
	lines := splitLines(raw)
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\n", file, file)
	fmt.Fprintf(&b, "--- a/%s (%s)\n", file, ref)
	b.WriteString("+++ /dev/null\n")
	// Numbered from 1 on the "new" side too: that is the side lines are
	// numbered, highlighted and opened by, and here it holds the old file.
	fmt.Fprintf(&b, "@@ -1,%d +1,%d @@\n", len(lines), len(lines))
	for _, l := range lines {
		b.WriteString(" " + l + "\n")
	}
	return b.String(), nil
}

// FullNewFileWithMarkers returns the current full content of file,
// formatted as a synthetic unified diff so that lines added or modified per
// diffText (a normal unified diff for the same file) are marked, while
// unchanged lines render as plain context. This lets a reviewer read the
// whole file in place without losing the change highlighting.
func FullNewFileWithMarkers(worktreePath, file, diffText string) (string, error) {
	data, err := ReadWorktreeFile(worktreePath, file)
	if err != nil {
		return "(file is not in the worktree — deleted?)\n", nil
	}
	if isBinary([]byte(data)) {
		return fmt.Sprintf("(%s is a binary file)\n", file), nil
	}
	lines := splitLines(data)
	changed := changedNewLines(diffText)

	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\n", file, file)
	fmt.Fprintf(&b, "--- a/%s\n", file)
	fmt.Fprintf(&b, "+++ b/%s\n", file)
	fmt.Fprintf(&b, "@@ -1,%d +1,%d @@\n", len(lines), len(lines))
	for i, l := range lines {
		if changed[i+1] {
			b.WriteString("+" + l + "\n")
		} else {
			b.WriteString(" " + l + "\n")
		}
	}
	return b.String(), nil
}

// splitLines splits on "\n", dropping a single trailing empty element caused
// by a final newline so a synthetic diff doesn't render a spurious blank line.
func splitLines(s string) []string {
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

var hunkHeaderRe = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// DiffLine is how one line of a unified diff reads.
type DiffLine struct {
	// Kind is 'h' for a header (or a "\ No newline" marker), '@' for a hunk
	// header, and '+', '-' or ' ' for a line of the hunk.
	Kind byte
	// NewLine is the 1-based line number on the new side for '+' and ' '
	// lines, and 0 otherwise.
	NewLine int
}

// ParseDiff classifies each line of diff, split on "\n". Inside a hunk
// every line is taken by its first character alone, since the code itself
// can start with "++" or "--" (an added "++i", a removed SQL comment); the
// "---"/"+++" file headers only come before the first hunk of a file.
func ParseDiff(diff string) []DiffLine {
	raw := strings.Split(diff, "\n")
	out := make([]DiffLine, len(raw))
	inHunk := false
	newLine := 0
	for i, l := range raw {
		var d DiffLine
		switch {
		case strings.HasPrefix(l, "@@"):
			d.Kind = '@'
			inHunk = true
			if m := hunkHeaderRe.FindStringSubmatch(l); m != nil {
				newLine, _ = strconv.Atoi(m[1])
			}
		case strings.HasPrefix(l, "diff "):
			d.Kind = 'h'
			inHunk = false
		case strings.HasPrefix(l, "\\"):
			d.Kind = 'h'
		case !inHunk:
			// A file header, or a message standing in for a diff.
			d.Kind = ' '
			for _, p := range []string{"---", "+++", "index ", "new file", "deleted file"} {
				if strings.HasPrefix(l, p) {
					d.Kind = 'h'
				}
			}
		case strings.HasPrefix(l, "+"):
			d.Kind, d.NewLine = '+', newLine
			newLine++
		case strings.HasPrefix(l, "-"):
			d.Kind = '-'
		default:
			d.Kind, d.NewLine = ' ', newLine
			newLine++
		}
		out[i] = d
	}
	return out
}

// changedNewLines parses a unified diff and returns the set of new-file
// (1-based) line numbers that were added.
func changedNewLines(diffText string) map[int]bool {
	changed := map[int]bool{}
	for _, d := range ParseDiff(diffText) {
		if d.Kind == '+' {
			changed[d.NewLine] = true
		}
	}
	return changed
}
