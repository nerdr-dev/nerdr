package gitsafe

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSandboxedRepositoryNeverRunsConfiguredFilters(t *testing.T) {
	for _, driver := range []string{"clean", "smudge", "process"} {
		t.Run(driver, func(t *testing.T) {
			resetWorktreeConfig(t)
			t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
			t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
			root, repo, wt := repoWithWorktree(t)
			Trust(repo, wt)
			marker := filepath.Join(root, "ran-filter")
			// The protected repo config delegates to an editable project file.
			if err := os.WriteFile(filepath.Join(wt, "filter.sh"), []byte("#!/bin/sh\ntouch '"+marker+"'\ncat\n"), 0755); err != nil {
				t.Fatal(err)
			}
			run(t, repo, "config", "filter.project."+driver, "./filter.sh")
			os.WriteFile(filepath.Join(wt, ".gitattributes"), []byte("*.txt filter=project\n"), 0644)
			os.WriteFile(filepath.Join(wt, "f.txt"), []byte("content\n"), 0644)
			Sandboxed(wt)
			for _, args := range [][]string{{"status", "--porcelain"}, {"diff", "HEAD"}, {"add", "f.txt"}} {
				if err := Command(wt, args...).Run(); err == nil {
					t.Fatalf("allowed %v with %s filter", args, driver)
				}
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("filter ran: %v", err)
			}
			if why := Refusals()[repo]; !strings.Contains(why, "executable Git filters") {
				t.Fatalf("missing explanation: %q", why)
			}
			run(t, repo, "config", "--unset", "filter.project."+driver)
			if out, err := Command(wt, "status", "--porcelain").CombinedOutput(); err != nil {
				t.Fatalf("filter removed: %v %s", err, out)
			}
		})
	}
}

func TestSandboxRepositoryIdentitySurvivesClosedWorktree(t *testing.T) {
	resetWorktreeConfig(t)
	_, repo, wt := repoWithWorktree(t)
	Trust(repo, wt)
	Sandboxed(wt)
	mu.Lock()
	delete(trusted, clean(wt)) // no live tab restores this mapping
	mu.Unlock()
	if !RepoSandboxed(repo) {
		t.Fatal("lost repository provenance with its worktree")
	}
}

func TestSandboxedRepositoryRefusesInactiveBranchFilterInclude(t *testing.T) {
	resetWorktreeConfig(t)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root, repo, wt := repoWithWorktree(t)
	Trust(repo, wt)
	config := filepath.Join(root, "branch-config")
	if err := os.WriteFile(config, []byte("[filter \"project\"]\nclean = ./filter.sh\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// Inactive now; a sandboxed agent can change HEAD after the check.
	run(t, repo, "config", "includeIf.onbranch:not-the-current-branch.path", config)
	Sandboxed(wt)
	if err := Command(wt, "status", "--porcelain").Run(); err == nil {
		t.Fatal("allowed mutable branch-dependent filter policy")
	}
	if why := Refusals()[repo]; !strings.Contains(why, "branch-dependent Git configuration") {
		t.Fatalf("wrong explanation: %q", why)
	}
}

func TestSandboxedRepositoryNeverRunsDiffOrMergeDrivers(t *testing.T) {
	for _, key := range []string{"diff.project.textconv", "diff.project.command", "diff.external", "merge.project.driver"} {
		t.Run(key, func(t *testing.T) {
			resetWorktreeConfig(t)
			t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
			t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
			root, repo, wt := repoWithWorktree(t)
			write := func(name, content string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(wt, name), []byte(content), 0755); err != nil {
					t.Fatal(err)
				}
			}
			commit := func() {
				t.Helper()
				run(t, wt, "add", ".")
				run(t, wt, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "fixture")
			}
			write("f.txt", "base\n")
			write(".gitattributes", "*.txt diff=project merge=project\n")
			commit()
			args := []string{"diff", "--ext-diff", "--textconv", "HEAD"}
			if key == "merge.project.driver" {
				run(t, wt, "checkout", "-qb", "other")
				write("f.txt", "other\n")
				commit()
				run(t, wt, "checkout", "-q", "wt")
				write("f.txt", "ours\n")
				commit()
				args = []string{"-c", "user.name=t", "-c", "user.email=t@t", "merge", "--no-commit", "other"}
			} else {
				write("f.txt", "changed\n")
			}
			marker := filepath.Join(root, "ran-driver")
			write("driver.sh", "#!/bin/sh\ntouch '"+marker+"'\nexit 0\n")
			run(t, repo, "config", key, "./driver.sh")
			// Prove the fixture actually executes the editable script.
			run(t, wt, args...)
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("unguarded driver did not run: %v", err)
			}
			if err := os.Remove(marker); err != nil {
				t.Fatal(err)
			}
			if key == "merge.project.driver" {
				run(t, wt, "merge", "--abort")
			}
			Trust(repo, wt)
			Sandboxed(wt)
			if err := Command(wt, args...).Run(); err == nil {
				t.Fatal("allowed executable driver")
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("guarded driver ran: %v", err)
			}
			if why := Refusals()[repo]; !strings.Contains(why, "diff/merge drivers") {
				t.Fatalf("missing driver explanation: %q", why)
			}
		})
	}
}

