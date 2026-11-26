package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// WorktreeInfo is one of a tab's worktrees as NERDR_WORKTREE_INFO gives
// it, for a wrapper to grant a sandbox what git needs without finding it
// out itself, from files the agent may have written: the shared git
// directory of a linked worktree is in its main checkout, outside the
// folder the agent starts in. Paths are real, with no links in them.
type WorktreeInfo struct {
	// Path is the worktree.
	Path string `json:"path"`
	// MainCheckout is its repository's main checkout, from nerdr's own
	// record of the worktree: "" when it has none.
	MainCheckout string `json:"main_checkout"`
	// GitCommonDir is the main checkout's .git folder, which holds the
	// objects, refs and the worktrees' indexes: "" when that is not a
	// folder of its own there (a link, a file, or not there).
	GitCommonDir string `json:"git_common_dir"`
	// Linked is whether Path is a linked worktree, not the main checkout.
	Linked bool `json:"linked"`
	// Start is whether Path is the folder the agent starts in: in a tab
	// with one repository its worktree, in one with several none of them.
	Start bool `json:"start"`
}

// worktreeInfo is NERDR_WORKTREE_INFO's list for wts, an agent of which
// starts in dir. A worktree that is not a folder of its own, a link or
// not there, is left out: the agent may write the folder it is in, and
// a link put there would lead a wrapper to grant whatever it points at.
// So is one whose path is not UTF-8, which JSON cannot carry: it would
// stand for another path, with U+FFFD in it. A main checkout's path that
// is not is given as none, with no git directory.
func worktreeInfo(wts []workspace.Worktree, dir string) []WorktreeInfo {
	start := realPath(dir)
	out := []WorktreeInfo{}
	for _, w := range wts {
		if w.Path == "" {
			continue
		}
		// Cleaned first: Lstat follows a link at a path that ends in "/".
		clean := filepath.Clean(w.Path)
		st, err := os.Lstat(clean)
		if err != nil || !st.IsDir() {
			continue
		}
		path := filepath.Join(realPath(filepath.Dir(clean)), filepath.Base(clean))
		if !utf8.ValidString(path) {
			continue
		}
		info := WorktreeInfo{Path: path, Start: SamePath(path, start)}
		if w.Repo != "" {
			main := realPath(w.Repo)
			info.Linked = !SamePath(path, main)
			if utf8.ValidString(main) {
				info.MainCheckout = main
				git := filepath.Join(main, ".git")
				if st, err := os.Lstat(git); err == nil && st.IsDir() {
					info.GitCommonDir = git
				}
			}
		}
		out = append(out, info)
	}
	return out
}

// worktreeInfoEnv is the NERDR_WORKTREE_INFO variable for wts and dir.
func worktreeInfoEnv(wts []workspace.Worktree, dir string) string {
	b, _ := json.Marshal(worktreeInfo(wts, dir))
	return "NERDR_WORKTREE_INFO=" + string(b)
}

// envEntry is env's NAME=value entry for name, "" when it has none.
func envEntry(env []string, name string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], name+"=") {
			return env[i]
		}
	}
	return ""
}

// withEntry is env with kv, a NAME=value entry, in place of the entries
// of that name.
func withEntry(env []string, kv string) []string {
	name, _, _ := strings.Cut(kv, "=")
	out := slices.DeleteFunc(slices.Clone(env), func(e string) bool { return strings.HasPrefix(e, name+"=") })
	return append(out, kv)
}

// realPath is p with its links followed, or p cleaned when they cannot be.
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}
