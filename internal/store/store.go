// Package store holds what agents record about a change: where the file
// lives, and how a line is appended to it safely.
//
// Where the files live is the data_dir setting (see DataDir). By default
// it is the git common directory — the .git directory every worktree of a
// repository shares — which is chosen for three things at once:
//
//   - It outlives the worktree. Agents work in worktrees that are deleted
//     once the branch is merged; anything kept inside one went with it, and
//     the main checkout never saw it.
//   - It is never repository content, without anyone having to exclude it.
//   - Every worktree reads the same file, so what was recorded on a branch
//     is still there once that branch's code arrives somewhere else.
//
// An agent in a sandbox may not be able to write there, so the folder can
// be put inside each worktree instead (a relative data_dir), or in one
// place for every repository (an absolute one).
//
// One JSON object per line, appended to. An append is made under a lock
// and as a single write, so agents in several worktrees writing at once
// cannot interleave their lines. Lines that can no longer be of use are
// dropped with Rewrite, under the same lock.
package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nerdr-dev/nerdr/internal/denied"
	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/proc"
)

// commonDirs caches each worktree's git common directory. The files are
// read on every refresh tick, and which repository a worktree belongs to
// does not change while it exists.
var commonDirs sync.Map

// CommonDir is the git common directory for the worktree at path, or ""
// when path is not inside a git repository.
func CommonDir(path string) string {
	if v, ok := commonDirs.Load(path); ok {
		return v.(string)
	}
	out, err := proc.Output(gitsafe.Command(path, "rev-parse",
		"--path-format=absolute", "--git-common-dir"))
	dir := ""
	if err == nil {
		dir = strings.TrimSpace(string(out))
	}
	commonDirs.Store(path, dir)
	return dir
}

// DataDir is the data_dir setting, set once at start: "" for the git
// common directory, a relative path for that folder inside each worktree,
// or an absolute path for one folder holding every repository's data.
var DataDir string

// EnvDataDir is the variable an agent gets DataDir in, so `nerdr why`
// run by the agent — perhaps in a sandbox that cannot read nerdr's config
// — keeps its notes where nerdr reads them.
const EnvDataDir = "NERDR_DATA_DIR"

// SetDataDir sets DataDir from the setting, with ~ expanded.
func SetDataDir(setting string) {
	setting = strings.TrimSpace(setting)
	if rest, ok := strings.CutPrefix(setting, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			setting = filepath.Join(home, rest)
		}
	}
	DataDir = setting
}

// roots caches each worktree path's top-level directory, as CommonDir does.
var roots sync.Map

func rootOf(path string) string {
	if v, ok := roots.Load(path); ok {
		return v.(string)
	}
	dir, _ := git(path, "rev-parse", "--show-toplevel")
	roots.Store(path, dir)
	return dir
}

// Dir is the folder the records for the worktree at path are kept in, or
// "" when path is not inside a git repository.
func Dir(worktreePath string) string {
	common := CommonDir(worktreePath)
	if common == "" {
		return ""
	}
	// Each is remembered with the folder above it that nerdr opens what it
	// writes there from; see safe.go.
	var dir, anchor string
	switch {
	case DataDir == "":
		dir, anchor = filepath.Join(common, "nerdr"), common
	case filepath.IsAbs(DataDir):
		// One folder per repository, named after its main checkout, so an
		// agent that cannot run nerdr can still work out where it is.
		dir, anchor = filepath.Join(DataDir, RepoName(common)), DataDir
	default:
		root := rootOf(worktreePath)
		if root == "" {
			return ""
		}
		dir, anchor = filepath.Join(root, filepath.FromSlash(DataDir)), root
	}
	remember(dir, anchor)
	return dir
}

// RepoName is the name of the main checkout of the repository whose git
// common directory is common.
func RepoName(common string) string {
	if filepath.Base(common) == ".git" {
		return filepath.Base(filepath.Dir(common))
	}
	return strings.TrimSuffix(filepath.Base(common), ".git")
}

// Path is where the file called name ("why.jsonl") is kept for the
// worktree at path, or "" when path is not inside a git repository.
func Path(worktreePath, name string) string {
	dir := Dir(worktreePath)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, name)
}