func TestSandboxedGlobalLFSFailsConversionWithoutBlockingOrdinaryFiles(t *testing.T) {
	resetWorktreeConfig(t)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root, repo, wt := repoWithWorktree(t)
	global := filepath.Join(root, "global-config")
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	marker := filepath.Join(root, "ran-lfs")
	if err := os.WriteFile(filepath.Join(wt, "lfs.sh"), []byte("#!/bin/sh\ntouch '"+marker+"'\ncat\n"), 0755); err != nil {
		t.Fatal(err)
	}
	// A standard LFS installation must not launch a worktree-controlled
	// executable found on PATH either.
	t.Setenv("PATH", wt+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := os.Rename(filepath.Join(wt, "lfs.sh"), filepath.Join(wt, "git-lfs")); err != nil {
		t.Fatal(err)
	}
	for driver, command := range map[string]string{
		"clean": "git-lfs clean -- %f", "smudge": "git-lfs smudge -- %f", "process": "git-lfs filter-process",
	} {
		run(t, repo, "config", "--global", "filter.lfs."+driver, command)
	}
	// Even a configured optional filter must not silently add raw LFS data.
	run(t, repo, "config", "--global", "filter.lfs.required", "false")
	Trust(repo, wt)
	Sandboxed(wt)
	for _, args := range [][]string{{"status", "--porcelain"}, {"diff", "HEAD"}, {"add", "git-lfs"}} {
		if out, err := Command(wt, args...).CombinedOutput(); err != nil {
			t.Fatalf("non-LFS %v failed: %v %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(wt, ".gitattributes"), []byte("*.bin filter=lfs\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "large.bin"), []byte("raw LFS content\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := Command(wt, "add", "large.bin").CombinedOutput(); err == nil || !strings.Contains(string(out), "filter 'lfs' failed") {
		t.Fatalf("LFS conversion must fail closed: %v %s", err, out)
	}
	if out := run(t, wt, "ls-files", "large.bin"); out != "" {
		t.Fatalf("raw LFS file was staged: %s", out)
	}
	// Prepare an index entry without invoking LFS, then verify smudge also
	// fails instead of placing an unconverted pointer in the worktree.
	run(t, wt, "-c", "filter.lfs.clean=", "-c", "filter.lfs.smudge=", "-c", "filter.lfs.process=", "-c", "filter.lfs.required=false", "add", "large.bin")
	if err := os.Remove(filepath.Join(wt, "large.bin")); err != nil {
		t.Fatal(err)
	}
	if out, err := Command(wt, "checkout-index", "--force", "--", "large.bin").CombinedOutput(); err == nil || !strings.Contains(string(out), "filter lfs failed") {
		t.Fatalf("LFS checkout must fail closed: %v %s", err, out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("LFS command ran: %v", err)
	}
}

func TestConfigurationScanTimeoutDiagnostic(t *testing.T) {
	_, repo, wt := repoWithWorktree(t)
	d, err := resolve(repo, wt)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	var disabled [][2]string
	why := filterRefusalContext(ctx, d, &disabled)
	if !strings.Contains(why, "configuration scan timed out") || !strings.Contains(why, "Git remains disabled") {
		t.Fatalf("missing timeout diagnosis: %q", why)
	}
}
