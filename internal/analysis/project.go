package analysis

import (
	"encoding/json"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Alias maps an import prefix to a directory, e.g. "@/" -> "src/".
type Alias struct {
	Prefix string
	Base   string
}

// Project holds the analysis state for a single worktree: the set of source
// files and the tsconfig path aliases used to resolve imports.
type Project struct {
	Root    string
	Aliases []Alias
	files   []string // source files, paths relative to Root

	fileSetCache map[string]bool
	wordIndex    map[string][]refEntry
	wordBuilt    bool
	stamps       map[string]sourceStamp
}

type sourceStamp struct {
	size     int64
	modified time.Time
}

var sourceExts = map[string]bool{
	".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".cjs": true,
}

var skipDirs = map[string]bool{
	"node_modules": true, ".git": true, ".next": true, "dist": true,
	"build": true, "out": true, ".turbo": true, ".cache": true, "coverage": true,
}

// LoadProject walks the worktree collecting source files and reading tsconfig
// path aliases. It never fails hard; an unreadable tree yields an empty file set.
func LoadProject(root string) *Project {
	p := &Project{Root: root}
	p.loadTsconfig(filepath.Join(root, "tsconfig.json"))
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if sourceExts[filepath.Ext(d.Name())] {
			rel, err := filepath.Rel(root, path)
			if err == nil {
				p.files = append(p.files, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	p.stamps = make(map[string]sourceStamp, len(p.files)+1)
	for _, file := range append(append([]string(nil), p.files...), "tsconfig.json") {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(file))); err == nil {
			p.stamps[file] = sourceStamp{size: info.Size(), modified: info.ModTime()}
		}
	}
	return p
}

// Same reports whether q was loaded from the same source files as p, none
// of them changed since.
func (p *Project) Same(q *Project) bool {
	return maps.Equal(p.stamps, q.stamps)
}

// Index reads every source file into the index References looks names up
// in. It is the slow part of a project, so whoever loads one off the UI's
// goroutine does it there too; References does it itself on a project
// that has not been indexed.
func (p *Project) Index() { p.buildWordIndex() }

func (p *Project) loadTsconfig(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var cfg struct {
		CompilerOptions struct {
			BaseURL string              `json:"baseUrl"`
			Paths   map[string][]string `json:"paths"`
		} `json:"compilerOptions"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return
	}
	base := cfg.CompilerOptions.BaseURL
	if base == "" {
		base = "."
	}
	for pattern, targets := range cfg.CompilerOptions.Paths {
		if len(targets) == 0 {
			continue
		}
		prefix, ok := aliasPrefix(pattern)
		if !ok {
			continue
		}
		dir := targets[0]
		dir = strings.TrimSuffix(dir, "/*")
		p.Aliases = append(p.Aliases, Alias{
			Prefix: prefix,
			Base:   filepath.ToSlash(filepath.Join(base, dir)),
		})
	}
}

// aliasPrefix extracts the literal prefix from a tsconfig paths pattern such
// as "@/*", returning ("@/", true). Patterns without a trailing "/*" are ignored.
func aliasPrefix(pattern string) (string, bool) {
	if !strings.HasSuffix(pattern, "/*") {
		return "", false
	}
	return pattern[:len(pattern)-1], true
}

// hasFile reports whether the given slash-separated path (relative to Root)
// exists in the file set.
func (p *Project) hasFile(rel string) bool {
	rel = strings.TrimPrefix(rel, "./")
	_, ok := p.fileSet()[rel]
	return ok
}

func (p *Project) fileSet() map[string]bool {
	if p.fileSetCache == nil {
		p.fileSetCache = make(map[string]bool, len(p.files))
		for _, f := range p.files {
			p.fileSetCache[f] = true
		}
	}
	return p.fileSetCache
}
