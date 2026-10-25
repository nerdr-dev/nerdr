# Security policy

## Reporting a vulnerability

Please report security problems privately with
[Report a vulnerability](https://github.com/nerdr-dev/nerdr/security/advisories/new)
on the repository's Security tab. Do not open an issue, a pull request or a
discussion for them, since those are public.

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

## What happens next

nerdr is maintained by a few people in their spare time, so there is no
fixed response time. We will answer as soon as we can, work out a fix with
you, and publish it with a
[GitHub security advisory](https://github.com/nerdr-dev/nerdr/security/advisories).
