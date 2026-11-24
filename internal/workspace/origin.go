package workspace

import (
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/nerdr-dev/nerdr/internal/proc"
)

// Every connection to origin is a login: with an ssh key behind Touch ID
// (in the Secure Enclave, in 1Password) or a token in the keychain that
// asks for it, a finger on the reader each. A new tab used to ask origin
// three times per repository: a fetch to see whether the branch is there,
// an ls-remote for the commit of main, and a fetch of main. Now the plan's
// one fetch says both, and its answer serves Sync while it is fresh
// (planFresh).

// hasPorcelain says whether git has fetch --porcelain (2.41 on), which
// says what origin named for every ref it fetched. Without it, nerdr asks
// origin with ls-remote as well.
var hasPorcelain = sync.OnceValue(func() bool {
	out, err := proc.Detach(exec.Command("git", "version")).Output()
	if err != nil {
		return false
	}
	return gitAtLeast(string(out), 2, 41)
})

// gitAtLeast says whether git version's answer ("git version 2.39.5
// (Apple Git-154)") is of major.minor or later.
func gitAtLeast(version string, major, minor int) bool {
	f := strings.Fields(version)
	if len(f) < 3 {
		return false
	}
	parts := strings.SplitN(f[2], ".", 3)
	if len(parts) < 2 {
		return false
	}
	ma, err1 := strconv.Atoi(parts[0])
	mi, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	return ma > major || ma == major && mi >= minor
}

// fetched is what a fetch from origin with --porcelain said: the commit
// origin named for each local ref it wrote, and the refs it could not
// write here (a stale origin/fix in the way of origin's fix/more).
type fetched struct {
	ids      map[string]string
	rejected map[string]bool
}

// listed says whether the fetch got as far as origin's refs.
func (f fetched) listed() bool { return len(f.ids) > 0 || len(f.rejected) > 0 }

// fetchOrigin fetches refspecs from origin into the main checkout repo.
// With a git that has fetch --porcelain it also says, by the local ref
// each went into, what commit origin named: origin's answer, which a
// sandboxed agent writing the main checkout's refs cannot change, as it
// could the refs. That is read even when the fetch fails for a ref it
// could not write. f is empty with an older git.
//
// It never prunes, whatever fetch.prune says: a pruned ref would be listed
// with a null id, and by the name of a loose ref file, which an agent may
// have made with a line break in it.
func fetchOrigin(repo string, refspecs ...string) (f fetched, err error) {
	porcelain := hasPorcelain()
	args := []string{"fetch", "--quiet", "--no-prune"}
	if porcelain {
		// --verbose lists the refs that were up to date as well.
		args = append(args, "--porcelain", "--verbose")
	}
	out, err := gitOrigin(repo, append(append(args, "origin"), refspecs...)...)
	if !porcelain {
		return f, err
	}
	f = fetched{ids: map[string]string{}, rejected: map[string]bool{}}
	for _, l := range strings.Split(out, "\n") {
		// <flag> <old id> <new id> <local ref>, the flag a space for a
		// fast-forward.
		if len(l) < 2 || l[1] != ' ' {
			continue
		}
		fs := strings.Fields(l[2:])
		if len(fs) != 3 || !isCommitID(fs[1]) || strings.Trim(fs[1], "0") == "" {
			continue
		}
		switch l[0] {
		case ' ', '+', '*', '=':
			f.ids[fs[2]] = fs[1]
		case '!':
			f.rejected[fs[2]] = true
		}
	}
	return f, err
}
