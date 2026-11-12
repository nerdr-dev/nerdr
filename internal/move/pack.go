package move

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/nerdr-dev/nerdr/internal/statedir"
)

// A pack is what a move carries from one machine to the other, as one
// file: a tar of the files named here, the manifest first. The client
// moves it in Chunk-sized pieces and never looks inside.

// validName is a file's name in a pack: no folders, nothing hidden.
var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ValidName says whether name may be a file's in a pack.
func ValidName(name string) bool { return validName.MatchString(name) }

// WritePack writes the files, by their names in the pack, to out, and
// gives its size and its SHA-256. It fails past Limit.
func WritePack(out string, files [][2]string) (int64, string, error) {
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, "", err
	}
	h := sha256.New()
	cw := &countWriter{w: io.MultiWriter(f, h)}
	tw := tar.NewWriter(cw)
	for _, nf := range files {
		if err := addFile(tw, nf[0], nf[1]); err != nil {
			f.Close()
			return 0, "", err
		}
		if cw.n > Limit {
			f.Close()
			return cw.n, "", TooBig(cw.n)
		}
	}
	if err := tw.Close(); err != nil {
		f.Close()
		return 0, "", err
	}
	if err := f.Close(); err != nil {
		return 0, "", err
	}
	if cw.n > Limit {
		return cw.n, "", TooBig(cw.n)
	}
	return cw.n, hex.EncodeToString(h.Sum(nil)), nil
}

// TooBig is the error for a pack of n bytes, past Limit.
func TooBig(n int64) error {
	return fmt.Errorf("the move is %s, more than the %s a move may be", Size(n), Size(Limit))
}

func addFile(tw *tar.Writer, name, path string) error {
	if !ValidName(name) {
		return fmt.Errorf("no file %q in a pack", name)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: st.Size(), Typeflag: tar.TypeReg, Format: tar.FormatPAX}); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// ReadPack checks the pack in file against sum and unpacks it into dir,
// which must be empty: only plain files with names ValidName takes, once
// each, at most Limit in all. It gives their names, in the pack's order.
func ReadPack(file, sum, dir string) ([]string, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, Limit+1)); err != nil {
		return nil, err
	}
	if hex.EncodeToString(h.Sum(nil)) != sum {
		return nil, errors.New("the pack did not arrive whole")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	tr := tar.NewReader(f)
	var names []string
	var total int64
	for {
		hd, err := tr.Next()
		if err == io.EOF {
			return names, nil
		}
		if err != nil {
			return nil, err
		}
		if hd.Typeflag != tar.TypeReg || !ValidName(hd.Name) {
			return nil, fmt.Errorf("the pack has %q, which a pack may not", hd.Name)
		}
		total += hd.Size
		if total > Limit {
			return nil, TooBig(total)
		}
		out, err := os.OpenFile(filepath.Join(dir, hd.Name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, err
		}
		_, err = io.Copy(out, io.LimitReader(tr, hd.Size))
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, err
		}
		names = append(names, hd.Name)
	}
}

// Size is n bytes in a few characters.
func Size(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d bytes", n)
	case n < 1<<20:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
}

// tempPrefixes are the temporary folders a move, and the making of a
// worktree from a verified base, make in statedir.TempDir.
var tempPrefixes = []string{"nerdr-move-out-", "nerdr-move-in-", "nerdr-move-look-", "nerdr-index-", "nerdr-objects-"}

// Sweep removes the temporary folders left by a core that ended in the
// middle of a move or of making a worktree: those of this user's older
// than age, in statedir.TempDir and in os.TempDir, where versions before
// it made them. A move under way, of another core's, is far younger.
func Sweep(age time.Duration) {
	sweep(statedir.TempDir(), age)
	sweep(os.TempDir(), age)
}

// sweep is Sweep in the folder dir.
func sweep(dir string, age time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	uid := os.Getuid()
	for _, e := range entries {
		if !e.IsDir() || !slices.ContainsFunc(tempPrefixes, func(p string) bool { return strings.HasPrefix(e.Name(), p) }) {
			continue
		}
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < age {
			continue
		}
		if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != uid {
			continue
		}
		_ = os.RemoveAll(filepath.Join(dir, e.Name()))
	}
}
