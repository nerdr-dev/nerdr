// Package denied keeps what nerdr was turned down when it tried: a program
// it ran that said so (permission denied, a sandbox's "BLOCKED by
// sandbox", a proxy's Forbidden), a file or socket it could not make for
// lack of permission. Much of what nerdr does goes on behind its screen,
// and what fails there fails quietly; in a sandbox such as cplt that is
// most of what does not work. The calls dialog (prefix and g) lists them,
// to say what to let through.
package denied

import (
	"errors"
	"io/fs"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Denial is one thing nerdr was turned down for, however many times.
type Denial struct {
	What        string // "git status in ~/src/x", "open ~/.local/state/nerdr/x.lock"
	Why         string // the line that said so
	N           int
	First, Last time.Time
}

// most is how many denials are kept: past it the one seen longest ago goes.
const most = 50

var (
	mu    sync.Mutex
	kept  = map[[2]string]*Denial{}
	noted int // denials kept since nerdr started, not counting repeats
)

// Note keeps that what was turned down, for why.
func Note(what, why string) {
	what, why = home(what), home(strings.TrimSpace(why))
	now := time.Now()
	mu.Lock()
	defer mu.Unlock()
	k := [2]string{what, why}
	if d := kept[k]; d != nil {
		d.N++
		d.Last = now
		return
	}
	if len(kept) >= most {
		var oldest [2]string
		for k, d := range kept {
			if oldest == ([2]string{}) || d.Last.Before(kept[oldest].Last) {
				oldest = k
			}
		}
		delete(kept, oldest)
	}
	kept[k] = &Denial{What: what, Why: why, N: 1, First: now, Last: now}
	noted++
}

// Noted is how many denials have been kept since nerdr started, repeats
// not counted: one more is a new one.
func Noted() int {
	mu.Lock()
	defer mu.Unlock()
	return noted
}

// List is the denials kept, the latest first.
func List() []Denial {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Denial, 0, len(kept))
	for _, d := range kept {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	return out
}

// Err keeps err when it is a lack of permission, by the file, folder or
// socket it says, and returns it as it is, so it can wrap a return.
func Err(err error) error {
	if !Is(err) {
		return err
	}
	var pe *fs.PathError
	var le *os.LinkError
	var oe *net.OpError
	switch {
	case errors.As(err, &pe):
		Note(pe.Op+" "+pe.Path, pe.Err.Error())
	case errors.As(err, &le):
		Note(le.Op+" "+le.Old+" → "+le.New, le.Err.Error())
	case errors.As(err, &oe) && oe.Addr != nil:
		Note(oe.Op+" "+oe.Addr.String(), oe.Err.Error())
	default:
		Note(err.Error(), "")
	}
	return err
}

// Is is whether err is a lack of permission: EACCES, EPERM, or a file
// system that is read only, as a sandbox's can be.
func Is(err error) bool {
	return err != nil && (errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EROFS))
}

// markers are what a program's errors say when it was turned down, in
// lower case: the system's own words, cplt's guards', an HTTPS proxy's.
var markers = []string{
	"blocked by sandbox",
	"permission denied",
	"operation not permitted",
	"read-only file system",
	"forbidden",
	"proxyconnect",
}

// Line is the first line of a program's errors that says it was turned
// down, if one does.
func Line(stderr string) (string, bool) {
	for _, l := range strings.Split(stderr, "\n") {
		low := strings.ToLower(l)
		for _, m := range markers {
			if strings.Contains(low, m) {
				return strings.TrimSpace(l), true
			}
		}
	}
	return "", false
}

// home writes the home folder in s as ~, which is shorter.
func home(s string) string {
	h, err := os.UserHomeDir()
	if err != nil || h == "" || h == "/" {
		return s
	}
	return strings.ReplaceAll(s, h, "~")
}
