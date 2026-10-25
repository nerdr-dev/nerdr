---
name: nerdr-tools
description: See and use the rest of nerdr from inside a nerdr tab — a line saying where your work stands, shown to the user (nerdr status), every tab's branches, pull requests, CI and agents (nerdr tabs), a command run in a terminal pane the user sees, restarted or closed (nerdr terminal, nerdr close), a new tab on a new branch with a task, made without asking the user (nerdr tab new), past agent conversations (nerdr search) and the Claude and Codex plans' rate limits (nerdr usage). Inside nerdr (NERDR=1), use this when your work changes phase — a cause found, a fix made, tests running, waiting on something — to keep the status line current, when the user asks how the other tabs or agents are doing ("which tabs have failing CI?"), to start, restart or close a dev server, watcher or long run they should see ("start the dev server", "restart the server", "close the terminal", "run the tests in a terminal"), to make new tabs only when the user says outright that they do not want to be asked about each ("make the tabs, don't ask me") — any other request for new tabs, branches or worktrees is for the nerdr-spinoff skill, where the user confirms each branch and agent — to find what was said or decided in an earlier conversation ("what did we decide about the cache last week?"), and before starting more agents or long work when the plans' limits matter ("how much quota is left?").
---

<!-- nerdr: installed and kept up to date by nerdr. Remove this line to keep your own edits. -->

# The rest of nerdr, from inside a tab

You are in a tab of nerdr, which runs coding agents in tabs, each tab a git
worktree and branch. These commands let you see the other tabs and use
nerdr's panes. Run them as `"${NERDR_BIN:-nerdr}" …`. If one fails, show
the user its message and stop there: do not look for, build or run
another nerdr.

If `NERDR_SANDBOXED=1` is set, you run in a sandbox and nerdr keeps you
in it: `nerdr terminal` is refused (run the command in your own shell,
or ask the user), `nerdr tab new` takes only agents that are also in a
sandbox, and `nerdr tabs` shows only your own tab.

## Where your work stands: `nerdr status`

A line the user sees above your pane, and other agents in `nerdr tabs`,
saying where the tab's work stands: what just happened and what comes
next, in one sentence. Set it when the work changes phase — a cause
found or ruled out, a fix made, tests running, a review done, waiting on
the user or on something outside — not after every step:

```sh
"${NERDR_BIN:-nerdr}" status "reproduced the timeout in auth.go; trying a longer retry"
```

`nerdr status` alone prints the line as it is; read it first when you
take over a tab, and keep what still holds. `nerdr status --clear` takes
it away. It is no place for questions: ask those in the conversation.

## Every tab: `nerdr tabs`

Prints every tab as JSON: its name and folder, `yours` for your own, its
branches (commits `ahead` and `behind` main, lines and files changed,
`pushed`, `pr`, `pr_state`, `ci`), its `status` line, its agents with their `state`
(starting, working, blocked, idle, done, exited) and its terminals. Use it
to answer how the work stands across tabs, or to follow a tab you made.
`ci` is `pass`, `fail`, `pending` or empty when there is none.

## A terminal the user sees: `nerdr terminal COMMAND…`

Runs the command in a new terminal pane of your tab, in the worktree,
beside you, where the user sees it and can use it. Use it for what should
keep running or be watched — a dev server, a file watcher, a long test or
build — instead of running it out of sight in your own shell. nerdr
answers with the pane's name; read what it shows with
`nerdr read terminal` (the newest) or `nerdr read terminal 2`:

```sh
"${NERDR_BIN:-nerdr}" terminal npm run dev
"${NERDR_BIN:-nerdr}" read --lines 40 terminal
```

The pane is a shell that stays when the command ends. Do not use it for
commands you only want the output of; run those yourself.

To restart what runs in it — a server after a change — use `--restart`:
nerdr stops it (ctrl+c, then harder) and runs the same command again in
the same pane, or the command you give:

```sh
"${NERDR_BIN:-nerdr}" terminal --restart terminal 2
"${NERDR_BIN:-nerdr}" terminal --restart terminal npm run dev -- --port 3001
```

## Closing a pane: `nerdr close`

`nerdr close terminal [K]` closes a terminal and stops what runs in it;
`nerdr close N` closes agent N's pane (it keeps its conversation). Not
your own pane, nor the tab's first agent's.

Close a pane only when the user asks you to. When you think one is no
longer needed — a server you are done with, an agent that has finished —
and the user has not said, ask them whether to close it, and close it if
they say yes.

## A new tab: `nerdr tab new`

Only when the user says outright that they want new tabs made without
being asked about each ("make the tabs, don't ask me"), or for tabs you
start as part of work they gave you to run that way. Every other request
for new tabs, branches or worktrees — "split this into tabs", "do these
in separate branches", "a worktree for each task" — goes to the
nerdr-spinoff skill, where the user names each branch and picks its
agent. When in doubt, use nerdr-spinoff: a dialog too many costs a click,
tabs the user did not confirm cost more.

```sh
"${NERDR_BIN:-nerdr}" tab new --branch fix/login-timeout --kind codex - <<'TASK'
…
TASK
```

nerdr makes the branch from main in your tab's repositories (or
`--repo NAME`), in a worktree of its own, and starts the agent (`--kind`,
`--model` as for `nerdr agent`) with the task as its first message. It
answers at once and tells you in a message when the tab is there, or why
it is not. The new agent has none of your conversation and cannot message
you: write the task in full, and follow the tab with `nerdr tabs`. The
user reviews and merges what it does.

## Past conversations: `nerdr search WORDS…`

Finds the claude and codex conversations had in this nerdr's repositories
and worktrees with all of the words said in them, by the user or the
agent, the latest first, each with its id and the part that matched.
`nerdr search --show ID` prints what was said in one. Use it when the user
refers to earlier work ("as we decided last week") or you need a decision
or a finding from a conversation you were not in. Say which conversation
you took it from.

## Rate limits: `nerdr usage`

Prints, as JSON, how much of the Claude and Codex plans' 5-hour and
weekly limits is used, in percent, and when each resets. Look before
starting several agents or long work: when a limit is nearly used, tell
the user, and prefer an agent whose plan has room, or a smaller model.
