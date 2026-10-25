package analysis

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Symbol is a definition found in a source file.
type Symbol struct {
	Name string
	Kind string
}

// Ref is a reference to an identifier in a specific file.
type Ref struct {
	File string
	Line int
}

var symbolRe = regexp.MustCompile(`(?m)^\s*export\s+(?:default\s+)?(?:async\s+)?(function|class|const|let|var|interface|type|enum)\s+([A-Za-z_$][\w$]*)`)

var defaultRe = regexp.MustCompile(`(?m)^\s*export\s+default\s+([A-Za-z_$][\w$]*)`)

var identRe = regexp.MustCompile(`[A-Za-z_$][\w$]*`)

type refEntry struct {
	file int32
	line int32
}

// ParseSymbolDecl reports whether line is an exported top-level definition
// (matching the same pattern Symbols uses) and returns its name and kind.
// It is exported so callers can match individual diff lines (with the
// leading "+"/"-" stripped) against the same rule.
func ParseSymbolDecl(line string) (name, kind string, ok bool) {
	if m := symbolRe.FindStringSubmatch(line); m != nil {
		return m[2], m[1], true
	}
	if m := defaultRe.FindStringSubmatch(line); m != nil {
		return m[1], "default", true
	}
	return "", "", false
}

// Symbols extracts exported top-level definitions from targetRel (slash path
// relative to Root). Exported symbols are the ones with meaningful
// cross-file references.
func (p *Project) Symbols(targetRel string) []Symbol {
	abs := filepath.Join(p.Root, filepath.FromSlash(targetRel))
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil
	}
	src := string(data)
	var out []Symbol
	seen := map[string]bool{}
	for _, m := range symbolRe.FindAllStringSubmatch(src, -1) {
		if !seen[m[2]] {
			seen[m[2]] = true
			out = append(out, Symbol{Name: m[2], Kind: m[1]})
		}
	}
	for _, m := range defaultRe.FindAllStringSubmatch(src, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, Symbol{Name: m[1], Kind: "default"})
		}
	}
	return out
}

// References returns the files and lines where name occurs, excluding the file
// exceptRel (the symbol's own definition file).
func (p *Project) References(name, exceptRel string) []Ref {
	p.buildWordIndex()
	entries := p.wordIndex[name]
	except := indexOf(p.files, normalize(exceptRel))
	out := make([]Ref, 0, len(entries))
	for _, e := range entries {
		if int(e.file) == except {
			continue
		}
		out = append(out, Ref{File: p.files[e.file], Line: int(e.line)})
	}
	return out
}

func (p *Project) buildWordIndex() {
	if p.wordBuilt {
		return
	}
	p.wordBuilt = true
	p.wordIndex = map[string][]refEntry{}
	for i, file := range p.files {
		data, err := os.ReadFile(filepath.Join(p.Root, filepath.FromSlash(file)))
		if err != nil {
			continue
		}
		if len(data) > 512*1024 {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for ln, line := range lines {
			for _, m := range identRe.FindAllString(line, -1) {
				p.wordIndex[m] = append(p.wordIndex[m], refEntry{file: int32(i), line: int32(ln + 1)})
			}
		}
	}
}

func indexOf(files []string, target string) int {
	for i, f := range files {
		if f == target {
			return i
		}
	}
	return -1
}
