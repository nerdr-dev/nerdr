package forge

import (
	"regexp"
	"strings"
)

// CondenseLog keeps the part of a job log that says what went wrong. A
// full job log is hundreds of kilobytes, and neither end of it is the
// answer: the head is runner provisioning, and the tail is post-job
// cleanup — checking out, unsetting git credentials — which runs *after*
// the failure. What matters is the output leading up to each line the
// runner marked as an error (marker: "##[error]" on GitHub Actions,
// "ERROR: " on a GitLab runner), so that is the window kept.
func CondenseLog(log string, limit int, marker string) string {
	if limit <= 0 {
		limit = 8000
	}
	lines := strings.Split(strings.ReplaceAll(log, "\r\n", "\n"), "\n")

	// Lines of context before an error marker. Enough to hold a compiler's
	// or a test runner's output, short enough not to reach back into
	// dependency downloads.
	const context = 80

	var windows [][2]int
	for i, line := range lines {
		if !strings.Contains(line, marker) {
			continue
		}
		start := max(0, i-context)
		if n := len(windows); n > 0 && start <= windows[n-1][1]+1 {
			windows[n-1][1] = i // overlaps the previous window: merge
			continue
		}
		windows = append(windows, [2]int{start, i})
	}
	if len(windows) == 0 {
		// Nothing was marked; the tail is the best guess left.
		return TailOf(log, limit)
	}

	var b strings.Builder
	for i, w := range windows {
		if i > 0 {
			b.WriteString("\n…\n")
		}
		b.WriteString(strings.Join(lines[w[0]:w[1]+1], "\n"))
	}
	// Later errors are usually the informative ones (the first may be a
	// tool warning), so trim from the front.
	return TailOf(b.String(), limit)
}

// TailOf is the end of s, at most limit bytes of it, from a whole line.
func TailOf(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	s = s[len(s)-limit:]
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:] // start on a whole line
	}
	return "…\n" + s
}

// ansiRe is a terminal's colour and cursor codes, which a GitLab job log
// is full of.
var ansiRe = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")

// StripANSI is s without terminal codes, and without the section markers
// a GitLab runner puts on lines of its own ("section_start:…").
func StripANSI(s string) string {
	s = ansiRe.ReplaceAllString(s, "")
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if i := strings.Index(line, "section_"); i >= 0 && (strings.HasPrefix(line[i:], "section_start:") || strings.HasPrefix(line[i:], "section_end:")) {
			// section_start:1700000000:name[collapsed=true]\r<the header>
			if j := strings.LastIndexByte(line, '\r'); j > i {
				line = line[:i] + line[j+1:]
			} else {
				continue
			}
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimSuffix(b.String(), "\n")
}
