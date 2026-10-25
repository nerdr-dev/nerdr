package review

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/analysis"
	"github.com/nerdr-dev/nerdr/internal/git"
	"github.com/nerdr-dev/nerdr/internal/order"

	tea "github.com/charmbracelet/bubbletea"
)

// treeNode is a directory (isDir) or file in the changed-files tree. Sections
// are synthetic directory nodes whose path is prefixed with "#".
type treeNode struct {
	name      string
	path      string // dir path, file path, or "#section"
	isDir     bool
	status    string // file status code (files only)
	children  []*treeNode
	fileCount int // changed files in the subtree (dirs only)
}

type flatRow struct {
	node  *treeNode
	depth int
}

// sectionPeek holds the file opened by following a reference out of the
// change set, so the tree marks what the diff pane is actually showing
// instead of leaving the previously selected file highlighted.
const sectionPeek = "not in this change · u back"

const (
	sectionChanged = "changed"
	sectionNew     = "new"
	sectionDeleted = "deleted"

	// sectionFiles holds the files with no import relationship to any
	// other changed file, shown as a plain directory tree.
	sectionFiles = "other"
)

func classify(code string) string {
	if code == "??" || (len(code) > 0 && code[0] == 'A') {
		return sectionNew
	}
	if len(code) > 0 && (code[0] == 'D' || (len(code) > 1 && code[1] == 'D')) {
		return sectionDeleted
	}
	return sectionChanged
}

// peekNode builds the synthetic section for a peeked file.
func peekNode(path string) *treeNode {
	root := &treeNode{name: sectionPeek, path: "#" + sectionPeek, isDir: true}
	root.children = append(root.children, &treeNode{name: path, path: path})
	computeCount(root)
	return root
}

// statusMark is the one-letter kind shown against a file: m)odified,
// n)ew, d)eleted. The raw porcelain codes it derives from ("??", " M",
// "A ", …) are precise but need decoding; the split they used to drive —
// separate sections per kind — scattered files that belong together.
func statusMark(code string) string {
	if strings.TrimSpace(code) == "" {
		return " " // the peeked file isn't part of the change set
	}
	switch classify(code) {
	case sectionNew:
		return "n"
	case sectionDeleted:
		return "d"
	default:
		return "m"
	}
}

// buildTree arranges the changed files as one list rather than splitting
// them by kind: files that belong together are grouped by their import
// relationships (see analysis.Group), ordered bottom-up so what is used
// comes before what uses it, and whatever has no such relationship falls
// into a plain directory tree. Each file carries its own m/n/d mark, so
// nothing is lost by not separating them.
func buildTree(files []git.FileStatus, groups []analysis.Group) []*treeNode {
	byPath := make(map[string]git.FileStatus, len(files))
	for _, f := range files {
		byPath[f.Path] = f
	}

	var out []*treeNode
	grouped := map[string]bool{}
	for gi, g := range groups {
		gnode := &treeNode{
			name:  fmt.Sprintf("group %d: %d files around %s", gi+1, len(g.Files), baseName(g.Base)),
			path:  fmt.Sprintf("#group/%d", gi),
			isDir: true,
		}
		for _, fp := range g.Files {
			fs, ok := byPath[fp]
			if !ok {
				continue
			}
			grouped[fp] = true
			gnode.children = append(gnode.children, &treeNode{name: fp, path: fp, status: fs.Code})
		}
		// Group by folder so files from the same package read together;
		// a stable sort leaves the dependency order from analysis.Group
		// intact within each folder, so what is used still comes before
		// what uses it where that is visible at all.
		sortByFolder(gnode.children)
		if len(gnode.children) > 0 {
			computeCount(gnode)
			out = append(out, gnode)
		}
	}

	var rest []git.FileStatus
	for _, f := range files {
		if !grouped[f.Path] {
			rest = append(rest, f)
		}
	}
	if len(rest) == 0 {
		return out
	}

	root := &treeNode{name: sectionFiles, path: "#" + sectionFiles, isDir: true}
	for _, f := range rest {
		root.children = append(root.children, &treeNode{name: f.Path, path: f.Path, status: f.Code})
	}
	sortByFolder(root.children)
	computeCount(root)
	if len(out) == 0 {
		// Nothing was grouped, so there is nothing to distinguish these
		// from — skip the wrapper and list them directly.
		return root.children
	}
	return append(out, root)
}

