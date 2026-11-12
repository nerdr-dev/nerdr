package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/denied"
	"github.com/nerdr-dev/nerdr/internal/proc"
	"github.com/nerdr-dev/nerdr/internal/statedir"
)

// A commit id says what a commit holds only as far as the objects under
// that id are what their names say. git does not check that when it
// checks files out, and a sandboxed agent can write the main checkout's
// .git/objects and refs, which it needs to commit: it can put a file of
// its own in the place of an object origin's commit names, in a loose
// object or a pack, or name another with a ref under refs/replace. The
// base's commit, pinned by its id, would then check out the agent's code,
// for the repository's setup command and hooks to run as the user.
//
// So where a sandboxed agent has run in the repository, or asked for the
// worktree, the files of a new branch are not checked out from the
// repository's objects. The commit is first fetched into a store of
// nerdr's own, where the agent cannot write: in the state folder's
// temporary folder (see statedir), not in os.TempDir, which on macOS is
// $TMPDIR and which sandboxes tend to let their agent write, between the
// fetch and the checkout. git names every object it
// receives by hashing it, so an object whose content is not its name's
// does not arrive under that name, and the fetch fails for the want of
// it; replace refs are not looked at. The checkout then reads that store
// alone.

// verifiedObjects fetches commit id of the repository at repo, with its
// files and nothing of its history, into an object store of nerdr's own,
// and gives the store's folder and what removes it. It fails when the
// repository's objects for the commit are not what their ids say.
func verifiedObjects(repo, id string) (objects string, done func(), err error) {
	tmp, err := statedir.MkdirTemp("nerdr-objects-")
	if denied.Err(err) != nil {
		return "", nil, err
	}
	done = func() { _ = os.RemoveAll(tmp) }
	run := func(args ...string) error {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		cmd := proc.Detach(exec.CommandContext(ctx, "git", args...))
		cmd.Dir = tmp
		cmd.Env = verifiedGitEnv()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("timed out after %s", fetchTimeout)
			}
			if why := gitReason(stderr.String()); why != "" {
				return errors.New(why)
			}
			return err
		}
		return nil
	}
	if err := run("init", "--bare", "--quiet", "."); err != nil {
		done()
		return "", nil, err
	}
	// From the repository's own folder, by the commit's id: the server
	// side is asked to give a commit that no ref of its needs to name.
	// Local transport clears command-scope configuration from the child
	// environment, so server overrides must be in its command itself.
	// Unlike packObjectsHook, alternateRefsCommand accepts local config.
	err = run("-c", "uploadpack.allowAnySHA1InWant=true", "-c", "protocol.file.allow=always",
		"-c", "core.hooksPath="+os.DevNull, "-c", "core.fsmonitor=false",
		"fetch", "--quiet", "--depth=1", "--no-tags",
		"--upload-pack="+verifiedUploadPack,
		filepath.Join(repo, ".git"), id)
	if err != nil {
		done()
		return "", nil, fmt.Errorf("the repository's objects for %s are not all what origin's commit names (%v): "+
			"nerdr does not check them out. Something has written to %s; fetch the repository anew, or run git fsck in it",
			short(id), err, filepath.Join(repo, ".git", "objects"))
	}
	return filepath.Join(tmp, "objects"), done, nil
}

// These command-scope settings are applied inside the upload-pack process,
// after local transport has cleared the client's command-scope environment.
// The alternate-ref no-op includes a shell metacharacter so Git invokes the
// shell builtin instead of searching PATH for an executable named ":".
const verifiedUploadPack = "git -c core.hooksPath=" + os.DevNull + " -c core.fsmonitor=false -c 'core.alternateRefsCommand=: #' -c uploadpack.allowAnySHA1InWant=true upload-pack"

// outsideRepo is env without the variables that name a repository: the
// git it is given to works in the folder it is run in.
func outsideRepo(env []string) []string {
	var out []string
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "GIT_DIR", "GIT_COMMON_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES":
			continue
		}
		out = append(out, kv)
	}
	return out
}

// verifiedGitEnv is deliberately independent of user Git configuration and
// executable environment overrides. This private object store has exactly one
// transport: the explicit local repository path. Local transport strips
// GIT_CONFIG_COUNT/KEY/VALUE/PARAMETERS before upload-pack, but preserves
// NOSYSTEM and GLOBAL. Those disable the protected config scopes that can
// enable uploadpack.packObjectsHook (local scope is ignored by Git). Do not
// set that hook to an empty value: Git would try to execute an empty command.
func verifiedGitEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, "GIT_") || key == "SSH_ASKPASS" {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_NO_REPLACE_OBJECTS=1", "GIT_TERMINAL_PROMPT=0",
		"GIT_ALLOW_PROTOCOL=file", "GIT_ASKPASS="+os.DevNull,
		"GIT_CONFIG_COUNT=5",
		"GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0="+os.DevNull,
		"GIT_CONFIG_KEY_1=core.fsmonitor", "GIT_CONFIG_VALUE_1=false",
		"GIT_CONFIG_KEY_2=maintenance.auto", "GIT_CONFIG_VALUE_2=false",
		"GIT_CONFIG_KEY_3=gc.auto", "GIT_CONFIG_VALUE_3=0",
		"GIT_CONFIG_KEY_4=core.alternateRefsCommand", "GIT_CONFIG_VALUE_4=: #")
}
