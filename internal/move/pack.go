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
// gives its size and its SHA-256. It fails past Limit, before it has
// written much more than Limit, and leaves no out when it fails.
func WritePack(out string, files [][2]string) (size int64, sum string, err error) {
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, "", err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(out)
		}
	}()
	h := sha256.New()
	cw := &countWriter{w: io.MultiWriter(f, h), max: Limit}
	tw := tar.NewWriter(cw)
	for _, nf := range files {
		if err := addFile(tw, cw, nf[0], nf[1]); err != nil {
			f.Close()
			return cw.n, "", err
		}
	}
	if err := tw.Close(); err != nil {
		f.Close()
		return cw.n, "", err
	}
	if err := f.Close(); err != nil {
		return 0, "", err
	}
	return cw.n, hex.EncodeToString(h.Sum(nil)), nil
}

// TooBig is the error for a pack of n bytes, past Limit.
func TooBig(n int64) error {
	return fmt.Errorf("the move is %s, more than the %s a move may be", Size(n), Size(Limit))
}

// addFile writes the file at path to tw, as name. It refuses, before
// writing it, a file that would take the pack, counted by cw, past Limit.
func addFile(tw *tar.Writer, cw *countWriter, name, path string) error {
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
	if st.Size() > Limit-cw.n {
		return TooBig(cw.n + st.Size())
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: st.Size(), Typeflag: tar.TypeReg, Format: tar.FormatPAX}); err != nil {
		return err
	}
	_, err = io.Copy(tw, io.LimitReader(f, st.Size()))
	return err
}

// A countWriter counts what it writes, and refuses a write that would
// take it past max.
type countWriter struct {
	w   io.Writer
	n   int64
	max int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > c.max-c.n {
		return 0, TooBig(c.n + int64(len(p)))
	}
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
	n, err := io.Copy(h, io.LimitReader(f, Limit+1))
	if err != nil {
		return nil, err
	}
	if n > Limit {
		return nil, TooBig(n)
	}
	if hex.EncodeToString(h.Sum(nil)) != sum {
		return nil, errors.New("the pack did not arrive whole")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	tr := tar.NewReader(io.LimitReader(f, Limit))
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
		if hd.Size < 0 || hd.Size > Limit-total {
			return nil, fmt.Errorf("the pack has %q of %d bytes, more than the %s a move may be", hd.Name, hd.Size, Size(Limit))
		}
		total += hd.Size
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
