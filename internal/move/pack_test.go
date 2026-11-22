package move

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// smallLimit sets Limit to n for the test.
func smallLimit(t *testing.T, n int64) {
	old := Limit
	Limit = n
	t.Cleanup(func() { Limit = old })
}

func TestReadPackRefusesAPackPastLimit(t *testing.T) {
	smallLimit(t, 4<<10)
	dir := t.TempDir()
	f := filepath.Join(dir, "f")
	write(t, f, "x")
	pack := filepath.Join(dir, "pack")
	if _, _, err := WritePack(pack, [][2]string{{"manifest.json", f}}); err != nil {
		t.Fatal(err)
	}
	// A pack far past Limit whose first Limit+1 bytes are what the sum
	// is of: the sum alone must not let the rest be read.
	b, err := os.ReadFile(pack)
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, make([]byte, 2*Limit)...)
	if err := os.WriteFile(pack, b, 0o600); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(b[:Limit+1])
	in := t.TempDir()
	if _, err := ReadPack(pack, hex.EncodeToString(h[:]), in); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("a pack past Limit was read: %v", err)
	}
	if ents, _ := os.ReadDir(in); len(ents) != 0 {
		t.Errorf("unpacked %v", ents)
	}
}

func TestReadPackRefusesFilesSaidToBeHuge(t *testing.T) {
	smallLimit(t, 4<<10)
	for _, sizes := range [][]int64{{math.MaxInt64}, {10, math.MaxInt64}, {10, math.MaxInt64 - 5}} {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		for i, size := range sizes {
			hd := &tar.Header{Name: "f" + string(rune('a'+i)), Mode: 0o600, Size: size, Typeflag: tar.TypeReg, Format: tar.FormatPAX}
			if err := tw.WriteHeader(hd); err != nil {
				t.Fatal(err)
			}
			if size < Limit {
				if _, err := tw.Write(make([]byte, size)); err != nil {
					t.Fatal(err)
				}
			}
		}
		// The last file's bytes never come: the header is all it takes.
		pack := filepath.Join(t.TempDir(), "pack")
		if err := os.WriteFile(pack, buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(buf.Bytes())
		in := t.TempDir()
		_, err := ReadPack(pack, hex.EncodeToString(h[:]), in)
		if err == nil || !strings.Contains(err.Error(), "more than") {
			t.Errorf("%v: %v", sizes, err)
		}
		if _, err := os.Stat(filepath.Join(in, "fb")); err == nil && len(sizes) > 1 {
			t.Errorf("%v: the huge file was unpacked", sizes)
		}
	}
}

func TestWritePackPastLimitLeavesNothing(t *testing.T) {
	smallLimit(t, 4<<10)
	dir := t.TempDir()
	big := filepath.Join(dir, "big")
	write(t, big, strings.Repeat("x", int(Limit)+1))
	half := filepath.Join(dir, "half")
	write(t, half, strings.Repeat("x", int(Limit)/2))
	for _, files := range [][][2]string{
		{{"big", big}},
		{{"a", half}, {"b", half}},
		{{"a", half}, {"b", half}, {"c", half}},
		{{"a", half}, {"../evil", half}},
	} {
		pack := filepath.Join(dir, "pack")
		if _, _, err := WritePack(pack, files); err == nil {
			t.Errorf("%v: packed", files)
		}
		if _, err := os.Stat(pack); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%v: left the pack: %v", files, err)
		}
	}
}