// Exclude makes git ignore pattern (a path from the worktree root, such as
// "/.nerdr/") in every worktree of the repository at path, through the
// info/exclude file in the git common directory, which git reads for all
// of them. Files nerdr puts inside a worktree then never show up as the
// change, nor get committed.
func Exclude(worktreePath, pattern string) error {
	common := CommonDir(worktreePath)
	if common == "" {
		return nil
	}
	file := filepath.Join(common, "info", "exclude")
	data, err := os.ReadFile(file)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) == pattern {
			return nil
		}
	}
	if err := denied.Err(os.MkdirAll(filepath.Dir(file), 0o755)); err != nil {
		return err
	}
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if denied.Err(err) != nil {
		return err
	}
	defer f.Close()
	prefix := ""
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		prefix = "\n"
	}
	_, err = f.WriteString(prefix + "# kept out of the change by nerdr\n" + pattern + "\n")
	return err
}

// ExcludeData keeps a data folder inside the worktree (a relative
// DataDir) out of git; with the folder anywhere else it does nothing.
func ExcludeData(worktreePath string) error {
	if DataDir == "" || filepath.IsAbs(DataDir) {
		return nil
	}
	return Exclude(worktreePath, "/"+strings.Trim(filepath.ToSlash(DataDir), "/")+"/")
}

// Root is the worktree root containing dir.
func Root(dir string) (string, error) {
	out, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", errors.New("not inside a git worktree")
	}
	return out, nil
}

// Branch is the branch checked out in dir, or "" in detached HEAD.
func Branch(dir string) string {
	out, _ := git(dir, "branch", "--show-current")
	return out
}

func git(dir string, args ...string) (string, error) {
	out, err := proc.Output(gitsafe.Command(dir, args...))
	return strings.TrimSpace(string(out)), err
}

// Local cleans file, a slash-separated path read back from a notes file,
// and reports whether it names something inside the worktree. What is
// read back was written by agents, by hand too, and is joined to the
// worktree to read and open the file: "src/../../x" must not reach
// outside it, as Relative makes sure on the way in.
func Local(file string) (string, bool) {
	file = path.Clean(file)
	return file, filepath.IsLocal(filepath.FromSlash(file))
}

// Relative resolves file, as the writer named it, to a slash-separated path
// relative to root. A relative path is tried against where the writer
// stands first, since that is what they will have meant if the file is
// there, and then against the root.
func Relative(root, dir, file string) (string, error) {
	var candidates []string
	if filepath.IsAbs(file) {
		candidates = []string{file}
	} else {
		candidates = []string{filepath.Join(dir, file), filepath.Join(root, file)}
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = root
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err != nil {
			continue
		}
		abs, err := filepath.Abs(c)
		if err != nil {
			continue
		}
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			abs = real
		}
		rel, err := filepath.Rel(realRoot, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("%s is outside the worktree %s", file, root)
		}
		return filepath.ToSlash(rel), nil
	}
	return "", fmt.Errorf("no file %s in %s or at the worktree root", file, dir)
}

// Removed resolves file the way Relative does, for a file that is no
// longer on disk: a change that deletes a file still has to say what the
// deletion is for. The path is accepted only when git has a record of it,
// so a mistyped name is still refused rather than recorded against nothing.
func Removed(root, dir, file string) (string, bool) {
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = root
	}
	var candidates []string
	if filepath.IsAbs(file) {
		candidates = []string{filepath.Clean(file)}
	} else {
		candidates = []string{filepath.Join(dir, file), filepath.Join(realRoot, file)}
	}
	for _, c := range candidates {
		rel, err := filepath.Rel(realRoot, c)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		rel = filepath.ToSlash(rel)
		if out, err := git(root, "rev-list", "-1", "HEAD", "--", ":(literal)"+rel); err == nil && out != "" {
			return rel, true
		}
	}
	return "", false
}

// Append writes v as one JSON line, under an exclusive lock and in a single
// write: agents in every worktree of the repository append to the same
// file, and a record with a long text is larger than the size the kernel
// promises to keep whole.
func Append(path string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // these records quote code: keep <, > and & readable
	if err := enc.Encode(v); err != nil {
		return err
	}

	f, err := lock(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE)
	if err != nil {
		return err
	}
	defer unlock(f)
	_, err = f.Write(buf.Bytes())
	return err
}

