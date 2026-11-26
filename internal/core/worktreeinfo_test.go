package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/nerdr-dev/nerdr/internal/workspace"
)

// TestWorktreeInfo covers a tab with several repositories: its agent
// starts in the folder above the worktrees, each worktree's git directory
// is in its main checkout, and a worktree the agent made a link of is
// left out, not followed.
func TestWorktreeInfo(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	api, web, other := filepath.Join(root, "src", "api"), filepath.Join(root, "src", "web"), filepath.Join(root, "elsewhere")
	tab := filepath.Join(root, "wt", "tab")
	for _, d := range []string{filepath.Join(api, ".git"), filepath.Join(web, ".git"), other,
		filepath.Join(tab, "api"), filepath.Join(tab, "web-real")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(other, filepath.Join(tab, "web")); err != nil {
		t.Fatal(err)
	}
	// The tab's folder by a path with a link in it, as /var is on macOS.
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(filepath.Join(root, "wt"), alias); err != nil {
		t.Fatal(err)
	}
	wts := []workspace.Worktree{
		{Repo: api, Path: filepath.Join(alias, "tab", "api")},
		{Repo: web, Path: filepath.Join(tab, "web")},
	}
	// One path with "/" at the end, which Lstat would follow, and one of
	// none: neither is a worktree.
	wts = append(wts, workspace.Worktree{Repo: web, Path: filepath.Join(tab, "web") + "/"}, workspace.Worktree{Repo: web})
	got := worktreeInfo(wts, filepath.Join(alias, "tab"))
	wts = wts[:2]
	want := []WorktreeInfo{{Path: filepath.Join(tab, "api"), MainCheckout: api, GitCommonDir: filepath.Join(api, ".git"), Linked: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}

	// A tab with one repository starts in its worktree.
	got = worktreeInfo(wts[:1], filepath.Join(tab, "api"))
	if len(got) != 1 || !got[0].Start || !got[0].Linked {
		t.Fatalf("one repository: %+v", got)
	}

	// A main checkout is no linked worktree; a .git that is a link is no
	// git directory to grant.
	if err := os.RemoveAll(filepath.Join(web, ".git")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(web, ".git")); err != nil {
		t.Fatal(err)
	}
	got = worktreeInfo([]workspace.Worktree{{Repo: web, Path: web}}, root)
	if len(got) != 1 || got[0].Linked || got[0].Start || got[0].GitCommonDir != "" {
		t.Fatalf("main checkout: %+v", got)
	}

	env := worktreeInfoEnv(nil, root)
	var list []WorktreeInfo
	if v, ok := strings.CutPrefix(env, "NERDR_WORKTREE_INFO="); !ok || json.Unmarshal([]byte(v), &list) != nil || list == nil {
		t.Fatalf("no worktrees: %q", env)
	}
}

// A tab's agents get NERDR_WORKTREE_INFO for its worktrees, in place of
// one they would inherit.
func TestChildEnvHasWorktreeInfo(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	repo, wt := filepath.Join(root, "repo"), filepath.Join(root, "tab", "repo")
	for _, d := range []string{filepath.Join(repo, ".git"), wt} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	tab := &Tab{Dir: filepath.Join(root, "tab"), Worktrees: []workspace.Worktree{{Repo: repo, Path: wt}}}
	env := ChildEnvFrom([]string{"NERDR_WORKTREE_INFO=inherited"}, tab)
	var infos []string
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "NERDR_WORKTREE_INFO="); ok {
			infos = append(infos, v)
		}
	}
	var list []WorktreeInfo
	if len(infos) != 1 || json.Unmarshal([]byte(infos[0]), &list) != nil || len(list) != 1 ||
		list[0].GitCommonDir != filepath.Join(repo, ".git") || list[0].Start || !list[0].Linked {
		t.Fatalf("%q", infos)
	}
	if got := withEntry([]string{"A=1", "NERDR_WORKTREE_INFO=x", "B=2"}, "NERDR_WORKTREE_INFO=y"); !slices.Equal(got, []string{"A=1", "B=2", "NERDR_WORKTREE_INFO=y"}) {
		t.Fatalf("withEntry: %q", got)
	}
}

// A path that is not UTF-8, which JSON would carry as another one, with
// U+FFFD in it, is no path to grant: a worktree of one is left out, and
// a main checkout of one has no git directory.
func TestWorktreeInfoIsUTF8(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	bad := string([]byte{'b', 0xff})
	repo, badRepo := filepath.Join(root, "repo"), filepath.Join(root, bad)
	for _, d := range []string{filepath.Join(repo, ".git"), filepath.Join(badRepo, ".git"),
		filepath.Join(root, "tab", "ok"), filepath.Join(root, "tab", bad)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Skip("no such path here: ", err)
		}
	}
	got := worktreeInfo([]workspace.Worktree{
		{Repo: repo, Path: filepath.Join(root, "tab", bad)},
		{Repo: badRepo, Path: filepath.Join(root, "tab", "ok")},
	}, filepath.Join(root, "tab"))
	if len(got) != 1 || got[0].Path != filepath.Join(root, "tab", "ok") || got[0].MainCheckout != "" || got[0].GitCommonDir != "" || !got[0].Linked {
		t.Fatalf("%+v", got)
	}
}
