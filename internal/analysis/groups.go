package analysis

import (
	"slices"
	"sort"
)

// Group is a cluster of changed files connected through local imports,
// ordered bottom-up: files nothing else in the group depends on come first,
// so a reviewer sees definitions before the code that uses them.
type Group struct {
	Files []string
	Base  string // the most foundational file, used for the group label
}

// GroupChanged clusters the given changed file paths into connected
// components using the local import graph, restricted to edges between
// files in paths. Files with no import relationship to any other file in
// paths are omitted; callers should treat them as ungrouped.
func (p *Project) GroupChanged(paths []string) []Group {
	return GroupEdges(paths, p.Imports(paths))
}

// Imports is the local import graph among paths: for each that imports
// others of them, those it imports, by normalized path. It reads the
// files.
func (p *Project) Imports(paths []string) map[string][]string {
	changed := make(map[string]bool, len(paths))
	for _, f := range paths {
		changed[normalize(f)] = true
	}
	out := map[string][]string{}
	for f := range changed {
		for _, dep := range p.ImportsOf(f) {
			if changed[dep] && dep != f {
				out[f] = append(out[f], dep)
			}
		}
	}
	return out
}

// GroupEdges is GroupChanged with the import graph among paths given, as
// Imports reads it, and reads nothing: a graph read for more paths serves
// any of them, the edges to the others left out.
func GroupEdges(paths []string, imports map[string][]string) []Group {
	changed := make(map[string]bool, len(paths))
	norm := make([]string, len(paths))
	for i, f := range paths {
		n := normalize(f)
		norm[i] = n
		changed[n] = true
	}

	edges := make(map[string][]string, len(norm)) // directed: f -> files f imports
	adjSet := make(map[string]map[string]bool, len(norm))
	for _, f := range norm {
		adjSet[f] = map[string]bool{}
	}
	for _, f := range norm {
		for _, dep := range imports[f] {
			if !changed[dep] || dep == f || slices.Contains(edges[f], dep) {
				continue
			}
			edges[f] = append(edges[f], dep)
			adjSet[f][dep] = true
			adjSet[dep][f] = true
		}
	}
	// Sorted adjacency so BFS traversal order (and thus sibling ordering
	// within a group) is deterministic across runs, not dependent on Go's
	// randomized map iteration.
	adj := make(map[string][]string, len(adjSet))
	for f, set := range adjSet {
		list := make([]string, 0, len(set))
		for n := range set {
			list = append(list, n)
		}
		sort.Strings(list)
		adj[f] = list
	}

	visited := map[string]bool{}
	var groups []Group
	for _, f := range norm {
		if visited[f] || len(adj[f]) == 0 {
			continue
		}
		var comp []string
		queue := []string{f}
		visited[f] = true
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			comp = append(comp, cur)
			for _, n := range adj[cur] {
				if !visited[n] {
					visited[n] = true
					queue = append(queue, n)
				}
			}
		}
		ordered := topoOrder(comp, edges)
		groups = append(groups, Group{Files: ordered, Base: ordered[0]})
	}

	sort.SliceStable(groups, func(i, j int) bool {
		if len(groups[i].Files) != len(groups[j].Files) {
			return len(groups[i].Files) > len(groups[j].Files)
		}
		return groups[i].Base < groups[j].Base
	})
	return groups
}

// topoOrder returns comp ordered so that if a imports b (both in comp), b
// comes before a: dependencies before dependents. Import cycles are broken
// arbitrarily rather than causing infinite recursion.
func topoOrder(comp []string, edges map[string][]string) []string {
	inComp := make(map[string]bool, len(comp))
	for _, f := range comp {
		inComp[f] = true
	}
	visited := map[string]bool{}
	inStack := map[string]bool{}
	out := make([]string, 0, len(comp))

	var visit func(f string)
	visit = func(f string) {
		if visited[f] || inStack[f] {
			return
		}
		inStack[f] = true
		for _, dep := range edges[f] {
			if inComp[dep] {
				visit(dep)
			}
		}
		inStack[f] = false
		if !visited[f] {
			visited[f] = true
			out = append(out, f)
		}
	}
	for _, f := range comp {
		visit(f)
	}
	return out
}
