package analysis

import (
	"encoding/json"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/nerdr-dev/nerdr/internal/safefile"
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
	incomplete   string
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

const (
	maxSourceBytes  = 512 << 10
	maxProjectFiles = 10000
	maxWalkEntries  = 100000
	maxIndexBytes   = 32 << 20
	maxIndexRefs    = 2000000
	maxIndexWords   = 250000 // bound map overhead for adversarial unique identifiers
)

// readSource never follows a worktree link or opens a special file. All
// analysis entry points use it, including reads after the initial walk.
func (p *Project) readSource(file string) ([]byte, error) {
	r, err := os.OpenRoot(p.Root)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return safefile.ReadRegularLimit(r, filepath.FromSlash(file), maxSourceBytes)
}

// LoadProject walks the worktree collecting source files and reading tsconfig
// path aliases. It never fails hard; an unreadable tree yields an empty file set.
func LoadProject(root string) *Project {
	return loadProject(root, maxProjectFiles, maxWalkEntries)
}

func loadProject(root string, fileLimit, entryLimit int) *Project {
	p := &Project{Root: root}
	p.loadTsconfig()
	r, err := os.OpenRoot(root)
	if err != nil {
		p.markIncomplete("source tree unreadable")
		return p
	}
	defer r.Close()
	// Read directories in bounded batches: WalkDir reads and sorts a whole
	// directory before invoking its callback, before any entry cap applies.
	entries := 0
	pending := []string{"."}
	for len(pending) > 0 && entries < entryLimit && len(p.files) < fileLimit {
		dir := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		f, err := r.OpenFile(dir, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_DIRECTORY, 0)
		if err != nil {
			p.markIncomplete("source directories unreadable")
			continue
		}
		for entries < entryLimit && len(p.files) < fileLimit {
			batch, err := f.ReadDir(128)
			for _, d := range batch {
				if entries >= entryLimit || len(p.files) >= fileLimit {
					break
				}
				entries++
				path := filepath.Join(dir, d.Name())
				if d.IsDir() && !skipDirs[d.Name()] {
					pending = append(pending, path)
				} else if d.Type().IsRegular() && sourceExts[filepath.Ext(d.Name())] {
					p.files = append(p.files, filepath.ToSlash(path))
				}
			}
			if err != nil {
				if err != io.EOF {
					p.markIncomplete("source directories unreadable")
				}
				break
			}
		}
		f.Close()
	}
	if entries >= entryLimit {
		p.markIncomplete("directory entry limit reached")
	}
	if len(p.files) >= fileLimit {
		p.markIncomplete("source file limit reached")
	}
	slices.Sort(p.files)
	p.stamps = make(map[string]sourceStamp, len(p.files)+1)
	for _, file := range append(append([]string(nil), p.files...), "tsconfig.json") {
		if info, err := r.Lstat(filepath.FromSlash(file)); err == nil && info.Mode().IsRegular() {
			p.stamps[file] = sourceStamp{size: info.Size(), modified: info.ModTime()}
		}
	}
	return p
}

// Same reports whether q was loaded from the same source files as p, none
// of them changed since.
func (p *Project) Same(q *Project) bool {
	return p.incomplete == q.incomplete && slices.Equal(p.files, q.files) && maps.Equal(p.stamps, q.stamps)
}

// Index reads every source file into the index References looks names up
// in. It is the slow part of a project, so whoever loads one off the UI's
// goroutine does it there too; References does it itself on a project
// that has not been indexed.
func (p *Project) Index() { p.buildWordIndex() }

func (p *Project) loadTsconfig() {
	data, err := p.readSource("tsconfig.json")
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

// Incomplete describes why analysis may omit files or references; empty means complete.
func (p *Project) Incomplete() string { return p.incomplete }

func (p *Project) markIncomplete(reason string) {
	if !strings.Contains(p.incomplete, reason) {
		if p.incomplete != "" {
			p.incomplete += "; "
		}
		p.incomplete += reason
	}
}
