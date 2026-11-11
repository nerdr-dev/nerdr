# Security policy

## Reporting a vulnerability

Please report security problems privately with
[Report a vulnerability](https://github.com/nerdr-dev/nerdr/security/advisories/new)
on the repository's Security tab, or by email to
[contact@nerdr.app](mailto:contact@nerdr.app). Do not open an issue, a pull
request or a discussion for them, since those are public.

A good report has:

- the nerdr version (`nerdr --version`) and your OS
- the steps to reproduce the problem
- what an attacker can do with it

## Supported versions

Only the latest [release](https://github.com/nerdr-dev/nerdr/releases) gets
security fixes. Releases are built from `v*` tags; update to the newest one
before you report.

## What is in scope

nerdr runs coding agents, `git` and `gh` in your repositories. Problems such
as these are in scope:

- a repository, a branch, a pull request or an agent that can make nerdr run
  commands you did not ask for
- nerdr leaking tokens, credentials or other secrets, to the screen, logs,
  agents or other repositories
- nerdr reading or writing outside the worktrees and settings it manages

What an agent does with the permissions you give it is up to the agent and
its settings, and is not a nerdr vulnerability.

## Host Git and sandboxed work

nerdr retains sandbox history for repositories after a worktree or tab is
removed, and records that history before importing sandboxed work. A failed
agent start does not clear it. Older sessions with surviving worktree history
are migrated conservatively by marking all known repositories. Older versions
could erase that history when a worktree disappeared; if none remains in the
saved session, migration cannot detect or recover that earlier sandbox use.

In repositories with sandbox history, nerdr refuses host Git operations
when executable filter, textconv, external diff or merge drivers are
configured. Even protected Git configuration can name a script that an
agent can edit. LFS filter commands are instead disabled with conversion
required: globally installed LFS does not block non-LFS repositories, but
operations requiring LFS conversion fail, and automatic LFS uploads are
refused. Run LFS inside the sandbox, or inspect the work and use your own
terminal. nerdr does not silently commit unfiltered content.
Host Git transports are disabled in repositories with sandbox history,
including SSH, HTTPS, local remotes and custom helpers. Executable
authentication/signing delegates and environment overrides are blocked
or suppressed. Required signing fails rather than silently producing an
unsigned commit. Fetch and push must run inside the sandbox or in the
user's terminal. Global GPG/SSH signing program settings and credential
helpers do not themselves block ordinary local Git. Other executable
delegates, including `core.gitProxy`, `core.alternateRefsCommand`,
`remote.*.uploadpack`/`receivepack`/`vcs`, `uploadpack.packObjectsHook` and
`gc.recentObjectsHook`, still cause whole-repository host Git refusal.
The private verified-object copy uses an isolated,
local-only transfer with executable upload hooks disabled.
Agent-side `why`, `order`, `walkthrough` and `spinoff` commands discover
the caller's repository in their own process, within the caller's sandbox.
This does not enable unregistered worktree discovery in the host core,
client or holder.
The sandbox must still protect Git configuration and its included files.
Branch-dependent `includeIf.onbranch` configuration is also refused: an
agent can change HEAD between the filter check and Git's own config read.

Source analysis reads only regular files within the worktree, with limits
of 512 KiB per source file, 10,000 files, 100,000 visited directory entries,
32 MiB of indexed source, 2,000,000 indexed references and 250,000
distinct identifiers. When a limit or unreadable source makes navigation
incomplete, the review panel says so. Worktree content shown
by the Git reader is limited to 16 MiB per regular file.
Index snapshots accept only regular files of at most 128 MiB; nonblocking
opens reject named pipes, and reads remain bounded if the file grows.

## What happens next

nerdr is maintained by a few people in their spare time, so there is no
fixed response time. We will answer as soon as we can, work out a fix with
you, and publish it with a
[GitHub security advisory](https://github.com/nerdr-dev/nerdr/security/advisories).
