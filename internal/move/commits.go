package move

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// The commits of a part split off to a tab on another machine (see
// internal/spinoff) go there as a bundle too: of those commits alone, and
// what they build on that origin's main has not. The target fetches
// origin's main as it makes the new branch, and has the rest then.

// FullCommits are the full ids of commits in the repository at repo, by
// the names given, short or full; those it does not have are left out:
// in a tab with several repositories, they are another one's.
func FullCommits(repo string, commits []string) []string {
	var out []string
	for _, c := range commits {
		id, err := run(repo, nil, "rev-parse", "--verify", "--quiet", c+"^{commit}")
		if id = strings.TrimSpace(id); err == nil && isHex(id) && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// BundleCommits writes the bundle of commits, full ids the repository at
// repo has, to out, leaving out what origin's main has.
func BundleCommits(repo string, commits []string, out string) error {
	if len(commits) == 0 {
		return errors.New("no commits to bundle")
	}
	base := "refs/nerdr-spinoff/" + token() + "/"
	var refs []string
	for i, c := range commits {
		if !isHex(c) {
			return fmt.Errorf("no commit %q", c)
		}
		ref := fmt.Sprintf("%s%d", base, i)
		if _, err := run(repo, nil, "update-ref", ref, c, ""); err != nil {
			return err
		}
		defer run(repo, nil, "update-ref", "-d", ref)
		refs = append(refs, ref)
	}
	var not []string
	for _, r := range originMain(repo) {
		not = append(not, "^"+r)
	}
	args := append(append([]string{"bundle", "create", "--quiet", out}, refs...), not...)
	_, err := run(repo, nil, args...)
	return err
}

// originMain are the commits of origin's main branch the repository at
// repo has: what a bundle for another clone of origin may leave out.
func originMain(repo string) []string {
	refs := []string{"refs/remotes/origin/main", "refs/remotes/origin/master"}
	if d, err := run(repo, nil, "symbolic-ref", "-q", "refs/remotes/origin/HEAD"); err == nil {
		refs = append([]string{strings.TrimSpace(d)}, refs...)
	}
	var out []string
	for _, r := range refs {
		if id, err := run(repo, nil, "rev-parse", "-q", "--verify", r+"^{commit}"); err == nil {
			if id = strings.TrimSpace(id); !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	return out
}

// UnbundleCommits brings the objects of bundle, made by BundleCommits,
// into the repository at repo, and checks that commits, and everything
// they need, are there then. No ref is made or moved: the commits are for
// git cherry-pick.
func UnbundleCommits(repo, bundle string, commits []string) error {
	if len(commits) == 0 {
		return errors.New("no commits in the bundle")
	}
	for _, c := range commits {
		if !isHex(c) {
			return fmt.Errorf("no commit %q", c)
		}
	}
	if _, err := run(repo, nil, "bundle", "verify", "--quiet", bundle); err != nil {
		return fmt.Errorf("the bundle does not fit this repository: %w", err)
	}
	heads, err := run(repo, nil, "bundle", "list-heads", bundle)
	if err != nil {
		return err
	}
	in := map[string]bool{}
	for _, l := range strings.Split(heads, "\n") {
		if f := strings.Fields(l); len(f) == 2 && strings.HasPrefix(f[1], "refs/nerdr-spinoff/") {
			in[f[0]] = true
		}
	}
	for _, c := range commits {
		if !in[c] {
			return fmt.Errorf("the bundle has no commit %s", short(c))
		}
	}
	// As Land: unbundle imports the objects without a transport, and
	// does not check them, which checkObjects does.
	if _, err := run(repo, nil, "bundle", "unbundle", bundle); err != nil {
		return fmt.Errorf("git unpack from the bundle: %w", err)
	}
	if err := checkObjects(repo, commits); err != nil {
		return fmt.Errorf("the commits are incomplete or invalid: %w", err)
	}
	return nil
}
