package git

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nerdr-dev/nerdr/internal/gitsafe"
)

func TestReadIndexRejectsUnsafeFiles(t *testing.T) {
	for _, kind := range []string{"fifo", "symlink", "dangling-symlink", "directory", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "index")
			switch kind {
			case "fifo":
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink", "dangling-symlink":
				target := path + "-target"
				if kind == "symlink" {
					if err := os.WriteFile(target, []byte("index data"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				f, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				// Sparse file: reject by size before allocating or reading its data.
				err = f.Truncate(maxIndexSize + 1)
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			done := make(chan error, 1)
			go func() {
				_, _, err := readIndex(path)
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil || errors.Is(err, os.ErrNotExist) {
					t.Fatalf("unsafe index should be rejected, got %v", err)
				}
			case <-time.After(2 * time.Second):
				if kind == "fifo" {
					// Release a regressed blocking open so the test leaves no reader.
					f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
					if err == nil {
						f.Close()
					}
				}
				t.Fatal("index read did not finish within two seconds")
			}
		})
	}
}

func TestReadIndexDataLimit(t *testing.T) {
	// Exercise the post-stat growth guard at a small limit, without a large
	// allocation or a timing-dependent concurrent writer.
	const limit = 16
	for _, size := range []int{0, limit - 1, limit, limit + 1, limit * 2} {
		r := strings.NewReader(strings.Repeat("x", size))
		data, err := readIndexData(r, limit)
		if size <= limit {
			if err != nil || len(data) != size {
				t.Fatalf("size %d: got %d bytes, %v", size, len(data), err)
			}
		} else {
			if err == nil || data != nil {
				t.Fatalf("size %d: oversized data accepted", size)
			}
			if consumed := size - r.Len(); consumed != limit+1 {
				t.Fatalf("size %d: read %d bytes, want %d", size, consumed, limit+1)
			}
		}
	}
}

func TestReadIndexRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index")
	want := []byte("index bytes")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(1700000000, 0)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	data, stat, err := readIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, want) || !stat.ModTime().Equal(stamp) {
		t.Fatalf("index bytes or timestamp changed: %q, %v", data, stat.ModTime())
	}
}

// Model a same-size edit within the filesystem's timestamp resolution:
// the cached stat matches, so only the index timestamp tells Git to rehash.
func TestIndexSnapshotRacyClean(t *testing.T) {
	for _, stage := range []bool{false, true} {
		name := "WorkTree"
		if stage {
			name = "StageAll"
		}
		t.Run(name, func(t *testing.T) {
			repo := initRepoWithCommit(t, map[string]string{"f": "a\n"})
			git := func(args ...string) string {
				t.Helper()
				out, err := gitsafe.Command(repo, args...).CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
				return strings.TrimSpace(string(out))
			}
			// Ignore ctime and subsecond stat fields to simulate coarse timestamps
			// without relying on the host filesystem or racing the wall clock.
			git("config", "core.trustctime", "false")
			git("config", "core.checkStat", "minimal")
			stamp := time.Unix(1700000000, 0)
			file := filepath.Join(repo, "f")
			if err := os.Chtimes(file, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			git("add", "f")
			index := git("rev-parse", "--path-format=absolute", "--git-path", "index")
			if err := os.Chtimes(index, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(index)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte("b\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(file, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			want := git("hash-object", "f")
			var tree string
			if stage {
				if err := StageAll(context.Background(), repo); err != nil {
					t.Fatal(err)
				}
				tree = git("write-tree")
			} else {
				tree, err = WorkTree(repo)
				if err != nil {
					t.Fatal(err)
				}
				after, err := os.ReadFile(index)
				if err != nil {
					t.Fatal(err)
				}
				st, err := os.Stat(index)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) || !st.ModTime().Equal(stamp) {
					t.Fatal("WorkTree changed the original index")
				}
			}
			if got := git("rev-parse", tree+":f"); got != want {
				t.Fatalf("snapshot blob = %s, want modified file %s", got, want)
			}
		})
	}
}
