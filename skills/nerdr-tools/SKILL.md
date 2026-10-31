---
name: nerdr-tools
description: Use nerdr from inside a nerdr tab (NERDR=1). Keep the status line above your pane current when your work changes phase — a cause found, a fix made, tests running, waiting on something (nerdr status). See how the other tabs and agents are doing — branches, pull requests, CI ("which tabs have failing CI?"). Start, restart or close a dev server, watcher or long run in a terminal pane the user sees ("start the dev server", "restart the server", "close the terminal", "run the tests in a terminal"). Find what was said or decided in an earlier conversation ("what did we decide about the cache last week?"). Check the Claude and Codex plans' rate limits before starting more agents or long work ("how much quota is left?"). Make new tabs only when the user says not to be asked about each ("make the tabs, don't ask me"); any other request for new tabs, branches or worktrees is for nerdr-spinoff.
---

<!-- nerdr: installed and kept up to date by nerdr. Remove this line to keep your own edits. -->

# The rest of nerdr, from inside a tab

You are in a tab of nerdr, which runs coding agents in tabs, each tab a git
worktree and branch. Run these commands as `"${NERDR_BIN:-nerdr}" …`; each
takes `--help`, which says what it prints and the flags it takes. If one
fails, show the user its message and stop there: do not look for, build or
run another nerdr.

With `NERDR_SANDBOXED=1` you run in a sandbox: `nerdr terminal` is refused
(run the command in your own shell, or ask the user), `nerdr tab new` takes
only sandboxed agents, and `nerdr tabs` shows only your own tab.

## Where your work stands: `nerdr status`

```sh
"${NERDR_BIN:-nerdr}" status "reproduced the timeout in auth.go; trying a longer retry"
```

One sentence the user sees above your pane, and other agents in
`nerdr tabs`: what just happened and what comes next. Set it when the work
changes phase — a cause found or ruled out, a fix made, tests running, a
review done, waiting on the user or on something outside — not after every
step. When you take over a tab, read the line first (`nerdr status` alone)
and keep what still holds. It is no place for questions: ask those in the
conversation.

## Every tab: `nerdr tabs`

Every tab as JSON — branches, pull requests, CI, status lines, agents and
their state, terminals. Use it to answer how the work stands across tabs,
or to follow a tab you made.

## A terminal the user sees: `nerdr terminal COMMAND…`

```sh
"${NERDR_BIN:-nerdr}" terminal npm run dev
"${NERDR_BIN:-nerdr}" read --lines 40 terminal      # what the newest shows
"${NERDR_BIN:-nerdr}" terminal --restart terminal 2 # stop it, run it again
```

For what should keep running or be watched — a dev server, a file watcher,
a long test or build — instead of running it out of sight in your own
shell. Not for commands you only want the output of; run those yourself.

## Closing a pane: `nerdr close`

`nerdr close terminal [K]` closes a terminal and stops what runs in it;
`nerdr close N` closes agent N's pane. Only when the user asks you to: when
you think one is no longer needed, ask them first.

## A new tab without asking: `nerdr tab new`

Only when the user says outright that they want new tabs made without
being asked about each ("make the tabs, don't ask me"), or for tabs you
start as part of work they gave you to run that way. Every other request
for new tabs, branches or worktrees — "split this into tabs", "do these in
separate branches" — goes to the nerdr-spinoff skill, where the user names
each branch and picks its agent. When in doubt, use nerdr-spinoff.

```sh
"${NERDR_BIN:-nerdr}" tab new --branch fix/login-timeout --kind codex - <<'TASK'
…
TASK
```

The new agent has none of your conversation and cannot message you: write
the task in full, and follow the tab with `nerdr tabs`.

## Past conversations: `nerdr search WORDS…`

Finds earlier claude and codex conversations in this nerdr's repositories
with all of the words; `nerdr search --show ID` prints one. Use it when
the user refers to earlier work ("as we decided last week") or you need a
decision from a conversation you were not in, and say which conversation
you took it from.

## Rate limits: `nerdr usage`

How much of the Claude and Codex plans' 5-hour and weekly limits is used,
and when each resets. Look before starting several agents or long work:
when a limit is nearly used, tell the user, and prefer an agent whose plan
has room, or a smaller model.