// lock opens path and takes its exclusive lock. Rewrite replaces the file
// rather than writing over it, so a writer that was waiting for the lock
// may hold the file that has just been replaced: it opens the new one and
// waits again, or its line would go to a file nobody reads. It is opened
// as openFile does, never through a link.
func lock(path string, flag int) (*os.File, error) {
	for {
		f, err := openFile(path, flag, 0o644)
		if err != nil {
			return nil, err
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
			f.Close()
			return nil, err
		}
		held, err1 := f.Stat()
		named, err2 := os.Lstat(path)
		if err1 == nil && err2 == nil && os.SameFile(held, named) {
			return f, nil
		}
		unlock(f)
		if err2 != nil && !os.IsNotExist(err2) {
			return nil, err2
		}
	}
}

func unlock(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	f.Close()
}

// Rewrite keeps the lines of the file at path that keep says to, under the
// lock Append takes, and reports how many it dropped. The new content is
// written to a file beside it and renamed over it, so a crash part way
// leaves the old file whole. A missing file has nothing to drop.
func Rewrite(path string, keep func([]byte) bool) (int, error) {
	f, err := lock(path, os.O_RDONLY)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer unlock(f)

	var out bytes.Buffer
	dropped := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		b := bytes.TrimSpace(sc.Bytes())
		if len(b) == 0 {
			continue
		}
		if !keep(b) {
			dropped++
			continue
		}
		out.Write(b)
		out.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	if dropped == 0 {
		return 0, nil
	}
	if err := WriteFile(path, out.Bytes(), 0o644); err != nil {
		return 0, err
	}
	return dropped, nil
}

// Branches lists the local branches of the repository the worktree at
// path belongs to.
func Branches(worktreePath string) (map[string]bool, error) {
	out, err := git(worktreePath, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			names[l] = true
		}
	}
	return names, nil
}

// BranchCreated is when branch was made, as the first line of its reflog
// says: a branch's reflog goes when the branch does, so a branch deleted
// and made again under the same name starts a new one. ok is false when
// that cannot be told — no reflog, or one whose first lines git has
// already expired, so its first line is not the creation.
func BranchCreated(worktreePath, branch string) (created time.Time, ok bool) {
	common := CommonDir(worktreePath)
	if common == "" || branch == "" || !filepath.IsLocal(filepath.FromSlash(branch)) {
		return time.Time{}, false
	}
	f, err := os.Open(filepath.Join(common, "logs", "refs", "heads", filepath.FromSlash(branch)))
	if err != nil {
		return time.Time{}, false
	}
	defer f.Close()
	line, _ := bufio.NewReader(f).ReadString('\n')
	// "<old> <new> <name> <<email>> <unix> <zone>\t<message>"
	head, _, _ := strings.Cut(line, "\t")
	old, _, _ := strings.Cut(head, " ")
	if old == "" || strings.Trim(old, "0") != "" {
		return time.Time{}, false
	}
	i := strings.LastIndex(head, "> ")
	if i < 0 {
		return time.Time{}, false
	}
	fields := strings.Fields(head[i+2:])
	if len(fields) == 0 {
		return time.Time{}, false
	}
	secs, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(secs, 0), true
}

// ReadLines hands each non-empty line of the file at path to line. A
// missing or unreadable file yields nothing rather than an error: these
// records are optional, and a repository nobody has annotated is a normal
// state, not a failure.
func ReadLines(path string, line func([]byte)) {
	if path == "" {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	// A line longer than a record can be — written by hand, or by an
	// agent — is passed over, not the end of the file: the records after
	// it are read as any others.
	r := bufio.NewReaderSize(f, 64*1024)
	var buf []byte
	long := false
	for {
		part, err := r.ReadSlice('\n')
		switch {
		case long:
		case len(buf)+len(part) > maxLine:
			long, buf = true, buf[:0]
		default:
			buf = append(buf, part...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue // the rest of the line is still to come
		}
		if !long {
			if b := bytes.TrimSpace(buf); len(b) > 0 {
				line(b)
			}
		}
		buf, long = buf[:0], false
		if err != nil {
			return
		}
	}
}

// maxLine is the longest line of a records file that is read.
const maxLine = 4 << 20
