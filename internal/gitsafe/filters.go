package gitsafe

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/nerdr-dev/nerdr/internal/proc"
)

// filterRefusal inspects configuration without running a configured command. Even a
// protected configuration can name a script in the agent's worktree, or
// a program that loads that worktree's config. Attributes the agent can
// edit select these programs during status, diff, add and checkout.
//
// Refuse rather than silently turn filters off: committing unfiltered
// content would corrupt repositories that depend on them. LFS is the one
// exception: env replaces its commands with a required but unavailable
// driver, allowing non-LFS files while failing any LFS conversion.
//
// Do not cache this check by time or just the main config mtime: Git also
// reads user/system config and recursive (potentially conditional) includes.
// Sound invalidation must cover that entire dependency graph.
// The sandbox must protect Git configuration and its included files.
func filterRefusal(d gitDirs, disabled *[][2]string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return filterRefusalContext(ctx, d, disabled)
}

func filterRefusalContext(ctx context.Context, d gitDirs, disabled *[][2]string) string {
	cmd := proc.Detach(exec.CommandContext(ctx, "git", "config", "--null", "--get-regexp", `^(filter\..*\.(clean|smudge|process)|diff\..*\.(textconv|command)|diff\.external|merge\..*\.driver|core\.(sshcommand|alternaterefscommand|gitproxy)|gc\.recentobjectshook|credential(\..*)?\.helper|gpg\.program|gpg\..*\.program|gpg\.ssh\.defaultkeycommand|remote\..*\.(uploadpack|receivepack|vcs)|uploadpack\.packobjectshook|includeif\.onbranch:.*\.path)$`))
	cmd.Dir = d.work
	cmd.Env = append(delegateEnv(os.Environ()), "GIT_DIR="+d.gitDir, "GIT_COMMON_DIR="+d.common, "GIT_WORK_TREE="+d.work)
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return "nerdr runs no git in " + d.work + ": executable Git configuration scan timed out; Git remains disabled because its safety check did not finish. Retry, or inspect Git configuration and its includes for slow or blocked reads."
	}
	if exitCode(err) == 1 {
		return ""
	} // no executable drivers
	if err != nil {
		return "nerdr runs no git in " + d.work + ": it could not check executable Git configuration in a repository where an agent ran in a sandbox"
	}
	for _, entry := range strings.Split(string(out), "\x00") {
		key, command, ok := strings.Cut(entry, "\n")
		// URL-specific credential lists override the generic reset, so reset
		// each configured scope too. This preserves harmless local Git when
		// the user has installed a helper globally (e.g. gh auth setup-git).
		if ok && strings.HasPrefix(key, "credential.") && strings.HasSuffix(key, ".helper") {
			*disabled = append(*disabled, [2]string{key, ""})
			continue
		}
		switch key {
		case "core.sshcommand", "gpg.program", "gpg.openpgp.program", "gpg.x509.program", "gpg.ssh.program", "gpg.ssh.defaultkeycommand":
			continue // These exact keys are replaced with /dev/null by env.
		}
		if ok && strings.TrimSpace(command) != "" {
			// HEAD is writable by the agent. A currently inactive branch
			// include could enable filters between this check and Git's
			// own config read, even though all config files are protected.
			if strings.HasPrefix(key, "includeif.onbranch:") {
				return "nerdr runs no git in " + d.work + ": this repository has branch-dependent Git configuration and an agent ran in a sandbox. " +
					"An agent can change branches to enable executable Git drivers after the safety check. " +
					"Use Git inside the sandbox, or inspect the work and use your own terminal."
			}
			if key == "filter.lfs.clean" || key == "filter.lfs.smudge" || key == "filter.lfs.process" {
				continue // env disables these, retaining required=true.
			}
			if strings.HasPrefix(key, "core.") || strings.HasPrefix(key, "gpg.") || strings.HasPrefix(key, "remote.") || strings.HasPrefix(key, "uploadpack.") || strings.HasPrefix(key, "gc.") {
				return "nerdr runs no git in " + d.work + ": executable Git setting " + key + " is configured and an agent ran in a sandbox. " +
					"Authentication, signing and transport delegates can run worktree code outside the sandbox. " +
					"Use Git inside the sandbox, or inspect the work and use your own terminal."
			}
			return "nerdr runs no git in " + d.work + ": this repository has executable Git filters or diff/merge drivers and an agent ran in a sandbox. " +
				"These drivers can run the agent's code outside its sandbox, even during status or diff. " +
				"Use Git inside the sandbox, or inspect the work and run Git in your own terminal. " +
				"Remove driver commands from the repository and user Git configuration only if the repository no longer needs them."
		}
	}
	return ""
}
