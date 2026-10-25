# nerdr

[website](https://nerdr-dev.github.io/nerdr/) ·
[install](https://nerdr-dev.github.io/nerdr/start/install/) ·
[quick start](https://nerdr-dev.github.io/nerdr/start/quick-start/) ·
[docs](https://nerdr-dev.github.io/nerdr/start/about/)

[![CI](https://github.com/nerdr-dev/nerdr/actions/workflows/ci.yml/badge.svg)](https://github.com/nerdr-dev/nerdr/actions/workflows/ci.yml)
[![License: Apache 2.0](https://img.shields.io/badge/license-Apache_2.0-blue.svg)](LICENSE)

**Run a team of coding agents, each in its own Git worktree, and review
their work, all in one terminal.**

![nerdr with three repositories: a new tab with Codex, switching repositories, the changes in F1, tests in a terminal, Codex reviewing Claude's work, and the waiting list taking you to an agent's question](docs/public/overview.gif)

*A new tab with Codex, the repositories and their status lights, Claude's
changes in F1, the tests in a terminal, Codex reviewing Claude's work, and
the waiting list taking you to the agent with a question.*

- **One task, one tab.** Each tab gets a branch and a worktree from the
  latest `main`, and the agent you pick. Start from an issue, a pull
  request or another tab's branch. [Tabs and worktrees →](https://nerdr-dev.github.io/nerdr/use/tabs/)
- **See who needs you.** Status lights show which agents work, wait for
  input or are done; the waiting list takes you to them. [Progress →](https://nerdr-dev.github.io/nerdr/use/progress/)
- **A workbench per tab.** Terminals, more agents and code reviews beside
  the agent, in the same worktree. Agents message each other and share
  out the work. [The workbench →](https://nerdr-dev.github.io/nerdr/use/workbench/)
- **Review before you merge.** Word-level diffs, the agent's notes on why,
  guided walkthroughs, and a second agent that reviews the work and sends
  findings back. [Code review →](https://nerdr-dev.github.io/nerdr/use/code-review/)
- **From task to merge.** Commit, push, open pull requests, hand review
  comments and failing CI to the agent, and close the tab when it is
  merged. [GitHub →](https://nerdr-dev.github.io/nerdr/panel/github/)
- **Your agents, as they are.** Claude Code, Codex, Copilot, opencode,
  Cursor, Gemini and Antigravity, with their own settings, in a sandbox
  if you like. [Agents →](https://nerdr-dev.github.io/nerdr/configure/agents/)
- **Your scripts too.** Plugins that agents run when they fit the task,
  with a screen of their own. [Plugins →](https://nerdr-dev.github.io/nerdr/configure/plugins/)
- **Keyboard and mouse.** Everything is a key after `ctrl+q`, and a click.
  [Keys →](https://nerdr-dev.github.io/nerdr/configure/keyboard/)

## Install

You need `git`, Go 1.27 or newer, and at least one coding agent on your
PATH. Sign in to `gh` for pull requests, CI and issues.

```sh
git clone https://github.com/nerdr-dev/nerdr && cd nerdr
make install          # installs to $(go env GOPATH)/bin
```

Start it in the folder that holds your repositories:

```sh
cd ~/develop
nerdr
```

Pick a repository, name a branch, choose an agent, and it starts working.
Press `ctrl+q ?` to find any command. See the
[quick start](https://nerdr-dev.github.io/nerdr/start/quick-start/).

## Docs

Everything lives at [nerdr-dev.github.io/nerdr](https://nerdr-dev.github.io/nerdr/):

[about](https://nerdr-dev.github.io/nerdr/start/about/) ·
[concepts](https://nerdr-dev.github.io/nerdr/start/concepts/) ·
[tabs and worktrees](https://nerdr-dev.github.io/nerdr/use/tabs/) ·
[the workbench](https://nerdr-dev.github.io/nerdr/use/workbench/) ·
[agents together](https://nerdr-dev.github.io/nerdr/use/agents-together/) ·
[code review](https://nerdr-dev.github.io/nerdr/use/code-review/) ·
[review panel](https://nerdr-dev.github.io/nerdr/panel/overview/) ·
[configuration](https://nerdr-dev.github.io/nerdr/configure/configuration/) ·
[keyboard](https://nerdr-dev.github.io/nerdr/configure/keyboard/) ·
[plugins](https://nerdr-dev.github.io/nerdr/configure/plugins/) ·
[remote machines](https://nerdr-dev.github.io/nerdr/use/remote/) ·
[security](https://nerdr-dev.github.io/nerdr/reference/security/)

## Inspired by herdr

nerdr builds on [herdr](https://herdr.dev)'s status lights, mouse controls
and rules for detecting an agent's state; see [NOTICE](NOTICE). Run nerdr
inside herdr to keep agents running when you detach, or on another
machine.

## Development

```sh
make build    # ./nerdr
make test     # unit tests
make e2e      # the real binary in a pseudo-terminal with a fake agent
```

The docs site is in [`docs/`](docs); see
[Development](https://nerdr-dev.github.io/nerdr/reference/development/).

## License

[Apache License 2.0](LICENSE)
