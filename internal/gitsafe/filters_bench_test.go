package gitsafe

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// BenchmarkSandboxConfigScan models a refresh across ten repositories with
// five tabs each and 100 tracked files per tab. Includes and credential scopes exercise the full config
// scan; no unsafe time-based cache is used. Run with -benchtime=100x -count=3
// for a bounded measurement (each operation is one tab, not all fifty).
func BenchmarkSandboxConfigScan(b *testing.B) {
	b.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root := b.TempDir()
	global := filepath.Join(root, "global")
	var config strings.Builder
	for i := 0; i < 3; i++ {
		include := filepath.Join(root, fmt.Sprintf("include-%d", i))
		var included strings.Builder
		for j := 0; j < 40; j++ {
			fmt.Fprintf(&included, "[alias]\n command%d%d = status\n", i, j)
		}
		fmt.Fprintf(&included, "[credential \"https://host%d.invalid/a=b'c\"]\n helper = !false\n", i)
		if err := os.WriteFile(include, []byte(included.String()), 0600); err != nil {
			b.Fatal(err)
		}
		fmt.Fprintf(&config, "[include]\n path = %s\n", include)
	}
	config.WriteString("[gpg \"ssh\"]\n program = /dev/null\n[credential]\n helper = !false\n")
	if err := os.WriteFile(global, []byte(config.String()), 0600); err != nil {
		b.Fatal(err)
	}
	b.Setenv("GIT_CONFIG_GLOBAL", global)
	var tabs []string
	for i := 0; i < 10; i++ {
		r, repo, wt := repoWithWorktree(b)
		for j := 0; j < 100; j++ {
			if err := os.WriteFile(filepath.Join(repo, fmt.Sprintf("file-%d.txt", j)), []byte("tracked content\n"), 0644); err != nil {
				b.Fatal(err)
			}
		}
		run(b, repo, "add", ".")
		run(b, repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "files")
		run(b, wt, "reset", "--hard", "main")
		Trust(repo, wt)
		Sandboxed(wt)
		tabs = append(tabs, wt)
		for j := 1; j < 5; j++ {
			wt := filepath.Join(r, fmt.Sprintf("tab-%d", j))
			run(b, repo, "worktree", "add", "-q", "-b", fmt.Sprintf("tab-%d", j), wt)
			Trust(repo, wt)
			tabs = append(tabs, wt)
		}
	}
	b.Run("Scan", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			d, _ := dirs(tabs[i%len(tabs)])
			var disabled [][2]string
			if why := filterRefusal(d, &disabled); why != "" {
				b.Fatal(why)
			}
		}
	})
	b.Run("Env", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			Env(tabs[i%len(tabs)])
		}
	})
	b.Run("RawStatus", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if out, err := exec.Command("git", "-C", tabs[i%len(tabs)], "status", "--porcelain").CombinedOutput(); err != nil {
				b.Fatalf("%v %s", err, out)
			}
		}
	})
	b.Run("GuardedStatus", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if out, err := Command(tabs[i%len(tabs)], "status", "--porcelain").CombinedOutput(); err != nil {
				b.Fatalf("%v %s", err, out)
			}
		}
	})
}