// sortByFolder orders file rows by the folder they sit in, stably, so any
// meaningful order they already had survives within a folder.
func sortByFolder(nodes []*treeNode) {
	sort.SliceStable(nodes, func(i, j int) bool {
		return dirOf(nodes[i].path) < dirOf(nodes[j].path)
	})
}

// dirOf returns the folder a path sits in, "" for a file at the root.
func dirOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return ""
}

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func computeCount(n *treeNode) int {
	if !n.isDir {
		return 1
	}
	c := 0
	for _, ch := range n.children {
		c += computeCount(ch)
	}
	n.fileCount = c
	return c
}

// isExpanded reports whether a directory node is expanded. Sections default to
// expanded; nested directories default to collapsed until toggled.
func (m *Model) isExpanded(n *treeNode) bool {
	if v, ok := m.expanded[n.path]; ok {
		return v
	}
	if n.path == "#order/"+order.RoleMechanical {
		return false // the same edit repeated is what you asked not to scroll through
	}
	return strings.HasPrefix(n.path, "#")
}

// flatRows flattens the tree into the currently visible rows.
func (m *Model) flatRows() []flatRow {
	var out []flatRow
	var walk func(nodes []*treeNode, depth int)
	walk = func(nodes []*treeNode, depth int) {
		for _, n := range nodes {
			out = append(out, flatRow{node: n, depth: depth})
			if n.isDir && m.isExpanded(n) {
				walk(n.children, depth+1)
			}
		}
	}
	walk(m.tree, 0)
	return out
}

// currentFile returns the path of the file under the cursor, if any.
func (m *Model) currentFile() (string, bool) {
	rows := m.flatRows()
	if m.cursor < 0 || m.cursor >= len(rows) || rows[m.cursor].node.isDir {
		return "", false
	}
	return rows[m.cursor].node.path, true
}

// sectionUndescribed holds changed files the reading order says nothing
// about. They are listed rather than hidden: an order that quietly leaves
// files out would have you believe you had read the change when you had
// not, and it is also what tells the agent what is left to describe.
const sectionUndescribed = "not described"

// buildOrderTree arranges the changed files by the part they play in the
// change, in the sequence the roles are meant to be read, instead of by the
// import graph. Within a role the files keep the order the agent introduced
// them in.
//
// The mechanical section is collapsed by default (see isExpanded): the
// whole point of marking an edit as the same thing done many times is not
// having to scroll past it.
func buildOrderTree(files []git.FileStatus, ix order.Index) []*treeNode {
	byPath := make(map[string]git.FileStatus, len(files))
	for _, f := range files {
		byPath[f.Path] = f
	}

	var out []*treeNode
	described := map[string]bool{}
	for _, role := range order.Roles() {
		node := &treeNode{name: order.Label(role), path: "#order/" + role, isDir: true}
		for _, path := range ix.Files() {
			e, _ := ix.For(path)
			fs, ok := byPath[path]
			if e.Role != role || !ok {
				continue // described, but not part of what is on screen now
			}
			described[path] = true
			node.children = append(node.children, &treeNode{
				name: path, path: path, status: fs.Code,
			})
		}
		if len(node.children) > 0 {
			computeCount(node)
			out = append(out, node)
		}
	}

	rest := &treeNode{name: sectionUndescribed, path: "#" + sectionUndescribed, isDir: true}
	for _, f := range files {
		if !described[f.Path] {
			rest.children = append(rest.children, &treeNode{name: f.Path, path: f.Path, status: f.Code})
		}
	}
	if len(rest.children) > 0 {
		sortByFolder(rest.children)
		computeCount(rest)
		out = append(out, rest)
	}
	return out
}

