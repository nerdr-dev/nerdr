package store

import (
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"
)

func TestReadLinesRejectsUnsafeFiles(t *testing.T) {
	for _, kind := range []string{"fifo", "symlink", "parent-symlink", "directory", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			anchor := t.TempDir()
			dir := filepath.Join(anchor, "notes")
			remember(dir, anchor)
			path := filepath.Join(dir, "review-sessions.jsonl")
			outside := t.TempDir()
			target := filepath.Join(outside, "review-sessions.jsonl")
			if err := os.WriteFile(target, []byte("secret\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if kind == "parent-symlink" {
				if err := os.Symlink(outside, dir); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "fifo":
				err = syscall.Mkfifo(path, 0o600)
			case "symlink":
				err = os.Symlink(target, path)
			case "directory":
				err = os.Mkdir(path, 0o700)
			case "oversized":
				var f *os.File
				f, err = os.Create(path)
				if err == nil {
					_, err = f.WriteString("record\n")
					if err == nil {
						err = f.Truncate(maxRecordsBytes + 1)
					}
					f.Close()
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan []string, 1)
			go func() {
				var lines []string
				ReadLines(path, func(b []byte) { lines = append(lines, string(b)) })
				done <- lines
			}()
			select {
			case lines := <-done:
				if len(lines) != 0 {
					t.Fatalf("read unsafe metadata: %q", lines)
				}
			case <-time.After(2 * time.Second):
				if kind == "fifo" {
					if f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
						f.Close()
					}
				}
				t.Fatal("metadata read blocked")
			}
		})
	}
}

func TestReadLinesDoesNotFollowGrowth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.jsonl")
	if err := os.WriteFile(path, []byte("first\nlast"), 0o600); err != nil {
		t.Fatal(err)
	}
	var got []string
	ReadLines(path, func(b []byte) {
		got = append(got, string(b))
		if len(got) != 1 {
			return
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.WriteString("\nnew\n")
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	})
	if !slices.Equal(got, []string{"first", "last"}) {
		t.Fatalf("read beyond the initial size: %q", got)
	}
}
