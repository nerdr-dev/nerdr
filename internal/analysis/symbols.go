package analysis

import (
	"errors"
	"io/fs"
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

var symbolRe = regexp.MustCompile(`(?m)^\s*export\s+(?:default\s+)?(?:async\s+|abstract\s+)?(function|class|const|let|var|interface|type|enum)\s+([A-Za-z_$][\w$]*)`)

var defaultRe = regexp.MustCompile(`(?m)^\s*export\s+default\s+([A-Za-z_$][\w$]*)`)

// declKeywords are the words that may follow "export default" to begin a
// declaration rather than name what is exported: "export default function
// () {}" exports nothing called "function".
var declKeywords = map[string]bool{
	"function": true, "async": true, "class": true, "abstract": true, "interface": true,
	"type": true, "enum": true, "const": true, "let": true, "var": true,
}

// defaultName is the name "export default" exports in m, a defaultRe
// match, or "" when the word after it begins a declaration.
func defaultName(m []string) string {
	if declKeywords[m[1]] {
		return ""
	}
	return m[1]
}

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
	if m := defaultRe.FindStringSubmatch(line); m != nil && defaultName(m) != "" {
		return m[1], "default", true
	}
	return "", "", false
}

// Symbols extracts exported top-level definitions from targetRel (slash path
// relative to Root). Exported symbols are the ones with meaningful
// cross-file references.
func (p *Project) Symbols(targetRel string) []Symbol {
	// As ImportsOf: only a source file the project reads has definitions
	// of the kind looked for, and any other (a lock file, a Go file) is
	// not read at all.
	if !sourceExts[filepath.Ext(targetRel)] {
		return nil
	}
	data, err := p.readSource(targetRel)
	if errors.Is(err, fs.ErrNotExist) {
		return nil // deleted: it defines nothing now
	}
	if err != nil {
		p.markIncomplete("source files unreadable or over size limit")
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
		if defaultName(m) != "" && !seen[m[1]] {
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
	p.buildWordIndexLimited(maxIndexBytes, maxIndexRefs, maxIndexWords)
}

func (p *Project) buildWordIndexLimited(byteLimit, refLimit, wordLimit int) {
	if p.wordBuilt {
		return
	}
	p.wordBuilt = true
	p.wordIndex = map[string][]refEntry{}
	bytesRead, refs := 0, 0
	// One root for the whole walk, not one opened for every file.
	root, err := os.OpenRoot(p.Root)
	if err != nil {
		p.markIncomplete("source files unreadable or over size limit")
		return
	}
	defer root.Close()
	for i, file := range p.files {
		data, err := readSourceIn(root, file)
		if err != nil {
			p.markIncomplete("source files unreadable or over size limit")
			continue
		}
		bytesRead += len(data)
		if bytesRead > byteLimit {
			p.markIncomplete("index byte limit reached")
			return
		}
		lines := strings.Split(string(data), "\n")
		for ln, line := range lines {
			for _, m := range identRe.FindAllString(line, -1) {
				if refs >= refLimit {
					p.markIncomplete("reference limit reached")
					return
				}
				if _, exists := p.wordIndex[m]; !exists {
					if len(p.wordIndex) >= wordLimit {
						p.markIncomplete("identifier limit reached")
						return
					}
					m = strings.Clone(m)
				}
				refs++
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