// cycleFileList is t: the file list in the reading order an agent
// recorded, grouped by imports, or in its folders, and round again. The
// reading order is left out while none is recorded.
func (m *Model) cycleFileList() tea.Cmd {
	switch {
	case m.folderTree:
		m.showOrder = true
		return m.toggleFolderTree() // the reading order, or the imports without one
	case m.showOrder && m.orderIndex.Len() > 0:
		return m.toggleOrder()
	}
	return m.toggleFolderTree()
}

// toggleOrder switches the file list between the reading order an agent
// recorded and the plain import-graph grouping. Which one helps depends on
// what you are doing — following the change as it was made, or looking for
// one particular file — so it is a key rather than a setting.
func (m *Model) toggleOrder() tea.Cmd {
	if m.folderTree && m.orderIndex.Len() > 0 {
		// Out of the folder tree, r is asked for the reading order.
		m.showOrder = false
	}
	if m.orderIndex.Len() == 0 {
		m.setToast("no reading order recorded for this branch — " + m.askForOne())
		return nil
	}
	m.showOrder = !m.showOrder
	m.folderTree = false
	path, _ := m.currentFile()
	m.rebuildTree(path)
	if m.showOrder {
		m.setToast("reading order: the change itself first, mechanical edits last")
	} else {
		m.setToast("files grouped by imports")
	}
	return nil
}

// sectionFolder prefixes the folder nodes of the folder tree. Like the
// other sections they start expanded (see isExpanded): the point of the
// tree is to see every file where it lives.
const sectionFolder = "#folder/"

// buildFolderTree lists the changed files in their folders, as GitHub's
// file tree does: folders first, then files, each by name, and a folder
// holding nothing but one other folder joined with it ("internal/review")
// rather than taking a row of its own.
func buildFolderTree(files []git.FileStatus) []*treeNode {
	root := &treeNode{isDir: true}
	dirs := map[string]*treeNode{"": root}
	var dirFor func(p string) *treeNode
	dirFor = func(p string) *treeNode {
		if d, ok := dirs[p]; ok {
			return d
		}
		d := &treeNode{name: baseName(p), path: sectionFolder + p, isDir: true}
		dirs[p] = d
		parent := dirFor(dirOf(p))
		parent.children = append(parent.children, d)
		return d
	}
	for _, f := range files {
		d := dirFor(dirOf(f.Path))
		d.children = append(d.children, &treeNode{name: baseName(f.Path), path: f.Path, status: f.Code})
	}
	var tidy func(n *treeNode)
	tidy = func(n *treeNode) {
		for _, c := range n.children {
			if !c.isDir {
				continue
			}
			for len(c.children) == 1 && c.children[0].isDir {
				only := c.children[0]
				c.name += "/" + only.name
				c.path = only.path
				c.children = only.children
			}
			tidy(c)
		}
		sort.SliceStable(n.children, func(i, j int) bool {
			a, b := n.children[i], n.children[j]
			if a.isDir != b.isDir {
				return a.isDir
			}
			return a.name < b.name
		})
	}
	tidy(root)
	computeCount(root)
	return root.children
}

// toggleFolderTree switches the file list between the files in their
// folders and the list as it was: by reading order, or by imports.
func (m *Model) toggleFolderTree() tea.Cmd {
	m.folderTree = !m.folderTree
	path, _ := m.currentFile()
	m.rebuildTree(path)
	if m.folderTree {
		m.setToast("files in their folders · t next")
	} else if m.showOrder && m.orderIndex.Len() > 0 {
		m.setToast("reading order: the change itself first, mechanical edits last")
	} else {
		m.setToast("files grouped by imports")
	}
	return nil
}

// toggleFolderNote switches what stands behind a file's name: what the
// reading order says the file is for, or the folder it is in.
func (m *Model) toggleFolderNote() tea.Cmd {
	m.showFolder = !m.showFolder
	switch {
	case m.orderIndex.Len() == 0:
		m.setToast("no explanation for this branch — " + m.askForOne())
	case m.showFolder:
		m.setToast("the folder behind each file · d its explanation")
	default:
		m.setToast("the explanation behind each file · d its folder · e all of it")
	}
	return nil
}
