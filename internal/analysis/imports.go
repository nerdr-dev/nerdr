package analysis

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	fromRe = regexp.MustCompile(`(?:from\s*['"]|require\s*\(\s*['"]|import\s*\(\s*['"])([^'"]+)`)
	sideRe = regexp.MustCompile(`(?m)^\s*import\s+['"]([^'"]+)['"]`)
)

// ImportsOf returns the local files imported by targetRel.
func (p *Project) ImportsOf(targetRel string) []string {
	abs := filepath.Join(p.Root, filepath.FromSlash(targetRel))
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil
	}
	specs := extractSpecs(string(data))

	dir := filepath.Dir(targetRel)
	seen := map[string]bool{}
	var out []string
	for _, spec := range specs {
		resolved, ok := p.resolve(spec, filepath.ToSlash(dir))
		if !ok || resolved == normalize(targetRel) {
			continue
		}
		if !seen[resolved] {
			seen[resolved] = true
			out = append(out, resolved)
		}
	}
	return out
}

func extractSpecs(src string) []string {
	var specs []string
	for _, re := range []*regexp.Regexp{fromRe, sideRe} {
		for _, m := range re.FindAllStringSubmatch(src, -1) {
			if len(m) > 1 && m[1] != "" {
				specs = append(specs, m[1])
			}
		}
	}
	return specs
}

// resolve maps an import specifier to a source file path (slash-separated,
// relative to Root). It returns ok=false for bare/non-local packages.
func (p *Project) resolve(spec, importerDir string) (string, bool) {
	var base string
	switch {
	case strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../"):
		base = filepath.ToSlash(filepath.Join(importerDir, spec))
	case spec == ".":
		base = importerDir
	default:
		// tsconfig alias?
		matched := false
		for _, a := range p.Aliases {
			if strings.HasPrefix(spec, a.Prefix) {
				base = filepath.ToSlash(filepath.Join(a.Base, strings.TrimPrefix(spec, a.Prefix)))
				matched = true
				break
			}
		}
		if !matched {
			return "", false
		}
	}

	base = strings.TrimPrefix(base, "./")
	if path, ok := tryResolve(p, base); ok {
		return path, true
	}
	return "", false
}

func tryResolve(p *Project, base string) (string, bool) {
	candidates := []string{
		base,
		base + ".ts", base + ".tsx", base + ".js", base + ".jsx", base + ".mjs", base + ".cjs",
		base + "/index.ts", base + "/index.tsx", base + "/index.js", base + "/index.jsx",
	}
	for _, c := range candidates {
		c = normalize(c)
		if p.hasFile(c) {
			return c, true
		}
	}
	return "", false
}

func normalize(rel string) string {
	return strings.TrimPrefix(filepath.ToSlash(rel), "./")
}
